import { define } from "@/utils.ts";
import ViolationBrowser from "@/islands/ViolationBrowser.tsx";

export default define.page(function ViolationsPage() {
  return <ViolationBrowser />;
});
