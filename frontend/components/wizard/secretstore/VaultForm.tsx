import { useSignal } from "@preact/signals";
import { Input } from "@/components/ui/Input.tsx";

/**
 * Vault provider form for SecretStoreWizard. Writes into the wizard's
 * `providerSpec: Record<string, unknown>` slot under the shape
 * `{ server, path?, version?, namespace?, auth: { <method>: {...} } }`.
 *
 * v1 supports five auth methods (token / kubernetes / appRole / jwt / cert);
 * additional methods (userPass, ldap, iam, gcp) are accessible via the YAML
 * editor but not driven by guided fields here. Switching auth method clears
 * the previously-entered method so stale fields don't leak into the preview.
 */

export type VaultAuthMethod =
  | "token"
  | "kubernetes"
  | "appRole"
  | "jwt"
  | "cert";

export interface VaultFormProps {
  spec: Record<string, unknown>;
  errors: Record<string, string>;
  onUpdateSpec: (spec: Record<string, unknown>) => void;
}

interface SecretRef {
  name?: string;
  key?: string;
}

const AUTH_METHODS: {
  id: VaultAuthMethod;
  label: string;
  description: string;
}[] = [
  {
    id: "token",
    label: "Token",
    description: "A Vault token loaded from a Kubernetes Secret.",
  },
  {
    id: "kubernetes",
    label: "Kubernetes",
    description:
      "Vault's Kubernetes auth method using the pod's service account JWT.",
  },
  {
    id: "appRole",
    label: "AppRole",
    description:
      "RoleID + SecretID pair, with the SecretID stored in a Secret.",
  },
  {
    id: "jwt",
    label: "JWT / OIDC",
    description:
      "JWT or OIDC token, either from a Secret or a service account.",
  },
  {
    id: "cert",
    label: "TLS Cert",
    description: "Mutual-TLS authentication using a client certificate.",
  },
];

