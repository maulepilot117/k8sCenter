import { useSignal } from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiDelete, apiGet, apiPut } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { Toast, useToast } from "@/components/ui/Toast.tsx";
import type { LocalUser } from "@/lib/user-types.ts";

type DialogState =
  | { kind: "idle" }
  | { kind: "confirmDelete"; user: LocalUser }
  | { kind: "changePassword"; user: LocalUser; password: string };

export default function UserManager() {
  const { user: currentUser } = useAuth();

  const users = useSignal<LocalUser[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const dialog = useSignal<DialogState>({ kind: "idle" });
  const actionLoading = useSignal(false);
  const { toast, show: showToast } = useToast();

  const fetchUsers = useCallback(async () => {
    loading.value = true;
    error.value = null;
    try {
      const res = await apiGet<LocalUser[]>("/v1/users");
      users.value = Array.isArray(res.data) ? res.data : [];
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Failed to load users";
    } finally {
      loading.value = false;
    }
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchUsers();
  }, []);

  const handleDelete = async (user: LocalUser) => {
    if (actionLoading.value) return;
    actionLoading.value = true;
    // Optimistic removal
    const prev = users.value;
    users.value = users.value.filter((u) => u.id !== user.id);
    try {
      await apiDelete(`/v1/users/${user.id}`);
      showToast(`Deleted user "${user.username}"`, "success");
      dialog.value = { kind: "idle" };
    } catch (err) {
      // Restore on failure
      users.value = prev;
      const msg = err instanceof Error ? err.message : "Delete failed";
      showToast(msg, "error");
    } finally {
      actionLoading.value = false;
    }
  };

  const handleChangePassword = async (user: LocalUser, password: string) => {
    if (actionLoading.value) return;
    actionLoading.value = true;
    try {
      await apiPut(`/v1/users/${user.id}/password`, { password });
      showToast(`Password updated for "${user.username}"`, "success");
      dialog.value = { kind: "idle" };
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Update failed";
      showToast(msg, "error");
    } finally {
      actionLoading.value = false;
    }
  };

  const currentUserId = currentUser.value?.id;

  return (
    <div class="space-y-4">
      <Toast toast={toast} />

      {/* Error */}
      {error.value && (
        <div class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">
          {error.value}
        </div>
      )}

      {/* Table */}
      <div class="rounded-lg border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-800">
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-slate-200 dark:border-slate-700">
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400">
                  Username
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400">
                  Kubernetes Identity
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400">
                  Roles
                </th>
                <th class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
              {loading.value && users.value.length === 0
                ? (
                  <tr>
                    <td
                      colSpan={4}
                      class="px-4 py-12 text-center text-sm text-slate-400"
                    >
                      Loading users...
                    </td>
                  </tr>
                )
                : users.value.length === 0
                ? (
                  <tr>
                    <td
                      colSpan={4}
                      class="px-4 py-12 text-center text-sm text-slate-400"
                    >
                      No local users found
                    </td>
                  </tr>
                )
                : users.value.map((user) => {
                  const isSelf = user.id === currentUserId;
                  return (
                    <tr
                      key={user.id}
                      class="transition-colors hover:bg-slate-50 dark:hover:bg-slate-800/50"
                    >
                      <td class="px-4 py-3 text-slate-700 dark:text-slate-300">
                        <span class="font-medium">{user.username}</span>
                        {isSelf && (
                          <span class="ml-2 inline-flex items-center rounded-full bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">
                            you
                          </span>
                        )}
                      </td>
                      <td class="px-4 py-3 text-slate-500 dark:text-slate-400">
                        {user.k8sUsername}
                      </td>
                      <td class="px-4 py-3">
                        <div class="flex flex-wrap gap-1">
                          {user.roles.map((role) => (
                            <span
                              key={role}
                              class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
                                role === "admin"
                                  ? "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400"
                                  : "bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-400"
                              }`}
                            >
                              {role}
                            </span>
                          ))}
                        </div>
                      </td>
                      <td class="px-4 py-3 text-right">
                        <div class="flex items-center justify-end gap-2">
                          <button
                            type="button"
                            onClick={() => {
                              dialog.value = {
                                kind: "changePassword",
                                user,
                                password: "",
                              };
                            }}
                            class="rounded px-2 py-1 text-xs font-medium text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-700"
                          >
                            Change Password
                          </button>
                          <button
                            type="button"
                            disabled={isSelf}
                            onClick={() => {
                              dialog.value = {
                                kind: "confirmDelete",
                                user,
                              };
                            }}
                            class="rounded px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-40 dark:text-red-400 dark:hover:bg-red-900/20"
                            title={isSelf
                              ? "Cannot delete your own account"
                              : `Delete ${user.username}`}
                          >
                            Delete
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
            </tbody>
          </table>
        </div>
      </div>

      {/* Delete Confirm Dialog */}
      {dialog.value.kind === "confirmDelete" && (
        <ConfirmDialog
          title={`Delete ${dialog.value.user.username}`}
          message={`This will permanently delete the user "${dialog.value.user.username}" and revoke their access.`}
          confirmLabel="Delete"
          danger
          typeToConfirm={dialog.value.user.username}
          loading={actionLoading.value}
          onConfirm={() => {
            if (dialog.value.kind === "confirmDelete") {
              handleDelete(dialog.value.user);
            }
          }}
          onCancel={() => {
            dialog.value = { kind: "idle" };
          }}
        />
      )}

      {/* Change Password Dialog */}
      {dialog.value.kind === "changePassword" && (
        <PasswordDialog
          username={dialog.value.user.username}
          password={dialog.value.password}
          loading={actionLoading.value}
          onPasswordInput={(v) => {
            if (dialog.value.kind === "changePassword") {
              dialog.value = { ...dialog.value, password: v };
            }
          }}
          onConfirm={() => {
            if (dialog.value.kind === "changePassword") {
              handleChangePassword(dialog.value.user, dialog.value.password);
            }
          }}
          onCancel={() => {
            dialog.value = { kind: "idle" };
          }}
        />
      )}
    </div>
  );
}

/** Simple password change modal. */
function PasswordDialog({
  username,
  password,
  loading,
  onPasswordInput,
  onConfirm,
  onCancel,
}: {
  username: string;
  password: string;
  loading: boolean;
  onPasswordInput: (v: string) => void;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const isValid = password.length >= 8;
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    globalThis.addEventListener("keydown", handler);
    inputRef.current?.focus();
    return () => globalThis.removeEventListener("keydown", handler);
  }, [onCancel]);

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="password-dialog-title"
        class="w-full max-w-sm rounded-lg bg-white p-6 shadow-xl dark:bg-slate-800"
        onClick={(e) => e.stopPropagation()}
      >
        <h3
          id="password-dialog-title"
          class="text-lg font-semibold text-slate-900 dark:text-white"
        >
          Change Password for {username}
        </h3>
        <div class="mt-4">
          <label class="block text-sm text-slate-600 dark:text-slate-400">
            New Password
          </label>
          <input
            ref={inputRef}
            type="password"
            value={password}
            onInput={(e) =>
              onPasswordInput((e.target as HTMLInputElement).value)}
            class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            placeholder="Minimum 8 characters"
          />
          {password.length > 0 && !isValid && (
            <p class="mt-1 text-xs text-red-500">
              Password must be at least 8 characters
            </p>
          )}
        </div>
        <div class="mt-6 flex justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            class="rounded-md border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={!isValid || loading}
            onClick={onConfirm}
            class="rounded-md bg-brand px-4 py-2 text-sm font-medium text-white hover:bg-brand/90 disabled:opacity-50"
          >
            {loading ? "..." : "Update Password"}
          </button>
        </div>
      </div>
    </div>
  );
}
