import { define } from "@/utils.ts";
import CRDResourceList from "@/islands/CRDResourceList.tsx";

export default define.page(function CRDResourcePage(ctx) {
  return (
    <CRDResourceList
      group={ctx.params.group}
      resource={ctx.params.resource}
    />
  );
});
