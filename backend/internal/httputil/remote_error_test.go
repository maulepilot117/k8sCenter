package httputil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// leakyHost is an address that must never reach a response body.
const leakyHost = "10.20.30.40"

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) *api.APIError {
	t.Helper()
	var resp api.Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if resp.Error == nil {
		t.Fatalf("no error envelope in %q", rr.Body.String())
	}
	return resp.Error
}

func assertNoLeak(t *testing.T, rr *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	body := rr.Body.String()
	for _, s := range secrets {
		if strings.Contains(body, s) {
			t.Errorf("response leaks %q: %s", s, body)
		}
	}
}

func transportErr() error {
	return &url.Error{Op: "Get", URL: "https://" + leakyHost + ":6443/apis", Err: &net.OpError{
		Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused"),
	}}
}

func TestWriteTargetError_StatusAndReasonPerClass(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		reason k8s.ReasonCode
	}{
		{"unknown cluster", fmt.Errorf("get cluster: %w", pgx.ErrNoRows), http.StatusNotFound, k8s.ReasonClusterUnknown},
		{"registry down", &pgconn.PgError{Code: "57P01", Message: "terminating connection"}, http.StatusServiceUnavailable, k8s.ReasonDBUnavailable},
		{"unreachable", fmt.Errorf("build config: %w", transportErr()), http.StatusBadGateway, k8s.ReasonUnreachable},
		{"timed out", fmt.Errorf("discovery: %w", context.DeadlineExceeded), http.StatusBadGateway, k8s.ReasonUnreachable},
		{"credentials", errors.New("decrypt credentials for " + leakyHost + ": cipher: message authentication failed"), http.StatusBadGateway, k8s.ReasonCredentialsInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			WriteTargetError(rr, tt.err)

			if rr.Code != tt.status {
				t.Errorf("status = %d, want %d", rr.Code, tt.status)
			}
			e := decodeError(t, rr)
			if e.Reason != string(tt.reason) {
				t.Errorf("reason = %q, want %q", e.Reason, tt.reason)
			}
			if e.Detail != "" {
				t.Errorf("detail = %q, want empty", e.Detail)
			}
			if strings.Contains(e.Message, tt.err.Error()) {
				t.Errorf("message echoes the raw error: %q", e.Message)
			}
			assertNoLeak(t, rr, leakyHost, "cipher", "connection refused")
		})
	}
}

func TestWriteRemoteError_TransportFailureIsUnreachableWithoutAddress(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteRemoteError(rr, transportErr())

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
	if e := decodeError(t, rr); e.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("reason = %q, want unreachable", e.Reason)
	}
	assertNoLeak(t, rr, leakyHost, "6443", "connection refused")
}

func TestWriteRemoteError_KubernetesStatusKeepsItsCode(t *testing.T) {
	gr := schema.GroupResource{Group: "argoproj.io", Resource: "applications"}
	tests := []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{"forbidden", apierrors.NewForbidden(gr, "prod-app", errors.New("user alice on "+leakyHost+" cannot patch")), http.StatusForbidden, string(k8s.ReasonForbidden)},
		{"not found", apierrors.NewNotFound(gr, "prod-app"), http.StatusNotFound, ""},
		{"conflict", apierrors.NewConflict(gr, "prod-app", errors.New("object was modified")), http.StatusConflict, ""},
		{"invalid", apierrors.NewInvalid(schema.GroupKind{Group: "argoproj.io", Kind: "Application"}, "prod-app", nil), http.StatusUnprocessableEntity, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			WriteRemoteError(rr, tt.err)

			if rr.Code != tt.status {
				t.Errorf("status = %d, want %d", rr.Code, tt.status)
			}
			e := decodeError(t, rr)
			if e.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", e.Reason, tt.reason)
			}
			if e.Detail != "" {
				t.Errorf("detail = %q, want empty", e.Detail)
			}
			assertNoLeak(t, rr, leakyHost, tt.err.Error())
		})
	}
}

func TestWriteRemoteError_UnclassifiedFailureIs502WithoutText(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteRemoteError(rr, errors.New("unexpected EOF reading from "+leakyHost))

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
	assertNoLeak(t, rr, leakyHost, "unexpected EOF")
}
