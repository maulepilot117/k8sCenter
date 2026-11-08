import { afterEach, expect, test } from "bun:test";
import { act, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { toasts, showToast } = await import("@/src/lib/toast-store.ts");
const { default: ToastProvider } = await import("./ToastProvider.tsx");

afterEach(() => {
  toasts.value = [];
});

test("showToast from the store shows the toast, and dismiss removes it", async () => {
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
