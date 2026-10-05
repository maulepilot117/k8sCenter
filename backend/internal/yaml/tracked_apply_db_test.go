package yaml

// tracked_apply_db_test.go — Release E U30a end to end: the yaml handler, a
// real changes.Service and the real store.ChangeReceiptStore over PostgreSQL.
// These prove the durable-intent protocol (plan D3) and the idempotency
// branches (D4) where they actually live: in the database.
//
// Gated exactly like the store package's harness: skipped when
// KUBECENTER_TEST_DATABASE_URL is unset, a failure when
// KUBECENTER_TEST_REQUIRE_DATABASE is set (as in CI). Every test acts as an
// owner id unique to it, so rows written here are invisible to other suites.
//
//	KUBECENTER_TEST_DATABASE_URL='postgresql://k8scenter:k8scenter@127.0.0.1:5432/<scratch db>?sslmode=disable' \
//	  go test ./internal/yaml/ -run DB

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/changes"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	testDatabaseURLEnv     = "KUBECENTER_TEST_DATABASE_URL"
	testDatabaseRequireEnv = "KUBECENTER_TEST_REQUIRE_DATABASE"
)

// testDatabaseRequired mirrors the canonical predicate in the store package's
// harness (backend/internal/store/testdb_test.go) exactly, so the two gates
// never disagree about whether a missing database is a skip or a failure.
func testDatabaseRequired(lookup func(string) (string, bool)) bool {
	v, ok := lookup(testDatabaseRequireEnv)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// The gate's own truthiness rules are tested without the gate, so a broken
// predicate cannot hide behind the skip it controls.
func TestTestDatabaseRequired_Truthiness(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  bool
	}{
		{"unset", "", false, false},
		{"empty", "", true, false},
		{"zero", "0", true, false},
		{"false", "false", true, false},
		{"FALSE padded", "  FALSE ", true, false},
		{"no", "no", true, false},
		{"one", "1", true, true},
		{"true", "true", true, true},
		{"yes", "yes", true, true},
		{"typo", "flase", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(key string) (string, bool) {
				if key != testDatabaseRequireEnv {
					t.Fatalf("looked up %q; want %q", key, testDatabaseRequireEnv)
				}
				return tc.value, tc.set
			}
			if got := testDatabaseRequired(lookup); got != tc.want {
				t.Errorf("testDatabaseRequired(%q, set=%v) = %v; want %v", tc.value, tc.set, got, tc.want)
			}
		})
	}
}

// openTestDB opens a new pool over the migrated test database (migrations run
// through the production entry point), or skips the calling test when none is
// configured. Each call is its own pool, so a test may close one mid-apply.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	connString := strings.TrimSpace(os.Getenv(testDatabaseURLEnv))
	if connString == "" {
		if testDatabaseRequired(os.LookupEnv) {
			t.Fatalf("%s is set but %s is empty; a database was required", testDatabaseRequireEnv, testDatabaseURLEnv)
		}
		t.Skipf("%s is not set; skipping PostgreSQL-backed tracked apply test", testDatabaseURLEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.New(ctx, connString, 4, 1, discardLogger)
	if err != nil {
		t.Fatalf("connecting to %s: %v", testDatabaseURLEnv, err)
	}
	t.Cleanup(db.Close)
	return db
}

// uniqueUser returns an authenticated user whose id belongs to this test only.
func uniqueUser(t *testing.T) *auth.User {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generating owner suffix: %v", err)
	}
	id := "test-u30a-" + hex.EncodeToString(suffix[:])
	return &auth.User{
		ID: id, Username: id, Provider: "local",
		KubernetesUsername: "alice", KubernetesGroups: []string{"system:authenticated"},
	}
}

// trackedEnv is the remote+local fixture (both clusters stateful) with a
// DB-backed changes service.
type trackedEnv struct {
	*fixture
	db       *store.DB
	receipts *store.ChangeReceiptStore
	audit    *recordingAuditLogger
	user     *auth.User
}

func newTrackedEnv(t *testing.T) *trackedEnv {
	t.Helper()
	db := openTestDB(t)
	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)
	makeStateful(fx.local.dyn)
	env := &trackedEnv{
		fixture:  fx,
		db:       db,
		receipts: store.NewChangeReceiptStore(db.Pool),
		audit:    &recordingAuditLogger{},
		user:     uniqueUser(t),
	}
	fx.handler.Changes = changes.NewService(env.receipts, discardLogger)
	fx.handler.AuditLogger = env.audit
	return env
}

