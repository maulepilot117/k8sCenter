import { define } from "@/utils.ts";
import SnapshotList from "@/islands/SnapshotList.tsx";

export default define.page(function SnapshotsPage() {
  return (
    <div class="p-6">
      <h1 class="text-2xl font-bold text-text-primary mb-4">
        Volume Snapshots
      </h1>
      <p class="text-sm text-text-secondary mb-6">
        VolumeSnapshot resources require the snapshot.storage.k8s.io CRDs to be
        installed in your cluster.
      </p>
      <SnapshotList />
    </div>
  );
});
