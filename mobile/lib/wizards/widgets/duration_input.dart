// Velero-style duration input. Free-text field that parses Go's
// time.Duration strings (`24h`, `7d`, `30m`, `1h30m`). Used by Velero
// Backup/Schedule (TTL field) and any other wizard that needs a
// duration string the backend will round-trip into Go time.
//
// Validation: empty is allowed (the field is optional in every
// callsite). Non-empty must match the duration grammar; invalid input
// surfaces as an inline error and the wizard's local validator can
// short-circuit the next-step advance.
//
// Note: Go's stdlib doesn't accept the `d` (days) suffix, but Velero's
// own parsing (k8s.io/apimachinery/pkg/util/duration) does. Accepting
// `d` matches operator expectations even though
// `time.ParseDuration("24h")` is what reaches Go.
//
// `0s` is a legal value — it disables expiry on a backup. Empty string
// is also legal and means "use Velero's default TTL".

import 'package:flutter/material.dart';

/// Validates a Velero/Go duration string. Returns null when [raw] is
/// empty (the field is optional everywhere it's used) or when [raw]
/// parses cleanly. Returns an error message otherwise.
String? validateDuration(String raw) {
  final s = raw.trim();
  if (s.isEmpty) return null;
  // Accepts one or more `<number><unit>` chunks. Units: ns, us, µs, ms,
  // s, m, h, d. Decimal numbers allowed.
  final re = RegExp(r'^(\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h|d))+$');
  if (!re.hasMatch(s)) {
    return 'Invalid duration. Use values like 30m, 24h, or 7d.';
  }
  return null;
}

/// Thin wrapper over [TextFormField] with duration-specific hint and
/// inline validation. Wizards bind it to a form field via [value] +
/// [onChanged] and surface server-routed errors via [errorText].
class DurationInput extends StatelessWidget {
  const DurationInput({
    super.key,
    required this.label,
    required this.value,
    required this.onChanged,
    this.hintText = 'e.g. 24h, 7d, 30m',
    this.errorText,
    this.helperText,
  });

  final String label;
  final String value;
  final ValueChanged<String> onChanged;
  final String hintText;
  final String? errorText;
  final String? helperText;

  @override
  Widget build(BuildContext context) {
    return TextFormField(
      initialValue: value,
      decoration: InputDecoration(
        labelText: label,
        hintText: hintText,
        helperText: helperText,
        border: const OutlineInputBorder(),
        errorText: errorText,
      ),
      onChanged: (v) => onChanged(v.trim()),
      keyboardType: TextInputType.text,
      autocorrect: false,
    );
  }
}
