// Package rubrik integrates Rubrik Security Cloud (RSC) as a backup
// provider. This file is the authenticated GraphQL transport: it exchanges
// the stored service-account credentials for an access token, caches the
// token, and sends GraphQL operations to the tenant.
package rubrik

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

// Error classes. Every error the client returns matches exactly one of them
// with errors.Is, so callers can pick a fixed message per class. No error
// carries the client secret, the access token or response body text; the
// only server text kept is a GraphQL error's messages (see GraphQLError).
// An error may name an address or status code, so callers log it and answer
// clients with a fixed message.
var (
	// ErrNotConfigured means no RSC connection is stored. A ConnectionSource
	// returns it (wrapped is fine) when there is no connection row.
	ErrNotConfigured = errors.New("rubrik: no connection configured")
	// ErrConnectionUnavailable means the stored connection could not be read
	// (a database or decryption failure). It says nothing about the tenant.
	ErrConnectionUnavailable = errors.New("rubrik: stored connection unavailable")
	// ErrInvalidRequest means the operation could not be encoded as JSON, a
	// programming error in the caller. No request is sent.
	ErrInvalidRequest = errors.New("rubrik: invalid request")
	// ErrInvalidTenantURL means the tenant URL is not an https URL, or it
	// resolves to a loopback, private, link-local, CGNAT or unspecified
	// address. No request is sent.
	ErrInvalidTenantURL = errors.New("rubrik: invalid tenant url")
	// ErrUnreachable is a transport failure, a timeout, a failed DNS lookup
	// of the tenant host, a redirect (never followed) or an unexpected status.
	ErrUnreachable = errors.New("rubrik: tenant unreachable")
	// ErrAuthFailed means the tenant refused the credentials, or refused a
	// fresh token again after one re-authentication.
	ErrAuthFailed = errors.New("rubrik: authentication failed")
	// ErrRateLimited is a 429 answer. The error is a *RateLimitError that
	// carries the Retry-After delay.
	ErrRateLimited = errors.New("rubrik: rate limited")
	// ErrGraphQL is an answer whose errors array is not empty. The error is
	// a *GraphQLError that carries the messages.
	ErrGraphQL = errors.New("rubrik: graphql error")
	// ErrUnreadable is an answer over the size cap or one that does not
	// decode.
	ErrUnreadable = errors.New("rubrik: unreadable answer")
)

// RateLimitError is returned for a 429 answer. RetryAfter is the delay the
// tenant asked for, 0 when it gave none, and never more than an hour.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s: retry after %s", ErrRateLimited, e.RetryAfter)
	}
	return ErrRateLimited.Error()
}

// Is reports ErrRateLimited.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// GraphQLError is returned when a GraphQL answer carries errors. Messages
// are the server's messages, at most maxGraphQLMessages of them, each cut to
// maxGraphQLMessageLen bytes, with the access token removed.
type GraphQLError struct {
	Messages []string
}

func (e *GraphQLError) Error() string {
	if len(e.Messages) == 0 {
		return ErrGraphQL.Error()
	}
	return ErrGraphQL.Error() + ": " + strings.Join(e.Messages, "; ")
}

// Is reports ErrGraphQL.
func (e *GraphQLError) Is(target error) bool { return target == ErrGraphQL }

const (
	tokenPath   = "/api/client_token"
	graphqlPath = "/api/graphql"

	// tokenBodyLimit caps a token answer; graphqlBodyLimit a GraphQL answer.
	// A larger answer is unreadable, never a partial result.
	tokenBodyLimit   = 64 << 10
	graphqlBodyLimit = 16 << 20

	// requestTimeout bounds one HTTP exchange; probeTimeout a whole probe.
	requestTimeout = 30 * time.Second
	probeTimeout   = 10 * time.Second

	// tokenRefreshMargin: a cached token is replaced this long before it
	// expires. defaultTokenLifetime applies when the answer gives none.
	tokenRefreshMargin   = 5 * time.Minute
	defaultTokenLifetime = time.Hour
	// maxTokenLifetime bounds a tenant's expires_in, so a huge value can
	// neither overflow nor keep a token cached for longer than a day.
	maxTokenLifetime = 24 * time.Hour

	maxRetryAfter        = time.Hour
	maxGraphQLMessages   = 5
	maxGraphQLMessageLen = 300

	userAgent = "kubecenter-rubrik"
)

