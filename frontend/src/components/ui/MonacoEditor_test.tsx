/** @jsxImportSource react */
import { expect, mock, test } from "bun:test";
import { act, setBrowser, setupDom } from "@/src/test-utils/render.tsx";

setupDom();

const CDN = "https://esm.sh/monaco-editor@0.52.2/esm/vs/editor/editor.api.js";
const created: HTMLElement[] = [];
mock.module(CDN, () => ({
  editor: {
    create: (el: HTMLElement) => {
      created.push(el);
      return {
        onDidChangeModelContent: () => {},
        getValue: () => "",
        getModel: () => null,
        updateOptions: () => {},
        dispose: () => {},
      };
    },
    setModelMarkers: () => {},
  },
}));

const { MonacoEditor } = await import("./MonacoEditor.tsx");
const { hydrateRoot } = await import("react-dom/client");
const { renderToString } = await import("react-dom/server");

test("Monaco loads after server render and hydration, not during the server pass", async () => {
  setBrowser(false);
  const html = renderToString(<MonacoEditor value="a: b" />);
  expect(created).toHaveLength(0);
  expect(html).not.toContain("Loading editor");

  const container = document.createElement("div");
  container.innerHTML = html;
  document.body.appendChild(container);
  setBrowser(true);
  let root: ReturnType<typeof hydrateRoot> | undefined;
  await act(() => {
    root = hydrateRoot(container, <MonacoEditor value="a: b" />);
  });
  await act(() => {
    root?.render(<MonacoEditor value="a: b" />);
  });
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });

  expect(created).toHaveLength(1);
  expect(container.contains(created[0])).toBe(true);
  await act(() => root?.unmount());
  container.remove();
});
