import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost, apiPut } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";

interface AlertingSettings {
  enabled: boolean;
  webhookToken: string;
  retentionDays: number;
  rateLimit: number;
  smtp: {
    host: string;
    port: number;
    username: string;
    password: string;
    from: string;
    tlsInsecure: boolean;
  };
}

export default function AlertSettings() {
  const settings = useSignal<AlertingSettings | null>(null);
  const loading = useSignal(true);
  const saving = useSignal(false);
  const testing = useSignal(false);
  const error = useSignal<string | null>(null);
  const success = useSignal<string | null>(null);

  // Form state
  const smtpHost = useSignal("");
  const smtpPort = useSignal(587);
  const smtpUsername = useSignal("");
  const smtpPassword = useSignal("");
  const smtpFrom = useSignal("");
  const smtpTLSInsecure = useSignal(false);
  const enabled = useSignal(false);
  const rateLimit = useSignal(120);
  const retentionDays = useSignal(30);

  function fetchSettings() {
    loading.value = true;
    apiGet<AlertingSettings>("/v1/alerts/settings")
      .then((res) => {
        const s = res.data;
        if (s) {
          settings.value = s;
          smtpHost.value = s.smtp.host;
          smtpPort.value = s.smtp.port;
          smtpUsername.value = s.smtp.username;
          smtpFrom.value = s.smtp.from;
          smtpTLSInsecure.value = s.smtp.tlsInsecure;
          enabled.value = s.enabled;
          rateLimit.value = s.rateLimit;
          retentionDays.value = s.retentionDays;
        }
        error.value = null;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to fetch settings";
      })
      .finally(() => {
        loading.value = false;
      });
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchSettings();
  }, []);

  function handleSave() {
    saving.value = true;
    error.value = null;
    success.value = null;

    apiPut("/v1/alerts/settings", {
      enabled: enabled.value,
      rateLimit: rateLimit.value,
      retentionDays: retentionDays.value,
      smtp: {
        host: smtpHost.value,
        port: smtpPort.value,
        username: smtpUsername.value,
        password: smtpPassword.value,
        from: smtpFrom.value,
        tlsInsecure: smtpTLSInsecure.value,
      },
    })
      .then(() => {
        success.value = "Settings saved.";
        smtpPassword.value = ""; // Clear password field after save
        fetchSettings();
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to save settings";
      })
      .finally(() => {
        saving.value = false;
      });
  }

  function handleTestEmail() {
    testing.value = true;
    error.value = null;
    success.value = null;

    apiPost("/v1/alerts/test")
      .then(() => {
        success.value = "Test email queued successfully.";
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to send test email";
      })
      .finally(() => {
        testing.value = false;
      });
  }

  if (loading.value) {
    return (
      <div
        style={{
          textAlign: "center",
          padding: "48px",
          fontSize: "13px",
          color: "var(--text-muted)",
        }}
      >
        Loading...
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Warning banner — glass-tinted, not a data surface */}
      <div
        style={{
          borderRadius: "12px",
          border: "1px solid var(--warning)",
          background: "color-mix(in srgb, var(--warning) 10%, transparent)",
          padding: "12px 16px",
          fontSize: "13px",
          color: "var(--warning)",
        }}
      >
        Settings configured here are stored in memory and will be lost on pod
        restart. Use environment variables for persistent configuration.
      </div>

      {error.value && <ErrorBanner message={error.value} />}

      {success.value && (
        <div
          style={{
            borderRadius: "12px",
            border: "1px solid var(--success)",
            background: "color-mix(in srgb, var(--success) 10%, transparent)",
            padding: "12px 16px",
            fontSize: "13px",
            color: "var(--success)",
          }}
        >
          {success.value}
        </div>
      )}

      {/* General Settings */}
      <WidgetShell title="General">
        <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
            <input
              type="checkbox"
              id="alerting-enabled"
              checked={enabled.value}
              onChange={(e) =>
                enabled.value = (e.target as HTMLInputElement).checked}
              style={{
                width: "16px",
                height: "16px",
                accentColor: "var(--accent)",
                cursor: "pointer",
              }}
            />
            <label
              for="alerting-enabled"
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                cursor: "pointer",
              }}
            >
              Enable alerting (webhook receiver and email notifications)
            </label>
          </div>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: "12px",
            }}
          >
            <Input
              label="Rate Limit (emails/hour)"
              type="number"
              value={String(rateLimit.value)}
              onInput={(e) =>
                rateLimit.value =
                  parseInt((e.target as HTMLInputElement).value) || 120}
            />
            <Input
              label="Retention (days)"
              type="number"
              value={String(retentionDays.value)}
              onInput={(e) =>
                retentionDays.value =
                  parseInt((e.target as HTMLInputElement).value) || 30}
            />
          </div>
        </div>
      </WidgetShell>

      {/* SMTP Configuration */}
      <WidgetShell title="SMTP Configuration">
        <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: "12px",
            }}
          >
            <Input
              label="SMTP Host"
              value={smtpHost.value}
              onInput={(e) =>
                smtpHost.value = (e.target as HTMLInputElement).value}
              placeholder="smtp.example.com"
            />
            <Input
              label="Port"
              type="number"
              value={String(smtpPort.value)}
              onInput={(e) =>
                smtpPort.value =
                  parseInt((e.target as HTMLInputElement).value) || 587}
            />
          </div>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: "12px",
            }}
          >
            <Input
              label="Username"
              value={smtpUsername.value}
              onInput={(e) =>
                smtpUsername.value = (e.target as HTMLInputElement).value}
            />
            <Input
              label="Password"
              type="password"
              value={smtpPassword.value}
              onInput={(e) =>
                smtpPassword.value = (e.target as HTMLInputElement).value}
              placeholder={settings.value?.smtp.password
                ? "**** (leave empty to keep current)"
                : ""}
            />
          </div>
          <Input
            label="From Address"
            value={smtpFrom.value}
            onInput={(e) =>
              smtpFrom.value = (e.target as HTMLInputElement).value}
            placeholder="alerts@example.com"
          />
          <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
            <input
              type="checkbox"
              id="tls-insecure"
              checked={smtpTLSInsecure.value}
              onChange={(e) =>
                smtpTLSInsecure.value = (e.target as HTMLInputElement).checked}
              style={{
                width: "16px",
                height: "16px",
                accentColor: "var(--accent)",
                cursor: "pointer",
              }}
            />
            <label
              for="tls-insecure"
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                cursor: "pointer",
              }}
            >
              Skip TLS verification (development only)
            </label>
          </div>
          <div style={{ display: "flex", gap: "8px" }}>
            <Button onClick={handleSave} disabled={saving.value}>
              {saving.value ? "Saving..." : "Save Settings"}
            </Button>
            <Button
              variant="secondary"
              onClick={handleTestEmail}
              disabled={testing.value}
            >
              {testing.value ? "Sending..." : "Send Test Email"}
            </Button>
          </div>
        </div>
      </WidgetShell>

      {/* Webhook Configuration */}
      <WidgetShell title="Webhook Configuration">
        <div style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
          <p
            style={{ margin: 0, fontSize: "13px", color: "var(--text-muted)" }}
          >
            Configure Alertmanager to send alerts to k8sCenter using the
            following receiver configuration:
          </p>
          {/* Solid code block — data surface stays opaque */}
          <div
            style={{
              borderRadius: "9px",
              background: "var(--bg-surface)",
              padding: "14px 16px",
              overflowX: "auto",
            }}
          >
            <pre
              style={{
                margin: 0,
                fontSize: "12px",
                fontFamily: "monospace",
                color: "var(--success)",
                whiteSpace: "pre",
              }}
            >{`receivers:
 - name: 'kubecenter'
 webhook_configs:
 - send_resolved: true
 url: 'http://<kubecenter-backend>:8080/api/v1/alerts/webhook'
 http_config:
 authorization:
 type: Bearer
 credentials: '<webhook-token>'`}</pre>
          </div>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            <span>Webhook Token:</span>
            <code
              style={{
                background: "var(--bg-elevated)",
                padding: "2px 8px",
                borderRadius: "6px",
                fontSize: "12px",
                fontFamily: "monospace",
                color: "var(--text-primary)",
              }}
            >
              {settings.value?.webhookToken || "not configured"}
            </code>
          </div>
        </div>
      </WidgetShell>
    </div>
  );
}
