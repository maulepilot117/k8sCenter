package velero

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/store"
)

// fuzzCanary is planted in every BackupStorageLocation's controller message
// and bucket. Oracle D: it may surface only in Detail's privileged fields.
const fuzzCanary = "FUZZ-CANARY-arn:aws:s3:::secret-bucket"

// FuzzAssuranceEvaluate asserts Evaluate is crash-safe and keeps its
// contract on arbitrary CRD-derived input. The fuzz object goes through the
// real parsers (parseBackup, parseSchedule, parseBSL) first, so the corpus
// exercises the production path, including parseSchedule's next-run
// computation over an attacker-chosen cron string.
//
// Oracle A: no panic. Contract: deterministic; every finding carries a valid
// condition, severity, subject kind and outcome; a failed collection yields
// at most one cluster-scoped collection_unknown; Hold only on overdue or
// never_run; one finding per exception identity. Oracle D: the planted
// controller text never escapes Detail's privileged fields.
func FuzzAssuranceEvaluate(f *testing.F) {
	// Realistic schedule-shaped object: a schedule, and a run of itself.
	f.Add([]byte(`{
		"metadata": {"name": "daily", "namespace": "velero", "uid": "u-1",
			"creationTimestamp": "2026-08-01T00:00:00Z",
			"labels": {"velero.io/schedule-name": "daily"}},
		"spec": {"schedule": "0 3 * * *", "storageLocation": "default",
			"includedNamespaces": ["app"],
			"template": {"includedNamespaces": ["app"], "storageLocation": "default"}},
		"status": {"phase": "Completed", "lastBackup": "2026-09-10T03:00:00Z",
			"startTimestamp": "2026-09-08T03:00:00Z", "completionTimestamp": "2026-09-08T03:05:00Z",
			"message": "access denied"}
	}`), uint8(0), uint32(86400), uint32(3600), uint8(0), int32(0))

	// PartiallyFailed, paused, treat-partial-as-success, alert on paused.
	f.Add([]byte(`{
		"metadata": {"name": "weekly", "namespace": "velero"},
		"spec": {"schedule": "CRON_TZ=America/New_York 30 1 * * 1", "paused": true},
		"status": {"phase": "PartiallyFailed", "completionTimestamp": "2026-09-07T05:40:00Z"}
	}`), uint8(0), uint32(300), uint32(0), uint8(0b0011), int32(-86400))

	// Teeth: robfig/cron v3.0.1 panics on a zone prefix with no schedule;
	// parseCron must reject it before robfig sees it (parseSchedule reaches
	// it through computeNextRun when the phase is Enabled).
	f.Add([]byte(`{"metadata":{"name":"s","namespace":"velero"},"spec":{"schedule":"CRON_TZ=UTC"},"status":{"phase":"Enabled"}}`),
		uint8(0), uint32(86400), uint32(3600), uint8(0), int32(0))
	f.Add([]byte(`{"spec":{"schedule":"TZ=Asia/Tokyo"},"status":{"phase":"Enabled"}}`),
		uint8(1), uint32(86400), uint32(3600), uint8(0), int32(0))

	// Teeth: an expression with no fire time and an unbounded walk.
	f.Add([]byte(`{"metadata":{"name":"x","namespace":"velero"},"spec":{"schedule":"0 0 30 2 *"}}`),
		uint8(0), uint32(300), uint32(0), uint8(0), int32(0))
	f.Add([]byte(`{"metadata":{"name":"x","namespace":"velero"},"spec":{"schedule":"@every 1s"},"status":{"phase":"Completed","startTimestamp":"2026-01-01T00:00:00Z"}}`),
		uint8(0), uint32(300), uint32(0), uint8(0), int32(0))

	// Failed and degraded collections, undetected Velero.
	f.Add([]byte(`{"metadata":{"name":"daily","namespace":"velero"},"status":{"phase":"Failed"}}`),
		uint8(2), uint32(86400), uint32(3600), uint8(0b0100), int32(0))
	f.Add([]byte(`{"metadata":{"name":"daily","namespace":"velero"},"status":{"phase":"Failed"}}`),
		uint8(1), uint32(86400), uint32(3600), uint8(0b1000), int32(0))
	f.Add([]byte(`{"metadata":{"name":"daily","namespace":"velero"}}`),
		uint8(0), uint32(86400), uint32(3600), uint8(0b110000), int32(0))

	// Malformed shapes (the normalizer teeth from parsers_fuzz_test.go).
	f.Add([]byte(`{}`), uint8(0), uint32(0), uint32(0), uint8(0), int32(0))
	f.Add([]byte(`{"metadata":"oops"}`), uint8(0), uint32(0), uint32(0), uint8(0), int32(0))
	f.Add([]byte(`{"spec":[],"status":"x"}`), uint8(0), uint32(0), uint32(0), uint8(0), int32(0))
	f.Add([]byte(`{"spec":{"includedNamespaces":42,"template":"x"},"status":{"completionTimestamp":"not-a-time"}}`),
		uint8(1), uint32(1), uint32(1), uint8(0xff), int32(1<<30))

	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	f.Fuzz(func(t *testing.T, data []byte, scope uint8, maxAgeSec, graceSec uint32, flags uint8, nowOffsetSec int32) {
		u, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}
		backup := parseBackup(u)
		schedule := parseSchedule(u)
		bsl := parseBSL(u)
		bsl.Message, bsl.Bucket = fuzzCanary, fuzzCanary

		// A run that certainly matches the schedule, beside the raw backup.
		run := backup
		run.Namespace, run.ScheduleName = schedule.Namespace, scheduleLabelValue(schedule.Name)
		sameNS := bsl
		sameNS.Namespace = schedule.Namespace

		collections := [...]Collection{CollectionOK, CollectionDegraded, CollectionFailed, ""}
		obs := Observation{
			ClusterID:  testCluster,
			Collection: collections[(flags>>2)&3],
			Status:     VeleroStatus{Detected: flags&0b1000_0000 == 0},
			Backups:    []Backup{backup, run},
			Schedules:  []Schedule{schedule},
			Locations:  &LocationsResponse{BackupStorageLocations: []BackupStorageLocation{bsl, sameNS}},
		}
		if flags&0b1_0000 != 0 {
			obs.FailedLists = append(obs.FailedLists, BackupGVR.Resource)
		}
		if flags&0b10_0000 != 0 {
			obs.FailedLists = append(obs.FailedLists, ScheduleGVR.Resource)
		}

		mk := func(i int, kind store.AssuranceScopeKind, ns, name string) store.BackupAssurancePolicy {
			p := store.BackupAssurancePolicy{
				ID: policyIDs[i], ClusterID: testCluster, ScopeKind: kind,
				ScopeNamespace: ns, ScopeName: name,
				MaxAge: time.Duration(maxAgeSec) * time.Second, Grace: time.Duration(graceSec) * time.Second,
				TreatPartialAs: store.AssuranceTreatPartialAsFailure,
				AlertOnPaused:  flags&0b10 != 0, Enabled: true,
			}
			if flags&1 != 0 {
				p.TreatPartialAs = store.AssuranceTreatPartialAsSuccess
			}
			return p
		}
		ns := schedule.Namespace
		if len(backup.IncludedNamespaces) > 0 {
			ns = backup.IncludedNamespaces[0]
		}
		all := []store.BackupAssurancePolicy{
			mk(0, store.ScopeSchedule, schedule.Namespace, schedule.Name),
			mk(1, store.ScopeNamespace, ns, ""),
			mk(2, store.ScopeCluster, "", ""),
		}
		policies := all[:1+int(scope)%3]
		known := map[uuid.UUID]bool{}
		for _, p := range policies {
			known[p.ID] = true
		}

		now := base.Add(time.Duration(nowOffsetSec) * time.Second)
		got := Evaluate(obs, policies, now)

		if again := Evaluate(obs, policies, now); !reflect.DeepEqual(got, again) {
			t.Fatalf("Evaluate is not deterministic:\n%+v\n%+v", got, again)
		}

		failed := (obs.Collection != CollectionOK && obs.Collection != CollectionDegraded) || !obs.Status.Detected
		if failed && (len(got) > 1 || len(got) == 1 && (got[0].Condition != store.ConditionCollectionUnknown || got[0].Subject.Kind != store.ScopeCluster)) {
			t.Fatalf("failed collection produced %+v, want at most one cluster collection_unknown", got)
		}

		type identity struct {
			s Subject
			c store.AssuranceCondition
		}
		seen := map[identity]bool{}
		for _, fd := range got {
			if !fd.Condition.Valid() || !fd.Subject.Kind.Valid() || !known[fd.PolicyID] {
				t.Fatalf("malformed finding %+v", fd)
			}
			switch fd.Severity {
			case store.AssuranceSeverityInfo, store.AssuranceSeverityWarning, store.AssuranceSeverityCritical:
			default:
				t.Fatalf("finding %+v has severity %q", fd, fd.Severity)
			}
			switch fd.Detail.LastOutcome {
			case OutcomeSuccess, OutcomePartial, OutcomeFailure, OutcomeInFlight, OutcomeUnknown:
			default:
				t.Fatalf("finding %+v has outcome %q", fd, fd.Detail.LastOutcome)
			}
			if fd.Hold && fd.Condition != store.ConditionOverdue && fd.Condition != store.ConditionNeverRun {
				t.Fatalf("Hold on %s", fd.Condition)
			}
			id := identity{fd.Subject, fd.Condition}
			if seen[id] {
				t.Fatalf("duplicate identity %+v", id)
			}
			seen[id] = true

			public := fd
			public.Detail.StorageLocation, public.Detail.BSLMessage, public.Detail.FailureReason = "", "", ""
			b, err := json.Marshal(public)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// Skipped only if the input itself spells the canary (it could
			// then legitimately be a schedule name).
			if strings.Contains(string(b), fuzzCanary) && !strings.Contains(string(data), fuzzCanary) {
				t.Fatalf("controller text escaped the privileged detail fields: %s", b)
			}
		}
	})
}
