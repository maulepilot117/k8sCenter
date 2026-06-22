import { define } from "@/utils.ts";
import VeleroBackupWizard from "@/islands/VeleroBackupWizard.tsx";

export default define.page(function NewBackupPage() {
  return (
    <VeleroBackupWizard
      onClose={() => {
        globalThis.location.href = "/backup/backups";
      }}
    />
  );
});
