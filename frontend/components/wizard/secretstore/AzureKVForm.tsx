import { useSignal } from "@preact/signals";
import { Input } from "@/components/ui/Input.tsx";

/**
 * Azure Key Vault provider form for SecretStoreWizard. Writes into the
 * wizard's `providerSpec: Record<string, unknown>` slot under the shape
 * `{ vaultUrl, authType, tenantId?, authSecretRef?, serviceAccountRef?,
 *    identityId? }`.
 *
 * Azure's auth discriminator is a top-level `authType` string, NOT a nested
 * auth sub-block like Vault. Three auth types are supported:
 *
 * - ManagedIdentity: no required credentials; optional identityId.
 * - ServicePrincipal: tenantId + authSecretRef.{clientId, clientSecret}.
 * - WorkloadIdentity: tenantId + serviceAccountRef.name.
 *
 * Switching authType clears the auth-related fields so stale values from the
 * prior selection can't leak into the preview.
 */

type AzureKVAuthType =
  | "ManagedIdentity"
  | "ServicePrincipal"
  | "WorkloadIdentity";

export interface AzureKVFormProps {
  spec: Record<string, unknown>;
  errors: Record<string, string>;
  onUpdateSpec: (spec: Record<string, unknown>) => void;
}

interface SecretRef {
  name?: string;
  key?: string;
}

interface AzureAuthSecretRef {
  clientId?: SecretRef;
  clientSecret?: SecretRef;
}

/** Typed shape for the Azure KV provider spec block (spec.provider.azurekv). */
interface AzureKVSpec {
  vaultUrl?: string;
  tenantId?: string;
  authType?: AzureKVAuthType;
  /** ServicePrincipal: references to clientId and clientSecret Secrets. */
  authSecretRef?: AzureAuthSecretRef;
  /** WorkloadIdentity: the service account that carries the federated identity. */
  serviceAccountRef?: { name?: string };
  /** ManagedIdentity: optional client ID when multiple MIs are assigned to the pod. */
  identityId?: string;
}

const AUTH_TYPES: {
  id: AzureKVAuthType;
  label: string;
  description: string;
}[] = [
  {
    id: "ManagedIdentity",
    label: "Managed Identity",
    description:
      "Use the AKS-assigned managed identity. No credentials required.",
  },
  {
    id: "ServicePrincipal",
    label: "Service Principal",
    description:
      "App registration with client ID + secret stored in a K8s Secret.",
  },
  {
    id: "WorkloadIdentity",
    label: "Workload Identity",
    description:
      "AKS Workload Identity via a federated service account (no long-lived secret).",
  },
];

/** Determine which auth type the spec currently encodes, or "" when none. */
function detectAuthType(spec: Record<string, unknown>): AzureKVAuthType | "" {
  const v = spec.authType;
  if (
    v === "ManagedIdentity" || v === "ServicePrincipal" ||
    v === "WorkloadIdentity"
  ) {
    return v;
  }
  return "";
}

/** Read a top-level string field from the spec. */
function getStr(spec: Record<string, unknown>, key: string): string {
  const v = spec[key];
  return typeof v === "string" ? v : "";
}

/**
 * Auth-related field keys that must be cleared when the auth type changes.
 * Ensures stale credentials from a prior selection don't leak into the preview.
 */
const AUTH_TYPE_OWNED_FIELDS: ReadonlyArray<keyof AzureKVSpec> = [
  "tenantId",
  "authSecretRef",
  "serviceAccountRef",
  "identityId",
];

