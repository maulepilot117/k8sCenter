/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { useState } from "react";
import { body, fireEvent, render, setupDom } from "./render.tsx";

setupDom({ browser: true });

const { IS_BROWSER } = await import("@/src/lib/is-browser.ts");
const { Tabs } = await import("@/components/ui/Tabs.tsx");

test("a rendered component is found by role and text, and cleanup empties the body", () => {
  const r = render(
    <button type="button" aria-label="Save changes">
      Save
    </button>,
  );
  expect(body().getByRole("button", { name: "Save changes" })).toBeTruthy();
  expect(body().getByText("Save")).toBeTruthy();
  r.unmount();
  expect(document.body.textContent).toBe("");
});

test("setupDom applies the requested IS_BROWSER", () => {
  expect(IS_BROWSER).toBe(true);
});

/** A component driving a shared component through its props. */
function TabsHost() {
  const [active, setActive] = useState("one");
  return (
    <Tabs
      activeTab={active}
      onTabChange={setActive}
      tabs={[
        { id: "one", label: "One", content: () => <p>first panel</p> },
        { id: "two", label: "Two", content: () => <p>second panel</p> },
      ]}
    />
  );
}

test("a component renders and drives a shared child component", () => {
  render(<TabsHost />);
  expect(body().getByRole("tabpanel").textContent).toBe("first panel");
  expect(body().getByRole("tab", { name: "One" }).className).toContain(
    "border-b-2",
  );

  fireEvent.click(body().getByRole("tab", { name: "Two" }));

  expect(body().getByRole("tabpanel").textContent).toBe("second panel");
});

/**
 * Isolation can only be seen across test files that share one bun process,
 * so this runs three generated files in a child `bun test`: one that asks
 * for the browser, one that uses the harness with the default, and a server
 * test that never touches it. Each prints its name so the order is checked
 * too; the leak checks only mean something if the server file runs last.
 */
