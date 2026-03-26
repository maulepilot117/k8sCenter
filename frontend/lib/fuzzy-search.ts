export interface SearchItem {
  id: string;
  type: "resource" | "action" | "navigation";
  label: string;
  detail?: string;
  href?: string;
  icon?: string;
  action?: () => void;
}

interface ScoredItem {
  item: SearchItem;
  score: number;
}

function fuzzyCharMatch(text: string, query: string): boolean {
  let qi = 0;
  for (let i = 0; i < text.length && qi < query.length; i++) {
    if (text[i] === query[qi]) qi++;
  }
  return qi === query.length;
}

function scoreItem(item: SearchItem, query: string): number {
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

export function fuzzySearch(
  items: SearchItem[],
  query: string,
): SearchItem[] {
  if (!query.trim()) {
    return items.slice(0, 8);
  }

  const scored: ScoredItem[] = [];
  for (const item of items) {
    const score = scoreItem(item, query.trim());
    if (score > 0) {
      scored.push({ item, score });
    }
  }

  scored.sort((a, b) => b.score - a.score);
  return scored.slice(0, 12).map((s) => s.item);
}
