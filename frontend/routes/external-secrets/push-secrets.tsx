import { define } from "@/utils.ts";
import ESOPushSecretsList from "@/islands/ESOPushSecretsList.tsx";

export default define.page(function PushSecretsPage() {
  return <ESOPushSecretsList />;
});
