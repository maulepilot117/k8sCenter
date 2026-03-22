import { useSignal } from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface UserFormState {
  username: string;
  password: string;
  confirmPassword: string;
  k8sUsername: string;
  k8sGroups: string;
  roles: string[];
  showAdvanced: boolean;
}

const STEPS = [
  { title: "Account" },
  { title: "Review" },
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

export default function UserWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<UserFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const submitting = useSignal(false);
  const submitError = useSignal<string | null>(null);
  const created = useSignal(false);
  const dirty = useSignal(false);
  const usernameRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    usernameRef.current?.focus();
  }, []);

  // beforeunload guard
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty.value && !created.value) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const validateStep = (step: number): boolean => {
    const f = form.value;
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

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const goNext = () => {
    if (!validateStep(currentStep.value)) return;
    currentStep.value = currentStep.value + 1;
  };

  const goBack = () => {
    if (currentStep.value > 0) {
      currentStep.value = currentStep.value - 1;
    }
  };

  const handleSubmit = async () => {
    if (submitting.value) return;
    submitting.value = true;
    submitError.value = null;

    const f = form.value;
    const k8sGroups = f.showAdvanced && f.k8sGroups
      ? f.k8sGroups.split(",").map((g) => g.trim()).filter(Boolean)
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
      created.value = true;
    } catch (err) {
      if (err instanceof Error) {
        submitError.value = err.message;
      } else {
        submitError.value = "Failed to create user";
      }
    } finally {
      submitting.value = false;
    }
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  // Success state
  if (created.value) {
    return (
      <div class="p-6">
        <div class="mx-auto max-w-lg">
          <div class="rounded-lg border border-green-200 bg-green-50 p-6 dark:border-green-800 dark:bg-green-900/20">
            <div class="flex items-center gap-3">
              <svg
                class="h-6 w-6 text-green-600 dark:text-green-400"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                stroke-width="2"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M5 13l4 4L19 7"
                />
              </svg>
              <h2 class="text-lg font-semibold text-green-800 dark:text-green-300">
                User Created
              </h2>
            </div>
            <p class="mt-2 text-sm text-green-700 dark:text-green-400">
              User "{form.value.username}" has been created successfully. To
              grant permissions, create a Role Binding.
            </p>
            <div class="mt-4 flex gap-3">
              <a
                href="/rbac/clusterrolebindings/new"
                class="inline-flex items-center rounded-md bg-brand px-4 py-2 text-sm font-medium text-white hover:bg-brand/90"
              >
                Create Role Binding
              </a>
              <a
                href="/settings/users"
                class="inline-flex items-center rounded-md border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700"
              >
                Back to Users
              </a>
            </div>
          </div>
        </div>
      </div>
    );
  }

  const f = form.value;
  const effectiveK8sUser = f.k8sUsername || f.username || "(same as username)";
  const effectiveK8sGroups = f.showAdvanced && f.k8sGroups
    ? f.k8sGroups
    : "system:authenticated";

  return (
    <div class="p-6">
      <div class="mb-6 flex items-center justify-between">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create User
        </h1>
        <a
          href="/settings/users"
          class="text-sm text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
        >
          Cancel
        </a>
      </div>

      <WizardStepper
        steps={STEPS}
        currentStep={currentStep.value}
        onStepClick={(step) => {
          if (step < currentStep.value) currentStep.value = step;
        }}
      />

      <div class="mx-auto mt-6 max-w-lg">
        {/* Step 1: Account */}
        {currentStep.value === 0 && (
          <div class="space-y-4">
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Username <span class="text-red-500">*</span>
              </label>
              <input
                ref={usernameRef}
                type="text"
                value={f.username}
                onInput={(e) =>
                  updateField(
                    "username",
                    (e.target as HTMLInputElement).value,
                  )}
                class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                placeholder="e.g. john.doe"
              />
              {errors.value.username && (
                <p class="mt-1 text-xs text-red-500">{errors.value.username}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Password <span class="text-red-500">*</span>
              </label>
              <input
                type="password"
                value={f.password}
                onInput={(e) =>
                  updateField(
                    "password",
                    (e.target as HTMLInputElement).value,
                  )}
                class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                placeholder="Minimum 8 characters"
              />
              {errors.value.password && (
                <p class="mt-1 text-xs text-red-500">{errors.value.password}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Confirm Password <span class="text-red-500">*</span>
              </label>
              <input
                type="password"
                value={f.confirmPassword}
                onInput={(e) =>
                  updateField(
                    "confirmPassword",
                    (e.target as HTMLInputElement).value,
                  )}
                class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                placeholder="Re-enter password"
              />
              {errors.value.confirmPassword && (
                <p class="mt-1 text-xs text-red-500">
                  {errors.value.confirmPassword}
                </p>
              )}
            </div>

            {/* Admin role toggle */}
            <div class="flex items-center gap-3">
              <input
                type="checkbox"
                id="admin-role"
                checked={f.roles.includes("admin")}
                onChange={(e) => {
                  const checked = (e.target as HTMLInputElement).checked;
                  updateField(
                    "roles",
                    checked ? ["admin"] : [],
                  );
                }}
                class="h-4 w-4 rounded border-slate-300 text-brand focus:ring-brand dark:border-slate-600"
              />
              <label
                for="admin-role"
                class="text-sm font-medium text-slate-700 dark:text-slate-300"
              >
                Admin role
              </label>
              <span class="text-xs text-slate-400">
                Grants access to user management, settings, and audit logs
              </span>
            </div>

            {/* Collapsible Advanced section */}
            <div class="rounded-md border border-slate-200 dark:border-slate-700">
              <button
                type="button"
                onClick={() => updateField("showAdvanced", !f.showAdvanced)}
                class="flex w-full items-center justify-between px-4 py-3 text-sm font-medium text-slate-600 hover:bg-slate-50 dark:text-slate-400 dark:hover:bg-slate-800/50"
              >
                <span>Advanced: Kubernetes Identity</span>
                <svg
                  class={`h-4 w-4 transition-transform ${
                    f.showAdvanced ? "rotate-180" : ""
                  }`}
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  stroke-width="2"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M19 9l-7 7-7-7"
                  />
                </svg>
              </button>

              {f.showAdvanced && (
                <div class="space-y-4 border-t border-slate-200 px-4 py-4 dark:border-slate-700">
                  <div>
                    <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                      K8s Username
                    </label>
                    <input
                      type="text"
                      value={f.k8sUsername}
                      onInput={(e) =>
                        updateField(
                          "k8sUsername",
                          (e.target as HTMLInputElement).value,
                        )}
                      class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                      placeholder={f.username || "Defaults to local username"}
                    />
                    <p class="mt-1 text-xs text-slate-400">
                      The username used for Kubernetes RBAC impersonation.
                      Defaults to the local username.
                    </p>
                    {errors.value.k8sUsername && (
                      <p class="mt-1 text-xs text-red-500">
                        {errors.value.k8sUsername}
                      </p>
                    )}
                  </div>

                  <div>
                    <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                      K8s Groups
                    </label>
                    <input
                      type="text"
                      value={f.k8sGroups}
                      onInput={(e) =>
                        updateField(
                          "k8sGroups",
                          (e.target as HTMLInputElement).value,
                        )}
                      class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                      placeholder="system:authenticated"
                    />
                    <p class="mt-1 text-xs text-slate-400">
                      Comma-separated list of Kubernetes groups. Defaults to
                      "system:authenticated".
                    </p>
                    {errors.value.k8sGroups && (
                      <p class="mt-1 text-xs text-red-500">
                        {errors.value.k8sGroups}
                      </p>
                    )}
                  </div>
                </div>
              )}
            </div>
          </div>
        )}

        {/* Step 2: Review */}
        {currentStep.value === 1 && (
          <div class="space-y-4">
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-4 dark:border-slate-700 dark:bg-slate-800/50">
              <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-300">
                Summary
              </h3>
              <dl class="mt-3 space-y-2 text-sm">
                <div class="flex justify-between">
                  <dt class="text-slate-500 dark:text-slate-400">Username</dt>
                  <dd class="font-medium text-slate-900 dark:text-white">
                    {f.username}
                  </dd>
                </div>
                <div class="flex justify-between">
                  <dt class="text-slate-500 dark:text-slate-400">
                    K8s Username
                  </dt>
                  <dd class="font-medium text-slate-900 dark:text-white">
                    {effectiveK8sUser}
                  </dd>
                </div>
                <div class="flex justify-between">
                  <dt class="text-slate-500 dark:text-slate-400">K8s Groups</dt>
                  <dd class="font-medium text-slate-900 dark:text-white">
                    {effectiveK8sGroups}
                  </dd>
                </div>
                <div class="flex justify-between">
                  <dt class="text-slate-500 dark:text-slate-400">Roles</dt>
                  <dd class="font-medium text-slate-900 dark:text-white">
                    {f.roles.length > 0 ? f.roles.join(", ") : "none"}
                  </dd>
                </div>
              </dl>
            </div>

            {submitError.value && (
              <div class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">
                {submitError.value}
              </div>
            )}
          </div>
        )}

        {/* Navigation */}
        <div class="mt-8 flex justify-between">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          {currentStep.value === 0
            ? (
              <Button variant="primary" onClick={goNext}>
                Next
              </Button>
            )
            : (
              <Button
                variant="primary"
                onClick={handleSubmit}
                loading={submitting.value}
                disabled={submitting.value}
              >
                Create User
              </Button>
            )}
        </div>
      </div>
    </div>
  );
}
