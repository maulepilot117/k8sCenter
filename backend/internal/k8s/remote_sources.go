package k8s

// Remote multi-source reads: the shared vocabulary feature handlers use to
// read several lists from a remote cluster independently and to say which of
// them failed (R-8 KTD4, KTD8). A remote cluster sends no informer events, so
// each handler lists on demand as the requesting identity; one list failing
// must not hide the others, and the response must disclose the gap rather
// than undercount silently.

import (
	"errors"
	"log/slog"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kubecenter/kubecenter/internal/recoverutil"
)

// TargetError marks a failure to resolve a client or schema for the selected
// cluster (ClusterClients.ClientForCluster, DynamicClientForCluster or
// TargetSchemaFor), as opposed to a call the cluster received and failed.
// httputil.WriteRemoteLoadError answers the two differently.
type TargetError struct{ Err error }

func (e TargetError) Error() string { return e.Err.Error() }
func (e TargetError) Unwrap() error { return e.Err }

// ErrDiscoveryUnavailable means a remote cluster's discovery could not be
// read, so whether a feature is installed there is unknown.
var ErrDiscoveryUnavailable = errors.New("discovery on the selected cluster is unavailable")

// ErrListPanicked is the result RunLists reports for a list whose goroutine
// panicked; recoverutil logs the panic itself.
var ErrListPanicked = errors.New("list panicked")

// RemoteReason maps a failure to read a feature's state on a remote cluster
// to the reason a status route reports for it.
func RemoteReason(err error) ReasonCode {
	var target TargetError
	if errors.As(err, &target) {
		return ClassifyTargetErr(target.Err)
	}
	return ReasonDiscoveryUnavailable
}

// SourceCoverage names one list a remote cluster could not provide. A
// multi-source response stays a partial 200 and discloses the gap here
// rather than failing whole or undercounting silently.
type SourceCoverage struct {
	Source     string `json:"source"` // resource name, e.g. "kustomizations"
	Status     string `json:"status"` // "forbidden" | "unavailable"
	ReasonCode string `json:"reasonCode"`
}

// CoverageStatusForbidden is the SourceCoverage status of a list the cluster
// refused; the other status, "unavailable", means it failed.
const CoverageStatusForbidden = "forbidden"

// CoverageOf describes each of sources, in order, that has an error in
// failed. It carries a reason code only, never the error text.
func CoverageOf(failed map[string]error, sources ...string) []SourceCoverage {
	var out []SourceCoverage
	for _, src := range sources {
		err := failed[src]
		if err == nil {
			continue
		}
		c := SourceCoverage{Source: src, Status: "unavailable", ReasonCode: string(ReasonUnreachable)}
		switch {
		case apierrors.IsForbidden(err):
			c.Status, c.ReasonCode = CoverageStatusForbidden, string(ReasonForbidden)
		case apierrors.IsUnauthorized(err):
			c.ReasonCode = string(ReasonCredentialsInvalid)
		}
		out = append(out, c)
	}
	return out
}

// NamedList is one list RunLists runs. Label names it in a recovered-panic
// log (package-prefixed, e.g. "gitops list kustomizations"), so an operator
// can tell which source a malformed remote object came from.
type NamedList struct {
	Label string
	Run   func() error
}

// RunLists runs every list concurrently, each under panic recovery, waits for
// all of them and returns their errors in the same order. One list failing
// never cancels another. A list whose goroutine panicked reports
// ErrListPanicked. Bound the lists' duration through the context they close
// over.
func RunLists(logger *slog.Logger, lists []NamedList) []error {
	errs := make([]error, len(lists))
	var g errgroup.Group
	for i, list := range lists {
		errs[i] = ErrListPanicked // overwritten unless the list panics
		recoverutil.Go(&g, logger, list.Label, func() error {
			errs[i] = list.Run()
			return nil
		})
	}
	_ = g.Wait() // every list records its own outcome in errs
	return errs
}
