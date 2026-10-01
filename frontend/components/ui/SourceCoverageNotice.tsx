import { Alert } from "@/components/ui/Alert.tsx";
import type { SourceCoverage } from "@/lib/k8s-types.ts";

/**
 * Names each source a multi-source remote list could not load, so a missing
 * list is not read as an empty one (R-8 R8, KTD8). The rows from the other
 * sources still render below it.
 *
 * Renders nothing when `coverage` is absent or empty, which is always the
 * case on the local cluster.
 */
export function SourceCoverageNotice({
  coverage,
  labels,
}: {
  coverage: SourceCoverage[] | undefined;
  /** Display names by resource name; a source missing here shows as-is. */
  labels: Readonly<Record<string, string>>;
}) {
  if (!coverage || coverage.length === 0) return null;
  return (
    <div class="mb-4 flex flex-col gap-2">
      {coverage.map((c) => {
        const name = labels[c.source] ?? c.source;
        return (
          <div key={c.source} role="status">
            <Alert variant="warning">
              {c.status === "forbidden"
                ? `You are not allowed to list ${name} on this cluster, so this page omits them.`
                : `Could not load ${name} from this cluster, so this page omits them.`}
            </Alert>
          </div>
        );
      })}
    </div>
  );
}