/** Determine which auth method the spec currently encodes, or "" when none. */
function detectMethod(spec: Record<string, unknown>): VaultAuthMethod | "" {
  const auth = spec.auth as Record<string, unknown> | undefined;
  if (!auth) return "";
  for (const m of ["token", "kubernetes", "appRole", "jwt", "cert"] as const) {
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
  method: VaultAuthMethod,
): Record<string, unknown> {
  const auth = (spec.auth as Record<string, unknown>) ?? {};
  return (auth[method] as Record<string, unknown>) ?? {};
}

export function VaultForm({ spec, errors, onUpdateSpec }: VaultFormProps) {
  // Track which auth method's fields are currently shown. Persisted in the
  // spec itself (via detectMethod) so the form survives Back/Next navigation.
  const method = useSignal<VaultAuthMethod | "">(detectMethod(spec));

  function patchTop(field: string, value: string) {
    const next = { ...spec };
    if (value === "") delete next[field];
    else next[field] = value;
    onUpdateSpec(next);
  }

  function setMethod(m: VaultAuthMethod) {
    if (method.value === m) return;
    method.value = m;
    // Clear the auth slate; preserve top-level (server/path/version/namespace).
    onUpdateSpec({
      ...spec,
      auth: { [m]: emptyMethodSpec(m) },
    });
  }

  function patchAuth(method: VaultAuthMethod, patch: Record<string, unknown>) {
    const auth = (spec.auth as Record<string, unknown>) ?? {};
    const block = (auth[method] as Record<string, unknown>) ?? {};
    onUpdateSpec({
      ...spec,
      auth: { ...auth, [method]: { ...block, ...patch } },
    });
  }

  function patchSecretRef(
    method: VaultAuthMethod,
    refField: string,
    patch: SecretRef,
  ) {
    const block = getAuthBlock(spec, method);
    const existing = (block[refField] as SecretRef) ?? {};
    patchAuth(method, { [refField]: { ...existing, ...patch } });
  }

  return (
    <div class="space-y-5">
      <div class="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
        Configure the Vault server connection and authentication method.
        Credentials must already exist as Kubernetes Secrets in this namespace;
        this wizard only references them — it never holds Vault credentials
        directly.
      </div>

      {/* Top-level Vault fields */}
      <div class="grid grid-cols-2 gap-4">
        <Input
          id="vault-server"
          label="Server URL"
          required
          value={getStr(spec, "server")}
          onInput={(e) =>
            patchTop("server", (e.target as HTMLInputElement).value)}
          placeholder="https://vault.example.com:8200"
          description="Must use https. Private and in-cluster addresses are accepted."
          error={errors["server"]}
        />
        <Input
          id="vault-path"
          label="Mount path (optional)"
          value={getStr(spec, "path")}
          onInput={(e) =>
            patchTop("path", (e.target as HTMLInputElement).value)}
          placeholder="secret"
          description="KV mount name. Leave blank for ESO default."
          error={errors["path"]}
        />
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div class="space-y-1">
          <label
            for="vault-version"
            class="block text-sm font-medium text-text-secondary"
          >
            KV version
          </label>
          <select
            id="vault-version"
            class="block w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
            value={getStr(spec, "version") || "v2"}
            onChange={(e) =>
              patchTop("version", (e.target as HTMLSelectElement).value)}
          >
            <option value="v2">v2 (recommended)</option>
            <option value="v1">v1</option>
          </select>
          {errors["version"] && (
            <p class="text-sm text-danger">{errors["version"]}</p>
          )}
        </div>
        <Input
          id="vault-namespace"
          label="Vault namespace (Enterprise)"
          value={getStr(spec, "namespace")}
          onInput={(e) =>
            patchTop("namespace", (e.target as HTMLInputElement).value)}
          placeholder="admin/dev"
          description="Vault Enterprise namespaces only. Leave blank for OSS."
          error={errors["namespace"]}
        />
      </div>

      {/* Auth method picker */}
      <div class="space-y-3">
        <h3 class="text-sm font-semibold text-text-primary">
          Authentication method
          <span aria-hidden="true" class="text-danger ml-0.5">*</span>
        </h3>
        <div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
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
      {method.value === "token" && (
        <TokenAuthFields
          block={getAuthBlock(spec, "token")}
          errors={errors}
          onPatchRef={(patch) =>
            patchSecretRef("token", "tokenSecretRef", patch)}
        />
      )}
      {method.value === "kubernetes" && (
        <KubernetesAuthFields
          block={getAuthBlock(spec, "kubernetes")}
          errors={errors}
          onPatch={(patch) => patchAuth("kubernetes", patch)}
        />
      )}
      {method.value === "appRole" && (
        <AppRoleAuthFields
          block={getAuthBlock(spec, "appRole")}
          errors={errors}
          onPatch={(patch) => patchAuth("appRole", patch)}
          onPatchSecretRef={(patch) =>
            patchSecretRef("appRole", "secretRef", patch)}
        />
      )}
      {method.value === "jwt" && (
        <JWTAuthFields
          block={getAuthBlock(spec, "jwt")}
          errors={errors}
          onPatch={(patch) => patchAuth("jwt", patch)}
          onPatchSecretRef={(patch) =>
            patchSecretRef("jwt", "secretRef", patch)}
        />
      )}
      {method.value === "cert" && (
        <CertAuthFields
          block={getAuthBlock(spec, "cert")}
          errors={errors}
          onPatchClientCert={(patch) =>
            patchSecretRef("cert", "clientCert", patch)}
          onPatchSecretRef={(patch) =>
            patchSecretRef("cert", "secretRef", patch)}
        />
      )}
    </div>
  );
}

/** Initial empty block for a freshly-selected auth method. The wizard's
 *  validator rejects empty blocks so the user must populate before preview. */
function emptyMethodSpec(m: VaultAuthMethod): Record<string, unknown> {
  switch (m) {
    case "token":
      return { tokenSecretRef: {} };
    case "kubernetes":
      return {};
    case "appRole":
      return { secretRef: {} };
    case "jwt":
      return { secretRef: {} };
    case "cert":
      return { clientCert: {}, secretRef: {} };
  }
}

// --- Per-method field components ---------------------------------------

interface TokenAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchRef: (patch: SecretRef) => void;
}

