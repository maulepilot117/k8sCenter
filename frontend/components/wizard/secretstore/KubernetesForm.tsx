import { useState } from "react";
import { Input } from "@/components/ui/Input.tsx";
import type { ProviderFormProps } from "@/lib/eso-types.ts";

/**
 * Kubernetes (cross-namespace) provider form for SecretStoreWizard.
 *
 * This provider lets ESO read Kubernetes Secrets from a different namespace
 * (or even a different cluster) via service-account impersonation. The ESO
 * controller authenticates to the target apiserver using one of three methods:
 *
 *   - ServiceAccount: a named SA in the source namespace whose token ESO
 *     mounts and presents to the apiserver.
 *   - Token: a static bearer token stored in a local Kubernetes Secret.
 *   - Cert: mTLS using a client certificate + key stored in Secrets.
 *
 * Writes into the wizard's `providerSpec` slot under the shape that mirrors
 * ESO's spec.provider.kubernetes:
 *
 *   {
 *     remoteNamespace: string,
 *     server?: { url?: string, caBundle?: string },
 *     auth: { serviceAccount?: {...} | token?: {...} | cert?: {...} }
 *   }
 *
 * Switching auth method clears stale fields so they don't leak into the YAML
 * preview (same pattern as VaultForm).
 */

type KubernetesAuthMethod = "serviceAccount" | "token" | "cert";

interface SecretRef {
  name?: string;
  key?: string;
}

/** Typed sub-shapes for each Kubernetes auth method block. */
interface KubernetesAuthSpec {
  serviceAccount?: { name?: string; audiences?: string[] };
  token?: { bearerToken?: SecretRef };
  cert?: { clientCert?: SecretRef; clientKey?: SecretRef };
}

const AUTH_METHODS: {
  id: KubernetesAuthMethod;
  label: string;
  description: string;
}[] = [
  {
    id: "serviceAccount",
    label: "ServiceAccount",
    description: "Impersonate a named ServiceAccount in the source namespace.",
  },
  {
    id: "token",
    label: "Bearer Token",
    description: "A static bearer token stored in a local Kubernetes Secret.",
  },
  {
    id: "cert",
    label: "TLS Cert",
    description:
      "Mutual-TLS with a client certificate + key pair from Secrets.",
  },
];

/** Determine which auth method the spec currently encodes, or "" when none. */
function detectMethod(
  spec: Record<string, unknown>,
): KubernetesAuthMethod | "" {
  const auth = spec.auth as Record<string, unknown> | undefined;
  if (!auth) return "";
  for (const m of AUTH_METHODS.map((x) => x.id)) {
    if (m in auth) return m;
  }
  return "";
}

function getStr(spec: Record<string, unknown>, key: string): string {
  const v = spec[key];
  return typeof v === "string" ? v : "";
}

function getServerBlock(
  spec: Record<string, unknown>,
): Record<string, unknown> {
  return (spec.server as Record<string, unknown>) ?? {};
}

function getAuthBlock(
  spec: Record<string, unknown>,
  method: KubernetesAuthMethod,
): Record<string, unknown> {
  const auth = (spec.auth as Record<string, unknown>) ?? {};
  return (auth[method] as Record<string, unknown>) ?? {};
}

/** Initial empty block for a freshly-selected auth method. The wizard's
 *  validator rejects empty blocks so the user must populate before preview. */
function emptyMethodSpec(m: KubernetesAuthMethod): Record<string, unknown> {
  switch (m) {
    case "serviceAccount":
      return {};
    case "token":
      return { bearerToken: {} };
    case "cert":
      return { clientCert: {}, clientKey: {} };
  }
}

