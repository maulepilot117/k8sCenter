import { useState } from "preact/hooks";
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
  const [expandedCategories, setExpandedCategories] = useState<Set<string>>(
    () => new Set(POLICY_CATEGORIES),
  );

  const toggleCategory = (cat: string) => {
    setExpandedCategories((prev) => {
      const next = new Set(prev);
      if (next.has(cat)) {
        next.delete(cat);
      } else {
        next.add(cat);
      }
      return next;
    });
  };

  return (
    <div class="space-y-4">
      <p class="text-sm text-text-muted">
        Choose a policy template to get started.
      </p>

      {POLICY_CATEGORIES.map((cat) => {
        const templates = grouped.get(cat) ?? [];
        if (templates.length === 0) return null;
        const isExpanded = expandedCategories.has(cat);

        return (
          <div key={cat} class="rounded-lg border border-border-primary">
            <button
              type="button"
              class="flex w-full items-center justify-between px-4 py-3 text-left"
              onClick={() => toggleCategory(cat)}
            >
              <span class="text-sm font-semibold text-text-primary">
                {cat}
              </span>
              <svg
                class={`w-4 h-4 text-text-muted transition-transform ${
                  isExpanded ? "rotate-180" : ""
                }`}
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M19 9l-7 7-7-7"
                />
              </svg>
            </button>

            {isExpanded && (
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-3 px-4 pb-4">
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
            )}
          </div>
        );
      })}
    </div>
  );
}
