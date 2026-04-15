import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import type { Issuer } from "@/lib/certmanager-types.ts";
import type { CertificateWizardForm } from "@/islands/CertificateWizard.tsx";

interface CertificateFormProps {
  form: CertificateWizardForm;
  errors: Record<string, string>;
  issuers: Issuer[];
  issuersLoading: boolean;
  namespaces: string[];
  onUpdate: (field: string, value: unknown) => void;
  onUpdatePrivateKey: (field: string, value: unknown) => void;
}

const PRIVATE_KEY_ALGORITHMS = ["RSA", "ECDSA", "Ed25519"] as const;
const RSA_SIZES = [2048, 3072, 4096];
const ECDSA_SIZES = [256, 384, 521];
const ROTATION_POLICIES = ["Always", "Never"] as const;

export function CertificateForm({
  form,
  errors,
  issuers,
  issuersLoading,
  namespaces,
  onUpdate,
  onUpdatePrivateKey,
}: CertificateFormProps) {
  const issuerOptionValue = (iss: Issuer) =>
    `${iss.scope === "Cluster" ? "ClusterIssuer" : "Issuer"}:${iss.name}`;

  const sizeOptions = form.privateKey.algorithm === "ECDSA"
    ? ECDSA_SIZES
    : RSA_SIZES;

  return (
    <div class="space-y-5">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label
            for="cert-name"
            class="block text-sm font-medium text-text-primary"
          >
            Name <span class="text-danger">*</span>
          </label>
          <input
            id="cert-name"
            type="text"
            value={form.name}
            onInput={(e) =>
              onUpdate("name", (e.target as HTMLInputElement).value)}
            placeholder="example-com-tls"
            class={WIZARD_INPUT_CLASS}
            aria-invalid={errors.name ? "true" : undefined}
            aria-describedby={errors.name ? "cert-name-error" : undefined}
          />
          {errors.name && (
            <p id="cert-name-error" class="mt-1 text-xs text-danger">
              {errors.name}
            </p>
          )}
        </div>

        <NamespaceSelect
          value={form.namespace}
          namespaces={namespaces}
          error={errors.namespace}
          onChange={(ns) => onUpdate("namespace", ns)}
        />
      </div>

      <div>
        <label
          for="cert-secret-name"
          class="block text-sm font-medium text-text-primary"
        >
          Secret Name <span class="text-danger">*</span>
        </label>
        <input
          id="cert-secret-name"
          type="text"
          value={form.secretName}
          onInput={(e) =>
            onUpdate("secretName", (e.target as HTMLInputElement).value)}
          placeholder="example-com-tls"
          class={WIZARD_INPUT_CLASS}
          aria-invalid={errors.secretName ? "true" : undefined}
          aria-describedby={errors.secretName
            ? "cert-secret-name-error"
            : undefined}
        />
        <p class="mt-1 text-xs text-text-muted">
          Secret where cert-manager will write the issued TLS certificate and
          private key.
        </p>
        {errors.secretName && (
          <p id="cert-secret-name-error" class="mt-1 text-xs text-danger">
            {errors.secretName}
          </p>
        )}
      </div>

      <div>
        <label
          for="cert-issuer"
          class="block text-sm font-medium text-text-primary"
        >
          Issuer <span class="text-danger">*</span>
        </label>
        <select
          id="cert-issuer"
          value={form.issuerRefValue}
          onChange={(e) =>
            onUpdate(
              "issuerRefValue",
              (e.target as HTMLSelectElement).value,
            )}
          class={WIZARD_INPUT_CLASS}
          disabled={issuersLoading}
        >
          <option value="">
            {issuersLoading ? "Loading issuers..." : "Select an issuer"}
          </option>
          {issuers.filter((i) => i.scope === "Cluster").length > 0 && (
            <optgroup label="ClusterIssuers">
              {issuers
                .filter((i) => i.scope === "Cluster")
                .map((i) => (
                  <option key={i.uid} value={issuerOptionValue(i)}>
                    {i.name} ({i.type})
                  </option>
                ))}
            </optgroup>
          )}
          {issuers.filter((i) => i.scope === "Namespaced").length > 0 && (
            <optgroup label="Issuers (namespaced)">
              {issuers
                .filter((i) => i.scope === "Namespaced")
                .map((i) => (
                  <option key={i.uid} value={issuerOptionValue(i)}>
                    {i.name} / {i.namespace} ({i.type})
                  </option>
                ))}
            </optgroup>
          )}
        </select>
        {errors.issuerRef && (
          <p class="mt-1 text-xs text-danger">{errors.issuerRef}</p>
        )}
      </div>

      <div>
        <label class="block text-sm font-medium text-text-primary">
          DNS Names
        </label>
        <input
          type="text"
          value={form.dnsNamesInput}
          onInput={(e) =>
            onUpdate(
              "dnsNamesInput",
              (e.target as HTMLInputElement).value,
            )}
          placeholder="example.com, www.example.com"
          class={WIZARD_INPUT_CLASS}
        />
        <p class="mt-1 text-xs text-text-muted">
          Comma-separated. At least one of DNS Names or Common Name is required.
        </p>
        {errors.dnsNames && (
          <p class="mt-1 text-xs text-danger">{errors.dnsNames}</p>
        )}
      </div>

      <div>
        <label class="block text-sm font-medium text-text-primary">
          Common Name
        </label>
        <input
          type="text"
          value={form.commonName}
          onInput={(e) =>
            onUpdate("commonName", (e.target as HTMLInputElement).value)}
          placeholder="example.com"
          class={WIZARD_INPUT_CLASS}
        />
      </div>

      <details class="rounded-md border border-border-primary bg-surface/50 p-4">
        <summary class="cursor-pointer text-sm font-medium text-text-primary">
          Advanced options
        </summary>

        <div class="mt-4 space-y-4">
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium text-text-primary">
                Duration
              </label>
              <input
                type="text"
                value={form.duration}
                onInput={(e) =>
                  onUpdate(
                    "duration",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="2160h"
                class={WIZARD_INPUT_CLASS}
              />
              <p class="mt-1 text-xs text-text-muted">
                Default 2160h (90 days).
              </p>
              {errors.duration && (
                <p class="mt-1 text-xs text-danger">{errors.duration}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-primary">
                Renew Before
              </label>
              <input
                type="text"
                value={form.renewBefore}
                onInput={(e) =>
                  onUpdate(
                    "renewBefore",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="360h"
                class={WIZARD_INPUT_CLASS}
              />
              <p class="mt-1 text-xs text-text-muted">
                Default 360h (15 days).
              </p>
              {errors.renewBefore && (
                <p class="mt-1 text-xs text-danger">{errors.renewBefore}</p>
              )}
            </div>
          </div>

          <div class="grid grid-cols-3 gap-4">
            <div>
              <label class="block text-sm font-medium text-text-primary">
                Algorithm
              </label>
              <select
                value={form.privateKey.algorithm}
                onChange={(e) =>
                  onUpdatePrivateKey(
                    "algorithm",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                {PRIVATE_KEY_ALGORITHMS.map((a) => (
                  <option key={a} value={a}>{a}</option>
                ))}
              </select>
            </div>

            <div>
              <label class="block text-sm font-medium text-text-primary">
                Key Size
              </label>
              <select
                value={form.privateKey.size}
                onChange={(e) =>
                  onUpdatePrivateKey(
                    "size",
                    Number((e.target as HTMLSelectElement).value),
                  )}
                class={WIZARD_INPUT_CLASS}
                disabled={form.privateKey.algorithm === "Ed25519"}
              >
                {sizeOptions.map((s) => <option key={s} value={s}>{s}</option>)}
              </select>
            </div>

            <div>
              <label class="block text-sm font-medium text-text-primary">
                Rotation
              </label>
              <select
                value={form.privateKey.rotationPolicy}
                onChange={(e) =>
                  onUpdatePrivateKey(
                    "rotationPolicy",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                {ROTATION_POLICIES.map((p) => (
                  <option key={p} value={p}>{p}</option>
                ))}
              </select>
            </div>
          </div>

          <div>
            <label class="inline-flex items-center gap-2 text-sm text-text-primary">
              <input
                type="checkbox"
                checked={form.isCA}
                onChange={(e) =>
                  onUpdate(
                    "isCA",
                    (e.target as HTMLInputElement).checked,
                  )}
              />
              <span>Issue as CA certificate (isCA: true)</span>
            </label>
          </div>
        </div>
      </details>
    </div>
  );
}
