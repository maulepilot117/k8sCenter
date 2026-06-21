import { define } from "@/utils.ts";
import VeleroDashboard from "@/islands/VeleroDashboard.tsx";

export default define.page(function SchedulesPage(_ctx) {
  return <VeleroDashboard initialTab="schedules" />;
});
