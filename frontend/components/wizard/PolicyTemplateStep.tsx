import {
  getTemplatesByCategory,
  POLICY_CATEGORIES,
} from "@/lib/policy-templates.ts";
import type { PolicyTemplateInfo } from "@/lib/policy-templates.ts";
import { SeverityBadge } from "@/components/ui/PolicyBadges.tsx";
import { EngineBadge } from "@/components/ui/PolicyBadges.tsx";

interface PolicyTemplateStepProps {
  selectedId: string;
  onSelect: (id: string) => void;
}

export function PolicyTemplateStep(
  { selectedId, onSelect }: PolicyTemplateStepProps,
) {
  const grouped = getTemplatesByCategory();

  return (
    <div class="space-y-4">
      <p class="text-sm text-text-muted">
        Choose a policy template to get started.
      </p>

      {POLICY_CATEGORIES.map((cat) => {
        const templates = grouped.get(cat) ?? [];
        if (templates.length === 0) return null;

        return (
          <div key={cat}>
            <h3 class="text-sm font-semibold text-text-primary mb-3">{cat}</h3>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              {templates.map((tmpl: PolicyTemplateInfo) => {
                const isSelected = tmpl.id === selectedId;
                return (
                  <div
                    key={tmpl.id}
                    class={`p-4 rounded-lg border cursor-pointer transition-colors ${
                      isSelected
                        ? "border-brand bg-brand/5"
                        : "border-border-primary hover:border-brand/50 bg-surface"
                    }`}
                    onClick={() => onSelect(tmpl.id)}
                  >
                    <div class="flex items-start justify-between gap-2 mb-2">
                      <span class="text-sm font-medium text-text-primary">
                        {tmpl.name}
                      </span>
                      <SeverityBadge severity={tmpl.severity} />
                    </div>
                    <p class="text-xs text-text-muted mb-3">
                      {tmpl.description}
                    </p>
                    <div class="flex items-center gap-1.5">
                      {tmpl.engines.map((eng) => (
                        <EngineBadge key={eng} engine={eng} />
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}
