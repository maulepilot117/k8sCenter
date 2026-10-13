package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// bodyMarker appears in every fake Alertmanager error or junk body, so a
// test can prove no response text reaches an error.
const bodyMarker = "AM-BODY-MARKER-7f3e"

// fakeAlertmanager is an httptest Alertmanager, served over TLS like a real
// binding, that answers GET <prefix>/api/v2/alerts with body and records the
// path, query and Authorization header of the last request.
type fakeAlertmanager struct {
	srv       *httptest.Server
	mu        sync.Mutex
	lastAuth  string
	lastPath  string
	lastQuery url.Values
	hits      atomic.Int32
}

func newFakeAlertmanager(t *testing.T, body string) *fakeAlertmanager {
	t.Helper()
	fa := &fakeAlertmanager{}
	fa.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fa.hits.Add(1)
		fa.mu.Lock()
		fa.lastAuth = r.Header.Get("Authorization")
		fa.lastPath = r.URL.Path
		fa.lastQuery = r.URL.Query()
		fa.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(fa.srv.Close)
	return fa
}

func (fa *fakeAlertmanager) last() (auth, path string, query url.Values) {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.lastAuth, fa.lastPath, fa.lastQuery
}

// amAlert renders one Alertmanager v2 alert; severity "" omits the label.
func amAlert(name, severity, state string) string {
	labels := fmt.Sprintf(`"alertname":%q`, name)
	if severity != "" {
		labels += fmt.Sprintf(`,"severity":%q`, severity)
	}
	return fmt.Sprintf(`{"labels":{%s},"status":{"state":%q,"silencedBy":[],"inhibitedBy":[]},"fingerprint":"x"}`, labels, state)
}

func amList(alerts ...string) string { return "[" + strings.Join(alerts, ",") + "]" }

func testAMClient(t *testing.T, rawURL, token string) *AlertmanagerClient {
	t.Helper()
	c, err := newAlertmanagerClient(rawURL, newBearerRoundTripper(testTransport(), rawURL, token))
	if err != nil {
		t.Fatalf("building alertmanager client: %v", err)
	}
	return c
}

func TestAlertmanagerClient_CountsSendsStateFilterAndCountsCritical(t *testing.T) {
	fa := newFakeAlertmanager(t, amList(
		amAlert("KubePodCrashLooping", "critical", "active"),
		amAlert("KubeCPUOvercommit", "warning", "active"),
		amAlert("NoSeverity", "", "active"),
	))
	c := testAMClient(t, fa.srv.URL, "am-tok")

	got, err := c.Counts(context.Background())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if got != (AlertCounts{Active: 3, Critical: 1}) {
		t.Fatalf("counts = %+v; want 3 active, 1 critical", got)
	}

	auth, path, q := fa.last()
	if path != "/api/v2/alerts" {
		t.Errorf("path = %q; want /api/v2/alerts", path)
	}
	want := map[string]string{"active": "true", "silenced": "false", "inhibited": "false", "unprocessed": "false"}
	for k, v := range want {
		if got := q[k]; len(got) != 1 || got[0] != v {
			t.Errorf("query %s = %q; want [%q]", k, got, v)
		}
	}
	if auth != "Bearer am-tok" {
		t.Errorf("Authorization = %q; want the Alertmanager token", auth)
	}
}

// The server may ignore the state parameters (an old Alertmanager or a proxy
// that strips them), so a non-active alert is dropped client-side; heartbeat
// alerts never count, even when labelled critical.
func TestAlertmanagerClient_CountsExcludesHeartbeatsAndNonActive(t *testing.T) {
	fa := newFakeAlertmanager(t, amList(
		amAlert("Watchdog", "critical", "active"),
		amAlert("DeadMansSwitch", "critical", "active"),
		amAlert("InfoInhibitor", "critical", "active"),
		amAlert("SilencedButReturned", "critical", "suppressed"),
		amAlert("Unprocessed", "critical", "unprocessed"),
		amAlert("RealWarning", "warning", "active"),
	))
	got, err := testAMClient(t, fa.srv.URL, "").Counts(context.Background())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if got != (AlertCounts{Active: 1, Critical: 0}) {
		t.Fatalf("counts = %+v; want 1 active, 0 critical", got)
	}
}