// applyAs posts body to /yaml/apply as user against clusterID.
func (e *trackedEnv) applyAs(user *auth.User, clusterID string, params map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, applyURL(params), strings.NewReader(body))
	r = r.WithContext(middleware.WithClusterID(auth.ContextWithUser(r.Context(), user), clusterID))
	return serve(e.handler.HandleApply, r)
}

func (e *trackedEnv) applyTracked(opID uuid.UUID, clusterID, body string) *httptest.ResponseRecorder {
	return e.applyAs(e.user, clusterID, trackedQuery(opID.String()), body)
}

// trackedApplyBody decodes the tracked wire shape.
type trackedApplyBody struct {
	Results  []ApplyResult          `json:"results"`
	Summary  ApplySummary           `json:"summary"`
	Tracking *changes.ApplyTracking `json:"tracking"`
}

func decodeTracked(t *testing.T, w *httptest.ResponseRecorder) trackedApplyBody {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	b := decodeData[trackedApplyBody](t, w)
	if b.Tracking == nil {
		t.Fatalf("tracked response has no tracking block: %s", w.Body.String())
	}
	return b
}

func (e *trackedEnv) receipt(t *testing.T, id uuid.UUID) *store.ChangeReceipt {
	t.Helper()
	r, err := e.receipts.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get receipt %s: %v", id, err)
	}
	return r
}

func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// --- Additive response --------------------------------------------------------

