import { define } from "@/utils.ts";
import AuditLogViewer from "@/islands/AuditLogViewer.tsx";

export default define.page(function AuditLogPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">
          Audit Log
        </h1>
        <p class="mt-1 text-sm text-text-secondary">
          View all write operations, authentication events, and secret accesses.
        </p>
      </div>
      <AuditLogViewer />
    </div>
  );
});
