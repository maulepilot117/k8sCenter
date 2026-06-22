import { define } from "@/utils.ts";
import MeshRouteDetail from "@/islands/MeshRouteDetail.tsx";

export default define.page(function MeshRouteDetailPage(ctx) {
  const id = decodeURIComponent(ctx.params.id);
  return <MeshRouteDetail id={id} />;
});
