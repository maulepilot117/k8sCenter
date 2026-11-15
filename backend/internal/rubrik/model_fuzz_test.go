package rubrik

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fuzzCanary stands in for a secret the tenant might echo into an answer.
// Seeds plant it in every position a decoder reads or skips.
const fuzzCanary = "canary-secret-7f3a9c"

// fuzzDecoderErrorText is the only shape a decoder error may take: one of
// the two error classes followed by fixed lowercase words, optionally with a
// count. None of the texts can quote a byte of the answer.
var fuzzDecoderErrorText = regexp.MustCompile(`^rubrik: (unreadable answer|request rejected by rsc): [a-z ]+(, got [0-9]+)?$`)

// fuzzJobStatuses re-derives the JobStatus values, so a new status leaking
// out of a decoder fails the oracle.
var fuzzJobStatuses = map[JobStatus]bool{
	"running": true, "succeeded": true, "failed": true, "partial": true, "canceled": true, "unknown": true,
}

func checkDecoderError(t *testing.T, name string, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnreadable) && !errors.Is(err, ErrRejected) {
		t.Fatalf("%s: error %v is neither ErrUnreadable nor ErrRejected", name, err)
	}
	if !fuzzDecoderErrorText.MatchString(err.Error()) {
		t.Fatalf("%s: error text %q is not a fixed text", name, err.Error())
	}
	if strings.Contains(err.Error(), fuzzCanary) {
		t.Fatalf("%s: error carries the canary", name)
	}
}

func checkObjects(t *testing.T, name string, objs []Object) {
	t.Helper()
	for _, o := range objs {
		if o.ID == "" {
			t.Fatalf("%s: accepted an object without an id", name)
		}
		c := o.Coverage
		if (c.All || c.Unresolved) && len(c.Namespaces) > 0 || c.All && c.Unresolved {
			t.Fatalf("%s: coverage %+v mixes states", name, c)
		}
		if !c.All && !c.Unresolved && len(c.Namespaces) != 1 {
			t.Fatalf("%s: resolved coverage %+v must name exactly one namespace", name, c)
		}
		for _, ns := range c.Namespaces {
			if strings.TrimSpace(ns) == "" || ns != strings.TrimSpace(ns) {
				t.Fatalf("%s: covered namespace %q is blank or untrimmed", name, ns)
			}
		}
		switch o.Family {
		case FamilyNamespace:
			if o.SetType != "" {
				t.Fatalf("%s: per-namespace object has a set type", name)
			}
		case FamilyProtectionSet:
			// KTD9, re-derived: RSC's answer gives no definition format, so
			// no protection set may claim to be free of cluster-scoped
			// resources (fail closed).
			if !o.MayHoldClusterScoped {
				t.Fatalf("%s: set of type %q claims no cluster-scoped resources", name, o.SetType)
			}
			if !strings.EqualFold(strings.TrimSpace(o.SetType), "namespace") && o.NamespaceScoped() {
				t.Fatalf("%s: set of type %q is an action target", name, o.SetType)
			}
		default:
			t.Fatalf("%s: unknown family %q", name, o.Family)
		}
		if o.Relic && o.Protecting() {
			t.Fatalf("%s: relic counts as protection", name)
		}
	}
}

