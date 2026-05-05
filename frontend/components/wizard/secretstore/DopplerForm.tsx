import { useSignal } from "@preact/signals";
import { Input } from "@/components/ui/Input.tsx";

/**
 * Doppler provider form for SecretStoreWizard. Writes into the wizard's
 * `providerSpec: Record<string, unknown>` slot under the shape:
 * ```
 * {
 *   project: string,
 *   config: string,
 *   auth: {
 *     secretRef: { dopplerToken: { name, key } }
 *     | oidcConfig: { identity, serviceAccountRef: { name } }
 *   }
 * }
 * ```
 *
 * ESO supports two auth methods for Doppler:
 *   - secretRef: a service token stored in a Kubernetes Secret.
 *   - oidcConfig: OIDC via Kubernetes ServiceAccount tokens (Workload Identity).
 *
 * Switching auth method clears the previously-entered method block so stale
 * fields don't leak into the YAML preview.
 */

export type DopplerAuthMethod = "secretRef" | "oidcConfig";

export interface DopplerFormProps {
  spec: Record<string, unknown>;
  errors: Record<string, string>;
  onUpdateSpec: (spec: Record<string, unknown>) => void;
}

interface SecretRef {
  name?: string;
  key?: string;
}

/** Typed sub-shapes for Doppler auth methods. */
interface DopplerAuthSpec {
  secretRef?: { dopplerToken?: SecretRef };
  oidcConfig?: {
    identity?: string;
    serviceAccountRef?: { name?: string };
    // Note: ESO's ServiceAccountSelector also accepts optional `namespace` and
    // `audiences` fields — intentionally omitted for v1 (accessible via YAML editor).
  };
}

/** Typed shape for a Doppler provider spec block (spec.provider.doppler). */
interface DopplerSpec {
  project?: string;
  config?: string;
  auth?: DopplerAuthSpec;
}

const AUTH_METHODS: {
  id: DopplerAuthMethod;
  label: string;
  description: string;
}[] = [
  {
    id: "secretRef",
    label: "Service Token",
    description:
      "A Doppler service token stored in a Kubernetes Secret. Recommended for most clusters.",
  },
  {
    id: "oidcConfig",
    label: "OIDC (Workload Identity)",
    description:
      "Authenticate via Kubernetes ServiceAccount tokens — no long-lived secrets required.",
  },
];

/** Determine which auth method the spec currently encodes, or "" when none. */
function detectMethod(spec: Record<string, unknown>): DopplerAuthMethod | "" {
  const auth = spec.auth as Record<string, unknown> | undefined;
  if (!auth) return "";
  for (const m of AUTH_METHODS.map((x) => x.id)) {
    if (m in auth) return m;
  }
  return "";
}

/** Read a top-level string field from the spec. */
function getStr(spec: Record<string, unknown>, key: string): string {
  const v = spec[key];
  return typeof v === "string" ? v : "";
}

function getAuthBlock(
  spec: Record<string, unknown>,
  method: DopplerAuthMethod,
): Record<string, unknown> {
  const auth = (spec.auth as Record<string, unknown>) ?? {};
  return (auth[method] as Record<string, unknown>) ?? {};
}

export function DopplerForm({ spec, errors, onUpdateSpec }: DopplerFormProps) {
  // Track which auth method's fields are currently shown. Persisted in the
  // spec itself (via detectMethod) so the form survives Back/Next navigation.
  const method = useSignal<DopplerAuthMethod | "">(detectMethod(spec));

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function setMethod(m: DopplerAuthMethod) {
    if (method.value === m) return;
    method.value = m;
    // Clear the auth slate; preserve top-level fields (project, config).
    onUpdateSpec({
      ...spec,
      auth: { [m]: emptyMethodSpec(m) },
    });
  }

  function patchAuth(
    authMethod: DopplerAuthMethod,
    patch: Record<string, unknown>,
  ) {
    const auth = (spec.auth as Record<string, unknown>) ?? {};
    const block = (auth[authMethod] as Record<string, unknown>) ?? {};
    onUpdateSpec({
      ...spec,
      auth: { ...auth, [authMethod]: { ...block, ...patch } },
    });
  }

  function patchSecretRef(
    authMethod: DopplerAuthMethod,
    refField: string,
    patch: SecretRef,
  ) {
    const block = getAuthBlock(spec, authMethod);
    const existing = (block[refField] as SecretRef) ?? {};
    patchAuth(authMethod, { [refField]: { ...existing, ...patch } });
  }

  return (
    <div class="space-y-5">
      <div class="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        Configure the Doppler provider connection. Doppler credentials must
        already exist as Kubernetes Secrets in this namespace (for service
        token) or be available via Workload Identity (for OIDC) — this wizard
        only references them and never stores credentials directly.
      </div>

      {/* Top-level Doppler fields */}
      <div class="grid grid-cols-2 gap-4">
        <Input
          id="doppler-project"
          label="Project"
          required
          value={getStr(spec, "project")}
          onInput={(e) =>
            patchTop("project", (e.target as HTMLInputElement).value)}
          placeholder="my-project"
          description="The Doppler project name."
          error={errors["project"]}
        />
        <Input
          id="doppler-config"
          label="Config"
          required
          value={getStr(spec, "config")}
          onInput={(e) =>
            patchTop("config", (e.target as HTMLInputElement).value)}
          placeholder="prd"
          description="The Doppler config (environment) within the project."
          error={errors["config"]}
        />
      </div>

      {/* Auth method picker */}
      <div class="space-y-3">
        <h3 class="text-sm font-semibold text-text-primary">
          Authentication method
          <span aria-hidden="true" class="text-danger ml-0.5">*</span>
        </h3>
        <div class="grid gap-2 sm:grid-cols-2">
          {AUTH_METHODS.map((m) => {
            const active = method.value === m.id;
            return (
              <button
                key={m.id}
                type="button"
                onClick={() => setMethod(m.id)}
                class={`text-left rounded-lg border p-3 transition-colors ${
                  active
                    ? "border-brand bg-brand/5"
                    : "border-border-primary bg-surface hover:border-border-emphasis"
                }`}
                aria-pressed={active}
              >
                <div class="font-medium text-text-primary">{m.label}</div>
                <p class="mt-1 text-xs text-text-muted">{m.description}</p>
              </button>
            );
          })}
        </div>
        {errors["auth"] && <p class="text-sm text-danger">{errors["auth"]}</p>}
      </div>

      {/* Auth-method-specific fields */}
      {method.value === "secretRef" && (
        <ServiceTokenFields
          block={getAuthBlock(spec, "secretRef")}
          errors={errors}
          onPatchRef={(patch) =>
            patchSecretRef("secretRef", "dopplerToken", patch)}
        />
      )}
      {method.value === "oidcConfig" && (
        <OIDCAuthFields
          block={getAuthBlock(spec, "oidcConfig")}
          errors={errors}
          onPatch={(patch) => patchAuth("oidcConfig", patch)}
        />
      )}
    </div>
  );
}

