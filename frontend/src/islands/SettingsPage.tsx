import { useEffect, useState } from "react";
import { AlertingFields } from "@/components/settings/AlertingFields.tsx";
import { MonitoringFields } from "@/components/settings/MonitoringFields.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { apiGet, apiPut } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface Settings {
  monitoringPrometheusUrl?: string | null;
  monitoringGrafanaUrl?: string | null;
  monitoringGrafanaToken?: string | null;
  monitoringNamespace?: string | null;
  alertingEnabled?: boolean | null;
  alertingSmtpHost?: string | null;
  alertingSmtpPort?: number | null;
  alertingSmtpUsername?: string | null;
  alertingSmtpPassword?: string | null;
  alertingSmtpFrom?: string | null;
  alertingRateLimit?: number | null;
  alertingRecipients?: string[] | null;
}

export default function SettingsPage() {
  const isClient = useIsClient();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  // Per-section dirty tracking
  const [dirtyMonitoring, setDirtyMonitoring] = useState(false);
  const [dirtyAlerting, setDirtyAlerting] = useState(false);
  const [savingSection, setSavingSection] = useState<string | null>(null);

  // Local form state
  const [promUrl, setPromUrl] = useState("");
  const [grafUrl, setGrafUrl] = useState("");
  const [grafToken, setGrafToken] = useState("");
  const [monNs, setMonNs] = useState("");
  const [alertEnabled, setAlertEnabled] = useState(false);
  const [smtpHost, setSmtpHost] = useState("");
  const [smtpPort, setSmtpPort] = useState(587);
  const [smtpUser, setSmtpUser] = useState("");
  const [smtpPass, setSmtpPass] = useState("");
  const [smtpFrom, setSmtpFrom] = useState("");
  const [alertRate, setAlertRate] = useState(5);
  const [alertRecipients, setAlertRecipients] = useState("");

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `fetchSettings` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchSettings();
  }, []);

  useDirtyGuard(dirtyMonitoring || dirtyAlerting);

  async function fetchSettings() {
    try {
      const res = await apiGet<Settings>("/v1/settings");
      const s = res.data;
      // Populate form fields
      setPromUrl(s.monitoringPrometheusUrl ?? "");
      setGrafUrl(s.monitoringGrafanaUrl ?? "");
      setGrafToken(s.monitoringGrafanaToken ?? "");
      setMonNs(s.monitoringNamespace ?? "");
      setAlertEnabled(s.alertingEnabled ?? false);
      setSmtpHost(s.alertingSmtpHost ?? "");
      setSmtpPort(s.alertingSmtpPort ?? 587);
      setSmtpUser(s.alertingSmtpUsername ?? "");
      setSmtpPass(s.alertingSmtpPassword ?? "");
      setSmtpFrom(s.alertingSmtpFrom ?? "");
      setAlertRate(s.alertingRateLimit ?? 5);
      setAlertRecipients((s.alertingRecipients ?? []).join(","));
      setLoading(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load");
      setLoading(false);
    }
  }

  async function saveMonitoring() {
    setSavingSection("monitoring");
    try {
      await apiPut("/v1/settings", {
        monitoringPrometheusUrl: promUrl || null,
        monitoringGrafanaUrl: grafUrl || null,
        monitoringGrafanaToken: grafToken || null,
        monitoringNamespace: monNs || null,
      });
      setDirtyMonitoring(false);
      showToast("Monitoring settings saved", "success");
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Save failed", "error");
    } finally {
      setSavingSection(null);
    }
  }

  async function saveAlerting() {
    setSavingSection("alerting");
    try {
      const recipients = alertRecipients
        .split(",")
        .map((r) => r.trim())
        .filter(Boolean);
      await apiPut("/v1/settings", {
        alertingEnabled: alertEnabled,
        alertingSmtpHost: smtpHost || null,
        alertingSmtpPort: smtpPort,
        alertingSmtpUsername: smtpUser || null,
        alertingSmtpPassword: smtpPass || null,
        alertingSmtpFrom: smtpFrom || null,
        alertingRateLimit: alertRate,
        alertingRecipients: recipients.length > 0 ? recipients : null,
      });
      setDirtyAlerting(false);
      showToast("Alerting settings saved", "success");
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Save failed", "error");
    } finally {
      setSavingSection(null);
    }
  }

  async function testEmail() {
    try {
      await apiPut("/v1/alerts/test", {});
      showToast("Test email sent", "success");
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Test failed", "error");
    }
  }

  if (!isClient) return null;

  if (loading) {
    return (
      <div style={{ fontSize: "13px", color: "var(--text-muted)" }}>
        Loading settings…
      </div>
    );
  }

  if (error) {
    return <ErrorBanner message={error} />;
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Monitoring Section */}
      <WidgetShell
        title="Monitoring"
        action={
          <Button
            variant="primary"
            size="sm"
            onClick={saveMonitoring}
            disabled={!dirtyMonitoring || savingSection === "monitoring"}
            loading={savingSection === "monitoring"}
          >
            Save Monitoring
          </Button>
        }
      >
        <MonitoringFields
          promUrl={promUrl}
          onPromUrlChange={setPromUrl}
          grafUrl={grafUrl}
          onGrafUrlChange={setGrafUrl}
          grafToken={grafToken}
          onGrafTokenChange={setGrafToken}
          monNs={monNs}
          onMonNsChange={setMonNs}
          onDirty={() => setDirtyMonitoring(true)}
        />
      </WidgetShell>

      {/* Alerting Section */}
      <WidgetShell
        title="Alerting"
        action={
          <div style={{ display: "flex", gap: "8px" }}>
            <Button variant="ghost" size="sm" onClick={testEmail}>
              Send Test Email
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={saveAlerting}
              disabled={!dirtyAlerting || savingSection === "alerting"}
              loading={savingSection === "alerting"}
            >
              Save Alerting
            </Button>
          </div>
        }
      >
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
          alertRate={alertRate}
          onAlertRateChange={setAlertRate}
          alertRecipients={alertRecipients}
          onAlertRecipientsChange={setAlertRecipients}
          onDirty={() => setDirtyAlerting(true)}
        />
      </WidgetShell>
    </div>
  );
}
