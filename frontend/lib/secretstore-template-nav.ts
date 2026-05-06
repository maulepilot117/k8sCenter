/**
 * Pure helper for the SecretStore-from-template editor's post-apply
 * navigation decision. Lives in `lib/` (not `islands/`) so the unit-test
 * file can import it from a `.ts` source without dragging in JSX/DOM types.
 *
 * The 5-clause conditional in here gates the only Phase-K-distinct side
 * effect; the test file `secretstore-template-nav_test.ts` exercises every
 * branch.
 */

import type { ApplyResponse } from "./yaml-apply.ts";

const NAVIGABLE_ACTIONS = new Set(["created", "configured", "unchanged"]);

/**
 * Compute the SecretStore detail-page href to navigate to after a successful
 * apply, or null when the response does not represent a clean single-Store
 * outcome.
 *
 * Counts as "clean single-Store outcome":
 *   - exactly one resource processed (total === 1, results.length === 1)
 *   - zero failures (summary.failed === 0)
 *   - that resource's kind is SecretStore
 *   - action ∈ {created, configured, unchanged}
 *     ("unchanged" is a successful re-apply: the live state already matched
 *     the submitted spec; the operator's intent is the same as for new
 *     creates)
 *   - both namespace and name are present (defensive — apply path always
 *     populates these, but the URL would be malformed without them)
 */
export function singleSecretStoreHref(res: ApplyResponse): string | null {
  if (
    res.summary.failed !== 0 ||
    res.summary.total !== 1 ||
    res.results.length !== 1
  ) {
    return null;
  }
  const finished = res.results[0];
  if (
    !NAVIGABLE_ACTIONS.has(finished.action) ||
    finished.kind !== "SecretStore" ||
    !finished.namespace ||
    !finished.name
  ) {
    return null;
  }
  return `/external-secrets/stores/${finished.namespace}/${finished.name}`;
}
