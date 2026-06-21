import { define } from "@/utils.ts";
import JobWizard from "@/islands/JobWizard.tsx";

export default define.page(function NewJobPage() {
  return (
    <JobWizard onClose={() => (globalThis.location.href = "/workloads/jobs")} />
  );
});
