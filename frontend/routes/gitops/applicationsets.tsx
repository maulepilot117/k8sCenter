import { define } from "@/utils.ts";
import GitOpsAppSets from "@/islands/GitOpsAppSets.tsx";

export default define.page(function ApplicationSetsPage() {
  return <GitOpsAppSets />;
});
