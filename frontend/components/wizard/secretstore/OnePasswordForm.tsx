import { useSignal } from "@preact/signals";
import { Input } from "@/components/ui/Input.tsx";

/**
 * 1Password Connect provider form for SecretStoreWizard.
 *
 * ESO provider key: "onepassword" (corrected from the U18 placeholder
 * "onepasswordsdk" — ESO v1beta1 has no "onepasswordsdk" key; the only
 * 1Password provider is the Connect-based one).
 *
 * Writes into the wizard's `providerSpec: Record<string, unknown>` under:
 *   {
 *     connectHost: string,               // HTTPS URL of the Connect server
 *     auth: {
 *       secretRef: {
 *         connectTokenSecretRef: { name: string, key: string }
 *       }
 *     },
 *     vaults: Record<string, number>     // vault name → search priority
 *   }
 *
 * The vaults section is driven by a dynamic list editor (add / remove rows)
 * since the provider accepts multiple vaults and uses the integer priority to
 * determine search order (lower integer = higher priority).
 */

export interface OnePasswordFormProps {
  spec: Record<string, unknown>;
  errors: Record<string, string>;
  onUpdateSpec: (spec: Record<string, unknown>) => void;
}

/** A single entry in the vaults editor. */
interface VaultEntry {
  name: string;
  priority: string; // kept as string for the input; parsed to int on emit
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function getStr(spec: Record<string, unknown>, key: string): string {
  const v = spec[key];
  return typeof v === "string" ? v : "";
}

function getTokenRef(
  spec: Record<string, unknown>,
): Record<string, string> {
  const auth = spec.auth as Record<string, unknown> | undefined;
  const secretRef = auth?.secretRef as Record<string, unknown> | undefined;
  const tokenRef = secretRef?.connectTokenSecretRef as
    | Record<string, string>
    | undefined;
  return tokenRef ?? {};
}

/** Read the vaults map from the spec and convert to editor rows. */
function vaultsToRows(spec: Record<string, unknown>): VaultEntry[] {
  const raw = spec.vaults as Record<string, unknown> | undefined;
  if (!raw || typeof raw !== "object") return [];
  return Object.entries(raw).map(([name, priority]) => ({
    name,
    priority: String(priority ?? ""),
  }));
}

/** Convert editor rows back to the map shape ESO expects. */
function rowsToVaultsMap(rows: VaultEntry[]): Record<string, number> {
  const out: Record<string, number> = {};
  for (const row of rows) {
    if (row.name.trim() === "") continue;
    const p = parseInt(row.priority, 10);
    out[row.name.trim()] = isNaN(p) ? 1 : p;
  }
  return out;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function OnePasswordForm(
  { spec, errors, onUpdateSpec }: OnePasswordFormProps,
) {
  // Vault rows managed locally as strings to preserve the in-progress input
  // before it's converted to a number. Initialised once from spec; subsequent
  // spec changes come back through onUpdateSpec.
  const initialRows = vaultsToRows(spec);
  const rows = useSignal<VaultEntry[]>(
    initialRows.length > 0 ? initialRows : [{ name: "", priority: "1" }],
  );

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function patchTokenRef(patch: Record<string, string>) {
    const auth = (spec.auth as Record<string, unknown>) ?? {};
    const secretRef = (auth.secretRef as Record<string, unknown>) ?? {};
    const existing =
      (secretRef.connectTokenSecretRef as Record<string, string>) ??
        {};
    onUpdateSpec({
      ...spec,
      auth: {
        ...auth,
        secretRef: {
          ...secretRef,
          connectTokenSecretRef: { ...existing, ...patch },
        },
      },
    });
  }

  function commitRows(updated: VaultEntry[]) {
    rows.value = updated;
    const vaultsMap = rowsToVaultsMap(updated);
    onUpdateSpec({ ...spec, vaults: vaultsMap });
  }

  function updateRow(idx: number, patch: Partial<VaultEntry>) {
    const updated = rows.value.map((r, i) =>
      i === idx ? { ...r, ...patch } : r
    );
    commitRows(updated);
  }

  function addRow() {
    commitRows([...rows.value, { name: "", priority: "1" }]);
  }

  function removeRow(idx: number) {
    const updated = rows.value.filter((_, i) => i !== idx);
    commitRows(updated.length > 0 ? updated : [{ name: "", priority: "1" }]);
  }

  const tokenRef = getTokenRef(spec);

  return (
    <div class="space-y-5">
      <div class="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        Configure the 1Password Connect server connection and credentials. The
        Connect token must already exist as a Kubernetes Secret — this wizard
        only references it and never holds credentials directly.
      </div>

      {/* Connect server URL */}
      <Input
        id="op-connect-host"
        label="Connect server URL"
        required
        value={getStr(spec, "connectHost")}
        onInput={(e) =>
          patchTop("connectHost", (e.target as HTMLInputElement).value)}
        placeholder="https://connect.example.com:8080"
        description="Must use https. Private and in-cluster addresses are accepted."
        error={errors["connectHost"]}
      />

      {/* Connect token secret reference */}
      <div class="rounded-md border border-border-primary p-4 space-y-3">
        <h3 class="text-sm font-semibold text-text-primary">
          Connect token Secret reference
          <span aria-hidden="true" class="text-danger ml-0.5">*</span>
        </h3>
        <p class="text-xs text-text-muted">
          Reference to the Kubernetes Secret that holds the 1Password Connect
          API token (
          <code class="font-mono">auth.secretRef.connectTokenSecretRef</code>).
        </p>
        {errors["auth"] && <p class="text-sm text-danger">{errors["auth"]}</p>}
        {errors["auth.secretRef"] && (
          <p class="text-sm text-danger">{errors["auth.secretRef"]}</p>
        )}
        {errors["auth.secretRef.connectTokenSecretRef"] && (
          <p class="text-sm text-danger">
            {errors["auth.secretRef.connectTokenSecretRef"]}
          </p>
        )}
        <div class="grid grid-cols-2 gap-3">
          <Input
            id="op-token-ref-name"
            label="Secret name"
            required
            value={tokenRef.name ?? ""}
            onInput={(e) =>
              patchTokenRef({ name: (e.target as HTMLInputElement).value })}
            placeholder="op-connect-token"
            error={errors["auth.secretRef.connectTokenSecretRef.name"]}
          />
          <Input
            id="op-token-ref-key"
            label="Key"
            required
            value={tokenRef.key ?? ""}
            onInput={(e) =>
              patchTokenRef({ key: (e.target as HTMLInputElement).value })}
            placeholder="token"
            description="The key within the Secret that contains the token value."
            error={errors["auth.secretRef.connectTokenSecretRef.key"]}
          />
        </div>
      </div>

      {/* Vaults map */}
      <div class="rounded-md border border-border-primary p-4 space-y-3">
        <h3 class="text-sm font-semibold text-text-primary">
          Vaults
          <span aria-hidden="true" class="text-danger ml-0.5">*</span>
        </h3>
        <p class="text-xs text-text-muted">
          Map each 1Password vault name to a search priority. ESO searches
          vaults in ascending priority order (lower number = searched first). At
          least one entry is required.
        </p>
        {errors["vaults"] && (
          <p class="text-sm text-danger">{errors["vaults"]}</p>
        )}

        <div class="space-y-2">
          {/* Header row */}
          <div class="grid grid-cols-[1fr_7rem_2.5rem] gap-2 px-1">
            <span class="text-xs font-medium text-text-muted uppercase tracking-wide">
              Vault name
            </span>
            <span class="text-xs font-medium text-text-muted uppercase tracking-wide">
              Priority
            </span>
            <span />
          </div>

          {rows.value.map((row, idx) => (
            <div
              key={idx}
              class="grid grid-cols-[1fr_7rem_2.5rem] gap-2 items-start"
            >
              <Input
                id={`op-vault-name-${idx}`}
                label=""
                value={row.name}
                onInput={(e) =>
                  updateRow(idx, {
                    name: (e.target as HTMLInputElement).value,
                  })}
                placeholder="production"
              />
              <Input
                id={`op-vault-priority-${idx}`}
                label=""
                value={row.priority}
                onInput={(e) =>
                  updateRow(idx, {
                    priority: (e.target as HTMLInputElement).value,
                  })}
                placeholder="1"
              />
              <button
                type="button"
                aria-label={`Remove vault row ${idx + 1}`}
                onClick={() => removeRow(idx)}
                disabled={rows.value.length === 1}
                class="mt-1 flex h-8 w-8 items-center justify-center rounded-md border border-border-primary text-text-muted hover:border-danger hover:text-danger disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
              >
                ×
              </button>
            </div>
          ))}
        </div>

        <button
          type="button"
          onClick={addRow}
          class="text-sm text-brand hover:text-brand/80 transition-colors"
        >
          + Add vault
        </button>
      </div>
    </div>
  );
}
