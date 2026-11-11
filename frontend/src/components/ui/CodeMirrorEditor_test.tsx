import { expect, test } from "bun:test";
import type { EditorView as EditorViewType } from "@codemirror/view";
import { useState } from "react";
import { act, render, setBrowser, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The editor is controlled: every edit goes out through onChange and comes
 * back in as `value`. When a second keystroke lands before the parent's
 * re-render for the first has reached the editor, that late `value` is the
 * editor's own earlier text. Treating it as an outside change replaced the
 * document with it, which echoed through onChange, which brought back the
 * newer text, and so on forever: the YAML tab froze on typing.
 *
 * ResourceDetail_test.tsx replaces the editor module with a textarea for the
 * whole test process; the query suffix loads the real one.
 */
setupDom({ browser: true });

const REAL_EDITOR = "./CodeMirrorEditor.tsx?real";
const { CodeMirrorEditor } = (await import(
  REAL_EDITOR
)) as typeof import("./CodeMirrorEditor.tsx");
const { EditorView } = await import("@codemirror/view");
const { hydrateRoot } = await import("react-dom/client");
const { renderToString } = await import("react-dom/server");

const DOC = "kind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  a: b\n";

function EchoParent({ onEdit }: { onEdit: (v: string) => void }) {
  const [content, setContent] = useState(DOC);
  return (
    <CodeMirrorEditor
      value={content}
      onChange={(v) => {
        onEdit(v);
        setContent(v);
      }}
    />
  );
}

function OutsideParent({
  onSetter,
}: {
  onSetter: (set: (v: string) => void) => void;
}) {
  const [content, setContent] = useState(DOC);
  onSetter(setContent);
  return <CodeMirrorEditor value={content} onChange={setContent} />;
}

function mount() {
  const state = { calls: 0, parentValue: DOC };
  const { container } = render(
    <EchoParent
      onEdit={(v) => {
        // A runaway echo would never return; fail it instead of hanging.
        if (++state.calls > 50) throw new Error("onChange echo loop");
        state.parentValue = v;
      }}
    />,
  );
  const dom = container.querySelector(".cm-editor") as HTMLElement | null;
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
  const { container } = render(
    <OutsideParent
      onSetter={(set) => {
        setOutside = set;
      }}
    />,
  );
  const view = EditorView.findFromDOM(
    container.querySelector(".cm-editor") as HTMLElement,
  ) as EditorViewType;

  type(view, "  c: d\n");
  await settle();
  await act(async () => setOutside("kind: Secret\n"));
  await settle();

  expect(view.state.doc.toString()).toBe("kind: Secret\n");
});

test("the editor mounts after server render and hydration", async () => {
  setBrowser(false);
  const html = renderToString(<CodeMirrorEditor value={DOC} />);
  expect(html).not.toContain("cm-editor");

  const container = document.createElement("div");
  container.innerHTML = html;
  document.body.appendChild(container);
  setBrowser(true);
  let root: ReturnType<typeof hydrateRoot> | undefined;
  await act(() => {
    root = hydrateRoot(container, <CodeMirrorEditor value={DOC} />);
  });
  await act(() => {
    root?.render(<CodeMirrorEditor value={DOC} />);
  });

  expect(container.querySelectorAll(".cm-editor")).toHaveLength(1);
  const view = EditorView.findFromDOM(
    container.querySelector(".cm-editor") as HTMLElement,
  ) as EditorViewType;
  expect(view.state.doc.toString()).toBe(DOC);
  await act(() => root?.unmount());
  container.remove();
});