// The tracked response adds exactly one key, carries the same results and
// summary an untracked apply of the same bundle produces, and records the
// RESOLVED target (cluster id and generation) on both the wire and the row.
func TestHandleApplyDB_TrackedAddsOnlyTrackingKey(t *testing.T) {
	body := bundle(widgetDoc("a"), widgetDoc("b"))

	untracked := newFixture(t, nil, nil)
	makeStateful(untracked.remoteDyn)
	legacy := decodeData[trackedApplyBody](t, untracked.apply(remoteClusterID, nil, body))

	e := newTrackedEnv(t)
	opID := uuid.New()
	w := e.applyTracked(opID, remoteClusterID, body)
	if got := dataKeys(t, w.Body.Bytes()); !slices.Equal(got, []string{"results", "summary", "tracking"}) {
		t.Fatalf("tracked data keys = %v; want exactly [results summary tracking]", got)
	}
	got := decodeTracked(t, w)
	if mustJSON(t, got.Results) != mustJSON(t, legacy.Results) || got.Summary != legacy.Summary {
		t.Fatalf("tracked results/summary differ from untracked:\n tracked:   %s %+v\n untracked: %s %+v",
			mustJSON(t, got.Results), got.Summary, mustJSON(t, legacy.Results), legacy.Summary)
	}

	// The wire contract U30b and U31 build on: exactly these keys (repairOf
	// is omitted when there is none).
	var raw struct {
		Data struct {
			Tracking map[string]json.RawMessage `json:"tracking"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range raw.Data.Tracking {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	wantKeys := []string{"clusterGeneration", "clusterId", "containsSecret", "contentDigest", "notAttempted",
		"objects", "operationId", "receiptUrl", "recordedThrough", "replayed", "state", "unrecorded", "verification", "warnings"}
	if !slices.Equal(keys, wantKeys) {
		t.Errorf("tracking keys = %v; want %v", keys, wantKeys)
	}

	tr := got.Tracking
	want := changes.ApplyTracking{
		OperationID:       opID.String(),
		ReceiptURL:        "/v1/changes/" + opID.String(),
		State:             store.ReceiptApplied,
		ClusterID:         remoteClusterID,
		ClusterGeneration: remoteGeneration,
		ContentDigest:     digestOf(body),
		RecordedThrough:   2,
		Objects: []changes.TrackedObjectRef{
			{Index: 0, Group: "example.com", Version: "v1", Resource: "widgets", UID: "uid-a-1"},
			{Index: 1, Group: "example.com", Version: "v1", Resource: "widgets", UID: "uid-b-2"},
		},
		Verification: changes.VerificationLink{State: store.VerifyPending, URL: "/v1/changes/" + opID.String() + "/verification"},
		Warnings:     []string{},
	}
	if !reflect.DeepEqual(*tr, want) {
		t.Errorf("tracking = %+v\nwant       %+v", *tr, want)
	}

	row := e.receipt(t, opID)
	if row == nil || row.OwnerID != e.user.ID || row.ClusterID != remoteClusterID || row.ClusterGeneration != remoteGeneration ||
		row.State != store.ReceiptApplied || row.DocumentCount != 2 || row.ContentDigest != digestOf(body) || len(row.Objects) != 2 {
		t.Fatalf("receipt row = %+v; want the resolved remote target, applied, 2 objects", row)
	}
	e.fixture.local.assertUntouched(t)
}

// AE7: two successes and one failure on the local cluster. The summary and
// the receipt agree object for object, and the state is partial.
func TestHandleApplyDB_TrackedTwoSuccessOneFailure(t *testing.T) {
	e := newTrackedEnv(t)
	rejectPatch(e.fixture.local.dyn, "bad", func(clienttesting.PatchAction) error {
		return apierrors.NewInvalid(schema.GroupKind{Kind: "ConfigMap"}, "bad",
			field.ErrorList{field.Invalid(field.NewPath("data"), "x", "rejected by admission")})
	})
	cm := func(name string) string { return strings.Replace(configMapYAML, "name: settings", "name: "+name, 1) }
	opID := uuid.New()

	got := decodeTracked(t, e.applyTracked(opID, "local", bundle(cm("one"), cm("bad"), cm("two"))))

	if got.Summary != (ApplySummary{Total: 3, Created: 2, Failed: 1}) {
		t.Errorf("summary = %+v; want 2 created, 1 failed of 3", got.Summary)
	}
	if got.Tracking.State != store.ReceiptPartial {
		t.Errorf("tracking.state = %q; want partial", got.Tracking.State)
	}
	if got.Tracking.ClusterID != "local" || got.Tracking.ClusterGeneration != "local" {
		t.Errorf("tracking target = %q/%q; want local/local", got.Tracking.ClusterID, got.Tracking.ClusterGeneration)
	}
	row := e.receipt(t, opID)
	if row.State != store.ReceiptPartial || len(row.Objects) != 3 {
		t.Fatalf("receipt = %+v; want partial with 3 recorded objects", row)
	}
	for i, o := range row.Objects {
		r := got.Results[i]
		if o.Index != r.Index || o.Action != r.Action || o.Name != r.Name || o.Namespace != r.Namespace || o.Kind != r.Kind {
			t.Errorf("receipt object %d = %+v; disagrees with result %+v", i, o, r)
		}
	}
	if o := row.Objects[1]; o.ErrorClass != changes.ErrorClassInvalid || !strings.Contains(o.Error, "rejected by admission") {
		t.Errorf("failed object = %+v; want class invalid with the (non-Secret) error kept", o)
	}
	if o := row.Objects[0]; o.Resource != "configmaps" || o.Version != "v1" || o.UID == "" {
		t.Errorf("recorded object = %+v; want the configmaps mapping and the applied UID", o)
	}
}

// --- Idempotency (D4) -----------------------------------------------------------

// A dropped response followed by a client retry under the same operation id
// replays the recorded outcome. Nothing reaches the cluster a second time and
// nothing is audited a second time.
func TestHandleApplyDB_DroppedResponseRetrySameOperationID_NoDuplicateApply(t *testing.T) {
	e := newTrackedEnv(t)
	rejectPatch(e.remoteDyn, "denied", func(clienttesting.PatchAction) error {
		return apierrors.NewForbidden(widgetGVR.GroupResource(), "denied", errors.New("user cannot patch"))
	})
	body := bundle(widgetDoc("a"), widgetDoc("denied"), widgetDoc("b"))
	opID := uuid.New()

	first := decodeTracked(t, e.applyTracked(opID, remoteClusterID, body))
	patches, audits := patchCount(e.remoteDyn), len(e.audit.snapshot())
	if patches != 3 || audits != 3 {
		t.Fatalf("first apply: %d patches, %d audit entries; want 3 and 3", patches, audits)
	}

	second := decodeTracked(t, e.applyTracked(opID, remoteClusterID, body))

	if got := patchCount(e.remoteDyn); got != patches {
		t.Fatalf("patches after retry = %d; want %d — a retried operation id must never apply again", got, patches)
	}
	if got := len(e.audit.snapshot()); got != audits {
		t.Errorf("audit entries after replay = %d; want %d — a replay applied nothing", got, audits)
	}
	if !second.Tracking.Replayed || first.Tracking.Replayed {
		t.Errorf("replayed = %v then %v; want false then true", first.Tracking.Replayed, second.Tracking.Replayed)
	}
	if mustJSON(t, second.Results) != mustJSON(t, first.Results) || second.Summary != first.Summary {
		t.Errorf("replay differs from the original:\n first:  %s %+v\n replay: %s %+v",
			mustJSON(t, first.Results), first.Summary, mustJSON(t, second.Results), second.Summary)
	}
	if second.Tracking.State != store.ReceiptPartial || second.Tracking.OperationID != opID.String() {
		t.Errorf("replay tracking = %+v; want the stored partial receipt", second.Tracking)
	}
}

func TestHandleApplyDB_SameIDDifferentContentOrCluster_Returns409Reused(t *testing.T) {
	e := newTrackedEnv(t)
	opID := uuid.New()
	decodeTracked(t, e.applyTracked(opID, remoteClusterID, widgetDoc("a")))
	before := patchCount(e.remoteDyn)

	for _, tc := range []struct {
		name, cluster, body string
	}{
		{"different content", remoteClusterID, widgetDocSized("a", 9)},
		{"different cluster", "local", widgetDoc("a")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := e.applyTracked(opID, tc.cluster, tc.body)
			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d; want 409, body=%s", w.Code, w.Body.String())
			}
			if r := decodeError(t, w).Error.Reason; r != changes.ReasonOperationIDReused {
				t.Errorf("reason = %q; want %q", r, changes.ReasonOperationIDReused)
			}
		})
	}
	if got := patchCount(e.remoteDyn); got != before {
		t.Errorf("remote patches = %d; want %d", got, before)
	}
	if got := patchCount(e.fixture.local.dyn); got != 0 {
		t.Errorf("local patches = %d; want 0", got)
	}
	// Each refusal is audited once, like a pin refusal, with the id.
	var refusals []string
	for _, a := range e.audit.snapshot() {
		if a.ResourceName == "" {
			refusals = append(refusals, a.Detail)
		}
	}
	want := changes.ReasonOperationIDReused + " op=" + opID.String()
	if len(refusals) != 2 || refusals[0] != want || refusals[1] != want {
		t.Errorf("refusal audit details = %v; want two of %q", refusals, want)
	}
}

// Another owner's operation id is refused without disclosing anything about
// that owner's receipt.
func TestHandleApplyDB_OtherOwnersOperationID_Returns409WithoutDisclosure(t *testing.T) {
	e := newTrackedEnv(t)
	opID := uuid.New()
	body := widgetDoc("a")
	decodeTracked(t, e.applyTracked(opID, remoteClusterID, body))
	before := patchCount(e.remoteDyn)

	mallory := uniqueUser(t)
	w := e.applyAs(mallory, remoteClusterID, trackedQuery(opID.String()), body)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409, body=%s", w.Code, w.Body.String())
	}
	errBody := decodeError(t, w)
	if errBody.Error.Reason != changes.ReasonOperationIDConflict || errBody.Error.Extra != nil {
		t.Errorf("error = %+v; want operation_id_conflict with no extra", errBody.Error)
	}
	for _, secret := range []string{e.user.ID, remoteClusterID, digestOf(body), "applied"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("409 body %s discloses %q from the other owner's receipt", w.Body.String(), secret)
		}
	}
	if got := patchCount(e.remoteDyn); got != before {
		t.Errorf("patches = %d; want %d", got, before)
	}
}

func TestHandleApplyDB_ForceConflictStillReportsConflictMessage(t *testing.T) {
	e := newTrackedEnv(t)
	rejectPatch(e.remoteDyn, "gizmo", func(p clienttesting.PatchAction) error {
		if o := p.(clienttesting.PatchActionImpl).GetPatchOptions(); o.Force != nil && *o.Force {
			return nil
		}
		return apierrors.NewConflict(widgetGVR.GroupResource(), "gizmo",
			errors.New(`Apply failed with 1 conflict: conflict with "kubectl" using example.com/v1: .spec.size`))
	})

	opID := uuid.New()
	got := decodeTracked(t, e.applyTracked(opID, remoteClusterID, widgetYAML))
	if r := got.Results[0]; r.Action != "failed" || !strings.Contains(r.Error, "Use force to override") {
		t.Fatalf("result = %+v; want the legacy conflict message with the force hint", r)
	}
	if got.Tracking.State != store.ReceiptFailed {
		t.Errorf("state = %q; want failed", got.Tracking.State)
	}
	if o := e.receipt(t, opID).Objects[0]; o.ErrorClass != changes.ErrorClassConflict {
		t.Errorf("recorded class = %q; want conflict", o.ErrorClass)
	}

	// Forcing is a new attempt: a new operation id.
	forcedID := uuid.New()
	w := e.applyAs(e.user, remoteClusterID, map[string]string{"trackedOperationId": forcedID.String(), "force": "true"}, widgetYAML)
	forced := decodeTracked(t, w)
	if r := forced.Results[0]; r.Action == "failed" {
		t.Fatalf("forced result = %+v; want success", r)
	}
	if row := e.receipt(t, forcedID); !row.Force || row.State != store.ReceiptApplied {
		t.Errorf("forced receipt = %+v; want force recorded and applied", row)
	}
}

// --- Secrets ----------------------------------------------------------------------

// No request content reaches the store: not the Secret's values (plain or
// base64), not even when the API server echoes one back in an error. The
// live response still carries that raw error to the submitter, as today.
func TestHandleApplyDB_SecretManifestStoresNoContent(t *testing.T) {
	e := newTrackedEnv(t)
	disc := e.targeter.schema.Discovery.(*fakediscovery.FakeDiscovery)
	disc.Resources = append(disc.Resources, &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{Name: "secrets", SingularName: "secret", Namespaced: true, Kind: "Secret"}},
	})
	e.targeter.schema = remoteSchemaFrom(t, disc)

	const plainValue = "u30a-plaintext-Zq81-do-not-store"
	const dataValue = "u30a-datafield-Kx27-do-not-store"
	encoded := base64.StdEncoding.EncodeToString([]byte(dataValue))
	secret := func(name string) string {
		return "apiVersion: v1\nkind: Secret\nmetadata:\n  name: " + name + "\n  namespace: team-a\n" +
			"stringData:\n  password: " + plainValue + "\ndata:\n  token: " + encoded + "\n"
	}
	rejectPatch(e.remoteDyn, "leaky", func(clienttesting.PatchAction) error {
		return apierrors.NewInvalid(schema.GroupKind{Kind: "Secret"}, "leaky",
			field.ErrorList{field.Invalid(field.NewPath("stringData", "password"), plainValue, "must not look like that")})
	})
	opID := uuid.New()

	got := decodeTracked(t, e.applyTracked(opID, remoteClusterID, bundle(secret("creds"), secret("leaky"))))

	if !strings.Contains(got.Results[1].Error, plainValue) {
		t.Fatalf("live error = %q; want the raw admission message returned to the submitter, as today", got.Results[1].Error)
	}
	if !got.Tracking.ContainsSecret {
		t.Error("tracking.containsSecret = false; want true")
	}

	var rowText string
	if err := e.db.Pool.QueryRow(context.Background(),
		`SELECT row_to_json(c)::text FROM change_receipts c WHERE id = $1`, opID).Scan(&rowText); err != nil {
		t.Fatalf("reading the stored row: %v", err)
	}
	for _, v := range []string{plainValue, dataValue, encoded} {
		if strings.Contains(rowText, v) {
			t.Errorf("stored receipt contains %q: %s", v, rowText)
		}
	}
	row := e.receipt(t, opID)
	if !row.ContainsSecret || row.Objects[1].ErrorClass != changes.ErrorClassInvalid ||
		!strings.Contains(row.Objects[1].Error, "withheld") {
		t.Errorf("receipt = %+v; want contains_secret and a sanitized invalid error", row)
	}
	for _, a := range e.audit.snapshot() {
		if strings.Contains(a.Detail, plainValue) {
			t.Errorf("audit detail %q carries the Secret value", a.Detail)
		}
	}
}

// --- Ordering: refusals before anything ---------------------------------------

func TestHandleApplyDB_PinMismatchRefusedBeforeAnything(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		pin          map[string]string
	}{
		{"cluster", "cluster_pin_mismatch", map[string]string{"targetCluster": "remote-b"}},
		{"generation", "cluster_generation_mismatch", map[string]string{"targetCluster": remoteClusterID, "targetGeneration": "2020-01-01T00:00:00Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrackedEnv(t)
			opID := uuid.New()
			params := trackedQuery(opID.String())
			for k, v := range tc.pin {
				params[k] = v
			}
			w := e.applyAs(e.user, remoteClusterID, params, widgetYAML)
			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d; want 409, body=%s", w.Code, w.Body.String())
			}
			if r := decodeError(t, w).Error.Reason; r != tc.reason {
				t.Errorf("reason = %q; want %q", r, tc.reason)
			}
			assertNoActions(t, "remote", e.remoteDyn)
			if row := e.receipt(t, opID); row != nil {
				t.Errorf("a pin refusal wrote a receipt: %+v", row)
			}
		})
	}
}

func TestHandleApplyDB_NoTrackedParam_WritesNoReceipt(t *testing.T) {
	e := newTrackedEnv(t)
	w := e.applyAs(e.user, remoteClusterID, nil, widgetYAML)
	if got := dataKeys(t, w.Body.Bytes()); !slices.Equal(got, []string{"results", "summary"}) {
		t.Fatalf("untracked data keys = %v; want [results summary]", got)
	}
	_, total, err := e.receipts.ListForOwner(context.Background(), store.ReceiptQueryParams{OwnerID: e.user.ID})
	if err != nil {
		t.Fatalf("ListForOwner: %v", err)
	}
	if total != 0 {
		t.Errorf("receipts for the owner = %d; want 0 — an untracked apply must not touch the changes service", total)
	}
}

// --- Interrupted mid-bundle (D3) -------------------------------------------------

// The receipt store goes away after document 0 is recorded and while
// document 1 is being applied. The live response is truthful (document 1's
// real outcome, document 2 never attempted), the row is left recovery-
// visible, a same-id retry is told the operation is in flight, and once
// reconciliation closes the row as unknown a retry replays what was recorded
// and says the rest is unknown. Nothing is ever applied twice.
func TestHandleApplyDB_InterruptedMidBundle_ReportsUnknownAndNeverReplays(t *testing.T) {
	e := newTrackedEnv(t)
	inspector := openTestDB(t) // survives the outage
	inspectorReceipts := store.NewChangeReceiptStore(inspector.Pool)

	e.remoteDyn.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.PatchAction).GetName() == "b" {
			e.db.Close() // the recording of document 1 will now fail
		}
		return false, nil, nil
	})
	body := bundle(widgetDoc("a"), widgetDoc("b"), widgetDoc("c"))
	opID := uuid.New()

	live := decodeTracked(t, e.applyTracked(opID, remoteClusterID, body))

	if got := patchCount(e.remoteDyn); got != 2 {
		t.Fatalf("patches = %d; want 2 — the engine must stop once recording fails", got)
	}
	if live.Results[0].Action != "created" || live.Results[1].Action != "created" {
		t.Errorf("results = %+v; want the two attempted documents' real outcomes", live.Results)
	}
	if r := live.Results[2]; r.Action != "failed" || r.Error != changes.NotAppliedError {
		t.Errorf("results[2] = %+v; want failed %q", r, changes.NotAppliedError)
	}
	if live.Summary.Total != 3 || live.Summary.Failed != 1 {
		t.Errorf("summary = %+v; want total 3 with 1 failed", live.Summary)
	}
	tr := live.Tracking
	if tr.State != store.ReceiptUnknown || tr.RecordedThrough != 1 || tr.NotAttempted != 1 || tr.Unrecorded != 0 {
		t.Errorf("tracking = %+v; want unknown, recordedThrough 1, notAttempted 1", tr)
	}
	if !slices.ContainsFunc(tr.Warnings, func(w string) bool { return strings.Contains(w, "receipt finalization failed") }) {
		t.Errorf("warnings = %v; want the finalization failure reported", tr.Warnings)
	}

	row, err := inspectorReceipts.Get(context.Background(), opID)
	if err != nil || row == nil {
		t.Fatalf("Get after outage: %v, %v", row, err)
	}
	if row.State != store.ReceiptApplying || row.MutationStartedAt == nil || row.CompletedAt != nil || len(row.Objects) != 1 {
		t.Fatalf("row after outage = %+v; want applying, mutation started, not completed, 1 recorded object", row)
	}

	// The database is back (a new service over a live pool).
	e.handler.Changes = changes.NewService(inspectorReceipts, discardLogger)

	w := e.applyTracked(opID, remoteClusterID, body)
	if w.Code != http.StatusConflict {
		t.Fatalf("retry while unreconciled: status = %d; want 409, body=%s", w.Code, w.Body.String())
	}
	if eb := decodeError(t, w); eb.Error.Reason != changes.ReasonOperationInFlight || eb.Error.Extra["receiptId"] != opID.String() {
		t.Errorf("retry error = %+v; want operation_in_flight naming the receipt", eb.Error)
	}

	// Reconcile exactly as boot does, after the row ages past the grace.
	// Harness convention 4: only this test's own row is backdated, and only
	// this row is asserted on (the sweep's count is table-global and is not
	// read). Other suites' rows younger than the grace are never touched.
	if _, err := inspector.Pool.Exec(context.Background(),
		`UPDATE change_receipts SET created_at = created_at - make_interval(secs => $2) WHERE id = $1`,
		opID, (2 * store.ReceiptOrphanGrace).Seconds()); err != nil {
		t.Fatalf("backdating: %v", err)
	}
	if _, err := inspectorReceipts.ReconcileOrphans(context.Background(), store.ReceiptOrphanGrace); err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}

	replayed := decodeTracked(t, e.applyTracked(opID, remoteClusterID, body))
	if got := patchCount(e.remoteDyn); got != 2 {
		t.Fatalf("patches after replay = %d; want still 2", got)
	}
	rt := replayed.Tracking
	if !rt.Replayed || rt.State != store.ReceiptUnknown || rt.RecordedThrough != 1 || rt.Unrecorded != 2 || rt.NotAttempted != 0 {
		t.Errorf("replay tracking = %+v; want replayed unknown, 1 recorded, 2 unrecorded", rt)
	}
	if replayed.Results[0].Action != "created" {
		t.Errorf("replayed results[0] = %+v; want the recorded outcome", replayed.Results[0])
	}
	for _, i := range []int{1, 2} {
		if r := replayed.Results[i]; r.Action != "failed" || r.Error != changes.NotRecordedError {
			t.Errorf("replayed results[%d] = %+v; want failed %q", i, r, changes.NotRecordedError)
		}
	}
}

// --- Request context ending mid-bundle ------------------------------------------

// The client hangs up (or the BFF's 30s cap cuts the request) right after
// document 0's PATCH. Document 0's outcome is still recorded and the receipt
// still finalized — the receipt writes run detached from the request context —
// and documents 1..2, never sent, are reported and recorded as not attempted,
// never as indeterminate. A later same-id retry replays exactly that.
func TestHandleApplyDB_ContextCancelledMidBundle_UnsentDocsNotAttempted(t *testing.T) {
	e := newTrackedEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelOnPatch(e.remoteDyn, "a", cancel, nil)
	body := bundle(widgetDoc("a"), widgetDoc("b"), widgetDoc("c"))
	opID := uuid.New()

	r := httptest.NewRequest(http.MethodPost, applyURL(trackedQuery(opID.String())), strings.NewReader(body))
	r = r.WithContext(middleware.WithClusterID(auth.ContextWithUser(ctx, e.user), remoteClusterID))
	live := decodeTracked(t, serve(e.handler.HandleApply, r))

	if got := patchCount(e.remoteDyn); got != 1 {
		t.Fatalf("patches = %d; want 1 — nothing may be sent after the request ended", got)
	}
	if live.Results[0].Action != "created" {
		t.Errorf("results[0] = %+v; want the sent document's outcome", live.Results[0])
	}
	for _, i := range []int{1, 2} {
		if r := live.Results[i]; r.Action != "failed" || r.Error != changes.NotAttemptedError {
			t.Errorf("results[%d] = %+v; want failed %q", i, r, changes.NotAttemptedError)
		}
	}
	tr := live.Tracking
	if tr.State != store.ReceiptPartial || tr.RecordedThrough != 1 || tr.NotAttempted != 2 || tr.Unrecorded != 0 || len(tr.Warnings) != 0 {
		t.Errorf("tracking = %+v; want partial, 1 recorded, 2 not attempted, no warnings", tr)
	}

	// Recorded and finalized although the request context was already done.
	row := e.receipt(t, opID)
	if row.State != store.ReceiptPartial || row.CompletedAt == nil || len(row.Objects) != 1 {
		t.Fatalf("receipt = %+v; want partial, completed, 1 recorded object", row)
	}
	for _, o := range row.Objects {
		if o.ErrorClass == changes.ErrorClassIndeterminate {
			t.Errorf("receipt object %+v is indeterminate; nothing unsent may be", o)
		}
	}

	replayed := decodeTracked(t, e.applyTracked(opID, remoteClusterID, body))
	if !replayed.Tracking.Replayed || replayed.Tracking.NotAttempted != 2 || replayed.Tracking.Unrecorded != 0 {
		t.Errorf("replay tracking = %+v; want replayed with 2 not attempted", replayed.Tracking)
	}
	if got := patchCount(e.remoteDyn); got != 1 {
		t.Errorf("patches after replay = %d; want still 1", got)
	}
}

// --- Operation id canonicalization ------------------------------------------------

// An uppercase id is the same operation as its lowercase form: everything
// that names it carries the canonical lowercase spelling, and a lowercase
// retry replays it.
func TestHandleApplyDB_UppercaseOperationIDIsCanonicalized(t *testing.T) {
	e := newTrackedEnv(t)
	opID := uuid.New()
	lower := opID.String()
	body := widgetDoc("a")

	first := decodeTracked(t, e.applyAs(e.user, remoteClusterID, trackedQuery(strings.ToUpper(lower)), body))
	if first.Tracking.OperationID != lower || first.Tracking.ReceiptURL != "/v1/changes/"+lower {
		t.Errorf("tracking id/url = %q %q; want the lowercase canonical form %q", first.Tracking.OperationID, first.Tracking.ReceiptURL, lower)
	}
	if row := e.receipt(t, opID); row == nil {
		t.Fatal("no receipt under the canonical id")
	}
	for _, a := range e.audit.snapshot() {
		if !strings.HasSuffix(a.Detail, " op="+lower) {
			t.Errorf("audit detail %q; want it to end with op=%s", a.Detail, lower)
		}
	}

	retry := decodeTracked(t, e.applyTracked(opID, remoteClusterID, body))
	if !retry.Tracking.Replayed || retry.Tracking.OperationID != lower {
		t.Errorf("lowercase retry tracking = %+v; want a replay of the same operation", retry.Tracking)
	}
	if got := patchCount(e.remoteDyn); got != 1 {
		t.Errorf("patches = %d; want 1", got)
	}
}

// --- Audit -------------------------------------------------------------------------

// A tracked apply audits exactly what an untracked one does — one entry per
// result, same fields — with the operation id appended to Detail.
func TestHandleApplyDB_AuditCardinalityUnchangedAndCarriesOperationID(t *testing.T) {
	body := bundle(widgetDoc("a"), widgetDoc("denied"), widgetDoc("b"))
	deny := func(dyn *trackedEnv) {
		rejectPatch(dyn.remoteDyn, "denied", func(clienttesting.PatchAction) error {
			return apierrors.NewForbidden(widgetGVR.GroupResource(), "denied", errors.New("user cannot patch"))
		})
	}

	plain := newTrackedEnv(t)
	deny(plain)
	plain.applyAs(plain.user, remoteClusterID, nil, body)

	tracked := newTrackedEnv(t)
	tracked.user = plain.user // same identity, so every other field must match
	deny(tracked)
	opID := uuid.New()
	decodeTracked(t, tracked.applyTracked(opID, remoteClusterID, body))

	want, got := plain.audit.snapshot(), tracked.audit.snapshot()
	if len(got) != len(want) || len(got) != 3 {
		t.Fatalf("audit entries: tracked %d, untracked %d; want 3 each", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		w.Timestamp, g.Timestamp = time.Time{}, time.Time{}
		w.Detail += " op=" + opID.String()
		if !reflect.DeepEqual(w, g) {
			t.Errorf("audit entry %d:\n tracked   %+v\n want      %+v", i, g, w)
		}
	}
}

func TestHandleApplyDB_RepairOfIsRecordedAsLinkOnly(t *testing.T) {
	e := newTrackedEnv(t)
	original, repair := uuid.New(), uuid.New()
	decodeTracked(t, e.applyTracked(original, remoteClusterID, widgetDoc("a")))

	w := e.applyAs(e.user, remoteClusterID,
		map[string]string{"trackedOperationId": repair.String(), "repairOf": original.String()}, widgetDocSized("a", 4))
	got := decodeTracked(t, w)
	if got.Tracking.RepairOf != original.String() {
		t.Errorf("tracking.repairOf = %q; want %q", got.Tracking.RepairOf, original)
	}
	if row := e.receipt(t, repair); row.RepairOf == nil || *row.RepairOf != original {
		t.Errorf("receipt repair_of = %v; want %s", row.RepairOf, original)
	}
}
