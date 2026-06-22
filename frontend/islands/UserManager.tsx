import { useSignal } from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiDelete, apiGet, apiPut } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { showToast } from "@/islands/ToastProvider.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import type { Column, Row } from "@/components/ui/ResourceTable.tsx";
import { Button } from "@/components/ui/Button.tsx";
import type { LocalUser } from "@/lib/user-types.ts";
import UserWizard from "@/islands/UserWizard.tsx";

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
  const wizardOpen = useSignal(false);

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

  const columns: Column[] = [
    { key: "username", label: "Username", width: "2fr" },
    { key: "k8sIdentity", label: "Kubernetes Identity", width: "2fr" },
    { key: "roles", label: "Roles", width: "1.5fr" },
    { key: "actions", label: "", width: "180px", align: "right" },
  ];

  const buildRows = (): Row[] => {
    if (loading.value && users.value.length === 0) {
      return [{
        id: "__loading__",
        cells: {
          username: (
            <span style={{ color: "var(--text-muted)", fontSize: "13px" }}>
              Loading users…
            </span>
          ),
          k8sIdentity: null,
          roles: null,
          actions: null,
        },
      }];
    }
    if (users.value.length === 0) {
      return [{
        id: "__empty__",
        cells: {
          username: (
            <span style={{ color: "var(--text-muted)", fontSize: "13px" }}>
              No local users found
            </span>
          ),
          k8sIdentity: null,
          roles: null,
          actions: null,
        },
      }];
    }
    return users.value.map((user) => {
      const isSelf = user.id === currentUserId;
      return {
        id: user.id,
        cells: {
          username: (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "6px",
                fontSize: "13px",
                fontWeight: 500,
                color: "var(--text-primary)",
              }}
            >
              {user.username}
              {isSelf && (
                <span
                  style={{
                    borderRadius: "6px",
                    padding: "1px 6px",
                    fontSize: "11px",
                    fontWeight: 500,
                    background: "var(--accent-dim)",
                    color: "var(--accent)",
                  }}
                >
                  you
                </span>
              )}
            </div>
          ),
          k8sIdentity: (
            <span
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                fontFamily: "var(--font-mono)",
              }}
            >
              {user.k8sUsername}
            </span>
          ),
          roles: (
            <div style={{ display: "flex", flexWrap: "wrap", gap: "4px" }}>
              {user.roles.map((role) => (
                <span
                  key={role}
                  style={{
                    borderRadius: "6px",
                    padding: "1px 6px",
                    fontSize: "11px",
                    fontWeight: 500,
                    background: role === "admin"
                      ? "var(--warning-dim)"
                      : "var(--bg-elevated)",
                    color: role === "admin"
                      ? "var(--warning)"
                      : "var(--text-muted)",
                  }}
                >
                  {role}
                </span>
              ))}
            </div>
          ),
          actions: (
            <div
              style={{
                display: "flex",
                justifyContent: "flex-end",
                gap: "6px",
              }}
            >
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  dialog.value = {
                    kind: "changePassword",
                    user,
                    password: "",
                  };
                }}
              >
                Change Password
              </Button>
              <Button
                type="button"
                variant="danger"
                size="sm"
                disabled={isSelf}
                onClick={() => {
                  dialog.value = { kind: "confirmDelete", user };
                }}
              >
                Delete
              </Button>
            </div>
          ),
        },
      };
    });
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Create User button */}
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <button
          type="button"
          onClick={() => (wizardOpen.value = true)}
          class="inline-flex items-center rounded-md bg-brand px-4 py-2 text-sm font-medium hover:bg-brand/90"
          style={{ color: "var(--bg-base)" }}
        >
          Create User
        </button>
      </div>

      {/* Error */}
      {error.value && <ErrorBanner message={error.value} />}

      {/* Table */}
      <ResourceTable
        columns={columns}
        rows={buildRows()}
        chevron={false}
      />

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

      {/* Create User Wizard */}
      {wizardOpen.value && (
        <UserWizard
          onClose={() => {
            wizardOpen.value = false;
            fetchUsers();
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
      class="fixed inset-0 z-50 flex items-center justify-center glass-scrim"
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="password-dialog-title"
        class="w-full max-w-sm glass-elevated rounded-2xl p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <h3
          id="password-dialog-title"
          style={{
            fontSize: "17px",
            fontWeight: 650,
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          Change Password for {username}
        </h3>
        <div style={{ marginTop: "16px" }}>
          <label
            style={{
              display: "block",
              fontSize: "12.5px",
              fontWeight: 600,
              color: "var(--text-secondary)",
              marginBottom: "7px",
            }}
          >
            New Password
          </label>
          <input
            ref={inputRef}
            type="password"
            value={password}
            onInput={(e) =>
              onPasswordInput((e.target as HTMLInputElement).value)}
            style={{
              width: "100%",
              padding: "10px 12px",
              borderRadius: "9px",
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
              color: "var(--text-primary)",
              fontSize: "13.5px",
              outline: "none",
              boxSizing: "border-box",
            }}
            placeholder="Minimum 8 characters"
          />
          {password.length > 0 && !isValid && (
            <p
              style={{
                marginTop: "4px",
                fontSize: "12px",
                color: "var(--error)",
              }}
            >
              Password must be at least 8 characters
            </p>
          )}
        </div>
        <div
          style={{
            marginTop: "24px",
            display: "flex",
            justifyContent: "flex-end",
            gap: "8px",
          }}
        >
          <Button type="button" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="primary"
            disabled={!isValid || loading}
            onClick={onConfirm}
          >
            {loading ? "…" : "Update Password"}
          </Button>
        </div>
      </div>
    </div>
  );
}
