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

/** A component on React APIs, driving an unconverted Preact component. */
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

test("a converted component renders and drives an unconverted Preact child", () => {
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
