/** @jsxImportSource react */
import { afterAll, beforeEach, expect, test } from "bun:test";
import { useState } from "react";

/**
 * useNamespacedServices feeds the widget parameter dialog's service picker,
 * which must offer exactly the chosen namespace's services once they load
 * (dashboard-catalog.spec.ts "service choices follow the namespace").
 *
 * The probe mirrors the dialog's shape: the namespace lives in state that the
 * select's change writes, and the options are read while rendering.
 */
// Bun's own fetch, captured before happy-dom replaces it.
const realFetch = globalThis.fetch;

const { act, render, setupDom } = await import("@/src/test-utils/render.tsx");
setupDom({ browser: true });

const { useNamespacedServices } = await import("./use-services.ts");
const { setAccessToken } = await import("@/lib/api.ts");

afterAll(() => {
  globalThis.fetch = realFetch;
});

type Reply = { status?: number; names: string[] | null };
let replies: Record<string, Reply> = {};
/** Requests held back by the test; each resolves when its gate is called. */
let gates: Record<string, () => void> = {};
let requested: string[] = [];

beforeEach(() => {
  replies = {};
  gates = {};
  requested = [];
  setAccessToken("test-token");
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = String(input);
    const ns = /\/resources\/services\/([^?]+)/.exec(url)?.[1] ?? "";
    requested.push(ns);
    const reply = replies[ns] ?? { names: [] };
    if (gates[ns] === undefined) {
      await new Promise<void>((resolve) => {
        gates[ns] = resolve;
      });
    }
    const status = reply.status ?? 200;
    const body =
      status === 200
        ? {
            data: (reply.names ?? []).map((name) => ({ metadata: { name } })),
          }
        : { error: { code: status, message: "nope" } };
    return new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
});

let choose: (ns: string) => void = () => {};

function Picker() {
  const [namespace, setNamespace] = useState("");
  choose = setNamespace;
  const services = useNamespacedServices(namespace);
  return (
    <select data-testid="service" disabled={services.length === 0}>
      <option value="">Choose a service...</option>
      {services.map((name) => (
        <option key={name} value={name}>
          {name}
        </option>
      ))}
    </select>
  );
}

const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/** Lets a held request finish and everything it writes render. */
async function release(ns: string) {
  await act(async () => {
    for (let i = 0; i < 20 && gates[ns] === undefined; i++) await wait(5);
    gates[ns]?.();
    await wait(20);
  });
}

function offered(): string[] {
  // The property, not the attribute: Preact sets an option's value as a DOM
  // property.
  return [...document.querySelectorAll("option")]
    .map((o) => o.value)
    .filter((v) => v !== "");
}

async function mount() {
  await act(async () => {
    render(<Picker />);
  });
}

async function select(ns: string) {
  await act(async () => {
    choose(ns);
    await wait(5);
  });
}

test("a changed namespace offers that namespace's services once they load", async () => {
  replies = {
    prod: { names: ["checkout"] },
    staging: { names: ["preview"] },
  };
  await mount();

  await select("prod");
  await release("prod");
  expect(offered()).toEqual(["checkout"]);

  await select("staging");
  // The previous namespace's services are gone before the new list arrives.
  expect(offered()).toEqual([]);
  await release("staging");
  expect(offered()).toEqual(["preview"]);
});

test("a late reply for a namespace the user left is never shown", async () => {
  replies = {
    default: { names: ["api"] },
    staging: { names: ["preview"] },
  };
  await mount();

  await select("default");
  await release("default");
  expect(offered()).toEqual(["api"]);

  // Leave default for staging and come back before staging answers.
  await select("staging");
  await select("default");
  await release("default");
  await release("staging");
  expect(offered()).toEqual(["api"]);
});

test("a namespace the caller cannot list offers nothing and disables the field", async () => {
  replies = { forbidden: { status: 403, names: null } };
  await mount();

  await select("forbidden");
  await release("forbidden");
  expect(offered()).toEqual([]);
  expect(
    (document.querySelector("[data-testid=service]") as HTMLSelectElement)
      .disabled,
  ).toBe(true);
});

test("clearing the namespace offers nothing and makes no request", async () => {
  replies = { prod: { names: ["checkout"] } };
  await mount();

  await select("prod");
  await release("prod");
  expect(offered()).toEqual(["checkout"]);

  await select("");
  expect(offered()).toEqual([]);
  expect(requested).toEqual(["prod"]);
});
