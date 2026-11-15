package rubrik

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const (
	testClientID     = "client|svc-k8scenter"
	testClientSecret = "canary-secret-7f3a9c"
)

// fakeSource is an in-memory ConnectionSource.
type fakeSource struct {
	mu   sync.Mutex
	conn Connection
	err  error
}

func (f *fakeSource) Connection(context.Context) (Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn, f.err
}

func (f *fakeSource) ConnectionRevision(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn.Revision, f.err
}

func (f *fakeSource) setRevision(rev int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conn.Revision = rev
}

// fakeTenant is an RSC tenant stand-in. Each exchange issues "tok-<n>".
type fakeTenant struct {
	srv       *httptest.Server
	exchanges atomic.Int32
	queries   atomic.Int32

	mu        sync.Mutex
	forms     []url.Values
	tokenFn   func(w http.ResponseWriter, n int32) bool // true: handled
	graphqlFn func(w http.ResponseWriter, r *http.Request, auth string)
}

func newFakeTenant(t *testing.T) *fakeTenant {
	t.Helper()
	ft := &fakeTenant{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+tokenPath, func(w http.ResponseWriter, r *http.Request) {
		n := ft.exchanges.Add(1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		ft.mu.Lock()
		ft.forms = append(ft.forms, r.PostForm)
		ft.forms[len(ft.forms)-1].Set("content-type", r.Header.Get("Content-Type"))
		fn := ft.tokenFn
		ft.mu.Unlock()
		if fn != nil && fn(w, n) {
			return
		}
		writeJSON(w, map[string]any{"access_token": fmt.Sprintf("tok-%d", n), "expires_in": 43200})
	})
	mux.HandleFunc("POST "+graphqlPath, func(w http.ResponseWriter, r *http.Request) {
		ft.queries.Add(1)
		ft.mu.Lock()
		fn := ft.graphqlFn
		ft.mu.Unlock()
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if fn != nil {
			fn(w, r, auth)
			return
		}
		writeJSON(w, map[string]any{"data": map[string]any{"token": auth}})
	})
	ft.srv = httptest.NewUnstartedServer(mux)
	// Closing the server mid-handshake (concurrent callers) is expected.
	ft.srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	ft.srv.StartTLS()
	t.Cleanup(ft.srv.Close)
	return ft
}

func (ft *fakeTenant) setToken(fn func(w http.ResponseWriter, n int32) bool) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.tokenFn = fn
}

func (ft *fakeTenant) setGraphQL(fn func(w http.ResponseWriter, r *http.Request, auth string)) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.graphqlFn = fn
}

func (ft *fakeTenant) source() *fakeSource {
	return &fakeSource{conn: Connection{
		TenantURL:    ft.srv.URL,
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		Revision:     1,
	}}
}

// client returns a client that trusts the tenant's test certificate and
// skips the address check (httptest listens on loopback).
func (ft *fakeTenant) client(src ConnectionSource, logger *slog.Logger) *Client {
	return newClient(src, logger, ft.srv.Client().Transport, nil)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type tokenData struct {
	Token string `json:"token"`
}

var testQuery = Request{Query: "query { clusterConnection { nodes { id } } }"}

func TestQueryExchangesAndCachesToken(t *testing.T) {
	ft := newFakeTenant(t)
	c := ft.client(ft.source(), nil)

	for i := range 3 {
		var out tokenData
		if err := c.Query(context.Background(), testQuery, &out); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		if out.Token != "tok-1" {
			t.Fatalf("query %d sent bearer %q, want tok-1", i, out.Token)
		}
	}
	if got := ft.exchanges.Load(); got != 1 {
		t.Fatalf("exchanges = %d, want 1", got)
	}
	form := ft.forms[0]
	if form.Get("client_id") != testClientID || form.Get("client_secret") != testClientSecret || form.Get("grant_type") != "client_credentials" {
		t.Fatalf("token form = %v", form)
	}
	if ct := form.Get("content-type"); ct != "application/x-www-form-urlencoded" {
		t.Fatalf("token content type = %q", ct)
	}
}

func TestConcurrentFirstQueriesShareOneExchange(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setToken(func(w http.ResponseWriter, n int32) bool {
		time.Sleep(200 * time.Millisecond) // hold the exchange open so the callers overlap
		return false
	})
	c := ft.client(ft.source(), nil)

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() { errs <- c.Query(context.Background(), testQuery, nil) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	if got := ft.exchanges.Load(); got != 1 {
		t.Fatalf("exchanges = %d, want 1", got)
	}
}

func TestUnauthorizedReauthenticatesOnce(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
		if auth == "tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{"data": map[string]any{"token": auth}})
	})
	c := ft.client(ft.source(), nil)

	var out tokenData
	if err := c.Query(context.Background(), testQuery, &out); err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.Token != "tok-2" || ft.exchanges.Load() != 2 || ft.queries.Load() != 2 {
		t.Fatalf("token %q, exchanges %d, queries %d; want tok-2, 2, 2", out.Token, ft.exchanges.Load(), ft.queries.Load())
	}
}

