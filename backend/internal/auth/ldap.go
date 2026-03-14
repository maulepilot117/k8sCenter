package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

const (
	ldapDialTimeout      = 5 * time.Second
	ldapOperationTimeout = 10 * time.Second
)

// usernameAllowlist restricts LDAP usernames to safe characters as a defense-in-depth
// measure against LDAP injection (in addition to ldap.EscapeFilter).
var usernameAllowlist = regexp.MustCompile(`^[a-zA-Z0-9._@-]+$`)

// LDAPProvider authenticates users against an LDAP directory using bind+search.
type LDAPProvider struct {
	config LDAPProviderConfig
	logger *slog.Logger
}

// LDAPProviderConfig holds the configuration for a single LDAP provider.
type LDAPProviderConfig struct {
	ID              string
	DisplayName     string
	URL             string   // ldaps://host:636 or ldap://host:389
	BindDN          string   // service account DN
	BindPassword    string   // service account password (from env/k8s Secret)
	StartTLS        bool     // upgrade plaintext connection to TLS
	TLSInsecure     bool     // skip TLS cert verification
	CACertPath      string   // custom CA cert
	UserBaseDN      string   // e.g., "DC=corp,DC=com"
	UserFilter      string   // e.g., "(sAMAccountName={0})" — {0} replaced with escaped username
	UserAttributes  []string // attributes to fetch (e.g., ["sAMAccountName", "mail", "memberOf"])
	GroupBaseDN     string   // where to search for groups
	GroupFilter     string   // e.g., "(member={0})" — {0} replaced with user DN
	GroupNameAttr   string   // default: "cn"
	UsernameMapAttr string   // attribute that maps to KubernetesUsername
	GroupsPrefix    string   // prepended to k8s group names
}

// NewLDAPProvider creates a new LDAP provider with the given configuration.
func NewLDAPProvider(config LDAPProviderConfig, logger *slog.Logger) *LDAPProvider {
	if config.GroupNameAttr == "" {
		config.GroupNameAttr = "cn"
	}
	if config.UsernameMapAttr == "" {
		config.UsernameMapAttr = "uid"
	}
	if len(config.UserAttributes) == 0 {
		config.UserAttributes = []string{"dn", "uid", "mail", "cn", "sAMAccountName", "memberOf"}
	}
	return &LDAPProvider{
		config: config,
		logger: logger.With("ldap_provider", config.ID),
	}
}

func (p *LDAPProvider) Type() string { return "ldap" }

// Authenticate performs LDAP bind+search authentication.
// 1. Validates username against allowlist
// 2. Connects to LDAP server
// 3. Binds as service account
// 4. Searches for user DN
// 5. Binds as user to verify password
// 6. Fetches group membership
// 7. Maps attributes to auth.User
func (p *LDAPProvider) Authenticate(ctx context.Context, creds Credentials) (*User, error) {
	// Defense-in-depth: validate username before any LDAP interaction
	if !usernameAllowlist.MatchString(creds.Username) {
		return nil, fmt.Errorf("invalid credentials")
	}

	conn, err := p.connect()
	if err != nil {
		p.logger.Error("LDAP connection failed", "error", err)
		return nil, fmt.Errorf("invalid credentials")
	}
	defer conn.Close()

	// Step 1: Bind as service account
	if err := conn.Bind(p.config.BindDN, p.config.BindPassword); err != nil {
		p.logger.Error("LDAP service account bind failed", "error", err)
		return nil, fmt.Errorf("invalid credentials")
	}

	// Step 2: Search for the user
	escapedUsername := ldap.EscapeFilter(creds.Username)
	filter := strings.ReplaceAll(p.config.UserFilter, "{0}", escapedUsername)

	searchReq := ldap.NewSearchRequest(
		p.config.UserBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		2,    // SizeLimit: expect exactly 1 result, 2 to detect ambiguity
		int(ldapOperationTimeout.Seconds()),
		false,
		filter,
		p.config.UserAttributes,
		nil,
	)

	result, err := conn.Search(searchReq)
	if err != nil {
		p.logger.Error("LDAP user search failed", "error", err)
		return nil, fmt.Errorf("invalid credentials")
	}

	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("invalid credentials")
	}
	if len(result.Entries) > 1 {
		p.logger.Warn("LDAP search returned multiple entries", "username", creds.Username, "count", len(result.Entries))
		return nil, fmt.Errorf("invalid credentials")
	}

	userEntry := result.Entries[0]
	userDN := userEntry.DN

	// Step 3: Bind as the user to verify their password
	if err := conn.Bind(userDN, creds.Password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, fmt.Errorf("invalid credentials")
		}
		p.logger.Error("LDAP user bind failed", "error", err)
		return nil, fmt.Errorf("invalid credentials")
	}

	// Step 4: Rebind as service account to fetch groups
	if err := conn.Bind(p.config.BindDN, p.config.BindPassword); err != nil {
		p.logger.Error("LDAP service account rebind failed", "error", err)
		return nil, fmt.Errorf("invalid credentials")
	}

	// Step 5: Get group membership
	groups := p.getGroups(conn, userEntry, userDN)

	// Step 6: Map to auth.User
	return p.mapToUser(userEntry, groups), nil
}

