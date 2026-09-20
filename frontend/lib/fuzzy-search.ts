/**
 * Anything this module can rank: something with a stable id, a visible label,
 * and optionally a second line that is also worth matching on.
 *
 * The scorer never reads anything else, so callers with their own richer entry
 * type -- the command palette's navigation items, the dashboard catalog's
 * widget definitions -- are ranked as themselves and come back as themselves
 * rather than through a lossy conversion to and from a shared record.
 */
export interface Searchable {
  id: string;
  label: string;
  detail?: string;
}

export interface SearchItem extends Searchable {
  type: "resource" | "action" | "navigation";
  href?: string;
  icon?: string;
  action?: () => void;
}

interface ScoredItem<T> {
  item: T;
  score: number;
}

/** Default caps for the command palette, whose list is hundreds of entries
 * long and whose dropdown shows a screenful. A caller with a closed catalog --
 * one where every entry must stay reachable -- passes its own `limit`. */
const DEFAULT_BROWSE_LIMIT = 8;
const DEFAULT_MATCH_LIMIT = 12;

function fuzzyCharMatch(text: string, query: string): boolean {
  let qi = 0;
  for (let i = 0; i < text.length && qi < query.length; i++) {
    if (text[i] === query[qi]) qi++;
  }
  return qi === query.length;
}

function scoreItem(item: Searchable, query: string): number {
  const label = item.label.toLowerCase();
  const q = query.toLowerCase();

  if (label === q) return 100;
  if (label.startsWith(q)) return 80;
  if (label.includes(q)) return 60;

  // Check detail field too
  const detail = (item.detail ?? "").toLowerCase();
  if (detail.includes(q)) return 50;

  if (fuzzyCharMatch(label, q)) return 40;
  if (fuzzyCharMatch(detail, q)) return 30;

  return 0;
}

/**
 * The entries matching `query`, best first.
 *
 * `limit` caps both the unfiltered browse list and the ranked matches. It
 * defaults to the command palette's screenful; pass the catalog's own size
 * when every entry has to remain reachable, because a silently truncated
 * catalog is one a user cannot tell from a missing widget.
 */
export function fuzzySearch<T extends Searchable>(
  items: readonly T[],
  query: string,
  limit?: number,
): T[] {
  if (!query.trim()) {
    return items.slice(0, limit ?? DEFAULT_BROWSE_LIMIT);
  }

  const scored: ScoredItem<T>[] = [];
  for (const item of items) {
    const score = scoreItem(item, query.trim());
    if (score > 0) {
      scored.push({ item, score });
    }
  }

  scored.sort((a, b) => b.score - a.score);
  return scored.slice(0, limit ?? DEFAULT_MATCH_LIMIT).map((s) => s.item);
}
