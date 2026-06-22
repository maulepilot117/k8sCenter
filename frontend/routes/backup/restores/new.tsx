import { define } from "@/utils.ts";
import VeleroRestoreWizard from "@/islands/VeleroRestoreWizard.tsx";

export default define.page(function NewRestorePage() {
  return (
    <VeleroRestoreWizard
      onClose={() => {
        globalThis.location.href = "/backup/restores";
      }}
    />
  );
});