// TestConnection verifies LDAP connectivity and service account bind.
func (p *LDAPProvider) TestConnection(_ context.Context) error {
	conn, err := p.connect()
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(p.config.BindDN, p.config.BindPassword); err != nil {
		return fmt.Errorf("service account bind failed: %w", err)
	}

	return nil
}

// connect establishes a connection to the LDAP server with optional TLS.
func (p *LDAPProvider) connect() (*ldap.Conn, error) {
	tlsConfig, err := p.buildTLSConfig()
	if err != nil {
		return nil, err
	}

	conn, err := ldap.DialURL(p.config.URL, ldap.DialWithTLSConfig(tlsConfig))
	if err != nil {
		return nil, fmt.Errorf("LDAP dial failed: %w", err)
	}

	conn.SetTimeout(ldapOperationTimeout)

	// Upgrade to TLS if using StartTLS on a plaintext connection
	if p.config.StartTLS && strings.HasPrefix(p.config.URL, "ldap://") {
		if err := conn.StartTLS(tlsConfig); err != nil {
			conn.Close()
			return nil, fmt.Errorf("StartTLS failed: %w", err)
		}
	}

	return conn, nil
}

// buildTLSConfig creates a TLS configuration for the LDAP connection.
func (p *LDAPProvider) buildTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: p.config.TLSInsecure,
	}

	if p.config.CACertPath != "" {
		caCert, err := os.ReadFile(p.config.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert from %s", p.config.CACertPath)
		}
		tlsConfig.RootCAs = pool
	}

	return tlsConfig, nil
}

// getGroups retrieves group membership for a user.
// First tries memberOf attribute on the user entry, then falls back to group search.
func (p *LDAPProvider) getGroups(conn *ldap.Conn, userEntry *ldap.Entry, userDN string) []string {
	// Try memberOf attribute first (common in Active Directory)
	memberOf := userEntry.GetAttributeValues("memberOf")
	if len(memberOf) > 0 {
		groups := make([]string, 0, len(memberOf))
		for _, groupDN := range memberOf {
			// Extract CN from the group DN
			cn := extractCNFromDN(groupDN)
			if cn != "" {
				groups = append(groups, cn)
			}
		}
		if len(groups) > 0 {
			return groups
		}
	}

	// Fall back to group search if configured
	if p.config.GroupBaseDN == "" || p.config.GroupFilter == "" {
		return nil
	}

	filter := strings.ReplaceAll(p.config.GroupFilter, "{0}", ldap.EscapeFilter(userDN))
	searchReq := ldap.NewSearchRequest(
		p.config.GroupBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		100, // SizeLimit
		int(ldapOperationTimeout.Seconds()),
		false,
		filter,
		[]string{p.config.GroupNameAttr},
		nil,
	)

	result, err := conn.Search(searchReq)
	if err != nil {
		p.logger.Warn("LDAP group search failed", "error", err)
		return nil
	}

	groups := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		name := entry.GetAttributeValue(p.config.GroupNameAttr)
		if name != "" {
			groups = append(groups, name)
		}
	}
	return groups
}

// mapToUser converts an LDAP entry and groups to an auth.User.
func (p *LDAPProvider) mapToUser(entry *ldap.Entry, groups []string) *User {
	// Determine Kubernetes username
	k8sUsername := entry.GetAttributeValue(p.config.UsernameMapAttr)
	if k8sUsername == "" {
		// Fallback chain: uid → sAMAccountName → mail → DN
		for _, attr := range []string{"uid", "sAMAccountName", "mail"} {
			k8sUsername = entry.GetAttributeValue(attr)
			if k8sUsername != "" {
				break
			}
		}
		if k8sUsername == "" {
			k8sUsername = entry.DN
		}
	}

	// Apply groups prefix
	k8sGroups := make([]string, 0, len(groups)+1)
	k8sGroups = append(k8sGroups, "k8scenter:users")
	for _, g := range groups {
		k8sGroups = append(k8sGroups, p.config.GroupsPrefix+g)
	}

	// Display name
	displayName := entry.GetAttributeValue("cn")
	if displayName == "" {
		displayName = k8sUsername
	}

	return &User{
		ID:                 fmt.Sprintf("ldap:%s:%s", p.config.ID, entry.DN),
		Username:           displayName,
		Provider:           "ldap",
		KubernetesUsername: k8sUsername,
		KubernetesGroups:   k8sGroups,
		Roles:              []string{"user"},
	}
}

// extractCNFromDN extracts the CN value from a Distinguished Name.
// e.g., "CN=Developers,OU=Groups,DC=corp,DC=com" → "Developers"
func extractCNFromDN(dn string) string {
	parts := strings.SplitN(dn, ",", 2)
	if len(parts) == 0 {
		return ""
	}
	first := parts[0]
	if strings.HasPrefix(strings.ToUpper(first), "CN=") {
		return first[3:]
	}
	return ""
}
