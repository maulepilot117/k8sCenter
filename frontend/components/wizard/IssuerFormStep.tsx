import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type {
  IssuerWizardForm,
  VaultAuthMethod,
} from "@/islands/IssuerWizard.tsx";

interface IssuerFormStepProps {
  scope: "namespaced" | "cluster";
  form: IssuerWizardForm;
  errors: Record<string, string>;
  onUpdate: (field: string, value: unknown) => void;
  onUpdateAcme: (field: string, value: unknown) => void;
  onUpdateCa: (field: string, value: unknown) => void;
  onUpdateVault: (field: string, value: unknown) => void;
  onUpdateVaultAuth: (method: VaultAuthMethod, value: string) => void;
}

const LE_PROD = "https://acme-v02.api.letsencrypt.org/directory";
const LE_STAGING = "https://acme-staging-v02.api.letsencrypt.org/directory";

export function IssuerFormStep({
  scope,
  form,
  errors,
  onUpdate,
  onUpdateAcme,
  onUpdateCa,
  onUpdateVault,
  onUpdateVaultAuth,
}: IssuerFormStepProps) {
  return (
    <div class="space-y-5">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-sm font-medium text-text-primary">
            Name <span class="text-danger">*</span>
          </label>
          <input
            type="text"
            value={form.name}
            onInput={(e) =>
              onUpdate("name", (e.target as HTMLInputElement).value)}
            placeholder={scope === "cluster" ? "letsencrypt-prod" : "my-issuer"}
            class={WIZARD_INPUT_CLASS}
          />
          {errors.name && <p class="mt-1 text-xs text-danger">{errors.name}</p>}
        </div>

        {scope === "namespaced" && (
          <div>
            <label class="block text-sm font-medium text-text-primary">
              Namespace <span class="text-danger">*</span>
            </label>
            <input
              type="text"
              value={form.namespace}
              onInput={(e) =>
                onUpdate(
                  "namespace",
                  (e.target as HTMLInputElement).value,
                )}
              placeholder="default"
              class={WIZARD_INPUT_CLASS}
            />
            {errors.namespace && (
              <p class="mt-1 text-xs text-danger">{errors.namespace}</p>
            )}
          </div>
        )}
      </div>

      {form.type === "selfSigned" && (
        <div class="rounded-md border border-border-primary bg-surface/50 p-4 text-sm text-text-muted">
          Self-signed issuers have no additional configuration. They sign
          certificates using their own temporary key material.
        </div>
      )}

      {form.type === "acme" && (
        <div class="space-y-4">
          <div>
            <label class="block text-sm font-medium text-text-primary">
              ACME Server <span class="text-danger">*</span>
            </label>
            <div class="mt-1 flex gap-2">
              <button
                type="button"
                class={`text-xs rounded border px-2 py-1 ${
                  form.acme.server === LE_STAGING
                    ? "border-brand text-brand"
                    : "border-border-primary text-text-muted"
                }`}
                onClick={() => onUpdateAcme("server", LE_STAGING)}
              >
                Let's Encrypt Staging
              </button>
              <button
                type="button"
                class={`text-xs rounded border px-2 py-1 ${
                  form.acme.server === LE_PROD
                    ? "border-brand text-brand"
                    : "border-border-primary text-text-muted"
                }`}
                onClick={() => onUpdateAcme("server", LE_PROD)}
              >
                Let's Encrypt Production
              </button>
            </div>
            <input
              type="text"
              value={form.acme.server}
              onInput={(e) =>
                onUpdateAcme("server", (e.target as HTMLInputElement).value)}
              placeholder="https://acme-v02.api.letsencrypt.org/directory"
              class={`${WIZARD_INPUT_CLASS} mt-2`}
            />
            <p class="mt-1 text-xs text-text-muted">
              Must be an HTTPS URL. Private IPs are rejected.
            </p>
            {errors["acme.server"] && (
              <p class="mt-1 text-xs text-danger">{errors["acme.server"]}</p>
            )}
          </div>

          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium text-text-primary">
                Contact Email <span class="text-danger">*</span>
              </label>
              <input
                type="email"
                value={form.acme.email}
                onInput={(e) =>
                  onUpdateAcme(
                    "email",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="admin@example.com"
                class={WIZARD_INPUT_CLASS}
              />
              {errors["acme.email"] && (
                <p class="mt-1 text-xs text-danger">{errors["acme.email"]}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-primary">
                Account Private Key Secret <span class="text-danger">*</span>
              </label>
              <input
                type="text"
                value={form.acme.privateKeySecretRefName}
                onInput={(e) =>
                  onUpdateAcme(
                    "privateKeySecretRefName",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="letsencrypt-account"
                class={WIZARD_INPUT_CLASS}
              />
              <p class="mt-1 text-xs text-text-muted">
                Name of the Secret cert-manager will create to hold the account
                key.
              </p>
              {errors["acme.privateKeySecretRefName"] && (
                <p class="mt-1 text-xs text-danger">
                  {errors["acme.privateKeySecretRefName"]}
                </p>
              )}
            </div>
          </div>

          <div>
            <label class="block text-sm font-medium text-text-primary">
              HTTP01 Ingress Class
            </label>
            <input
              type="text"
              value={form.acme.ingressClassName}
              onInput={(e) =>
                onUpdateAcme(
                  "ingressClassName",
                  (e.target as HTMLInputElement).value,
                )}
              placeholder="nginx"
              class={WIZARD_INPUT_CLASS}
            />
            <p class="mt-1 text-xs text-text-muted">
              Ingress class used for HTTP01 challenges. Leave blank to use the
              default class.
            </p>
          </div>
        </div>
      )}

      {form.type === "ca" && (
        <div>
          <label class="block text-sm font-medium text-text-primary">
            CA Secret Name <span class="text-danger">*</span>
          </label>
          <input
            type="text"
            value={form.ca.secretName}
            onInput={(e) =>
              onUpdateCa(
                "secretName",
                (e.target as HTMLInputElement).value,
              )}
            placeholder="my-ca-secret"
            class={WIZARD_INPUT_CLASS}
          />
          <p class="mt-1 text-xs text-text-muted">
            Secret containing tls.crt and tls.key for the signing CA.
          </p>
          {errors["ca.secretName"] && (
            <p class="mt-1 text-xs text-danger">{errors["ca.secretName"]}</p>
          )}
        </div>
      )}

      {form.type === "vault" && (
        <div class="space-y-4">
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium text-text-primary">
                Vault Server <span class="text-danger">*</span>
              </label>
              <input
                type="text"
                value={form.vault.server}
                onInput={(e) =>
                  onUpdateVault(
                    "server",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="https://vault.example.com"
                class={WIZARD_INPUT_CLASS}
              />
              {errors["vault.server"] && (
                <p class="mt-1 text-xs text-danger">
                  {errors["vault.server"]}
                </p>
              )}
            </div>
            <div>
              <label class="block text-sm font-medium text-text-primary">
                PKI Path <span class="text-danger">*</span>
              </label>
              <input
                type="text"
                value={form.vault.path}
                onInput={(e) =>
                  onUpdateVault(
                    "path",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="pki/sign/example-dot-com"
                class={WIZARD_INPUT_CLASS}
              />
              {errors["vault.path"] && (
                <p class="mt-1 text-xs text-danger">
                  {errors["vault.path"]}
                </p>
              )}
            </div>
          </div>

          <div>
            <label class="block text-sm font-medium text-text-primary mb-1">
              Authentication
            </label>
            <div class="space-y-2">
              {[
                {
                  id: "token" as VaultAuthMethod,
                  label: "Token Secret Name",
                  placeholder: "vault-token",
                },
                {
                  id: "appRole" as VaultAuthMethod,
                  label: "AppRole Secret Name",
                  placeholder: "vault-approle",
                },
                {
                  id: "kubernetes" as VaultAuthMethod,
                  label: "Kubernetes Role",
                  placeholder: "cert-manager",
                },
              ].map(({ id, label, placeholder }) => (
                <div key={id} class="flex items-center gap-3">
                  <input
                    type="radio"
                    name="vault-auth-method"
                    checked={form.vault.authMethod === id}
                    onChange={() =>
                      onUpdateVaultAuth(id, form.vault.authValue)}
                    class="h-4 w-4"
                  />
                  <label class="text-sm text-text-primary w-44">
                    {label}
                  </label>
                  <input
                    type="text"
                    value={form.vault.authMethod === id
                      ? form.vault.authValue
                      : ""}
                    onInput={(e) =>
                      onUpdateVaultAuth(
                        id,
                        (e.target as HTMLInputElement).value,
                      )}
                    placeholder={placeholder}
                    class={`${WIZARD_INPUT_CLASS} flex-1`}
                    disabled={form.vault.authMethod !== id}
                  />
                </div>
              ))}
            </div>
            {errors["vault.auth"] && (
              <p class="mt-1 text-xs text-danger">{errors["vault.auth"]}</p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