func TestSecondUnauthorizedIsAuthFailed(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := ft.client(ft.source(), nil)

	err := c.Query(context.Background(), testQuery, nil)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	if ft.exchanges.Load() != 2 || ft.queries.Load() != 2 {
		t.Fatalf("exchanges %d, queries %d; want exactly one retry (2, 2)", ft.exchanges.Load(), ft.queries.Load())
	}
}

func TestRefusedCredentialsAreAuthFailed(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setToken(func(w http.ResponseWriter, n int32) bool {
		w.WriteHeader(http.StatusUnauthorized)
		return true
	})
	c := ft.client(ft.source(), nil)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	if ft.queries.Load() != 0 {
		t.Fatalf("a query was sent without a token")
	}
}

func TestRateLimitedCarriesRetryAfter(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c := ft.client(ft.source(), nil)

	err := c.Query(context.Background(), testQuery, nil)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second {
		t.Fatalf("err = %#v, want RetryAfter 7s", err)
	}
	if errors.Is(err, ErrUnreachable) {
		t.Fatalf("rate limited error also matches ErrUnreachable")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"30", 30 * time.Second},
		{"-5", 0},
		{"999999999999", maxRetryAfter},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{now.Add(5 * time.Hour).Format(http.TimeFormat), maxRetryAfter},
		{"soon", 0},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.in, now); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestGraphQLErrorsReturnMessages(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
		writeJSON(w, map[string]any{
			"data":   nil,
			"errors": []map[string]any{{"message": "object not found"}, {"message": "bearer " + auth + " rejected"}},
		})
	})
	c := ft.client(ft.source(), nil)

	err := c.Query(context.Background(), testQuery, nil)
	var ge *GraphQLError
	if !errors.As(err, &ge) || !errors.Is(err, ErrGraphQL) {
		t.Fatalf("err = %v, want *GraphQLError", err)
	}
	if len(ge.Messages) != 2 || ge.Messages[0] != "object not found" {
		t.Fatalf("messages = %q", ge.Messages)
	}
	if strings.Contains(err.Error(), "tok-1") {
		t.Fatalf("error carries the access token: %v", err)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var elsewhere atomic.Int32
	var leaked atomic.Bool
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
	}))
	t.Cleanup(other.Close)

	for _, endpoint := range []string{tokenPath, graphqlPath} {
		t.Run(endpoint, func(t *testing.T) {
			ft := newFakeTenant(t)
			redirect := func(w http.ResponseWriter) {
				w.Header().Set("Location", other.URL+endpoint)
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
			if endpoint == tokenPath {
				ft.setToken(func(w http.ResponseWriter, n int32) bool { redirect(w); return true })
			} else {
				ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) { redirect(w) })
			}
			c := ft.client(ft.source(), nil)
			if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrUnreachable) {
				t.Fatalf("err = %v, want ErrUnreachable", err)
			}
		})
	}
	if elsewhere.Load() != 0 || leaked.Load() {
		t.Fatalf("redirect target was contacted %d times (bearer leaked: %v)", elsewhere.Load(), leaked.Load())
	}
}

