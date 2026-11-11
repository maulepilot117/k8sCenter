import { afterAll, describe, expect, mock, test } from "bun:test";
import type { CopyableLayout } from "@/lib/dashboard/layout-store.ts";
import type { LayoutItem, WidgetDef } from "@/lib/dashboard/types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// Bun's own fetch, captured before happy-dom replaces it.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const NAMESPACES = ["dev", "prod"];
const SERVICES: Record<string, string[]> = {
  prod: ["api", "web"],
  dev: ["debug-svc"],
};

// useNamespaces and useNamespacedServices fetch their options; answer them.
globalThis.fetch = (async (input: RequestInfo | URL) => {
  const url = String(input);
  const svc = /\/resources\/services\/([^?/]+)/.exec(url);
  const names = svc ? (SERVICES[decodeURIComponent(svc[1])] ?? []) : NAMESPACES;
  return new Response(
    JSON.stringify({ data: names.map((name) => ({ metadata: { name } })) }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}) as unknown as typeof globalThis.fetch;
afterAll(() => {
  globalThis.fetch = bunFetch;
});

const { default: ModalDialogShell } = await import(
  "@/components/dashboard/ModalDialogShell.tsx"
);
const { default: LayoutCopyDialog } = await import(
  "@/components/dashboard/LayoutCopyDialog.tsx"
);
const { default: WidgetParamDialog } = await import(
  "@/components/dashboard/WidgetParamDialog.tsx"
);

function renderShell(onClose: () => void) {
  return render(
    <ModalDialogShell
      titleId="t-id"
      title="Shell title"
      testId="shell"
      closeTestId="shell-close"
      onClose={onClose}
    >
      <button type="button">First body</button>
      <button type="button">Last body</button>
    </ModalDialogShell>,
  );
}

describe("ModalDialogShell", () => {
  test("renders a labelled modal dialog with its title and children", () => {
    renderShell(() => {});
    const dialog = body().getByRole("dialog");
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(dialog.getAttribute("aria-labelledby")).toBe("t-id");
    expect(dialog.getAttribute("data-testid")).toBe("shell");
    expect(body().getByText("Shell title").getAttribute("id")).toBe("t-id");
    expect(body().getByText("First body")).toBeTruthy();
  });

  test("Escape closes", () => {
    const onClose = mock(() => {});
    renderShell(onClose);
    fireEvent.keyDown(body().getByText("First body"), { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test("Close button closes", () => {
    const onClose = mock(() => {});
    renderShell(onClose);
    fireEvent.click(body().getByTestId("shell-close"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test("Tab from the last stop wraps to the first, Shift+Tab from the first to the last", () => {
    renderShell(() => {});
    const close = body().getByTestId("shell-close");
    const last = body().getByText("Last body");

    last.focus();
    fireEvent.keyDown(last, { key: "Tab" });
    expect(document.activeElement).toBe(close);

    close.focus();
    fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);
  });

  test("Tab in the middle is left to the browser", () => {
    renderShell(() => {});
    const mid = body().getByText("First body");
    mid.focus();
    const proceeded = fireEvent.keyDown(mid, { key: "Tab" });
    expect(proceeded).toBe(true);
    expect(document.activeElement).toBe(mid);
  });

  test("clicking the scrim closes, clicking the panel does not", () => {
    const onClose = mock(() => {});
    renderShell(onClose);
    const dialog = body().getByRole("dialog");
    fireEvent.click(dialog);
    expect(onClose).toHaveBeenCalledTimes(0);
    fireEvent.click(dialog.parentElement as HTMLElement);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

function layoutFor(id: string, label: string, widgets: number): CopyableLayout {
  return {
    id,
    clusterId: id,
    clusterLabel: label,
    updatedAt: "2026-01-15T10:00:00Z",
    warnings: [],
    config: {
      schemaVersion: 1,
      scope: "overview",
      columns: 12,
      items: Array.from({ length: widgets }, (_, i) => ({
        instanceId: `i${i}`,
        id: "w",
        x: 0,
        y: i,
        w: 1,
        h: 1,
      })),
    },
  } as CopyableLayout;
}

describe("LayoutCopyDialog", () => {
  test("lists the offered clusters and hands the picked one to onCopy", () => {
    const layouts = [layoutFor("c-a", "Alpha", 3), layoutFor("c-b", "Beta", 1)];
    const onCopy = mock((_l: CopyableLayout) => {});
    render(
      <LayoutCopyDialog layouts={layouts} onCopy={onCopy} onClose={() => {}} />,
    );
    const rows = body().getAllByTestId("copy-layout-option");
    expect(
      rows.map((r: HTMLElement) => r.getAttribute("data-cluster-id")),
    ).toEqual(["c-a", "c-b"]);
    expect(body().getByText("Alpha")).toBeTruthy();
    expect(body().getByText(/3 widgets/)).toBeTruthy();
    expect(body().getByText(/1 widget(?!s)/)).toBeTruthy();

    fireEvent.click(rows[1]);
    expect(onCopy).toHaveBeenCalledTimes(1);
    expect(onCopy.mock.calls[0][0]).toBe(layouts[1]);
  });

  test("focus lands on the first row on open", () => {
    render(
      <LayoutCopyDialog
        layouts={[layoutFor("c-a", "Alpha", 1)]}
        onCopy={() => {}}
        onClose={() => {}}
      />,
    );
    expect(document.activeElement).toBe(
      body().getAllByTestId("copy-layout-option")[0],
    );
  });

  test("Close and Escape call onClose without copying", () => {
    const onCopy = mock(() => {});
    const onClose = mock(() => {});
    render(
      <LayoutCopyDialog
        layouts={[layoutFor("c-a", "Alpha", 1)]}
        onCopy={onCopy}
        onClose={onClose}
      />,
    );
    fireEvent.click(body().getByTestId("close-layout-copy"));
    fireEvent.keyDown(body().getByTestId("copy-layout-option"), {
      key: "Escape",
    });
    expect(onClose).toHaveBeenCalledTimes(2);
    expect(onCopy).toHaveBeenCalledTimes(0);
  });
});

const def = {
  id: "ns-widget",
  title: "Namespace Pods",
  params: { namespace: [] },
} as unknown as WidgetDef;

const closedDef = {
  id: "closed-widget",
  title: "Closed Set",
  params: { range: ["1h", "6h"] },
} as unknown as WidgetDef;

const serviceDef = {
  id: "svc-widget",
  title: "Service Signals",
  params: { namespace: [], service: [] },
} as unknown as WidgetDef;

function item(
  instanceId: string,
  id: string,
  params: Record<string, string>,
): LayoutItem {
  return { instanceId, id, x: 0, y: 0, w: 1, h: 1, params };
}

async function namespacesLoaded() {
  await waitFor(() => {
    expect(body().getByText("dev")).toBeTruthy();
  });
}

describe("WidgetParamDialog", () => {
  test("pre-fills the current values and saves an edited one", async () => {
    const onConfirm = mock((_v: Record<string, string>) => {});
    const editing = item("x1", def.id, { namespace: "prod" });
    render(
      <WidgetParamDialog
        def={def}
        placed={[editing]}
        editing={editing}
        onConfirm={onConfirm}
        onCancel={() => {}}
      />,
    );
    await namespacesLoaded();
    expect(body().getByText("Change what Namespace Pods reads")).toBeTruthy();
    const select = body().getByTestId(
      "widget-param-namespace",
    ) as HTMLSelectElement;
    expect(select.value).toBe("prod");

    fireEvent.change(select, { target: { value: "dev" } });
    fireEvent.click(body().getByTestId("confirm-widget-params"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onConfirm.mock.calls[0][0]).toEqual({ namespace: "dev" });
  });

  test("an unfilled field names itself and does not save; editing clears the message", async () => {
    const onConfirm = mock(() => {});
    render(
      <WidgetParamDialog
        def={def}
        placed={[]}
        onConfirm={onConfirm}
        onCancel={() => {}}
      />,
    );
    await namespacesLoaded();
    expect(body().getByText("Add Namespace Pods")).toBeTruthy();
    fireEvent.click(body().getByTestId("confirm-widget-params"));
    expect(body().getByTestId("widget-param-error").textContent).toBe(
      "Choose a namespace.",
    );
    expect(onConfirm).toHaveBeenCalledTimes(0);

    fireEvent.change(body().getByTestId("widget-param-namespace"), {
      target: { value: "prod" },
    });
    expect(body().queryByTestId("widget-param-error")).toBeNull();
    fireEvent.click(body().getByTestId("confirm-widget-params"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  test("a value outside a declared closed set is refused", () => {
    const onConfirm = mock(() => {});
    render(
      <WidgetParamDialog
        def={closedDef}
        placed={[]}
        editing={item("x1", closedDef.id, { range: "99y" })}
        onConfirm={onConfirm}
        onCancel={() => {}}
      />,
    );
    fireEvent.click(body().getByTestId("confirm-widget-params"));
    expect(body().getByTestId("widget-param-error").textContent).toBe(
      "Choose one of the offered values.",
    );
    expect(onConfirm).toHaveBeenCalledTimes(0);

    fireEvent.change(body().getByTestId("widget-param-range"), {
      target: { value: "6h" },
    });
    fireEvent.click(body().getByTestId("confirm-widget-params"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  test("the same widget with the same values is refused as a duplicate", async () => {
    const onConfirm = mock(() => {});
    render(
      <WidgetParamDialog
        def={def}
        placed={[item("other", def.id, { namespace: "prod" })]}
        onConfirm={onConfirm}
        onCancel={() => {}}
      />,
    );
    await namespacesLoaded();
    fireEvent.change(body().getByTestId("widget-param-namespace"), {
      target: { value: "prod" },
    });
    fireEvent.click(body().getByTestId("confirm-widget-params"));
    expect(body().getByTestId("widget-param-error").textContent).toContain(
      "is already on this dashboard",
    );
    expect(onConfirm).toHaveBeenCalledTimes(0);
  });

  test("Cancel calls onCancel and saves nothing", () => {
    const onConfirm = mock(() => {});
    const onCancel = mock(() => {});
    render(
      <WidgetParamDialog
        def={def}
        placed={[]}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );
    fireEvent.click(body().getByTestId("cancel-widget-params"));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).toHaveBeenCalledTimes(0);
  });

  test("a service field waits for a namespace, and changing the namespace clears the service", async () => {
    const onConfirm = mock((_v: Record<string, string>) => {});
    render(
      <WidgetParamDialog
        def={serviceDef}
        placed={[]}
        onConfirm={onConfirm}
        onCancel={() => {}}
      />,
    );
    await namespacesLoaded();
    const service = body().getByTestId(
      "widget-param-service",
    ) as HTMLSelectElement;
    expect(service.disabled).toBe(true);
    expect(body().getByTestId("widget-param-service-hint").textContent).toBe(
      "Choose a namespace first.",
    );

    const ns = body().getByTestId("widget-param-namespace");
    fireEvent.change(ns, { target: { value: "prod" } });
    await waitFor(() => {
      expect(service.disabled).toBe(false);
      expect(body().getByText("web")).toBeTruthy();
    });
    fireEvent.change(service, { target: { value: "web" } });
    expect(service.value).toBe("web");

    await act(async () => {
      fireEvent.change(ns, { target: { value: "dev" } });
    });
    await waitFor(() => {
      expect(body().getByText("debug-svc")).toBeTruthy();
    });
    expect(
      (body().getByTestId("widget-param-service") as HTMLSelectElement).value,
    ).toBe("");

    fireEvent.change(body().getByTestId("widget-param-service"), {
      target: { value: "debug-svc" },
    });
    fireEvent.click(body().getByTestId("confirm-widget-params"));
    expect(onConfirm.mock.calls[0][0]).toEqual({
      namespace: "dev",
      service: "debug-svc",
    });
  });
});
