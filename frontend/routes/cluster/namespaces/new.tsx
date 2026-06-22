import { define } from "@/utils.ts";
import NamespaceCreator from "@/islands/NamespaceCreator.tsx";

export default define.page(function NewNamespacePage() {
  return <NamespaceCreator />;
});
