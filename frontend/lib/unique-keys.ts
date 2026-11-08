/**
 * React keys for list rows that carry no id of their own: each row's
 * description, plus a counter so two identical rows still get different keys.
 * A position-only key would tie each row's identity to its index instead.
 */
export function withUniqueKeys<T>(
  items: readonly T[],
  describe: (item: T) => string,
): { item: T; key: string }[] {
  const seen = new Map<string, number>();
  return items.map((item) => {
    const base = describe(item);
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    return { item, key: `${base}#${n}` };
  });
}
