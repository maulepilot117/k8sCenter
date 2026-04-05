package loki

// LokiStatus is the cached discovery result.
type LokiStatus struct {
	Detected    bool   `json:"detected"`
	URL         string `json:"url,omitempty"`
	DetectedVia string `json:"detectedVia,omitempty"`
	LastChecked string `json:"lastChecked"`
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

// LokiError is the error response format from Loki.
type LokiError struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
}