export function AzureKVForm({ spec, errors, onUpdateSpec }: AzureKVFormProps) {
  const authType = useSignal<AzureKVAuthType | "">(detectAuthType(spec));

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function setAuthType(t: AzureKVAuthType) {
    if (authType.value === t) return;
    authType.value = t;
    // Clear all auth-type-owned fields so old credentials don't bleed through.
    const next = { ...spec };
    for (const f of AUTH_TYPE_OWNED_FIELDS) {
      delete next[f as string];
    }
    next.authType = t;
    onUpdateSpec(next);
  }

  function patchAuthSecretRef(
    field: "clientId" | "clientSecret",
    patch: SecretRef,
  ) {
    const existing = (spec.authSecretRef as AzureAuthSecretRef) ?? {};
    const existingRef = (existing[field] as SecretRef) ?? {};
    onUpdateSpec({
      ...spec,
      authSecretRef: {
        ...existing,
        [field]: { ...existingRef, ...patch },
      },
    });
  }

  function patchServiceAccountRef(patch: { name?: string }) {
    const existing = (spec.serviceAccountRef as { name?: string }) ?? {};
    onUpdateSpec({
      ...spec,
      serviceAccountRef: { ...existing, ...patch },
    });
  }

  const authSecretRef = (spec.authSecretRef as AzureAuthSecretRef) ?? {};
  const saRef = (spec.serviceAccountRef as { name?: string }) ?? {};

  return (
    <div class="space-y-5">
      <div class="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        Configure the Azure Key Vault connection and authentication method.
        Credentials must already exist as Kubernetes Secrets in this namespace;
        this wizard only references them — it never holds Azure credentials
        directly.
      </div>

      {/* Vault URL */}
      <Input
        id="azurekv-vault-url"
        label="Vault URL"
        required
        value={getStr(spec, "vaultUrl")}
        onInput={(e) =>
          patchTop("vaultUrl", (e.target as HTMLInputElement).value)}
        placeholder="https://my-vault.vault.azure.net"
        description="Must use https. Typically ends in .vault.azure.net."
        error={errors["vaultUrl"]}
      />

      {/* Auth type picker */}
      <div class="space-y-3">
        <h3 class="text-sm font-semibold text-text-primary">
          Authentication type
          <span aria-hidden="true" class="text-danger ml-0.5">*</span>
        </h3>
        <div class="grid gap-2 sm:grid-cols-3">
          {AUTH_TYPES.map((t) => {
            const active = authType.value === t.id;
            return (
              <button
                key={t.id}
                type="button"
                onClick={() => setAuthType(t.id)}
                class={`text-left rounded-lg border p-3 transition-colors ${
                  active
                    ? "border-brand bg-brand/5"
                    : "border-border-primary bg-surface hover:border-border-emphasis"
                }`}
                aria-pressed={active}
              >
                <div class="font-medium text-text-primary">{t.label}</div>
                <p class="mt-1 text-xs text-text-muted">{t.description}</p>
              </button>
            );
          })}
        </div>
        {errors["authType"] && (
          <p class="text-sm text-danger">{errors["authType"]}</p>
        )}
      </div>

      {/* Auth-type-specific fields */}
      {authType.value === "ManagedIdentity" && (
        <ManagedIdentityFields
          identityId={getStr(spec, "identityId")}
          errors={errors}
          onPatchIdentityId={(v) => patchTop("identityId", v)}
        />
      )}
      {authType.value === "ServicePrincipal" && (
        <ServicePrincipalFields
          tenantId={getStr(spec, "tenantId")}
          authSecretRef={authSecretRef}
          errors={errors}
          onPatchTenantId={(v) => patchTop("tenantId", v)}
          onPatchClientId={(patch) => patchAuthSecretRef("clientId", patch)}
          onPatchClientSecret={(patch) =>
            patchAuthSecretRef("clientSecret", patch)}
        />
      )}
      {authType.value === "WorkloadIdentity" && (
        <WorkloadIdentityFields
          tenantId={getStr(spec, "tenantId")}
          saName={saRef.name ?? ""}
          errors={errors}
          onPatchTenantId={(v) => patchTop("tenantId", v)}
          onPatchSaName={(name) => patchServiceAccountRef({ name })}
        />
      )}
    </div>
  );
}

// --- Per-type field components -----------------------------------------------

interface ManagedIdentityFieldsProps {
  identityId: string;
  errors: Record<string, string>;
  onPatchIdentityId: (v: string) => void;
}

function ManagedIdentityFields(
  { identityId, errors, onPatchIdentityId }: ManagedIdentityFieldsProps,
) {
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">Managed Identity</h4>
      <p class="text-xs text-text-muted">
        No credentials required. ESO uses the managed identity bound to the
        controller pod by AKS. If multiple managed identities are assigned,
        specify the client ID below.
      </p>
      <Input
        id="azurekv-mi-identity-id"
        label="Identity client ID (optional)"
        value={identityId}
        onInput={(e) => onPatchIdentityId((e.target as HTMLInputElement).value)}
        placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
        description="Leave blank to use the AKS-default managed identity."
        error={errors["identityId"]}
      />
    </div>
  );
}

