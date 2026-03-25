package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/kubecenter/kubecenter/internal/monitoring/dashboards"
)

// GrafanaClient talks to the Grafana HTTP API for dashboard provisioning.
type GrafanaClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewGrafanaClient creates a Grafana API client.
func NewGrafanaClient(baseURL, token string) *GrafanaClient {
	return &GrafanaClient{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// CreateFolder ensures a dashboard folder exists.
func (g *GrafanaClient) CreateFolder(ctx context.Context, uid, title string) error {
	payload, _ := json.Marshal(map[string]string{"uid": uid, "title": title})

	req, err := http.NewRequestWithContext(ctx, "POST", g.baseURL+"/api/folders", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("grafana folder request: %w", err)
	}
	defer resp.Body.Close()

	// 409 = folder already exists, which is fine
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("creating folder: %d %s", resp.StatusCode, body)
	}
	return nil
}

// UpsertDashboard creates or updates a dashboard.
func (g *GrafanaClient) UpsertDashboard(ctx context.Context, dashboard json.RawMessage, folderUID string) error {
	payload := map[string]any{
		"dashboard": json.RawMessage(dashboard),
		"folderUid": folderUID,
		"overwrite": true,
		"message":   "Provisioned by KubeCenter",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling dashboard: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", g.baseURL+"/api/dashboards/db", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("grafana dashboard request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("grafana API returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// DashboardSearchResult is a single entry from Grafana's dashboard search.
type DashboardSearchResult struct {
	UID   string   `json:"uid"`
	Title string   `json:"title"`
	URL   string   `json:"url"`
	Tags  []string `json:"tags"`
}

// SearchDashboards lists dashboards by tag.
func (g *GrafanaClient) SearchDashboards(ctx context.Context, tag string) ([]DashboardSearchResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", g.baseURL+"/api/search?tag="+tag, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("grafana search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grafana search returned %d", resp.StatusCode)
	}

	var results []DashboardSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decoding search results: %w", err)
	}
	return results, nil
}

// ProvisionDashboards loads embedded dashboard JSON files and upserts them
// into Grafana under a "KubeCenter" folder. Returns the number of dashboards
// provisioned.
func (g *GrafanaClient) ProvisionDashboards(ctx context.Context, logger *slog.Logger) (int, error) {
	// Ensure the KubeCenter folder exists
	if err := g.CreateFolder(ctx, "kubecenter", "KubeCenter"); err != nil {
		return 0, fmt.Errorf("creating folder: %w", err)
	}

	entries, err := dashboards.FS.ReadDir(".")
	if err != nil {
		return 0, fmt.Errorf("reading embedded dashboards: %w", err)
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := dashboards.FS.ReadFile(entry.Name())
		if err != nil {
			logger.Error("failed to read dashboard file", "file", entry.Name(), "error", err)
			continue
		}
		if err := g.UpsertDashboard(ctx, json.RawMessage(data), "kubecenter"); err != nil {
			logger.Error("failed to upsert dashboard", "file", entry.Name(), "error", err)
			continue
		}
		count++
		logger.Info("provisioned dashboard", "file", entry.Name())
	}
	return count, nil
}