export function KubernetesForm({
  spec,
  errors,
  onUpdateSpec,
}: ProviderFormProps) {
  const [method, setMethodState] = useState<KubernetesAuthMethod | "">(
    detectMethod(spec),
  );

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function patchServer(patch: Record<string, unknown>) {
    const srv = (spec.server as Record<string, unknown>) ?? {};
    const merged = { ...srv, ...patch };
    // Prune empty-string values so the serialised YAML stays minimal.
    for (const k of Object.keys(merged)) {
      if (merged[k] === "") delete merged[k];
    }
    if (Object.keys(merged).length === 0) {
      const { server: _srv, ...rest } = spec;
      void _srv;
      onUpdateSpec(rest);
    } else {
      onUpdateSpec({ ...spec, server: merged });
    }
  }

  function setMethod(m: KubernetesAuthMethod) {
    if (method === m) return;
    setMethodState(m);
    // Clear auth slate; preserve top-level fields.
    onUpdateSpec({
      ...spec,
      auth: { [m]: emptyMethodSpec(m) },
    });
  }

  function patchAuth(
    authMethod: KubernetesAuthMethod,
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
    authMethod: KubernetesAuthMethod,
    refField: string,
    patch: SecretRef,
  ) {
    const block = getAuthBlock(spec, authMethod);
    const existing = (block[refField] as SecretRef) ?? {};
    patchAuth(authMethod, { [refField]: { ...existing, ...patch } });
  }

  const srv = getServerBlock(spec);

  return (
    <div className="space-y-5">
      {/* Contextual banner — explains what this provider does */}
      <div className="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        The Kubernetes provider reads Secrets from{" "}
        <span className="font-medium text-text-secondary">
          another namespace
        </span>{" "}
        (or cluster) via service-account impersonation. Set{" "}
        <span className="font-mono text-xs">Remote namespace</span> to the
        source namespace, then choose how ESO authenticates to that apiserver.
      </div>

      {/* Remote namespace — most important field, shown prominently */}
      <div className="grid grid-cols-2 gap-4">
        <Input
          id="k8s-remote-namespace"
          label="Remote namespace"
          required
          value={getStr(spec, "remoteNamespace")}
          onChange={(e) => patchTop("remoteNamespace", e.currentTarget.value)}
          placeholder="secrets-ns"
          description="Namespace in the source cluster where Secrets live. Defaults to 'default' in ESO when omitted."
          error={errors.remoteNamespace}
        />
      </div>

      {/* Optional server block */}
      <details className="group">
        <summary className="cursor-pointer select-none text-sm font-medium text-text-secondary hover:text-text-primary">
          Advanced: custom apiserver URL
        </summary>
        <div className="mt-3 grid grid-cols-2 gap-4 rounded-md border border-border-primary p-4">
          <Input
            id="k8s-server-url"
            label="Apiserver URL (optional)"
            value={(srv.url as string) ?? ""}
            onChange={(e) => patchServer({ url: e.currentTarget.value })}
            placeholder="https://apiserver.example.com:6443"
            description="Leave blank to use the in-cluster apiserver. Must use https."
            error={errors["server.url"]}
          />
          <Input
            id="k8s-server-ca"
            label="CA bundle (base64, optional)"
            value={(srv.caBundle as string) ?? ""}
            onChange={(e) => patchServer({ caBundle: e.currentTarget.value })}
            placeholder="LS0tLS1CRUdJTi…"
            description="Base64-encoded CA bundle for the target apiserver. Leave blank to use the cluster's default CA."
            error={errors["server.caBundle"]}
          />
        </div>
      </details>

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
      {method === "serviceAccount" && (
        <ServiceAccountAuthFields
          block={getAuthBlock(spec, "serviceAccount")}
          errors={errors}
          onPatch={(patch) => patchAuth("serviceAccount", patch)}
        />
      )}
      {method === "token" && (
        <TokenAuthFields
          block={getAuthBlock(spec, "token")}
          errors={errors}
          onPatchRef={(patch) => patchSecretRef("token", "bearerToken", patch)}
        />
      )}
      {method === "cert" && (
        <CertAuthFields
          block={getAuthBlock(spec, "cert")}
          errors={errors}
          onPatchClientCert={(patch) =>
            patchSecretRef("cert", "clientCert", patch)
          }
          onPatchClientKey={(patch) =>
            patchSecretRef("cert", "clientKey", patch)
          }
        />
      )}
    </div>
  );
}

// --- Per-method field components -------------------------------------------

interface ServiceAccountAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatch: (patch: Record<string, unknown>) => void;
}

