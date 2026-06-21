import { define } from "@/utils.ts";
import GitOpsAppSetDetail from "@/islands/GitOpsAppSetDetail.tsx";

export default define.page(function AppSetDetailPage(ctx) {
  const id = decodeURIComponent(ctx.params.id);
  return <GitOpsAppSetDetail id={id} />;
});
