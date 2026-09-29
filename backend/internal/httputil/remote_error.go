package httputil

import (
	"errors"
	"log/slog"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// Remote-cluster failures are answered with a fixed message and a reason
// from k8s.ReasonCode, never with the underlying error: a remote client's
// error text carries the API server's host and address, and sometimes
// credential-handling detail. The raw error is logged instead.

// targetFailure is the response for one class of target-resolution failure.
type targetFailure struct {
	status  int
	message string
}

var targetFailures = map[k8s.ReasonCode]targetFailure{
	k8s.ReasonClusterUnknown:     {http.StatusNotFound, "the selected cluster is not registered"},
	k8s.ReasonDBUnavailable:      {http.StatusServiceUnavailable, "the cluster registry is unavailable"},
	k8s.ReasonUnreachable:        {http.StatusBadGateway, "the selected cluster could not be reached"},
	k8s.ReasonCredentialsInvalid: {http.StatusBadGateway, "could not connect to the selected cluster with its stored credentials"},
}

// WriteTargetError answers a failure to resolve a client or schema for the
// selected cluster (ClusterClients.ClientForCluster, DynamicClientForCluster
// or TargetSchemaFor).
func WriteTargetError(w http.ResponseWriter, err error) {
	reason := k8s.ClassifyTargetErr(err)
	f, ok := targetFailures[reason]
	if !ok {
		f = targetFailures[k8s.ReasonCredentialsInvalid]
	}
	slog.Warn("cluster target resolution failed", "reason", reason, "error", err)
	WriteErrorWithReason(w, f.status, f.message, string(reason), nil)
}

// remoteStatusMessages are the fixed messages for Kubernetes status errors
// returned by a resolved remote client, by HTTP status.
var remoteStatusMessages = map[int]string{
	http.StatusBadRequest:          "the cluster rejected the request",
	http.StatusUnauthorized:        "the cluster did not accept the impersonated identity",
	http.StatusForbidden:           "the cluster denied this request",
	http.StatusNotFound:            "not found on the cluster",
	http.StatusConflict:            "the object changed on the cluster; reload and try again",
	http.StatusUnprocessableEntity: "the cluster rejected the request as invalid",
	http.StatusTooManyRequests:     "the cluster is throttling requests; try again shortly",
}

// WriteRemoteError answers a failure of a call made through an already
// resolved remote client. A Kubernetes status error keeps its HTTP status
// with a fixed message; a transport failure is 502 unreachable; anything
// else is a 502 with no reason.
func WriteRemoteError(w http.ResponseWriter, err error) {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		code := int(status.Status().Code)
		msg, known := remoteStatusMessages[code]
		if !known {
			code, msg = http.StatusBadGateway, "the cluster returned an error"
		}
		reason := ""
		if code == http.StatusForbidden {
			reason = string(k8s.ReasonForbidden)
		}
		slog.Warn("remote cluster call failed", "status", status.Status().Code, "error", err)
		WriteErrorWithReason(w, code, msg, reason, nil)
		return
	}

	if k8s.ClassifyTargetErr(err) == k8s.ReasonUnreachable {
		slog.Warn("remote cluster unreachable", "error", err)
		WriteErrorWithReason(w, http.StatusBadGateway, targetFailures[k8s.ReasonUnreachable].message, string(k8s.ReasonUnreachable), nil)
		return
	}

	slog.Warn("remote cluster call failed", "error", err)
	WriteErrorWithReason(w, http.StatusBadGateway, "the cluster request failed", "", nil)
}