// Connection is the stored RSC connection the client authenticates with.
// ClientSecret must never be logged or returned to a client.
type Connection struct {
	TenantURL    string
	ClientID     string
	ClientSecret string
	// Revision changes on every save of the connection and must never repeat
	// a value the connection had before, including after the connection is
	// deleted and saved again: a cached token is reused only while the
	// revision it was issued for is current.
	Revision int64
}

// ConnectionSource reads the stored connection. Both methods return an
// error matching ErrNotConfigured when there is no connection; the client
// reports any other error as ErrConnectionUnavailable. ConnectionRevision
// must be cheap (no decryption): the client calls it before every operation
// to notice a save made on another replica.
type ConnectionSource interface {
	Connection(ctx context.Context) (Connection, error)
	ConnectionRevision(ctx context.Context) (int64, error)
}

// Request is one GraphQL operation.
type Request struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// cachedToken is an access token and the connection it was issued for.
type cachedToken struct {
	value     string
	origin    string // tenant origin, "https://host[:port]"
	revision  int64
	refreshAt time.Time
	expiresAt time.Time
}

// Client sends GraphQL operations to the RSC tenant of the stored
// connection. It is safe for concurrent use. Build it with NewClient.
type Client struct {
	source   ConnectionSource
	logger   *slog.Logger
	http     *http.Client
	validate func(ctx context.Context, rawURL string) error
	now      func() time.Time
	// bodyLimit caps a GraphQL answer (graphqlBodyLimit unless a test
	// lowers it).
	bodyLimit int64

	exchanges singleflight.Group

	mu    sync.Mutex
	token *cachedToken
	// epoch advances on every Flush, so an exchange that began before a
	// Flush does not cache its token.
	epoch uint64
}

// NewClient returns a client over the stored connection. It dials through
// the strict transport (public addresses only) and checks the tenant URL
// with k8s.ValidateRemoteURLContext before every request, so a tenant URL
// that resolves to an internal address is refused even when an environment
// proxy (HTTPS_PROXY) carries the traffic. The proxy itself may sit on a
// private address; it is dialed with k8s.SafeDialContext.
func NewClient(source ConnectionSource, logger *slog.Logger) *Client {
	return newClient(source, logger, productionTransport(), k8s.ValidateRemoteURLContext)
}

// productionTransport is the transport NewClient and Probe dial through:
// the environment proxy, the strict dialer for the tenant and the safe
// dialer for the proxy.
func productionTransport() *http.Transport {
	return tenantTransport(http.ProxyFromEnvironment)
}

// tenantTransport is productionTransport with the proxy decision supplied,
// so tests can pin the production dialers behind a known proxy.
func tenantTransport(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return newTenantTransport(proxy, k8s.StrictDialContext, k8s.SafeDialContext)
}

func newClient(source ConnectionSource, logger *slog.Logger, base http.RoundTripper, validate func(context.Context, string) error) *Client {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Client{
		source:    source,
		logger:    logger.With("component", "rubrik-client"),
		http:      newHTTPClient(base),
		validate:  validate,
		bodyLimit: graphqlBodyLimit,
	}
}

// newHTTPClient never follows a redirect: a 3xx is returned as the answer,
// so neither the credentials nor the bearer can be steered to another
// origin.
func newHTTPClient(base http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: base,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// dialFunc is a net.Dialer.DialContext-compatible function.
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// newTenantTransport returns a transport that sends tenant requests through
// proxy (nil URL: direct). A connection to an address proxy named is dialed
// with viaProxy; every other connection with direct. The proxy addresses are
// recorded as proxy returns them, so the dial choice follows the same
// decision the transport made.
func newTenantTransport(proxy func(*http.Request) (*url.URL, error), direct, viaProxy dialFunc) *http.Transport {
	t := k8s.StrictHTTPTransport()
	var proxies sync.Map // canonical "host:port" -> struct{}
	t.Proxy = func(req *http.Request) (*url.URL, error) {
		u, err := proxy(req)
		if err == nil && u != nil {
			proxies.Store(canonicalProxyAddr(u), struct{}{})
		}
		return u, err
	}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if _, ok := proxies.Load(address); ok {
			return viaProxy(ctx, network, address)
		}
		return direct(ctx, network, address)
	}
	return t
}