func TestOversizedAnswerIsUnreadable(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
		writeJSON(w, map[string]any{"data": map[string]any{"token": strings.Repeat("x", 4096)}})
	})
	c := ft.client(ft.source(), nil)
	c.bodyLimit = 1024

	var out tokenData
	if err := c.Query(context.Background(), testQuery, &out); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err = %v, want ErrUnreadable", err)
	}
}

func TestUndecodableAnswerIsUnreadable(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
		_, _ = w.Write([]byte("<html>gateway</html>"))
	})
	c := ft.client(ft.source(), nil)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err = %v, want ErrUnreadable", err)
	}
}

func TestServerErrorIsUnreachable(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
		w.WriteHeader(http.StatusBadGateway)
	})
	c := ft.client(ft.source(), nil)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

// TestProductionClientRefusesLoopbackTenant proves NewClient and Probe use
// the address check and the strict transport: a tenant on loopback gets no
// request at all.
func TestProductionClientRefusesLoopbackTenant(t *testing.T) {
	ft := newFakeTenant(t)

	c := NewClient(ft.source(), nil)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrInvalidTenantURL) {
		t.Fatalf("NewClient query err = %v, want ErrInvalidTenantURL", err)
	}
	if err := Probe(context.Background(), ft.srv.URL, testClientID, testClientSecret); !errors.Is(err, ErrInvalidTenantURL) {
		t.Fatalf("Probe err = %v, want ErrInvalidTenantURL", err)
	}

	// Without the address check, the strict dialer alone still refuses.
	direct := newClient(ft.source(), nil,
		newTenantTransport(func(*http.Request) (*url.URL, error) { return nil, nil }, k8s.StrictDialContext, k8s.SafeDialContext),
		nil)
	if err := direct.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("strict transport err = %v, want ErrUnreachable", err)
	}

	if n := ft.exchanges.Load() + ft.queries.Load(); n != 0 {
		t.Fatalf("loopback tenant received %d requests", n)
	}
}

// TestProxyOnPrivateAddressStillChecksTenant: the proxy itself may sit on
// an internal address (here loopback, standing in for a private one), but a
// tenant that resolves to an internal address is refused before any request
// reaches the proxy.
func TestProxyOnPrivateAddressStillChecksTenant(t *testing.T) {
	var mu sync.Mutex
	var connects []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connects = append(connects, r.Method+" "+r.Host)
		mu.Unlock()
		w.WriteHeader(http.StatusForbidden) // refuse the tunnel; reaching us is the point
	}))
	t.Cleanup(proxy.Close)
	proxyURL, _ := url.Parse(proxy.URL)

	plain := (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	transport := newTenantTransport(http.ProxyURL(proxyURL), k8s.StrictDialContext, plain)

	// 203.0.113.0/24 is a documentation range the address check treats as
	// public, so the request goes to the proxy.
	public := &fakeSource{conn: Connection{TenantURL: "https://203.0.113.10", ClientID: testClientID, ClientSecret: testClientSecret, Revision: 1}}
	c := newClient(public, nil, transport, k8s.ValidateRemoteURLContext)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("public tenant err = %v, want ErrUnreachable (proxy refused the tunnel)", err)
	}
	mu.Lock()
	got := append([]string(nil), connects...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "CONNECT 203.0.113.10:443" {
		t.Fatalf("proxy saw %q, want one CONNECT 203.0.113.10:443", got)
	}

	private := &fakeSource{conn: Connection{TenantURL: "https://10.1.2.3", ClientID: testClientID, ClientSecret: testClientSecret, Revision: 1}}
	c = newClient(private, nil, transport, k8s.ValidateRemoteURLContext)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrInvalidTenantURL) {
		t.Fatalf("private tenant err = %v, want ErrInvalidTenantURL", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(connects) != 1 {
		t.Fatalf("private tenant reached the proxy: %q", connects)
	}
}

func TestPlainHTTPTenantIsRefused(t *testing.T) {
	src := &fakeSource{conn: Connection{TenantURL: "http://example.my.rubrik.com", ClientID: testClientID, ClientSecret: testClientSecret}}
	c := newClient(src, nil, http.DefaultTransport, nil)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrInvalidTenantURL) {
		t.Fatalf("err = %v, want ErrInvalidTenantURL", err)
	}
}

