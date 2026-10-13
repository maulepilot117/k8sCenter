package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// These tests pin KTD2's impersonation-rights check (#608): the one remote
// read made with the registration credential's own permissions. The router
// half stops at the config, because the SSRF dialer refuses the loopback
// httptest server; the review half runs against that server through a plain
// dialer and records exactly what the API server would see.

// sarServer answers SelfSubjectAccessReviews, allowing every review unless
// deny names its resource, and records each request and its attributes.
type sarServer struct {
	*httptest.Server
	mu      sync.Mutex
	headers []http.Header
	attrs   []authzv1.ResourceAttributes
	deny    string
	status  int
}

func newSARServer(t *testing.T) *sarServer {
	t.Helper()
	s := &sarServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var sar authzv1.SelfSubjectAccessReview
		_, _, _ = scheme.Codecs.UniversalDeserializer().Decode(body, nil, &sar)
		s.mu.Lock()
		s.headers = append(s.headers, r.Header.Clone())
		if sar.Spec.ResourceAttributes != nil {
			s.attrs = append(s.attrs, *sar.Spec.ResourceAttributes)
		}
		deny, status := s.deny, s.status
		s.mu.Unlock()
		if status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","code":%d}`, status)
			return
		}
		sar.Status.Allowed = sar.Spec.ResourceAttributes == nil || sar.Spec.ResourceAttributes.Resource != deny
		if !sar.Status.Allowed {
			sar.Status.Reason = "no RBAC policy matched"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&sar)
	}))
	t.Cleanup(s.Close)
	return s
}

// client builds a clientset the way CanImpersonate does, from
// NewRemoteRESTConfig with no impersonation, but dialing loopback.
func (s *sarServer) client(t *testing.T) kubernetes.Interface {
	t.Helper()
	cfg := NewRemoteRESTConfig(s.URL, "stored-cluster-token", serverCAPEM(s.Server))
	cfg.Dial = plainDial
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("NewForConfig: %v", err)
	}
	return cs
}

func (s *sarServer) recorded() ([]http.Header, []authzv1.ResourceAttributes) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.headers...), append([]authzv1.ResourceAttributes(nil), s.attrs...)
}

func TestCheckImpersonateRights_ReviewsUserAndEachGroupWithoutImpersonating(t *testing.T) {
	srv := newSARServer(t)
	if err := checkImpersonateRights(context.Background(), srv.client(t), "compliance-bot", []string{"auditors", "viewers"}); err != nil {
		t.Fatalf("checkImpersonateRights: %v", err)
	}
	headers, attrs := srv.recorded()
	want := []authzv1.ResourceAttributes{
		{Verb: "impersonate", Resource: "users", Name: "compliance-bot"},
		{Verb: "impersonate", Resource: "groups", Name: "auditors"},
		{Verb: "impersonate", Resource: "groups", Name: "viewers"},
	}
	if !reflect.DeepEqual(attrs, want) {
		t.Fatalf("reviews = %+v, want %+v", attrs, want)
	}
	for i, h := range headers {
		for name := range h {
			if strings.HasPrefix(http.CanonicalHeaderKey(name), "Impersonate-") {
				t.Errorf("review %d sent %s: the check must run as the registration credential", i, name)
			}
		}
		if got := h.Get("Authorization"); got != "Bearer stored-cluster-token" {
			t.Errorf("review %d Authorization = %q, want the stored cluster credential", i, got)
		}
	}
}

func TestCheckImpersonateRights_ServiceAccountReviewsServiceAccounts(t *testing.T) {
	srv := newSARServer(t)
	if err := checkImpersonateRights(context.Background(), srv.client(t), "system:serviceaccount:policy-audit:reader", nil); err != nil {
		t.Fatalf("checkImpersonateRights: %v", err)
	}
	_, attrs := srv.recorded()
	want := []authzv1.ResourceAttributes{
		{Verb: "impersonate", Resource: "serviceaccounts", Namespace: "policy-audit", Name: "reader"},
	}
	if !reflect.DeepEqual(attrs, want) {
		t.Fatalf("reviews = %+v, want %+v", attrs, want)
	}
}

func TestImpersonationReviews_MalformedServiceAccountIsAUser(t *testing.T) {
	for _, name := range []string{"system:serviceaccount:ns", "system:serviceaccount::reader", "system:serviceaccount:ns:", "system:serviceaccount:a:b:c"} {
		attrs := impersonationReviews(name, nil)
		if len(attrs) != 1 || attrs[0].Resource != "users" || attrs[0].Name != name {
			t.Errorf("impersonationReviews(%q) = %+v, want one users review", name, attrs)
		}
	}
}

