import { define } from "@/utils.ts";
import RestoreSnapshotWizard from "@/islands/RestoreSnapshotWizard.tsx";

/**
 * Deep-link route for snapshot restores. URL params are parsed client-side
 * inside RestoreSnapshotWizard (via globalThis.location.search) and passed
 * as snapshotParams=null here so the island reads them itself on mount.
 */
export default define.page(function RestoreSnapshotPage() {
  return (
    <RestoreSnapshotWizard
      onClose={() => {
        globalThis.location.href = "/storage/snapshots";
      }}
      snapshotParams={null}
    />
  );
});
