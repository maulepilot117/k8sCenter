package audit

import "time"

const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// QueryParams defines filters and pagination for audit log queries.
type QueryParams struct {
	User         string
	Action       string
	ResourceKind string
	Namespace    string
	Since        time.Time
	Until        time.Time
	Page         int
	PageSize     int
}

// Normalize applies defaults and clamps values.
func (q *QueryParams) Normalize() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = DefaultPageSize
	}
	if q.PageSize > MaxPageSize {
		q.PageSize = MaxPageSize
	}
}

// Offset returns the SQL offset for the current page.
func (q *QueryParams) Offset() int {
	return (q.Page - 1) * q.PageSize
}
