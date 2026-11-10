import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { checkSource, checkTree } from "./check-no-render-is-browser.ts";
import { FRONTEND_ROOT } from "./source-files.ts";

const lines = (code: string) =>
  checkSource(code, "x.tsx").map((f) => `${f.line}: ${f.message}`);

const SUFFIX =
  "use useIsClient() so the browser's first render matches the server's";

test("IS_BROWSER in a component body fails, naming the component and line", () => {
  const code = [
    'import { IS_BROWSER } from "@/src/lib/is-browser.ts";',
    "export default function Panel() {",
    "  if (!IS_BROWSER) return <p>loading</p>;",
    "  return <p>ready</p>;",
    "}",
  ].join("\n");
  expect(lines(code)).toEqual([
    `3: reads IS_BROWSER while rendering Panel; ${SUFFIX}`,
  ]);
});

test("reads in JSX, a hook body, and callbacks the render calls all fail", () => {
  const code = [
    "const Arrow = () => <p>{IS_BROWSER ? 'a' : 'b'}</p>;",
    "function useWidth() { return IS_BROWSER ? innerWidth : 0; }",
    "function List({ items }) {",
    "  const label = useMemo(() => (IS_BROWSER ? 'x' : 'y'), []);",
    "  return <ul>{items.map((i) => <li>{IS_BROWSER && i}</li>)}</ul>;",
    "}",
    "const Wrapped = memo(() => (typeof window === 'undefined' ? null : <b />));",
    "function Probe() { return hasDom() ? <i /> : null; }",
  ].join("\n");
  expect(lines(code)).toEqual([
    `1: reads IS_BROWSER while rendering Arrow; ${SUFFIX}`,
    `2: reads IS_BROWSER while rendering useWidth; ${SUFFIX}`,
    `4: reads IS_BROWSER while rendering List; ${SUFFIX}`,
    `5: reads IS_BROWSER while rendering List; ${SUFFIX}`,
    `7: checks typeof window while rendering Wrapped; ${SUFFIX}`,
    `8: calls hasDom() while rendering Probe; ${SUFFIX}`,
  ]);
});

test("a helper a render calls fails, local or at module scope", () => {
  const code = [
    "function fromUrl() { return IS_BROWSER ? location.search : ''; }",
    "function Page() {",
    "  const pick = () => IS_BROWSER;",
    "  return <p data-q={fromUrl()}>{pick() ? 1 : 0}</p>;",
    "}",
  ].join("\n");
  expect(lines(code)).toEqual([
    `1: reads IS_BROWSER while rendering Page (through fromUrl()); ${SUFFIX}`,
    `3: reads IS_BROWSER while rendering Page (through pick()); ${SUFFIX}`,
  ]);
});

test("lazy state initialisers fail: React calls them while rendering", () => {
  const code = [
    "function fromUrl() { return IS_BROWSER ? location.search : ''; }",
    "function initial() { return { open: hasDom() }; }",
    "export function Page() {",
    "  const [q] = useState(() => (IS_BROWSER ? 1 : 0));",
    "  const [s] = useReducer(reduce, null, () => fromUrl());",
    "  const [o] = useState(initial);",
    "  return <p>{q}{s}{o.open}</p>;",
    "}",
  ].join("\n");
  expect(lines(code)).toEqual([
    `1: reads IS_BROWSER while rendering Page (through fromUrl()); ${SUFFIX}`,
    `2: calls hasDom() while rendering Page (through initial()); ${SUFFIX}`,
    `4: reads IS_BROWSER while rendering Page; ${SUFFIX}`,
  ]);
});

test("effects, handlers and callbacks pass", () => {
  const code = [
    "export function Page() {",
    "  const [q] = useState('');",
    "  const [s] = useReducer(reduce, null);",
    "  useEffect(() => { if (!IS_BROWSER) return; load(); }, []);",
    "  useLayoutEffect(() => { (async () => IS_BROWSER)(); });",
    "  const save = useCallback(() => IS_BROWSER && post(), []);",
    "  function onKey() { if (IS_BROWSER) location.reload(); }",
    "  const close = () => IS_BROWSER && history.back();",
    "  useYamlApply(q, { onApplySuccess: () => IS_BROWSER && go() });",
    "  return (",
    "    <button onClick={() => IS_BROWSER && go()} ref={(el) => IS_BROWSER && el}",
    "      onKeyDown={onKey}>{q}{s}</button>",
    "  );",
    "}",
  ].join("\n");
  expect(lines(code)).toEqual([]);
});

test("module-scope browser checks pass: a store reading storage once at load", () => {
  const code = [
    'import { IS_BROWSER } from "@/src/lib/is-browser.ts";',
    "const stored = IS_BROWSER ? localStorage.getItem('ns') : null;",
    "export const selected = signal(stored ?? 'all');",
    "if (IS_BROWSER) { addEventListener('storage', sync); }",
    "export function readTheme() { return IS_BROWSER ? localStorage.theme : 'dark'; }",
  ].join("\n");
  expect(lines(code)).toEqual([]);
});

test("use-is-client.ts, the one hook built on IS_BROWSER, is skipped by the tree check and passes anyway", () => {
  const path = join(FRONTEND_ROOT, "src", "lib", "use-is-client.ts");
  expect(checkSource(readFileSync(path, "utf8"), "use-is-client.ts")).toEqual(
    [],
  );
});

test("the repository has no render-path browser check", () => {
  expect(checkTree()).toEqual([]);
});