func TestAlertmanagerClient_EmptyListCountsZero(t *testing.T) {
	fa := newFakeAlertmanager(t, "[]")
	got, err := testAMClient(t, fa.srv.URL, "").Counts(context.Background())
	if err != nil || got != (AlertCounts{}) {
		t.Fatalf("counts = %+v, err %v; want zero counts", got, err)
	}
}

func TestAlertmanagerClient_PathPrefixIsKept(t *testing.T) {
	for _, suffix := range []string{"/alertmanager", "/alertmanager/"} {
		t.Run(suffix, func(t *testing.T) {
			fa := newFakeAlertmanager(t, "[]")
			if _, err := testAMClient(t, fa.srv.URL+suffix, "").Counts(context.Background()); err != nil {
				t.Fatalf("Counts: %v", err)
			}
			if _, path, _ := fa.last(); path != "/alertmanager/api/v2/alerts" {
				t.Fatalf("path = %q; want /alertmanager/api/v2/alerts", path)
			}
		})
	}
}

// amErrClasses lists every class a read can fail with, so each case can
// prove it matches exactly one.
var amErrClasses = []error{
	ErrAlertmanagerRefused,
	ErrAlertmanagerRedirected,
	ErrAlertmanagerUnreachable,
	ErrAlertmanagerUnreadable,
	ErrAlertmanagerTokenReentry,
	ErrNoAlertmanager,
}

func assertAMClass(t *testing.T, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %v, got no error", want)
	}
	for _, class := range amErrClasses {
		if got := errors.Is(err, class); got != (class == want) {
			t.Errorf("errors.Is(%q, %v) = %v", err, class, got)
		}
	}
	if strings.Contains(err.Error(), bodyMarker) {
		t.Errorf("error %q quotes the response body", err)
	}
}

// statusServer answers every request with code and a body carrying the
// marker.
func statusServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		fmt.Fprint(w, bodyMarker)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAlertmanagerClient_ErrorClasses(t *testing.T) {
	cases := []struct {
		name string
		srv  func(t *testing.T) string // returns the bound Alertmanager URL
		want error
	}{
		{"401", func(t *testing.T) string { return statusServer(t, http.StatusUnauthorized).URL }, ErrAlertmanagerRefused},
		{"403", func(t *testing.T) string { return statusServer(t, http.StatusForbidden).URL }, ErrAlertmanagerRefused},
		{"500", func(t *testing.T) string { return statusServer(t, http.StatusInternalServerError).URL }, ErrAlertmanagerUnreachable},
		{"503", func(t *testing.T) string { return statusServer(t, http.StatusServiceUnavailable).URL }, ErrAlertmanagerUnreachable},
		{"connection refused", func(t *testing.T) string {
			srv := httptest.NewTLSServer(http.NotFoundHandler())
			u := srv.URL
			srv.Close()
			return u
		}, ErrAlertmanagerUnreachable},
		{"not JSON", func(t *testing.T) string {
			return newFakeAlertmanager(t, "<html>"+bodyMarker+"</html>").srv.URL
		}, ErrAlertmanagerUnreadable},
		{"JSON object instead of array", func(t *testing.T) string {
			return newFakeAlertmanager(t, `{"error":"`+bodyMarker+`"}`).srv.URL
		}, ErrAlertmanagerUnreadable},
		{"JSON null", func(t *testing.T) string { return newFakeAlertmanager(t, "null").srv.URL }, ErrAlertmanagerUnreadable},
		{"array of the wrong element type", func(t *testing.T) string {
			return newFakeAlertmanager(t, `["`+bodyMarker+`"]`).srv.URL
		}, ErrAlertmanagerUnreadable},
		{"truncated array", func(t *testing.T) string {
			return newFakeAlertmanager(t, `[{"labels":{"alertname":"`+bodyMarker).srv.URL
		}, ErrAlertmanagerUnreadable},
		{"body over 16 MiB", func(t *testing.T) string {
			// A valid JSON array, so only the size cap can reject it.
			return newFakeAlertmanager(t, "["+strings.Repeat(" ", alertmanagerBodyLimit)+"]").srv.URL
		}, ErrAlertmanagerUnreadable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := c.srv(t)
			_, err := testAMClient(t, u, "am-tok").Counts(context.Background())
			assertAMClass(t, err, c.want)
		})
	}
}

func TestAlertmanagerClient_TimeoutIsUnreachable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := testAMClient(t, srv.URL, "").Counts(ctx)
	assertAMClass(t, err, ErrAlertmanagerUnreachable)
}

