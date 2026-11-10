/** @jsxImportSource react */
import { afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { getWidget, registerWidget } from "@/lib/dashboard/registry.ts";
import type { LayoutItem } from "@/lib/dashboard/types.ts";
import { act, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: DashboardGrid } = await import(
  "@/components/dashboard/DashboardGrid.tsx"
);

// Two small registered widgets with different titles and no data sources, so
// the host renders them straight away. The `fixture-` prefix is the repo's
// convention for registrations the registry cannot drop (it has no
// unregister) and other dashboard tests filter out.
const CPU = "fixture-grid-first";
const ALERTS = "fixture-grid-second";
for (const [id, title] of [
  [CPU, "First fixture"],
  [ALERTS, "Second fixture"],
] as const) {
  registerWidget({
    id,
    title,
    family: "cluster",
    scopes: ["overview"],
    sources: [],
    minW: 2,
    minH: 2,
    defaultW: 3,
    defaultH: 3,
    modes: ["normal"],
    render: () => <div>{title} body</div>,
  });
}

const GRID_WIDTH = 1200;
// Cell stride: (1200 - 11 gaps of 16) / 12 columns, plus one 16px gap.
const COL_STRIDE = (GRID_WIDTH - 16 * 11) / 12 + 16;
const ROW_STRIDE = 40 + 16;
/** A client x inside column `col` (0-based). */
const colX = (col: number) => col * COL_STRIDE + COL_STRIDE / 2;
const rowY = (row: number) => row * ROW_STRIDE + 10;

function item(
  instanceId: string,
  id: string,
  x: number,
  y: number,
  w: number,
  h: number,
): LayoutItem {
  return { instanceId, id, x, y, w, h };
}

function layoutOf(items: LayoutItem[]) {
  return { schemaVersion: 1, scope: "overview" as const, columns: 12, items };
}

const restores: Array<() => void> = [];
function stub(target: object, key: string, value: unknown): void {
  const prev = Object.getOwnPropertyDescriptor(target, key);
  Object.defineProperty(target, key, {
    configurable: true,
    writable: true,
    value,
  });
  restores.push(() => {
    if (prev) Object.defineProperty(target, key, prev);
    else Reflect.deleteProperty(target, key);
  });
}

beforeEach(() => {
  // happy-dom does no layout: give the grid a width (wide mode) and a box.
  const prevWidth = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "clientWidth",
  );
  Object.defineProperty(HTMLElement.prototype, "clientWidth", {
    configurable: true,
    get: () => GRID_WIDTH,
  });
  restores.push(() => {
    if (prevWidth)
      Object.defineProperty(HTMLElement.prototype, "clientWidth", prevWidth);
    else Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
  });
  stub(Element.prototype, "getBoundingClientRect", function (this: Element) {
    const width =
      this.getAttribute("data-testid") === "dashboard-grid" ? GRID_WIDTH : 0;
    return {
      left: 0,
      top: 0,
      right: width,
      bottom: 0,
      width,
      height: 0,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    };
  });
  if (typeof globalThis.ResizeObserver === "undefined") {
    stub(
      globalThis,
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
  }
  for (const name of ["setPointerCapture", "releasePointerCapture"] as const) {
    if (typeof Element.prototype[name] !== "function") {
      stub(Element.prototype, name, () => {});
    }
  }
  if (typeof Element.prototype.hasPointerCapture !== "function") {
    stub(Element.prototype, "hasPointerCapture", () => false);
  }
});

afterEach(() => {
  while (restores.length) restores.pop()?.();
});

function cell(container: HTMLElement, instanceId: string): HTMLElement {
  const el = container.querySelector<HTMLElement>(
    `[data-instance-id="${instanceId}"]`,
  );
  if (!el) throw new Error(`no cell ${instanceId}`);
  return el;
}

function handleOf(container: HTMLElement, instanceId: string): HTMLElement {
  return cell(container, instanceId).querySelector(
    '[data-testid="drag-handle"]',
  ) as HTMLElement;
}

function domOrder(container: HTMLElement): string[] {
  return [...container.querySelectorAll("[data-instance-id]")].map(
    (e) => e.getAttribute("data-instance-id") ?? "",
  );
}

function announcement(container: HTMLElement): string {
  return (
    container.querySelector('[data-testid="grid-announcement"]')?.textContent ??
    ""
  );
}

/** The layout the most recent onChange call reported. */
function committed(onChange: ReturnType<typeof mock>): LayoutItem[] {
  const calls = onChange.mock.calls;
  return calls[calls.length - 1][0] as LayoutItem[];
}

function find(items: LayoutItem[], instanceId: string): LayoutItem {
  const found = items.find((i) => i.instanceId === instanceId);
  if (!found) throw new Error(`no item ${instanceId}`);
  return found;
}

/** Mounts the grid inside a scroll box, the way <main> holds it in the page. */
function mount(items: LayoutItem[], props: Record<string, unknown> = {}) {
  const scroller = document.createElement("div");
  document.body.appendChild(scroller);
  const onChange = mock((_items: LayoutItem[]) => {});
  const view = render(
    <DashboardGrid
      initial={layoutOf(items)}
      editable
      onChange={onChange}
      {...props}
    />,
    { container: scroller },
  );
  return { container: view.container as HTMLElement, scroller, onChange };
}

function press(el: Element, init: { clientX: number; clientY: number }) {
  fireEvent.pointerDown(el, { pointerId: 1, button: 0, ...init });
}

function moveTo(col: number) {
  fireEvent.pointerMove(window, {
    pointerId: 1,
    clientX: colX(col),
    clientY: rowY(0),
  });
}

describe("DashboardGrid pointer drag", () => {
  test("one session across three cells lands the widget in the last one", () => {
    const { container, onChange } = mount([
      item("a", CPU, 0, 0, 2, 2),
      item("b", ALERTS, 0, 2, 3, 3),
    ]);

    press(handleOf(container, "a"), { clientX: colX(0), clientY: rowY(0) });
    expect(cell(container, "a").getAttribute("data-dragging")).toBe("true");
    for (const col of [1, 2, 3]) moveTo(col);
    fireEvent.pointerUp(window, { pointerId: 1 });

    const a = find(committed(onChange), "a");
    expect(a.x).toBe(3);
    expect(a.y).toBe(0);
    expect(cell(container, "a").getAttribute("data-dragging")).toBeNull();
    expect(cell(container, "a").style.gridColumn).toBe("4 / span 2");
  });

  test("Escape during a drag puts the layout back", () => {
    const { container, onChange } = mount([
      item("a", CPU, 0, 0, 2, 2),
      item("b", ALERTS, 0, 2, 3, 3),
    ]);

    press(handleOf(container, "a"), { clientX: colX(0), clientY: rowY(0) });
    moveTo(4);
    expect(find(committed(onChange), "a").x).toBe(4);
    fireEvent.keyDown(window, { key: "Escape" });

    expect(find(committed(onChange), "a").x).toBe(0);
    expect(cell(container, "a").getAttribute("data-dragging")).toBeNull();
  });
});

describe("DashboardGrid keyboard", () => {
  test("an arrow moves the focused widget, keeps focus, announces it", () => {
    moveLikeChromium(document.createElement("div"));
    const scrolled: Element[] = [];
    stub(Element.prototype, "scrollIntoView", function (this: Element) {
      scrolled.push(this);
    });
    const { container, onChange } = mount([
      item("a", CPU, 0, 0, 2, 2),
      item("b", ALERTS, 2, 0, 3, 3),
    ]);
    const b = cell(container, "b");
    act(() => b.focus());
    expect(document.activeElement).toBe(b);

    fireEvent.keyDown(b, { key: "ArrowLeft" });

    expect(find(committed(onChange), "b").x).toBe(1);
    // b now overlaps a, which is displaced below it: DOM order follows.
    expect(domOrder(container)[0]).toBe("b");
    expect(document.activeElement).toBe(cell(container, "b"));
    // The grid never scrolls a cell into view itself; the focus hook does,
    // for moves between widgets.
    expect(scrolled).toHaveLength(0);
    const title = getWidget(ALERTS)?.title;
    expect(announcement(container)).toBe(
      `${title}, column 2 of 12, row 1, 3 wide, 3 tall`,
    );
  });

  test("Shift and an arrow resizes and announces the new size", () => {
    const { container, onChange } = mount([item("a", CPU, 0, 0, 3, 3)]);
    const a = cell(container, "a");
    act(() => a.focus());
    fireEvent.keyDown(a, { key: "ArrowRight", shiftKey: true });
    expect(committed(onChange)[0].w).toBe(4);
    expect(announcement(container)).toContain("4 wide, 3 tall");
  });

  test("Escape on a widget asks the caller to leave edit mode", () => {
    const onExitEdit = mock(() => {});
    const { container } = mount([item("a", CPU, 0, 0, 3, 3)], { onExitEdit });
    fireEvent.keyDown(cell(container, "a"), { key: "Escape" });
    expect(onExitEdit).toHaveBeenCalledTimes(1);
  });

  test("removing a widget reports its position and announces it", () => {
    const onRemoved = mock((_index: number) => {});
    const { container, onChange } = mount(
      [item("a", CPU, 0, 0, 2, 2), item("b", ALERTS, 2, 0, 3, 3)],
      { onRemoved },
    );
    fireEvent.click(
      cell(container, "b").querySelector(
        '[data-testid="remove-widget"]',
      ) as HTMLElement,
    );
    expect(onRemoved).toHaveBeenCalledWith(1);
    expect(committed(onChange).map((i) => i.instanceId)).toEqual(["a"]);
    expect(announcement(container)).toBe(`Removed ${getWidget(ALERTS)?.title}`);
  });
});

/**
 * A reorder that keeps focus on the moved cell (as `moveBefore` does) makes
 * Chromium scroll every box above the focused cell to show it. happy-dom has
 * neither behaviour (its insertBefore drops focus and never scrolls), so this
 * stands in for the worst case: a reorder of the grid's children while focus
 * is inside it keeps focus and resets the scroll of the ancestors to 0.
 */
function moveLikeChromium(scroller: HTMLElement) {
  const original = Node.prototype.insertBefore;
  stub(
    Node.prototype,
    "insertBefore",
    function (this: Element, node: Node, ref: Node | null) {
      const focused =
        this.getAttribute?.("data-testid") === "dashboard-grid" &&
        this.contains(document.activeElement)
          ? (document.activeElement as HTMLElement)
          : null;
      const out = original.call(this, node, ref);
      if (focused) {
        focused.focus();
        scroller.scrollTop = 0;
        document.documentElement.scrollTop = 0;
      }
      return out;
    },
  );
}

describe("DashboardGrid scroll hold (a reorder that keeps focus)", () => {
  test("a keyboard move leaves every ancestor's scroll offset where it was", () => {
    const { container, scroller } = mount([
      item("a", CPU, 0, 0, 2, 2),
      item("b", ALERTS, 2, 0, 3, 3),
    ]);
    scroller.scrollTop = 300;
    document.documentElement.scrollTop = 120;
    moveLikeChromium(scroller);

    const b = cell(container, "b");
    act(() => b.focus());
    fireEvent.keyDown(b, { key: "ArrowLeft" });

    expect(domOrder(container)[0]).toBe("b");
    expect(scroller.scrollTop).toBe(300);
    expect(document.documentElement.scrollTop).toBe(120);
  });

  test("a pointer drag move leaves the scroll offsets where they were", () => {
    const { container, scroller } = mount([
      item("a", CPU, 0, 0, 2, 2),
      item("b", ALERTS, 2, 0, 3, 3),
    ]);
    scroller.scrollTop = 300;
    moveLikeChromium(scroller);

    act(() => cell(container, "b").focus());
    press(handleOf(container, "b"), { clientX: colX(2), clientY: rowY(0) });
    moveTo(1);
    fireEvent.pointerUp(window, { pointerId: 1 });

    expect(domOrder(container)[0]).toBe("b");
    expect(scroller.scrollTop).toBe(300);
  });
});
