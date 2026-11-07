package resources

import (
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// timeNow is a function variable to allow testing with fixed times.
var timeNow = time.Now

// NoPreviousContainerMessage is what a log request for `previous=true` answers
// when the container has never restarted.
const NoPreviousContainerMessage = "this container has no previous instance: it has not restarted"

// IsNoPreviousContainer reports whether err is the API server refusing a
// `previous=true` log request because the container has no terminated
// instance. The API server answers that with a bare 400 whose only marker is
// its message ("previous terminated container \"x\" in pod \"y\" not found").
func IsNoPreviousContainer(err error) bool {
	return apierrors.IsBadRequest(err) &&
		strings.Contains(err.Error(), "previous terminated container")
}

// mapK8sError maps a Kubernetes API error to an HTTP status code and user-friendly message.
func mapK8sError(w http.ResponseWriter, err error, verb, kind, namespace, name string) {
	if statusErr, ok := err.(*apierrors.StatusError); ok {
		code := int(statusErr.Status().Code)
		switch {
		case apierrors.IsNotFound(err):
			writeError(w, http.StatusNotFound,
				kind+" '"+name+"' not found in namespace '"+namespace+"'",
				statusErr.Status().Message,
			)
		case apierrors.IsForbidden(err):
			writeError(w, http.StatusForbidden,
				"you do not have permission to "+verb+" "+kind+" '"+name+"'",
				statusErr.Status().Message,
			)
		case apierrors.IsAlreadyExists(err):
			writeError(w, http.StatusConflict,
				kind+" '"+name+"' already exists in namespace '"+namespace+"'",
				statusErr.Status().Message,
			)
		case apierrors.IsConflict(err):
			writeError(w, http.StatusConflict,
				"conflict updating "+kind+" '"+name+"' — resource was modified",
				statusErr.Status().Message,
			)
		case apierrors.IsInvalid(err):
			writeError(w, http.StatusUnprocessableEntity,
				"invalid "+kind+" specification",
				statusErr.Status().Message,
			)
		case apierrors.IsServiceUnavailable(err):
			writeError(w, http.StatusServiceUnavailable,
				"kubernetes API is unavailable",
				statusErr.Status().Message,
			)
		default:
			writeError(w, code, "kubernetes API error", statusErr.Status().Message)
		}
		return
	}

	writeError(w, http.StatusInternalServerError,
		"failed to "+verb+" "+kind,
		err.Error(),
	)
}
