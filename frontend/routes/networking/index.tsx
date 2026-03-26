import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import ResourceTable from "@/islands/ResourceTable.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";

export default define.page(function NetworkingPage(ctx) {
  const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;
  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div style={{ padding: "16px 20px 12px" }}>
        <h1
          style={{
            fontSize: "20px",
            fontWeight: 600,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          Networking
        </h1>
        <p
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            marginTop: "2px",
            margin: 0,
          }}
        >
          Manage Services, Ingresses, Network Policies, and Cilium resources
        </p>
      </div>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ResourceTable kind="services" title="Services" />
      </div>
    </div>
  );
});
