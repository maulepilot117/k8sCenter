/** @jsxImportSource react */
import { expect, test } from "bun:test";
// A static import, so the hook module loads before setupDom registers a DOM,
// as it does when another test file (or a server render) imports it first:
// the guard must not decide "no browser" once at load and keep that forever.
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

function leave(): Event {
  const event = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(event);
  return event;
}

function Form({ dirty }: { dirty: boolean }) {
  useDirtyGuard(dirty);
  return null;
}

test("leaving is blocked only while the form is dirty", () => {
  const view = render(<Form dirty={false} />);
  expect(leave().defaultPrevented).toBe(false);

  act(() => view.rerender(<Form dirty={true} />));
  expect(leave().defaultPrevented).toBe(true);

  act(() => view.rerender(<Form dirty={false} />));
  expect(leave().defaultPrevented).toBe(false);

  act(() => view.unmount());
});

test("the warning is gone after the form unmounts", () => {
  const view = render(<Form dirty={true} />);
  expect(leave().defaultPrevented).toBe(true);

  act(() => view.unmount());
  expect(leave().defaultPrevented).toBe(false);
});
