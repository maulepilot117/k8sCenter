import { define } from "@/utils.ts";
import CiliumPolicyEditor from "@/islands/CiliumPolicyEditor.tsx";

export default define.page(function NewCiliumPolicyPage() {
  return <CiliumPolicyEditor />;
});
