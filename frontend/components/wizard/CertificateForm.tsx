import { Input } from "@/components/ui/Input.tsx";
import { Select } from "@/components/ui/Select.tsx";
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

  const clusterIssuers = issuers.filter((i) => i.scope === "Cluster");
  const namespacedIssuers = issuers.filter((i) => i.scope === "Namespaced");

  return (
    <div class="space-y-5">
      <div class="grid grid-cols-2 gap-4">
        <Input
          id="cert-name"
          label="Name"
          required
          value={form.name}
          onInput={(e) =>
            onUpdate("name", (e.target as HTMLInputElement).value)}
          placeholder="example-com-tls"
          error={errors.name}
        />

        <NamespaceSelect
          value={form.namespace}
          namespaces={namespaces}
          error={errors.namespace}
          onChange={(ns) => onUpdate("namespace", ns)}
        />
      </div>

      <Input
        id="cert-secret-name"
        label="Secret Name"
        required
        value={form.secretName}
        onInput={(e) =>
          onUpdate("secretName", (e.target as HTMLInputElement).value)}
        placeholder="example-com-tls"
        description="Secret where cert-manager will write the issued TLS certificate and private key."
        error={errors.secretName}
      />

      <Select
        id="cert-issuer"
        label="Issuer"
        required
        value={form.issuerRefValue}
        onChange={(e) =>
          onUpdate(
            "issuerRefValue",
            (e.target as HTMLSelectElement).value,
          )}
        disabled={issuersLoading}
        error={errors.issuerRef}
      >
        <option value="">
          {issuersLoading ? "Loading issuers..." : "Select an issuer"}
        </option>
        {clusterIssuers.length > 0 && (
          <optgroup label="ClusterIssuers">
            {clusterIssuers.map((i) => (
              <option key={i.uid} value={issuerOptionValue(i)}>
                {i.name} ({i.type})
              </option>
            ))}
          </optgroup>
        )}
        {namespacedIssuers.length > 0 && (
          <optgroup label="Issuers (namespaced)">
            {namespacedIssuers.map((i) => (
              <option key={i.uid} value={issuerOptionValue(i)}>
                {i.name} / {i.namespace} ({i.type})
              </option>
            ))}
          </optgroup>
        )}
      </Select>

      <Input
        id="cert-dns-names"
        label="DNS Names"
        value={form.dnsNamesInput}
        onInput={(e) =>
          onUpdate("dnsNamesInput", (e.target as HTMLInputElement).value)}
        placeholder="example.com, www.example.com"
        description="Comma-separated. At least one of DNS Names or Common Name is required."
        error={errors.dnsNames}
      />

      <Input
        id="cert-common-name"
        label="Common Name"
        value={form.commonName}
        onInput={(e) =>
          onUpdate("commonName", (e.target as HTMLInputElement).value)}
        placeholder="example.com"
      />

      <details class="rounded-md border border-border-primary bg-surface/50 p-4">
        <summary class="cursor-pointer text-sm font-medium text-text-primary">
          Advanced options
        </summary>

        <div class="mt-4 space-y-4">
          <div class="grid grid-cols-2 gap-4">
            <Input
              id="cert-duration"
              label="Duration"
              value={form.duration}
              onInput={(e) =>
                onUpdate("duration", (e.target as HTMLInputElement).value)}
              placeholder="2160h"
              description="Default 2160h (90 days)."
              error={errors.duration}
            />
            <Input
              id="cert-renew-before"
              label="Renew Before"
              value={form.renewBefore}
              onInput={(e) =>
                onUpdate(
                  "renewBefore",
                  (e.target as HTMLInputElement).value,
                )}
              placeholder="360h"
              description="Default 360h (15 days)."
              error={errors.renewBefore}
            />
          </div>

          <div class="grid grid-cols-3 gap-4">
            <Select
              id="cert-pk-algorithm"
              label="Algorithm"
              value={form.privateKey.algorithm}
              onChange={(e) =>
                onUpdatePrivateKey(
                  "algorithm",
                  (e.target as HTMLSelectElement).value,
                )}
              options={PRIVATE_KEY_ALGORITHMS.map((a) => ({
                value: a,
                label: a,
              }))}
            />
            <Select
              id="cert-pk-size"
              label="Key Size"
              value={String(form.privateKey.size)}
              onChange={(e) =>
                onUpdatePrivateKey(
                  "size",
                  Number((e.target as HTMLSelectElement).value),
                )}
              disabled={form.privateKey.algorithm === "Ed25519"}
              options={sizeOptions.map((s) => ({
                value: String(s),
                label: String(s),
              }))}
            />
            <Select
              id="cert-pk-rotation"
              label="Rotation"
              value={form.privateKey.rotationPolicy}
              onChange={(e) =>
                onUpdatePrivateKey(
                  "rotationPolicy",
                  (e.target as HTMLSelectElement).value,
                )}
              options={ROTATION_POLICIES.map((p) => ({ value: p, label: p }))}
            />
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