/** Initial empty block for a freshly-selected auth method. The wizard's
 *  validator rejects empty blocks so the user must populate before preview. */
function emptyMethodSpec(m: DopplerAuthMethod): Record<string, unknown> {
  switch (m) {
    case "secretRef":
      return { dopplerToken: {} };
    case "oidcConfig":
      return { serviceAccountRef: {} };
  }
}

// --- Per-method field components ---------------------------------------

interface ServiceTokenFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchRef: (patch: SecretRef) => void;
}

function ServiceTokenFields(
  { block, errors, onPatchRef }: ServiceTokenFieldsProps,
) {
  const ref = (block.dopplerToken as SecretRef) ?? {};
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">
        Service token Secret reference
      </h4>
      <p class="text-xs text-text-muted">
        The key defaults to <code class="font-mono">dopplerToken</code>{" "}
        in ESO when omitted, but an explicit value is required here to produce
        unambiguous YAML.
      </p>
      <div class="grid grid-cols-2 gap-3">
        <Input
          id="doppler-token-ref-name"
          label="Secret name"
          required
          value={ref.name ?? ""}
          onInput={(e) =>
            onPatchRef({ name: (e.target as HTMLInputElement).value })}
          placeholder="doppler-token"
          error={errors["auth.secretRef.dopplerToken.name"]}
        />
        <Input
          id="doppler-token-ref-key"
          label="Key"
          required
          value={ref.key ?? ""}
          onInput={(e) =>
            onPatchRef({ key: (e.target as HTMLInputElement).value })}
          placeholder="serviceToken"
          error={errors["auth.secretRef.dopplerToken.key"]}
        />
      </div>
    </div>
  );
}

interface OIDCAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatch: (patch: Record<string, unknown>) => void;
}

function OIDCAuthFields({ block, errors, onPatch }: OIDCAuthFieldsProps) {
  const identity = (block.identity as string) ?? "";
  const saRef = (block.serviceAccountRef as Record<string, unknown>) ?? {};
  const saName = (saRef.name as string) ?? "";

  function patchSARef(patch: Record<string, unknown>) {
    onPatch({ serviceAccountRef: { ...saRef, ...patch } });
  }

  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">
        OIDC / Workload Identity auth
      </h4>
      <Input
        id="doppler-oidc-identity"
        label="Doppler Identity ID"
        required
        value={identity}
        onInput={(e) =>
          onPatch({ identity: (e.target as HTMLInputElement).value })}
        placeholder="abc123..."
        description="The Doppler Service Account Identity ID configured for OIDC."
        error={errors["auth.oidcConfig.identity"]}
      />
      <Input
        id="doppler-oidc-sa-name"
        label="ServiceAccount name"
        required
        value={saName}
        onInput={(e) =>
          patchSARef({ name: (e.target as HTMLInputElement).value })}
        placeholder="my-app"
        description="Kubernetes ServiceAccount whose token is exchanged for a Doppler credential."
        error={errors["auth.oidcConfig.serviceAccountRef.name"]}
      />
      {errors["auth.oidcConfig.serviceAccountRef"] && (
        <p class="text-sm text-danger">
          {errors["auth.oidcConfig.serviceAccountRef"]}
        </p>
      )}
    </div>
  );
}
