import { describe, expect, test } from "bun:test";
import type { SearchItem } from "./fuzzy-search.ts";
import { fuzzySearch } from "./fuzzy-search.ts";

// The scorer serves two callers with opposite needs, which is the whole reason
// it takes a limit: the command palette ranks hundreds of navigation entries
// and shows a screenful, while the dashboard's widget catalog is a closed list
// whose contract is that every entry stays reachable. A cap that silently
// truncated the catalog would read as a missing widget rather than as a cap,
// so the defaults and the override are both pinned here.

function nav(id: string, label: string, detail = ""): SearchItem {
  return { id, type: "navigation", label, detail };
}

/** Enough entries to exceed both default caps (8 browsing, 12 matching). */
const MANY: SearchItem[] = Array.from({ length: 30 }, (_, i) =>
  nav(`n${i}`, `Pod ${i}`, "Workloads"),
);

describe("fuzzySearch: ranking", () => {
  const items = [
    nav("exact", "Nodes"),
    nav("prefix", "Node pools"),
    nav("contains", "Cluster nodes"),
    nav("detail", "Storage", "node volumes"),
    nav("fuzzy", "Network overview"),
    nav("miss", "Certificates"),
  ];

  test("an exact label comes first", () => {
    expect(fuzzySearch(items, "nodes")[0].id).toBe("exact");
  });

  test("a prefix beats a substring", () => {
    // "node", not "nodes": "Node pools" is a prefix match for the former and
    // only a fuzzy character match for the latter, which is the distinction
    // this ordering is about.
    const out = fuzzySearch(items, "node").map((i) => i.id);
    expect(out.indexOf("prefix")).toBeLessThan(out.indexOf("contains"));
  });

  test("a label match outranks a detail match", () => {
    const out = fuzzySearch(items, "node").map((i) => i.id);
    expect(out.indexOf("contains")).toBeLessThan(out.indexOf("detail"));
  });

  test("the detail line is searchable, not just the label", () => {
    // The widget catalog puts the family name here, so "cluster" finds every
    // widget in the cluster family even though no title contains the word.
    expect(fuzzySearch(items, "volumes").map((i) => i.id)).toEqual(["detail"]);
  });

  test("an entry that matches nothing is dropped", () => {
    expect(fuzzySearch(items, "nodes").map((i) => i.id)).not.toContain("miss");
  });

  test("case and surrounding whitespace do not change the answer", () => {
    expect(fuzzySearch(items, "  NODES  ").map((i) => i.id)).toEqual(
      fuzzySearch(items, "nodes").map((i) => i.id),
    );
  });
});

describe("fuzzySearch: limits", () => {
  test("an empty query browses, capped at a screenful by default", () => {
    expect(fuzzySearch(MANY, "")).toHaveLength(8);
    expect(fuzzySearch(MANY, "   ")).toHaveLength(8);
  });

  test("a matching query is capped at a screenful by default", () => {
    expect(fuzzySearch(MANY, "pod")).toHaveLength(12);
  });

  test("an explicit limit replaces both defaults", () => {
    // What the widget catalog passes, so that a closed list of entries is
    // never silently shortened: the caller's own size, not the palette's.
    expect(fuzzySearch(MANY, "", MANY.length)).toHaveLength(30);
    expect(fuzzySearch(MANY, "pod", MANY.length)).toHaveLength(30);
  });

  test("a limit below the match count still truncates", () => {
    expect(fuzzySearch(MANY, "pod", 3)).toHaveLength(3);
  });
});

describe("fuzzySearch: shape", () => {
  test("entries come back as themselves, not as a copy", () => {
    // The catalog carries its widget definition on the entry and reads it off
    // the result, so a scorer that rebuilt the record would lose it.
    interface Entry {
      id: string;
      label: string;
      detail?: string;
      payload: { widget: string };
    }
    const entries: Entry[] = [
      {
        id: "a",
        label: "Cluster Health",
        payload: { widget: "cluster-health" },
      },
      { id: "b", label: "Recent Events", payload: { widget: "recent-events" } },
    ];
    const [hit] = fuzzySearch(entries, "health");
    expect(hit).toBe(entries[0]);
    expect(hit.payload.widget).toBe("cluster-health");
  });

  test("the input array is not reordered in place", () => {
    const items = [nav("a", "Zeta"), nav("b", "Alpha")];
    fuzzySearch(items, "alpha");
    expect(items.map((i) => i.id)).toEqual(["a", "b"]);
  });
});