// FuzzRubrikDecoders fuzzes every decoder that turns an RSC GraphQL answer's
// data into the model. The answers come from an operator-registered tenant
// over the network, so every byte is untrusted.
//
//   - Oracle A: no decoder panics.
//   - Oracle B: every rejection is ErrUnreadable or ErrRejected with a fixed
//     text, so a decode error never echoes the answer into a log.
//   - Oracle D: the canary, planted in decoded and skipped fields alike,
//     never appears in an error.
//   - Oracle C: accepted objects have ids, coherent coverage, and the KTD9
//     rule that no protection set may claim to be free of cluster-scoped
//     resources; relics never count as protection; job states stay within
//     the known set; SLA frequencies are never negative.
func FuzzRubrikDecoders(f *testing.F) {
	// Valid answers, one per decoder.
	f.Add([]byte(`{"k8sClusters":{"nodes":[{"id":"c1","name":"prod","status":"STATUS_CONNECTED"}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}`))
	f.Add([]byte(`{"kubernetesClusters":{"nodes":[{"id":"c2","name":"cdm"}],"pageInfo":{"hasNextPage":true,"endCursor":"x"}}}`))
	f.Add([]byte(`{"k8sNamespaces":{"nodes":[{"id":"n1","namespaceName":"team-a","isRelic":false,"clusterScoped":false,"effectiveSlaDomain":{"id":"gold","name":"Gold"},"newestSnapshot":{"date":"2026-11-13T00:00:00Z"}}],"pageInfo":{"hasNextPage":false}}}`))
	f.Add([]byte(`{"kubernetesProtectionSets":{"nodes":[{"id":"s1","name":"all","rsType":"cluster","definition":"{}"},{"id":"s2","name":"a","rsType":"namespace","namespace":"team-a","definition":"{\"kinds\":[\"ClusterRole\"]}"}]}}`))
	f.Add([]byte(`{"k8sNamespace":{"snapshotConnection":{"nodes":[{"id":"x","date":"2026-11-13T00:00:00Z","expirationDate":null,"isExpired":true}]}}}`))
	f.Add([]byte(`{"kubernetesProtectionSet":{"snapshotConnection":null}}`))
	f.Add([]byte(`{"activitySeriesConnection":{"nodes":[{"activitySeriesId":"j","lastActivityStatus":"Failure","failureReason":"disk full"}]}}`))
	f.Add([]byte(`{"taskchain":{"state":"FAILED","error":"boom"}}`))
	f.Add([]byte(`{"slaDomains":{"nodes":[{"id":"g","name":"Gold","snapshotSchedule":{"hourly":{"basicSchedule":{"frequency":4}},"yearly":{"basicSchedule":{"frequency":9223372036854775807}}}}]}}`))
	f.Add([]byte(`{"createK8sNamespaceSnapshots":[{"jobId":1,"taskchainId":"t"}]}`))
	f.Add([]byte(`{"createK8sProtectionSetSnapshot":{"id":"r","status":"QUEUED"},"restoreK8sProtectionSetSnapshot":{"id":"r2"}}`))
	f.Add([]byte(`{"restoreK8sNamespace":{"jobId":2,"taskchainId":"t2"}}`))
	f.Add([]byte(`{"assignSla":{"success":true}}`))

	// Teeth, each aimed at one guard.
	f.Add([]byte(`null`))                                                                                                                                       // null data: every "no list" guard
	f.Add([]byte(`{}`))                                                                                                                                         // missing roots
	f.Add([]byte(`{"k8sNamespaces":{"nodes":[{"namespaceName":"` + fuzzCanary + `"}]}}`))                                                                       // missing id, canary in a decoded field
	f.Add([]byte(`{"k8sNamespaces":{"nodes":[{"id":"n","namespaceName":"  "}]}}`))                                                                              // blank namespace: unresolved, clusterScoped missing fails closed
	f.Add([]byte(`{"k8sNamespaces":{"nodes":[{"id":"n","newestSnapshot":{"date":"` + fuzzCanary + `"}}]}}`))                                                    // bad time quoting the canary
	f.Add([]byte(`{"kubernetesProtectionSets":{"nodes":[{"id":"s","rsType":" Namespace ","namespace":" team-a "}]}}`))                                          // padded type and namespace
	f.Add([]byte(`{"kubernetesProtectionSets":{"nodes":[{"id":"s","rsType":"cluster","labelSelector":"app=x"}]}}`))                                             // cluster set with a selector: unresolved
	f.Add([]byte(`{"kubernetesProtectionSets":{"nodes":[{"id":"s","rsType":7}]}}`))                                                                             // wrong type, whose text names the field
	f.Add([]byte(`{"kubernetesProtectionSets":{"nodes":[{"id":"s","rsType":"namespace","namespace":"a","isRelic":true,"effectiveSlaDomain":{"id":"gold"}}]}}`)) // relic with a real SLA
	f.Add([]byte(`{"k8sNamespace":{"snapshotConnection":{"nodes":[{"id":"x"}]}}}`))                                                                             // snapshot without a time
	f.Add([]byte(`{"k8sNamespace":null}`))                                                                                                                      // object missing
	f.Add([]byte(`{"activitySeriesConnection":{"nodes":[{"activitySeriesId":"j","lastActivityStatus":"` + fuzzCanary + `"}]}}`))                                // unknown status
	f.Add([]byte(`{"taskchain":{"state":"SUCCEEDED","error":"stale"}}`))                                                                                        // error on success is dropped
	f.Add([]byte(`{"slaDomains":{"nodes":[{"id":"g","snapshotSchedule":{"daily":{"basicSchedule":{"frequency":-5}}}}]}}`))                                      // negative frequency
	f.Add([]byte(`{"createK8sNamespaceSnapshots":[]}`))                                                                                                         // no job
	f.Add([]byte(`{"createK8sNamespaceSnapshots":[{},{}]}`))                                                                                                    // two jobs
	f.Add([]byte(`{"createK8sProtectionSetSnapshot":"` + fuzzCanary + `"}`))                                                                                    // reply of the wrong type
	f.Add([]byte(`{"assignSla":{"success":false}}`))                                                                                                            // rejected
	f.Add([]byte(`{"assignSla":{"success":"` + fuzzCanary + `"}}`))                                                                                             // wrong-typed success quoting the canary
	f.Add([]byte(`{"k8sClusters":{"nodes":[{"id":"c","access_token":"` + fuzzCanary + `"}]}}`))                                                                 // canary in a skipped field
	f.Add([]byte("{\"k8sNamespaces\":{\"nodes\":[{\"id\":\"\xff\xfe\"}]}}"))                                                                                    // invalid UTF-8
	f.Add([]byte(strings.Repeat(`{"a":`, 2048)))                                                                                                                // deep nesting

	f.Fuzz(func(t *testing.T, data []byte) {
		pages := map[string]func([]byte) ([]Object, pageInfo, error){
			"namespaces":      decodeK8sNamespacesPage,
			"protection sets": decodeProtectionSetsPage,
		}
		for name, decode := range pages {
			objs, _, err := decode(data)
			if err != nil {
				checkDecoderError(t, name, err)
				continue
			}
			checkObjects(t, name, objs)
		}

		for name, decode := range map[string]func([]byte) ([]Cluster, pageInfo, error){
			"k8s clusters":        decodeK8sClustersPage,
			"kubernetes clusters": decodeKubernetesClustersPage,
		} {
			clusters, _, err := decode(data)
			if err != nil {
				checkDecoderError(t, name, err)
				continue
			}
			for _, c := range clusters {
				if c.ID == "" {
					t.Fatalf("%s: accepted a cluster without an id", name)
				}
			}
		}

		for name, decode := range map[string]func([]byte) ([]Snapshot, pageInfo, error){
			"namespace snapshots": decodeNamespaceSnapshotsPage,
			"set snapshots":       decodeProtectionSetSnapshotsPage,
		} {
			snaps, _, err := decode(data)
			if err != nil {
				checkDecoderError(t, name, err)
				continue
			}
			for _, s := range snaps {
				if s.ID == "" || s.Time.IsZero() && !strings.Contains(string(data), "0001-01-01") {
					t.Fatalf("%s: accepted a snapshot without an id or time", name)
				}
			}
		}

		if jobs, _, err := decodeActivityPage(data); err != nil {
			checkDecoderError(t, "jobs", err)
		} else {
			for _, j := range jobs {
				if j.ID == "" || !fuzzJobStatuses[j.Status] {
					t.Fatalf("jobs: accepted job %q with status %q", j.ID, j.Status)
				}
				if j.FailureReason != "" && j.Status != JobFailed && j.Status != JobPartial {
					t.Fatalf("jobs: failure reason on a %q job", j.Status)
				}
			}
		}

		if st, err := decodeTaskchain(data); err != nil {
			checkDecoderError(t, "taskchain", err)
		} else if !fuzzJobStatuses[st.Status] || st.Error != "" && st.Status != JobFailed {
			t.Fatalf("taskchain: %+v", st)
		}

		if domains, _, err := decodeSLADomainsPage(data); err != nil {
			checkDecoderError(t, "sla domains", err)
		} else {
			for _, d := range domains {
				if d.ID == "" || d.Frequency < 0 {
					t.Fatalf("sla domains: accepted %+v", d)
				}
				// Compliance over any decoded frequency never panics and
				// stays within the three judgments.
				now := time.Unix(1_800_000_000, 0)
				switch EvaluateCompliance(&now, d.Frequency, now.Add(d.Frequency)) {
				case ComplianceInCompliance, ComplianceOutOfCompliance, ComplianceUnknown:
				default:
					t.Fatal("sla domains: compliance outside the three judgments")
				}
			}
		}

		handles := map[string]func([]byte) (JobHandle, error){
			"namespace snapshot": decodeNamespaceSnapshotReply,
			"restore namespace":  decodeRestoreNamespaceReply,
			"set snapshot":       func(d []byte) (JobHandle, error) { return decodeAsyncReply(d, "createK8sProtectionSetSnapshot") },
			"set restore":        func(d []byte) (JobHandle, error) { return decodeAsyncReply(d, "restoreK8sProtectionSetSnapshot") },
		}
		for name, decode := range handles {
			h, err := decode(data)
			if err != nil {
				checkDecoderError(t, name, err)
				continue
			}
			if h.TaskchainID == "" && h.RequestID == "" {
				t.Fatalf("%s: accepted a reply with no job handle", name)
			}
		}

		if err := decodeAssignSLAReply(data); err != nil {
			checkDecoderError(t, "assign sla", err)
		}
	})
}

