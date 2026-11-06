/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import type { EditorView as EditorViewType } from "@codemirror/view";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { useSignal } from "@preact/signals";
import { render } from "preact";
import { act } from "preact/test-utils";

/**
 * The editor is controlled: every edit goes out through onChange and comes
 * back in as `value`. When a second keystroke lands before the parent's
 * re-render for the first has reached the editor, that late `value` is the
 * editor's own earlier text. Treating it as an outside change replaced the
 * document with it, which echoed through onChange, which brought back the
 * newer text, and so on forever: the YAML tab froze on typing.
 *
 * As in CiliumConfigEditor_test.tsx, IS_BROWSER is stubbed to true and put
 * back afterwards. ResourceDetail_test.tsx replaces this module with a
 * textarea for the whole test process; the query suffix loads the real one.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const REAL_EDITOR = "./CodeMirrorEditor.tsx?real";
const { CodeMirrorEditor } = (await import(
  REAL_EDITOR
)) as typeof import("./CodeMirrorEditor.tsx");
const { EditorView } = await import("@codemirror/view");

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

let host: HTMLElement | null = null;
afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
});

const DOC = "kind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  a: b\n";

function mount() {
  const state = { calls: 0, parentValue: DOC };
  function Parent() {
    const content = useSignal(DOC);
    return (
      <CodeMirrorEditor
        value={content.value}
        onChange={(v) => {
          // A runaway echo would never return; fail it instead of hanging.
          if (++state.calls > 50) throw new Error("onChange echo loop");
          content.value = v;
          state.parentValue = v;
        }}
      />
    );
  }
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<Parent />, host as HTMLElement));
  const dom = host.querySelector(".cm-editor") as HTMLElement | null;
  if (!dom) throw new Error("editor did not mount");
  const view = EditorView.findFromDOM(dom) as EditorViewType;
  return { view, state };
}

const settle = () => new Promise((r) => setTimeout(r, 120));
const microtasks = async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve();
};

function type(view: EditorViewType, text: string) {
  const at = view.state.doc.length;
  view.dispatch({
    changes: { from: at, insert: text },
    selection: { anchor: at + text.length },
    userEvent: "input.type",
  });
}

test("a keystroke that lands before the parent catches up is kept", async () => {
  const { view, state } = mount();

  type(view, "  c: d");
  // The parent re-renders with the first edit; its sync effect is still
  // pending when the next key arrives.
  await microtasks();
  type(view, "\n");
  await settle();

  const want = `${DOC}  c: d\n`;
  expect(state.calls).toBeLessThan(10);
  expect(view.state.doc.toString()).toBe(want);
  expect(state.parentValue).toBe(want);
});

test("a value set from outside still replaces the document", async () => {
  let setOutside: (v: string) => void = () => {};
  function Parent() {
    const content = useSignal(DOC);
    setOutside = (v) => {
      content.value = v;
    };
    return (
      <CodeMirrorEditor
        value={content.value}
        onChange={(v) => {
          content.value = v;
        }}
      />
    );
  }
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<Parent />, host as HTMLElement));
  const view = EditorView.findFromDOM(
    host.querySelector(".cm-editor") as HTMLElement,
  ) as EditorViewType;

  type(view, "  c: d\n");
  await settle();
  await act(async () => setOutside("kind: Secret\n"));
  await settle();

  expect(view.state.doc.toString()).toBe("kind: Secret\n");
});
