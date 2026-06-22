import { define } from "@/utils.ts";
import VeleroScheduleWizard from "@/islands/VeleroScheduleWizard.tsx";

export default define.page(function NewSchedulePage() {
  return (
    <VeleroScheduleWizard
      onClose={() => {
        globalThis.location.href = "/backup/schedules";
      }}
    />
  );
});
