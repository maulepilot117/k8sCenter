import { useSignal } from "@preact/signals";
import { Input } from "@/components/ui/Input.tsx";

/**
 * AWS Secrets Manager provider form for SecretStoreWizard. Writes into the
 * wizard's `providerSpec: Record<string, unknown>` slot under the shape
 * `{ region, role?, auth: { <method>: {...} } }`.
 *
 * v1 supports two auth methods:
 * - jwt: IAM workload identity via a Kubernetes service account reference.
 * - secretRef: static credentials (Access Key ID + Secret Access Key) stored
 *   in Kubernetes Secrets.
 *
 * The `service` field is intentionally absent from this form — ESO defaults
 * to SecretsManager when omitted, and the synthetic "awsps" UX discriminator
 * (AWS Parameter Store) injects service: ParameterStore upstream in the
 * backend's ToSecretStore remap. AWSPS will have its own form sub-PR.
 *
 * Switching auth method clears the auth slate via onUpdateSpec so stale fields
 * from the prior method don't leak into the YAML preview.
 */

export type AWSAuthMethod = "jwt" | "secretRef";

export interface AWSFormProps {
  spec: Record<string, unknown>;
  errors: Record<string, string>;
  onUpdateSpec: (spec: Record<string, unknown>) => void;
}

interface SecretKeyRef {
  name?: string;
  key?: string;
}

/** Typed sub-shapes for each AWS auth method block. */
interface AWSAuthSpec {
  jwt?: { serviceAccountRef?: { name?: string } };
  secretRef?: {
    accessKeyIDSecretRef?: SecretKeyRef;
    secretAccessKeySecretRef?: SecretKeyRef;
  };
}

/** Typed shape for an AWS provider spec block (spec.provider.aws). */
interface AWSSpec {
  region?: string;
  role?: string;
  auth?: AWSAuthSpec;
}

const AUTH_METHODS: {
  id: AWSAuthMethod;
  label: string;
  description: string;
}[] = [
  {
    id: "jwt",
    label: "IAM / IRSA",
    description:
      "Workload identity: the pod's service account assumes an IAM role via IRSA or EKS Pod Identity.",
  },
  {
    id: "secretRef",
    label: "Static credentials",
    description:
      "Access Key ID + Secret Access Key pair stored in Kubernetes Secrets.",
  },
];

/** Determine which auth method the spec currently encodes, or "" when none. */
function detectMethod(spec: Record<string, unknown>): AWSAuthMethod | "" {
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
  method: AWSAuthMethod,
): Record<string, unknown> {
  const auth = (spec.auth as Record<string, unknown>) ?? {};
  return (auth[method] as Record<string, unknown>) ?? {};
}

export function AWSForm({ spec, errors, onUpdateSpec }: AWSFormProps) {
  // AWSSpec / AWSAuthSpec interfaces above document the shape; field reads go
  // through getStr() and getAuthBlock() helpers which narrow at each access
  // site rather than via a single top-level cast.

  // Track which auth method's fields are currently shown. Persisted in the
  // spec itself (via detectMethod) so the form survives Back/Next navigation.
  const method = useSignal<AWSAuthMethod | "">(detectMethod(spec));

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function setMethod(m: AWSAuthMethod) {
    if (method.value === m) return;
    method.value = m;
    // Clear the auth slate; preserve top-level fields (region, role).
    onUpdateSpec({
      ...spec,
      auth: { [m]: emptyMethodSpec(m) },
    });
  }

  function patchAuth(
    authMethod: AWSAuthMethod,
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
    authMethod: AWSAuthMethod,
    refField: string,
    patch: SecretKeyRef,
  ) {
    const block = getAuthBlock(spec, authMethod);
    const existing = (block[refField] as SecretKeyRef) ?? {};
    patchAuth(authMethod, { [refField]: { ...existing, ...patch } });
  }

  return (
    <div class="space-y-5">
      <div class="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        Configure the AWS Secrets Manager connection and authentication method.
        Credentials must already exist as Kubernetes Secrets in this namespace;
        this wizard only references them — it never holds AWS credentials
        directly.
      </div>

      {/* Top-level AWS fields */}
      <div class="grid grid-cols-2 gap-4">
        <Input
          id="aws-region"
          label="AWS Region"
          required
          value={getStr(spec, "region")}
          onInput={(e) =>
            patchTop("region", (e.target as HTMLInputElement).value)}
          placeholder="us-east-1"
          description="The AWS region where your secrets are stored."
          error={errors["region"]}
        />
        <Input
          id="aws-role"
          label="Assume-role ARN (optional)"
          value={getStr(spec, "role")}
          onInput={(e) =>
            patchTop("role", (e.target as HTMLInputElement).value)}
          placeholder="arn:aws:iam::123456789012:role/my-role"
          description="IAM role to assume before fetching secrets. Leave blank to use the pod's own identity."
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
        <StaticCredAuthFields
          block={getAuthBlock(spec, "secretRef")}
          errors={errors}
          onPatchAccessKeyRef={(patch) =>
            patchSecretKeyRef("secretRef", "accessKeyIDSecretRef", patch)}
          onPatchSecretKeyRef={(patch) =>
            patchSecretKeyRef("secretRef", "secretAccessKeySecretRef", patch)}
        />
      )}
    </div>
  );
}

