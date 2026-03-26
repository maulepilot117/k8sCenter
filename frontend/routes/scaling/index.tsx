import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ScalingPage() {
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
          Scaling
        </h1>
        <p
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            marginTop: "2px",
            margin: 0,
          }}
        >
          Manage HorizontalPodAutoscalers and PodDisruptionBudgets
        </p>
      </div>
      <nav
        style={{
          display: "flex",
          alignItems: "stretch",
          gap: "0",
          borderBottom: "1px solid var(--border-subtle)",
          background: "var(--bg-surface)",
          paddingLeft: "16px",
          paddingRight: "16px",
          flexShrink: 0,
        }}
      >
        <a
          href="/scaling/hpas"
          style={{
            display: "flex",
            alignItems: "center",
            padding: "8px 12px",
            fontSize: "13px",
            fontWeight: 500,
            color: "var(--accent)",
            textDecoration: "none",
            borderBottom: "2px solid var(--accent)",
            whiteSpace: "nowrap",
          }}
        >
          HPAs
        </a>
        <a
          href="/scaling/pdbs"
          style={{
            display: "flex",
            alignItems: "center",
            padding: "8px 12px",
            fontSize: "13px",
            fontWeight: 400,
            color: "var(--text-muted)",
            textDecoration: "none",
            borderBottom: "2px solid transparent",
            whiteSpace: "nowrap",
          }}
        >
          PDBs
        </a>
      </nav>
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ResourceTable kind="horizontalpodautoscalers" title="HPAs" />
      </div>
    </div>
  );
});
