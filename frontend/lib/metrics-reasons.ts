/**
 * Plain copy for the metrics reasons a remote cluster answers with. The
 * dashboard cards and the mesh pages read their copy here, so a reason reads
 * the same wherever it appears and its raw code is never printed.
 */

/** The wire reason a remote cluster without a Prometheus binding answers with. */
export const METRICS_NOT_CONFIGURED_REASON = "metrics_not_configured";

/** What a card says when the selected cluster has no metrics binding. */
export const METRICS_NOT_CONFIGURED_MESSAGE =
  "Metrics are not configured for this cluster. Add a Prometheus binding in Clusters.";

/**
 * What golden signals say when they are unavailable, by the route's reason: a
 * remote cluster answers 200 with `metrics_not_configured` (no binding) or
 * `unreachable` (bound, every query failed), and a mesh without Prometheus
 * answers `metrics_unavailable`. Null for any other reason, which the caller
 * words for its own surface.
 */
export function goldenSignalsReasonCopy(reason: string): string | null {
  switch (reason) {
    case METRICS_NOT_CONFIGURED_REASON:
      return METRICS_NOT_CONFIGURED_MESSAGE;
    case "unreachable":
      return "This cluster's Prometheus could not be read, so there are no signals to show.";
    case "metrics_unavailable":
      return "A service mesh is installed but no Prometheus is wired up, so there are no signals to read.";
    default:
      return null;
  }
}
