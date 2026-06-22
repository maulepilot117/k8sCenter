import { define } from "@/utils.ts";
import RoleBindingWizard from "@/islands/RoleBindingWizard.tsx";

export default define.page(function NewClusterRoleBindingPage() {
  return (
    <RoleBindingWizard
      clusterScoped
      onClose={() => {
        globalThis.location.href = "/rbac/clusterrolebindings";
      }}
    />
  );
});
