import { afterAll, beforeAll, expect, setSystemTime, test } from "bun:test";
import { createElement, Fragment } from "react";
import { renderToString } from "react-dom/server";
import type { K8sResource } from "@/lib/k8s-types.ts";
import { RESOURCE_COLUMNS } from "@/lib/resource-columns.ts";

// Ages render from the clock, so pin it.
beforeAll(() => setSystemTime(new Date("2026-01-10T12:00:00Z")));
afterAll(() => setSystemTime());

const meta = {
  name: "web-1",
  namespace: "prod",
  uid: "u",
  creationTimestamp: "2026-01-08T08:00:00Z",
};

const fixtures: Record<string, unknown> = {
  pods: {
    metadata: meta,
    spec: { nodeName: "node-a", containers: [{}, {}] },
    status: {
      phase: "Running",
      containerStatuses: [
        { ready: true, restartCount: 2 },
        { ready: false, restartCount: 1 },
      ],
    },
  },
  deployments: {
    metadata: meta,
    spec: {
      replicas: 3,
      template: { spec: { containers: [{ image: "nginx:1.27" }] } },
    },
    status: { readyReplicas: 1, availableReplicas: 2 },
  },
  services: {
    metadata: meta,
    spec: {
      type: "NodePort",
      clusterIP: "10.0.0.1",
      ports: [{ port: 80, protocol: "TCP" }, { port: 443 }],
    },
  },
  events: {
    metadata: meta,
    type: "Warning",
    reason: "BackOff",
    involvedObject: { kind: "Pod", name: "web-1" },
    message: "Back-off restarting",
    count: 4,
    lastTimestamp: "2026-01-10T11:30:00Z",
  },
  networkpolicies: {
    metadata: meta,
    spec: {
      podSelector: { matchLabels: { app: "web", tier: "fe" } },
      policyTypes: ["Ingress", "Egress"],
      ingress: [{}],
      egress: [{}, {}],
    },
  },
};