// redirectingAM binds an https Alertmanager that answers every request with
// a 302 to target(srvURL, path), and dials through a recorder.
func redirectingAM(t *testing.T, target func(srvURL, path string) string) (*AlertmanagerClient, *recordingTransport) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target(srv.URL, r.URL.Path), http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	rec := &recordingTransport{base: testTransport()}
	c, err := newAlertmanagerClient(srv.URL, newBearerRoundTripper(rec, srv.URL, "am-tok"))
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	return c, rec
}

func TestAlertmanagerClient_RedirectToAnotherHostIsNotFollowed(t *testing.T) {
	other := newFakeAlertmanager(t, "[]")
	c, rec := redirectingAM(t, func(_, path string) string { return other.srv.URL + path })

	_, err := c.Counts(context.Background())
	assertAMClass(t, err, ErrAlertmanagerRedirected)
	if other.hits.Load() != 0 {
		t.Fatal("the redirect was followed to another host")
	}
	if got := rec.seen(); len(got) != 1 {
		t.Fatalf("want only the original request on the wire, got %q", got)
	}
}

func TestAlertmanagerClient_RedirectToPlainHTTPIsNotFollowed(t *testing.T) {
	c, rec := redirectingAM(t, func(srvURL, path string) string {
		return "http://" + strings.TrimPrefix(srvURL, "https://") + path
	})

	_, err := c.Counts(context.Background())
	assertAMClass(t, err, ErrAlertmanagerRedirected)
	got := rec.seen()
	if len(got) != 1 || !strings.HasPrefix(got[0], "https://") {
		t.Fatalf("want only the original https request on the wire, got %q", got)
	}
}

// The token is bound to the https origin of the Alertmanager URL: a bound
// URL given as plain http never carries it.
func TestAlertmanagerClient_PlainHTTPBindingSendsNoToken(t *testing.T) {
	plain, seen := authRecorder(t)
	c, err := newAlertmanagerClient(plain.URL, newBearerRoundTripper(testTransport(), plain.URL, "am-tok"))
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	_, _ = c.Counts(context.Background())
	if got := seen(); len(got) != 1 || got[0] != "" {
		t.Fatalf("a plain-http Alertmanager received %q", got)
	}
}

func TestProbeAlertmanager_SendsNoMatchFilterAndToken(t *testing.T) {
	fa := newFakeAlertmanager(t, "[]")
	if err := probeAlertmanager(context.Background(), testTransport(), fa.srv.URL+"/am", "am-tok"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	auth, path, q := fa.last()
	if path != "/am/api/v2/alerts" {
		t.Errorf("path = %q; want /am/api/v2/alerts", path)
	}
	if auth != "Bearer am-tok" {
		t.Errorf("Authorization = %q; want Bearer am-tok", auth)
	}
	if got := q["filter"]; len(got) != 1 || got[0] != alertmanagerProbeFilter {
		t.Errorf("filter = %q; want [%q]", got, alertmanagerProbeFilter)
	}
}

func TestProbeAlertmanager_ErrorClasses(t *testing.T) {
	refused := statusServer(t, http.StatusUnauthorized)
	err := probeAlertmanager(context.Background(), testTransport(), refused.URL, "am-tok")
	assertAMClass(t, err, ErrAlertmanagerRefused)

	object := newFakeAlertmanager(t, `{"status":"`+bodyMarker+`"}`)
	err = probeAlertmanager(context.Background(), testTransport(), object.srv.URL, "")
	assertAMClass(t, err, ErrAlertmanagerUnreadable)

	other := newFakeAlertmanager(t, "[]")
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.srv.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	err = probeAlertmanager(context.Background(), testTransport(), redirect.URL, "am-tok")
	assertAMClass(t, err, ErrAlertmanagerRedirected)
	if other.hits.Load() != 0 {
		t.Fatal("the probe followed a redirect")
	}
}

// TestProbeAlertmanager_UsesStrictTransport proves the exported probe dials
// through the strict transport: a loopback Alertmanager is refused.
func TestProbeAlertmanager_UsesStrictTransport(t *testing.T) {
	fa := newFakeAlertmanager(t, "[]")
	err := ProbeAlertmanager(context.Background(), fa.srv.URL, "am-tok")
	assertAMClass(t, err, ErrAlertmanagerUnreachable)
	if fa.hits.Load() != 0 {
		t.Fatal("the probe reached a loopback address")
	}
}