interface ServicePrincipalFieldsProps {
  tenantId: string;
  authSecretRef: AzureAuthSecretRef;
  errors: Record<string, string>;
  onPatchTenantId: (v: string) => void;
  onPatchClientId: (patch: SecretRef) => void;
  onPatchClientSecret: (patch: SecretRef) => void;
}

function ServicePrincipalFields(
  {
    tenantId,
    authSecretRef,
    errors,
    onPatchTenantId,
    onPatchClientId,
    onPatchClientSecret,
  }: ServicePrincipalFieldsProps,
) {
  const clientId = (authSecretRef.clientId as SecretRef) ?? {};
  const clientSecret = (authSecretRef.clientSecret as SecretRef) ?? {};
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-4">
      <h4 class="text-sm font-medium text-text-primary">Service Principal</h4>

      <Input
        id="azurekv-sp-tenant-id"
        label="Tenant ID"
        required
        value={tenantId}
        onInput={(e) => onPatchTenantId((e.target as HTMLInputElement).value)}
        placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
        description="Azure AD tenant ID (Directory ID)."
        error={errors["tenantId"]}
      />

      <div>
        <h5 class="text-sm font-medium text-text-primary mb-2">
          Client ID Secret reference
        </h5>
        <div class="grid grid-cols-2 gap-3">
          <Input
            id="azurekv-sp-clientid-name"
            label="Secret name"
            required
            value={clientId.name ?? ""}
            onInput={(e) =>
              onPatchClientId({ name: (e.target as HTMLInputElement).value })}
            placeholder="azure-sp-secret"
            error={errors["authSecretRef.clientId.name"]}
          />
          <Input
            id="azurekv-sp-clientid-key"
            label="Key"
            required
            value={clientId.key ?? ""}
            onInput={(e) =>
              onPatchClientId({ key: (e.target as HTMLInputElement).value })}
            placeholder="client-id"
            error={errors["authSecretRef.clientId.key"]}
          />
        </div>
      </div>

      <div>
        <h5 class="text-sm font-medium text-text-primary mb-2">
          Client Secret reference
        </h5>
        <div class="grid grid-cols-2 gap-3">
          <Input
            id="azurekv-sp-secret-name"
            label="Secret name"
            required
            value={clientSecret.name ?? ""}
            onInput={(e) =>
              onPatchClientSecret({
                name: (e.target as HTMLInputElement).value,
              })}
            placeholder="azure-sp-secret"
            error={errors["authSecretRef.clientSecret.name"]}
          />
          <Input
            id="azurekv-sp-secret-key"
            label="Key"
            required
            value={clientSecret.key ?? ""}
            onInput={(e) =>
              onPatchClientSecret({
                key: (e.target as HTMLInputElement).value,
              })}
            placeholder="client-secret"
            error={errors["authSecretRef.clientSecret.key"]}
          />
        </div>
      </div>
    </div>
  );
}

interface WorkloadIdentityFieldsProps {
  tenantId: string;
  saName: string;
  errors: Record<string, string>;
  onPatchTenantId: (v: string) => void;
  onPatchSaName: (name: string) => void;
}

function WorkloadIdentityFields(
  {
    tenantId,
    saName,
    errors,
    onPatchTenantId,
    onPatchSaName,
  }: WorkloadIdentityFieldsProps,
) {
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">Workload Identity</h4>

      <Input
        id="azurekv-wi-tenant-id"
        label="Tenant ID"
        required
        value={tenantId}
        onInput={(e) => onPatchTenantId((e.target as HTMLInputElement).value)}
        placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
        description="Azure AD tenant ID (Directory ID)."
        error={errors["tenantId"]}
      />
      <Input
        id="azurekv-wi-sa-name"
        label="Service account name"
        required
        value={saName}
        onInput={(e) => onPatchSaName((e.target as HTMLInputElement).value)}
        placeholder="eso-workload-sa"
        description="The K8s ServiceAccount annotated with the Azure federated identity."
        error={errors["serviceAccountRef.name"]}
      />
    </div>
  );
}
