/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { ClusterRoleBindingOverview } = await import(
  "./ClusterRoleBindingOverview.tsx"
);
const { ClusterRoleOverview } = await import("./ClusterRoleOverview.tsx");
const { ConditionsGrid } = await import("./ConditionsGrid.tsx");
const { ConditionsTable } = await import("./ConditionsTable.tsx");
const { ConfigMapOverview } = await import("./ConfigMapOverview.tsx");
const { ContainerResourcesTable } = await import(
  "./ContainerResourcesTable.tsx"
);
const { CronJobOverview } = await import("./CronJobOverview.tsx");
const { DaemonSetOverview } = await import("./DaemonSetOverview.tsx");

/** Partial resource fixtures: the panels only read the fields they show. */
function fx(o: object): never {
  return o as never;
}

const threeDaysAgo = new Date(Date.now() - 3 * 86400000).toISOString();

function texts(container: Element, selector: string): string[] {
  return Array.from(container.querySelectorAll(selector)).map(
    (e) => e.textContent ?? "",
  );
}

test("ClusterRoleBindingOverview shows role ref and subjects", () => {
  const resource = fx({
    metadata: { name: "admins" },
    roleRef: {
      kind: "ClusterRole",
      name: "cluster-admin",
      apiGroup: "rbac.authorization.k8s.io",
    },
    subjects: [{ kind: "User", name: "alice" }],
  });
  const { container } = render(
    <ClusterRoleBindingOverview resource={resource} />,
  );
  expect(container.textContent).toContain("Role Reference");
  expect(container.textContent).toContain("cluster-admin");
  expect(container.textContent).toContain("alice");
  expect(container.querySelector("a")?.className).toContain("text-brand");
  expect(texts(container, "th")).toContain("Kind");
});

test("ClusterRoleOverview lists rules", () => {
  const resource = fx({
    metadata: { name: "viewer" },
    rules: [{ apiGroups: [""], resources: ["pods"], verbs: ["get", "list"] }],
  });
  const { container } = render(<ClusterRoleOverview resource={resource} />);
  expect(texts(container, "th")).toEqual(["API Groups", "Resources", "Verbs"]);
  expect(container.textContent).toContain("pods");
  expect(container.textContent).toContain("list");
});

test("ClusterRoleOverview without rules says so", () => {
  const { container } = render(
    <ClusterRoleOverview resource={fx({ metadata: { name: "x" } })} />,
  );
  expect(container.textContent).toBe("No rules defined");
});

test("ConditionsGrid colours status and shows message fallback", () => {
  const { container } = render(
    <ConditionsGrid
      conditions={[
        {
          type: "Ready",
          status: "True",
          message: "all good",
          lastTransitionTime: threeDaysAgo,
        },
        { type: "Synced", status: "False", reason: "Drift" },
      ]}
    />,
  );
  expect(container.textContent).toContain("Conditions");
  expect(container.textContent).toContain("all good");
  expect(container.textContent).toContain("Drift");
  expect(container.textContent).toContain("3d");
  const statusSpans = Array.from(container.querySelectorAll("span")).filter(
    (s) => s.textContent === "True" || s.textContent === "False",
  ) as HTMLElement[];
  expect(statusSpans[0].style.color).toBe("var(--success)");
  expect(statusSpans[1].style.color).toBe("var(--error)");
});

test("ConditionsGrid rows highlight on hover", () => {
  const { container } = render(
    <ConditionsGrid conditions={[{ type: "Ready", status: "True" }]} />,
  );
  const row = Array.from(container.querySelectorAll("div")).find(
    (d) => (d as HTMLElement).style.display === "grid",
  ) as HTMLElement;
  row.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
  expect(row.style.background).toBe("var(--bg-elevated)");
  row.dispatchEvent(new MouseEvent("mouseout", { bubbles: true }));
  expect(row.style.background).toBe("");
});

test("ConditionsTable renders headers, badges and dashes", () => {
  const { container } = render(
    <ConditionsTable
      conditions={[
        { type: "Available", status: "True", reason: "Up", message: "ok" },
        { type: "Progressing", status: "False" },
      ]}
    />,
  );
  expect(texts(container, "th")).toEqual([
    "Type",
    "Status",
    "Reason",
    "Message",
    "Last Transition",
  ]);
  const badges = container.querySelectorAll("tbody span");
  expect(badges[0].className).toContain("bg-success/10");
  expect(badges[0].className).toContain("ring-1");
  expect(badges[1].className).toContain("bg-danger/10");
  const lastRow = container.querySelectorAll("tbody tr")[1];
  expect(texts(lastRow, "td").slice(2)).toEqual(["-", "-", "-"]);
});

