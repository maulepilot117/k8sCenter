package wizard

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/pkg/api"
	sigsyaml "sigs.k8s.io/yaml"
)

// FieldError represents a single validation error for a specific field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Handler handles wizard preview HTTP endpoints.
type Handler struct {
	Logger *slog.Logger
}

// HandleDeploymentPreview handles POST /api/v1/wizards/deployment/preview.
// It validates the input, constructs a typed Deployment, and returns YAML.
func (h *Handler) HandleDeploymentPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	var input DeploymentInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if errs := input.Validate(); len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}

	dep, err := input.ToDeployment()
	if err != nil {
		httputil.WriteError(w, http.StatusUnprocessableEntity, err.Error(), "")
		return
	}
	yamlBytes, err := sigsyaml.Marshal(dep)
	if err != nil {
		h.Logger.Error("failed to marshal deployment to YAML", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to generate YAML", "")
		return
	}

	httputil.WriteData(w, map[string]string{"yaml": string(yamlBytes)})
}

// HandleServicePreview handles POST /api/v1/wizards/service/preview.
// It validates the input, constructs a typed Service, and returns YAML.
func (h *Handler) HandleServicePreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	var input ServiceInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if errs := input.Validate(); len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}

	svc := input.ToService()
	yamlBytes, err := sigsyaml.Marshal(svc)
	if err != nil {
		h.Logger.Error("failed to marshal service to YAML", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to generate YAML", "")
		return
	}

	httputil.WriteData(w, map[string]string{"yaml": string(yamlBytes)})
}

// HandleStorageClassPreview handles POST /api/v1/wizards/storageclass/preview.
// It validates the input, constructs a typed StorageClass, and returns YAML.
func (h *Handler) HandleStorageClassPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	var input StorageClassInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if errs := input.Validate(); len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}

	sc := input.ToStorageClass()
	yamlBytes, err := sigsyaml.Marshal(sc)
	if err != nil {
		h.Logger.Error("failed to marshal storage class to YAML", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to generate YAML", "")
		return
	}

	httputil.WriteData(w, map[string]string{"yaml": string(yamlBytes)})
}

func writeValidationErrors(w http.ResponseWriter, errs []FieldError) {
	httputil.WriteJSON(w, http.StatusUnprocessableEntity, api.Response{
		Error: &api.APIError{
			Code:    http.StatusUnprocessableEntity,
			Message: "validation failed",
		},
		Data: map[string]any{"fields": errs},
	})
}
