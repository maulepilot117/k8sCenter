import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";

export default define.page(function ToolsPage(ctx) {
  const section = DOMAIN_SECTIONS.find((s) => s.id === "tools")!;
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
          Tools
        </h1>
        <p
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            marginTop: "2px",
            margin: 0,
          }}
        >
          YAML tools, wizards, and cluster utilities
        </p>
      </div>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <div
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: "40px 20px",
        }}
      >
        <div style={{ textAlign: "center" }}>
          <p
            style={{
              fontSize: "15px",
              color: "var(--text-muted)",
              marginBottom: "16px",
            }}
          >
            Select a tool from the tabs above to get started.
          </p>
          <div style={{ display: "flex", gap: "12px", justifyContent: "center" }}>
            <a
              href="/tools/yaml-apply"
              style={{
                padding: "8px 16px",
                fontSize: "13px",
                fontWeight: 500,
                color: "white",
                background: "var(--accent)",
                borderRadius: "6px",
                textDecoration: "none",
              }}
            >
              YAML Apply
            </a>
            <a
              href="/tools/storageclass-wizard"
              style={{
                padding: "8px 16px",
                fontSize: "13px",
                fontWeight: 500,
                color: "var(--text-primary)",
                background: "var(--bg-elevated)",
                borderRadius: "6px",
                textDecoration: "none",
                border: "1px solid var(--border-subtle)",
              }}
            >
              StorageClass Wizard
            </a>
          </div>
        </div>
      </div>
    </div>
  );
});