func TestCheckImpersonateRights_DeniedIsSentinel(t *testing.T) {
	for _, deny := range []string{"users", "groups"} {
		srv := newSARServer(t)
		srv.deny = deny
		err := checkImpersonateRights(context.Background(), srv.client(t), "compliance-bot", []string{"auditors"})
		if !errors.Is(err, ErrImpersonationDenied) {
			t.Errorf("deny %s: err = %v, want ErrImpersonationDenied", deny, err)
		}
		var te TargetError
		if errors.As(err, &te) {
			t.Errorf("deny %s: a denied review must not read as a target failure", deny)
		}
	}
}

func TestCheckImpersonateRights_TransportFailuresClassify(t *testing.T) {
	srv := newSARServer(t)
	srv.status = http.StatusUnauthorized
	err := checkImpersonateRights(context.Background(), srv.client(t), "compliance-bot", nil)
	var te TargetError
	if !errors.As(err, &te) || errors.Is(err, ErrImpersonationDenied) {
		t.Fatalf("401: err = %v, want a TargetError", err)
	}
	if got := ClassifyTargetErr(err); got != ReasonCredentialsInvalid {
		t.Errorf("401 classified %q, want %q", got, ReasonCredentialsInvalid)
	}

	dead := newSARServer(t)
	cs := dead.client(t)
	dead.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = checkImpersonateRights(ctx, cs, "compliance-bot", nil)
	if !errors.As(err, &te) {
		t.Fatalf("closed server: err = %v, want a TargetError", err)
	}
	if got := ClassifyTargetErr(err); got != ReasonUnreachable {
		t.Errorf("closed server classified %q, want %q", got, ReasonUnreachable)
	}
}

func TestRegistrationConfig_NoImpersonationStrictDialer(t *testing.T) {
	rec := testClusterRecord(t, time.Now())
	router := newRemoteTestRouter(&fakeClusterGetter{record: rec})
	cfg, err := router.registrationConfig(context.Background(), "remote-1")
	if err != nil {
		t.Fatalf("registrationConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg.Impersonate, rest.ImpersonationConfig{}) {
		t.Errorf("Impersonate = %+v, want none: the check runs as the registration credential", cfg.Impersonate)
	}
	if cfg.BearerToken != "fake-bearer-token" || cfg.Host != rec.APIServerURL {
		t.Errorf("config = host %q token %q, want the registered server and stored credential", cfg.Host, cfg.BearerToken)
	}
	if reflect.ValueOf(cfg.Dial).Pointer() != reflect.ValueOf(StrictDialContext).Pointer() {
		t.Error("registration config does not dial through StrictDialContext")
	}
	if !cfg.TLSClientConfig.Insecure {
		t.Error("the cluster's TLS policy (AllowInsecureTLS, no CA) was not applied")
	}

	// A CA-less cluster that did not opt in to insecure TLS is refused, as
	// the impersonating router refuses it.
	strict := *rec
	strict.AllowInsecureTLS = false
	if _, err := newRemoteTestRouter(&fakeClusterGetter{record: &strict}).registrationConfig(context.Background(), "remote-1"); err == nil {
		t.Error("registrationConfig built a CA-less config without AllowInsecureTLS")
	}
}

func TestCanImpersonate_RefusesLocalAnonymousAndUnavailableTargets(t *testing.T) {
	ctx := context.Background()
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})
	for _, id := range []string{"", "local"} {
		if err := router.CanImpersonate(ctx, id, "alice", nil); err == nil {
			t.Errorf("CanImpersonate(%q) = nil, want a refusal: the check is remote-only", id)
		}
	}
	if err := router.CanImpersonate(ctx, "remote-1", "", []string{"eng"}); err == nil {
		t.Error("CanImpersonate with no username = nil, want a refusal")
	}

	var te TargetError
	err := newRemoteTestRouter(nil).CanImpersonate(ctx, "remote-1", "alice", nil)
	if !errors.As(err, &te) || ClassifyTargetErr(err) != ReasonDBUnavailable {
		t.Errorf("no cluster store: err = %v (%q), want a db_unavailable TargetError", err, ClassifyTargetErr(err))
	}

	err = newRemoteTestRouter(&fakeClusterGetter{err: pgx.ErrNoRows}).CanImpersonate(ctx, "remote-1", "alice", nil)
	if !errors.As(err, &te) || ClassifyTargetErr(err) != ReasonClusterUnknown {
		t.Errorf("missing cluster: err = %v (%q), want a cluster_unknown TargetError", err, ClassifyTargetErr(err))
	}
}
