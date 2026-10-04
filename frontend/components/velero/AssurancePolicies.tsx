/** Backup assurance — freshness policy form model, editor and table
 * (Release F U36). Admin only; the island decides whether to render them.
 *
 * The form holds minutes as typed; the local checks mirror the handler's and
 * the server stays authoritative.
 */

import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { Select } from "@/components/ui/Select.tsx";
import {
  minutesToSeconds,
  type PolicyForm,
} from "@/lib/assurance-policy-form.ts";
import {
  type AssurancePolicy,
  type AssuranceScopeKind,
  formatSeconds,
  MIN_MAX_AGE_SECONDS,
  scopeLabel,
  type TreatPartialAs,
} from "@/lib/backup-assurance-types.ts";

// Re-exported so the island's existing imports keep working.
export {
  emptyForm,
  FIELD_LABELS,
  formFromPolicy,
  localFieldErrors,
  minutesToSeconds,
  type PolicyForm,
} from "@/lib/assurance-policy-form.ts";

const SCOPE_OPTIONS = [
  { value: "schedule", label: "Schedule" },
  { value: "namespace", label: "Namespace" },
  { value: "cluster", label: "Cluster" },
];

const PARTIAL_OPTIONS = [
  { value: "failure", label: "Failure (opens an exception)" },
  {
    value: "success",
    label: "Success (still shown as partial, never as a plain success)",
  },
];

export function PolicyEditor({
  editing,
  form,
  fieldErrors,
  formError,
  conflict,
  saving,
  onChange,
  onSubmit,
  onCancel,
  onReload,
}: {
  editing: AssurancePolicy | null;
  form: PolicyForm;
  fieldErrors: Record<string, string>;
  formError: string | null;
  conflict: boolean;
  saving: boolean;
  onChange: (patch: Partial<PolicyForm>) => void;
  onSubmit: () => void;
  onCancel: () => void;
  onReload: () => void;
}) {
  const creating = editing === null;
  const maxAgeSec = minutesToSeconds(form.maxAgeMinutes);
  const graceSec = minutesToSeconds(form.graceMinutes);
  const value = (e: Event) =>
    (e.target as HTMLInputElement | HTMLSelectElement).value;
  return (
    <form
      class="rounded-lg border border-border-subtle bg-surface p-4"
      data-testid="assurance-policy-form"
      aria-label={creating ? "New freshness policy" : "Edit freshness policy"}
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
    >
      <h3 class="text-sm font-semibold text-text-primary">
        {creating
          ? "New freshness policy"
          : `Edit policy for ${scopeLabel(
              editing.scopeKind,
              editing.scopeNamespace,
              editing.scopeName,
            )}`}
      </h3>
      {!creating && (
        <p class="mt-1 text-xs text-text-muted">
          A policy's scope cannot be changed. To watch a different scope, create
          a new policy.
        </p>
      )}
      <div class="mt-3 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Select
          id="assurance-policy-scope-kind"
          label="Scope"
          options={SCOPE_OPTIONS}
          value={form.scopeKind}
          disabled={!creating}
          error={fieldErrors.scopeKind}
          onChange={(e) =>
            onChange({ scopeKind: value(e) as AssuranceScopeKind })
          }
        />
        {form.scopeKind !== "cluster" && (
          <Input
            id="assurance-policy-namespace"
            label="Namespace"
            value={form.scopeNamespace}
            disabled={!creating}
            error={fieldErrors.scopeNamespace}
            onInput={(e) => onChange({ scopeNamespace: value(e) })}
          />
        )}
        {form.scopeKind === "schedule" && (
          <Input
            id="assurance-policy-schedule"
            label="Schedule"
            value={form.scopeName}
            disabled={!creating}
            error={fieldErrors.scopeName}
            onInput={(e) => onChange({ scopeName: value(e) })}
          />
        )}
      </div>
      <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Input
          id="assurance-policy-max-age"
          label="Maximum age (minutes)"
          type="number"
          min={MIN_MAX_AGE_SECONDS / 60}
          step="any"
          value={form.maxAgeMinutes}
          error={fieldErrors.maxAgeSeconds}
          description={
            maxAgeSec !== null && maxAgeSec > 0
              ? `${formatSeconds(maxAgeSec)} without a successful backup opens an exception (after grace).`
              : `At least ${MIN_MAX_AGE_SECONDS / 60} minutes.`
          }
          onInput={(e) => onChange({ maxAgeMinutes: value(e) })}
        />
        <Input
          id="assurance-policy-grace"
          label="Grace (minutes)"
          type="number"
          min={0}
          step="any"
          value={form.graceMinutes}
          error={fieldErrors.graceSeconds}
          description={
            graceSec !== null && graceSec >= 0
              ? `${formatSeconds(graceSec)} of slack for late runs and clock changes.`
              : "Slack for late runs and clock changes."
          }
          onInput={(e) => onChange({ graceMinutes: value(e) })}
        />
        <Select
          id="assurance-policy-partial"
          label="Treat PartiallyFailed as"
          options={PARTIAL_OPTIONS}
          value={form.treatPartialAs}
          error={fieldErrors.treatPartialAs}
          onChange={(e) =>
            onChange({ treatPartialAs: value(e) as TreatPartialAs })
          }
        />
      </div>
      <div class="mt-4 flex flex-wrap gap-6 text-sm text-text-secondary">
        <label class="inline-flex items-center gap-2">
          <input
            type="checkbox"
            checked={form.alertOnPaused}
            onChange={(e) =>
              onChange({
                alertOnPaused: (e.target as HTMLInputElement).checked,
              })
            }
          />
          Open an exception when the schedule is paused
        </label>
        <label class="inline-flex items-center gap-2">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) =>
              onChange({ enabled: (e.target as HTMLInputElement).checked })
            }
          />
          Enabled
        </label>
      </div>
      {formError && (
        <div
          class="mt-4"
          role="alert"
          data-testid="assurance-policy-form-error"
        >
          <Alert variant="error">
            {formError}
            {conflict && (
              <Button
                type="button"
                variant="secondary"
                size="sm"
                class="ml-3"
                onClick={onReload}
              >
                Reload policy
              </Button>
            )}
          </Alert>
        </div>
      )}
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" loading={saving} disabled={conflict}>
          {creating ? "Create policy" : "Save changes"}
        </Button>
      </div>
    </form>
  );
}

