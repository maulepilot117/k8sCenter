import { useState } from "react";
import { Input } from "@/components/ui/Input.tsx";

/**
 * GCP Secret Manager provider form for SecretStoreWizard. Writes into the
 * wizard's `providerSpec: Record<string, unknown>` slot under the shape
 * `{ projectID, location?, auth?: { <method>: {...} } }`.
 *
 * v1 supports two auth methods (workloadIdentity / secretRef) plus the
 * implicit default-credentials path (no auth block). The third ESO method —
 * workloadIdentityFederation — covers multi-cloud token exchange (AWS → GCP
 * etc.) and is accessible via the YAML editor but not driven by guided fields
 * here (per the plan's L7.2 culling pass).
 *
 * Switching auth method clears the previously-entered method so stale fields
 * don't leak into the preview.
 */

type GCPSMAuthMethod = "workloadIdentity" | "secretRef" | "default";

interface GCPSMFormProps {
  spec: Record<string, unknown>;
  errors: Record<string, string>;
  onUpdateSpec: (spec: Record<string, unknown>) => void;
}

interface ServiceAccountRef {
  name?: string;
}

interface GCPSMWorkloadIdentitySpec {
  serviceAccountRef?: ServiceAccountRef;
  clusterLocation?: string;
  clusterName?: string;
  clusterProjectID?: string;
}

interface GCPSMSecretRef {
  secretAccessKeySecretRef?: {
    name?: string;
    key?: string;
  };
}

/** Typed auth sub-shapes for each GCP SM auth method block. */
interface GCPSMAuthSpec {
  workloadIdentity?: GCPSMWorkloadIdentitySpec;
  secretRef?: GCPSMSecretRef;
}

/** Typed shape for a GCP SM provider spec block (spec.provider.gcpsm). */
interface GCPSMSpec {
  projectID?: string;
  location?: string;
  auth?: GCPSMAuthSpec;
}

const AUTH_METHODS: {
  id: GCPSMAuthMethod;
  label: string;
  description: string;
}[] = [
  {
    id: "workloadIdentity",
    label: "Workload Identity",
    description:
      "GKE Workload Identity — maps a Kubernetes ServiceAccount to a GCP service account.",
  },
  {
    id: "secretRef",
    label: "SA Key (JSON)",
    description:
      "Service Account JSON key stored in a Kubernetes Secret. Simpler but less secure than Workload Identity.",
  },
  {
    id: "default",
    label: "Default Credentials",
    description:
      "Use the node/pod identity (Application Default Credentials or GKE metadata server). No extra config required.",
  },
];

/** Determine which auth method the spec currently encodes, or "default" when
 *  no explicit auth block is set. */
function detectMethod(spec: Record<string, unknown>): GCPSMAuthMethod {
  const auth = spec.auth as Record<string, unknown> | undefined;
  if (!auth) return "default";
  if ("workloadIdentity" in auth) return "workloadIdentity";
  if ("secretRef" in auth) return "secretRef";
  return "default";
}

function getStr(spec: Record<string, unknown>, key: string): string {
  const v = spec[key];
  return typeof v === "string" ? v : "";
}

function getAuthBlock(
  spec: Record<string, unknown>,
  method: GCPSMAuthMethod,
): Record<string, unknown> {
  if (method === "default") return {};
  const auth = (spec.auth as Record<string, unknown>) ?? {};
  return (auth[method] as Record<string, unknown>) ?? {};
}

