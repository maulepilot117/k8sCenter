import { useComputed, useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPut } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { Button } from "@/components/ui/Button.tsx";
import { showToast } from "@/islands/ToastProvider.tsx";
import { MonitoringFields } from "@/components/settings/MonitoringFields.tsx";
import { AlertingFields } from "@/components/settings/AlertingFields.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";

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
  const settings = useSignal<Settings>({});
  const loading = useSignal(true);
  const error = useSignal("");

  // Per-section dirty tracking
  const dirtyMonitoring = useSignal(false);
  const dirtyAlerting = useSignal(false);
  const savingSection = useSignal<string | null>(null);

  // Local form state
  const promUrl = useSignal("");
  const grafUrl = useSignal("");
  const grafToken = useSignal("");
  const monNs = useSignal("");
  const alertEnabled = useSignal(false);
  const smtpHost = useSignal("");
  const smtpPort = useSignal(587);
  const smtpUser = useSignal("");
  const smtpPass = useSignal("");
  const smtpFrom = useSignal("");
  const alertRate = useSignal(5);
  const alertRecipients = useSignal("");

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchSettings();
  }, []);

  const dirty = useComputed(() => dirtyMonitoring.value || dirtyAlerting.value);
  useDirtyGuard(dirty);

  async function fetchSettings() {
    try {
      const res = await apiGet<Settings>("/v1/settings");
      const s = res.data;
      settings.value = s;
      // Populate form fields
      promUrl.value = s.monitoringPrometheusUrl ?? "";
      grafUrl.value = s.monitoringGrafanaUrl ?? "";
      grafToken.value = s.monitoringGrafanaToken ?? "";
      monNs.value = s.monitoringNamespace ?? "";
      alertEnabled.value = s.alertingEnabled ?? false;
      smtpHost.value = s.alertingSmtpHost ?? "";
      smtpPort.value = s.alertingSmtpPort ?? 587;
      smtpUser.value = s.alertingSmtpUsername ?? "";
      smtpPass.value = s.alertingSmtpPassword ?? "";
      smtpFrom.value = s.alertingSmtpFrom ?? "";
      alertRate.value = s.alertingRateLimit ?? 5;
      alertRecipients.value = (s.alertingRecipients ?? []).join(",");
      loading.value = false;
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Failed to load";
      loading.value = false;
    }
  }

  async function saveMonitoring() {
    savingSection.value = "monitoring";
    try {
      await apiPut("/v1/settings", {
        monitoringPrometheusUrl: promUrl.value || null,
        monitoringGrafanaUrl: grafUrl.value || null,
        monitoringGrafanaToken: grafToken.value || null,
        monitoringNamespace: monNs.value || null,
      });
      dirtyMonitoring.value = false;
      showToast("Monitoring settings saved", "success");
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Save failed",
        "error",
      );
    } finally {
      savingSection.value = null;
    }
  }

  async function saveAlerting() {
    savingSection.value = "alerting";
    try {
      const recipients = alertRecipients.value
        .split(",")
        .map((r) => r.trim())
        .filter(Boolean);
      await apiPut("/v1/settings", {
        alertingEnabled: alertEnabled.value,
        alertingSmtpHost: smtpHost.value || null,
        alertingSmtpPort: smtpPort.value,
        alertingSmtpUsername: smtpUser.value || null,
        alertingSmtpPassword: smtpPass.value || null,
        alertingSmtpFrom: smtpFrom.value || null,
        alertingRateLimit: alertRate.value,
        alertingRecipients: recipients.length > 0 ? recipients : null,
      });
      dirtyAlerting.value = false;
      showToast("Alerting settings saved", "success");
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Save failed",
        "error",
      );
    } finally {
      savingSection.value = null;
    }
  }

  async function testEmail() {
    try {
      await apiPut("/v1/alerts/test", {});
      showToast("Test email sent", "success");
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Test failed",
        "error",
      );
    }
  }

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div style={{ fontSize: "13px", color: "var(--text-muted)" }}>
        Loading settings…
      </div>
    );
  }

  if (error.value) {
    return <ErrorBanner message={error.value} />;
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
            disabled={!dirtyMonitoring.value ||
              savingSection.value === "monitoring"}
            loading={savingSection.value === "monitoring"}
          >
            Save Monitoring
          </Button>
        }
      >
        <MonitoringFields
          promUrl={promUrl}
          grafUrl={grafUrl}
          grafToken={grafToken}
          monNs={monNs}
          onDirty={() => {
            dirtyMonitoring.value = true;
          }}
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
              disabled={!dirtyAlerting.value ||
                savingSection.value === "alerting"}
              loading={savingSection.value === "alerting"}
            >
              Save Alerting
            </Button>
          </div>
        }
      >
        <AlertingFields
          alertEnabled={alertEnabled}
          smtpHost={smtpHost}
          smtpPort={smtpPort}
          smtpUser={smtpUser}
          smtpPass={smtpPass}
          smtpFrom={smtpFrom}
          alertRate={alertRate}
          alertRecipients={alertRecipients}
          onDirty={() => {
            dirtyAlerting.value = true;
          }}
        />
      </WidgetShell>
    </div>
  );
}
