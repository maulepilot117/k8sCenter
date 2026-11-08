/** @jsxImportSource react */
import { expect, test } from "bun:test";
import type { K8sResource } from "@/lib/k8s-types.ts";
import { body, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { DeploymentOverview } = await import("./DeploymentOverview.tsx");
const { InfoGrid } = await import("./InfoGrid.tsx");
const { IngressOverview } = await import("./IngressOverview.tsx");
const { JobOverview } = await import("./JobOverview.tsx");
const { KeyValueTable } = await import("./KeyValueTable.tsx");
const { MetadataSection } = await import("./MetadataSection.tsx");
const { NamespaceOverview } = await import("./NamespaceOverview.tsx");
const { NetworkPolicyOverview } = await import("./NetworkPolicyOverview.tsx");

const asResource = (r: unknown) => r as K8sResource;

test("InfoGrid renders each label with its value", () => {
  const { container } = render(
    <InfoGrid
      items={[
        { label: "Phase", value: "Running" },
        { label: "Node", value: <b>node-1</b> },
      ]}
    />,
  );
  const v = body();
  expect(v.getByText("Phase")).toBeTruthy();
  expect(v.getByText("Running")).toBeTruthy();
  expect(v.getByText("Node")).toBeTruthy();
  expect(container.querySelector("b")?.textContent).toBe("node-1");
  expect((container.firstElementChild as HTMLElement).style.display).toBe(
    "grid",
  );
});

test("KeyValueTable sorts keys and renders nothing when empty", () => {
  const { container, unmount } = render(
    <KeyValueTable title="Labels" data={{ zeta: "1", alpha: "2" }} />,
  );
  expect(body().getByText("Labels")).toBeTruthy();
  const keys = [...container.querySelectorAll("tbody td:first-child")].map(
    (td) => td.textContent,
  );
  expect(keys).toEqual(["alpha", "zeta"]);
  expect(container.querySelector("td.font-mono")).not.toBeNull();
  expect(container.querySelector("h4")?.className).toContain("uppercase");
  unmount();
  const empty = render(<KeyValueTable title="Labels" data={{}} />);
  expect(empty.container.innerHTML).toBe("");
});

test("MetadataSection shows fields, owner link, finalizers, labels", () => {
  const { container } = render(
    <MetadataSection
      resource={asResource({
        metadata: {
          name: "web",
          namespace: "prod",
          uid: "uid-123",
          creationTimestamp: "2020-01-01T00:00:00Z",
          resourceVersion: "42",
          ownerReferences: [
            {
              kind: "ReplicaSet",
              name: "web-abc",
              uid: "o1",
              controller: true,
            },
            { kind: "Widget", name: "w", uid: "o2" },
          ],
          finalizers: ["kubernetes.io/pv-protection"],
          labels: { app: "web" },
          annotations: { note: "hi" },
        },
      })}
    />,
  );
  const v = body();
  expect(v.getByText("Metadata")).toBeTruthy();
  expect(v.getByText("prod")).toBeTruthy();
  expect(v.getByText("uid-123")).toBeTruthy();
  expect(v.getByText("42")).toBeTruthy();
  expect(v.getByText("Owner References")).toBeTruthy();
  expect(v.getByText("(controller)")).toBeTruthy();
  expect(v.getByText("kubernetes.io/pv-protection")).toBeTruthy();
  expect(v.getByText("Labels")).toBeTruthy();
  expect(v.getByText("Annotations")).toBeTruthy();
  expect(v.getByText("Widget/w").tagName).toBe("SPAN");
  const link = container.querySelector("a");
  expect(link?.textContent).toBe("ReplicaSet/web-abc");
  expect(link?.getAttribute("href")).toContain("/prod/web-abc");
  expect(link?.className).toContain("hover:underline");
});

test("NamespaceOverview shows phase badge, defaulting to Active", () => {
  const { container, unmount } = render(
    <NamespaceOverview
      resource={asResource({
        metadata: { name: "x" },
        status: { phase: "Terminating" },
      })}
    />,
  );
  expect(body().getByText("Summary")).toBeTruthy();
  const badge = body().getByText("Terminating");
  expect(badge.className).toContain("rounded-full");
  expect(container.querySelector(".space-y-4")).not.toBeNull();
  unmount();
  render(
    <NamespaceOverview resource={asResource({ metadata: { name: "x" } })} />,
  );
  expect(body().getByText("Active")).toBeTruthy();
});

test("JobOverview shows configuration, status and defaults", () => {
  render(
    <JobOverview
      resource={asResource({
        metadata: { name: "j" },
        spec: { completions: 5, backoffLimit: 2 },
        status: { active: 1, succeeded: 3, failed: 4 },
      })}
    />,
  );
  const v = body();
  expect(v.getByText("Configuration")).toBeTruthy();
  expect(v.getByText("Completions").parentElement?.textContent).toContain("5");
  expect(v.getByText("Parallelism").parentElement?.textContent).toContain("1");
  expect(v.getByText("Backoff Limit").parentElement?.textContent).toContain(
    "2",
  );
  expect(v.getByText("Succeeded").parentElement?.textContent).toContain("3");
  expect(v.getByText("Failed").parentElement?.textContent).toContain("4");
  expect(v.getByText("Start Time").parentElement?.textContent).toContain("-");
});

test("IngressOverview shows load balancer, rules and tls", () => {
  const { container } = render(
    <IngressOverview
      resource={asResource({
        metadata: { name: "i" },
        spec: {
          rules: [
            {
              host: "a.example.com",
              http: {
                paths: [
                  {
                    path: "/api",
                    backend: { service: { name: "api", port: { number: 80 } } },
                  },
                ],
              },
            },
          ],
          tls: [
            { hosts: ["a.example.com", "b.example.com"], secretName: "cert" },
          ],
        },
        status: { loadBalancer: { ingress: [{ ip: "10.0.0.1" }] } },
      })}
    />,
  );
  const v = body();
  expect(v.getByText("Load Balancer")).toBeTruthy();
  expect(v.getByText("IP")).toBeTruthy();
  expect(v.getByText("10.0.0.1")).toBeTruthy();
  expect(v.getByText("/api")).toBeTruthy();
  expect(v.getByText("api:80")).toBeTruthy();
  expect(v.getByText("a.example.com,b.example.com")).toBeTruthy();
  expect(v.getByText("cert")).toBeTruthy();
  expect(container.querySelectorAll("table").length).toBe(2);
  expect(container.querySelector("td.font-mono")).not.toBeNull();
});

test("NetworkPolicyOverview renders summary, selector and rules", () => {
  const { container } = render(
    <NetworkPolicyOverview
      resource={asResource({
        metadata: { name: "np" },
        spec: {
          podSelector: { matchLabels: { app: "db" } },
          policyTypes: ["Ingress", "Egress"],
          ingress: [
            {
              from: [
                { ipBlock: { cidr: "10.0.0.0/8", except: ["10.1.0.0/16"] } },
                { podSelector: { matchLabels: { role: "web" } } },
              ],
              ports: [{ port: 5432, protocol: "TCP" }],
            },
          ],
          egress: [],
        },
      })}
    />,
  );
  const v = body();
  expect(v.getByText("Ingress,Egress")).toBeTruthy();
  expect(v.getByText("Pod Selector")).toBeTruthy();
  expect(v.getByText("app")).toBeTruthy();
  expect(v.getByText(/Ingress Rules \(1\)/)).toBeTruthy();
  expect(v.getByText(/Egress Rules \(0\)/)).toBeTruthy();
  expect(v.getByText(/CIDR: 10\.0\.0\.0\/8/)).toBeTruthy();
  expect(v.getByText("(except 10.1.0.0/16)")).toBeTruthy();
  expect(v.getByText("role=web")).toBeTruthy();
  expect(v.getByText("5432/TCP")).toBeTruthy();
  expect(container.querySelector("p.italic")?.textContent).toBe(
    "No egress rules — all egress traffic is blocked",
  );
  expect(container.querySelector("span.text-success")?.textContent).toBe("→");
});

test("NetworkPolicyOverview with empty selector and no peers", () => {
  render(
    <NetworkPolicyOverview
      resource={asResource({
        metadata: { name: "np" },
        spec: { podSelector: {}, ingress: [{}] },
      })}
    />,
  );
  const v = body();
  expect(v.getByText("All pods in namespace (empty selector)")).toBeTruthy();
  expect(v.getByText("Any source")).toBeTruthy();
  expect(v.getByText("All ports")).toBeTruthy();
});

test("DeploymentOverview shows status, replicas, containers and bars", () => {
  const { container } = render(
    <DeploymentOverview
      resource={asResource({
        metadata: {
          name: "web",
          namespace: "prod",
          creationTimestamp: "2020-01-01T00:00:00Z",
          annotations: { "deployment.kubernetes.io/revision": "7" },
        },
        spec: {
          replicas: 3,
          strategy: { type: "RollingUpdate" },
          selector: { matchLabels: { app: "web", tier: "fe" } },
          template: {
            spec: {
              containers: [
                {
                  name: "app",
                  image: "nginx:1.25",
                  ports: [{ containerPort: 8080, protocol: "TCP" }],
                  resources: {
                    requests: { cpu: "250m", memory: "128Mi" },
                    limits: { cpu: "500m", memory: "256Mi" },
                  },
                },
              ],
            },
          },
        },
        status: {
          readyReplicas: 2,
          conditions: [{ type: "Available", status: "True" }],
        },
      })}
    />,
  );
  const v = body();
  expect(v.getByText("2/3 ready")).toBeTruthy();
  expect(v.getByText("prod")).toBeTruthy();
  expect(v.getByText("RollingUpdate")).toBeTruthy();
  expect(v.getByText("#7")).toBeTruthy();
  expect(v.getByText("app=web, tier=fe")).toBeTruthy();
  expect(v.getByText("View vulnerability scan results")).toBeTruthy();
  expect(v.getByText("Containers")).toBeTruthy();
  expect(v.getByText("Running")).toBeTruthy();
  expect(v.getByText("8080/TCP")).toBeTruthy();
  expect(v.getByText("CPU Request/Limit").nextElementSibling?.textContent).toBe(
    "250m / 500m",
  );
  const bars = [...container.querySelectorAll("div")].filter(
    (d) => d.style.height === "100%",
  ) as HTMLElement[];
  expect(bars.map((b) => b.style.width)).toEqual(["50%", "50%"]);
});

test("DeploymentOverview unavailable with no containers", () => {
  render(
    <DeploymentOverview
      resource={asResource({
        metadata: { name: "web" },
        spec: {},
        status: {
          conditions: [
            {
              type: "Available",
              status: "False",
              reason: "MinimumReplicasUnavailable",
            },
          ],
        },
      })}
    />,
  );
  const v = body();
  expect(v.getAllByText("MinimumReplicasUnavailable").length).toBeGreaterThan(
    0,
  );
  expect(v.getByText("0/0 ready")).toBeTruthy();
  expect(v.getByText("#-")).toBeTruthy();
  expect(v.queryByText("Containers")).toBeNull();
});