test("separate test files do not leak IS_BROWSER or happy-dom into each other", () => {
  const frontend = join(import.meta.dir, "..", "..");
  const dir = join(import.meta.dir, `.isolation-${process.pid}`);
  const harness = "@/src/test-utils/render.tsx";
  const files: Record<string, string> = {
    "a_test.tsx": `import { expect, test } from "bun:test";
import { setupDom } from "${harness}";
setupDom({ browser: true });
const { IS_BROWSER } = await import("@/src/lib/is-browser.ts");
test("a", () => { console.log("ran:a"); expect(IS_BROWSER).toBe(true); });
`,
    "b_test.tsx": `import { expect, test } from "bun:test";
import { setupDom } from "${harness}";
setupDom();
const { IS_BROWSER } = await import("@/src/lib/is-browser.ts");
test("b", () => {
  console.log("ran:b");
  expect(IS_BROWSER).toBe(false);
  expect(typeof document).toBe("object");
});
`,
    "c_test.ts": `import { expect, test } from "bun:test";
const { IS_BROWSER } = await import("@/src/lib/is-browser.ts");
const native = (x: unknown) => String(x).includes("[native code]");
test("c", () => {
  console.log("ran:c");
  expect(IS_BROWSER).toBe(false);
  expect(typeof window).toBe("undefined");
  expect([native(fetch), native(Request), native(Response), native(WebSocket)])
    .toEqual([true, true, true, true]);
});
`,
  };
  mkdirSync(dir, { recursive: true });
  try {
    for (const [name, code] of Object.entries(files)) {
      writeFileSync(join(dir, name), code);
    }
    const run = Bun.spawnSync(
      [
        process.execPath,
        "test",
        ...Object.keys(files).map((name) => join(dir, name)),
      ],
      { cwd: frontend, stdout: "pipe", stderr: "pipe" },
    );
    const output = `${run.stdout.toString()}${run.stderr.toString()}`;
    expect({ exit: run.exitCode, output }).toMatchObject({ exit: 0 });
    expect(output.match(/ran:[abc]/g)).toEqual(["ran:a", "ran:b", "ran:c"]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

/**
 * An `act(async ...)` that is never awaited, or whose callback never settles,
 * leaves React's act scope open, and from then on React parks every update
 * in it instead of rendering, in this file and in every file after it. The
 * harness cannot close the scope from outside, so it fails the test that left
 * it open, which names the cause instead of leaving only the failures it
 * causes later.
 */
test("a test that leaves an act scope open fails with a message naming the cause", () => {
  const frontend = join(import.meta.dir, "..", "..");
  const dir = join(import.meta.dir, `.act-leak-${process.pid}`);
  const harness = "@/src/test-utils/render.tsx";
  const files: Record<string, string> = {
    "a_test.tsx": `import { test } from "bun:test";
import { act, setupDom } from "${harness}";
setupDom({ browser: true });
test("awaits", async () => {
  console.log("ran:a");
  await act(async () => { await Promise.resolve(); });
});
test("leaks", () => {
  console.log("ran:b");
  act(async () => { await new Promise(() => {}); });
});
`,
  };
  mkdirSync(dir, { recursive: true });
  try {
    for (const [name, code] of Object.entries(files)) {
      writeFileSync(join(dir, name), code);
    }
    const run = Bun.spawnSync(
      [
        process.execPath,
        "test",
        ...Object.keys(files).map((name) => join(dir, name)),
      ],
      { cwd: frontend, stdout: "pipe", stderr: "pipe" },
    );
    const output = `${run.stdout.toString()}${run.stderr.toString()}`;
    expect(output.match(/ran:[ab]/g)).toEqual(["ran:a", "ran:b"]);
    expect(output).toMatch(/\(fail\) leaks/);
    expect(output).toMatch(/ 1 pass\s+1 fail/);
    expect(output).toContain("This test left a React act() scope open");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

/**
 * React reads `window.event` first thing whenever its scheduler runs a
 * render. An update scheduled outside act at the very end of a DOM file must
 * still run while that file's document exists, not after setupDom has removed
 * it, or the next file reports an unhandled TypeError.
 */
test("render work still queued when a DOM file ends runs before its document goes", () => {
  const frontend = join(import.meta.dir, "..", "..");
  const dir = join(import.meta.dir, `.react-drain-${process.pid}`);
  const harness = "@/src/test-utils/render.tsx";
  const files: Record<string, string> = {
    "a_test.tsx": `import { test } from "bun:test";
import { useState } from "react";
import { render, setupDom } from "${harness}";
setupDom({ browser: true });
let bump = () => {};
function Counter() {
  const [n, setN] = useState(0);
  bump = () => setN((x) => x + 1);
  return <span>{n}</span>;
}
test("ends with an update outside act", () => {
  console.log("ran:a");
  render(<Counter />);
  bump();
});
`,
    "b_test.ts": `import { expect, test } from "bun:test";
test("b", async () => {
  console.log("ran:b");
  await new Promise((r) => setTimeout(r, 20));
  expect(typeof window).toBe("undefined");
});
`,
  };
  mkdirSync(dir, { recursive: true });
  try {
    for (const [name, code] of Object.entries(files)) {
      writeFileSync(join(dir, name), code);
    }
    const run = Bun.spawnSync(
      [
        process.execPath,
        "test",
        ...Object.keys(files).map((name) => join(dir, name)),
      ],
      { cwd: frontend, stdout: "pipe", stderr: "pipe" },
    );
    const output = `${run.stdout.toString()}${run.stderr.toString()}`;
    expect(output.match(/ran:[ab]/g)).toEqual(["ran:a", "ran:b"]);
    expect(output).not.toContain("Unhandled error");
    expect({ exit: run.exitCode, output }).toMatchObject({ exit: 0 });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

/**
 * React decides once, when react-dom first loads, whether the page supports
 * the `input` event; without a document it falls back to a polyfill that
 * never fires `onChange` for typing. And React keeps the timer functions it
 * found at that moment. So typing and a state update scheduled outside act
 * must both still work in a DOM file that runs after a server file, when
 * happy-dom has been removed and registered again in between.
 */
test("typing and late updates reach React in every DOM file, before and after a server file", () => {
  const frontend = join(import.meta.dir, "..", "..");
  const dir = join(import.meta.dir, `.react-env-${process.pid}`);
  const harness = "@/src/test-utils/render.tsx";
  const domFile = (name: string) => `import { expect, test } from "bun:test";
import { useEffect, useState } from "react";
import { body, fireEvent, render, settle, setupDom, waitFor } from "${harness}";
setupDom({ browser: true });
function Probe() {
  const [typed, setTyped] = useState("");
  const [late, setLate] = useState("waiting");
  useEffect(() => { setTimeout(() => setLate("arrived"), 0); }, []);
  return (
    <div>
      <input placeholder="p" value={typed} onChange={(e) => setTyped(e.currentTarget.value)} />
      <span>{typed}|{late}</span>
    </div>
  );
}
test("${name}", async () => {
  console.log("ran:${name}");
  const view = render(<Probe />);
  fireEvent.input(body().getByPlaceholderText("p"), { target: { value: "abc" } });
  await settle();
  expect(view.container.querySelector("span")?.textContent).toBe("abc|arrived");
});
function Slow() {
  const [text, setText] = useState("waiting");
  useEffect(() => { setTimeout(() => setText("arrived"), 20); }, []);
  return <b>{text}</b>;
}
test("${name} waitFor", async () => {
  const view = render(<Slow />);
  await waitFor(() => expect(view.container.textContent).toBe("arrived"));
});
`;
  const files: Record<string, string> = {
    "a_test.tsx": domFile("a"),
    "b_test.ts": `import { expect, test } from "bun:test";
test("b", () => { console.log("ran:b"); expect(typeof window).toBe("undefined"); });
`,
    "c_test.tsx": domFile("c"),
  };
  mkdirSync(dir, { recursive: true });
  try {
    for (const [name, code] of Object.entries(files)) {
      writeFileSync(join(dir, name), code);
    }
    const run = Bun.spawnSync(
      [
        process.execPath,
        "test",
        ...Object.keys(files).map((name) => join(dir, name)),
      ],
      { cwd: frontend, stdout: "pipe", stderr: "pipe" },
    );
    const output = `${run.stdout.toString()}${run.stderr.toString()}`;
    expect({ exit: run.exitCode, output }).toMatchObject({ exit: 0 });
    expect(output.match(/ran:[abc]/g)).toEqual(["ran:a", "ran:b", "ran:c"]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