function ServiceAccountAuthFields({
  block,
  errors,
  onPatch,
}: ServiceAccountAuthFieldsProps) {
  const name = (block.name as string) ?? "";
  const audiences = (block.audiences as string[] | undefined) ?? [];
  const audiencesStr = audiences.join(", ");

  function handleAudiencesInput(raw: string) {
    const trimmed = raw.trim();
    if (trimmed === "") {
      // Remove audiences entirely when empty so the YAML stays minimal.
      const { audiences: _a, ...rest } = block;
      void _a;
      onPatch(rest);
    } else {
      onPatch({
        audiences: trimmed
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      });
    }
  }

  return (
    <div className="rounded-md border border-border-primary p-4 space-y-3">
      <h4 className="text-sm font-medium text-text-primary">ServiceAccount</h4>
      <div className="grid grid-cols-2 gap-3">
        <Input
          id="k8s-sa-name"
          label="ServiceAccount name"
          required
          value={name}
          onChange={(e) => onPatch({ name: e.currentTarget.value })}
          placeholder="eso-reader"
          description="The SA in the source namespace whose token ESO presents to the apiserver."
          error={errors["auth.serviceAccount.name"]}
        />
        <Input
          id="k8s-sa-audiences"
          label="Token audiences (optional)"
          value={audiencesStr}
          onChange={(e) => handleAudiencesInput(e.currentTarget.value)}
          placeholder="https://kubernetes.default.svc"
          description="Comma-separated list. Leave blank for the default cluster audience."
          error={errors["auth.serviceAccount.audiences"]}
        />
      </div>
    </div>
  );
}

interface TokenAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchRef: (patch: SecretRef) => void;
}

function TokenAuthFields({ block, errors, onPatchRef }: TokenAuthFieldsProps) {
  const ref = (block.bearerToken as SecretRef) ?? {};
  return (
    <div className="rounded-md border border-border-primary p-4 space-y-3">
      <h4 className="text-sm font-medium text-text-primary">
        Bearer token Secret reference
      </h4>
      <div className="grid grid-cols-2 gap-3">
        <Input
          id="k8s-token-ref-name"
          label="Secret name"
          required
          value={ref.name ?? ""}
          onChange={(e) => onPatchRef({ name: e.currentTarget.value })}
          placeholder="k8s-token"
          error={errors["auth.token.bearerToken.name"]}
        />
        <Input
          id="k8s-token-ref-key"
          label="Key"
          required
          value={ref.key ?? ""}
          onChange={(e) => onPatchRef({ key: e.currentTarget.value })}
          placeholder="token"
          error={errors["auth.token.bearerToken.key"]}
        />
      </div>
    </div>
  );
}

interface CertAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchClientCert: (patch: SecretRef) => void;
  onPatchClientKey: (patch: SecretRef) => void;
}

function CertAuthFields({
  block,
  errors,
  onPatchClientCert,
  onPatchClientKey,
}: CertAuthFieldsProps) {
  const clientCert = (block.clientCert as SecretRef) ?? {};
  const clientKey = (block.clientKey as SecretRef) ?? {};
  return (
    <div className="rounded-md border border-border-primary p-4 space-y-4">
      <div>
        <h4 className="text-sm font-medium text-text-primary mb-2">
          Client certificate
        </h4>
        <div className="grid grid-cols-2 gap-3">
          <Input
            id="k8s-cert-cert-name"
            label="Secret name"
            required
            value={clientCert.name ?? ""}
            onChange={(e) =>
              onPatchClientCert({
                name: e.currentTarget.value,
              })
            }
            placeholder="k8s-client-cert"
            error={errors["auth.cert.clientCert.name"]}
          />
          <Input
            id="k8s-cert-cert-key"
            label="Key"
            required
            value={clientCert.key ?? ""}
            onChange={(e) => onPatchClientCert({ key: e.currentTarget.value })}
            placeholder="tls.crt"
            error={errors["auth.cert.clientCert.key"]}
          />
        </div>
      </div>
      <div>
        <h4 className="text-sm font-medium text-text-primary mb-2">
          Client key
        </h4>
        <div className="grid grid-cols-2 gap-3">
          <Input
            id="k8s-cert-key-name"
            label="Secret name"
            required
            value={clientKey.name ?? ""}
            onChange={(e) => onPatchClientKey({ name: e.currentTarget.value })}
            placeholder="k8s-client-key"
            error={errors["auth.cert.clientKey.name"]}
          />
          <Input
            id="k8s-cert-key-key"
            label="Key"
            required
            value={clientKey.key ?? ""}
            onChange={(e) => onPatchClientKey({ key: e.currentTarget.value })}
            placeholder="tls.key"
            error={errors["auth.cert.clientKey.key"]}
          />
        </div>
      </div>
    </div>
  );
}
