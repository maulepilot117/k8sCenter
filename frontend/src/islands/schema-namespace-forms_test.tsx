import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { useContext, useEffect } from "react";
import { wizardBusy } from "@/lib/wizard-busy.ts";
import { WizardPrimaryActionContext } from "@/lib/wizard-primary-action.ts";
import { toasts } from "@/src/lib/toast-store.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// Bun's fetch, captured before setupDom swaps in happy-dom's.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: WizardShell } = await import("./WizardShell.tsx");
const { default: NamespaceCreator } = await import("./NamespaceCreator.tsx");
const { default: SchemaForm } = await import("./SchemaForm.tsx");

afterAll(() => {
  globalThis.fetch = bunFetch;
});

// ── fetch stub ──────────────────────────────────────────────────────────

interface Call {
  method: string;
  url: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];
let routes: Array<(c: Call) => unknown | undefined> = [];

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(status === 200 ? { data } : data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  calls = [];
  routes = [];
  toasts.value = [];
  wizardBusy.value = false;
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const call: Call = {
      method: init?.method ?? "GET",
      url: String(input),
      body: init?.body ? JSON.parse(String(init.body)) : null,
    };
    calls.push(call);
    for (const route of routes) {
      const out = route(call);
      if (out !== undefined) return out as Response;
    }
    return json([]);
  }) as unknown as typeof fetch;
});

afterEach(() => {
  wizardBusy.value = false;
});

const posts = () => calls.filter((c) => c.method === "POST");

/** Typing: an `input` event, which is what text inputs listen to. */
function type(el: Element, value: string) {
  fireEvent.input(el, { target: { value } });
}

// ── WizardShell ─────────────────────────────────────────────────────────

function shell(opts: {
  onCancel?: () => void;
  onNext?: () => void;
  children?: React.ReactNode;
}) {
  return render(
    <WizardShell
      title="Create Thing"
      steps={[{ label: "Basics" }, { label: "Review" }]}
      current={0}
      onStep={() => {}}
      onCancel={opts.onCancel ?? (() => {})}
      onBack={() => {}}
      onNext={opts.onNext ?? (() => {})}
      nextLabel="Next"
    >
      {opts.children ?? <p>step body</p>}
    </WizardShell>,
  );
}

/** The fixed scrim behind the sheet: the shell's outermost element. */
const scrim = (container: Element) =>
  container.firstElementChild as HTMLElement;

test("WizardShell: scrim, X and Cancel call onCancel when idle", () => {
  let cancelled = 0;
  const { container } = shell({ onCancel: () => cancelled++ });

  act(() => scrim(container).click());
  expect(cancelled).toBe(1);
  act(() => body().getByLabelText("Close").click());
  expect(cancelled).toBe(2);
  act(() => body().getByText("Cancel").click());
  expect(cancelled).toBe(3);
});

test("WizardShell: a click inside the sheet does not close it", () => {
  let cancelled = 0;
  shell({ onCancel: () => cancelled++ });
  act(() => body().getByText("step body").click());
  expect(cancelled).toBe(0);
});

