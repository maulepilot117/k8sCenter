import { useSignal } from "@preact/signals";
import { Input } from "@/components/ui/Input.tsx";

/**
 * AWS Parameter Store provider form for SecretStoreWizard. Writes into the
 * wizard's `providerSpec: Record<string, unknown>` slot under the shape
 * `{ region, role?, auth: { <method>: {...} } }`.
 *
 * v1 supports two auth methods:
 *   - jwt: IAM workload identity (IRSA) via a Kubernetes service account.
 *   - secretRef: static AWS credentials stored in Kubernetes Secrets.
 *
 * The AWS Parameter Store path is set in the ExternalSecret remoteRef.key, not
 * here — this form only configures the store connection and authentication.
 * Switching auth method clears the previously-entered method so stale fields
 * don't leak into the preview.
 *
 * Note: "service: ParameterStore" is NOT a field in this form. The backend
 * injects it automatically when provider == "awsps" (U18 ToSecretStore remap).
 */

export type AWSPSAuthMethod = "jwt" | "secretRef";

export interface AWSPSFormProps {
  spec: Record<string, unknown>;
  errors: Record<string, string>;
  onUpdateSpec: (spec: Record<string, unknown>) => void;
}

interface SecretKeyRef {
  name?: string;
  key?: string;
}

/** Typed sub-shapes for each AWSPS auth method block. */
interface AWSPSAuthSpec {
  jwt?: {
    serviceAccountRef?: { name?: string };
    role?: string;
  };
  secretRef?: {
    accessKeyIDSecretRef?: SecretKeyRef;
    secretAccessKeySecretRef?: SecretKeyRef;
  };
}

/** Typed shape for an AWS Parameter Store provider spec block. */
interface AWSPSSpec {
  region?: string;
  role?: string;
  auth?: AWSPSAuthSpec;
}

const AUTH_METHODS: {
  id: AWSPSAuthMethod;
  label: string;
  description: string;
}[] = [
  {
    id: "jwt",
    label: "IAM (IRSA)",
    description:
      "Workload identity via a Kubernetes service account JWT. Recommended for EKS.",
  },
  {
    id: "secretRef",
    label: "Static credentials",
    description:
      "Access Key ID + Secret Access Key stored in Kubernetes Secrets.",
  },
];

/** Determine which auth method the spec currently encodes, or "" when none. */
function detectMethod(spec: Record<string, unknown>): AWSPSAuthMethod | "" {
  const auth = spec.auth as Record<string, unknown> | undefined;
  if (!auth) return "";
  // Iterate AUTH_METHODS (single source of truth) instead of a separate array.
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
  method: AWSPSAuthMethod,
): Record<string, unknown> {
  const auth = (spec.auth as Record<string, unknown>) ?? {};
  return (auth[method] as Record<string, unknown>) ?? {};
}

export function AWSPSForm({ spec, errors, onUpdateSpec }: AWSPSFormProps) {
  // AWSPSSpec / AWSPSAuthSpec interfaces above document the shape; field reads
  // go through getStr() and getAuthBlock() helpers which narrow at each access
  // site rather than via a single top-level cast.

  // Track which auth method's fields are currently shown. Persisted in the
  // spec itself (via detectMethod) so the form survives Back/Next navigation.
  const method = useSignal<AWSPSAuthMethod | "">(detectMethod(spec));

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function setMethod(m: AWSPSAuthMethod) {
    if (method.value === m) return;
    method.value = m;
    // Clear the auth slate; preserve top-level fields (region, role).
    onUpdateSpec({
      ...spec,
      auth: { [m]: emptyMethodSpec(m) },
    });
  }

  function patchAuth(
    authMethod: AWSPSAuthMethod,
    patch: Record<string, unknown>,
  ) {
    const auth = (spec.auth as Record<string, unknown>) ?? {};
    const block = (auth[authMethod] as Record<string, unknown>) ?? {};
    onUpdateSpec({
      ...spec,
      auth: { ...auth, [authMethod]: { ...block, ...patch } },
    });
  }

  function patchSecretKeyRef(
    refPath: string[],
    patch: SecretKeyRef,
  ) {
    // refPath is ["secretRef", "accessKeyIDSecretRef"] or similar.
    // Walk the auth block and merge the patch at the leaf.
    const auth = (spec.auth as Record<string, unknown>) ?? {};
    const topBlock = (auth["secretRef"] as Record<string, unknown>) ?? {};
    const [, leafKey] = refPath;
    const existing = (topBlock[leafKey] as SecretKeyRef) ?? {};
    onUpdateSpec({
      ...spec,
      auth: {
        ...auth,
        secretRef: { ...topBlock, [leafKey]: { ...existing, ...patch } },
      },
    });
  }

  return (
    <div class="space-y-5">
      <div class="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        Configure the AWS Parameter Store connection and authentication method.
        The Parameter Store path is set in the ExternalSecret{" "}
        <code class="font-mono">remoteRef.key</code>, not here. Credentials must
        already exist as Kubernetes Secrets in this namespace; this wizard only
        references them — it never holds AWS credentials directly.
      </div>

      {/* Top-level fields */}
      <div class="grid grid-cols-2 gap-4">
        <Input
          id="awsps-region"
          label="AWS Region"
          required
          value={getStr(spec, "region")}
          onInput={(e) =>
            patchTop("region", (e.target as HTMLInputElement).value)}
          placeholder="us-east-1"
          description="AWS region where Parameter Store is accessed."
          error={errors["region"]}
        />
        <Input
          id="awsps-role"
          label="Assume-role ARN (optional)"
          value={getStr(spec, "role")}
          onInput={(e) =>
            patchTop("role", (e.target as HTMLInputElement).value)}
          placeholder="arn:aws:iam::123456789012:role/my-role"
          description="IAM role to assume before reading parameters. Leave blank to use the pod's identity directly."
          error={errors["role"]}
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
      {method.value === "jwt" && (
        <JWTAuthFields
          block={getAuthBlock(spec, "jwt")}
          errors={errors}
          onPatch={(patch) => patchAuth("jwt", patch)}
        />
      )}
      {method.value === "secretRef" && (
        <StaticCredsAuthFields
          block={getAuthBlock(spec, "secretRef")}
          errors={errors}
          onPatchAccessKey={(patch) =>
            patchSecretKeyRef(["secretRef", "accessKeyIDSecretRef"], patch)}
          onPatchSecretKey={(patch) =>
            patchSecretKeyRef(
              ["secretRef", "secretAccessKeySecretRef"],
              patch,
            )}
        />
      )}
    </div>
  );
}

