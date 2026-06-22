import { define } from "@/utils.ts";
import CronJobWizard from "@/islands/CronJobWizard.tsx";

export default define.page(function NewCronJobPage() {
  return <CronJobWizard />;
});
