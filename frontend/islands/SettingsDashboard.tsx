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
    <div class="flex flex-col h-full">
      {/* Page header */}
      <div class="flex items-center justify-between mb-5">
        <div>
          <h1 class="text-xl font-semibold tracking-tight text-text-primary">
            Settings
          </h1>
          <p class="text-xs text-text-muted mt-0.5">
            Configure application settings, clusters, users, and authentication
          </p>
        </div>
      </div>

      {/* Sub-navigation */}
      <SubNav tabs={SETTINGS_SECTION.tabs ?? []} currentPath={currentPath} />

      {/* Content area */}
      <div class="flex-1 min-h-0 overflow-auto">
        {resolveContent(currentPath)}
      </div>
    </div>
  );
}
