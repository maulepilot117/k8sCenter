/** @jsxImportSource react */
import { expect, mock, test } from "bun:test";
import "react";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { ConfirmDialog } = await import("@/components/ui/ConfirmDialog.tsx");

function confirmButton() {
  return body().getByRole("button", { name: "Delete" }) as HTMLButtonElement;
}

test("confirm stays disabled until the exact confirm text is typed", () => {
  const onConfirm = mock(() => {});
  render(
    <ConfirmDialog
      title="Delete thing"
      confirmLabel="Delete"
      typeToConfirm="my-app"
      onConfirm={onConfirm}
      onCancel={() => {}}
    />,
  );
  const input = body().getByPlaceholderText("my-app") as HTMLInputElement;
  expect(confirmButton().disabled).toBe(true);

  fireEvent.input(input, { target: { value: "my-ap" } });
  expect(confirmButton().disabled).toBe(true);

  fireEvent.input(input, { target: { value: "my-app" } });
  expect(confirmButton().disabled).toBe(false);

  fireEvent.input(input, { target: { value: "my-app2" } });
  expect(confirmButton().disabled).toBe(true);
});

test("Escape calls onCancel", () => {
  const onCancel = mock(() => {});
  render(
    <ConfirmDialog
      title="Delete thing"
      confirmLabel="Delete"
      onConfirm={() => {}}
      onCancel={onCancel}
    />,
  );
  act(() => {
    globalThis.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  });
  expect(onCancel).toHaveBeenCalledTimes(1);
});

test("confirm calls onConfirm once when enabled", () => {
  const onConfirm = mock(() => {});
  render(
    <ConfirmDialog
      title="Delete thing"
      confirmLabel="Delete"
      typeToConfirm="x"
      onConfirm={onConfirm}
      onCancel={() => {}}
    />,
  );
  fireEvent.click(confirmButton());
  expect(onConfirm).toHaveBeenCalledTimes(0);
  fireEvent.input(body().getByPlaceholderText("x"), { target: { value: "x" } });
  fireEvent.click(confirmButton());
  expect(onConfirm).toHaveBeenCalledTimes(1);
});
