import { describe, expect, test } from "bun:test";
import { type ReactElement, useState } from "react";
import type { EngineStatus } from "@/lib/policy-types.ts";
import type { EnvVarEntry, PortEntry } from "@/lib/wizard-types.ts";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { KeyValueListEditor } = await import(
  "@/components/ui/KeyValueListEditor.tsx"
);
const { ContainerForm } = await import("./ContainerForm.tsx");
const { DeploymentNetworkStep } = await import("./DeploymentNetworkStep.tsx");
const { ServicePortsStep } = await import("./ServicePortsStep.tsx");
const { PolicyConfigStep } = await import("./PolicyConfigStep.tsx");

/**
 * Every editable-row editor keys its rows through `useRowKeys` and calls
 * `removeAt(i)` before it tells the parent to remove row i. Two regressions
 * pass a callbacks-only test but break the editor: a position key (removing
 * the middle row hands its DOM to the row after it) and a content key
 * (typing remounts the input and drops focus). Each case below drives one
 * editor through both.
 */
interface Case<T> {
  name: string;
  /** Row `i` of a list, distinct per i. */
  row: (i: number) => T;
  /** The same row with its text changed, as a keystroke would leave it. */
  edited: (row: T) => T;
  /** The editor under test, rendering `rows` and reporting a new list. */
  ui: (rows: T[], setRows: (rows: T[]) => void) => ReactElement;
  /** One text input per row, in document order. */
  inputs: () => HTMLElement[];
  /** One remove control per row, in document order. */
  removes: () => HTMLElement[];
}

const noop = () => {};

function define<T>(c: Case<T>): Case<unknown> {
  return c as unknown as Case<unknown>;
}

/** The unlabelled remove button that sits beside a row's input. */
const siblingButtons = (inputs: HTMLElement[]) =>
  inputs.map((el) => el.parentElement?.querySelector("button") as HTMLElement);

const port = (i: number): PortEntry => ({
  containerPort: 80 + i,
  protocol: "TCP",
});
const envVar = (i: number): EnvVarEntry => ({
  name: `VAR_${i}`,
  value: "",
  configMapRef: "",
  secretRef: "",
  key: "",
});
const kv = (i: number) => ({ key: `k${i}`, value: `v${i}` });

const containerProps = {
  image: "nginx:1",
  command: "",
  args: "",
  requestCpu: "",
  requestMemory: "",
  limitCpu: "",
  limitMemory: "",
  errors: {} as Record<string, string>,
};

const netPort = (i: number) => ({
  name: `p${i}`,
  containerPort: 80 + i,
  protocol: "TCP",
});
const netEnv = (i: number) => ({
  name: `VAR_${i}`,
  type: "literal" as const,
  value: "",
  ref: "",
  key: "",
});
const servicePort = (i: number) => ({
  name: `p${i}`,
  port: 80 + i,
  targetPort: 8080 + i,
  protocol: "TCP",
  nodePort: 0,
});

const policyForm = (excludedNamespaces: string[], allowedAdd: string[]) => ({
  templateId: "restrict-capabilities",
  engine: "kyverno" as const,
  name: "p1",
  action: "Audit",
  targetKinds: ["Pod"],
  excludedNamespaces,
  description: "",
  params: { dropAll: true, allowedAdd },
});
const engineStatus = { detected: "kyverno", lastChecked: "" } as EngineStatus;