// canonicalProxyAddr is the "host:port" net/http dials for proxy URL u.
func canonicalProxyAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// Flush drops the cached token, so the next operation exchanges the stored
// credentials again. Call it after the connection is saved or deleted. A
// save on another replica is noticed through the revision instead.
func (c *Client) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = nil
	c.epoch++
}

// Query sends req to the tenant and decodes the answer's data into out
// (which may be nil). A 401 is answered by one re-authentication and one
// retry; a second 401 returns ErrAuthFailed.
func (c *Client) Query(ctx context.Context, req Request, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	rev, err := c.source.ConnectionRevision(ctx)
	if err != nil {
		return sourceError(err)
	}
	tok, err := c.currentToken(ctx, rev)
	if err != nil {
		return err
	}
	err = c.post(ctx, tok, body, out)
	if !errors.Is(err, errUnauthorized) {
		return err
	}

	c.logger.Info("rubrik token refused; authenticating again")
	c.dropToken(tok)
	tok, err = c.currentToken(ctx, rev)
	if err != nil {
		return err
	}
	err = c.post(ctx, tok, body, out)
	if errors.Is(err, errUnauthorized) {
		c.dropToken(tok)
		return fmt.Errorf("%w: token refused after re-authentication", ErrAuthFailed)
	}
	return err
}

// errUnauthorized marks a 401 answer to a GraphQL request. It never leaves
// Query.
var errUnauthorized = errors.New("rubrik: unauthorized")

// sourceError classifies a ConnectionSource error: ErrNotConfigured passes
// through, anything else is ErrConnectionUnavailable.
func sourceError(err error) error {
	if errors.Is(err, ErrNotConfigured) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrConnectionUnavailable, err)
}

// currentToken returns a cached token issued for revision rev that is not
// due for refresh, or exchanges the credentials for a new one. Concurrent
// callers share one exchange per revision. When a refresh fails while the
// cached token for rev has not yet expired, that token is returned instead
// of the error, so a tenant hiccup inside the refresh margin does not fail
// queries the old token can still serve.
func (c *Client) currentToken(ctx context.Context, rev int64) (*cachedToken, error) {
	c.mu.Lock()
	now := c.clock()
	var stale *cachedToken
	if t := c.token; t != nil && t.revision == rev {
		if now.Before(t.refreshAt) {
			c.mu.Unlock()
			return t, nil
		}
		if now.Before(t.expiresAt) {
			stale = t
		}
	}
	epoch := c.epoch
	c.mu.Unlock()

	ch := c.exchanges.DoChan(strconv.FormatInt(rev, 10), func() (any, error) {
		return c.exchangeAndCache(ctx, epoch)
	})
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, ctx.Err())
	case r := <-ch:
		if r.Err == nil {
			return r.Val.(*cachedToken), nil
		}
		if stale != nil && c.clock().Before(stale.expiresAt) {
			c.logger.Warn("rubrik token refresh failed; using the unexpired token", "error", r.Err)
			return stale, nil
		}
		return nil, r.Err
	}
}

