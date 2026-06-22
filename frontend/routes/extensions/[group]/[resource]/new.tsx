import { define } from "@/utils.ts";
import SchemaForm from "@/islands/SchemaForm.tsx";

export default define.page(function NewCRDResourcePage(ctx) {
  const { group, resource } = ctx.params;
  return (
    <SchemaForm
      group={group}
      resource={resource}
      mode="create"
    />
  );
});
