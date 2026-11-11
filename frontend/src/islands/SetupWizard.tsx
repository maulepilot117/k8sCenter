import { useRef, useState } from "react";
import { AlertingFields } from "@/components/settings/AlertingFields.tsx";
import { MonitoringFields } from "@/components/settings/MonitoringFields.tsx";
import { settingsInputClass } from "@/components/settings/shared.ts";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import StepRail from "@/components/wizard/StepRail.tsx";
import { apiPut, setAccessToken } from "@/lib/api.ts";
import { Logo } from "@/src/components/ui/Logo.tsx";
import { useIsClient } from "@/src/lib/use-is-client.ts";

const STEPS = [
  { label: "Welcome" },
  { label: "Admin Account" },
  { label: "Monitoring" },
  { label: "Alerting" },
  { label: "Review" },
];

export default function SetupWizard() {
  const isClient = useIsClient();
  const [step, setStep] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  // Set synchronously so a second click before the next render is ignored.
  const submitting = useRef(false);

  // Form state
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [setupToken, setSetupToken] = useState("");

  const [promUrl, setPromUrl] = useState("");
  const [grafUrl, setGrafUrl] = useState("");
  const [grafToken, setGrafToken] = useState("");

  const [smtpHost, setSmtpHost] = useState("");
  const [smtpPort, setSmtpPort] = useState(587);
  const [smtpFrom, setSmtpFrom] = useState("");
  const [smtpUser, setSmtpUser] = useState("");
  const [smtpPass, setSmtpPass] = useState("");

  // Track what was configured
  const [adminCreated, setAdminCreated] = useState(false);
  const [monitoringConfigured, setMonitoringConfigured] = useState(false);
  const [alertingConfigured, setAlertingConfigured] = useState(false);

  // The alerting fields need an enabled value even though the wizard doesn't
  // show the toggle.
  const [alertEnabled, setAlertEnabled] = useState(true);

  const createAdmin = async () => {
    if (submitting.current) return; // guard against double-click
    if (password !== confirmPassword) {
      setError("Passwords do not match");
      return;
    }
    if (password.length < 8) {
      setError("Password must be at least 8 characters");
      return;
    }

    submitting.current = true;
    setLoading(true);
    setError("");

    try {
      // Create admin
      const createRes = await fetch("/api/v1/setup/init", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Requested-With": "XMLHttpRequest",
        },
        body: JSON.stringify({
          username,
          password,
          setupToken,
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
          username,
          password,
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

      setAdminCreated(true);
      setStep(2);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Setup failed");
    } finally {
      submitting.current = false;
      setLoading(false);
    }
  };

  const saveMonitoring = async () => {
    if (!promUrl && !grafUrl) {
      // Skip — nothing to save
      setStep(3);
      return;
    }

    setLoading(true);
    setError("");

    try {
      await apiPut("/v1/settings", {
        monitoringPrometheusUrl: promUrl || null,
        monitoringGrafanaUrl: grafUrl || null,
        monitoringGrafanaToken: grafToken || null,
      });

      setMonitoringConfigured(true);
      setStep(3);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Save failed");
    } finally {
      setLoading(false);
    }
  };

  const saveAlerting = async () => {
    if (!smtpHost) {
      setStep(4);
      return;
    }

    setLoading(true);
    setError("");

    try {
      await apiPut("/v1/settings", {
        alertingEnabled: true,
        alertingSmtpHost: smtpHost,
        alertingSmtpPort: smtpPort,
        alertingSmtpFrom: smtpFrom || null,
        alertingSmtpUsername: smtpUser || null,
        alertingSmtpPassword: smtpPass || null,
      });

      setAlertingConfigured(true);
      setStep(4);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Save failed");
    } finally {
      setLoading(false);
    }
  };

  if (!isClient) return null;

  return (
    <div className="mx-auto max-w-2xl px-4 py-12">
      <StepRail
        steps={STEPS}
        current={step}
        onStep={(s) => {
          if (s < step) setStep(s);
        }}
        orientation="horizontal"
      />

      {error && (
        <div className="mb-4">
          <ErrorBanner message={error} />
        </div>
      )}

      {/* Step 0: Welcome */}
      {step === 0 && (
        <div className="text-center">
          <div className="mx-auto mb-6 w-16 h-16">
            <Logo />
          </div>
          <h1 className="text-2xl font-bold text-text-primary">
            Welcome to k8sCenter
          </h1>
          <p className="mt-3 text-text-secondary">
            Let's set up your Kubernetes management platform. This wizard will
            create your admin account and configure optional monitoring and
            alerting integrations.
          </p>
          <div className="mt-8">
            <Button variant="primary" onClick={() => setStep(1)}>
              Get Started
            </Button>
          </div>
        </div>
      )}

      {/* Step 1: Admin Account */}
      {step === 1 && (
        <div className="space-y-4">
          <h2 className="text-lg font-semibold text-text-primary">
            Create Admin Account
          </h2>
          <div>
            <label className="mb-1 block text-sm font-medium text-text-secondary">
              Username
            </label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.currentTarget.value)}
              className={settingsInputClass}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-text-secondary">
              Password
            </label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.currentTarget.value)}
              className={settingsInputClass}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-text-secondary">
              Confirm Password
            </label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.currentTarget.value)}
              className={settingsInputClass}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-text-secondary">
              Setup Token
              <span className="ml-1 text-xs text-text-muted">
                (if configured)
              </span>
            </label>
            <input
              type="password"
              value={setupToken}
              onChange={(e) => setSetupToken(e.currentTarget.value)}
              placeholder="Leave empty if not required"
              className={settingsInputClass}
            />
          </div>
          <div className="flex justify-end pt-2">
            <Button
              variant="primary"
              onClick={createAdmin}
              loading={loading}
              disabled={!username || !password}
            >
              Create Account
            </Button>
          </div>
        </div>
      )}

      {/* Step 2: Monitoring */}
      {step === 2 && (
        <div className="space-y-4">
          <h2 className="text-lg font-semibold text-text-primary">
            Monitoring (Optional)
          </h2>
          <p className="text-sm text-text-muted">
            Connect to your Prometheus and Grafana instances. You can configure
            this later from Settings.
          </p>
          <MonitoringFields
            promUrl={promUrl}
            onPromUrlChange={setPromUrl}
            grafUrl={grafUrl}
            onGrafUrlChange={setGrafUrl}
            grafToken={grafToken}
            onGrafTokenChange={setGrafToken}
          />
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="ghost" onClick={() => setStep(3)}>
              Skip
            </Button>
            <Button
              variant="primary"
              onClick={saveMonitoring}
              loading={loading}
            >
              Save & Continue
            </Button>
          </div>
        </div>
      )}

      {/* Step 3: Alerting */}
      {step === 3 && (
        <div className="space-y-4">
          <h2 className="text-lg font-semibold text-text-primary">
            Alerting (Optional)
          </h2>
          <p className="text-sm text-text-muted">
            Configure SMTP for email alerts. You can configure this later from
            Settings.
          </p>
          <AlertingFields
            alertEnabled={alertEnabled}
            onAlertEnabledChange={setAlertEnabled}
            smtpHost={smtpHost}
            onSmtpHostChange={setSmtpHost}
            smtpPort={smtpPort}
            onSmtpPortChange={setSmtpPort}
            smtpUser={smtpUser}
            onSmtpUserChange={setSmtpUser}
            smtpPass={smtpPass}
            onSmtpPassChange={setSmtpPass}
            smtpFrom={smtpFrom}
            onSmtpFromChange={setSmtpFrom}
          />
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="ghost" onClick={() => setStep(4)}>
              Skip
            </Button>
            <Button variant="primary" onClick={saveAlerting} loading={loading}>
              Save & Continue
            </Button>
          </div>
        </div>
      )}

      {/* Step 4: Review */}
      {step === 4 && (
        <div className="space-y-4">
          <h2 className="text-lg font-semibold text-text-primary">
            Setup Complete
          </h2>
          <div className="rounded-lg border border-border-primary bg-surface p-4 bg-surface">
            <dl className="space-y-3 text-sm">
              <div className="flex justify-between">
                <dt className="text-text-muted">Admin Account</dt>
                <dd className="font-medium text-success">
                  {adminCreated ? `Created (${username})` : "—"}
                </dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-text-muted">Monitoring</dt>
                <dd
                  className={`font-medium ${
                    monitoringConfigured ? "text-success" : "text-text-muted"
                  }`}
                >
                  {monitoringConfigured ? "Configured" : "Skipped"}
                </dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-text-muted">Alerting</dt>
                <dd
                  className={`font-medium ${
                    alertingConfigured ? "text-success" : "text-text-muted"
                  }`}
                >
                  {alertingConfigured ? "Configured" : "Skipped"}
                </dd>
              </div>
            </dl>
          </div>
          <p className="text-sm text-text-muted">
            You can change any of these settings later from the Settings page.
          </p>
          <div className="flex justify-end pt-2">
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