func TestNotConfiguredPassesThrough(t *testing.T) {
	src := &fakeSource{err: fmt.Errorf("reading connection: %w", ErrNotConfigured)}
	c := newClient(src, nil, http.DefaultTransport, nil)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestRevisionChangeDiscardsCachedToken(t *testing.T) {
	ft := newFakeTenant(t)
	src := ft.source()
	c := ft.client(src, nil)

	var out tokenData
	if err := c.Query(context.Background(), testQuery, &out); err != nil || out.Token != "tok-1" {
		t.Fatalf("first query: %v, token %q", err, out.Token)
	}
	src.setRevision(2) // a save on another replica: no Flush here
	if err := c.Query(context.Background(), testQuery, &out); err != nil || out.Token != "tok-2" {
		t.Fatalf("query after revision change: %v, token %q (want tok-2)", err, out.Token)
	}
	if ft.exchanges.Load() != 2 {
		t.Fatalf("exchanges = %d, want 2", ft.exchanges.Load())
	}
}

func TestFlushDiscardsCachedToken(t *testing.T) {
	ft := newFakeTenant(t)
	c := ft.client(ft.source(), nil)
	for range 2 {
		if err := c.Query(context.Background(), testQuery, nil); err != nil {
			t.Fatal(err)
		}
		c.Flush()
	}
	if ft.exchanges.Load() != 2 {
		t.Fatalf("exchanges = %d, want 2", ft.exchanges.Load())
	}
}

func TestTokenRefreshedBeforeExpiry(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setToken(func(w http.ResponseWriter, n int32) bool {
		writeJSON(w, map[string]any{"access_token": fmt.Sprintf("tok-%d", n), "expires_in": 3600})
		return true
	})
	c := ft.client(ft.source(), nil)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	query := func() string {
		t.Helper()
		var out tokenData
		if err := c.Query(context.Background(), testQuery, &out); err != nil {
			t.Fatal(err)
		}
		return out.Token
	}
	if got := query(); got != "tok-1" {
		t.Fatalf("token %q", got)
	}
	now = now.Add(54 * time.Minute)
	if got := query(); got != "tok-1" {
		t.Fatalf("token replaced too early: %q", got)
	}
	now = now.Add(2 * time.Minute) // 56m: inside the 5 minute margin
	if got := query(); got != "tok-2" {
		t.Fatalf("token not refreshed inside the margin: %q", got)
	}
}

// TestSecretNeverLeaks runs the failure paths with a tenant that echoes the
// client secret and the access token back, and checks no returned error or
// log line carries either.
func TestSecretNeverLeaks(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	scenarios := map[string]func(ft *fakeTenant){
		"refused credentials": func(ft *fakeTenant) {
			ft.setToken(func(w http.ResponseWriter, n int32) bool {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("bad secret " + testClientSecret))
				return true
			})
		},
		"undecodable token answer": func(ft *fakeTenant) {
			ft.setToken(func(w http.ResponseWriter, n int32) bool {
				_, _ = w.Write([]byte(`{"access_token": ` + testClientSecret))
				return true
			})
		},
		"token server error": func(ft *fakeTenant) {
			ft.setToken(func(w http.ResponseWriter, n int32) bool {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(testClientSecret))
				return true
			})
		},
		"graphql error echoing token": func(ft *fakeTenant) {
			ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
				writeJSON(w, map[string]any{"errors": []map[string]any{{"message": "token " + auth}}})
			})
		},
		"repeated unauthorized": func(ft *fakeTenant) {
			ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(auth))
			})
		},
		"undecodable data": func(ft *fakeTenant) {
			ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
				// A valid envelope whose data does not fit the operation,
				// so the data decode itself fails with the token in hand.
				_, _ = w.Write([]byte(`{"data": {"token": ["` + auth + `"]}}`))
			})
		},
	}
	for name, setup := range scenarios {
		t.Run(name, func(t *testing.T) {
			ft := newFakeTenant(t)
			setup(ft)
			c := ft.client(ft.source(), logger)
			var out tokenData
			err := c.Query(context.Background(), testQuery, &out)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, canary := range []string{testClientSecret, "tok-1", "tok-2"} {
				if strings.Contains(err.Error(), canary) {
					t.Errorf("error carries %q: %v", canary, err)
				}
			}
			perr := probe(context.Background(), ft.srv.Client().Transport, nil, ft.source().conn)
			if perr != nil && strings.Contains(perr.Error(), testClientSecret) {
				t.Errorf("probe error carries the secret: %v", perr)
			}
		})
	}
	for _, canary := range []string{testClientSecret, "tok-1", "tok-2"} {
		if strings.Contains(logs.String(), canary) {
			t.Errorf("logs carry %q:\n%s", canary, logs.String())
		}
	}
}

