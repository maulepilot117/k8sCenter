import { define } from "@/utils.ts";
import SnapshotWizard from "@/islands/SnapshotWizard.tsx";

export default define.page(function NewSnapshotPage() {
  return (
    <SnapshotWizard
      onClose={() => {
        globalThis.location.href = "/storage/snapshots";
      }}
    />
  );
});
