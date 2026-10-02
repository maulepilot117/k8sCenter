package resources

import (
	"net/url"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
)

// FuzzParseInvolvedObjectFilter checks the events involvedObject filter, the
// seam that turns untrusted query strings into a remote API-server field
// selector, against arbitrary kind/name pairs:
//
//  1. Oracle A: parsing, rendering and matching never panic.
//  2. A rejected pair yields the zero (inactive) filter.
//  3. An accepted kind is an ASCII identifier of at most 63 characters and an
//     accepted name is at most 253 bytes of valid UTF-8 with no '/', '%',
//     control character, and is not "." or "..". Both rules are re-derived
//     here rather than read from production, so dropping a guard is caught.
//  4. Oracle B: an accepted filter's fieldSelector() parses back through
//     fields.ParseSelector into exactly one equality term per non-empty input,
//     on involvedObject.kind / involvedObject.name, whose value is the input
//     byte for byte. Selector syntax in a value can never add a term.
//  5. An accepted filter matches an event carrying exactly its kind and name,
//     and an active one drops anything that is not an Event.
func FuzzParseInvolvedObjectFilter(f *testing.F) {
	// Realistic inputs.
	f.Add("Pod", "web-1")
	f.Add("Node", "node-1")
	f.Add("", "")
	f.Add("ClusterRole", "system:controller:job-controller")
	f.Add("HorizontalPodAutoscaler", "")
	// Teeth: selector syntax that must stay inside its value.
	f.Add("ClusterRole", "a,involvedObject.name=b")
	f.Add("Pod", `a\,b`)
	f.Add("Pod", `trailing\`)
	f.Add("Pod", "x!=y")
	f.Add("Pod,involvedObject.name=x", "web-1")
	// Teeth: inputs the guards must reject.
	f.Add("Pod", "web\x001")
	f.Add("Pod", "web\n1")
	f.Add("Pod", strings.Repeat("a", 254))
	f.Add("P"+strings.Repeat("o", 63), "x")
	f.Add("Pod", "\xff\xfe")
	f.Add("Pod", "..")
	f.Add("Pod", ".")
	f.Add("Pod", "a/b")
	f.Add("Pod", "a%2Fb")
	f.Add("1Pod", "x")
	f.Add("Pód", "x")

	f.Fuzz(func(t *testing.T, kind, name string) {
		q := url.Values{}
		q.Set(queryInvolvedObjectKind, kind)
		q.Set(queryInvolvedObjectName, name)

		filter, err := parseInvolvedObjectFilter(q)
		if err != nil {
			if filter != (involvedObjectFilter{}) {
				t.Fatalf("rejected %q/%q returned a non-zero filter %+v", kind, name, filter)
			}
			if fuzzKindOK(kind) && fuzzNameOK(name) {
				t.Fatalf("valid kind %q / name %q rejected: %v", kind, name, err)
			}
			return
		}

		if !fuzzKindOK(kind) {
			t.Fatalf("invalid kind %q accepted", kind)
		}
		if !fuzzNameOK(name) {
			t.Fatalf("invalid name %q accepted", name)
		}
		if filter.kind != kind || filter.name != name {
			t.Fatalf("filter %+v does not carry the inputs %q/%q", filter, kind, name)
		}

		rendered := filter.fieldSelector()
		want := map[string]string{}
		if kind != "" {
			want["involvedObject.kind"] = kind
		}
		if name != "" {
			want["involvedObject.name"] = name
		}
		if len(want) == 0 {
			if rendered != "" {
				t.Fatalf("inactive filter rendered selector %q", rendered)
			}
		} else {
			sel, perr := fields.ParseSelector(rendered)
			if perr != nil {
				t.Fatalf("selector %q for %q/%q does not parse: %v", rendered, kind, name, perr)
			}
			reqs := sel.Requirements()
			if len(reqs) != len(want) {
				t.Fatalf("selector %q has %d terms, want %d", rendered, len(reqs), len(want))
			}
			for _, r := range reqs {
				v, ok := want[r.Field]
				if !ok || r.Value != v || r.Operator != "=" {
					t.Fatalf("selector %q term %+v, want an equality on one of %v", rendered, r, want)
				}
			}
		}

		e := &corev1.Event{InvolvedObject: corev1.ObjectReference{Kind: kind, Name: name}}
		if !filter.matches(e) {
			t.Fatalf("filter %+v does not match an event about %q/%q", filter, kind, name)
		}
		// The inactive filter returns its input untouched by contract.
		if filter.active() {
			if got := filter.filterEvents([]any{e, "not an event"}); len(got) != 1 {
				t.Fatalf("filterEvents kept %d items, want only the matching event", len(got))
			}
		}
	})
}

// fuzzKindOK re-derives the Kind rule: empty, or an ASCII letter followed by
// up to 62 ASCII letters or digits.
func fuzzKindOK(kind string) bool {
	if kind == "" {
		return true
	}
	if len(kind) > 63 {
		return false
	}
	for i, c := range kind {
		isLetter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// fuzzNameOK re-derives the name rule: empty, or at most 253 bytes of valid
// UTF-8 that is a valid API path segment and holds no control character.
func fuzzNameOK(name string) bool {
	if name == "" {
		return true
	}
	if len(name) > 253 || !utf8.ValidString(name) {
		return false
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/%") {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}
