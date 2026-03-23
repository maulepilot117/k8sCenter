import { define } from "@/utils.ts";
import ConfigMapWizard from "@/islands/ConfigMapWizard.tsx";

export default define.page(function NewConfigMapPage() {
  return <ConfigMapWizard />;
});
