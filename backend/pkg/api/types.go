package api

// Response is the standard API response envelope.
type Response struct {
	Data     any       `json:"data,omitempty"`
	Metadata *Metadata `json:"metadata,omitempty"`
	Error    *APIError `json:"error,omitempty"`
}

// Metadata contains pagination info for list responses.
type Metadata struct {
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// APIError is the standard error response.
type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}
