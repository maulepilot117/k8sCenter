package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is an HTTP client for the Loki API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	tenantID   string // X-Scope-OrgID for multi-tenant Loki
}

// NewClient creates a Loki API client.
func NewClient(baseURL, tenantID string) *Client {
	return &Client{
		httpClient: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		baseURL:  strings.TrimRight(baseURL, "/"),
		tenantID: tenantID,
	}
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
		return fmt.Errorf("loki health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("loki not ready: status %d", resp.StatusCode)
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
		return fmt.Errorf("loki request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB limit
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var lokiErr LokiError
		if json.Unmarshal(body, &lokiErr) == nil && lokiErr.Error != "" {
			return fmt.Errorf("loki error (%d): %s", resp.StatusCode, lokiErr.Error)
		}
		return fmt.Errorf("loki returned status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// setHeaders adds common headers to a Loki request.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if c.tenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.tenantID)
	}
}