function TokenAuthFields({ block, errors, onPatchRef }: TokenAuthFieldsProps) {
  const ref = (block.tokenSecretRef as SecretRef) ?? {};
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">
        Token Secret reference
      </h4>
      <div class="grid grid-cols-2 gap-3">
        <Input
          id="vault-token-ref-name"
          label="Secret name"
          required
          value={ref.name ?? ""}
          onInput={(e) =>
            onPatchRef({ name: (e.target as HTMLInputElement).value })}
          placeholder="vault-token"
          error={errors["auth.token.tokenSecretRef.name"]}
        />
        <Input
          id="vault-token-ref-key"
          label="Key"
          required
          value={ref.key ?? ""}
          onInput={(e) =>
            onPatchRef({ key: (e.target as HTMLInputElement).value })}
          placeholder="token"
          error={errors["auth.token.tokenSecretRef.key"]}
        />
      </div>
    </div>
  );
}

interface KubernetesAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatch: (patch: Record<string, unknown>) => void;
}

function KubernetesAuthFields(
  { block, errors, onPatch }: KubernetesAuthFieldsProps,
) {
  const mountPath = (block.mountPath as string) ?? "";
  const role = (block.role as string) ?? "";
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">Kubernetes auth</h4>
      <div class="grid grid-cols-2 gap-3">
        <Input
          id="vault-k8s-mount"
          label="Mount path"
          required
          value={mountPath}
          onInput={(e) =>
            onPatch({ mountPath: (e.target as HTMLInputElement).value })}
          placeholder="kubernetes"
          description="The Vault auth path where Kubernetes auth is enabled."
          error={errors["auth.kubernetes.mountPath"]}
        />
        <Input
          id="vault-k8s-role"
          label="Role"
          required
          value={role}
          onInput={(e) =>
            onPatch({ role: (e.target as HTMLInputElement).value })}
          placeholder="my-app"
          description="Vault role bound to this service account."
          error={errors["auth.kubernetes.role"]}
        />
      </div>
    </div>
  );
}

interface AppRoleAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatch: (patch: Record<string, unknown>) => void;
  onPatchSecretRef: (patch: SecretRef) => void;
}

function AppRoleAuthFields(
  { block, errors, onPatch, onPatchSecretRef }: AppRoleAuthFieldsProps,
) {
  const path = (block.path as string) ?? "";
  const roleId = (block.roleId as string) ?? "";
  const secretRef = (block.secretRef as SecretRef) ?? {};
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">AppRole auth</h4>
      <div class="grid grid-cols-2 gap-3">
        <Input
          id="vault-approle-path"
          label="Auth path"
          required
          value={path}
          onInput={(e) =>
            onPatch({ path: (e.target as HTMLInputElement).value })}
          placeholder="approle"
          error={errors["auth.appRole.path"]}
        />
        <Input
          id="vault-approle-roleid"
          label="Role ID"
          required
          value={roleId}
          onInput={(e) =>
            onPatch({ roleId: (e.target as HTMLInputElement).value })}
          placeholder="abc-123-…"
          description="The literal RoleID from `vault read auth/approle/role/<name>/role-id`."
          error={errors["auth.appRole.roleId"]}
        />
      </div>
      <h4 class="text-sm font-medium text-text-primary">
        SecretID Secret reference
      </h4>
      <div class="grid grid-cols-2 gap-3">
        <Input
          id="vault-approle-ref-name"
          label="Secret name"
          required
          value={secretRef.name ?? ""}
          onInput={(e) =>
            onPatchSecretRef({ name: (e.target as HTMLInputElement).value })}
          placeholder="approle-secret"
          error={errors["auth.appRole.secretRef.name"]}
        />
        <Input
          id="vault-approle-ref-key"
          label="Key"
          required
          value={secretRef.key ?? ""}
          onInput={(e) =>
            onPatchSecretRef({ key: (e.target as HTMLInputElement).value })}
          placeholder="secret-id"
          error={errors["auth.appRole.secretRef.key"]}
        />
      </div>
    </div>
  );
}

interface JWTAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatch: (patch: Record<string, unknown>) => void;
  onPatchSecretRef: (patch: SecretRef) => void;
}

