package preferences

// handler_test.go — validation and HTTP-surface coverage for the personal
// preferences endpoints (Release A, U2).
//
// The validation tests are pure and always run. The handler tests that need a
// real record come through the same KUBECENTER_TEST_DATABASE_URL gate the
// store package uses, so `go test ./...` stays green without a database.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// ---------------------------------------------------------------------------
// Saved-view envelope validation
// ---------------------------------------------------------------------------

// reasonOf returns the wire reason code carried by a ValidationError, or ""
// when err is nil or of another type.
func reasonOf(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	return ""
}

func savedViewJSON(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()

	base := map[string]any{
		"schemaVersion": SavedViewSchemaVersion,
		"resourceKind":  "pods",
		"namespace":     "",
		"search":        "",
		"statusFilter":  "all",
		"sortKey":       "name",
		"sortDir":       "asc",
	}
	for k, v := range fields {
		if v == nil {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshalling fixture: %v", err)
	}
	return raw
}

func TestValidateSavedView_Allowlist(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		value      string
		wantReason string // "" means accepted
	}{
		{"statusFilter all", "statusFilter", "all", ""},
		{"statusFilter running", "statusFilter", "running", ""},
		{"statusFilter pending", "statusFilter", "pending", ""},
		{"statusFilter failed", "statusFilter", "failed", ""},
		{"statusFilter progressing", "statusFilter", "progressing", ""},
		{"statusFilter unknown", "statusFilter", "terminating", "invalid_config"},
		{"statusFilter empty", "statusFilter", "", "invalid_config"},
		{"statusFilter case-sensitive", "statusFilter", "Running", "invalid_config"},

		{"sortKey name", "sortKey", "name", ""},
		{"sortKey namespace", "sortKey", "namespace", ""},
		{"sortKey age", "sortKey", "age", ""},
		// The ResourceTable comparator handles only the three keys above.
		// Accepting "status" here would silently sort by name instead.
		{"sortKey status", "sortKey", "status", "invalid_config"},
		{"sortKey empty", "sortKey", "", "invalid_config"},

		{"sortDir asc", "sortDir", "asc", ""},
		{"sortDir desc", "sortDir", "desc", ""},
		{"sortDir ascending", "sortDir", "ascending", "invalid_config"},
		{"sortDir empty", "sortDir", "", "invalid_config"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{tc.field: tc.value}))
			if got := reasonOf(err); got != tc.wantReason {
				t.Fatalf("ValidateSavedView(%s=%q) reason = %q (err %v); want %q",
					tc.field, tc.value, got, err, tc.wantReason)
			}
		})
	}
}

func TestValidateSavedView_UnknownResourceKind(t *testing.T) {
	_, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{"resourceKind": "widgets"}))
	if got := reasonOf(err); got != "unknown_resource_kind" {
		t.Fatalf("reason = %q (err %v); want unknown_resource_kind", got, err)
	}

	// A registered kind is accepted.
	if _, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{"resourceKind": "deployments"})); err != nil {
		t.Fatalf("ValidateSavedView(deployments): %v", err)
	}
}

func TestValidateSavedView_NamespaceOnClusterScopedKind(t *testing.T) {
	// nodes is cluster-scoped: a namespace scope is meaningless there.
	_, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{
		"resourceKind": "nodes", "namespace": "prod",
	}))
	if got := reasonOf(err); got != "invalid_config" {
		t.Fatalf("reason = %q (err %v); want invalid_config", got, err)
	}

	// The same kind with no namespace is fine.
	if _, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{
		"resourceKind": "nodes", "namespace": "",
	})); err != nil {
		t.Fatalf("cluster-scoped kind with empty namespace: %v", err)
	}
}

func TestValidateSavedView_InvalidNamespace(t *testing.T) {
	for _, ns := range []string{"Prod", "has space", strings.Repeat("n", 64), "-leading", "trailing-"} {
		t.Run(ns, func(t *testing.T) {
			_, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{"namespace": ns}))
			if got := reasonOf(err); got != "invalid_config" {
				t.Fatalf("namespace %q reason = %q (err %v); want invalid_config", ns, got, err)
			}
		})
	}
}

// TestValidateSavedView_DropsUnlistedFields is the KTD4 guard: nothing outside
// the allowlist may reach the config column, so the validator re-marshals its
// own typed struct rather than passing the caller's bytes through.
func TestValidateSavedView_DropsUnlistedFields(t *testing.T) {
	raw := savedViewJSON(t, map[string]any{
		"script":      "<script>alert(1)</script>",
		"url":         "https://evil.example/redirect",
		"ownerId":     "someone-else",
		"clusterId":   "other-cluster",
		"__proto__":   map[string]any{"polluted": true},
		"extraNested": map[string]any{"deep": []any{1, 2, 3}},
	})

	_, normalized, err := ValidateSavedView(raw)
	if err != nil {
		t.Fatalf("ValidateSavedView: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(normalized, &got); err != nil {
		t.Fatalf("normalized config is not valid JSON: %v", err)
	}
	for _, banned := range []string{"script", "url", "ownerId", "clusterId", "__proto__", "extraNested"} {
		if _, present := got[banned]; present {
			t.Errorf("unlisted field %q survived normalization into the stored config", banned)
		}
	}

	want := map[string]struct{}{
		"schemaVersion": {}, "resourceKind": {}, "namespace": {},
		"search": {}, "statusFilter": {}, "sortKey": {}, "sortDir": {},
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("normalized config carries unexpected key %q", k)
		}
	}
}

