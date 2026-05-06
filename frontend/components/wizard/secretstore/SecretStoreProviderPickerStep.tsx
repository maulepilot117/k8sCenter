import type { SecretStoreProvider } from "@/lib/eso-types.ts";
import {
  isTemplateOnlyProvider,
  READY_SECRET_STORE_PROVIDERS,
} from "@/lib/eso-types.ts";

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
  {
    id: "pulumi",
    title: "Pulumi ESC",
    description:
      "Pulumi Environments, Secrets & Configuration. Org + project + environment.",
  },
  {
    id: "passbolt",
    title: "Passbolt",
    description:
      "Passbolt CE / Pro. GPG private key + passphrase. Resource UUID references.",
  },
  {
    id: "keepersecurity",
    title: "Keeper Secrets Manager",
    description:
      "KSM config blob (base64). Records by record UID; folder-scoped.",
  },
  {
    id: "onboardbase",
    title: "Onboardbase",
    description: "API key + passcode. Project + environment binding.",
  },
  {
    id: "oracle",
    title: "Oracle Cloud Vault",
    description:
      "OCI Vault. Workload / instance principal (preferred) or API-key creds.",
  },
  {
    id: "alibaba",
    title: "Alibaba Cloud KMS",
    description:
      "Region-scoped KMS Secret. AccessKey ID + secret in a Kubernetes Secret.",
  },
  {
    id: "webhook",
    title: "Generic webhook",
    description:
      "Any HTTP/JSON backend. URL templating + jsonPath response selector.",
  },
];

export function SecretStoreProviderPickerStep({
  selected,
  onSelect,
}: SecretStoreProviderPickerStepProps) {
  return (
    <div class="space-y-4">
      <p class="text-sm text-text-muted">
        Select the source-store backend. Providers without a guided form open a
        pre-filled YAML template instead — fill the placeholders and apply.
      </p>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {PROVIDERS.map((p) => {
          const active = selected === p.id;
          const ready = READY_SECRET_STORE_PROVIDERS.has(p.id);
          const templateOnly = isTemplateOnlyProvider(p.id);
          // Tri-state:
          //   ready        → click triggers wizard onSelect (current behavior)
          //   templateOnly → click navigates to the template editor
          //   neither      → defensive disabled state; should not occur after
          //                  Phase K but kept so a future split-PR doesn't
          //                  silently render a dead button.
          const clickable = ready || templateOnly;
          const handleClick = () => {
            if (ready) {
              onSelect(p.id);
              return;
            }
            if (templateOnly) {
              globalThis.location.href =
                `/external-secrets/stores/new-from-template?template=${p.id}`;
            }
          };
          return (
            <button
              key={p.id}
              type="button"
              onClick={handleClick}
              disabled={!clickable}
              aria-pressed={active}
              aria-disabled={!clickable || undefined}
              class={`text-left rounded-lg border p-4 transition-colors ${
                active
                  ? "border-brand bg-brand/5"
                  : clickable
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
                  : templateOnly
                  ? (
                    <span class="text-xs font-medium text-text-muted whitespace-nowrap">
                      template
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