// exchangeAndCache reads the stored connection, exchanges its credentials
// and caches the token unless a Flush happened since epoch was read. It runs
// on the goroutine singleflight starts, outside chi's recovery, so a panic
// is recovered and returned as ErrUnreachable; singleflight would otherwise
// re-raise it where nothing can catch it. The exchange is detached from any
// one caller's cancellation, so a caller giving up does not fail the others
// waiting on it; it is still bounded by requestTimeout.
func (c *Client) exchangeAndCache(ctx context.Context, epoch uint64) (any, error) {
	var tok *cachedToken
	err := fmt.Errorf("%w: token exchange panicked", ErrUnreachable)
	recoverutil.Safe(c.logger, "rubrik token exchange", func() {
		xctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
		defer cancel()
		conn, cerr := c.source.Connection(xctx)
		if cerr != nil {
			err = sourceError(cerr)
			return
		}
		tok, err = exchange(xctx, c.http, c.validate, c.clock(), conn)
		if err != nil {
			return
		}
		c.mu.Lock()
		if c.epoch == epoch {
			c.token = tok
		}
		c.mu.Unlock()
		c.logger.Debug("rubrik token issued", "revision", tok.revision)
	})
	if err != nil {
		return nil, err
	}
	return tok, nil
}

// dropToken removes the cached token only while it is still tok, so a token
// a concurrent caller just fetched survives.
func (c *Client) dropToken(tok *cachedToken) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == tok {
		c.token = nil
	}
}

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// tenantOrigin returns the https origin of a tenant URL, or
// ErrInvalidTenantURL.
func tenantOrigin(raw string) (string, error) {
	origin := urlorigin.Of(raw)
	if !strings.HasPrefix(origin, "https://") {
		return "", fmt.Errorf("%w: tenant url must be an https url", ErrInvalidTenantURL)
	}
	return origin, nil
}