func TestValidateSavedView_UnsupportedSchemaVersion(t *testing.T) {
	for _, v := range []any{0, 2, 99} {
		t.Run(strings.TrimSpace(string(mustJSON(t, v))), func(t *testing.T) {
			_, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{"schemaVersion": v}))
			if got := reasonOf(err); got != "unsupported_schema_version" {
				t.Fatalf("schemaVersion %v reason = %q (err %v); want unsupported_schema_version", v, got, err)
			}
		})
	}
}

func TestValidateSavedView_SearchBounds(t *testing.T) {
	// At the limit is fine.
	if _, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{
		"search": strings.Repeat("x", maxSearchLen),
	})); err != nil {
		t.Fatalf("search at the limit was rejected: %v", err)
	}

	// One past it is not.
	_, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{
		"search": strings.Repeat("x", maxSearchLen+1),
	}))
	if got := reasonOf(err); got != "invalid_config" {
		t.Fatalf("oversize search reason = %q (err %v); want invalid_config", got, err)
	}

	// Control characters are rejected: they would corrupt the table's filter
	// display and are never typed by a real user.
	for _, s := range []string{"na\x00me", "line\nbreak", "tab\tsep", "bell\x07"} {
		_, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{"search": s}))
		if got := reasonOf(err); got != "invalid_config" {
			t.Errorf("search %q reason = %q; want invalid_config", s, got)
		}
	}
}

func TestValidateSavedView_MalformedJSON(t *testing.T) {
	_, _, err := ValidateSavedView(json.RawMessage(`{"schemaVersion":`))
	if got := reasonOf(err); got != "invalid_config" {
		t.Fatalf("malformed JSON reason = %q (err %v); want invalid_config", got, err)
	}
}

// ---------------------------------------------------------------------------
// Pin envelope validation
// ---------------------------------------------------------------------------

func pinJSON(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()

	base := map[string]any{
		"schemaVersion": PinSchemaVersion,
		"resourceKind":  "deployments",
		"group":         "",
		"version":       "",
		"namespace":     "prod",
		"name":          "api",
		"uid":           "8b1e0000-0000-4000-8000-000000000001",
		"displayKind":   "Deployment",
	}
	for k, v := range fields {
		if v == nil {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshalling fixture: %v", err)
	}
	return raw
}

// TestValidatePin_DedupKeyExcludesUID proves the identity contract: the uid is
// evidence, not identity, so a recreated object collides with the existing pin
// (R1, R6).
func TestValidatePin_DedupKeyExcludesUID(t *testing.T) {
	first, _, err := ValidatePin(pinJSON(t, map[string]any{"uid": "uid-original"}))
	if err != nil {
		t.Fatalf("ValidatePin: %v", err)
	}
	second, _, err := ValidatePin(pinJSON(t, map[string]any{"uid": "uid-recreated"}))
	if err != nil {
		t.Fatalf("ValidatePin: %v", err)
	}

	if PinDedupKey(first) != PinDedupKey(second) {
		t.Fatalf("dedup keys differ across uids: %q vs %q — uid must not be part of identity",
			PinDedupKey(first), PinDedupKey(second))
	}
	if want := "deployments/prod/api"; PinDedupKey(first) != want {
		t.Errorf("PinDedupKey = %q; want %q", PinDedupKey(first), want)
	}

	// A different namespace is a different pin.
	other, _, err := ValidatePin(pinJSON(t, map[string]any{"namespace": "staging"}))
	if err != nil {
		t.Fatalf("ValidatePin: %v", err)
	}
	if PinDedupKey(other) == PinDedupKey(first) {
		t.Error("pins in different namespaces produced the same dedup key")
	}
}

func TestValidatePin_Rejections(t *testing.T) {
	tests := []struct {
		name       string
		fields     map[string]any
		wantReason string
	}{
		{"unknown kind", map[string]any{"resourceKind": "widgets"}, "unknown_resource_kind"},
		{"bad schema version", map[string]any{"schemaVersion": 2}, "unsupported_schema_version"},
		{"empty name", map[string]any{"name": ""}, "invalid_config"},
		{"name too long", map[string]any{"name": strings.Repeat("a", 254)}, "invalid_config"},
		{"name not dns-1123", map[string]any{"name": "Not_A_Name"}, "invalid_config"},
		{"namespace on cluster-scoped kind", map[string]any{"resourceKind": "nodes", "namespace": "prod"}, "invalid_config"},
		{"missing namespace on namespaced kind", map[string]any{"namespace": ""}, "invalid_config"},
		{"uid with illegal characters", map[string]any{"uid": "uid/../../etc"}, "invalid_config"},
		{"uid too long", map[string]any{"uid": strings.Repeat("a", 65)}, "invalid_config"},
		// group/version are reserved-empty in Release A; a client that sets
		// them is talking to a contract that does not exist yet.
		{"group set", map[string]any{"group": "apps"}, "invalid_config"},
		{"version set", map[string]any{"version": "v1"}, "invalid_config"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ValidatePin(pinJSON(t, tc.fields))
			if got := reasonOf(err); got != tc.wantReason {
				t.Fatalf("reason = %q (err %v); want %q", got, err, tc.wantReason)
			}
		})
	}
}

