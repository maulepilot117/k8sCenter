import { define } from "@/utils.ts";
import RestoreSnapshotWizard from "@/islands/RestoreSnapshotWizard.tsx";

export default define.page(function RestoreSnapshotPage() {
  return <RestoreSnapshotWizard />;
});
