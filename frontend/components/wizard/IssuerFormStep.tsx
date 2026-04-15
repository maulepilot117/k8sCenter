import {
  LE_PROD_ACME,
  LE_STAGING_ACME,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import type { IssuerWizardForm } from "@/islands/IssuerWizard.tsx";

interface IssuerFormStepProps {
  scope: "namespaced" | "cluster";
  form: IssuerWizardForm;
  errors: Record<string, string>;
  namespaces: string[];
  onUpdate: (field: string, value: unknown) => void;
  onUpdateAcme: (field: string, value: unknown) => void;
}

export function IssuerFormStep({
  scope,
  form,
  errors,
  namespaces,
  onUpdate,
  onUpdateAcme,
}: IssuerFormStepProps) {
  return (
    <div class="space-y-5">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label
            for="issuer-name"
            class="block text-sm font-medium text-text-primary"
          >
            Name <span class="text-danger">*</span>
          </label>
          <input
            id="issuer-name"
            type="text"
            value={form.name}
            onInput={(e) =>
              onUpdate("name", (e.target as HTMLInputElement).value)}
            placeholder={scope === "cluster" ? "letsencrypt-prod" : "my-issuer"}
            class={WIZARD_INPUT_CLASS}
            aria-invalid={errors.name ? "true" : undefined}
            aria-describedby={errors.name ? "issuer-name-error" : undefined}
          />
          {errors.name && (
            <p id="issuer-name-error" class="mt-1 text-xs text-danger">
              {errors.name}
            </p>
          )}
        </div>

        {scope === "namespaced" && (
          <NamespaceSelect
            value={form.namespace}
            namespaces={namespaces}
            error={errors.namespace}
            onChange={(ns) => onUpdate("namespace", ns)}
          />
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
            <label
              for="acme-server"
              class="block text-sm font-medium text-text-primary"
            >
              ACME Server <span class="text-danger">*</span>
            </label>
            <div class="mt-1 flex gap-2">
              <button
                type="button"
                class={`text-xs rounded border px-2 py-1 ${
                  form.acme.server === LE_STAGING_ACME
                    ? "border-brand text-brand"
                    : "border-border-primary text-text-muted"
                }`}
                onClick={() => onUpdateAcme("server", LE_STAGING_ACME)}
              >
                Let's Encrypt Staging
              </button>
              <button
                type="button"
                class={`text-xs rounded border px-2 py-1 ${
                  form.acme.server === LE_PROD_ACME
                    ? "border-brand text-brand"
                    : "border-border-primary text-text-muted"
                }`}
                onClick={() => onUpdateAcme("server", LE_PROD_ACME)}
              >
                Let's Encrypt Production
              </button>
            </div>
            <input
              id="acme-server"
              type="text"
              value={form.acme.server}
              onInput={(e) =>
                onUpdateAcme("server", (e.target as HTMLInputElement).value)}
              placeholder="https://acme-v02.api.letsencrypt.org/directory"
              class={`${WIZARD_INPUT_CLASS} mt-2`}
              aria-invalid={errors["acme.server"] ? "true" : undefined}
              aria-describedby={errors["acme.server"]
                ? "acme-server-error"
                : undefined}
            />
            <p class="mt-1 text-xs text-text-muted">
              Must be an HTTPS URL. Private and loopback addresses are rejected.
            </p>
            {errors["acme.server"] && (
              <p id="acme-server-error" class="mt-1 text-xs text-danger">
                {errors["acme.server"]}
              </p>
            )}
          </div>

          <div class="grid grid-cols-2 gap-4">
            <div>
              <label
                for="acme-email"
                class="block text-sm font-medium text-text-primary"
              >
                Contact Email <span class="text-danger">*</span>
              </label>
              <input
                id="acme-email"
                type="email"
                value={form.acme.email}
                onInput={(e) =>
                  onUpdateAcme(
                    "email",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="admin@example.com"
                class={WIZARD_INPUT_CLASS}
                aria-invalid={errors["acme.email"] ? "true" : undefined}
                aria-describedby={errors["acme.email"]
                  ? "acme-email-error"
                  : undefined}
              />
              {errors["acme.email"] && (
                <p id="acme-email-error" class="mt-1 text-xs text-danger">
                  {errors["acme.email"]}
                </p>
              )}
            </div>

            <div>
              <label
                for="acme-key-secret"
                class="block text-sm font-medium text-text-primary"
              >
                Account Private Key Secret <span class="text-danger">*</span>
              </label>
              <input
                id="acme-key-secret"
                type="text"
                value={form.acme.privateKeySecretRefName}
                onInput={(e) =>
                  onUpdateAcme(
                    "privateKeySecretRefName",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="letsencrypt-account"
                class={WIZARD_INPUT_CLASS}
                aria-invalid={errors["acme.privateKeySecretRefName"]
                  ? "true"
                  : undefined}
                aria-describedby={errors["acme.privateKeySecretRefName"]
                  ? "acme-key-secret-error"
                  : undefined}
              />
              <p class="mt-1 text-xs text-text-muted">
                Name of the Secret cert-manager will create to hold the account
                key.
              </p>
              {errors["acme.privateKeySecretRefName"] && (
                <p
                  id="acme-key-secret-error"
                  class="mt-1 text-xs text-danger"
                >
                  {errors["acme.privateKeySecretRefName"]}
                </p>
              )}
            </div>
          </div>

          <div>
            <label
              for="acme-ingress-class"
              class="block text-sm font-medium text-text-primary"
            >
              HTTP01 Ingress Class
            </label>
            <input
              id="acme-ingress-class"
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
    </div>
  );
}
