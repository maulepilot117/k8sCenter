import { afterAll, afterEach, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";

GlobalRegistrator.register({ url: "http://localhost/" });

const { act, cleanup, fireEvent, render } = await import(
  "@testing-library/preact"
);
const { toasts } = await import("@/src/lib/toast-store.ts");
const { default: ToastProvider, showToast } = await import(
  "./ToastProvider.tsx"
);

afterEach(() => {
  cleanup();
  toasts.value = [];
});
afterAll(() => GlobalRegistrator.unregister());

test("showToast from the old import path shows the toast, and dismiss removes it", async () => {
  const r = render(<ToastProvider />);
  expect(r.getByRole("status").textContent).toBe("");

  await act(() => {
    showToast("Saved", "success");
  });
  expect(r.getByRole("status").textContent).toContain("Saved");

  await act(() => {
    fireEvent.click(r.getByRole("button"));
  });
  expect(r.getByRole("status").textContent).toBe("");
});
