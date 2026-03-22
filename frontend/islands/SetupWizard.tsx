import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Logo } from "@/components/ui/Logo.tsx";
import { getAccessToken, setAccessToken } from "@/lib/api.ts";

const STEPS = [
  { title: "Welcome" },
  { title: "Admin Account" },
  { title: "Monitoring" },
  { title: "Alerting" },
  { title: "Review" },
];

export default function SetupWizard() {
  const step = useSignal(0);
  const loading = useSignal(false);
  const error = useSignal("");

  // Form state
  const username = useSignal("");
  const password = useSignal("");
  const confirmPassword = useSignal("");
  const setupToken = useSignal("");

  const promUrl = useSignal("");
  const grafUrl = useSignal("");
  const grafToken = useSignal("");

  const smtpHost = useSignal("");
  const smtpPort = useSignal(587);
  const smtpFrom = useSignal("");
  const smtpUser = useSignal("");
  const smtpPass = useSignal("");

  // Track what was configured
  const adminCreated = useSignal(false);
  const monitoringConfigured = useSignal(false);
  const alertingConfigured = useSignal(false);

  const inputClass =
    "w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 dark:border-slate-600 dark:bg-slate-700 dark:text-white";

  const createAdmin = async () => {
    if (loading.value) return; // guard against double-click
    if (password.value !== confirmPassword.value) {
      error.value = "Passwords do not match";
      return;
    }
    if (password.value.length < 8) {
      error.value = "Password must be at least 8 characters";
      return;
    }

    loading.value = true;
    error.value = "";

    try {
      // Create admin
      const createRes = await fetch("/api/v1/setup/init", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Requested-With": "XMLHttpRequest",
        },
        body: JSON.stringify({
          username: username.value,
          password: password.value,
          setupToken: setupToken.value,
        }),
      });

      if (!createRes.ok) {
        const body = await createRes.json();
        throw new Error(body.error?.message ?? "Failed to create admin");
      }

      // Auto-login with the just-created credentials
      const loginRes = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Requested-With": "XMLHttpRequest",
        },
        credentials: "include",
        body: JSON.stringify({
          username: username.value,
          password: password.value,
        }),
      });

      if (!loginRes.ok) {
        throw new Error(
          "Admin created but auto-login failed. Please login manually.",
        );
      }

      const loginBody = await loginRes.json();
      if (loginBody.data?.accessToken) {
        setAccessToken(loginBody.data.accessToken);
      }

      adminCreated.value = true;
      step.value = 2;
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Setup failed";
    } finally {
      loading.value = false;
    }
  };

  const saveMonitoring = async () => {
    if (!promUrl.value && !grafUrl.value) {
      // Skip — nothing to save
      step.value = 3;
      return;
    }

    loading.value = true;
    error.value = "";

    try {
      const res = await fetch("/api/v1/settings", {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          "X-Requested-With": "XMLHttpRequest",
          "Authorization": `Bearer ${getAccessToken() ?? ""}`,
        },
        body: JSON.stringify({
          monitoringPrometheusUrl: promUrl.value || null,
          monitoringGrafanaUrl: grafUrl.value || null,
          monitoringGrafanaToken: grafToken.value || null,
        }),
      });

      if (!res.ok) {
        const body = await res.json();
        throw new Error(body.error?.message ?? "Failed to save");
      }

      monitoringConfigured.value = true;
      step.value = 3;
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Save failed";
    } finally {
      loading.value = false;
    }
  };

  const saveAlerting = async () => {
    if (!smtpHost.value) {
      step.value = 4;
      return;
    }

    loading.value = true;
    error.value = "";

    try {
      const res = await fetch("/api/v1/settings", {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          "X-Requested-With": "XMLHttpRequest",
          "Authorization": `Bearer ${getAccessToken() ?? ""}`,
        },
        body: JSON.stringify({
          alertingEnabled: true,
          alertingSmtpHost: smtpHost.value,
          alertingSmtpPort: smtpPort.value,
          alertingSmtpFrom: smtpFrom.value || null,
          alertingSmtpUsername: smtpUser.value || null,
          alertingSmtpPassword: smtpPass.value || null,
        }),
      });

      if (!res.ok) {
        const body = await res.json();
        throw new Error(body.error?.message ?? "Failed to save");
      }

      alertingConfigured.value = true;
      step.value = 4;
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Save failed";
    } finally {
      loading.value = false;
    }
  };

  if (!IS_BROWSER) return null;

  return (
    <div class="mx-auto max-w-2xl px-4 py-12">
      <WizardStepper
        steps={STEPS}
        currentStep={step.value}
        onStepClick={(s) => {
          if (s < step.value) step.value = s;
        }}
      />

      {error.value && (
        <div class="mb-4 rounded-md bg-red-50 px-4 py-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
          {error.value}
        </div>
      )}

      {/* Step 0: Welcome */}
      {step.value === 0 && (
        <div class="text-center">
          <div class="mx-auto mb-6 w-16 h-16">
            <Logo />
          </div>
          <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
            Welcome to k8sCenter
          </h1>
          <p class="mt-3 text-slate-600 dark:text-slate-400">
            Let's set up your Kubernetes management platform. This wizard will
            create your admin account and configure optional monitoring and
            alerting integrations.
          </p>
          <div class="mt-8">
            <Button
              variant="primary"
              onClick={() => {
                step.value = 1;
              }}
            >
              Get Started
            </Button>
          </div>
        </div>
      )}

      {/* Step 1: Admin Account */}
      {step.value === 1 && (
        <div class="space-y-4">
          <h2 class="text-lg font-semibold text-slate-900 dark:text-white">
            Create Admin Account
          </h2>
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              Username
            </label>
            <input
              type="text"
              value={username.value}
              onInput={(e) => {
                username.value = (e.target as HTMLInputElement).value;
              }}
              class={inputClass}
              autoFocus
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              Password
            </label>
            <input
              type="password"
              value={password.value}
              onInput={(e) => {
                password.value = (e.target as HTMLInputElement).value;
              }}
              class={inputClass}
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              Confirm Password
            </label>
            <input
              type="password"
              value={confirmPassword.value}
              onInput={(e) => {
                confirmPassword.value = (e.target as HTMLInputElement).value;
              }}
              class={inputClass}
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              Setup Token
              <span class="ml-1 text-xs text-slate-400">(if configured)</span>
            </label>
            <input
              type="password"
              value={setupToken.value}
              onInput={(e) => {
                setupToken.value = (e.target as HTMLInputElement).value;
              }}
              placeholder="Leave empty if not required"
              class={inputClass}
            />
          </div>
          <div class="flex justify-end pt-2">
            <Button
              variant="primary"
              onClick={createAdmin}
              loading={loading.value}
              disabled={!username.value || !password.value}
            >
              Create Account
            </Button>
          </div>
        </div>
      )}

      {/* Step 2: Monitoring */}
      {step.value === 2 && (
        <div class="space-y-4">
          <h2 class="text-lg font-semibold text-slate-900 dark:text-white">
            Monitoring (Optional)
          </h2>
          <p class="text-sm text-slate-500 dark:text-slate-400">
            Connect to your Prometheus and Grafana instances. You can configure
            this later from Settings.
          </p>
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              Prometheus URL
            </label>
            <input
              type="url"
              value={promUrl.value}
              onInput={(e) => {
                promUrl.value = (e.target as HTMLInputElement).value;
              }}
              placeholder="http://prometheus:9090"
              class={inputClass}
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              Grafana URL
            </label>
            <input
              type="url"
              value={grafUrl.value}
              onInput={(e) => {
                grafUrl.value = (e.target as HTMLInputElement).value;
              }}
              placeholder="http://grafana:3000"
              class={inputClass}
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              Grafana API Token
            </label>
            <input
              type="password"
              value={grafToken.value}
              onInput={(e) => {
                grafToken.value = (e.target as HTMLInputElement).value;
              }}
              class={inputClass}
            />
          </div>
          <div class="flex justify-end gap-3 pt-2">
            <Button
              variant="ghost"
              onClick={() => {
                step.value = 3;
              }}
            >
              Skip
            </Button>
            <Button
              variant="primary"
              onClick={saveMonitoring}
              loading={loading.value}
            >
              Save & Continue
            </Button>
          </div>
        </div>
      )}

      {/* Step 3: Alerting */}
      {step.value === 3 && (
        <div class="space-y-4">
          <h2 class="text-lg font-semibold text-slate-900 dark:text-white">
            Alerting (Optional)
          </h2>
          <p class="text-sm text-slate-500 dark:text-slate-400">
            Configure SMTP for email alerts. You can configure this later from
            Settings.
          </p>
          <div class="grid gap-4 sm:grid-cols-2">
            <div>
              <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
                SMTP Host
              </label>
              <input
                type="text"
                value={smtpHost.value}
                onInput={(e) => {
                  smtpHost.value = (e.target as HTMLInputElement).value;
                }}
                placeholder="smtp.example.com"
                class={inputClass}
              />
            </div>
            <div>
              <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
                SMTP Port
              </label>
              <input
                type="number"
                value={smtpPort.value}
                onInput={(e) => {
                  smtpPort.value =
                    parseInt((e.target as HTMLInputElement).value) || 587;
                }}
                class={inputClass}
              />
            </div>
            <div>
              <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
                From Address
              </label>
              <input
                type="email"
                value={smtpFrom.value}
                onInput={(e) => {
                  smtpFrom.value = (e.target as HTMLInputElement).value;
                }}
                placeholder="alerts@example.com"
                class={inputClass}
              />
            </div>
            <div>
              <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
                SMTP Username
              </label>
              <input
                type="text"
                value={smtpUser.value}
                onInput={(e) => {
                  smtpUser.value = (e.target as HTMLInputElement).value;
                }}
                class={inputClass}
              />
            </div>
            <div class="sm:col-span-2">
              <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
                SMTP Password
              </label>
              <input
                type="password"
                value={smtpPass.value}
                onInput={(e) => {
                  smtpPass.value = (e.target as HTMLInputElement).value;
                }}
                class={inputClass}
              />
            </div>
          </div>
          <div class="flex justify-end gap-3 pt-2">
            <Button
              variant="ghost"
              onClick={() => {
                step.value = 4;
              }}
            >
              Skip
            </Button>
            <Button
              variant="primary"
              onClick={saveAlerting}
              loading={loading.value}
            >
              Save & Continue
            </Button>
          </div>
        </div>
      )}

      {/* Step 4: Review */}
      {step.value === 4 && (
        <div class="space-y-4">
          <h2 class="text-lg font-semibold text-slate-900 dark:text-white">
            Setup Complete
          </h2>
          <div class="rounded-lg border border-slate-200 bg-slate-50 p-4 dark:border-slate-700 dark:bg-slate-800/50">
            <dl class="space-y-3 text-sm">
              <div class="flex justify-between">
                <dt class="text-slate-500 dark:text-slate-400">
                  Admin Account
                </dt>
                <dd class="font-medium text-green-600 dark:text-green-400">
                  {adminCreated.value ? `Created (${username.value})` : "—"}
                </dd>
              </div>
              <div class="flex justify-between">
                <dt class="text-slate-500 dark:text-slate-400">Monitoring</dt>
                <dd
                  class={`font-medium ${
                    monitoringConfigured.value
                      ? "text-green-600 dark:text-green-400"
                      : "text-slate-400"
                  }`}
                >
                  {monitoringConfigured.value ? "Configured" : "Skipped"}
                </dd>
              </div>
              <div class="flex justify-between">
                <dt class="text-slate-500 dark:text-slate-400">Alerting</dt>
                <dd
                  class={`font-medium ${
                    alertingConfigured.value
                      ? "text-green-600 dark:text-green-400"
                      : "text-slate-400"
                  }`}
                >
                  {alertingConfigured.value ? "Configured" : "Skipped"}
                </dd>
              </div>
            </dl>
          </div>
          <p class="text-sm text-slate-500 dark:text-slate-400">
            You can change any of these settings later from the Settings page.
          </p>
          <div class="flex justify-end pt-2">
            <Button
              variant="primary"
              onClick={() => {
                globalThis.location.href = "/";
              }}
            >
              Go to Dashboard
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
