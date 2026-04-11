package scanning

// Scanner identifies which security scanning tool produced a report.
type Scanner string

const (
	ScannerNone      Scanner = ""
	ScannerTrivy     Scanner = "trivy"
	ScannerKubescape Scanner = "kubescape"
	ScannerBoth      Scanner = "both"
)

// ScannerStatus reports which security scanners are detected in the cluster.
type ScannerStatus struct {
	Detected    Scanner        `json:"detected"`
	Trivy       *ScannerDetail `json:"trivy,omitempty"`
	Kubescape   *ScannerDetail `json:"kubescape,omitempty"`
	LastChecked string         `json:"lastChecked"`
}

// ScannerDetail describes a single scanner's availability.
type ScannerDetail struct {
	Available bool   `json:"available"`
	Namespace string `json:"namespace,omitempty"`
}

// SeveritySummary holds vulnerability counts per severity level.
type SeveritySummary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

// WorkloadVulnSummary is the scanner-agnostic vulnerability summary per workload.
type WorkloadVulnSummary struct {
	Namespace   string          `json:"namespace"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Images      []ImageVulnInfo `json:"images"`
	Total       SeveritySummary `json:"total"`
	LastScanned string          `json:"lastScanned"`
	Scanner     Scanner         `json:"scanner"`
}

// ImageVulnInfo holds vulnerability counts for a single container image.
type ImageVulnInfo struct {
	Image      string          `json:"image"`
	Severities SeveritySummary `json:"severities"`
}

// VulnListMetadata provides summary counts for the vulnerability list response.
type VulnListMetadata struct {
	Total    int             `json:"total"`
	Severity SeveritySummary `json:"severity"`
}

// CVEDetail represents an individual vulnerability finding with full metadata.
type CVEDetail struct {
	ID               string   `json:"id"`
	Severity         string   `json:"severity"` // CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN
	CVSSScore        *float64 `json:"cvssScore"` // nil when unavailable
	Package          string   `json:"package"`
	InstalledVersion string   `json:"installedVersion"`
	FixedVersion     string   `json:"fixedVersion"` // empty = no fix available
	Title            string   `json:"title"`
	PrimaryLink      string   `json:"primaryLink"`
}

// ImageVulnDetail holds detailed vulnerabilities for a single container image.
type ImageVulnDetail struct {
	Name            string      `json:"name"`      // image reference (repo:tag)
	Container       string      `json:"container"` // container name in workload
	Vulnerabilities []CVEDetail `json:"vulnerabilities"`
}

// WorkloadVulnDetail is the full detail response for a workload.
// Summary counts are computed client-side from the vulnerabilities array.
type WorkloadVulnDetail struct {
	Namespace   string            `json:"namespace"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Scanner     Scanner           `json:"scanner"`
	LastScanned string            `json:"lastScanned"`
	Images      []ImageVulnDetail `json:"images"`
}