func TestValidatePin_ClusterScopedKindNeedsNoNamespace(t *testing.T) {
	cfg, _, err := ValidatePin(pinJSON(t, map[string]any{
		"resourceKind": "nodes", "namespace": "", "displayKind": "Node",
	}))
	if err != nil {
		t.Fatalf("ValidatePin(cluster-scoped): %v", err)
	}
	if want := "nodes//node-1"; PinDedupKey(cfg) == want {
		t.Errorf("unexpected dedup shape %q", PinDedupKey(cfg))
	}
	if !strings.HasPrefix(PinDedupKey(cfg), "nodes//") {
		t.Errorf("cluster-scoped pin dedup key = %q; want an empty namespace segment", PinDedupKey(cfg))
	}
}

func TestValidatePin_DropsUnlistedFields(t *testing.T) {
	raw := pinJSON(t, map[string]any{"script": "alert(1)", "ownerId": "someone-else"})

	_, normalized, err := ValidatePin(raw)
	if err != nil {
		t.Fatalf("ValidatePin: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(normalized, &got); err != nil {
		t.Fatalf("normalized config is not valid JSON: %v", err)
	}
	for _, banned := range []string{"script", "ownerId"} {
		if _, present := got[banned]; present {
			t.Errorf("unlisted field %q survived normalization", banned)
		}
	}
}

// ---------------------------------------------------------------------------
// Dedup keys and record-name validation
// ---------------------------------------------------------------------------

func TestSavedViewDedupKey_FoldsCaseAndSpace(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Prod pods", "prod pods"},
		{"  Prod pods  ", "prod pods"},
		{"PROD PODS", "prod pods"},
	}
	for _, tc := range tests {
		if got := SavedViewDedupKey(tc.in); got != tc.want {
			t.Errorf("SavedViewDedupKey(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateRecordName(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantReason string
	}{
		{"ordinary", "Prod failing pods", ""},
		{"at the limit", strings.Repeat("a", maxRecordNameLen), ""},
		{"empty", "", "invalid_name"},
		{"whitespace only", "   ", "invalid_name"},
		{"too long", strings.Repeat("a", maxRecordNameLen+1), "invalid_name"},
		{"control character", "prod\x00pods", "invalid_name"},
		{"newline", "prod\npods", "invalid_name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRecordName(tc.in)
			if got := reasonOf(err); got != tc.wantReason {
				t.Fatalf("ValidateRecordName(%q) reason = %q (err %v); want %q",
					tc.in, got, err, tc.wantReason)
			}
		})
	}
}

// TestValidateOwnerID_Bounds covers the one length bound whose value the
// client does not control. auth.User.ID is server-supplied and can be a long
// LDAP distinguished name; the DDL caps owner_id at 512 characters, so an
// identity past that must be refused deliberately rather than surfacing as an
// opaque constraint violation from the database.
func TestValidateOwnerID_Bounds(t *testing.T) {
	if err := ValidateOwnerID(strings.Repeat("a", maxOwnerIDLen)); err != nil {
		t.Fatalf("owner id at the limit was rejected: %v", err)
	}
	err := ValidateOwnerID(strings.Repeat("a", maxOwnerIDLen+1))
	if got := reasonOf(err); got != "identity_too_long" {
		t.Fatalf("oversize owner id reason = %q (err %v); want identity_too_long", got, err)
	}
	if err := ValidateOwnerID(""); reasonOf(err) != "identity_too_long" {
		t.Errorf("empty owner id was accepted; want rejection")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// HTTP surface
// ---------------------------------------------------------------------------

// endpoint describes one route for the table-driven surface tests.
type endpoint struct {
	method, path string
	body         string
}

// allEndpoints is every route registerPreferencesRoutes wires.
func allEndpoints() []endpoint {
	const viewBody = `{"name":"v","config":{"schemaVersion":1,"resourceKind":"pods","namespace":"","search":"","statusFilter":"all","sortKey":"name","sortDir":"asc"}}`
	const pinBody = `{"name":"p","config":{"schemaVersion":1,"resourceKind":"deployments","group":"","version":"","namespace":"prod","name":"api","uid":"","displayKind":"Deployment"}}`
	id := uuid.New().String()
	return []endpoint{
		{http.MethodGet, "/preferences/views", ""},
		{http.MethodPost, "/preferences/views", viewBody},
		{http.MethodPut, "/preferences/views/" + id, `{"name":"v","revision":1,"config":{"schemaVersion":1,"resourceKind":"pods","namespace":"","search":"","statusFilter":"all","sortKey":"name","sortDir":"asc"}}`},
		{http.MethodDelete, "/preferences/views/" + id, ""},
		{http.MethodGet, "/preferences/pins", ""},
		{http.MethodPost, "/preferences/pins", pinBody},
		{http.MethodDelete, "/preferences/pins/" + id, ""},
	}
}

// testDatabaseURLEnv gates the handler tests that need a real record, exactly
// as the store package's harness does.
const (
	testDatabaseURLEnv     = "KUBECENTER_TEST_DATABASE_URL"
	testDatabaseRequireEnv = "KUBECENTER_TEST_REQUIRE_DATABASE"
)

// testDatabaseRequired reports whether the caller demanded a real database.
//
// This mirrors the canonical predicate in the store package's harness
// (backend/internal/store/testdb_test.go) exactly — lowercased and trimmed,
// with "", "0", "false" and "no" all meaning not-required. Two gates that
// claim the same semantics must not disagree: a divergence here would make
// one package skip where the other hard-fails, which is precisely the silent
// gate the harness conventions exist to prevent.
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

// testStore returns a PreferenceStore over the migrated test database, or
// skips the calling test when none is configured. Migrations run through the
// production entry point, so the schema is the one the binary builds.
func testStore(t *testing.T) *store.PreferenceStore {
	t.Helper()

	connString := strings.TrimSpace(os.Getenv(testDatabaseURLEnv))
	if connString == "" {
		if testDatabaseRequired(os.LookupEnv) {
			t.Fatalf("%s is set but %s is empty; a database was required", testDatabaseRequireEnv, testDatabaseURLEnv)
		}
		t.Skipf("%s is not set; skipping PostgreSQL-backed handler test", testDatabaseURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := store.New(ctx, connString, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("connecting to %s: %v", testDatabaseURLEnv, err)
	}
	t.Cleanup(db.Close)
	return store.NewPreferenceStore(db.Pool)
}

// testUser returns an authenticated user whose id is unique to this test, so
// rows written here are invisible to every other suite.
func testUser(t *testing.T) *auth.User {
	t.Helper()

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generating owner suffix: %v", err)
	}
	id := "test-pref-" + hex.EncodeToString(suffix[:])
	return &auth.User{ID: id, Username: id, Provider: "local"}
}

// prefRouter wires the same routes registerPreferencesRoutes does, so the
// {id} parameter resolves exactly as it will in the server.
func prefRouter(h *Handler) chi.Router {
	r := chi.NewRouter()
	r.Route("/preferences", func(pr chi.Router) {
		pr.Route("/views", func(vr chi.Router) {
			vr.Get("/", h.HandleListViews)
			vr.Post("/", h.HandleCreateView)
			vr.Put("/{id}", h.HandleUpdateView)
			vr.Delete("/{id}", h.HandleDeleteView)
		})
		pr.Route("/pins", func(pnr chi.Router) {
			pnr.Get("/", h.HandleListPins)
			pnr.Post("/", h.HandleCreatePin)
			pnr.Delete("/{id}", h.HandleDeletePin)
		})
	})
	return r
}

// do issues one request through the router as the given user (nil = anonymous)
// on the given cluster.
func do(t *testing.T, h *Handler, u *auth.User, clusterID string, e endpoint) *httptest.ResponseRecorder {
	t.Helper()

	var body io.Reader
	if e.body != "" {
		body = strings.NewReader(e.body)
	}
	req := httptest.NewRequest(e.method, e.path, body)
	req.Header.Set("Content-Type", "application/json")
	if u != nil {
		req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	}
	if clusterID != "" {
		req = req.WithContext(middleware.WithClusterID(req.Context(), clusterID))
	}

	rec := httptest.NewRecorder()
	prefRouter(h).ServeHTTP(rec, req)
	return rec
}

// decodeEnvelope reads the standard response envelope from a recorder.
func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) api.Response {
	t.Helper()

	var resp api.Response
	if rec.Body.Len() == 0 {
		return resp
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a valid envelope (%q): %v", rec.Body.String(), err)
	}
	return resp
}

// TestHandler_NoDatabase_Returns503 is the G5 guard: a deployment without a
// database must say so, not answer 404. The routes are registered either way,
// because chi's bare 404 for an unregistered path is indistinguishable from a
// missing record.
func TestHandler_NoDatabase_Returns503(t *testing.T) {
	h := &Handler{Store: nil}
	user := testUser(t)

	for _, e := range allEndpoints() {
		t.Run(e.method+" "+e.path, func(t *testing.T) {
			rec := do(t, h, user, "local", e)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d; want 503", rec.Code)
			}
			resp := decodeEnvelope(t, rec)
			if resp.Error == nil || resp.Error.Reason != "database_unavailable" {
				t.Fatalf("reason = %+v; want database_unavailable", resp.Error)
			}
		})
	}
}

func TestHandler_Unauthenticated_Returns401(t *testing.T) {
	h := &Handler{Store: nil}

	for _, e := range allEndpoints() {
		t.Run(e.method+" "+e.path, func(t *testing.T) {
			rec := do(t, h, nil, "local", e)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d; want 401 (authentication is checked before the database)", rec.Code)
			}
		})
	}
}

// TestHandler_ServerDerivedFieldsRejectedInBody proves a client cannot choose
// its own owner or cluster. The request types carry neither field and the
// decoder rejects unknown ones, so the attempt is refused by name rather than
// silently dropped — a client built against the wrong contract finds out.
func TestHandler_ServerDerivedFieldsRejectedInBody(t *testing.T) {
	// A real store: the availability gate runs before the body is parsed, so
	// a nil store would answer 503 and never reach the decoder.
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	const cfg = `{"schemaVersion":1,"resourceKind":"pods","namespace":"","search":"","statusFilter":"all","sortKey":"name","sortDir":"asc"}`
	for _, field := range []string{`"ownerId":"someone-else"`, `"clusterId":"other-cluster"`, `"revision":99`} {
		t.Run(field, func(t *testing.T) {
			body := `{"name":"v",` + field + `,"config":` + cfg + `}`
			rec := do(t, h, user, "local", endpoint{http.MethodPost, "/preferences/views", body})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400 for a body carrying a server-derived field", rec.Code)
			}
		})
	}
}

