import { define } from "@/utils.ts";
import StatefulSetWizard from "@/islands/StatefulSetWizard.tsx";

export default define.page(function NewStatefulSetPage() {
  return <StatefulSetWizard />;
});
