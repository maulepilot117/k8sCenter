import { Alert } from "@/components/ui/Alert.tsx";
import { receiptHref, sameCluster, shortId } from "@/lib/change-copy.ts";
import type { RepairState } from "@/lib/change-tracking.ts";

/**
 * Shown when YAML Apply was opened from a receipt's "Retry failed objects".
 * It says plainly that nothing is filled in: the receipt stores no content,
 * so the operator supplies current manifests and they are applied as a new
 * change that only links back to the original.
 */
export function RepairBanner({
  repair,
  tracked,
  onStop,
  selectedClusterId,
  clusterLabel,
}: {
  repair: RepairState;
  tracked: boolean;
  onStop: () => void;
  /** The cluster a validate on this page would target now. */
  selectedClusterId: string;
  /** How the page names a cluster id. */
  clusterLabel: (id: string) => string;
}) {
  if (repair.status === "invalid") {
    return (
      <div role="status">
        <Alert variant="warning">
          The repair link does not name a valid change, so this apply will not
          be linked to one.
        </Alert>
      </div>
    );
  }
  if (repair.status !== "valid") return null;
  const elsewhere =
    repair.clusterId !== undefined &&
    !sameCluster(repair.clusterId, selectedClusterId);
  return (
    <div role="status" class="flex flex-col gap-2">
      {elsewhere && repair.clusterId && (
        <Alert variant="warning">
          The original change was applied to{" "}
          <strong>{clusterLabel(repair.clusterId)}</strong>, but you are viewing{" "}
          <strong>{clusterLabel(selectedClusterId)}</strong>. A repair validated
          now applies to {clusterLabel(selectedClusterId)}. Switch clusters
          before validating to retry on {clusterLabel(repair.clusterId)}.
        </Alert>
      )}
      <Alert variant="info" class="flex flex-col gap-2">
        <span>
          <strong>Repairing change {shortId(repair.id)}.</strong> Paste or
          upload the current manifests for the objects you want to retry, then
          validate and apply them as a new change. Nothing is filled in from the
          original change: k8sCenter does not store applied content.
        </span>
        {!tracked && (
          <span>
            The link to the original change is recorded only while “Keep a
            record of this change” is on.
          </span>
        )}
        <span class="flex flex-wrap items-center gap-4">
          <a
            href={receiptHref(repair.id)}
            class="font-medium text-inherit underline"
          >
            Open the original receipt
          </a>
          <button
            type="button"
            onClick={onStop}
            class="cursor-pointer border-0 bg-transparent p-0 font-medium text-inherit underline"
          >
            Stop repairing
          </button>
        </span>
      </Alert>
    </div>
  );
}
