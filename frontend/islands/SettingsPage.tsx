import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPut } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Toast, useToast } from "@/components/ui/Toast.tsx";
import { MonitoringFields } from "@/components/settings/MonitoringFields.tsx";
import { AlertingFields } from "@/components/settings/AlertingFields.tsx";

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
  const { toast, show: showToast } = useToast();

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

  // Beforeunload guard
  useEffect(() => {
    if (!IS_BROWSER) return;
    const dirty = dirtyMonitoring.value || dirtyAlerting.value;
    if (!dirty) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, [dirtyMonitoring.value, dirtyAlerting.value]);

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
      alertRecipients.value = (s.alertingRecipients ?? []).join(", ");
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
      <div class="text-sm text-slate-500 dark:text-slate-400">
        Loading settings...
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="rounded-md bg-red-50 px-4 py-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
        {error.value}
      </div>
    );
  }

  return (
    <div class="space-y-4">
      <Toast toast={toast} />

      {/* Monitoring Section */}
      <details
        open
        class="rounded-lg border border-slate-200 dark:border-slate-700"
      >
        <summary class="cursor-pointer select-none px-4 py-3 text-sm font-semibold text-slate-900 dark:text-white">
          Monitoring
        </summary>
        <div class="border-t border-slate-200 px-4 py-4 dark:border-slate-700">
          <MonitoringFields
            promUrl={promUrl}
            grafUrl={grafUrl}
            grafToken={grafToken}
            monNs={monNs}
            onDirty={() => {
              dirtyMonitoring.value = true;
            }}
          />
          <div class="mt-4 flex justify-end">
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
          </div>
        </div>
      </details>

      {/* Alerting Section */}
      <details class="rounded-lg border border-slate-200 dark:border-slate-700">
        <summary class="cursor-pointer select-none px-4 py-3 text-sm font-semibold text-slate-900 dark:text-white">
          Alerting
        </summary>
        <div class="border-t border-slate-200 px-4 py-4 dark:border-slate-700">
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
          <div class="mt-4 flex items-center justify-end gap-3">
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
        </div>
      </details>
    </div>
  );
}
