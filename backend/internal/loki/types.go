package loki

// LokiStatus is the cached discovery result.
type LokiStatus struct {
	Detected bool   `json:"detected"`
	URL      string `json:"url,omitempty"`
	// DetectedVia is how Loki was found: a local discovery method
	// (config-override, service-name, service-label), or for a remote
	// cluster binding or port-forward.
	DetectedVia string `json:"detectedVia,omitempty"`
	// Namespace and Service name the Service a remote cluster's Loki is
	// reached through by port-forward, so the admin can see which one
	// answers.
	Namespace   string `json:"namespace,omitempty"`
	Service     string `json:"service,omitempty"`
	LastChecked string `json:"lastChecked"`
	// Reason and Message explain a cluster whose Loki is unavailable, such
	// as loki_not_found or forbidden.
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// QueryResponse is the response from Loki query endpoints.
type QueryResponse struct {
	Status string    `json:"status"`
	Data   QueryData `json:"data"`
}

type QueryData struct {
	ResultType string   `json:"resultType"`
	Result     []Stream `json:"result"`
	Stats      any      `json:"stats,omitempty"`
}

type Stream struct {
	Labels map[string]string `json:"stream"`
	Values [][]string        `json:"values"` // each: [nanosecond_ts, line]
}

// VolumeResponse is the response from Loki volume endpoints.
type VolumeResponse struct {
	Status string     `json:"status"`
	Data   VolumeData `json:"data"`
}

type VolumeData struct {
	Result []VolumeEntry `json:"result"`
}

type VolumeEntry struct {
	Metric map[string]string `json:"metric"`
	Values [][]any           `json:"values"` // each: [timestamp, "count_string"]
}
