import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { ownershipCopy } from "@/lib/change-copy.ts";
import type { ObjectRef, OwnershipView } from "@/lib/change-types.ts";

function objectLabel(o: Partial<ObjectRef>): string {
  const where = o.namespace ? `${o.namespace}/` : "";
  return `${o.kind ?? "Object"} ${where}${o.name ?? ""}`.trim();
}

/**
 * One row per object: what is known about which GitOps controller manages
 * it. The wording comes only from `confidence` and `reason`
 * (lib/change-copy.ts). There is deliberately no action on a row — Release E
 * never offers a Git write, whatever the result carries.
 */
export function OwnershipList({
  results,
  label = "GitOps ownership",
}: {
  results: OwnershipView[];
  label?: string;
}) {
  return (
    <ul
      aria-label={label}
      class="m-0 list-none divide-y divide-border-subtle overflow-hidden rounded-lg border border-border-subtle bg-surface p-0"
    >
      {results.map((r, i) => {
        const copy = ownershipCopy(r);
        return (
          <li
            key={`${i}-${r.object.kind}-${r.object.namespace ?? ""}-${r.object.name}`}
            class="flex flex-col gap-1 px-3.5 py-2.5 text-sm"
          >
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-mono text-text-primary">
                {objectLabel(r.object)}
              </span>
              <StatusBadge label={copy.label} tone={copy.tone} />
            </div>
            <p class="m-0 text-text-secondary">{copy.text}</p>
          </li>
        );
      })}
    </ul>
  );
}
