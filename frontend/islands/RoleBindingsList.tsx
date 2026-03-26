import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";

interface Binding {
  metadata: { name: string; namespace?: string };
  roleRef: { kind: string; name: string };
  subjects?: Array<{ kind: string; name: string; namespace?: string }>;
}

interface Props {
  roleName: string;
  roleKind: string; //"Role" or"ClusterRole"
  namespace?: string;
}

/**
 * Small island that fetches RoleBindings/ClusterRoleBindings and shows
 * which ones reference the given role. Mounted on Role/ClusterRole detail pages.
 */
export default function RoleBindingsList(
  { roleName, roleKind, namespace }: Props,
) {
  const bindings = useSignal<Binding[]>([]);
  const loading = useSignal(true);

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function fetch() {
      try {
        const results: Binding[] = [];

        // Fetch bindings that might reference this role
        if (roleKind === "ClusterRole") {
          // ClusterRoles can be referenced by both RoleBindings and ClusterRoleBindings
          const [rb, crb] = await Promise.all([
            apiGet<Binding[]>("/v1/resources/rolebindings?limit=500"),
            apiGet<Binding[]>(
              "/v1/resources/clusterrolebindings?limit=500",
            ),
          ]);
          if (Array.isArray(rb.data)) results.push(...rb.data);
          if (Array.isArray(crb.data)) results.push(...crb.data);
        } else {
          // Roles can only be referenced by RoleBindings in the same namespace
          const rb = await apiGet<Binding[]>(
            `/v1/resources/rolebindings/${namespace}?limit=500`,
          );
          if (Array.isArray(rb.data)) results.push(...rb.data);
        }

        // Filter to bindings that reference this role
        bindings.value = results.filter(
          (b) => b.roleRef.name === roleName && b.roleRef.kind === roleKind,
        );
      } catch {
        // Silently fail — this is a supplementary section
      }
      loading.value = false;
    }

    fetch();
  }, [roleName, roleKind, namespace]);

  if (!IS_BROWSER || loading.value) return null;
  if (bindings.value.length === 0) {
    return (
      <div class="mt-4">
        <h3 class="text-sm font-semibold text-text-primary mb-2">
          Bindings Using This {roleKind}
        </h3>
        <p class="text-sm text-text-muted">
          No bindings reference this {roleKind.toLowerCase()}.
        </p>
      </div>
    );
  }

  return (
    <div class="mt-4">
      <h3 class="text-sm font-semibold text-text-primary mb-2">
        Bindings Using This {roleKind} ({bindings.value.length})
      </h3>
      <div class="overflow-x-auto rounded-md border border-border-primary">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-border-primary">
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Binding
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Kind
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Subjects
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {bindings.value.map((b) => {
              const isClusterScoped = !b.metadata.namespace;
              const bindingKind = isClusterScoped
                ? "clusterrolebindings"
                : "rolebindings";
              const bindingPath = RESOURCE_DETAIL_PATHS[bindingKind];
              const href = isClusterScoped
                ? `${bindingPath}/${b.metadata.name}`
                : `${bindingPath}/${b.metadata.namespace}/${b.metadata.name}`;

              return (
                <tr key={b.metadata.name + (b.metadata.namespace ?? "")}>
                  <td class="px-3 py-1.5">
                    <a
                      href={href}
                      class="text-brand hover:underline font-medium"
                    >
                      {b.metadata.name}
                    </a>
                    {b.metadata.namespace && (
                      <span class="ml-1 text-xs text-text-muted">
                        ({b.metadata.namespace})
                      </span>
                    )}
                  </td>
                  <td class="px-3 py-1.5 text-text-secondary">
                    {isClusterScoped ? "ClusterRoleBinding" : "RoleBinding"}
                  </td>
                  <td class="px-3 py-1.5 text-text-secondary">
                    {b.subjects
                      ?.map((s) => `${s.kind}:${s.name}`)
                      .join(",") ?? "-"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
