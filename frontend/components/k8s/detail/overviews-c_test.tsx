import { expect, test } from "bun:test";
import type { K8sResource } from "@/lib/k8s-types.ts";
import { body, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { NodeOverview } = await import("./NodeOverview.tsx");
const { PVCOverview } = await import("./PVCOverview.tsx");
const { PodOverview } = await import("./PodOverview.tsx");
const { RoleBindingOverview } = await import("./RoleBindingOverview.tsx");
const { RoleOverview } = await import("./RoleOverview.tsx");
const { RulesTable } = await import("./RulesTable.tsx");
const { StatefulSetOverview } = await import("./StatefulSetOverview.tsx");
const { VulnerabilityLink } = await import("./VulnerabilityLink.tsx");

const res = (o: unknown) => o as K8sResource;

function cells(container: Element): string[][] {
  return [...container.querySelectorAll("tbody tr")].map((tr) =>
    [...tr.querySelectorAll("td")].map((td) => td.textContent ?? ""),
  );
}

test("NodeOverview renders info, addresses, capacity, taints and cordon badge", () => {
  const { container } = render(
    <NodeOverview
      resource={res({
        metadata: { name: "n1" },
        spec: {
          unschedulable: true,
          taints: [{ key: "dedicated", value: "gpu", effect: "NoSchedule" }],
        },
        status: {
          nodeInfo: {
            kubeletVersion: "v1.35.0",
            osImage: "Talos",
            architecture: "amd64",
            containerRuntimeVersion: "containerd://2.0",
          },
          addresses: [{ type: "InternalIP", address: "10.0.0.5" }],
          capacity: { cpu: "8", memory: "32Gi" },
          allocatable: { cpu: "7800m", pods: "110" },
          conditions: [],
        },
      })}
    />,
  );
  const q = body();
  expect(q.getByText("System Info")).toBeTruthy();
  expect(q.getByText("v1.35.0")).toBeTruthy();
  expect(q.getByText("containerd://2.0")).toBeTruthy();
  expect(q.getByText("10.0.0.5").className).toContain("font-mono");
  expect(q.getByText("Cordoned (Unschedulable)").className).toContain(
    "bg-warning-dim",
  );
  expect(cells(container)).toEqual([
    ["cpu", "8", "7800m"],
    ["memory", "32Gi", "-"],
    ["pods", "-", "110"],
    ["dedicated", "gpu", "NoSchedule"],
  ]);
  expect(q.getByText("dedicated").className).toContain("text-accent");
});

test("NodeOverview with an empty node renders no sections", () => {
  const { container } = render(
    <NodeOverview resource={res({ metadata: { name: "n2" }, spec: {} })} />,
  );
  expect(container.textContent).toBe("");
  expect(container.firstElementChild?.className).toBe("space-y-4");
});

test("PVCOverview shows phase badge and fields, with defaults", () => {
  const { container } = render(
    <PVCOverview
      resource={res({
        metadata: { name: "data" },
        spec: {
          storageClassName: "fast",
          accessModes: ["ReadWriteOnce", "ReadOnlyMany"],
          resources: { requests: { storage: "10Gi" } },
          volumeName: "pv-1",
        },
        status: { phase: "Bound", capacity: { storage: "10Gi" } },
      })}
    />,
  );
  const q = body();
  const badge = q.getByText("Bound");
  expect(badge.className).toContain("ring-success/20");
  expect(q.getByText("fast")).toBeTruthy();
  expect(q.getByText("ReadWriteOnce,ReadOnlyMany")).toBeTruthy();
  expect(q.getByText("pv-1").className).toContain("font-mono");
  expect(container.textContent).toContain("Requested Capacity10Gi");
  expect(container.textContent).toContain("Actual Capacity10Gi");

  const bare = render(
    <PVCOverview resource={res({ metadata: { name: "x" } })} />,
  );
  expect(bare.container.textContent).toContain("Pending");
  expect(bare.container.textContent).toContain("Storage Class-");
});

test("PodOverview lists containers with state, ready and restarts plus vulnerability link", () => {
  const { container } = render(
    <PodOverview
      resource={res({
        metadata: { name: "web-1", namespace: "shop" },
        spec: {
          nodeName: "node-a",
          containers: [
            { name: "app", image: "nginx:1.27" },
            { name: "side", image: "envoy:1" },
            { name: "job", image: "busybox" },
          ],
        },
        status: {
          phase: "Running",
          containerStatuses: [
            {
              name: "app",
              ready: true,
              restartCount: 2,
              state: { running: {} },
            },
            {
              name: "side",
              ready: false,
              restartCount: 0,
              state: { waiting: { reason: "CrashLoopBackOff" } },
            },
          ],
        },
      })}
    />,
  );
  const q = body();
  expect(q.getByText("node-a")).toBeTruthy();
  expect(q.getByText("Always")).toBeTruthy();
  const phase = container.querySelector("dd span") as HTMLElement;
  expect(phase.textContent).toBe("Running");
  expect(phase.className).toContain("ring-success/20");
  const link = q.getByText("View vulnerability scan results", {
    exact: false,
  });
  expect(link.getAttribute("href")).toBe(
    "/security/vulnerabilities/shop/Pod/web-1",
  );
  const rows = [
    ...container.querySelectorAll("table")[0].querySelectorAll("tbody tr"),
  ].map((tr) => [...tr.querySelectorAll("td")].map((td) => td.textContent));
  expect(rows).toEqual([
    ["app", "nginx:1.27", "Running", "Yes", "2"],
    ["side", "envoy:1", "Waiting: CrashLoopBackOff", "No", "0"],
    ["job", "busybox", "-", "-", "0"],
  ]);
});

test("RoleBindingOverview links the role and lists subjects", () => {
  render(
    <RoleBindingOverview
      resource={res({
        metadata: { name: "rb", namespace: "dev" },
        roleRef: { kind: "Role", name: "editor" },
        subjects: [
          { kind: "User", name: "alice" },
          { kind: "ServiceAccount", name: "ci", namespace: "build" },
        ],
      })}
    />,
  );
  const q = body();
  const a = q.getByText("editor");
  expect(a.getAttribute("href")).toBe("/rbac/roles/dev/editor");
  expect(a.className).toContain("text-brand");
  expect(q.getByText("rbac.authorization.k8s.io")).toBeTruthy();
  expect(cells(document.body)).toEqual([
    ["User", "alice", "-"],
    ["ServiceAccount", "ci", "build"],
  ]);
});

test("RoleBindingOverview to a ClusterRole uses the cluster path; no roleRef shows dashes", () => {
  const a = render(
    <RoleBindingOverview
      resource={res({
        metadata: { name: "crb" },
        roleRef: { kind: "ClusterRole", name: "admin" },
      })}
    />,
  );
  expect(a.getByText("admin").getAttribute("href")).toBe(
    "/rbac/clusterroles/admin",
  );
  a.unmount();
  const b = render(
    <RoleBindingOverview resource={res({ metadata: { name: "x" } })} />,
  );
  expect(b.container.textContent).toContain("Kind-");
  expect(b.container.querySelector("a")).toBeNull();
  expect(b.container.textContent).not.toContain("Subjects");
});

test("RulesTable and RoleOverview render rules, wildcards and the empty state", () => {
  const t = render(
    <RulesTable
      rules={[
        {
          apiGroups: [""],
          resources: ["pods", "pods/log"],
          verbs: ["get", "list"],
        },
        { verbs: [] },
      ]}
    />,
  );
  expect(cells(t.container)).toEqual([
    ["*", "pods,pods/log", "get,list"],
    ["*", "*", "*"],
  ]);
  t.unmount();
  const e = render(<RulesTable rules={[]} />);
  expect(e.getByText("No rules defined").className).toContain("text-sm");
  e.unmount();
  const r = render(
    <RoleOverview
      resource={res({
        metadata: { name: "r" },
        rules: [
          { apiGroups: ["apps"], resources: ["deployments"], verbs: ["get"] },
        ],
      })}
    />,
  );
  expect(cells(r.container)).toEqual([["apps", "deployments", "get"]]);
});

test("StatefulSetOverview shows replicas, strategy, containers and selector", () => {
  const { container } = render(
    <StatefulSetOverview
      resource={res({
        metadata: { name: "db" },
        spec: {
          replicas: 3,
          serviceName: "db-headless",
          updateStrategy: {
            type: "RollingUpdate",
            rollingUpdate: { partition: 1 },
          },
          selector: { matchLabels: { app: "db" } },
          template: {
            spec: { containers: [{ name: "pg", image: "postgres:17" }] },
          },
        },
        status: { readyReplicas: 2, currentReplicas: 3, updatedReplicas: 1 },
      })}
    />,
  );
  const text = container.textContent ?? "";
  expect(text).toContain("Desired3");
  expect(text).toContain("Ready2");
  expect(text).toContain("Current3");
  expect(text).toContain("Updated1");
  expect(text).toContain("Service Namedb-headless");
  expect(text).toContain("Update StrategyRollingUpdate");
  expect(text).toContain("Partition1");
  expect(text).toContain("Selector");
  expect(text).toContain("app");
  expect(cells(container)[0]).toEqual(["pg", "postgres:17"]);
});

test("VulnerabilityLink encodes the path, and renders nothing without namespace or name", () => {
  const a = render(
    <VulnerabilityLink namespace="my ns" kind="Deployment" name="a/b" />,
  );
  const link = a.getByText("View vulnerability scan results", {
    exact: false,
  });
  expect(link.getAttribute("href")).toBe(
    "/security/vulnerabilities/my%20ns/Deployment/a%2Fb",
  );
  expect(link.className).toContain("hover:underline");
  expect(a.getByText("Security").className).toContain("font-semibold");
  a.unmount();
  const b = render(
    <VulnerabilityLink namespace={undefined} kind="Pod" name="x" />,
  );
  expect(b.container.innerHTML).toBe("");
  b.unmount();
  const c = render(
    <VulnerabilityLink namespace="ns" kind="Pod" name={undefined} />,
  );
  expect(c.container.innerHTML).toBe("");
});