function JWTAuthFields(
  { block, errors, onPatch, onPatchSecretRef }: JWTAuthFieldsProps,
) {
  const path = (block.path as string) ?? "";
  const role = (block.role as string) ?? "";
  const secretRef = (block.secretRef as SecretRef) ?? {};
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-3">
      <h4 class="text-sm font-medium text-text-primary">JWT / OIDC auth</h4>
      <div class="grid grid-cols-2 gap-3">
        <Input
          id="vault-jwt-path"
          label="Auth path"
          required
          value={path}
          onInput={(e) =>
            onPatch({ path: (e.target as HTMLInputElement).value })}
          placeholder="jwt"
          error={errors["auth.jwt.path"]}
        />
        <Input
          id="vault-jwt-role"
          label="Role (optional)"
          value={role}
          onInput={(e) =>
            onPatch({ role: (e.target as HTMLInputElement).value })}
          placeholder="my-role"
        />
      </div>
      <h4 class="text-sm font-medium text-text-primary">
        JWT Secret reference
      </h4>
      <div class="grid grid-cols-2 gap-3">
        <Input
          id="vault-jwt-ref-name"
          label="Secret name"
          required
          value={secretRef.name ?? ""}
          onInput={(e) =>
            onPatchSecretRef({ name: (e.target as HTMLInputElement).value })}
          placeholder="jwt-token"
          error={errors["auth.jwt.secretRef.name"]}
        />
        <Input
          id="vault-jwt-ref-key"
          label="Key"
          required
          value={secretRef.key ?? ""}
          onInput={(e) =>
            onPatchSecretRef({ key: (e.target as HTMLInputElement).value })}
          placeholder="jwt"
          error={errors["auth.jwt.secretRef.key"]}
        />
      </div>
    </div>
  );
}

interface CertAuthFieldsProps {
  block: Record<string, unknown>;
  errors: Record<string, string>;
  onPatchClientCert: (patch: SecretRef) => void;
  onPatchSecretRef: (patch: SecretRef) => void;
}

function CertAuthFields(
  { block, errors, onPatchClientCert, onPatchSecretRef }: CertAuthFieldsProps,
) {
  const clientCert = (block.clientCert as SecretRef) ?? {};
  const secretRef = (block.secretRef as SecretRef) ?? {};
  return (
    <div class="rounded-md border border-border-primary p-4 space-y-4">
      <div>
        <h4 class="text-sm font-medium text-text-primary mb-2">
          Client certificate
        </h4>
        <div class="grid grid-cols-2 gap-3">
          <Input
            id="vault-cert-cert-name"
            label="Secret name"
            required
            value={clientCert.name ?? ""}
            onInput={(e) =>
              onPatchClientCert({
                name: (e.target as HTMLInputElement).value,
              })}
            placeholder="vault-client-cert"
            error={errors["auth.cert.clientCert.name"]}
          />
          <Input
            id="vault-cert-cert-key"
            label="Key"
            required
            value={clientCert.key ?? ""}
            onInput={(e) =>
              onPatchClientCert({ key: (e.target as HTMLInputElement).value })}
            placeholder="tls.crt"
            error={errors["auth.cert.clientCert.key"]}
          />
        </div>
      </div>
      <div>
        <h4 class="text-sm font-medium text-text-primary mb-2">Client key</h4>
        <div class="grid grid-cols-2 gap-3">
          <Input
            id="vault-cert-key-name"
            label="Secret name"
            required
            value={secretRef.name ?? ""}
            onInput={(e) =>
              onPatchSecretRef({ name: (e.target as HTMLInputElement).value })}
            placeholder="vault-client-key"
            error={errors["auth.cert.secretRef.name"]}
          />
          <Input
            id="vault-cert-key-key"
            label="Key"
            required
            value={secretRef.key ?? ""}
            onInput={(e) =>
              onPatchSecretRef({ key: (e.target as HTMLInputElement).value })}
            placeholder="tls.key"
            error={errors["auth.cert.secretRef.key"]}
          />
        </div>
      </div>
    </div>
  );
}
