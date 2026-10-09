package k8s

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// RemoteListPageSize matches the prober's node-list cap
	// (cluster_prober.go) so one page never asks a remote API server for more
	// than it already serves us elsewhere.
	RemoteListPageSize = 500
	// RemoteListMaxPages bounds one remote list to 5,000 items. A list still
	// carrying a continue token after this many pages is reported as
	// truncated.
	RemoteListMaxPages = 10
)

// PageList is the one remote paging policy: it reads list in pages of
// RemoteListPageSize, following continue tokens, for at most
// RemoteListMaxPages pages. base carries the caller's selectors; its Limit and
// Continue are overwritten. truncated reports that the list still had a
// continue token after the last allowed page. On error it returns the error
// together with the items read before it, and the caller decides whether
// those may be shown (a refusal at a later page means they may not).
func PageList[T any](
	ctx context.Context, base metav1.ListOptions,
	list func(context.Context, metav1.ListOptions) ([]T, string, error),
) (items []T, truncated bool, err error) {
	opts := base
	opts.Limit = RemoteListPageSize
	opts.Continue = ""
	for page := 0; page < RemoteListMaxPages; page++ {
		batch, next, err := list(ctx, opts)
		if err != nil {
			return items, false, err
		}
		items = append(items, batch...)
		if next == "" {
			return items, false, nil
		}
		opts.Continue = next
	}
	return items, true, nil
}