test("ConditionsTable renders nothing when empty", () => {
  const { container } = render(<ConditionsTable conditions={[]} />);
  expect(container.innerHTML).toBe("");
});

test("ConfigMapOverview sorts keys and truncates long values", () => {
  const long = "x".repeat(250);
  const resource = fx({
    metadata: { name: "cm" },
    data: { zeta: "z", alpha: long },
  });
  const { container } = render(<ConfigMapOverview resource={resource} />);
  expect(container.textContent).toContain("Data (2 keys)");
  expect(texts(container, "tbody td.font-mono")).toEqual(["alpha", "zeta"]);
  const pres = texts(container, "pre");
  expect(pres[0]).toBe(`${"x".repeat(200)}...`);
  expect(pres[1]).toBe("z");
  expect(container.querySelector("pre")?.className).toContain(
    "whitespace-pre-wrap",
  );
});

test("ConfigMapOverview empty state and singular key", () => {
  const empty = render(
    <ConfigMapOverview resource={fx({ metadata: { name: "e" } })} />,
  );
  expect(empty.container.textContent).toContain("Data (0 keys)");
  expect(empty.container.textContent).toContain("No data keys.");
  const one = render(
    <ConfigMapOverview
      resource={fx({ metadata: { name: "o" }, data: { a: "1" } })}
    />,
  );
  expect(one.container.textContent).toContain("Data (1 key)");
});

test("ContainerResourcesTable shows requests and limits", () => {
  const { container } = render(
    <ContainerResourcesTable
      containers={[
        {
          name: "app",
          resources: {
            requests: { cpu: "100m", memory: "128Mi" },
            limits: { cpu: "500m" },
          },
        },
      ]}
    />,
  );
  expect(container.textContent).toContain("Resource Requests & Limits");
  expect(texts(container, "th")).toEqual([
    "Container",
    "CPU Request",
    "CPU Limit",
    "Memory Request",
    "Memory Limit",
  ]);
  expect(texts(container, "tbody td")).toEqual([
    "app",
    "100m",
    "500m",
    "128Mi",
    "-",
  ]);
});

test("ContainerResourcesTable renders nothing without specs", () => {
  const { container } = render(
    <ContainerResourcesTable containers={[{ name: "bare" }]} />,
  );
  expect(container.innerHTML).toBe("");
});

test("CronJobOverview shows schedule, suspend badge and active jobs", () => {
  const resource = fx({
    metadata: { name: "cj" },
    spec: { schedule: "*/5 * * * *", suspend: true },
    status: {
      lastScheduleTime: threeDaysAgo,
      active: [{ name: "cj-123" }],
    },
  });
  const { container } = render(<CronJobOverview resource={resource} />);
  expect(container.textContent).toContain("*/5 * * * *");
  expect(container.textContent).toContain("Allow");
  expect(container.textContent).toContain("3d ago");
  expect(container.textContent).toContain("Active Job References");
  const badges = Array.from(container.querySelectorAll("span"));
  const yes = badges.find((s) => s.textContent === "Yes");
  expect(yes?.className).toContain("bg-warning/10");
  const ref = badges.find((s) => s.textContent === "cj-123");
  expect(ref?.className).toContain("bg-accent-dim");
});

test("CronJobOverview not suspended, no status", () => {
  const resource = fx({
    metadata: { name: "cj" },
    spec: { schedule: "0 * * * *", concurrencyPolicy: "Forbid" },
  });
  const { container } = render(<CronJobOverview resource={resource} />);
  const no = Array.from(container.querySelectorAll("span")).find(
    (s) => s.textContent === "No",
  );
  expect(no?.className).toContain("bg-success/10");
  expect(container.textContent).toContain("Forbid");
  expect(container.textContent).not.toContain("Active Job References");
});

test("DaemonSetOverview shows counts, containers and selector", () => {
  const resource = fx({
    metadata: { name: "ds" },
    spec: {
      selector: { matchLabels: { app: "agent" } },
      template: {
        spec: {
          containers: [
            {
              name: "agent",
              image: "ghcr.io/x/agent:1.2",
              resources: { requests: { cpu: "50m" } },
            },
          ],
        },
      },
    },
    status: {
      desiredNumberScheduled: 5,
      currentNumberScheduled: 4,
      numberReady: 3,
      numberAvailable: 2,
    },
  });
  const { container } = render(<DaemonSetOverview resource={resource} />);
  expect(texts(container, "dd").slice(0, 4)).toEqual(["5", "4", "3", "2"]);
  expect(container.textContent).toContain("ghcr.io/x/agent:1.2");
  expect(container.textContent).toContain("Resource Requests & Limits");
  expect(container.textContent).toContain("Selector");
  expect(container.querySelector("div")?.className).toBe("space-y-4");
});