/** Initial empty block for a freshly-selected auth method. The wizard's
 *  validator rejects empty blocks so the user must populate before preview. */
function emptyMethodSpec(m: AWSPSAuthMethod): Record<string, unknown> {
  switch (m) {
    case "jwt":
      return { serviceAccountRef: {}, role: "" };
    case "secretRef":
      return { accessKeyIDSecretRef: {}, secretAccessKeySecretRef: {} };
  }
}

// --- Per-method field components ---------------------------------------

interface JWTAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatch: (patch: Record<string, unknown>) => void;
}

function JWTAuthFields({ block, errors, onPatch }: JWTAuthFieldsProps) {
  // Typed reads via AWSPSAuthSpec.jwt shape.
  const jwtBlock = block as {
    serviceAccountRef?: { name?: string };
    role?: string;
  };
  const saName = jwtBlock.serviceAccountRef?.name ?? "";
  const role = jwtBlock.role ?? "";

  function patchSARef(name: string) {
    const existing = (block.serviceAccountRef as Record<string, unknown>) ?? {};
    onPatch({ serviceAccountRef: { ...existing, name } });
  }

  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">
        IAM workload identity (IRSA)
      </h4>
      <div class="grid grid-cols-2 gap-3">
        <Input
          id="awsps-jwt-sa-name"
          label="Service account name"
          required
          value={saName}
          onInput={(e) => patchSARef((e.target as HTMLInputElement).value)}
          placeholder="my-app"
          description="Kubernetes ServiceAccount annotated with the IAM role ARN."
          error={errors["auth.jwt.serviceAccountRef.name"]}
        />
        <Input
          id="awsps-jwt-role"
          label="IAM role ARN"
          required
          value={role}
          onInput={(e) =>
            onPatch({ role: (e.target as HTMLInputElement).value })}
          placeholder="arn:aws:iam::123456789012:role/my-role"
          description="Role ARN bound to the service account via IRSA annotation."
          error={errors["auth.jwt.role"]}
        />
      </div>
    </div>
  );
}

interface StaticCredsAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchAccessKey: (patch: SecretKeyRef) => void;
  onPatchSecretKey: (patch: SecretKeyRef) => void;
}

function StaticCredsAuthFields(
  { block, errors, onPatchAccessKey, onPatchSecretKey }:
    StaticCredsAuthFieldsProps,
) {
  // Typed reads via AWSPSAuthSpec.secretRef shape.
  const srBlock = block as {
    accessKeyIDSecretRef?: SecretKeyRef;
    secretAccessKeySecretRef?: SecretKeyRef;
  };
  const akRef = srBlock.accessKeyIDSecretRef ?? {};
  const sakRef = srBlock.secretAccessKeySecretRef ?? {};

  return (
    <div class="rounded-md border border-border-primary p-4 space-y-4">
      <div>
        <h4 class="text-sm font-medium text-text-primary mb-2">
          Access Key ID Secret reference
        </h4>
        <div class="grid grid-cols-2 gap-3">
          <Input
            id="awsps-ak-name"
            label="Secret name"
            required
            value={akRef.name ?? ""}
            onInput={(e) =>
              onPatchAccessKey({
                name: (e.target as HTMLInputElement).value,
              })}
            placeholder="aws-creds"
            error={errors["auth.secretRef.accessKeyIDSecretRef.name"]}
          />
          <Input
            id="awsps-ak-key"
            label="Key"
            required
            value={akRef.key ?? ""}
            onInput={(e) =>
              onPatchAccessKey({ key: (e.target as HTMLInputElement).value })}
            placeholder="access-key-id"
            error={errors["auth.secretRef.accessKeyIDSecretRef.key"]}
          />
        </div>
      </div>
      <div>
        <h4 class="text-sm font-medium text-text-primary mb-2">
          Secret Access Key Secret reference
        </h4>
        <div class="grid grid-cols-2 gap-3">
          <Input
            id="awsps-sak-name"
            label="Secret name"
            required
            value={sakRef.name ?? ""}
            onInput={(e) =>
              onPatchSecretKey({
                name: (e.target as HTMLInputElement).value,
              })}
            placeholder="aws-creds"
            error={errors["auth.secretRef.secretAccessKeySecretRef.name"]}
          />
          <Input
            id="awsps-sak-key"
            label="Key"
            required
            value={sakRef.key ?? ""}
            onInput={(e) =>
              onPatchSecretKey({ key: (e.target as HTMLInputElement).value })}
            placeholder="secret-access-key"
            error={errors["auth.secretRef.secretAccessKeySecretRef.key"]}
          />
        </div>
      </div>
    </div>
  );
}
