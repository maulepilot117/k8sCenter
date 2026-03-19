import { define } from "@/utils.ts";
import UserManager from "@/islands/UserManager.tsx";

export default define.page(function UsersPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          User Management
        </h1>
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Manage local user accounts and credentials.
        </p>
      </div>
      <UserManager />
    </div>
  );
});