test("WizardShell: scrim, X and Cancel do not close while an apply is in flight", () => {
  let cancelled = 0;
  const { container } = shell({ onCancel: () => cancelled++ });

  act(() => {
    wizardBusy.value = true;
  });
  expect((body().getByLabelText("Close") as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect((body().getByText("Cancel") as HTMLButtonElement).disabled).toBe(true);
  act(() => scrim(container).click());
  act(() => body().getByLabelText("Close").click());
  act(() => body().getByText("Cancel").click());
  expect(cancelled).toBe(0);

  act(() => {
    wizardBusy.value = false;
  });
  act(() => scrim(container).click());
  expect(cancelled).toBe(1);
});

function RegisterAction(props: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
}) {
  const slot = useContext(WizardPrimaryActionContext);
  useEffect(() => {
    if (!slot) return;
    slot({
      label: props.label,
      onClick: props.onClick,
      disabled: props.disabled,
    });
    return () => {
      slot(null);
    };
  }, [slot, props.label, props.onClick, props.disabled]);
  return <p>registering step</p>;
}

test("WizardShell: a step's registered action replaces the Next button", () => {
  let next = 0;
  let own = 0;
  const onOwn = () => own++;
  const view = shell({
    onNext: () => next++,
    children: <RegisterAction label="Do it" onClick={onOwn} />,
  });

  expect(body().queryByText("Next")).toBeNull();
  act(() => body().getByText("Do it").click());
  expect(own).toBe(1);
  expect(next).toBe(0);

  // Unmounting the step restores the wizard's own button.
  view.rerender(
    <WizardShell
      title="Create Thing"
      steps={[{ label: "Basics" }, { label: "Review" }]}
      current={0}
      onStep={() => {}}
      onCancel={() => {}}
      onBack={() => {}}
      onNext={() => next++}
      nextLabel="Next"
    >
      <p>plain step</p>
    </WizardShell>,
  );
  expect(body().queryByText("Do it")).toBeNull();
  act(() => body().getByText("Next").click());
  expect(next).toBe(1);
});

test("WizardShell: a registered disabled action disables the footer button", () => {
  let own = 0;
  const onOwn = () => own++;
  shell({ children: <RegisterAction label="Wait" onClick={onOwn} disabled /> });
  const btn = body().getByText("Wait").closest("button") as HTMLButtonElement;
  expect(btn.disabled).toBe(true);
  act(() => btn.click());
  expect(own).toBe(0);
});

// ── NamespaceCreator ────────────────────────────────────────────────────

test("NamespaceCreator: submitting a valid name posts it with its labels", async () => {
  render(<NamespaceCreator />);
  type(body().getByLabelText(/Namespace Name/), "team-a");

  // The label editor starts with one empty row.
  const keys = body().getAllByPlaceholderText("key");
  const values = body().getAllByPlaceholderText("value");
  type(keys[0], "env");
  type(values[0], "prod");

  await act(async () => {
    fireEvent.submit(
      body()
        .getByLabelText(/Namespace Name/)
        .closest("form") as Element,
    );
  });

  await waitFor(() => expect(posts()).toHaveLength(1));
  expect(posts()[0].url).toContain("/v1/resources/namespaces");
  expect(posts()[0].body).toEqual({
    metadata: { name: "team-a", labels: { env: "prod" } },
  });
});

test("NamespaceCreator: an invalid name shows an error and sends nothing", async () => {
  render(<NamespaceCreator />);
  type(body().getByLabelText(/Namespace Name/), "Bad_Name");
  await act(async () => {
    fireEvent.submit(
      body()
        .getByLabelText(/Namespace Name/)
        .closest("form") as Element,
    );
  });
  expect(body().getByText(/lowercase alphanumeric/)).toBeTruthy();
  expect(posts()).toHaveLength(0);
});

test("NamespaceCreator: a reserved kube- name is refused", async () => {
  render(<NamespaceCreator />);
  type(body().getByLabelText(/Namespace Name/), "kube-thing");
  await act(async () => {
    fireEvent.submit(
      body()
        .getByLabelText(/Namespace Name/)
        .closest("form") as Element,
    );
  });
  expect(body().getByText(/reserved for Kubernetes/)).toBeTruthy();
  expect(posts()).toHaveLength(0);
});

test("NamespaceCreator: an empty name is refused", async () => {
  render(<NamespaceCreator />);
  await act(async () => {
    fireEvent.submit(
      body()
        .getByLabelText(/Namespace Name/)
        .closest("form") as Element,
    );
  });
  expect(body().getByText("Name is required")).toBeTruthy();
  expect(posts()).toHaveLength(0);
});

// ── SchemaForm / SchemaFormField ────────────────────────────────────────

const CRD = {
  info: { kind: "Widget", scope: "Namespaced", version: "v1" },
  schema: {
    type: "object",
    properties: {
      spec: {
        type: "object",
        required: ["host"],
        properties: {
          host: { type: "string" },
          replicas: { type: "integer" },
          tags: { type: "array", items: { type: "string" } },
          rules: {
            type: "array",
            items: {
              type: "object",
              properties: { path: { type: "string" } },
            },
          },
        },
      },
    },
  },
};

function mountForm() {
  routes.push((c) => {
    if (c.method === "GET" && c.url.includes("/v1/extensions/crds/")) {
      return json(CRD);
    }
    if (c.method === "GET" && c.url.includes("/v1/resources/namespaces")) {
      return json([
        { metadata: { name: "default" } },
        { metadata: { name: "team-a" } },
      ]);
    }
    if (c.method === "POST") return json({});
    return undefined;
  });
  const view = render(
    <SchemaForm group="example.io" resource="widgets" mode="create" />,
  );
  return view;
}

const nameInput = () =>
  document.getElementById("field-metadata-name") as HTMLInputElement;

async function ready() {
  await waitFor(() => expect(nameInput()).toBeTruthy());
}

test("SchemaForm: editing fields puts their values in the submit body", async () => {
  mountForm();
  await ready();
  type(nameInput(), "w1");
  type(document.getElementById("field-spec-host") as Element, "example.com");
  type(document.getElementById("field-spec-replicas") as Element, "3");

  await act(async () => body().getByText("Create").click());
  await waitFor(() => expect(posts()).toHaveLength(1));

  const sent = posts()[0];
  expect(sent.url).toContain("/v1/extensions/resources/example.io/widgets/");
  expect(sent.body).toMatchObject({
    apiVersion: "example.io/v1",
    kind: "Widget",
    metadata: { name: "w1" },
    spec: { host: "example.com", replicas: 3 },
  });
});

test("SchemaForm: the YAML tab reflects the edited fields", async () => {
  mountForm();
  await ready();
  type(nameInput(), "w1");
  type(document.getElementById("field-spec-host") as Element, "example.com");

  act(() => body().getByText("View YAML").click());
  await waitFor(() =>
    expect(document.body.textContent).toContain("example.com"),
  );
  expect(document.body.textContent).toContain("w1");
});

test("SchemaForm: a required field is marked, and an empty name sends nothing", async () => {
  mountForm();
  await ready();
  const host = body().getByText("host");
  expect(host.parentElement?.textContent).toContain("*");

  await act(async () => body().getByText("Create").click());
  expect(toasts.value.map((t) => t.message)).toContain("Name is required");
  expect(posts()).toHaveLength(0);
});

test("SchemaForm: adding and removing primitive array items keeps the others' values", async () => {
  mountForm();
  await ready();
  // The optional array starts collapsed into a plain field with an Add button.
  const add = () => body().getAllByText("+ Add").at(-1) as HTMLElement;
  for (const v of ["a", "b", "c"]) {
    act(() => add().click());
    const inputs = document.querySelectorAll<HTMLInputElement>(
      "input[type=text]:not(#field-metadata-name):not(#field-spec-host)",
    );
    type(inputs[inputs.length - 1], v);
  }
  const values = () =>
    [
      ...document.querySelectorAll<HTMLInputElement>(
        "input[type=text]:not(#field-metadata-name):not(#field-spec-host)",
      ),
    ].map((i) => i.value);
  expect(values()).toEqual(["a", "b", "c"]);

  act(() => (body().getAllByTitle("Remove")[1] as HTMLElement).click());
  expect(values()).toEqual(["a", "c"]);
});

test("SchemaForm: removing an object-array item keeps the other items and their state", async () => {
  mountForm();
  await ready();
  const addRule = () => body().getByText("+ Add rules");
  act(() => addRule().click());
  act(() => addRule().click());
  act(() => addRule().click());

  const paths = () => [
    ...document.querySelectorAll<HTMLInputElement>("input[id$='-path']"),
  ];
  expect(paths()).toHaveLength(3);
  paths().forEach((p, i) => {
    type(p, `/p${i + 1}`);
  });

  // Collapse Item 3: its collapsed state belongs to that item, not to slot 3.
  act(() => (body().getByText("Item 3") as HTMLElement).click());
  expect(paths()).toHaveLength(2);

  // Remove Item 1.
  act(() => (body().getAllByText("Remove")[0] as HTMLElement).click());

  // The survivors are the old items 2 and 3: item 3 is still collapsed, so
  // only item 2's path input shows, with its value.
  expect(body().getAllByText(/^Item \d$/)).toHaveLength(2);
  expect(paths().map((p) => p.value)).toEqual(["/p2"]);
});
