package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/kubecenter/kubecenter/internal/config"
)

// ErrLDAPTransient wraps connection / timeout / unexpected server failures
// during LDAP revalidation. Callers can branch on it to apply a bounded
// grace window (reuse last-known identity) rather than logging every LDAP
// user out the moment the directory has a brief hiccup. Definitive
// rejections — user not found, account disabled, bad service-account
// credentials — are NOT wrapped in this sentinel; they return
// [ErrInvalidCredentials] and the caller must fail closed.
var ErrLDAPTransient = errors.New("ldap transient failure")

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

// LDAPProviderConfig is an alias for config.LDAPConfig.
// Kept as a type alias for backward compatibility with existing code
// that references auth.LDAPProviderConfig.
type LDAPProviderConfig = config.LDAPConfig

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
	p := &LDAPProvider{
		config: config,
		logger: logger.With("ldap_provider", config.ID),
	}
	// P3-1 (2026-05-22 audit): when the operator explicitly opted in
	// via auth.ldap[].insecureplaintext, the config validator already
	// allowed boot but the runtime should remind on every restart that
	// service-account + user-bind credentials are leaving the host in
	// the clear. Without the opt-in, config.validate() would have
	// returned an error before reaching this constructor.
	if strings.HasPrefix(config.URL, "ldap://") && !config.StartTLS {
		p.logger.Warn("LDAP connection is plaintext — credentials will be transmitted unencrypted. Operator opted in via insecureplaintext=true; switch to ldaps:// or enable StartTLS for production.")
	}
	return p
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
		return nil, ErrInvalidCredentials
	}

	conn, err := p.connect()
	if err != nil {
		p.logger.Error("LDAP connection failed", "error", err)
		return nil, ErrInvalidCredentials
	}
	defer conn.Close()

	// Step 1: Bind as service account
	if err := conn.Bind(p.config.BindDN, p.config.BindPassword); err != nil {
		p.logger.Error("LDAP service account bind failed", "error", err)
		return nil, ErrInvalidCredentials
	}

	// Step 2: Search for the user
	escapedUsername := ldap.EscapeFilter(creds.Username)
	filter := strings.ReplaceAll(p.config.UserFilter, "{0}", escapedUsername)

	searchReq := ldap.NewSearchRequest(
		p.config.UserBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		2, // SizeLimit: expect exactly 1 result, 2 to detect ambiguity
		int(ldapOperationTimeout.Seconds()),
		false,
		filter,
		p.config.UserAttributes,
		nil,
	)

	result, err := conn.Search(searchReq)
	if err != nil {
		p.logger.Error("LDAP user search failed", "error", err)
		return nil, ErrInvalidCredentials
	}

	if len(result.Entries) == 0 {
		return nil, ErrInvalidCredentials
	}
	if len(result.Entries) > 1 {
		p.logger.Warn("LDAP search returned multiple entries", "username", creds.Username, "count", len(result.Entries))
		return nil, ErrInvalidCredentials
	}

	userEntry := result.Entries[0]
	userDN := userEntry.DN

	// Step 3: Bind as the user to verify their password
	if err := conn.Bind(userDN, creds.Password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, ErrInvalidCredentials
		}
		p.logger.Error("LDAP user bind failed", "error", err)
		return nil, ErrInvalidCredentials
	}

	// Step 4: Rebind as service account to fetch groups
	if err := conn.Bind(p.config.BindDN, p.config.BindPassword); err != nil {
		p.logger.Error("LDAP service account rebind failed", "error", err)
		return nil, ErrInvalidCredentials
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

	conn, err := ldap.DialURL(p.config.URL,
		ldap.DialWithTLSConfig(tlsConfig),
		ldap.DialWithDialer(&net.Dialer{Timeout: ldapDialTimeout}),
	)
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

// Revalidate re-authorizes a previously authenticated LDAP user without
// re-prompting for their password. It binds as the service account,
// searches for the user by DN, and returns a freshly mapped [*User] with
// the directory's current group membership. The refresh handler calls
// this on every refresh so that revoked identity (account disabled,
// group removed) propagates within the LDAP refresh-token cap rather
// than waiting for the cached session to expire — closes audit finding
// P2-3 (2026-05-22).
//
// Error contract:
//   - Returns [ErrInvalidCredentials] when the user is no longer present
//     (search returned zero entries), the DN is malformed, or the entry
//     set is ambiguous. The caller MUST fail closed on these — the user
//     is genuinely gone from the directory.
//   - Returns an error wrapping [ErrLDAPTransient] for connection,
//     timeout, and unexpected-server errors. The caller MAY fall back to
//     last-known identity within a bounded grace window to avoid
//     evicting every active LDAP user during a brief directory outage.
//
// Inputs:
//   - ctx: only checked once, after connect() and before Bind. The
//     underlying ldap.Conn operations are bounded by
//     [ldapOperationTimeout] (10s wall-clock deadline set via SetTimeout)
//     but do NOT propagate ctx cancellation mid-call — a context canceled
//     while Bind / Search is in flight is observed only after the LDAP
//     op returns or the 10s deadline expires. Filed as a Phase 3
//     follow-up; the current bound is acceptable for refresh latency.
//   - userDN: the user's distinguished name as captured at login time
//     (the second-colon-separated segment of the session UserID).
func (p *LDAPProvider) Revalidate(ctx context.Context, userDN string) (*User, error) {
	if userDN == "" {
		return nil, ErrInvalidCredentials
	}

	conn, err := p.connect()
	if err != nil {
		return nil, fmt.Errorf("%w: connect: %w", ErrLDAPTransient, err)
	}
	defer conn.Close()

	// honor caller cancellation if it's already expired before any IO.
	if cerr := ctx.Err(); cerr != nil {
		return nil, fmt.Errorf("%w: %w", ErrLDAPTransient, cerr)
	}

	if berr := conn.Bind(p.config.BindDN, p.config.BindPassword); berr != nil {
		// Service-account credentials breaking is a configuration problem,
		// not a per-user signal. Classify as transient so an in-flight
		// admin rotation doesn't take down every LDAP session at once;
		// the operator still sees the failure in logs and the audit table.
		return nil, fmt.Errorf("%w: service bind: %w", ErrLDAPTransient, berr)
	}

	searchReq := ldap.NewSearchRequest(
		userDN,
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		2, // SizeLimit: expect exactly 1 entry; 2 surfaces ambiguity.
		int(ldapOperationTimeout.Seconds()),
		false,
		"(objectClass=*)",
		p.config.UserAttributes,
		nil,
	)

	result, err := conn.Search(searchReq)
	if err != nil {
		return nil, classifyLDAPError(err)
	}

	switch len(result.Entries) {
	case 0:
		// Entry no longer exists — definitive rejection.
		return nil, ErrInvalidCredentials
	case 1:
		// happy path; fall through.
	default:
		// Ambiguous identity — refuse to choose. Logged for the operator.
		p.logger.Warn("LDAP revalidation found multiple entries", "dn", userDN, "count", len(result.Entries))
		return nil, ErrInvalidCredentials
	}

	entry := result.Entries[0]
	groups := p.getGroups(conn, entry, userDN)
	return p.mapToUser(entry, groups), nil
}

// classifyLDAPError translates a go-ldap error into the public sentinel
// the refresh handler branches on. NoSuchObject is the only LDAP result
// code that means "user is gone from the directory"; everything else is
// a connectivity or server-side hiccup that the caller should treat as
// transient. This helper is unit-tested in isolation because the rest of
// the LDAP code path requires a live directory.
func classifyLDAPError(err error) error {
	if err == nil {
		return nil
	}
	if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		return ErrInvalidCredentials
	}
	return fmt.Errorf("%w: %w", ErrLDAPTransient, err)
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
