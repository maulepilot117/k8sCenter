import { useState } from "react";
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

type AWSPSAuthMethod = "jwt" | "secretRef";

interface AWSPSFormProps {
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
    // Note: AWSJWTAuth has only serviceAccountRef. The IAM role ARN lives at
    // the top-level AWSProvider.role field (spec.provider.aws.role), not here.
    serviceAccountRef?: { name?: string };
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
  const [method, setMethodState] = useState<AWSPSAuthMethod | "">(
    detectMethod(spec),
  );

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function setMethod(m: AWSPSAuthMethod) {
    if (method === m) return;
    setMethodState(m);
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

  function patchSecretKeyRef(leafKey: string, patch: SecretKeyRef) {
    // leafKey is "accessKeyIDSecretRef" or "secretAccessKeySecretRef".
    const auth = (spec.auth as Record<string, unknown>) ?? {};
    const topBlock = (auth.secretRef as Record<string, unknown>) ?? {};
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
    <div className="space-y-5">
      <div className="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        Configure the AWS Parameter Store connection and authentication method.
        The Parameter Store path is set in the ExternalSecret{" "}
        <code className="font-mono">remoteRef.key</code>, not here. Credentials
        must already exist as Kubernetes Secrets in this namespace; this wizard
        only references them — it never holds AWS credentials directly.
      </div>

      {/* Top-level fields */}
      <div className="grid grid-cols-2 gap-4">
        <Input
          id="awsps-region"
          label="AWS Region"
          required
          value={getStr(spec, "region")}
          onChange={(e) => patchTop("region", e.currentTarget.value)}
          placeholder="us-east-1"
          description="AWS region where Parameter Store is accessed."
          error={errors.region}
        />
        <Input
          id="awsps-role"
          label={
            method === "jwt" ? "IAM role ARN" : "Assume-role ARN (optional)"
          }
          required={method === "jwt"}
          value={getStr(spec, "role")}
          onChange={(e) => patchTop("role", e.currentTarget.value)}
          placeholder="arn:aws:iam::123456789012:role/my-role"
          description={
            method === "jwt"
              ? "Role ARN bound to the service account via IRSA annotation."
              : "IAM role to assume before reading parameters. Leave blank to use the pod's identity directly."
          }
          error={errors.role}
        />
      </div>

      {/* Auth method picker */}
      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-text-primary">
          Authentication method
          <span aria-hidden="true" className="text-danger ml-0.5">
            *
          </span>
        </h3>
        <div className="grid gap-2 sm:grid-cols-2">
          {AUTH_METHODS.map((m) => {
            const active = method === m.id;
            return (
              <button
                key={m.id}
                type="button"
                onClick={() => setMethod(m.id)}
                className={`text-left rounded-lg border p-3 transition-colors ${
                  active
                    ? "border-brand bg-brand/5"
                    : "border-border-primary bg-surface hover:border-brand/50"
                }`}
                aria-pressed={active}
              >
                <div className="font-medium text-text-primary">{m.label}</div>
                <p className="mt-1 text-xs text-text-muted">{m.description}</p>
              </button>
            );
          })}
        </div>
        {errors.auth && <p className="text-sm text-danger">{errors.auth}</p>}
      </div>

      {/* Auth-method-specific fields */}
      {method === "jwt" && (
        <JWTAuthFields
          block={getAuthBlock(spec, "jwt")}
          errors={errors}
          onPatch={(patch) => patchAuth("jwt", patch)}
        />
      )}
      {method === "secretRef" && (
        <StaticCredsAuthFields
          block={getAuthBlock(spec, "secretRef")}
          errors={errors}
          onPatchAccessKey={(patch) =>
            patchSecretKeyRef("accessKeyIDSecretRef", patch)
          }
          onPatchSecretKey={(patch) =>
            patchSecretKeyRef("secretAccessKeySecretRef", patch)
          }
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
      // role lives at top-level spec (patchTop), not inside this auth block.
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
  // Typed reads via AWSPSAuthSpec.jwt shape.
  // Note: role ARN is a top-level AWSProvider field edited in the region/role
  // row above — AWSJWTAuth has only serviceAccountRef.
  const jwtBlock = block as {
    serviceAccountRef?: { name?: string };
  };
  const saName = jwtBlock.serviceAccountRef?.name ?? "";

  function patchSARef(name: string) {
    const existing = (block.serviceAccountRef as Record<string, unknown>) ?? {};
    onPatch({ serviceAccountRef: { ...existing, name } });
  }

  return (
    <div className="rounded-md border border-border-primary p-4 space-y-3">
      <h4 className="text-sm font-medium text-text-primary">
        IAM workload identity (IRSA)
      </h4>
      <Input
        id="awsps-jwt-sa-name"
        label="Service account name"
        required
        value={saName}
        onChange={(e) => patchSARef(e.currentTarget.value)}
        placeholder="my-app"
        description="Kubernetes ServiceAccount annotated with the IAM role ARN (set above)."
        error={errors["auth.jwt.serviceAccountRef.name"]}
      />
    </div>
  );
}

interface StaticCredsAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchAccessKey: (patch: SecretKeyRef) => void;
  onPatchSecretKey: (patch: SecretKeyRef) => void;
}

function StaticCredsAuthFields({
  block,
  errors,
  onPatchAccessKey,
  onPatchSecretKey,
}: StaticCredsAuthFieldsProps) {
  // Typed reads via AWSPSAuthSpec.secretRef shape.
  const srBlock = block as {
    accessKeyIDSecretRef?: SecretKeyRef;
    secretAccessKeySecretRef?: SecretKeyRef;
  };
  const akRef = srBlock.accessKeyIDSecretRef ?? {};
  const sakRef = srBlock.secretAccessKeySecretRef ?? {};

  return (
    <div className="rounded-md border border-border-primary p-4 space-y-4">
      <div>
        <h4 className="text-sm font-medium text-text-primary mb-2">
          Access Key ID Secret reference
        </h4>
        <div className="grid grid-cols-2 gap-3">
          <Input
            id="awsps-ak-name"
            label="Secret name"
            required
            value={akRef.name ?? ""}
            onChange={(e) =>
              onPatchAccessKey({
                name: e.currentTarget.value,
              })
            }
            placeholder="aws-creds"
            error={errors["auth.secretRef.accessKeyIDSecretRef.name"]}
          />
          <Input
            id="awsps-ak-key"
            label="Key"
            required
            value={akRef.key ?? ""}
            onChange={(e) => onPatchAccessKey({ key: e.currentTarget.value })}
            placeholder="access-key-id"
            error={errors["auth.secretRef.accessKeyIDSecretRef.key"]}
          />
        </div>
      </div>
      <div>
        <h4 className="text-sm font-medium text-text-primary mb-2">
          Secret Access Key Secret reference
        </h4>
        <div className="grid grid-cols-2 gap-3">
          <Input
            id="awsps-sak-name"
            label="Secret name"
            required
            value={sakRef.name ?? ""}
            onChange={(e) =>
              onPatchSecretKey({
                name: e.currentTarget.value,
              })
            }
            placeholder="aws-creds"
            error={errors["auth.secretRef.secretAccessKeySecretRef.name"]}
          />
          <Input
            id="awsps-sak-key"
            label="Key"
            required
            value={sakRef.key ?? ""}
            onChange={(e) => onPatchSecretKey({ key: e.currentTarget.value })}
            placeholder="secret-access-key"
            error={errors["auth.secretRef.secretAccessKeySecretRef.key"]}
          />
        </div>
      </div>
    </div>
  );
}