func TestProbeExchangesCredentials(t *testing.T) {
	ft := newFakeTenant(t)
	if err := probe(context.Background(), ft.srv.Client().Transport, nil, ft.source().conn); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if ft.exchanges.Load() != 1 || ft.queries.Load() != 0 {
		t.Fatalf("exchanges %d, queries %d; want 1, 0", ft.exchanges.Load(), ft.queries.Load())
	}
}

func TestCanonicalProxyAddr(t *testing.T) {
	cases := map[string]string{
		"http://10.0.0.5":        "10.0.0.5:80",
		"https://proxy.corp":     "proxy.corp:443",
		"http://proxy.corp:3128": "proxy.corp:3128",
		"socks5://10.0.0.5":      "10.0.0.5:1080",
		"http://[fd00::1]":       "[fd00::1]:80",
	}
	for in, want := range cases {
		u, _ := url.Parse(in)
		if got := canonicalProxyAddr(u); got != want {
			t.Errorf("canonicalProxyAddr(%s) = %s, want %s", in, got, want)
		}
	}
}

// panickingSource panics when the connection is read, standing in for a
// store bug inside the token exchange.
type panickingSource struct{ fakeSource }

func (p *panickingSource) Connection(context.Context) (Connection, error) {
	panic("store bug")
}

// TestExchangePanicIsRecovered: a panic inside the singleflight exchange
// fails the query with ErrUnreachable instead of crashing the process.
func TestExchangePanicIsRecovered(t *testing.T) {
	ft := newFakeTenant(t)
	src := &panickingSource{}
	src.conn = ft.source().conn
	c := ft.client(src, nil)
	if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if ft.exchanges.Load() != 0 {
		t.Fatalf("a token request was sent after the panic")
	}
}

func TestSourceErrorsAreConnectionUnavailable(t *testing.T) {
	storeDown := errors.New("connection refused by postgres")
	src := &fakeSource{err: storeDown}
	c := newClient(src, nil, http.DefaultTransport, nil)
	err := c.Query(context.Background(), testQuery, nil)
	if !errors.Is(err, ErrConnectionUnavailable) || !errors.Is(err, storeDown) {
		t.Fatalf("err = %v, want ErrConnectionUnavailable wrapping the store error", err)
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Fatalf("a store outage was reported as not configured")
	}
}

func TestUnencodableRequestIsInvalidRequest(t *testing.T) {
	ft := newFakeTenant(t)
	c := ft.client(ft.source(), nil)
	req := Request{Query: "query { x }", Variables: map[string]any{"bad": make(chan int)}}
	if err := c.Query(context.Background(), req, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if ft.exchanges.Load()+ft.queries.Load() != 0 {
		t.Fatalf("an unencodable request reached the tenant")
	}
}

func TestCheckTenantClassifiesLookupFailures(t *testing.T) {
	cases := map[string]struct {
		err  error
		want error
	}{
		"dns failure":     {fmt.Errorf("DNS resolution failed for x: %w", &net.DNSError{Err: "no such host", Name: "x"}), ErrUnreachable},
		"lookup timeout":  {fmt.Errorf("DNS resolution failed for x: %w", context.DeadlineExceeded), ErrUnreachable},
		"caller canceled": {context.Canceled, ErrUnreachable},
		"private address": {errors.New("URL resolves to private address 10.1.2.3"), ErrInvalidTenantURL},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := checkTenant(context.Background(), func(context.Context, string) error { return tc.err }, "https://tenant.example")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			other := ErrInvalidTenantURL
			if tc.want == ErrInvalidTenantURL {
				other = ErrUnreachable
			}
			if errors.Is(err, other) {
				t.Fatalf("err = %v also matches %v", err, other)
			}
		})
	}
}