func TestHandler_MalformedUUID_Returns404(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	for _, id := range []string{"not-a-uuid", "../../etc/passwd", "123"} {
		t.Run(id, func(t *testing.T) {
			rec := do(t, h, user, "local", endpoint{http.MethodDelete, "/preferences/views/" + id, ""})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d; want 404 so id probing is indistinguishable from a real miss", rec.Code)
			}
		})
	}
}

func TestHandler_EmptyList_ReturnsEmptyArray(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	rec := do(t, h, user, "local", endpoint{http.MethodGet, "/preferences/views", ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"data":[]`) {
		t.Errorf("empty list body = %s; want \"data\":[] rather than null", body)
	}
}

const viewConfigJSON = `{"schemaVersion":1,"resourceKind":"pods","namespace":"","search":"","statusFilter":"all","sortKey":"name","sortDir":"asc"}`

// createView posts one saved view and returns the decoded record.
func createView(t *testing.T, h *Handler, u *auth.User, clusterID, name string) store.PreferenceRecord {
	t.Helper()

	body := `{"name":` + string(mustJSON(t, name)) + `,"config":` + viewConfigJSON + `}`
	rec := do(t, h, u, clusterID, endpoint{http.MethodPost, "/preferences/views", body})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %q: status = %d, body = %s; want 201", name, rec.Code, rec.Body.String())
	}
	var out struct {
		Data store.PreferenceRecord `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding created record: %v", err)
	}
	return out.Data
}

// TestHandler_ClusterIDComesFromContext proves the stored cluster is the one
// the middleware resolved, never anything the caller could influence.
func TestHandler_ClusterIDComesFromContext(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	created := createView(t, h, user, "prod-east", "east view")
	if created.ClusterID != "prod-east" {
		t.Fatalf("stored clusterId = %q; want the context value prod-east", created.ClusterID)
	}

	// The same name on another cluster is a different record, which is only
	// true if the cluster really came from context.
	other := createView(t, h, user, "prod-west", "east view")
	if other.ID == created.ID {
		t.Fatal("the same record was returned for two clusters")
	}
	if other.ClusterID != "prod-west" {
		t.Errorf("stored clusterId = %q; want prod-west", other.ClusterID)
	}
}

// TestHandler_CrossUser_Returns404 is the isolation guard at the HTTP layer: a
// record id belonging to someone else must be indistinguishable from one that
// never existed, and the 404 body must leak nothing about it.
func TestHandler_CrossUser_Returns404(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	alice, bob := testUser(t), testUser(t)

	rec := createView(t, h, alice, "local", "alice private view")

	t.Run("update", func(t *testing.T) {
		body := `{"name":"hijacked","revision":` + string(mustJSON(t, rec.Revision)) + `,"config":` + viewConfigJSON + `}`
		got := do(t, h, bob, "local", endpoint{http.MethodPut, "/preferences/views/" + rec.ID.String(), body})
		if got.Code != http.StatusNotFound {
			t.Fatalf("status = %d; want 404", got.Code)
		}
		if body := got.Body.String(); strings.Contains(body, "alice private view") {
			t.Errorf("404 body leaked record metadata: %s", body)
		}
	})

	t.Run("delete", func(t *testing.T) {
		got := do(t, h, bob, "local", endpoint{http.MethodDelete, "/preferences/views/" + rec.ID.String(), ""})
		if got.Code != http.StatusNotFound {
			t.Fatalf("status = %d; want 404", got.Code)
		}
	})

	t.Run("list is owner scoped", func(t *testing.T) {
		got := do(t, h, bob, "local", endpoint{http.MethodGet, "/preferences/views", ""})
		if body := got.Body.String(); strings.Contains(body, "alice private view") {
			t.Errorf("bob's list contains alice's record: %s", body)
		}
	})

	// Alice's record survived every attempt.
	list := do(t, h, alice, "local", endpoint{http.MethodGet, "/preferences/views", ""})
	if !strings.Contains(list.Body.String(), "alice private view") {
		t.Error("alice's record disappeared after bob's attempts")
	}
}

func TestHandler_StaleRevision_Returns409(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	rec := createView(t, h, user, "local", "view")
	update := func(revision int64, name string) *httptest.ResponseRecorder {
		body := `{"name":` + string(mustJSON(t, name)) + `,"revision":` + string(mustJSON(t, revision)) + `,"config":` + viewConfigJSON + `}`
		return do(t, h, user, "local", endpoint{http.MethodPut, "/preferences/views/" + rec.ID.String(), body})
	}

	if got := update(rec.Revision, "first"); got.Code != http.StatusOK {
		t.Fatalf("first update status = %d, body = %s; want 200", got.Code, got.Body.String())
	}

	got := update(rec.Revision, "second") // stale
	if got.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d; want 409", got.Code)
	}
	if resp := decodeEnvelope(t, got); resp.Error == nil || resp.Error.Reason != "revision_conflict" {
		t.Fatalf("reason = %+v; want revision_conflict", resp.Error)
	}
}

