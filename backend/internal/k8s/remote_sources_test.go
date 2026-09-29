package k8s

import (
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestCoverageOf_ReportsFailedSourcesInOrderWithoutErrorText(t *testing.T) {
	gr := schema.GroupResource{Resource: "things"}
	failed := map[string]error{
		"b": fmt.Errorf("list b: %w", apierrors.NewForbidden(gr, "", errors.New("secret detail"))),
		"c": apierrors.NewUnauthorized("secret detail"),
		"d": errors.New("dial tcp 10.20.30.40:6443: connection refused"),
	}
	got := CoverageOf(failed, "a", "d", "b", "c")
	want := []SourceCoverage{
		{Source: "d", Status: "unavailable", ReasonCode: string(ReasonUnreachable)},
		{Source: "b", Status: CoverageStatusForbidden, ReasonCode: string(ReasonForbidden)},
		{Source: "c", Status: "unavailable", ReasonCode: string(ReasonCredentialsInvalid)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CoverageOf = %+v, want %+v", got, want)
	}
	if CoverageOf(nil, "a") != nil {
		t.Error("no failures must give no coverage")
	}
}

func TestRemoteReason(t *testing.T) {
	if got := RemoteReason(fmt.Errorf("x: %w", TargetError{Err: errors.New("decrypt failed")})); got != ReasonCredentialsInvalid {
		t.Errorf("target error reason = %q", got)
	}
	if got := RemoteReason(ErrDiscoveryUnavailable); got != ReasonDiscoveryUnavailable {
		t.Errorf("discovery reason = %q", got)
	}
}

func TestRunLists_RunsEveryListAndIsolatesAPanic(t *testing.T) {
	boom := errors.New("boom")
	var ran atomic.Int32
	errs := RunLists(nil, []NamedList{
		{"test ok", func() error { ran.Add(1); return nil }},
		{"test panics", func() error { ran.Add(1); panic("bad remote object") }},
		{"test fails", func() error { ran.Add(1); return boom }},
	})
	if ran.Load() != 3 {
		t.Fatalf("ran %d lists, want 3", ran.Load())
	}
	if errs[0] != nil || !errors.Is(errs[1], ErrListPanicked) || !errors.Is(errs[2], boom) {
		t.Errorf("errs = %v, want [nil ErrListPanicked boom]", errs)
	}
}
