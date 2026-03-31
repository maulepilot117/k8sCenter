import { define } from "@/utils.ts";
import SchemaForm from "@/islands/SchemaForm.tsx";

export default define.page(function CRDClusterScopedDetailPage(ctx) {
  return (
    <SchemaForm
      group={ctx.params.group}
      resource={ctx.params.resource}
      name={ctx.params.name}
      mode="edit"
    />
  );
});
