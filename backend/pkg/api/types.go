package api

// Response is the standard API response envelope.
type Response struct {
	Data     any       `json:"data,omitempty"`
	Metadata *Metadata `json:"metadata,omitempty"`
	Error    *APIError `json:"error,omitempty"`
}

// Metadata contains pagination info for list responses.
type Metadata struct {
	Total    int    `json:"total"`
	Continue string `json:"continue,omitempty"`
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"pageSize,omitempty"`
}

// APIError is the standard error response.
//
// Reason and Extra were added to support endpoint-specific 409 metadata
// (e.g. "active_job_exists" + jobId) without each endpoint hand-rolling
// JSON. Frontend consumers can read APIError.Reason directly. See todo #350.
type APIError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Detail  string         `json:"detail,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}
