/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { body, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { ColorBadge } = await import("./ColorBadge.tsx");
const { ErrorBanner } = await import("./ErrorBanner.tsx");
const { StatusBadge: CertStatusBadge, ExpiryBadge } = await import(
  "./CertificateBadges.tsx"
);
const { ProtocolBadge } = await import("./GatewayBadges.tsx");
const { ToolBadge } = await import("./GitOpsBadges.tsx");
const { MeshBadge } = await import("./MeshBadges.tsx");
const { SeverityDot, SourceBadge } = await import("./NotifCenterBadges.tsx");
const { ProviderTypeBadge } = await import("./NotificationBadges.tsx");
const { BlockingBadge, EngineBadge } = await import("./PolicyBadges.tsx");
const { FixAvailableBadge, SeverityCount } = await import("./ScanBadges.tsx");

const COLOR_BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium";

function classOf(text: string): string {
  return body().getByText(text).className;
}

test("ColorBadge renders its label with its tint classes and color", () => {
  render(<ColorBadge label="Hello" color="red" />);
  const el = body().getByText("Hello");
  expect(el.className).toBe(COLOR_BADGE_CLASS);
  expect(el.style.color).toBe("red");
});

test("ErrorBanner renders the message in the danger banner", () => {
  render(<ErrorBanner message="Boom" />);
  expect(classOf("Boom")).toBe(
    "rounded-md border border-danger bg-danger-dim px-4 py-3 text-sm text-danger",
  );
});

test("certificate badges", () => {
  render(
    <>
      <CertStatusBadge status="Ready" />
      <ExpiryBadge />
    </>,
  );
  expect(classOf("Ready")).toBe(COLOR_BADGE_CLASS);
  expect(classOf("—")).toBe("text-xs text-text-muted");
});

test("ProtocolBadge keeps its pill and protocol colour classes", () => {
  render(<ProtocolBadge protocol="HTTPS" />);
  expect(classOf("HTTPS")).toBe(
    "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium text-success bg-success/10",
  );
});

test("ToolBadge, MeshBadge, EngineBadge, ProviderTypeBadge use ColorBadge", () => {
  render(
    <>
      <ToolBadge tool="argocd" />
      <MeshBadge mesh="istio" />
      <EngineBadge engine="kyverno" />
      <ProviderTypeBadge type="slack" />
    </>,
  );
  for (const text of ["Argo CD", "Istio", "kyverno", "slack"]) {
    expect(classOf(text)).toBe(COLOR_BADGE_CLASS);
  }
});

test("notification centre badges", () => {
  const { container } = render(
    <>
      <SourceBadge source="alert" />
      <SeverityDot severity="critical" read />
    </>,
  );
  const source = container.querySelector('span[class*="px-1"]') as HTMLElement;
  expect(source.className).toBe(
    "inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium",
  );
  const dot = container.querySelector("[data-read]") as HTMLElement;
  expect(dot.getAttribute("data-read")).toBe("true");
  expect(dot.style.width).toBe("8px");
});

test("policy and scan badges", () => {
  render(
    <>
      <BlockingBadge blocking />
      <SeverityCount label="critical" count={3} color="red" />
      <FixAvailableBadge fixedVersion="" />
    </>,
  );
  expect(classOf("Enforce")).toBe(
    "inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium",
  );
  expect(classOf("3")).toBe("font-bold");
  expect(classOf("—")).toBe("text-text-muted");
});
