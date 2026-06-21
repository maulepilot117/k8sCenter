import { define } from "@/utils.ts";
import GatewayClassDetail from "@/islands/GatewayClassDetail.tsx";

export default define.page(function GatewayClassDetailPage(ctx) {
  const { name } = ctx.params;
  return <GatewayClassDetail name={name} />;
});