const cases: Case<unknown>[] = [
  define({
    name: "KeyValueListEditor",
    row: kv,
    edited: (r) => ({ ...r, key: `${r.key}x` }),
    ui: (rows, setRows) => (
      <KeyValueListEditor
        label="Labels"
        entries={rows}
        onUpdate={noop}
        onAdd={noop}
        onRemove={(i) => setRows(rows.filter((_, idx) => idx !== i))}
      />
    ),
    inputs: () => body().getAllByPlaceholderText("key"),
    removes: () => body().getAllByTitle("Remove labels"),
  }),
  define({
    name: "ContainerForm ports",
    row: port,
    edited: (r) => ({ ...r, containerPort: r.containerPort + 1000 }),
    ui: (rows, setRows) => (
      <ContainerForm
        {...containerProps}
        ports={rows}
        envVars={[]}
        onChange={(f, v) => f === "ports" && setRows(v as PortEntry[])}
      />
    ),
    inputs: () => body().getAllByPlaceholderText("80"),
    removes: () => body().getAllByTitle("Remove port"),
  }),
  define({
    name: "ContainerForm env vars",
    row: envVar,
    edited: (r) => ({ ...r, name: `${r.name}x` }),
    ui: (rows, setRows) => (
      <ContainerForm
        {...containerProps}
        ports={[]}
        envVars={rows}
        onChange={(f, v) => f === "envVars" && setRows(v as EnvVarEntry[])}
      />
    ),
    inputs: () => body().getAllByPlaceholderText("MY_VAR"),
    removes: () => body().getAllByTitle("Remove env var"),
  }),
  define({
    name: "ServicePortsStep ports",
    row: servicePort,
    edited: (r) => ({ ...r, name: `${r.name}x` }),
    ui: (rows, setRows) => (
      <ServicePortsStep
        ports={rows}
        selector={[]}
        serviceType="ClusterIP"
        errors={{}}
        onChange={(f, v) => f === "ports" && setRows(v as typeof rows)}
      />
    ),
    inputs: () => body().getAllByPlaceholderText("http"),
    removes: () => body().getAllByTitle("Remove port"),
  }),
  define({
    name: "ServicePortsStep selector",
    row: kv,
    edited: (r) => ({ ...r, key: `${r.key}x` }),
    ui: (rows, setRows) => (
      <ServicePortsStep
        ports={[]}
        selector={rows}
        serviceType="ClusterIP"
        errors={{}}
        onChange={(f, v) => f === "selector" && setRows(v as typeof rows)}
      />
    ),
    inputs: () => body().getAllByPlaceholderText("key"),
    removes: () => body().getAllByTitle("Remove pod selector"),
  }),
  define({
    name: "DeploymentNetworkStep ports",
    row: netPort,
    edited: (r) => ({ ...r, name: `${r.name}x` }),
    ui: (rows, setRows) => (
      <DeploymentNetworkStep
        ports={rows}
        envVars={[]}
        errors={{}}
        onChange={(f, v) => f === "ports" && setRows(v as typeof rows)}
      />
    ),
    inputs: () => body().getAllByPlaceholderText("http"),
    removes: () => body().getAllByTitle("Remove port"),
  }),
  define({
    name: "DeploymentNetworkStep env vars",
    row: netEnv,
    edited: (r) => ({ ...r, name: `${r.name}x` }),
    ui: (rows, setRows) => (
      <DeploymentNetworkStep
        ports={[]}
        envVars={rows}
        errors={{}}
        onChange={(f, v) => f === "envVars" && setRows(v as typeof rows)}
      />
    ),
    inputs: () => body().getAllByPlaceholderText("MY_VAR"),
    removes: () => body().getAllByTitle("Remove env var"),
  }),
  define({
    name: "PolicyConfigStep excluded namespaces",
    row: (i: number) => `ns-${i}`,
    edited: (r: string) => `${r}x`,
    ui: (rows: string[], setRows) => (
      <PolicyConfigStep
        form={policyForm(rows, [])}
        errors={{}}
        engineStatus={engineStatus}
        onUpdate={(f, v) =>
          f === "excludedNamespaces" && setRows(v as string[])
        }
        onUpdateParam={noop}
      />
    ),
    inputs: () => body().getAllByPlaceholderText("namespace"),
    removes: () => siblingButtons(body().getAllByPlaceholderText("namespace")),
  }),
  define({
    name: "PolicyConfigStep string-list param",
    row: (i: number) => `CAP_${i}`,
    edited: (r: string) => `${r}X`,
    ui: (rows: string[], setRows) => (
      <PolicyConfigStep
        form={policyForm([], rows)}
        errors={{}}
        engineStatus={engineStatus}
        onUpdate={noop}
        onUpdateParam={(k, v) => k === "allowedAdd" && setRows(v as string[])}
      />
    ),
    inputs: () => body().getAllByPlaceholderText(/^Value \d+$/),
    removes: () =>
      siblingButtons(body().getAllByPlaceholderText(/^Value \d+$/)),
  }),
];

/** Owns the rows and applies the editor's remove callback, as a wizard does. */
function Stateful({ c, initial }: { c: Case<unknown>; initial: unknown[] }) {
  const [rows, setRows] = useState(initial);
  return c.ui(rows, setRows);
}

describe.each(cases.map((c) => [c.name, c] as const))("%s rows", (_n, c) => {
  test("keeps row 0's input mounted while its content changes", () => {
    const rows = [c.row(0), c.row(1)];
    const view = render(c.ui(rows, noop));
    const first = c.inputs()[0];
    view.rerender(c.ui([c.edited(rows[0]), rows[1]], noop));
    expect(c.inputs()[0]).toBe(first);
  });

  test("removing the middle row keeps the survivors' DOM", () => {
    render(<Stateful c={c} initial={[c.row(0), c.row(1), c.row(2)]} />);
    const before = c.inputs();
    expect(before).toHaveLength(3);
    const [first, , last] = before;

    fireEvent.click(c.removes()[1]);

    const after = c.inputs();
    expect(after).toHaveLength(2);
    expect(after[0]).toBe(first);
    expect(after[1]).toBe(last);
  });
});
