/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { body, render, settle, setupDom } from "@/src/test-utils/render.tsx";

/**
 * useClusterNames reads each remote cluster's record from the registry once
 * for the component's life, skips the local cluster, prefers the display name
 * and leaves out a cluster whose record cannot be read.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { clusterLabel, useClusterNames } = await import("./cluster-label.ts");

let urls: string[] = [];
let records: Record<string, unknown> = {};

beforeEach(() => {
  urls = [];
  records = {};
  setAccessToken("t");
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    const id = decodeURIComponent(url.split("/").pop() ?? "");
    const record = records[id];
    if (!record) {
      return new Response(
        JSON.stringify({ error: { code: 404, message: "x" } }),
        {
          status: 404,
          headers: { "Content-Type": "application/json" },
        },
      );
    }
    return new Response(JSON.stringify({ data: record }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

function Probe({ ids }: { ids: (string | undefined)[] }) {
  const names = useClusterNames(ids);
  return (
    <ul>
      {ids.map((id) => (
        <li key={id ?? "none"}>{id ? clusterLabel(id, names) : "-"}</li>
      ))}
    </ul>
  );
}

test("names a remote cluster from its record, preferring the display name", async () => {
  records = {
    "c-1": { id: "c-1", name: "prod", displayName: "Production" },
    "c-2": { id: "c-2", name: "stage" },
  };
  render(<Probe ids={["c-1", "c-2"]} />);
  body().getByText("c-1");
  await settle();
  body().getByText("Production");
  body().getByText("stage");
});

test("the local cluster and empty ids are never fetched", async () => {
  render(<Probe ids={["local", undefined]} />);
  await settle();
  expect(urls).toEqual([]);
  body().getByText("the local cluster");
});

test("a cluster whose record cannot be read keeps its id as the label", async () => {
  render(<Probe ids={["c-9"]} />);
  await settle();
  expect(urls).toEqual(["/api/v1/clusters/c-9"]);
  body().getByText("c-9");
});

test("an id already read is not read again when another is added", async () => {
  records = {
    "c-1": { id: "c-1", name: "one" },
    "c-2": { id: "c-2", name: "two" },
  };
  const view = render(<Probe ids={["c-1"]} />);
  await settle();
  view.rerender(<Probe ids={["c-1", "c-2"]} />);
  await settle();
  expect(urls).toEqual(["/api/v1/clusters/c-1", "/api/v1/clusters/c-2"]);
  body().getByText("one");
  body().getByText("two");
});
