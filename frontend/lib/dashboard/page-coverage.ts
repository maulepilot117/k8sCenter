/**
 * What a card can say about the page its numbers were computed over.
 *
 * `total` is the list route's own count of the whole population and `counted`
 * is how much of it this page carried. The route caps a page at 500 items
 * server-side, so a ranking over the first 500 of 3000 is a sample, and a card
 * that printed it as "the worst pods on the cluster" would be wrong on exactly
 * the clusters where it matters most. `truncated` is what lets the card say
 * which of the two it is showing.
 *
 * This shipped as a private eight-line copy inside `pod-health.ts` and was
 * copied a second time into `autoscaling.ts`, whose copy carried the note that
 * a THIRD copy is the point at which it should move to a shared module.
 * `pressure.ts` was the third, so it moved here. Both original copies now
 * import from this file; neither changed behaviour in the move -- the two
 * implementations differed only in whether the finite check was spelled inline
 * or through a local helper.
 *
 * Pure: no DOM, no fetch, no signals (D-10).
 */
import { list } from "./narrow.ts";
import type { ResourceListPage } from "./wire-types.ts";

export interface PageCoverage {
  /** The route's count of the whole population. */
  total: number;
  /** How many items this page actually carried. */
  counted: number;
  /** The page is a sample of the population rather than all of it. */
  truncated: boolean;
  /**
   * The payload carried a list this build could read.
   *
   * False when the route answered with a null payload, a missing `items`, or
   * an `items` that is not an array. Every count beside this one is then zero
   * because there was nothing to count -- NOT because the cluster is clean --
   * so a card that renders its empty state without checking this prints good
   * news over a question that was never answered.
   */
  readable: boolean;
}

/**
 * Coverage for a page, given how many items the caller read off it.
 *
 * A `total` that is missing or not a finite number falls back to `counted`,
 * which reports the page as complete. That is the safe direction here and only
 * here: the alternative is a card that claims to be a sample of an unknown
 * population, which tells the reader nothing they can act on.
 */
export function coverage(
  page: ResourceListPage | null | undefined,
  counted: number,
): PageCoverage {
  const raw = page?.total;
  const total = typeof raw === "number" && Number.isFinite(raw) ? raw : counted;
  // Read off the payload rather than taken from the caller: every view that
  // spreads this already passes the page, and deriving it here is what makes
  // the signal impossible for a new card to forget. A card still has to
  // render it -- but it can no longer be absent from the view it renders.
  const readable = list(page?.items) !== null;
  return { total, counted, truncated: total > counted, readable };
}
