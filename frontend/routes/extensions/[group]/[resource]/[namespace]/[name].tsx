import { define } from "@/utils.ts";
import SchemaForm from "@/islands/SchemaForm.tsx";

export default define.page(function CRDDetailPage(ctx) {
  return (
    <SchemaForm
      group={ctx.params.group}
      resource={ctx.params.resource}
      namespace={ctx.params.namespace}
      name={ctx.params.name}
      mode="edit"
    />
  );
});
