import { define } from "@/utils.ts";
import GitOpsAppDetail from "@/islands/GitOpsAppDetail.tsx";

export default define.page(function GitOpsAppDetailPage(ctx) {
  const id = decodeURIComponent(ctx.params.id);
  return <GitOpsAppDetail id={id} />;
});
