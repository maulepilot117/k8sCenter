import { type ChangeEvent, useEffect, useState } from "react";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiGet } from "@/lib/api.ts";
import { RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";
import {
  computeEffectivePermissions,
  type EffectiveRule,
  type ResolvedBinding,
  resolveBindings,
  resolveRoleHref,
  roleMapKey,
} from "@/lib/rbac-utils.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

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
  const isClient = useIsClient();
  const [rows, setRows] = useState<ResolvedBinding[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [hideSystem, setHideSystem] = useState(true);
  const [selectedSubject, setSelectedSubject] = useState<string | null>(null);
  const [effectiveRules, setEffectiveRules] = useState<EffectiveRule[]>([]);

  // Store raw data for effective permissions computation
  const [rawBindings, setRawBindings] = useState<Binding[]>([]);
  const [roleMap, setRoleMap] = useState<Map<string, Role>>(new Map());

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function fetchData() {
      try {
        const [rb, crb, roles, clusterRoles] = await Promise.all([
          apiGet<Binding[]>("/v1/resources/rolebindings?limit=500"),
          apiGet<Binding[]>("/v1/resources/clusterrolebindings?limit=500"),
          apiGet<Role[]>("/v1/resources/roles?limit=500"),
          apiGet<Role[]>("/v1/resources/clusterroles?limit=500"),
        ]);

        const allBindings: Binding[] = [];
        if (Array.isArray(rb.data)) allBindings.push(...rb.data);
        if (Array.isArray(crb.data)) allBindings.push(...crb.data);
        setRawBindings(allBindings);

        // Build role lookup map using shared key scheme
        const rMap = new Map<string, Role>();
        if (Array.isArray(roles.data)) {
          for (const r of roles.data) {
            rMap.set(
              roleMapKey("Role", r.metadata.namespace, r.metadata.name),
              r,
            );
          }
        }
        if (Array.isArray(clusterRoles.data)) {
          for (const cr of clusterRoles.data) {
            rMap.set(
              roleMapKey("ClusterRole", undefined, cr.metadata.name),
              cr,
            );
          }
        }
        setRoleMap(rMap);

        setRows(resolveBindings(allBindings, rMap));
      } catch {
        setError("Failed to load RBAC data");
      }
      setLoading(false);
    }

    fetchData();
  }, []);

  function handleSubjectClick(subjectName: string) {
    if (selectedSubject === subjectName) {
      setSelectedSubject(null);
      setEffectiveRules([]);
      return;
    }
    setSelectedSubject(subjectName);
    setEffectiveRules(
      computeEffectivePermissions(subjectName, rawBindings, roleMap),
    );
  }

  if (!isClient) return null;

  const filtered = rows.filter((r) => {
    if (hideSystem && r.subjectName.startsWith("system:")) {
      return false;
    }
    if (search) {
      const q = search.toLowerCase();
      return (
        r.subjectName.toLowerCase().includes(q) ||
        r.roleName.toLowerCase().includes(q) ||
        r.bindingName.toLowerCase().includes(q)
      );
    }
    return true;
  });

  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold text-text-primary mb-1">
        RBAC Overview
      </h1>
      <p className="text-sm text-text-muted mb-6">
        Subject → Binding → Role relationships across the cluster.
      </p>

      {/* Filters */}
      <div className="mb-4 flex items-center gap-4">
        <div className="flex-1 max-w-xs">
          <SearchBar
            value={search}
            onInput={setSearch}
            placeholder="Filter by subject, role, or binding..."
          />
        </div>
        <label className="flex items-center gap-2 text-sm text-text-secondary">
          <input
            type="checkbox"
            checked={hideSystem}
            onChange={(e: ChangeEvent<HTMLInputElement>) =>
              setHideSystem(e.currentTarget.checked)
            }
            className="rounded border-border-primary text-brand focus:ring-brand"
          />
          Hide system accounts
        </label>
        <span className="text-xs text-text-muted">
          {filtered.length} of {rows.length} entries
        </span>
      </div>

      {loading && (
        <div className="flex justify-center py-12">
          <Spinner className="text-brand" />
        </div>
      )}

      {error && <p className="text-sm text-danger py-4">{error}</p>}

      {!loading && !error && (
        <div className="overflow-x-auto rounded-lg border border-border-primary">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border-primary bg-surface bg-surface">
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Subject
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Kind
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Binding
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Role
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Rules
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border-subtle">
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

                const isSelected = selectedSubject === r.subjectName;

                return (
                  <tr
                    // biome-ignore lint/suspicious/noArrayIndexKey: a subject can repeat within one binding, so the index disambiguates; rows are replaced whole
                    key={`${r.subjectName}-${r.bindingName}-${i}`}
                    className={`cursor-pointer hover:bg-hover/30 ${
                      isSelected ? "bg-accent-dim" : ""
                    }`}
                    onClick={() => handleSubjectClick(r.subjectName)}
                  >
                    <td className="px-3 py-2 font-medium text-text-primary">
                      {r.subjectName}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
                      {r.subjectKind}
                    </td>
                    <td className="px-3 py-2">
                      <a
                        href={bindingHref}
                        className="text-brand hover:underline"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {r.bindingName}
                      </a>
                    </td>
                    <td className="px-3 py-2">
                      <a
                        href={roleHref}
                        className="text-brand hover:underline"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {r.roleName}
                      </a>
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
                      {r.bindingNamespace ?? "(cluster)"}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
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
      {selectedSubject && effectiveRules.length > 0 && (
        <div className="mt-6">
          <h2 className="text-lg font-semibold text-text-primary mb-3">
            Effective Permissions: {selectedSubject}
          </h2>
          <div className="overflow-x-auto rounded-lg border border-border-primary">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary bg-surface bg-surface">
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Resources
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Verbs
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Source
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {effectiveRules.map((rule, i) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: rules have no id and the list is replaced whole, never reordered
                  <tr key={i}>
                    <td className="px-3 py-2 font-mono text-xs text-text-secondary">
                      {rule.resources.join(",")}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
                      {rule.verbs.join(",")}
                    </td>
                    <td className="px-3 py-2 text-xs text-text-muted">
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