func TestHandler_DuplicateName_Returns409(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	createView(t, h, user, "local", "Prod pods")

	body := `{"name":"PROD PODS","config":` + viewConfigJSON + `}`
	got := do(t, h, user, "local", endpoint{http.MethodPost, "/preferences/views", body})
	if got.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409 for a case-folded duplicate", got.Code)
	}
	if resp := decodeEnvelope(t, got); resp.Error == nil || resp.Error.Reason != "duplicate_name" {
		t.Fatalf("reason = %+v; want duplicate_name", resp.Error)
	}
}

func TestHandler_DuplicatePin_ReportsAlreadyPinned(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	pinBody := func(uid string) string {
		return `{"name":"api","config":{"schemaVersion":1,"resourceKind":"deployments","group":"","version":"","namespace":"prod","name":"api","uid":"` + uid + `","displayKind":"Deployment"}}`
	}
	if got := do(t, h, user, "local", endpoint{http.MethodPost, "/preferences/pins", pinBody("uid-original")}); got.Code != http.StatusCreated {
		t.Fatalf("create pin status = %d, body = %s; want 201", got.Code, got.Body.String())
	}

	// A recreated object: same kind/namespace/name, new uid. It must collide
	// with the existing pin rather than creating a duplicate.
	got := do(t, h, user, "local", endpoint{http.MethodPost, "/preferences/pins", pinBody("uid-recreated")})
	if got.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409", got.Code)
	}
	if resp := decodeEnvelope(t, got); resp.Error == nil || resp.Error.Reason != "already_pinned" {
		t.Fatalf("reason = %+v; want already_pinned", resp.Error)
	}
}

