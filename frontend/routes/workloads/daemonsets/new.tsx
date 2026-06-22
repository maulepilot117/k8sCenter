import { define } from "@/utils.ts";
import DaemonSetWizard from "@/islands/DaemonSetWizard.tsx";

export default define.page(function NewDaemonSetPage() {
  return <DaemonSetWizard />;
});
