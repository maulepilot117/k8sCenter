import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import FluxProviders from "@/islands/FluxProviders.tsx";
import FluxAlerts from "@/islands/FluxAlerts.tsx";
import FluxReceivers from "@/islands/FluxReceivers.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "gitops")!;

function TabLink(
  { label, tab, active }: { label: string; tab: string; active: string },
) {
  const isActive = tab === active;
  return (
    <a
      href={`?tab=${tab}`}
      class={`px-4 py-2 text-sm font-medium -mb-px ${
        isActive ? "border-b-2" : ""
      }`}
      style={{
        color: isActive ? "var(--accent)" : "var(--text-secondary)",
        borderColor: isActive ? "var(--accent)" : "transparent",
      }}
    >
      {label}
    </a>
  );
}

export default define.page(function NotificationsPage(ctx) {
  const tab = ctx.url.searchParams.get("tab") ?? "providers";
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      {/* Sub-tab bar */}
      <div
        class="flex gap-1 px-6 pt-4 border-b"
        style={{ borderColor: "var(--border)" }}
      >
        <TabLink label="Providers" tab="providers" active={tab} />
        <TabLink label="Alerts" tab="alerts" active={tab} />
        <TabLink label="Receivers" tab="receivers" active={tab} />
      </div>
      {/* Tab content */}
      {tab === "providers" && <FluxProviders />}
      {tab === "alerts" && <FluxAlerts />}
      {tab === "receivers" && <FluxReceivers />}
    </>
  );
});
