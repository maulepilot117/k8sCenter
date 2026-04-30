/** "ESO not installed" tile shared between the dashboard and list islands.
 *
 * Backend Phase A returns `{detected: false}` from `/externalsecrets/status`
 * when no ESO CRDs are present. Per plan R2 every list/index surface should
 * surface that as an install prompt rather than the empty-list copy. */
export function ESONotDetected() {
  return (
    <div class="rounded-lg border border-border-primary p-8 text-center bg-elevated">
      <p class="text-lg font-medium text-text-primary mb-2">
        External Secrets Operator is not installed in this cluster.
      </p>
      <p class="text-sm text-text-muted">
        Install via the{" "}
        <a
          class="text-brand hover:underline"
          href="https://external-secrets.io/latest/introduction/getting-started/"
          target="_blank"
          rel="noopener noreferrer"
        >
          ESO Helm chart
        </a>{" "}
        to start managing secrets across stores.
      </p>
    </div>
  );
}