/** Initial empty block for a freshly-selected auth method. The wizard's
 *  validator rejects empty blocks so the user must populate before preview. */
function emptyMethodSpec(m: AWSAuthMethod): Record<string, unknown> {
  switch (m) {
    case "jwt":
      return { serviceAccountRef: {} };
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
  const saRef = (block.serviceAccountRef as { name?: string }) ?? {};
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">
        IAM / IRSA — service account reference
      </h4>
      <p class="text-xs text-text-muted">
        The service account must have an IAM role ARN annotation
        (eks.amazonaws.com/role-arn) or be bound via EKS Pod Identity. The
        assume-role ARN above takes precedence if set.
      </p>
      <Input
        id="aws-jwt-sa-name"
        label="Service account name"
        required
        value={saRef.name ?? ""}
        onInput={(e) =>
          onPatch({
            serviceAccountRef: {
              ...saRef,
              name: (e.target as HTMLInputElement).value,
            },
          })}
        placeholder="my-service-account"
        description="The Kubernetes ServiceAccount whose projected token is exchanged for AWS credentials."
        error={errors["auth.jwt.serviceAccountRef.name"]}
      />
    </div>
  );
}

interface StaticCredAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchAccessKeyRef: (patch: SecretKeyRef) => void;
  onPatchSecretKeyRef: (patch: SecretKeyRef) => void;
}

function StaticCredAuthFields(
  {
    block,
    errors,
    onPatchAccessKeyRef,
    onPatchSecretKeyRef,
  }: StaticCredAuthFieldsProps,
) {
  const akRef = (block.accessKeyIDSecretRef as SecretKeyRef) ?? {};
  const sakRef = (block.secretAccessKeySecretRef as SecretKeyRef) ?? {};
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-4">
      <div>
        <h4 class="text-sm font-medium text-text-primary mb-2">
          Access Key ID Secret reference
        </h4>
        <div class="grid grid-cols-2 gap-3">
          <Input
            id="aws-akid-name"
            label="Secret name"
            required
            value={akRef.name ?? ""}
            onInput={(e) =>
              onPatchAccessKeyRef({
                name: (e.target as HTMLInputElement).value,
              })}
            placeholder="aws-credentials"
            error={errors["auth.secretRef.accessKeyIDSecretRef.name"]}
          />
          <Input
            id="aws-akid-key"
            label="Key"
            required
            value={akRef.key ?? ""}
            onInput={(e) =>
              onPatchAccessKeyRef({
                key: (e.target as HTMLInputElement).value,
              })}
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
            id="aws-sak-name"
            label="Secret name"
            required
            value={sakRef.name ?? ""}
            onInput={(e) =>
              onPatchSecretKeyRef({
                name: (e.target as HTMLInputElement).value,
              })}
            placeholder="aws-credentials"
            error={errors["auth.secretRef.secretAccessKeySecretRef.name"]}
          />
          <Input
            id="aws-sak-key"
            label="Key"
            required
            value={sakRef.key ?? ""}
            onInput={(e) =>
              onPatchSecretKeyRef({
                key: (e.target as HTMLInputElement).value,
              })}
            placeholder="secret-access-key"
            error={errors["auth.secretRef.secretAccessKeySecretRef.key"]}
          />
        </div>
      </div>
    </div>
  );
}
