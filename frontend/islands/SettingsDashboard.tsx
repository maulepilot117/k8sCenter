import { SETTINGS_SECTION } from "@/lib/constants.ts";
import SubNav from "@/islands/SubNav.tsx";
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
      {/* Page header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: "20px",
        }}
      >
        <div>
          <h1
            style={{
              fontSize: "20px",
              fontWeight: 600,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              margin: 0,
            }}
          >
            Settings
          </h1>
          <p
            style={{
              fontSize: "13px",
              color: "var(--text-muted)",
              marginTop: "2px",
              marginBottom: 0,
            }}
          >
            Configure application settings, clusters, users, and authentication
          </p>
        </div>
      </div>

      {/* Sub-navigation */}
      <SubNav tabs={SETTINGS_SECTION.tabs ?? []} currentPath={currentPath} />

      {/* Content area */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        {resolveContent(currentPath)}
      </div>
    </div>
  );
}
