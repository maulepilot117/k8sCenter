import SettingsPage from "@/islands/SettingsPage.tsx";
import ClusterManager from "@/islands/ClusterManager.tsx";
import UserManager from "@/islands/UserManager.tsx";
import AuthSettings from "@/islands/AuthSettings.tsx";
import AuditLogViewer from "@/islands/AuditLogViewer.tsx";

function resolveContent(currentPath: string) {
  const path = currentPath.replace(/\/$/, "");

  if (path === "/settings/clusters") return <ClusterManager />;
  if (path === "/settings/users") return <UserManager />;
  if (path === "/settings/auth") return <AuthSettings />;
  if (path === "/settings/audit") return <AuditLogViewer />;

  // Default: general settings
  return <SettingsPage />;
}

export default function SettingsDashboard(
  { currentPath }: { currentPath: string },
) {
  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Page header — 24/700/-0.02em per archetype spec */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-end",
          justifyContent: "space-between",
          marginBottom: "24px",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
            }}
          >
            Settings
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Configure application settings, clusters, users, and authentication
          </p>
        </div>
      </div>

      {/* Content area */}
      <div style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
        {resolveContent(currentPath)}
      </div>
    </div>
  );
}
