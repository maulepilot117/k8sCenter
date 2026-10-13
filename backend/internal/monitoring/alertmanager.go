package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// Alertmanager error classes. Every error from AlertmanagerFor, Counts and
// the probes matches exactly one of them (errors.Is), so callers can choose
// a fixed client message per class. No error ever carries response body
// text; one may name an address or status code, so callers log it and
// never return it.
//
// They alias the resources package's sentinels (where they are defined
// because this package imports resources), so the dashboard classifies them
// with errors.Is without importing monitoring.
var (
	// ErrNoAlertmanager means the cluster's binding has no Alertmanager URL.
	ErrNoAlertmanager = resources.ErrNoAlertmanager
	// ErrAlertmanagerTokenReentry means a stored Alertmanager token no
	// longer matches the Alertmanager URL's origin and must be entered
	// again. No request is sent.
	ErrAlertmanagerTokenReentry = resources.ErrAlertmanagerTokenReentry
	// ErrAlertmanagerRefused is a 401 or 403 answer.
	ErrAlertmanagerRefused = resources.ErrAlertmanagerRefused
	// ErrAlertmanagerRedirected is a 3xx answer, which is never followed.
	ErrAlertmanagerRedirected = resources.ErrAlertmanagerRedirected
	// ErrAlertmanagerUnreachable is a transport failure, a timeout, or a
	// status that is not 2xx, 3xx, 401 or 403 (5xx included).
	ErrAlertmanagerUnreachable = resources.ErrAlertmanagerUnreachable
	// ErrAlertmanagerUnreadable is a 2xx answer over the size cap, not JSON,
	// or not a JSON array of alerts.
	ErrAlertmanagerUnreadable = resources.ErrAlertmanagerUnreadable
)

const (
	// alertmanagerBodyLimit caps an alerts answer. A larger answer is
	// unreadable, never a partial count.
	alertmanagerBodyLimit = 16 << 20
	// alertmanagerReadTimeout bounds one Counts read end to end.
	alertmanagerReadTimeout = 10 * time.Second
	// alertmanagerProbeFilter is a label matcher no real alert carries, so
	// the probe exercises the alerts read path without fetching alerts.
	alertmanagerProbeFilter = `alertname="kubecenter-probe-matches-nothing"`
)

// heartbeatAlerts are the always-firing alerts a remote count skips: the
// heartbeats the local count skips, plus those only a direct alerts read
// sees (resources documents both lists).
var heartbeatAlerts = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range resources.HeartbeatAlertNames {
		m[n] = true
	}
	for _, n := range resources.RemoteOnlyHeartbeatAlertNames {
		m[n] = true
	}
	return m
}()

// AlertCounts is a remote cluster's active alert totals, heartbeat alerts
// excluded. Critical counts alerts labelled severity="critical".
type AlertCounts struct {
	Active   int
	Critical int
}

// AlertmanagerClient reads one bound Alertmanager's v2 alerts API. Build it
// with newAlertmanagerClient over a bearer round tripper bound to the
// Alertmanager URL, so its token goes only to that origin over https.
type AlertmanagerClient struct {
	alertsURL *url.URL
	client    *http.Client
}

// newAlertmanagerClient returns a client for the Alertmanager at rawURL,
// keeping any path prefix. Like the remote Prometheus client it never
// follows a redirect.
func newAlertmanagerClient(rawURL string, rt http.RoundTripper) (*AlertmanagerClient, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid alertmanager url", ErrAlertmanagerUnreachable)
	}
	alerts := u.JoinPath("api", "v2", "alerts")
	alerts.RawQuery = ""
	alerts.Fragment = ""
	return &AlertmanagerClient{
		alertsURL: alerts,
		client: &http.Client{
			Transport: rt,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Counts reads the active alerts that are not silenced, inhibited or
// unprocessed and totals them. Any failure is one of the error classes,
// never a partial count.
func (c *AlertmanagerClient) Counts(ctx context.Context) (AlertCounts, error) {
	ctx, cancel := context.WithTimeout(ctx, alertmanagerReadTimeout)
	defer cancel()
	alerts, err := c.list(ctx, "")
	if err != nil {
		return AlertCounts{}, err
	}
	var counts AlertCounts
	for _, a := range alerts {
		// The server may ignore the state parameters; count only active.
		if a.Status.State != "active" || heartbeatAlerts[a.Labels["alertname"]] {
			continue
		}
		counts.Active++
		if a.Labels["severity"] == "critical" {
			counts.Critical++
		}
	}
	return counts, nil
}

// amAlertJSON is the part of an Alertmanager v2 gettableAlert the counts
// read needs.
type amAlertJSON struct {
	Labels map[string]string `json:"labels"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
}

// list issues GET <url>/api/v2/alerts with the active-only state filter and,
// when filter is non-empty, that label matcher.
func (c *AlertmanagerClient) list(ctx context.Context, filter string) ([]amAlertJSON, error) {
	target := *c.alertsURL
	q := url.Values{
		"active":      {"true"},
		"silenced":    {"false"},
		"inhibited":   {"false"},
		"unprocessed": {"false"},
	}
	if filter != "" {
		q.Set("filter", filter)
	}
	target.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: building request", ErrAlertmanagerUnreachable)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAlertmanagerUnreachable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Drain a little for connection reuse; the body is never read
		// into an error.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, fmt.Errorf("%w: status %d", ErrAlertmanagerRefused, resp.StatusCode)
		case resp.StatusCode >= 300 && resp.StatusCode <= 399:
			return nil, fmt.Errorf("%w: status %d", ErrAlertmanagerRedirected, resp.StatusCode)
		default:
			return nil, fmt.Errorf("%w: status %d", ErrAlertmanagerUnreachable, resp.StatusCode)
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, alertmanagerBodyLimit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading answer: %w", ErrAlertmanagerUnreachable, err)
	}
	return decodeAlerts(body)
}

// decodeAlerts parses an alerts answer. Decoder errors are dropped, not
// wrapped: their text can quote body bytes.
func decodeAlerts(body []byte) ([]amAlertJSON, error) {
	if len(body) > alertmanagerBodyLimit {
		return nil, fmt.Errorf("%w: answer over %d bytes", ErrAlertmanagerUnreadable, alertmanagerBodyLimit)
	}
	if trimmed := bytes.TrimLeft(body, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("%w: answer is not a JSON array", ErrAlertmanagerUnreadable)
	}
	var alerts []amAlertJSON
	if err := json.Unmarshal(body, &alerts); err != nil {
		return nil, fmt.Errorf("%w: answer is not a list of alerts", ErrAlertmanagerUnreadable)
	}
	return alerts, nil
}

// ProbeAlertmanager checks that alertmanagerURL answers its alerts API with
// token: GET <url>/api/v2/alerts with a filter that matches nothing, through
// the strict transport, within 5s, without following redirects, expecting a
// JSON array. Errors carry the read's error class; callers answer clients
// with a fixed message and only log the error.
func ProbeAlertmanager(ctx context.Context, alertmanagerURL, token string) error {
	t := k8s.StrictHTTPTransport()
	defer t.CloseIdleConnections()
	return probeAlertmanager(ctx, t, alertmanagerURL, token)
}

func probeAlertmanager(ctx context.Context, base http.RoundTripper, alertmanagerURL, token string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c, err := newAlertmanagerClient(alertmanagerURL, newBearerRoundTripper(base, alertmanagerURL, token))
	if err != nil {
		return err
	}
	_, err = c.list(ctx, alertmanagerProbeFilter)
	return err
}
