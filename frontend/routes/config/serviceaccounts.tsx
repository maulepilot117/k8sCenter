import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ServiceAccountsPage() {
  return <ResourceTable kind="serviceaccounts" title="ServiceAccounts" />;
});