// TestProductionTransportDialers pins the dialers NewClient and Probe use:
// a tenant address is dialed strictly (an RFC1918 literal is refused before
// any I/O, which the safe dialer would allow), and only a recorded proxy
// address gets the safe dialer.
func TestProductionTransportDialers(t *testing.T) {
	ctx := context.Background()

	c := NewClient(&fakeSource{}, nil)
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("NewClient transport is %T, want *http.Transport", c.http.Transport)
	}
	if _, err := tr.DialContext(ctx, "tcp", "10.0.0.1:443"); err == nil || !strings.Contains(err.Error(), "SSRF dial refused") {
		t.Fatalf("NewClient dial to 10.0.0.1:443 err = %v, want strict refusal", err)
	}

	proxyURL, _ := url.Parse("http://10.0.0.1:3128")
	pt := tenantTransport(http.ProxyURL(proxyURL))
	req, _ := http.NewRequest(http.MethodPost, "https://tenant.example/api/graphql", nil)
	if u, err := pt.Proxy(req); err != nil || u == nil {
		t.Fatalf("proxy decision = %v, %v", u, err)
	}
	dctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if conn, err := pt.DialContext(dctx, "tcp", "10.0.0.1:3128"); err == nil {
		_ = conn.Close()
	} else if strings.Contains(err.Error(), "SSRF dial refused") {
		t.Fatalf("dial to the recorded private proxy was refused: %v", err)
	}
	if _, err := pt.DialContext(ctx, "tcp", "10.0.0.2:443"); err == nil || !strings.Contains(err.Error(), "SSRF dial refused") {
		t.Fatalf("dial to a private non-proxy address err = %v, want strict refusal", err)
	}
}

// TestFlushDuringExchangeIsNotCached: a token whose exchange began before a
// Flush (a connection save) is used by its own caller but never cached.
func TestFlushDuringExchangeIsNotCached(t *testing.T) {
	ft := newFakeTenant(t)
	started := make(chan struct{})
	release := make(chan struct{})
	ft.setToken(func(w http.ResponseWriter, n int32) bool {
		if n == 1 {
			close(started)
			<-release
		}
		return false
	})
	c := ft.client(ft.source(), nil)

	done := make(chan error, 1)
	go func() { done <- c.Query(context.Background(), testQuery, nil) }()
	<-started
	c.Flush()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first query: %v", err)
	}
	if err := c.Query(context.Background(), testQuery, nil); err != nil {
		t.Fatalf("second query: %v", err)
	}
	if got := ft.exchanges.Load(); got != 2 {
		t.Fatalf("exchanges = %d, want 2 (the pre-Flush token must not be cached)", got)
	}
}

