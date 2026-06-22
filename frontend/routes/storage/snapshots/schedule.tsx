import { define } from "@/utils.ts";
import ScheduledSnapshotWizard from "@/islands/ScheduledSnapshotWizard.tsx";

export default define.page(function ScheduleSnapshotPage() {
  return <ScheduledSnapshotWizard />;
});
