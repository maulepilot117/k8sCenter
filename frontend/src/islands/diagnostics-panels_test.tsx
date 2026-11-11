import { expect, test } from "bun:test";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { default: DiagnosticChecklist } = await import(
  "./DiagnosticChecklist.tsx"
);
const { default: BlastRadiusPanel } = await import("./BlastRadiusPanel.tsx");

test("the checklist separates failed and warned checks from passed ones", () => {
  const { container } = render(
    <DiagnosticChecklist
      namespace="team-a"
      results={[
        {
          ruleName: "crash-loop",
          status: "fail",
          severity: "critical",
          message: "container restarts",
          links: [{ label: "View Logs", kind: "Pod", name: "web-1" }],
        },
        {
          ruleName: "no-limits",
          status: "warn",
          severity: "warning",
          message: "limits missing",
        },
        {
          ruleName: "image-pull",
          status: "pass",
          severity: "info",
          message: "images pulled",
        },
      ]}
    />,
  );
  const text = container.textContent ?? "";
  expect(text).toContain("Failed Checks (2)");
  expect(text).toContain("Passed Checks (1)");
  expect(text).toContain("✗");
  expect(text).toContain("⚠");
  expect(text).toContain("✓");
  expect(text).toContain("crash-loop");
  expect(text).toContain("limits missing");
  expect(text).toContain("image-pull");
  const link = container.querySelector("a");
  expect(link?.getAttribute("href")).toBe(
    "/observability/logs?namespace=team-a&pod=web-1",
  );
});

test("the checklist says so when nothing failed or passed", () => {
  const { container } = render(
    <DiagnosticChecklist namespace="team-a" results={[]} />,
  );
  expect(container.textContent).toContain("No issues found");
  expect(container.textContent).toContain("No checks passed");
});

test("the blast radius lists affected resources as links and shows empty states", () => {
  const { container } = render(
    <BlastRadiusPanel
      namespace="team-a"
      directlyAffected={[
        { kind: "Service", name: "web-svc", health: "failing", impact: "down" },
      ]}
      potentiallyAffected={[]}
    />,
  );
  const text = container.textContent ?? "";
  expect(text).toContain("Directly Affected (1)");
  expect(text).toContain("Potentially Affected (0)");
  expect(text).toContain("No affected resources");
  const link = container.querySelector("a");
  expect(link?.getAttribute("href")).toBe(
    "/networking/services/team-a/web-svc",
  );
  expect(link?.textContent).toContain("failing");
});
