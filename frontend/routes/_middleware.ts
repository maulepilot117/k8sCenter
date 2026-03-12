import { define } from "@/utils.ts";

/**
 * Global middleware — sets default state values.
 * Client-side auth check happens in islands (not here, since
 * the JWT lives in browser memory, not in cookies accessible to SSR).
 */
export default define.middleware(async (ctx) => {
  ctx.state.user = null;
  ctx.state.title = "KubeCenter";
  return await ctx.next();
});
