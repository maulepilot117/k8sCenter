import type { SecretStoreProvider } from "@/islands/SecretStoreWizard.tsx";

interface SecretStoreProviderPickerStepProps {
  selected: SecretStoreProvider | "";
  onSelect: (p: SecretStoreProvider) => void;
}

interface ProviderEntry {
  id: SecretStoreProvider;
  title: string;
  description: string;
  /** True once Phase H Unit 19 lands a per-provider validator + form for this
   *  provider. Until then the wizard preview falls through to "not yet
   *  implemented" and the picker disables the option with explanatory copy.
   *  This file is the single edit point as providers ship in Unit 19. */
  ready: boolean;
}

const PROVIDERS: ProviderEntry[] = [
  {
    id: "vault",
    title: "HashiCorp Vault",
    description:
      "On-prem or cloud Vault. KV v2 + Transit. Token / Kubernetes / AppRole / JWT / Cert auth.",
    ready: false,
  },
  {
    id: "aws",
    title: "AWS Secrets Manager",
    description: "Region-scoped. IAM workload identity or static keys.",
    ready: false,
  },
  {
    id: "awsps",
    title: "AWS Parameter Store",
    description:
      "Region-scoped. IAM workload identity. Standard or advanced parameter tier.",
    ready: false,
  },
  {
    id: "azurekv",
    title: "Azure Key Vault",
    description:
      "Vault URL + tenant. Managed identity, service principal, or workload identity.",
    ready: false,
  },
  {
    id: "gcpsm",
    title: "GCP Secret Manager",
    description: "Project ID. Workload identity or service account key.",
    ready: false,
  },
  {
    id: "kubernetes",
    title: "Kubernetes (cross-namespace)",
    description:
      "Read Secrets from another namespace via service account impersonation.",
    ready: false,
  },
  {
    id: "akeyless",
    title: "Akeyless",
    description: "JWT / Kubernetes / plain auth. Free-text item path.",
    ready: false,
  },
  {
    id: "doppler",
    title: "Doppler",
    description: "Service token. Project + config selectors.",
    ready: false,
  },
  {
    id: "onepasswordsdk",
    title: "1Password Connect",
    description: "Connect token. Vault + item picker.",
    ready: false,
  },
  {
    id: "bitwardensecretsmanager",
    title: "Bitwarden Secrets Manager",
    description: "Access token. Project picker.",
    ready: false,
  },
  {
    id: "conjur",
    title: "CyberArk Conjur",
    description: "API key or JWT. Free-text path.",
    ready: false,
  },
  {
    id: "infisical",
    title: "Infisical",
    description: "Universal-auth (machine identity). Project + environment.",
    ready: false,
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
          return (
            <button
              key={p.id}
              type="button"
              onClick={() => onSelect(p.id)}
              class={`text-left rounded-lg border p-4 transition-colors ${
                active
                  ? "border-brand bg-brand/5"
                  : "border-border-primary bg-surface hover:border-border-emphasis"
              }`}
              aria-pressed={active}
            >
              <div class="flex items-center justify-between gap-2">
                <span class="font-medium text-text-primary">{p.title}</span>
                {active
                  ? (
                    <span class="text-xs font-medium text-brand whitespace-nowrap">
                      Selected
                    </span>
                  )
                  : !p.ready
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