// checkTenant checks the tenant origin's addresses before a request. A
// lookup that fails (DNS error, timeout, cancellation) is ErrUnreachable,
// since it says nothing about the URL; an address the check refuses is
// ErrInvalidTenantURL.
func checkTenant(ctx context.Context, validate func(context.Context, string) error, origin string) error {
	if validate == nil {
		return nil
	}
	err := validate(ctx, origin)
	if err == nil {
		return nil
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return fmt.Errorf("%w: %w", ErrInvalidTenantURL, err)
}

// tokenAnswer is the part of a client_token answer the client reads.
type tokenAnswer struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// exchange posts the connection's credentials, form-encoded, to the tenant's
// client_token endpoint and returns the issued token.
func exchange(ctx context.Context, hc *http.Client, validate func(context.Context, string) error, now time.Time, conn Connection) (*cachedToken, error) {
	origin, err := tenantOrigin(conn.TenantURL)
	if err != nil {
		return nil, err
	}
	if conn.ClientID == "" || conn.ClientSecret == "" {
		return nil, fmt.Errorf("%w: client id and secret are required", ErrAuthFailed)
	}
	if err := checkTenant(ctx, validate, origin); err != nil {
		return nil, err
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {conn.ClientID},
		"client_secret": {conn.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+tokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: building token request", ErrUnreachable)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		drain(resp.Body)
		return nil, fmt.Errorf("%w: token request refused with status %d", ErrAuthFailed, resp.StatusCode)
	default:
		return nil, statusError(resp)
	}

	body, err := readCapped(resp.Body, tokenBodyLimit)
	if err != nil {
		return nil, err
	}
	var ans tokenAnswer
	// Decoder errors are dropped, not wrapped: their text can quote body
	// bytes.
	if json.Unmarshal(body, &ans) != nil || ans.AccessToken == "" {
		return nil, fmt.Errorf("%w: token answer has no access token", ErrUnreadable)
	}
	lifetime := defaultTokenLifetime
	switch {
	case ans.ExpiresIn > int64(maxTokenLifetime/time.Second):
		lifetime = maxTokenLifetime
	case ans.ExpiresIn > 0:
		lifetime = time.Duration(ans.ExpiresIn) * time.Second
	}
	// Refresh tokenRefreshMargin before expiry; a token too short-lived for
	// that margin is refreshed half way through its life.
	refreshIn := max(lifetime-tokenRefreshMargin, lifetime/2)
	return &cachedToken{
		value:     ans.AccessToken,
		origin:    origin,
		revision:  conn.Revision,
		refreshAt: now.Add(refreshIn),
		expiresAt: now.Add(lifetime),
	}, nil
}

// graphqlAnswer is a GraphQL answer envelope.
type graphqlAnswer struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// post sends one GraphQL request with tok and decodes the answer. A 401
// returns errUnauthorized for Query to handle.
func (c *Client) post(ctx context.Context, tok *cachedToken, body []byte, out any) error {
	if err := checkTenant(ctx, c.validate, tok.origin); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	// The URL is built from the token's own origin, so the bearer only ever
	// goes to the tenant that issued it.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tok.origin+graphqlPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: building request", ErrUnreachable)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+tok.value)

	resp, err := c.http.Do(req)
	if err != nil {
		return transportError(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusBadRequest:
		// A 400 carries GraphQL errors for a malformed operation.
	case http.StatusUnauthorized:
		drain(resp.Body)
		return errUnauthorized
	case http.StatusForbidden:
		drain(resp.Body)
		return fmt.Errorf("%w: status %d", ErrAuthFailed, resp.StatusCode)
	default:
		return statusError(resp)
	}

	raw, err := readCapped(resp.Body, c.bodyLimit)
	if err != nil {
		return err
	}
	var ans graphqlAnswer
	if json.Unmarshal(raw, &ans) != nil {
		return fmt.Errorf("%w: answer is not a GraphQL answer", ErrUnreadable)
	}
	if len(ans.Errors) > 0 {
		return graphQLError(ans, tok.value)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrUnreachable, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if len(ans.Data) == 0 || string(ans.Data) == "null" {
		return fmt.Errorf("%w: answer has no data", ErrUnreadable)
	}
	if json.Unmarshal(ans.Data, out) != nil {
		return fmt.Errorf("%w: answer data does not match the operation", ErrUnreadable)
	}
	return nil
}

func graphQLError(ans graphqlAnswer, token string) *GraphQLError {
	e := &GraphQLError{}
	for i, m := range ans.Errors {
		if i == maxGraphQLMessages {
			break
		}
		msg := m.Message
		if token != "" {
			msg = strings.ReplaceAll(msg, token, "[redacted]")
		}
		if len(msg) > maxGraphQLMessageLen {
			msg = msg[:maxGraphQLMessageLen]
		}
		e.Messages = append(e.Messages, msg)
	}
	return e
}

// statusError classifies an unexpected status: 429 is rate limited, every
// other one (redirects included) unreachable. The body is drained, never
// read into the error.
func statusError(resp *http.Response) error {
	drain(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		return &RateLimitError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		return fmt.Errorf("%w: redirect with status %d not followed", ErrUnreachable, resp.StatusCode)
	}
	return fmt.Errorf("%w: status %d", ErrUnreachable, resp.StatusCode)
}

// transportError wraps a client.Do failure. A *url.Error names the request
// URL, which carries no credentials (they travel in the body and headers).
func transportError(err error) error {
	return fmt.Errorf("%w: %w", ErrUnreachable, err)
}

// parseRetryAfter reads a Retry-After value in seconds or as an HTTP date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	}
	if d < 0 {
		return 0
	}
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}

// readCapped reads at most limit bytes; a longer body is unreadable.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading answer: %w", ErrUnreachable, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: answer over %d bytes", ErrUnreadable, limit)
	}
	return body, nil
}

// drain reads a little of a body for connection reuse.
func drain(r io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r, 64<<10))
}

// Probe checks a tenant URL and credentials before they are saved: it
// checks the URL with k8s.ValidateRemoteURLContext, then exchanges the
// credentials through the strict transport and discards the token. Errors
// carry the client's error classes; callers answer clients with a fixed
// message per class and only log the error.
func Probe(ctx context.Context, tenantURL, clientID, clientSecret string) error {
	t := productionTransport()
	defer t.CloseIdleConnections()
	return probe(ctx, t, k8s.ValidateRemoteURLContext, Connection{
		TenantURL:    tenantURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	})
}

func probe(ctx context.Context, base http.RoundTripper, validate func(context.Context, string) error, conn Connection) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, err := exchange(ctx, newHTTPClient(base), validate, time.Now(), conn)
	return err
}
