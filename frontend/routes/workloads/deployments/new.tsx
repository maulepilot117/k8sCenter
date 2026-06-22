import { define } from "@/utils.ts";
import DeploymentWizard from "@/islands/DeploymentWizard.tsx";

export default define.page(function NewDeploymentPage() {
  return <DeploymentWizard />;
});
