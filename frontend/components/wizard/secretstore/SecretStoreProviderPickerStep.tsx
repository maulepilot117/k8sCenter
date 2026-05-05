import type { SecretStoreProvider } from "@/lib/eso-types.ts";
import { READY_SECRET_STORE_PROVIDERS } from "@/lib/eso-types.ts";

interface SecretStoreProviderPickerStepProps {
  selected: SecretStoreProvider | "";
  onSelect: (p: SecretStoreProvider) => void;
}

interface ProviderEntry {
  id: SecretStoreProvider;
  title: string;
  description: string;
}

const PROVIDERS: ProviderEntry[] = [
  {
    id: "vault",
    title: "HashiCorp Vault",
    description:
      "On-prem or cloud Vault. KV v2 + Transit. Token / Kubernetes / AppRole / JWT / Cert auth.",
  },
  {
    id: "aws",
    title: "AWS Secrets Manager",
    description: "Region-scoped. IAM workload identity or static keys.",
  },
  {
    id: "awsps",
    title: "AWS Parameter Store",
    description:
      "Region-scoped. IAM workload identity. Standard or advanced parameter tier.",
  },
  {
    id: "azurekv",
    title: "Azure Key Vault",
    description:
      "Vault URL + tenant. Managed identity, service principal, or workload identity.",
  },
  {
    id: "gcpsm",
    title: "GCP Secret Manager",
    description: "Project ID. Workload identity or service account key.",
  },
  {
    id: "kubernetes",
    title: "Kubernetes (cross-namespace)",
    description:
      "Read Secrets from another namespace via service account impersonation.",
  },
  {
    id: "akeyless",
    title: "Akeyless",
    description: "JWT / Kubernetes / plain auth. Free-text item path.",
  },
  {
    id: "doppler",
    title: "Doppler",
    description: "Service token. Project + config selectors.",
  },
  {
    id: "onepassword",
    title: "1Password Connect",
    description: "Connect token + host URL. Vault name → priority mapping.",
  },
  {
    id: "bitwardensecretsmanager",
    title: "Bitwarden Secrets Manager",
    description: "Access token. Project picker.",
  },
  {
    id: "conjur",
    title: "CyberArk Conjur",
    description: "API key or JWT. Free-text path.",
  },
  {
    id: "infisical",
    title: "Infisical",
    description: "Universal-auth (machine identity). Project + environment.",
  },
];

export function SecretStoreProviderPickerStep({
  selected,
  onSelect,
}: SecretStoreProviderPickerStepProps) {
  return (
    <div class="space-y-4">
      <p class="text-sm text-text-muted">
        Select the source-store backend. Providers marked{" "}
        <span class="font-medium">coming soon</span>{" "}
        are recognized but their guided form ships in a follow-up; use the YAML
        editor for those today.
      </p>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {PROVIDERS.map((p) => {
          const active = selected === p.id;
          const ready = READY_SECRET_STORE_PROVIDERS.has(p.id);
          return (
            <button
              key={p.id}
              type="button"
              onClick={() => {
                if (!ready) return;
                onSelect(p.id);
              }}
              aria-pressed={active}
              aria-disabled={!ready || undefined}
              class={`text-left rounded-lg border p-4 transition-colors ${
                active
                  ? "border-brand bg-brand/5"
                  : ready
                  ? "border-border-primary bg-surface hover:border-border-emphasis"
                  : "border-border-primary bg-surface opacity-60 cursor-not-allowed"
              }`}
            >
              <div class="flex items-center justify-between gap-2">
                <span class="font-medium text-text-primary">{p.title}</span>
                {active
                  ? (
                    <span class="text-xs font-medium text-brand whitespace-nowrap">
                      Selected
                    </span>
                  )
                  : !ready
                  ? (
                    <span class="text-xs font-medium text-text-muted whitespace-nowrap">
                      coming soon
                    </span>
                  )
                  : null}
              </div>
              <p class="mt-2 text-sm text-text-muted">{p.description}</p>
            </button>
          );
        })}
      </div>
    </div>
  );
}