// HTML captured from the cells before they were built with createElement.
const expected: Record<string, Record<string, string>> = {
  pods: {
    name: '<span style="display:inline-flex;align-items:center;gap:7px;"><span style="width:6px;height:6px;background:var(--success);" class="inline-block rounded-full "></span><span style="color:var(--accent);font-family:var(--font-mono, monospace);font-weight:500;font-size:13px;">web-1</span></span>',
    namespace:
      '<span style="font-family:var(--font-mono, monospace);font-size:11px;padding:2px 6px;background:var(--bg-base);border-radius:4px;color:var(--text-secondary);">prod</span>',
    status:
      '<span style="display:inline-flex;align-items:center;gap:5px;padding:3px 10px;border-radius:12px;font-size:11px;font-weight:500;background:var(--success-dim);color:var(--success);"><span style="width:6px;height:6px;border-radius:50%;background:var(--success);"></span>Running</span>',
    ready:
      '<div style="display:flex;align-items:center;gap:6px;"><div style="display:flex;gap:3px;"><div style="width:8px;height:8px;border-radius:2px;background:var(--success);"></div><div style="width:8px;height:8px;border-radius:2px;background:var(--error);"></div></div><span style="font-size:12px;font-family:var(--font-mono, monospace);color:var(--text-secondary);">1/2</span></div>',
    restarts:
      '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">3</span>',
    node: '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">node-a</span>',
    age: '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">2d</span>',
  },
  deployments: {
    name: '<span style="display:inline-flex;align-items:center;gap:7px;"><span style="width:6px;height:6px;background:var(--warning);" class="inline-block rounded-full "></span><span style="color:var(--accent);font-family:var(--font-mono, monospace);font-weight:500;font-size:13px;">web-1</span></span>',
    namespace:
      '<span style="font-family:var(--font-mono, monospace);font-size:11px;padding:2px 6px;background:var(--bg-base);border-radius:4px;color:var(--text-secondary);">prod</span>',
    status:
      '<span style="display:inline-flex;align-items:center;gap:5px;padding:3px 10px;border-radius:12px;font-size:11px;font-weight:500;background:var(--warning-dim);color:var(--warning);"><span style="width:6px;height:6px;border-radius:50%;background:var(--warning);"></span>Progressing</span>',
    replicas:
      '<div style="display:flex;align-items:center;gap:6px;"><div style="display:flex;gap:3px;"><div style="width:8px;height:8px;border-radius:2px;background:var(--success);"></div><div style="width:8px;height:8px;border-radius:2px;background:var(--warning);"></div><div style="width:8px;height:8px;border-radius:2px;background:var(--error);"></div></div><span style="font-size:12px;font-family:var(--font-mono, monospace);color:var(--text-secondary);">1/3</span></div>',
    image:
      '<span style="font-family:var(--font-mono, monospace);font-size:11px;color:var(--text-muted);max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;display:block;">nginx:1.27</span>',
    age: '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">2d</span>',
  },
  services: {
    name: '<span style="display:inline-flex;align-items:center;gap:7px;"><span style="width:6px;height:6px;background:var(--success);" class="inline-block rounded-full "></span><span style="color:var(--accent);font-family:var(--font-mono, monospace);font-weight:500;font-size:13px;">web-1</span></span>',
    namespace:
      '<span style="font-family:var(--font-mono, monospace);font-size:11px;padding:2px 6px;background:var(--bg-base);border-radius:4px;color:var(--text-secondary);">prod</span>',
    type: '<span style="display:inline-flex;align-items:center;gap:5px;padding:3px 10px;border-radius:12px;font-size:11px;font-weight:500;background:var(--success-dim);color:var(--success);"><span style="width:6px;height:6px;border-radius:50%;background:var(--success);"></span>NodePort</span>',
    clusterIP:
      '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">10.0.0.1</span>',
    ports:
      '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">80/TCP, 443/TCP</span>',
    age: '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">2d</span>',
  },
  events: {
    type: '<span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset bg-warning/10 text-warning ring-warning/20">Warning</span>',
    reason: "BackOff",
    object: "Pod/web-1",
    message: "Back-off restarting",
    count: "4",
    lastSeen: "30m",
  },
  networkpolicies: {
    name: '<span style="display:inline-flex;align-items:center;gap:7px;"><span style="width:6px;height:6px;background:var(--text-muted);" class="inline-block rounded-full "></span><span style="color:var(--accent);font-family:var(--font-mono, monospace);font-weight:500;font-size:13px;">web-1</span></span>',
    namespace:
      '<span style="font-family:var(--font-mono, monospace);font-size:11px;padding:2px 6px;background:var(--bg-base);border-radius:4px;color:var(--text-secondary);">prod</span>',
    podSelector:
      '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;display:block;">app=web, tier=fe</span>',
    policyTypes:
      '<span style="display:flex;gap:4px;"><span style="display:inline-flex;align-items:center;gap:5px;padding:3px 10px;border-radius:12px;font-size:11px;font-weight:500;background:var(--accent-dim);color:var(--accent);"><span style="width:6px;height:6px;border-radius:50%;background:var(--accent);"></span>Ingress</span><span style="display:inline-flex;align-items:center;gap:5px;padding:3px 10px;border-radius:12px;font-size:11px;font-weight:500;background:var(--warning-dim);color:var(--warning);"><span style="width:6px;height:6px;border-radius:50%;background:var(--warning);"></span>Egress</span></span>',
    rules:
      '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">3</span>',
    age: '<span style="font-family:var(--font-mono, monospace);font-size:12px;color:var(--text-secondary);">2d</span>',
  },
};

// The recorded markup came from another renderer, which writes a trailing ";"
// after the last style declaration and keeps attributes in source order. React
// drops the ";" and orders them differently. Same DOM either way, so compare
// each tag with its style trimmed and its attributes sorted.
function canonical(html: string): string {
  return html.replace(
    /<([a-z0-9]+)((?:\s+[a-z-]+="[^"]*")*)\s*>/g,
    (_m, tag, attrs) => {
      const list = [...String(attrs).matchAll(/([a-z-]+)="([^"]*)"/g)]
        .map(([, name, value]) =>
          name === "style"
            ? `style="${value.replace(/;$/, "")}"`
            : `${name}="${value}"`,
        )
        .sort();
      return `<${tag}${list.map((a) => ` ${a}`).join("")}>`;
    },
  );
}

for (const [kind, cells] of Object.entries(expected)) {
  test(`${kind} cells render the recorded markup`, () => {
    const item = fixtures[kind] as K8sResource;
    const columns = RESOURCE_COLUMNS[kind];
    expect(columns.map((c) => c.key)).toEqual(Object.keys(cells));
    for (const col of columns) {
      const html = renderToString(
        createElement(Fragment, null, col.render?.(item)),
      );
      expect(canonical(html)).toBe(canonical(cells[col.key]));
    }
  });
}
