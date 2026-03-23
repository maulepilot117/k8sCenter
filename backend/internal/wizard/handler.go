package wizard

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// FieldError represents a single validation error for a specific field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// WizardInput is the contract all wizard input types must implement.
// Validate returns field-level errors. ToYAML returns the resource(s) as YAML.
type WizardInput interface {
	Validate() []FieldError
	ToYAML() (string, error)
}

// Handler handles wizard preview HTTP endpoints.
type Handler struct {
	Logger *slog.Logger
}

// HandlePreview returns a generic HTTP handler for any WizardInput type.
// The newInput factory creates a fresh instance per request (required for json.Decode).
func (h *Handler) HandlePreview(newInput func() WizardInput) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := httputil.RequireUser(w, r); !ok {
			return
		}

		input := newInput()
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(input); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
			return
		}

		if errs := input.Validate(); len(errs) > 0 {
			writeValidationErrors(w, errs)
			return
		}

		yaml, err := input.ToYAML()
		if err != nil {
			h.Logger.Error("failed to generate YAML preview", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to generate YAML", "")
			return
		}

		httputil.WriteData(w, map[string]string{"yaml": yaml})
	}
}

func writeValidationErrors(w http.ResponseWriter, errs []FieldError) {
	// Encode field errors as JSON in the Detail field to follow the
	// project convention of using Error only (never both Data and Error).
	detail, _ := json.Marshal(errs)
	httputil.WriteJSON(w, http.StatusUnprocessableEntity, api.Response{
		Error: &api.APIError{
			Code:    http.StatusUnprocessableEntity,
			Message: "validation failed",
			Detail:  string(detail),
		},
	})
}
