/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { useState } from "react";
import { act, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The notification form shell focuses its first field when it opens. Its
 * callers pass a new inline onCancel on every render and re-render on every
 * keystroke, so the shell must not refocus on a re-render, and Escape must
 * still reach the caller's latest onCancel.
 */
setupDom({ browser: true });

const { NotificationFormShell } = await import("./NotificationShared.tsx");

function Form({ onCancel }: { onCancel: (name: string) => void }) {
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  return (
    <NotificationFormShell
      id="t"
      title="Create"
      submitting={false}
      error={null}
      onSubmit={() => {}}
      onCancel={() => onCancel(name)}
    >
      <input
        aria-label="Name"
        value={name}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      <input
        aria-label="URL"
        value={url}
        onChange={(e) => setUrl(e.currentTarget.value)}
      />
    </NotificationFormShell>
  );
}

test("opening the shell focuses the first field", async () => {
  const view = render(<Form onCancel={() => {}} />);
  await act(async () => {});
  expect(document.activeElement).toBe(view.getByLabelText("Name"));
});

test("typing in a later field keeps focus there", async () => {
  const view = render(<Form onCancel={() => {}} />);
  await act(async () => {});
  const url = view.getByLabelText("URL") as HTMLInputElement;
  url.focus();
  await act(async () => {
    fireEvent.input(url, { target: { value: "https://hooks.example" } });
  });
  expect(url.value).toBe("https://hooks.example");
  expect(document.activeElement).toBe(url);
});

test("Escape cancels with the form's latest state", async () => {
  const cancelled: string[] = [];
  const view = render(<Form onCancel={(name) => cancelled.push(name)} />);
  await act(async () => {});
  await act(async () => {
    fireEvent.input(view.getByLabelText("Name"), {
      target: { value: "slack" },
    });
  });
  await act(async () => {
    fireEvent.keyDown(globalThis.window, { key: "Escape" });
  });
  expect(cancelled).toEqual(["slack"]);
});
