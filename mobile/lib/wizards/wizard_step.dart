// Wizard step model + per-step field-error map.
//
// Mirrors the shape of `frontend/components/wizard/WizardStepper.tsx`'s
// `WizardStep` plus the per-step `errors` slot the controller fills when
// /v1/wizards/:type/preview returns a 422 with field-level details.

/// One row in the wizard's stepper. Title is shown on tablet's horizontal
/// stepper; description renders as a subtitle on phone's vertical
/// stepper.
class WizardStep {
  const WizardStep({required this.title, this.description});

  final String title;
  final String? description;
}

/// Field-error returned by /v1/wizards/:type/preview on 422.
///
/// Backend shape (see `backend/internal/wizard/handler.go:HandlePreview`):
///   { "field": "spec.template.spec.containers[0].image",
///     "message": "must be a non-empty string" }
class WizardFieldError {
  const WizardFieldError({required this.field, required this.message});

  factory WizardFieldError.fromJson(Map<String, dynamic> json) {
    return WizardFieldError(
      field: json['field'] as String? ?? '',
      message: json['message'] as String? ?? '',
    );
  }

  final String field;
  final String message;
}

/// Per-step error map: server field path → human message. Step widgets
/// consume their own slice of this map and render messages under the
/// relevant inputs.
typedef StepFieldErrors = Map<String, String>;
