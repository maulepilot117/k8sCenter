import type { IssuerType } from "@/src/islands/IssuerWizard.tsx";

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
];

export function IssuerTypePickerStep({
  selected,
  onSelect,
}: IssuerTypePickerStepProps) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {TYPES.map((t) => {
        const active = selected === t.id;
        return (
          <button
            key={t.id}
            type="button"
            onClick={() => onSelect(t.id)}
            className={`text-left rounded-lg border p-4 transition-colors ${
              active
                ? "border-brand bg-brand/5"
                : "border-border-primary bg-surface hover:border-brand/50"
            }`}
          >
            <div className="flex items-center justify-between">
              <span className="font-medium text-text-primary">{t.title}</span>
              {active && (
                <span className="text-xs font-medium text-brand">Selected</span>
              )}
            </div>
            <p className="mt-2 text-sm text-text-muted">{t.description}</p>
          </button>
        );
      })}
    </div>
  );
}
