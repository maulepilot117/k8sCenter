/** @jsxImportSource react */
import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: MonitoringStatus } = await import("./MonitoringStatus.tsx");

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

const status = (hasOperator: boolean) => ({
  prometheus: { available: true, url: "http://prom", lastChecked: "t1" },
  grafana: { available: false, lastChecked: "t2" },
  dashboards: { provisioned: true, count: 3 },
  hasOperator,
});

const json = (data: unknown) =>
  new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

test("shows the status cards, and Re-scan posts once and shows the new result", async () => {
  setAccessToken("t");
  const calls: string[] = [];
  let release: (() => void) | null = null;
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    calls.push(`${init?.method ?? "GET"} ${url}`);
    if (url.includes("/monitoring/rediscover")) {
      await new Promise<void>((r) => {
        release = r;
      });
      return json(status(true));
    }
    return json(status(false));
  }) as unknown as typeof globalThis.fetch;

  const view = render(<MonitoringStatus />);
  await settle();
  expect(view.container.textContent).toContain("http://prom");
  expect(view.container.textContent).toContain("Not detected");

  const button = body().getByRole("button", { name: "Re-scan Cluster" });
  fireEvent.click(button);
  await settle();
  expect(body().getByRole("button", { name: "Scanning..." })).toBeTruthy();
  fireEvent.click(body().getByRole("button", { name: "Scanning..." }));
  await settle();
  expect(calls.filter((c) => c.includes("rediscover")).length).toBe(1);

  await act(async () => {
    release?.();
  });
  await settle();
  expect(view.container.textContent).toContain("Detected (ServiceMonitor");
  expect(body().getByRole("button", { name: "Re-scan Cluster" })).toBeTruthy();
});
