package loki

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

// DialFunc opens the network connection a Loki request or tail runs over.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// StatusError is a Loki answer that was not 200 OK. It carries the status
// only: Loki's response body, which a remote Loki controls, never reaches
// an error message, a log line or a client.
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("loki returned status %d", e.Code)
}

// errLokiTransport marks a request that got no answer from Loki: the
// connection could not be made or broke. QueryFailure reports it as Loki
// being unreachable.
var errLokiTransport = errors.New("loki request failed")

// Client is an HTTP client for the Loki API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	tenantID   string // X-Scope-OrgID for multi-tenant Loki

	// token is the bearer token, sent only to tokenOrigin, the https origin
	// of baseURL; tokenOrigin is "" when no token may be sent.
	token       string
	tokenOrigin string
	// tailDial and tailTLS are the tail dialer's connection and TLS
	// settings; nil means the defaults.
	tailDial DialFunc
	tailTLS  *tls.Config
}

// NewClient creates a Loki API client for the local cluster's Loki.
func NewClient(baseURL, tenantID string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          10,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 15 * time.Second,
			},
		},
		baseURL:  strings.TrimRight(baseURL, "/"),
		tenantID: tenantID,
	}
}

// newRemoteClient creates a client for a remote cluster's Loki. Requests
// go through rt and the tail dials through dial, so the caller decides
// where a connection can go (the strict dialer, or a port-forward tunnel).
// A redirect is never followed: the 3xx is the answer, and fails the
// request, so Loki or a proxy in front of it cannot steer the request, or
// the token, anywhere else. token is sent only when baseURL is https, and
// only to its origin.
func newRemoteClient(baseURL, tenantID, token string, rt http.RoundTripper, dial DialFunc) *Client {
	c := &Client{
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: rt,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL:  strings.TrimRight(baseURL, "/"),
		tenantID: tenantID,
		tailDial: dial,
	}
	if origin := urlorigin.Of(c.baseURL); token != "" && strings.HasPrefix(origin, "https://") {
		c.token, c.tokenOrigin = token, origin
	}
	return c
}

// BaseURL returns the base URL this client is configured for.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// QueryRange executes a LogQL range query.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, limit int, direction string) (*QueryResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	params := url.Values{
		"query":     {query},
		"start":     {strconv.FormatInt(start.UnixNano(), 10)},
		"end":       {strconv.FormatInt(end.UnixNano(), 10)},
		"limit":     {strconv.Itoa(limit)},
		"direction": {direction},
	}

	var resp QueryResponse
	if err := c.get(ctx, "/loki/api/v1/query_range", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Labels returns all label names.
func (c *Client) Labels(ctx context.Context, start, end time.Time) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	params := url.Values{}
	if !start.IsZero() {
		params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}

	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := c.get(ctx, "/loki/api/v1/labels", params, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// LabelValues returns values for a specific label.
func (c *Client) LabelValues(ctx context.Context, name string, start, end time.Time, query string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	params := url.Values{}
	if !start.IsZero() {
		params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}
	if query != "" {
		params.Set("query", query)
	}

	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	path := fmt.Sprintf("/loki/api/v1/label/%s/values", url.PathEscape(name))
	if err := c.get(ctx, path, params, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// VolumeRange returns log volume over a time range.
func (c *Client) VolumeRange(ctx context.Context, query string, start, end time.Time, step string, targetLabels []string) (*VolumeResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	params := url.Values{
		"query": {query},
		"start": {strconv.FormatInt(start.UnixNano(), 10)},
		"end":   {strconv.FormatInt(end.UnixNano(), 10)},
	}
	if step != "" {
		params.Set("step", step)
	}
	if len(targetLabels) > 0 {
		params.Set("targetLabels", strings.Join(targetLabels, ","))
	}

	var resp VolumeResponse
	if err := c.get(ctx, "/loki/api/v1/index/volume_range", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Ready checks if Loki is ready to accept queries.
func (c *Client) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/ready", nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("loki health check: %w: %w", errLokiTransport, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &StatusError{Code: resp.StatusCode}
	}
	return nil
}

// TailURL returns the WebSocket URL for the Loki tail endpoint.
func (c *Client) TailURL(query string, startNano int64, limit int) string {
	wsURL := strings.Replace(c.baseURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)

	params := url.Values{
		"query": {query},
	}
	if startNano > 0 {
		params.Set("start", strconv.FormatInt(startNano, 10))
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	return wsURL + "/loki/api/v1/tail?" + params.Encode()
}

// DialTail opens Loki's tail WebSocket for query, through the client's tail
// dialer and with its tenant and token headers. A refused handshake is a
// *StatusError; a connection that could not be made is a transport error.
func (c *Client) DialTail(ctx context.Context, query string, startNano int64, limit int) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		NetDialContext:   c.tailDial,
		TLSClientConfig:  c.tailTLS,
	}
	headers := http.Header{}
	if c.tenantID != "" {
		headers.Set("X-Scope-OrgID", c.tenantID)
	}
	// The tail URL is the base URL with its scheme swapped, so it has the
	// token's origin exactly when the base URL is https.
	if c.tokenOrigin != "" {
		headers.Set("Authorization", "Bearer "+c.token)
	}
	conn, resp, err := dialer.DialContext(ctx, c.TailURL(query, startNano, limit), headers)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return nil, &StatusError{Code: resp.StatusCode}
		}
		return nil, fmt.Errorf("loki tail: %w: %w", errLokiTransport, err)
	}
	return conn, nil
}

// get performs a GET request to a Loki API endpoint and decodes the response.
func (c *Client) get(ctx context.Context, path string, params url.Values, result any) error {
	reqURL := c.baseURL + path
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", errLokiTransport, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Drain a bounded amount so the connection can be reused; the body
		// itself is never read into the error.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return &StatusError{Code: resp.StatusCode}
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// setHeaders adds common headers to a Loki request, and the bearer token
// when the request goes to the token's origin.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if c.tenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.tenantID)
	}
	if c.tokenOrigin != "" && urlorigin.Of(req.URL.String()) == c.tokenOrigin {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// closeIdle releases the client's pooled connections.
func (c *Client) closeIdle() {
	c.httpClient.CloseIdleConnections()
}