func TestHandler_OversizeBody_Returns413(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	huge := `{"name":"v","config":{"schemaVersion":1,"resourceKind":"pods","search":"` +
		strings.Repeat("x", maxBodyBytes+1) + `"}}`
	got := do(t, h, user, "local", endpoint{http.MethodPost, "/preferences/views", huge})
	if got.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413", got.Code)
	}
}

// recordingAudit captures audit entries in memory. It satisfies audit.Logger.
type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recordingAudit) Log(_ context.Context, e audit.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func (r *recordingAudit) snapshot() []audit.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// TestHandler_WriteEmitsAudit checks the audit contract, including that no
// config content reaches the audit record.
func TestHandler_WriteEmitsAudit(t *testing.T) {
	recorder := &recordingAudit{}
	h := &Handler{Store: testStore(t), AuditLogger: recorder}
	user := testUser(t)

	created := createView(t, h, user, "prod-east", "audited view")
	if got := do(t, h, user, "prod-east", endpoint{http.MethodDelete, "/preferences/views/" + created.ID.String(), ""}); got.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204", got.Code)
	}

	entries := recorder.snapshot()
	if len(entries) != 2 {
		t.Fatalf("recorded %d audit entries; want 2 (create + delete)", len(entries))
	}
	if entries[0].Action != audit.ActionCreate || entries[1].Action != audit.ActionDelete {
		t.Errorf("actions = %q, %q; want create, delete", entries[0].Action, entries[1].Action)
	}
	for _, e := range entries {
		if e.ResourceKind != "savedView" {
			t.Errorf("ResourceKind = %q; want savedView", e.ResourceKind)
		}
		if e.ClusterID != "prod-east" {
			t.Errorf("ClusterID = %q; want the request's cluster prod-east", e.ClusterID)
		}
		if e.User != user.Username {
			t.Errorf("User = %q; want %q", e.User, user.Username)
		}
		if e.Result != audit.ResultSuccess {
			t.Errorf("Result = %q; want success", e.Result)
		}
		// The config is the user's own text; it must not be copied into a
		// second, longer-lived store.
		if strings.Contains(e.Detail, "resourceKind") || strings.Contains(e.Detail, "sortKey") {
			t.Errorf("audit Detail leaked config contents: %q", e.Detail)
		}
	}
}