func TestStatusClassification(t *testing.T) {
	cases := []struct {
		name    string
		token   int // 0: issue a token normally
		graphql func(w http.ResponseWriter)
		want    error
	}{
		{"graphql 403", 0, func(w http.ResponseWriter) { w.WriteHeader(http.StatusForbidden) }, ErrAuthFailed},
		{"graphql 400 with errors", 0, func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"syntax error"}]}`))
		}, ErrGraphQL},
		{"graphql 400 without errors", 0, func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{}`))
		}, ErrUnreachable},
		{"token 400", http.StatusBadRequest, nil, ErrAuthFailed},
		{"token 403", http.StatusForbidden, nil, ErrAuthFailed},
		{"token 429", http.StatusTooManyRequests, nil, ErrRateLimited},
		{"token 503", http.StatusServiceUnavailable, nil, ErrUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeTenant(t)
			if tc.token != 0 {
				status := tc.token
				ft.setToken(func(w http.ResponseWriter, n int32) bool { w.WriteHeader(status); return true })
			}
			if tc.graphql != nil {
				ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) { tc.graphql(w) })
			}
			c := ft.client(ft.source(), nil)
			if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDataDecoding(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		nilOut  bool
		wantErr error
	}{
		{"null data", `{"data":null}`, false, ErrUnreadable},
		{"missing data", `{}`, false, ErrUnreadable},
		{"mismatched data", `{"data":{"token":5}}`, false, ErrUnreadable},
		{"null data, no output wanted", `{"data":null}`, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeTenant(t)
			ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) { _, _ = w.Write([]byte(tc.body)) })
			c := ft.client(ft.source(), nil)
			var out tokenData
			var dst any = &out
			if tc.nilOut {
				dst = nil
			}
			err := c.Query(context.Background(), testQuery, dst)
			if (tc.wantErr == nil && err != nil) || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestGraphQLErrorCaps(t *testing.T) {
	msgs := make([]map[string]any, 0, maxGraphQLMessages+3)
	for i := range maxGraphQLMessages + 3 {
		msgs = append(msgs, map[string]any{"message": fmt.Sprintf("%d%s", i, strings.Repeat("x", maxGraphQLMessageLen*2))})
	}
	ft := newFakeTenant(t)
	ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, auth string) {
		writeJSON(w, map[string]any{"errors": msgs})
	})
	c := ft.client(ft.source(), nil)
	var ge *GraphQLError
	if err := c.Query(context.Background(), testQuery, nil); !errors.As(err, &ge) {
		t.Fatalf("err = %v, want *GraphQLError", err)
	}
	if len(ge.Messages) != maxGraphQLMessages {
		t.Fatalf("kept %d messages, want %d", len(ge.Messages), maxGraphQLMessages)
	}
	for _, m := range ge.Messages {
		if len(m) > maxGraphQLMessageLen {
			t.Fatalf("message of %d bytes kept, cap is %d", len(m), maxGraphQLMessageLen)
		}
	}
}

// TestRefreshFailureFallsBackToUnexpiredToken: a failed refresh inside the
// refresh margin keeps serving the still-valid token, and only an expired
// token surfaces the failure.
func TestRefreshFailureFallsBackToUnexpiredToken(t *testing.T) {
	ft := newFakeTenant(t)
	var failing atomic.Bool
	ft.setToken(func(w http.ResponseWriter, n int32) bool {
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return true
		}
		writeJSON(w, map[string]any{"access_token": fmt.Sprintf("tok-%d", n), "expires_in": 3600})
		return true
	})
	c := ft.client(ft.source(), nil)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	var out tokenData
	if err := c.Query(context.Background(), testQuery, &out); err != nil || out.Token != "tok-1" {
		t.Fatalf("first query: %v, %q", err, out.Token)
	}
	failing.Store(true)
	now = now.Add(57 * time.Minute) // inside the margin, token still valid
	if err := c.Query(context.Background(), testQuery, &out); err != nil || out.Token != "tok-1" {
		t.Fatalf("query inside the margin: %v, %q (want the unexpired tok-1)", err, out.Token)
	}
	now = now.Add(4 * time.Minute) // 61m: expired
	if err := c.Query(context.Background(), testQuery, &out); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("query after expiry err = %v, want ErrUnreachable", err)
	}
}

func TestTokenLifetimeIsCapped(t *testing.T) {
	ft := newFakeTenant(t)
	ft.setToken(func(w http.ResponseWriter, n int32) bool {
		writeJSON(w, map[string]any{"access_token": "tok", "expires_in": int64(1) << 62})
		return true
	})
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	tok, err := exchange(context.Background(), ft.srv.Client(), nil, now, ft.source().conn)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(maxTokenLifetime); !tok.expiresAt.Equal(want) {
		t.Fatalf("expiresAt = %s, want %s", tok.expiresAt, want)
	}
	if want := now.Add(maxTokenLifetime - tokenRefreshMargin); !tok.refreshAt.Equal(want) {
		t.Fatalf("refreshAt = %s, want %s", tok.refreshAt, want)
	}
}
