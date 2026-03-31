import { define } from "@/utils.ts";
import SchemaForm from "@/islands/SchemaForm.tsx";

export default define.page(function NewCRDResourcePage(ctx) {
  return (
    <SchemaForm
      group={ctx.params.group}
      resource={ctx.params.resource}
      mode="create"
    />
  );
});
