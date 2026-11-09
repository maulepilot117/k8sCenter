/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { useSignal } from "@/src/lib/signal-hooks.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

interface UserFormState {
  username: string;
  password: string;
  confirmPassword: string;
  k8sUsername: string;
  k8sGroups: string;
  roles: string[];
  showAdvanced: boolean;
}

const STEPS: WizardStep[] = [
  { label: "Account", sub: "Username & password" },
  { label: "Review", sub: "Confirm & create" },
];

function initialState(): UserFormState {
  return {
    username: "",
    password: "",
    confirmPassword: "",
    k8sUsername: "",
    k8sGroups: "",
    roles: [],
    showAdvanced: false,
  };
}

export default function UserWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<UserFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [created, setCreated] = useState(false);
  const dirty = useSignal(false);
  const usernameRef = useRef<HTMLInputElement>(null);
  const formRef = useLatest(form);
  // A second click must be refused before the next render shows "submitting".
  const submittingRef = useRef(false);

  useEffect(() => {
    usernameRef.current?.focus();
  }, []);

  useDirtyGuard(dirty);

  const updateField = useCallback(
    (field: string, value: unknown) => {
      dirty.value = true;
      setForm((prev) => ({ ...prev, [field]: value }));
    },
    [dirty],
  );

  const validateStep = (step: number): boolean => {
    const f = form;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.username || !/^[a-zA-Z0-9][a-zA-Z0-9_.@-]*$/.test(f.username)) {
        errs.username =
          "Must start with alphanumeric, can contain letters, numbers, _, ., @, -";
      }
      if (f.username.length > 253) {
        errs.username = "Must be 253 characters or less";
      }
      if (f.password.length < 8 || f.password.length > 128) {
        errs.password = "Must be 8-128 characters";
      }
      if (f.password !== f.confirmPassword) {
        errs.confirmPassword = "Passwords do not match";
      }
      // Validate k8s identity (always, not just when advanced is open)
      const k8sUser = f.k8sUsername || f.username;
      if (k8sUser.startsWith("system:")) {
        errs.k8sUsername =
          "Cannot start with 'system:' (reserved by Kubernetes)";
      }
      if (f.k8sGroups) {
        const groups = f.k8sGroups
          .split(",")
          .map((g) => g.trim())
          .filter(Boolean);
        if (groups.includes("system:masters")) {
          errs.k8sGroups =
            "Cannot include 'system:masters' (bypasses all RBAC)";
        }
      }
    }

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const goNext = () => {
    if (!validateStep(currentStep)) return;
    setCurrentStep(currentStep + 1);
  };

  const goBack = () => {
    if (currentStep > 0) {
      setCurrentStep(currentStep - 1);
    }
  };

  const handleSubmit = async () => {
    if (submittingRef.current) return;
    submittingRef.current = true;
    setSubmitError(null);

    const f = formRef.current;
    const k8sGroups =
      f.showAdvanced && f.k8sGroups
        ? f.k8sGroups
            .split(",")
            .map((g) => g.trim())
            .filter(Boolean)
        : undefined;

    const payload: Record<string, unknown> = {
      username: f.username,
      password: f.password,
      roles: f.roles,
    };
    if (f.showAdvanced && f.k8sUsername) {
      payload.k8sUsername = f.k8sUsername;
    }
    if (k8sGroups && k8sGroups.length > 0) {
      payload.k8sGroups = k8sGroups;
    }

    try {
      await apiPost("/v1/users", payload);
      // The user exists now: leaving the page loses nothing.
      dirty.value = false;
      setCreated(true);
    } catch (err) {
      if (err instanceof Error) {
        setSubmitError(err.message);
      } else {
        setSubmitError("Failed to create user");
      }
    } finally {
      submittingRef.current = false;
    }
  };

  const f = form;
  const effectiveK8sUser = f.k8sUsername || f.username || "(same as username)";
  const effectiveK8sGroups =
    f.showAdvanced && f.k8sGroups ? f.k8sGroups : "system:authenticated";

  return (
    <WizardShell
      title="Create User"
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={goBack}
      onNext={created ? close : currentStep === 0 ? goNext : handleSubmit}
      nextLabel={
        created ? "Done" : currentStep === 0 ? "Continue" : "Create User"
      }
    >
      {created ? (
        <div className="mx-auto max-w-lg">
          <div className="rounded-lg border border-success bg-success-dim p-6">
            <div className="flex items-center gap-3">
              <svg
                className="h-6 w-6 text-success"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth="2"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M5 13l4 4L19 7"
                />
              </svg>
              <h2 className="text-lg font-semibold text-success">
                User Created
              </h2>
            </div>
            <p className="mt-2 text-sm text-success">
              User "{f.username}" has been created successfully. To grant
              permissions, create a Role Binding.
            </p>
            <div className="mt-4 flex gap-3">
              <a
                href="/rbac/clusterrolebindings/new"
                className="inline-flex items-center rounded-md bg-brand px-4 py-2 text-sm font-medium hover:bg-brand/90"
                style={{ color: "var(--bg-base)" }}
              >
                Create Role Binding
              </a>
              <button
                type="button"
                onClick={close}
                className="inline-flex items-center rounded-md border border-border-primary px-4 py-2 text-sm font-medium text-text-secondary hover:bg-hover"
              >
                Close
              </button>
            </div>
          </div>
        </div>
      ) : (
        <div className="mx-auto max-w-lg">
          {/* Step 1: Account */}
          {currentStep === 0 && (
            <div className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-text-secondary">
                  Username <span className="text-error">*</span>
                </label>
                <input
                  ref={usernameRef}
                  type="text"
                  value={f.username}
                  onChange={(e) =>
                    updateField("username", e.currentTarget.value)
                  }
                  className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
                  placeholder="e.g. john.doe"
                />
                {errors.username && (
                  <p className="mt-1 text-xs text-error">{errors.username}</p>
                )}
              </div>

              <div>
                <label className="block text-sm font-medium text-text-secondary">
                  Password <span className="text-error">*</span>
                </label>
                <input
                  type="password"
                  value={f.password}
                  onChange={(e) =>
                    updateField("password", e.currentTarget.value)
                  }
                  className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
                  placeholder="Minimum 8 characters"
                />
                {errors.password && (
                  <p className="mt-1 text-xs text-error">{errors.password}</p>
                )}
              </div>

              <div>
                <label className="block text-sm font-medium text-text-secondary">
                  Confirm Password <span className="text-error">*</span>
                </label>
                <input
                  type="password"
                  value={f.confirmPassword}
                  onChange={(e) =>
                    updateField("confirmPassword", e.currentTarget.value)
                  }
                  className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
                  placeholder="Re-enter password"
                />
                {errors.confirmPassword && (
                  <p className="mt-1 text-xs text-error">
                    {errors.confirmPassword}
                  </p>
                )}
              </div>

              {/* Admin role toggle */}
              <div className="flex items-center gap-3">
                <input
                  type="checkbox"
                  id="admin-role"
                  checked={f.roles.includes("admin")}
                  onChange={(e) => {
                    const checked = e.currentTarget.checked;
                    updateField("roles", checked ? ["admin"] : []);
                  }}
                  className="h-4 w-4 rounded border-border-primary text-brand focus:ring-brand border-border-primary"
                />
                <label
                  htmlFor="admin-role"
                  className="text-sm font-medium text-text-secondary"
                >
                  Admin role
                </label>
                <span className="text-xs text-text-muted">
                  Grants access to user management, settings, and audit logs
                </span>
              </div>

              {/* Collapsible Advanced section */}
              <div className="rounded-md border border-border-primary">
                <button
                  type="button"
                  onClick={() => updateField("showAdvanced", !f.showAdvanced)}
                  className="flex w-full items-center justify-between px-4 py-3 text-sm font-medium text-text-secondary hover:bg-surface text-text-muted /50"
                >
                  <span>Advanced: Kubernetes Identity</span>
                  <svg
                    className={`h-4 w-4 transition-transform ${
                      f.showAdvanced ? "rotate-180" : ""
                    }`}
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    strokeWidth="2"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      d="M19 9l-7 7-7-7"
                    />
                  </svg>
                </button>

                {f.showAdvanced && (
                  <div className="space-y-4 border-t border-border-primary px-4 py-4">
                    <div>
                      <label className="block text-sm font-medium text-text-secondary">
                        K8s Username
                      </label>
                      <input
                        type="text"
                        value={f.k8sUsername}
                        onChange={(e) =>
                          updateField("k8sUsername", e.currentTarget.value)
                        }
                        className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
                        placeholder={f.username || "Defaults to local username"}
                      />
                      <p className="mt-1 text-xs text-text-muted">
                        The username used for Kubernetes RBAC impersonation.
                        Defaults to the local username.
                      </p>
                      {errors.k8sUsername && (
                        <p className="mt-1 text-xs text-error">
                          {errors.k8sUsername}
                        </p>
                      )}
                    </div>

                    <div>
                      <label className="block text-sm font-medium text-text-secondary">
                        K8s Groups
                      </label>
                      <input
                        type="text"
                        value={f.k8sGroups}
                        onChange={(e) =>
                          updateField("k8sGroups", e.currentTarget.value)
                        }
                        className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
                        placeholder="system:authenticated"
                      />
                      <p className="mt-1 text-xs text-text-muted">
                        Comma-separated list of Kubernetes groups. Defaults to
                        "system:authenticated".
                      </p>
                      {errors.k8sGroups && (
                        <p className="mt-1 text-xs text-error">
                          {errors.k8sGroups}
                        </p>
                      )}
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* Step 2: Review */}
          {currentStep === 1 && (
            <div className="space-y-4">
              <div className="rounded-lg border border-border-primary bg-surface p-4">
                <h3 className="text-sm font-semibold text-text-secondary">
                  Summary
                </h3>
                <dl className="mt-3 space-y-2 text-sm">
                  <div className="flex justify-between">
                    <dt className="text-text-muted">Username</dt>
                    <dd className="font-medium text-text-primary">
                      {f.username}
                    </dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-text-muted">K8s Username</dt>
                    <dd className="font-medium text-text-primary">
                      {effectiveK8sUser}
                    </dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-text-muted">K8s Groups</dt>
                    <dd className="font-medium text-text-primary">
                      {effectiveK8sGroups}
                    </dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-text-muted">Roles</dt>
                    <dd className="font-medium text-text-primary">
                      {f.roles.length > 0 ? f.roles.join(",") : "none"}
                    </dd>
                  </div>
                </dl>
              </div>

              {submitError && <ErrorBanner message={submitError} />}
            </div>
          )}
        </div>
      )}
    </WizardShell>
  );
}
