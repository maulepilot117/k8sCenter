import { LE_PROD_ACME, LE_STAGING_ACME } from "@/lib/wizard-constants.ts";
import { Input } from "@/components/ui/Input.tsx";
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
        <Input
          id="issuer-name"
          label="Name"
          required
          value={form.name}
          onInput={(e) =>
            onUpdate("name", (e.target as HTMLInputElement).value)}
          placeholder={scope === "cluster" ? "letsencrypt-prod" : "my-issuer"}
          error={errors.name}
        />

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
            <div class="flex gap-2 mb-1">
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
            <Input
              id="acme-server"
              label="ACME Server"
              required
              value={form.acme.server}
              onInput={(e) =>
                onUpdateAcme("server", (e.target as HTMLInputElement).value)}
              placeholder="https://acme-v02.api.letsencrypt.org/directory"
              description="Must be an HTTPS URL. Private and loopback addresses are rejected."
              error={errors["acme.server"]}
            />
          </div>

          <div class="grid grid-cols-2 gap-4">
            <Input
              id="acme-email"
              label="Contact Email"
              required
              type="email"
              value={form.acme.email}
              onInput={(e) =>
                onUpdateAcme("email", (e.target as HTMLInputElement).value)}
              placeholder="admin@example.com"
              error={errors["acme.email"]}
            />
            <Input
              id="acme-key-secret"
              label="Account Private Key Secret"
              required
              value={form.acme.privateKeySecretRefName}
              onInput={(e) =>
                onUpdateAcme(
                  "privateKeySecretRefName",
                  (e.target as HTMLInputElement).value,
                )}
              placeholder="letsencrypt-account"
              description="Name of the Secret cert-manager will create to hold the account key."
              error={errors["acme.privateKeySecretRefName"]}
            />
          </div>

          <Input
            id="acme-ingress-class"
            label="HTTP01 Ingress Class"
            value={form.acme.ingressClassName}
            onInput={(e) =>
              onUpdateAcme(
                "ingressClassName",
                (e.target as HTMLInputElement).value,
              )}
            placeholder="nginx"
            description="Ingress class used for HTTP01 challenges. Leave blank to use the default class."
          />
        </div>
      )}
    </div>
  );
}
