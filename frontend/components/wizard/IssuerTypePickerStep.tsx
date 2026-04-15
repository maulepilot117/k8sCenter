import type { IssuerType } from "@/islands/IssuerWizard.tsx";

interface IssuerTypePickerStepProps {
  selected: IssuerType | "";
  onSelect: (type: IssuerType) => void;
}

const TYPES: Array<{
  id: IssuerType;
  title: string;
  description: string;
}> = [
  {
    id: "selfSigned",
    title: "Self-Signed",
    description:
      "No external authority. Useful for test issuers or bootstrapping an internal CA.",
  },
  {
    id: "acme",
    title: "ACME (Let's Encrypt)",
    description:
      "Automated public certificates. HTTP01 ingress solver supported in this release.",
  },
  {
    id: "ca",
    title: "CA",
    description:
      "Sign from a private CA stored in a Kubernetes Secret (tls.crt + tls.key).",
  },
  {
    id: "vault",
    title: "Vault",
    description:
      "Sign from HashiCorp Vault's PKI engine using token, AppRole, or Kubernetes auth.",
  },
];

export function IssuerTypePickerStep({
  selected,
  onSelect,
}: IssuerTypePickerStepProps) {
  return (
    <div class="grid gap-3 sm:grid-cols-2">
      {TYPES.map((t) => {
        const active = selected === t.id;
        return (
          <button
            key={t.id}
            type="button"
            onClick={() => onSelect(t.id)}
            class={`text-left rounded-lg border p-4 transition-colors ${
              active
                ? "border-brand bg-brand/5"
                : "border-border-primary bg-surface hover:border-border-emphasis"
            }`}
          >
            <div class="flex items-center justify-between">
              <span class="font-medium text-text-primary">{t.title}</span>
              {active && (
                <span class="text-xs font-medium text-brand">Selected</span>
              )}
            </div>
            <p class="mt-2 text-sm text-text-muted">{t.description}</p>
          </button>
        );
      })}
    </div>
  );
}
