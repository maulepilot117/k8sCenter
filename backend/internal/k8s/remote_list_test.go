package k8s

import (
	"context"
	"errors"
	"strconv"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPageList_FollowsContinueUntilEmpty(t *testing.T) {
	var seen []metav1.ListOptions
	base := metav1.ListOptions{LabelSelector: "app=x", Limit: 7, Continue: "stale"}
	items, truncated, err := PageList(context.Background(), base,
		func(_ context.Context, opts metav1.ListOptions) ([]int, string, error) {
			seen = append(seen, opts)
			if len(seen) < 3 {
				return []int{len(seen)}, "tok" + strconv.Itoa(len(seen)), nil
			}
			return []int{len(seen)}, "", nil
		})
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v, want nil/false", err, truncated)
	}
	if len(items) != 3 || items[0] != 1 || items[2] != 3 {
		t.Fatalf("items = %v, want [1 2 3]", items)
	}
	wantContinue := []string{"", "tok1", "tok2"}
	for i, o := range seen {
		if o.Limit != RemoteListPageSize {
			t.Errorf("page %d Limit = %d, want %d", i, o.Limit, RemoteListPageSize)
		}
		if o.Continue != wantContinue[i] {
			t.Errorf("page %d Continue = %q, want %q", i, o.Continue, wantContinue[i])
		}
		if o.LabelSelector != "app=x" {
			t.Errorf("page %d lost the caller's selector: %q", i, o.LabelSelector)
		}
	}
}

func TestPageList_StopsAtCapAndReportsTruncated(t *testing.T) {
	pages := 0
	items, truncated, err := PageList(context.Background(), metav1.ListOptions{},
		func(context.Context, metav1.ListOptions) ([]int, string, error) {
			pages++
			return []int{pages}, "more", nil
		})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true when a continue token remains after the cap")
	}
	if pages != RemoteListMaxPages || len(items) != RemoteListMaxPages {
		t.Errorf("pages=%d items=%d, want %d each", pages, len(items), RemoteListMaxPages)
	}
}

func TestPageList_ErrorReturnsItemsReadBeforeIt(t *testing.T) {
	boom := errors.New("boom")
	pages := 0
	items, truncated, err := PageList(context.Background(), metav1.ListOptions{},
		func(context.Context, metav1.ListOptions) ([]int, string, error) {
			pages++
			if pages == 3 {
				return nil, "", boom
			}
			return []int{pages}, "more", nil
		})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if truncated {
		t.Error("truncated = true on error, want false")
	}
	if len(items) != 2 || items[0] != 1 || items[1] != 2 {
		t.Errorf("items = %v, want the two read before the error", items)
	}
}
