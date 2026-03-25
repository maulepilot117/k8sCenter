import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import {
  type EffectiveRule,
  type ResolvedBinding,
  computeEffectivePermissions,
  resolveBindings,
  resolveRoleHref,
} from "@/lib/rbac-utils.ts";
import { RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";

interface Binding {
  metadata: { name: string; namespace?: string };
  roleRef: { kind: string; name: string; apiGroup: string };
  subjects?: Array<{ kind: string; name: string; namespace?: string }>;
}

interface Role {
  metadata: { name: string; namespace?: string };
  rules?: Array<{
    apiGroups?: string[];
    resources?: string[];
    verbs?: string[];
  }>;
}

export default function RBACOverview() {
  const rows = useSignal<ResolvedBinding[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const hideSystem = useSignal(true);
  const selectedSubject = useSignal<string | null>(null);
  const effectiveRules = useSignal<EffectiveRule[]>([]);

  // Store raw data for effective permissions computation
  const rawBindings = useSignal<Binding[]>([]);
  const roleMap = useSignal<Map<string, Role>>(new Map());

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function fetchData() {
      try {
        const [rb, crb, roles, clusterRoles] = await Promise.all([
          apiGet<Binding[]>("/v1/resources/rolebindings?limit=500"),
          apiGet<Binding[]>(
            "/v1/resources/clusterrolebindings?limit=500",
          ),
          apiGet<Role[]>("/v1/resources/roles?limit=500"),
          apiGet<Role[]>("/v1/resources/clusterroles?limit=500"),
        ]);

        const allBindings: Binding[] = [];
        if (Array.isArray(rb.data)) allBindings.push(...rb.data);
        if (Array.isArray(crb.data)) allBindings.push(...crb.data);
        rawBindings.value = allBindings;

        // Build role lookup map (keyed by "cluster:name" or "namespace:name")
        const rMap = new Map<string, Role>();
        if (Array.isArray(roles.data)) {
          for (const r of roles.data) {
            rMap.set(`${r.metadata.namespace}:${r.metadata.name}`, r);
          }
        }
        if (Array.isArray(clusterRoles.data)) {
          for (const cr of clusterRoles.data) {
            rMap.set(`cluster:${cr.metadata.name}`, cr);
          }
        }
        roleMap.value = rMap;

        rows.value = resolveBindings(allBindings, rMap);
      } catch {
        error.value = "Failed to load RBAC data";
      }
      loading.value = false;
    }

    fetchData();
  }, []);

  function handleSubjectClick(subjectName: string) {
    if (selectedSubject.value === subjectName) {
      selectedSubject.value = null;
      effectiveRules.value = [];
      return;
    }
    selectedSubject.value = subjectName;
    effectiveRules.value = computeEffectivePermissions(
      subjectName,
      rawBindings.value,
      roleMap.value,
    );
  }

  if (!IS_BROWSER) return null;

  const filtered = rows.value.filter((r) => {
    if (hideSystem.value && r.subjectName.startsWith("system:")) {
      return false;
    }
    if (search.value) {
      const q = search.value.toLowerCase();
      return (
        r.subjectName.toLowerCase().includes(q) ||
        r.roleName.toLowerCase().includes(q) ||
        r.bindingName.toLowerCase().includes(q)
      );
    }
    return true;
  });

  return (
    <div class="p-6">
      <h1 class="text-2xl font-bold text-slate-900 dark:text-white mb-1">
        RBAC Overview
      </h1>
      <p class="text-sm text-slate-500 dark:text-slate-400 mb-6">
        Subject → Binding → Role relationships across the cluster.
      </p>

      {/* Filters */}
      <div class="mb-4 flex items-center gap-4">
        <div class="flex-1 max-w-xs">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
            }}
            placeholder="Filter by subject, role, or binding..."
          />
        </div>
        <label class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300">
          <input
            type="checkbox"
            checked={hideSystem.value}
            onChange={(e) => {
              hideSystem.value = (e.target as HTMLInputElement).checked;
            }}
            class="rounded border-slate-300 text-brand focus:ring-brand"
          />
          Hide system accounts
        </label>
        <span class="text-xs text-slate-400">
          {filtered.length} of {rows.value.length} entries
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <div class="h-6 w-6 animate-spin rounded-full border-2 border-slate-300 border-t-brand" />
        </div>
      )}

      {error.value && (
        <p class="text-sm text-danger py-4">{error.value}</p>
      )}

      {!loading.value && !error.value && (
        <div class="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-700">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-slate-200 bg-slate-50 dark:border-slate-700 dark:bg-slate-800/50">
                <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                  Subject
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                  Kind
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                  Binding
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                  Role
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                  Namespace
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                  Rules
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
              {filtered.map((r, i) => {
                const bindingPath =
                  RESOURCE_DETAIL_PATHS[
                    r.bindingKind === "ClusterRoleBinding"
                      ? "clusterrolebindings"
                      : "rolebindings"
                  ];
                const bindingHref = r.bindingNamespace
                  ? `${bindingPath}/${r.bindingNamespace}/${r.bindingName}`
                  : `${bindingPath}/${r.bindingName}`;

                const roleHref = resolveRoleHref(
                  r.roleKind,
                  r.roleName,
                  r.bindingNamespace,
                );

                const isSelected =
                  selectedSubject.value === r.subjectName;

                return (
                  <tr
                    key={`${r.subjectName}-${r.bindingName}-${i}`}
                    class={`cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-800/30 ${isSelected ? "bg-blue-50 dark:bg-blue-900/20" : ""}`}
                    onClick={() => handleSubjectClick(r.subjectName)}
                  >
                    <td class="px-3 py-2 font-medium text-slate-900 dark:text-white">
                      {r.subjectName}
                    </td>
                    <td class="px-3 py-2 text-slate-600 dark:text-slate-400">
                      {r.subjectKind}
                    </td>
                    <td class="px-3 py-2">
                      <a
                        href={bindingHref}
                        class="text-brand hover:underline"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {r.bindingName}
                      </a>
                    </td>
                    <td class="px-3 py-2">
                      <a
                        href={roleHref}
                        class="text-brand hover:underline"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {r.roleName}
                      </a>
                    </td>
                    <td class="px-3 py-2 text-slate-600 dark:text-slate-400">
                      {r.bindingNamespace ?? "(cluster)"}
                    </td>
                    <td class="px-3 py-2 text-slate-600 dark:text-slate-400">
                      {r.rulesCount || "-"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Effective Permissions Panel */}
      {selectedSubject.value && effectiveRules.value.length > 0 && (
        <div class="mt-6">
          <h2 class="text-lg font-semibold text-slate-900 dark:text-white mb-3">
            Effective Permissions: {selectedSubject.value}
          </h2>
          <div class="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-700">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-slate-200 bg-slate-50 dark:border-slate-700 dark:bg-slate-800/50">
                  <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                    Resources
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                    Verbs
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-slate-500">
                    Source
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                {effectiveRules.value.map((rule, i) => (
                  <tr key={i}>
                    <td class="px-3 py-2 font-mono text-xs text-slate-700 dark:text-slate-300">
                      {rule.resources.join(", ")}
                    </td>
                    <td class="px-3 py-2 text-slate-600 dark:text-slate-400">
                      {rule.verbs.join(", ")}
                    </td>
                    <td class="px-3 py-2 text-xs text-slate-500 dark:text-slate-400">
                      {rule.source}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