function scheduleStatusNote(p: AssurancePolicy): string | null {
  switch (p.scheduleStatus) {
    case "not_found":
      return `${p.scheduleNote ?? "schedule not found"} — this policy currently watches nothing`;
    case "unknown":
      return "schedule existence unknown — Velero could not be read";
    default:
      return null;
  }
}

export function PolicyTable({
  policies,
  busy,
  onEdit,
  onDelete,
}: {
  policies: AssurancePolicy[];
  busy: boolean;
  onEdit: (p: AssurancePolicy) => void;
  onDelete: (p: AssurancePolicy) => void;
}) {
  return (
    <div class="overflow-x-auto rounded-lg border border-border-subtle bg-surface">
      <table class="w-full text-left text-sm">
        <thead class="border-b border-border-subtle text-xs uppercase tracking-wide text-text-muted">
          <tr>
            <th scope="col" class="px-4 py-2 font-medium">
              Scope
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              Max age
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              Grace
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              PartiallyFailed
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              Paused
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              State
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              <span class="sr-only">Actions</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {policies.map((p) => {
            const note = scheduleStatusNote(p);
            return (
              <tr
                key={p.id}
                class="border-b border-border-subtle last:border-b-0"
                data-testid="assurance-policy-row"
              >
                <td class="px-4 py-2">
                  <span class="break-all font-mono text-text-primary">
                    {scopeLabel(p.scopeKind, p.scopeNamespace, p.scopeName)}
                  </span>
                  {note && (
                    <span class="block text-xs text-warning">{note}</span>
                  )}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  {formatSeconds(p.maxAgeSeconds)}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  {formatSeconds(p.graceSeconds)}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  as {p.treatPartialAs}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  {p.alertOnPaused ? "alert" : "ignore"}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  {p.enabled ? "enabled" : "disabled"}
                </td>
                <td class="whitespace-nowrap px-4 py-2 text-right">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
                    onClick={() => onEdit(p)}
                  >
                    Edit
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    class="text-error"
                    disabled={busy}
                    onClick={() => onDelete(p)}
                  >
                    Delete
                  </Button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