// Re-derived from client.go, so the oracle notices a cap moving instead of
// agreeing with it.
const (
	fuzzMaxRetryAfter      = time.Hour
	fuzzMaxGraphQLMessages = 5
	fuzzMaxMessageLen      = 300
)

var fuzzTokenShape = regexp.MustCompile(`^[A-Za-z0-9._-]{8,}$`)

// FuzzClientAnswerParsers fuzzes the client's parsers of tenant-controlled
// answer parts: the Retry-After header and GraphQL error messages.
//
//   - Oracle A: neither panics.
//   - Oracle B: a Retry-After delay is within [0, 1h].
//   - Oracle D: an access token of the shape RSC issues never survives in a
//     GraphQL error, and at most five messages of at most 300 bytes are kept.
func FuzzClientAnswerParsers(f *testing.F) {
	f.Add("120", "token "+fuzzCanary+" expired", fuzzCanary, 1)
	f.Add("Wed, 21 Oct 2015 07:28:00 GMT", "", "eyJhbGciOiJIUzI1NiJ9.e30.sig", 6)
	f.Add("-1", strings.Repeat("x", 400)+fuzzCanary, fuzzCanary, 3)
	f.Add("99999999999999999999", fuzzCanary+fuzzCanary, fuzzCanary, 2)
	f.Add("9223372036", strings.Repeat("é", 200), "", 0)
	f.Add(" 0 ", "", "short", 9)

	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, retryAfter, message, token string, count int) {
		if d := parseRetryAfter(retryAfter, now); d < 0 || d > fuzzMaxRetryAfter {
			t.Fatalf("parseRetryAfter(%q) = %s, outside [0, 1h]", retryAfter, d)
		}

		count = max(0, min(count, 20))
		var ans graphqlAnswer
		for range count {
			ans.Errors = append(ans.Errors, struct {
				Message string `json:"message"`
			}{Message: message})
		}
		e := graphQLError(ans, token)
		if len(e.Messages) > fuzzMaxGraphQLMessages || len(e.Messages) != min(count, fuzzMaxGraphQLMessages) {
			t.Fatalf("kept %d messages of %d", len(e.Messages), count)
		}
		for _, m := range e.Messages {
			if len(m) > fuzzMaxMessageLen {
				t.Fatalf("message of %d bytes kept", len(m))
			}
			// A token that is a piece of the replacement text itself
			// ("redacted") is not a token RSC can issue.
			if fuzzTokenShape.MatchString(token) && !strings.Contains("[redacted]", token) && strings.Contains(m, token) {
				t.Fatalf("token survived in %q", m)
			}
		}
		if !errors.Is(e, ErrGraphQL) {
			t.Fatal("GraphQLError is not ErrGraphQL")
		}
	})
}