export function GCPSMForm({ spec, errors, onUpdateSpec }: GCPSMFormProps) {
  // GCPSMSpec / GCPSMAuthSpec interfaces above document the shape; field reads
  // go through getStr() and getAuthBlock() helpers which narrow at each access
  // site rather than via a single top-level cast.

  // Track which auth method's fields are currently shown. Persisted in the
  // spec itself (via detectMethod) so the form survives Back/Next navigation.
  const [method, setMethodState] = useState<GCPSMAuthMethod>(
    detectMethod(spec),
  );

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function setMethod(m: GCPSMAuthMethod) {
    if (method === m) return;
    setMethodState(m);
    // Clear the auth slate; preserve top-level fields (projectID, location).
    if (m === "default") {
      const next = { ...spec };
      delete next.auth;
      onUpdateSpec(next);
    } else {
      onUpdateSpec({
        ...spec,
        auth: { [m]: emptyMethodSpec(m) },
      });
    }
  }

  function patchWI(patch: Partial<GCPSMWorkloadIdentitySpec>) {
    const auth = (spec.auth as Record<string, unknown>) ?? {};
    const block = (auth.workloadIdentity as Record<string, unknown>) ?? {};
    onUpdateSpec({
      ...spec,
      auth: { ...auth, workloadIdentity: { ...block, ...patch } },
    });
  }

  function patchSARef(patch: { name?: string; key?: string }) {
    const auth = (spec.auth as Record<string, unknown>) ?? {};
    const block = (auth.secretRef as Record<string, unknown>) ?? {};
    const existing =
      (block.secretAccessKeySecretRef as Record<string, unknown>) ?? {};
    onUpdateSpec({
      ...spec,
      auth: {
        ...auth,
        secretRef: {
          ...block,
          secretAccessKeySecretRef: { ...existing, ...patch },
        },
      },
    });
  }

  return (
    <div className="space-y-5">
      <div className="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        Configure the GCP Secret Manager connection. The project ID is the only
        required field. Credentials are referenced by name from Kubernetes
        Secrets — this wizard never holds GCP credentials directly.
      </div>

      {/* Top-level GCP SM fields */}
      <div className="grid grid-cols-2 gap-4">
        <Input
          id="gcpsm-project-id"
          label="Project ID"
          required
          value={getStr(spec, "projectID")}
          onChange={(e) => patchTop("projectID", e.currentTarget.value)}
          placeholder="my-gcp-project"
          description="GCP project ID where your secrets are stored."
          error={errors.projectID}
        />
        <Input
          id="gcpsm-location"
          label="Location (optional)"
          value={getStr(spec, "location")}
          onChange={(e) => patchTop("location", e.currentTarget.value)}
          placeholder="us-central1"
          description="Regional endpoint. Leave blank to use the global endpoint."
          error={errors.location}
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
        <div className="grid gap-2 sm:grid-cols-3">
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
      {method === "workloadIdentity" && (
        <WorkloadIdentityFields
          block={getAuthBlock(spec, "workloadIdentity")}
          errors={errors}
          onPatch={patchWI}
        />
      )}
      {method === "secretRef" && (
        <SAKeyFields
          block={getAuthBlock(spec, "secretRef")}
          errors={errors}
          onPatchRef={patchSARef}
        />
      )}
      {method === "default" && (
        <div className="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
          No additional configuration required. ESO will use Application Default
          Credentials — typically the GKE node service account or a
          metadata-server-provided identity. Ensure the identity has{" "}
          <code className="font-mono">secretmanager.versions.access</code>{" "}
          permission on the target project.
        </div>
      )}
    </div>
  );
}

/** Initial empty block for a freshly-selected auth method. */
function emptyMethodSpec(
  m: Exclude<GCPSMAuthMethod, "default">,
): Record<string, unknown> {
  switch (m) {
    case "workloadIdentity":
      return { serviceAccountRef: {} };
    case "secretRef":
      return { secretAccessKeySecretRef: {} };
  }
}

// --- Per-method field components -------------------------------------------

interface WorkloadIdentityFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatch: (patch: Partial<GCPSMWorkloadIdentitySpec>) => void;
}