// TestHandler_RequestCancelled_NoPartialWrite proves a cancelled request
// leaves nothing behind.
func TestHandler_RequestCancelled_NoPartialWrite(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	body := `{"name":"cancelled","config":` + viewConfigJSON + `}`
	req := httptest.NewRequest(http.MethodPost, "/preferences/views", strings.NewReader(body)).WithContext(ctx)
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	out := httptest.NewRecorder()
	prefRouter(h).ServeHTTP(out, req)

	if out.Code == http.StatusCreated {
		t.Fatal("a cancelled request reported a created record")
	}

	list := do(t, h, user, "local", endpoint{http.MethodGet, "/preferences/views", ""})
	if strings.Contains(list.Body.String(), "cancelled") {
		t.Errorf("cancelled request left a row behind: %s", list.Body.String())
	}
}

// TestPreferencesRoutes_RequireCSRF asserts the state-changing endpoints sit
// behind the CSRF middleware the authenticated group applies.
func TestPreferencesRoutes_RequireCSRF(t *testing.T) {
	h := &Handler{Store: nil}
	user := testUser(t)
	guarded := middleware.CSRF(prefRouter(h))

	for _, e := range allEndpoints() {
		if e.method == http.MethodGet {
			continue
		}
		t.Run(e.method+" "+e.path, func(t *testing.T) {
			var body io.Reader
			if e.body != "" {
				body = strings.NewReader(e.body)
			}
			req := httptest.NewRequest(e.method, e.path, body)
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
			out := httptest.NewRecorder()
			guarded.ServeHTTP(out, req)

			if out.Code != http.StatusForbidden {
				t.Fatalf("status = %d without X-Requested-With; want 403", out.Code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Regressions from the U2 code review
// ---------------------------------------------------------------------------

// TestTestDatabaseRequired_MatchesCanonicalGate pins this package's gate to the
// store package's semantics. The two are separate functions in separate
// packages, so only a test keeps them honest.
func TestTestDatabaseRequired_MatchesCanonicalGate(t *testing.T) {
	lookupOf := func(value string, present bool) func(string) (string, bool) {
		return func(k string) (string, bool) {
			if k == testDatabaseRequireEnv {
				return value, present
			}
			return "", false
		}
	}
	tests := []struct {
		name   string
		lookup func(string) (string, bool)
		want   bool
	}{
		{"unset", lookupOf("", false), false},
		{"empty", lookupOf("", true), false},
		{"zero", lookupOf("0", true), false},
		{"false", lookupOf("false", true), false},
		{"uppercase FALSE", lookupOf("FALSE", true), false},
		{"mixed-case False", lookupOf("False", true), false},
		{"no", lookupOf("no", true), false},
		{"uppercase NO", lookupOf("NO", true), false},
		{"whitespace", lookupOf("  \n", true), false},
		{"one", lookupOf("1", true), true},
		{"true", lookupOf("true", true), true},
		{"padded true", lookupOf(" true ", true), true},
		{"anything else", lookupOf("yes", true), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := testDatabaseRequired(tc.lookup); got != tc.want {
				t.Fatalf("testDatabaseRequired() = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestValidateRecordName_CountsCharactersNotBytes guards the bound against
// regressing to a byte count. The database constrains name with char_length,
// so a byte count would refuse a name in any non-Latin script at a fraction of
// the limit the error message quotes.
func TestValidateRecordName_CountsCharactersNotBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		char string
	}{
		{"two-byte (Latin-1 supplement)", "é"},
		{"three-byte (CJK)", "日"},
		{"four-byte (emoji)", "🚀"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atLimit := strings.Repeat(tc.char, maxRecordNameLen)
			if err := ValidateRecordName(atLimit); err != nil {
				t.Fatalf("a %d-character name was rejected (%d bytes): %v",
					maxRecordNameLen, len(atLimit), err)
			}
			over := strings.Repeat(tc.char, maxRecordNameLen+1)
			if got := reasonOf(ValidateRecordName(over)); got != "invalid_name" {
				t.Fatalf("a %d-character name gave reason %q; want invalid_name",
					maxRecordNameLen+1, got)
			}
		})
	}

	// The identity bound and the search bound share the rule.
	if err := ValidateOwnerID(strings.Repeat("é", maxOwnerIDLen)); err != nil {
		t.Errorf("owner id of %d characters was rejected: %v", maxOwnerIDLen, err)
	}
	if _, _, err := ValidateSavedView(savedViewJSON(t, map[string]any{
		"search": strings.Repeat("日", maxSearchLen),
	})); err != nil {
		t.Errorf("search of %d characters was rejected: %v", maxSearchLen, err)
	}
}

// TestHandler_CrossKindID_Returns404 proves a record id addressed through the
// wrong kind's route is refused, and — the part that matters — that the record
// it names is left exactly as it was.
//
// Saved views and pins share one id space and the store scopes its mutations
// by owner and id alone, so without a kind check a pin's id sent to the view
// update route would overwrite that pin's config while the row still said pin,
// and sent to the view delete route would destroy it.
func TestHandler_CrossKindID_Returns404(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	user := testUser(t)

	pinBody := `{"name":"api","config":{"schemaVersion":1,"resourceKind":"deployments","group":"","version":"","namespace":"prod","name":"api","uid":"uid-1","displayKind":"Deployment"}}`
	created := do(t, h, user, "local", endpoint{http.MethodPost, "/preferences/pins", pinBody})
	if created.Code != http.StatusCreated {
		t.Fatalf("create pin: status = %d, body = %s", created.Code, created.Body.String())
	}
	var out struct {
		Data store.PreferenceRecord `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding created pin: %v", err)
	}
	pin := out.Data

	view := createView(t, h, user, "local", "a real view")

	t.Run("pin id on the view update route", func(t *testing.T) {
		body := `{"name":"hijacked","revision":1,"config":` + viewConfigJSON + `}`
		got := do(t, h, user, "local", endpoint{http.MethodPut, "/preferences/views/" + pin.ID.String(), body})
		if got.Code != http.StatusNotFound {
			t.Fatalf("status = %d; want 404", got.Code)
		}
	})

	t.Run("pin id on the view delete route", func(t *testing.T) {
		got := do(t, h, user, "local", endpoint{http.MethodDelete, "/preferences/views/" + pin.ID.String(), ""})
		if got.Code != http.StatusNotFound {
			t.Fatalf("status = %d; want 404", got.Code)
		}
	})

	t.Run("view id on the pin delete route", func(t *testing.T) {
		got := do(t, h, user, "local", endpoint{http.MethodDelete, "/preferences/pins/" + view.ID.String(), ""})
		if got.Code != http.StatusNotFound {
			t.Fatalf("status = %d; want 404", got.Code)
		}
	})

	// Both records survived, unmodified: the pin still has its own config and
	// its original revision, and the view is still there.
	pins := do(t, h, user, "local", endpoint{http.MethodGet, "/preferences/pins", ""})
	if !strings.Contains(pins.Body.String(), `"uid-1"`) {
		t.Errorf("the pin lost its config or was deleted: %s", pins.Body.String())
	}
	if strings.Contains(pins.Body.String(), "hijacked") {
		t.Errorf("the pin was overwritten through the view route: %s", pins.Body.String())
	}
	views := do(t, h, user, "local", endpoint{http.MethodGet, "/preferences/views", ""})
	if !strings.Contains(views.Body.String(), "a real view") {
		t.Errorf("the saved view was deleted through the pin route: %s", views.Body.String())
	}
}

// TestHandler_LimitReached_Returns409WithLimit covers the quota branch. The
// ceiling is lowered for the test so this does not have to create a hundred
// records to reach it.
func TestHandler_LimitReached_Returns409WithLimit(t *testing.T) {
	const ceiling = 3
	h := &Handler{Store: testStore(t), maxSavedViews: ceiling}
	user := testUser(t)

	for i := range ceiling {
		createView(t, h, user, "local", fmt.Sprintf("view-%d", i))
	}

	body := `{"name":"one too many","config":` + viewConfigJSON + `}`
	got := do(t, h, user, "local", endpoint{http.MethodPost, "/preferences/views", body})
	if got.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s; want 409", got.Code, got.Body.String())
	}

	resp := decodeEnvelope(t, got)
	if resp.Error == nil || resp.Error.Reason != "limit_reached" {
		t.Fatalf("reason = %+v; want limit_reached", resp.Error)
	}
	limit, ok := resp.Error.Extra["limit"]
	if !ok {
		t.Fatalf("409 carries no limit in extras: %+v", resp.Error.Extra)
	}
	// JSON numbers decode as float64.
	if got, want := fmt.Sprintf("%v", limit), fmt.Sprintf("%v", float64(ceiling)); got != want {
		t.Errorf("extras limit = %v; want %v", got, want)
	}

	// Pins are a separate bucket and are unaffected by a saturated view quota.
	pinBody := `{"name":"api","config":{"schemaVersion":1,"resourceKind":"deployments","group":"","version":"","namespace":"prod","name":"api","uid":"","displayKind":"Deployment"}}`
	if pinned := do(t, h, user, "local", endpoint{http.MethodPost, "/preferences/pins", pinBody}); pinned.Code != http.StatusCreated {
		t.Errorf("pin create under a saturated view quota: status = %d", pinned.Code)
	}
}

// TestHandler_CrossUser_ListDoesNotLeak strengthens the owner-scoping check:
// the second user now owns a record of their own, so a handler that ignored
// the owner filter entirely would return two records here and fail. The
// original sub-test could not catch that, because the second user had nothing
// for the leaked record to appear alongside.
func TestHandler_CrossUser_ListDoesNotLeak(t *testing.T) {
	h := &Handler{Store: testStore(t)}
	alice, bob := testUser(t), testUser(t)

	createView(t, h, alice, "local", "alice view")
	createView(t, h, bob, "local", "bob view")

	got := do(t, h, bob, "local", endpoint{http.MethodGet, "/preferences/views", ""})
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", got.Code)
	}

	var out struct {
		Data     []store.PreferenceRecord `json:"data"`
		Metadata *struct {
			Total int `json:"total"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("bob sees %d records; want exactly his own", len(out.Data))
	}
	if out.Data[0].Name != "bob view" {
		t.Errorf("bob sees %q; want his own record", out.Data[0].Name)
	}
	if out.Metadata == nil || out.Metadata.Total != 1 {
		t.Errorf("metadata.total = %+v; want 1", out.Metadata)
	}
}
