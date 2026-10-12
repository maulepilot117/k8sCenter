package policy

// The compliance recorder's read of a remote cluster (#608, KTD4). Unlike
// fetchRemote, which serves the live routes and fails closed on the first
// failed list, this read collects every list's outcome and says which engines
// could be read, so a day the identity sees only part of the cluster becomes
// a partial point rather than a lost one. It runs with no request: the
// identity is given, and neither the request-scoped caches nor h.load are
// used, so every snapshot reads the cluster afresh.

import (
	"context"
	"errors"
	"sync"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// recordOutcome is what one recorder read of a remote cluster yields.
type recordOutcome string

const (
	// recordFull is a point read from every installed engine.
	recordFull recordOutcome = "full"
	// recordPartial is a point read from some installed engines; the others
	// are named in remoteRecord.Unreadable.
	recordPartial recordOutcome = "partial"
	// recordGap is a day with no point; remoteRecord.GapReason says why.
	recordGap recordOutcome = "gap"
)

// Gap reasons this read decides (KTD4 steps 3 to 5). The impersonation
// pre-check and the target's reachability (steps 1 and 2) are the
// recorder's, which classifies a returned k8s.TargetError.
const (
	gapDiscoveryUnavailable = string(k8s.ReasonDiscoveryUnavailable)
	gapNoPolicyEngine       = "no_policy_engine"
	gapForbidden            = string(k8s.ReasonForbidden)
	gapUnreachable          = string(k8s.ReasonUnreachable)
)

// remoteRecord is one recorder read of a remote cluster as one identity.
type remoteRecord struct {
	Outcome recordOutcome
	// GapReason is set only when Outcome is recordGap.
	GapReason string
	// Policies and Violations come from readable engines only, and Score is
	// computed from them as the local recorder computes its own.
	Policies   []NormalizedPolicy
	Violations []NormalizedViolation
	Score      ComplianceScore
	// Installed are the engines discovery shows; Unreadable the installed
	// ones with at least one failed list. Both in kyverno, gatekeeper order.
	Installed  []Engine
	Unreadable []Engine
	// Coverage names each failed list, with a reason code and no error text.
	Coverage []k8s.SourceCoverage
}

func gapRecord(reason string, installed, unreadable []Engine, coverage []k8s.SourceCoverage) *remoteRecord {
	return &remoteRecord{Outcome: recordGap, GapReason: reason, Installed: installed, Unreadable: unreadable, Coverage: coverage}
}

// sourceEngine is the engine a remote list belongs to.
func sourceEngine(src remoteSource) Engine {
	if src.gvr.Group == gatekeeperConstraintsGroup {
		return EngineGatekeeper
	}
	return EngineKyverno
}

// readRemoteForRecord reads clusterID's policies and violations as the given
// identity and classifies the result (KTD4 steps 3 to 7). An engine is
// readable only when every list it needs succeeded.
//
// It returns an error, and no record, when the outcome is not this read's to
// decide: a k8s.TargetError (the cluster could not be reached or its
// credentials were refused, KTD4 step 2), errRemoteBudget (more lists than
// one read may issue), or ctx's error when the read was cancelled or ran out
// of time, which says nothing about the identity's view.
func (h *Handler) readRemoteForRecord(ctx context.Context, clusterID, username string, groups []string) (*remoteRecord, error) {
	user := &auth.User{KubernetesUsername: username, KubernetesGroups: groups}

	d, err := h.remoteDiscovery(ctx, clusterID, user)
	switch {
	case errors.Is(err, k8s.ErrDiscoveryUnavailable):
		return gapRecord(gapDiscoveryUnavailable, nil, nil, nil), nil
	case err != nil:
		return nil, err
	}

	var installed []Engine
	if d.status.Kyverno != nil {
		installed = append(installed, EngineKyverno)
	}
	if d.status.Gatekeeper != nil {
		installed = append(installed, EngineGatekeeper)
	}
	if len(installed) == 0 {
		return gapRecord(gapNoPolicyEngine, nil, nil, nil), nil
	}
	if len(d.sources) > remoteMaxSources {
		h.Logger.Warn("remote compliance read refused: too many sources", "cluster", clusterID, "sources", len(d.sources), "max", remoteMaxSources)
		return nil, errRemoteBudget
	}

	byEngine := map[Engine]*remoteData{}
	for _, e := range installed {
		byEngine[e] = &remoteData{}
	}
	var errs []error
	if len(d.sources) > 0 {
		dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, username, groups)
		if err != nil {
			return nil, k8s.TargetError{Err: err}
		}
		var mu sync.Mutex
		sem := make(chan struct{}, remoteListConcurrency)
		runs := make([]k8s.NamedList, len(d.sources))
		for i, src := range d.sources {
			bucket := byEngine[sourceEngine(src)]
			runs[i] = k8s.NamedList{Label: src.label, Run: func() error {
				sem <- struct{}{}
				defer func() { <-sem }()
				items, err := listRemote(ctx, dyn, src.gvr)
				if err != nil {
					return err
				}
				mu.Lock()
				defer mu.Unlock()
				for j := range items {
					src.add(bucket, &items[j])
				}
				return nil
			}}
		}
		errs = k8s.RunLists(h.Logger, runs)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	failed := map[string]error{}
	names := make([]string, 0, len(d.sources))
	unreadableSet := map[Engine]bool{}
	allForbidden := true
	for i, src := range d.sources {
		names = append(names, src.gvr.Resource)
		switch err := errs[i]; {
		case err == nil:
		case k8s.IsResourceGone(err):
			// The resource went away after discovery: nothing to read.
			h.Presence.Recheck(ctx, clusterID, username, groups, src.gvr.GroupResource())
		default:
			failed[src.gvr.Resource] = err
			unreadableSet[sourceEngine(src)] = true
		}
	}
	coverage := k8s.CoverageOf(failed, names...)
	for _, c := range coverage {
		if c.Status != k8s.CoverageStatusForbidden {
			allForbidden = false
		}
	}

	rec := &remoteRecord{Installed: installed, Coverage: coverage, Policies: []NormalizedPolicy{}, Violations: []NormalizedViolation{}}
	for _, e := range installed {
		if unreadableSet[e] {
			rec.Unreadable = append(rec.Unreadable, e)
			continue
		}
		rec.Policies = append(rec.Policies, byEngine[e].policies...)
		rec.Violations = append(rec.Violations, byEngine[e].violations...)
	}

	switch {
	case len(rec.Unreadable) == len(installed):
		reason := gapUnreachable
		if allForbidden {
			reason = gapForbidden
		}
		return gapRecord(reason, installed, rec.Unreadable, coverage), nil
	case len(rec.Unreadable) > 0:
		rec.Outcome = recordPartial
	default:
		rec.Outcome = recordFull
	}
	rec.Score = computeCompliance(rec.Policies, rec.Violations, "")
	return rec, nil
}
