/** @jsxImportSource react */
import { expect, test } from "bun:test";
import type { DriftUnknownReason } from "@/lib/eso-types.ts";
import { body, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { DriftBadge, ProviderBadge, SourceBadge, StatusBadge } = await import(
  "@/components/eso/ESOBadges.tsx"
);
const { ESODriftIndicator } = await import(
  "@/components/eso/ESODriftIndicator.tsx"
);
const { ESONotDetected } = await import("@/components/eso/ESONotDetected.tsx");
const { default: BackendRefsTable } = await import(
  "@/components/gateway/BackendRefsTable.tsx"
);
const { default: ConditionsTable } = await import(
  "@/components/gateway/ConditionsTable.tsx"
);
const { default: ParentGatewaysTable } = await import(
  "@/components/gateway/ParentGatewaysTable.tsx"
);

test("StatusBadge and DriftBadge show the label in the lifecycle color", () => {
  const { getByText } = render(
    <>
      <StatusBadge status="SyncFailed" />
      <DriftBadge status="InSync" />
    </>,
  );
  expect((getByText("SyncFailed") as HTMLElement).style.color).toBe(
    "var(--error)",
  );
  expect((getByText("InSync") as HTMLElement).style.color).toBe(
    "var(--success)",
  );
});

test("SourceBadge labels the source and renders nothing without one", () => {
  const { container, rerender } = render(<SourceBadge source="secretstore" />);
  expect(body().getByText("Store").className).toContain("border-border-subtle");
  rerender(<SourceBadge />);
  expect(container.textContent).toBe("");
});

test("ProviderBadge shows the provider or a dash", () => {
  const { container, rerender } = render(<ProviderBadge provider="vault" />);
  expect(container.textContent).toBe("vault");
  rerender(<ProviderBadge provider="" />);
  const dash = container.querySelector("span");
  expect(dash?.textContent).toBe("—");
  expect(dash?.className).toContain("text-text-muted");
});

test("ESODriftIndicator explains each drift state", () => {
  const { container, rerender } = render(
    <ESODriftIndicator status="Drifted" />,
  );
  expect(container.textContent).toContain("Drifted");
  expect(container.textContent).toContain("Force sync rewrites it");

  rerender(<ESODriftIndicator status="InSync" />);
  expect(container.textContent).toContain("matches the hash ESO recorded");

  rerender(<ESODriftIndicator status="Unknown" />);
  expect(container.textContent).toBe("Unknown");

  const reason: DriftUnknownReason = "rbac_denied";
  rerender(<ESODriftIndicator status="Unknown" reason={reason} />);
  expect(container.textContent).toContain("get secret");

  rerender(
    <ESODriftIndicator
      status="Unknown"
      reason={"future" as unknown as DriftUnknownReason}
    />,
  );
  expect(container.textContent).toContain("Drift state is currently unknown.");
});

test("ESONotDetected links to the ESO chart in a new tab", () => {
  render(<ESONotDetected />);
  const a = body().getByText("ESO Helm chart") as HTMLAnchorElement;
  expect(a.getAttribute("href")).toBe(
    "https://external-secrets.io/latest/introduction/getting-started/",
  );
  expect(a.getAttribute("target")).toBe("_blank");
  expect(a.getAttribute("rel")).toBe("noopener noreferrer");
  expect(a.className).toContain("text-brand");
  body().getByText(/External Secrets Operator is not installed/);
});

test("BackendRefsTable links resolved Services and marks resolution", () => {
  const base = { group: "", namespace: "shop" };
  const { container } = render(
    <BackendRefsTable
      backendRefs={[
        {
          ...base,
          kind: "Service",
          name: "web",
          port: 80,
          weight: 5,
          resolved: true,
        },
        {
          ...base,
          kind: "Service",
          name: "gone",
          namespace: "",
          resolved: false,
        },
        { ...base, kind: "Other", name: "x", resolved: true },
      ]}
    />,
  );
  body().getByText("Backend References");
  const link = body().getByText("web") as HTMLAnchorElement;
  expect(link.getAttribute("href")).toBe("/networking/services/shop/web");
  expect(body().getByText("gone").tagName).toBe("SPAN");
  expect(body().getByText("x").tagName).toBe("SPAN");
  const rows = container.querySelectorAll("tbody tr");
  expect(rows.length).toBe(3);
  expect(rows[0].textContent).toContain("80");
  expect(rows[1].textContent).toContain("-");
  expect(body().getAllByText("Yes")[0].className).toBe("text-success");
  expect(body().getByText("No").className).toBe("text-danger");
});

test("BackendRefsTable renders nothing when empty", () => {
  const { container, rerender } = render(<BackendRefsTable backendRefs={[]} />);
  expect(container.textContent).toBe("");
  rerender(<BackendRefsTable />);
  expect(container.textContent).toBe("");
});

test("ConditionsTable colours by status and dashes empty cells", () => {
  const { container } = render(
    <ConditionsTable
      conditions={[
        {
          type: "Ready",
          status: "True",
          reason: "OK",
          message: "fine",
          lastTransitionTime: "2026-01-02T03:04:05Z",
        },
        { type: "Bad", status: "False", reason: "", message: "" },
        { type: "Meh", status: "Unknown", reason: "R", message: "m" },
      ]}
    />,
  );
  body().getByText("Conditions");
  expect(body().getByText("True").className).toBe("text-success");
  expect(body().getByText("False").className).toBe("text-danger");
  expect(body().getByText("Unknown").className).toBe("text-warning");
  const rows = container.querySelectorAll("tbody tr");
  expect(rows[0].textContent).toContain(
    `${new Date("2026-01-02T03:04:05Z").toLocaleDateString()} `,
  );
  expect(rows[1].querySelectorAll("td")[2].textContent).toBe("-");
  expect(rows[1].querySelectorAll("td")[4].textContent).toBe("-");
});

test("ConditionsTable renders nothing when empty", () => {
  const { container } = render(<ConditionsTable conditions={[]} />);
  expect(container.textContent).toBe("");
});

test("ParentGatewaysTable links Gateway parents only", () => {
  const { container } = render(
    <ParentGatewaysTable
      parentRefs={[
        {
          group: "",
          kind: "Gateway",
          name: "gw",
          namespace: "infra",
          sectionName: "http",
          status: "Accepted",
        },
        {
          group: "",
          kind: "Service",
          name: "svc",
          namespace: "infra",
          sectionName: "",
          status: "Rejected",
        },
        {
          group: "",
          kind: "Gateway",
          name: "nons",
          namespace: "",
          sectionName: "",
          status: "",
        },
      ]}
    />,
  );
  body().getByText("Parent Gateways");
  expect(body().getByText("gw").getAttribute("href")).toBe(
    "/networking/gateway-api/gateways/infra/gw",
  );
  expect(body().getByText("svc").tagName).toBe("SPAN");
  expect(body().getByText("nons").tagName).toBe("SPAN");
  expect(body().getByText("Accepted").className).toBe("text-success");
  expect(body().getByText("Rejected").className).toBe("text-danger");
  const last = container.querySelectorAll("tbody tr")[2].querySelectorAll("td");
  expect(last[3].textContent).toBe("-");
  expect(last[3].querySelector("span")?.className).toBe("text-text-muted");
});

test("ParentGatewaysTable renders nothing when empty", () => {
  const { container } = render(<ParentGatewaysTable />);
  expect(container.textContent).toBe("");
});