function WorkloadIdentityFields({
  block,
  errors,
  onPatch,
}: WorkloadIdentityFieldsProps) {
  const saRef = (block.serviceAccountRef as ServiceAccountRef) ?? {};
  const clusterLocation = (block.clusterLocation as string) ?? "";
  const clusterName = (block.clusterName as string) ?? "";
  const clusterProjectID = (block.clusterProjectID as string) ?? "";

  function patchSARefName(name: string) {
    onPatch({ serviceAccountRef: { ...saRef, name } });
  }

  return (
    <div className="rounded-md border border-border-primary p-4 space-y-4">
      <div>
        <h4 className="text-sm font-medium text-text-primary mb-2">
          Kubernetes ServiceAccount
        </h4>
        <Input
          id="gcpsm-wi-sa-name"
          label="ServiceAccount name"
          required
          value={saRef.name ?? ""}
          onChange={(e) => patchSARefName(e.currentTarget.value)}
          placeholder="eso-gcp-sa"
          description="The Kubernetes ServiceAccount annotated with the GCP service account email via Workload Identity."
          error={
            errors["auth.workloadIdentity.serviceAccountRef.name"] ??
            errors["auth.workloadIdentity.serviceAccountRef"]
          }
        />
      </div>
      <div>
        <h4 className="text-sm font-medium text-text-primary mb-2">
          Cluster details{" "}
          <span className="text-text-muted font-normal">
            (optional — read from metadata server if omitted)
          </span>
        </h4>
        <div className="grid grid-cols-3 gap-3">
          <Input
            id="gcpsm-wi-cluster-location"
            label="Cluster location"
            value={clusterLocation}
            onChange={(e) =>
              onPatch({
                clusterLocation: e.currentTarget.value || undefined,
              })
            }
            placeholder="us-central1"
            error={errors["auth.workloadIdentity.clusterLocation"]}
          />
          <Input
            id="gcpsm-wi-cluster-name"
            label="Cluster name"
            value={clusterName}
            onChange={(e) =>
              onPatch({
                clusterName: e.currentTarget.value || undefined,
              })
            }
            placeholder="my-cluster"
            error={errors["auth.workloadIdentity.clusterName"]}
          />
          <Input
            id="gcpsm-wi-cluster-project"
            label="Cluster project ID"
            value={clusterProjectID}
            onChange={(e) =>
              onPatch({
                clusterProjectID: e.currentTarget.value || undefined,
              })
            }
            placeholder="my-infra-project"
            error={errors["auth.workloadIdentity.clusterProjectID"]}
          />
        </div>
      </div>
    </div>
  );
}

interface SAKeyFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchRef: (patch: { name?: string; key?: string }) => void;
}

function SAKeyFields({ block, errors, onPatchRef }: SAKeyFieldsProps) {
  const sakRef = ((block.secretAccessKeySecretRef as Record<string, unknown>) ??
    {}) as { name?: string; key?: string };

  return (
    <div className="rounded-md border border-border-primary p-4 space-y-3">
      <h4 className="text-sm font-medium text-text-primary">
        Service Account key Secret reference
      </h4>
      <p className="text-xs text-text-muted">
        The Kubernetes Secret containing the GCP Service Account JSON key file
        (downloaded from IAM &amp; Admin → Service Accounts → Keys).
      </p>
      <div className="grid grid-cols-2 gap-3">
        <Input
          id="gcpsm-sa-key-name"
          label="Secret name"
          required
          value={sakRef.name ?? ""}
          onChange={(e) => onPatchRef({ name: e.currentTarget.value })}
          placeholder="gcp-sa-key"
          error={errors["auth.secretRef.secretAccessKeySecretRef.name"]}
        />
        <Input
          id="gcpsm-sa-key-key"
          label="Key"
          required
          value={sakRef.key ?? ""}
          onChange={(e) => onPatchRef({ key: e.currentTarget.value })}
          placeholder="key.json"
          description="The key within the Secret holding the SA JSON content."
          error={errors["auth.secretRef.secretAccessKeySecretRef.key"]}
        />
      </div>
    </div>
  );
}
