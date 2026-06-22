import { define } from "@/utils.ts";
import RoleBindingWizard from "@/islands/RoleBindingWizard.tsx";

export default define.page(function NewRoleBindingPage() {
  return (
    <RoleBindingWizard
      clusterScoped={false}
      onClose={() => {
        globalThis.location.href = "/rbac/rolebindings";
      }}
    />
  );
});
