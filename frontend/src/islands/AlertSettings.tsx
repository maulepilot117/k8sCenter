/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { Input } from "@/components/ui/Input.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { apiGet, apiPost, apiPut } from "@/lib/api.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

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
  const [settings, setSettings] = useState<AlertingSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  // Form state
  const [smtpHost, setSmtpHost] = useState("");
  const [smtpPort, setSmtpPort] = useState(587);
  const [smtpUsername, setSmtpUsername] = useState("");
  const [smtpPassword, setSmtpPassword] = useState("");
  const [smtpFrom, setSmtpFrom] = useState("");
  const [smtpTLSInsecure, setSmtpTLSInsecure] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [rateLimit, setRateLimit] = useState(120);
  const [retentionDays, setRetentionDays] = useState(30);

  function fetchSettings() {
    setLoading(true);
    apiGet<AlertingSettings>("/v1/alerts/settings")
      .then((res) => {
        const s = res.data;
        if (s) {
          setSettings(s);
          setSmtpHost(s.smtp.host);
          setSmtpPort(s.smtp.port);
          setSmtpUsername(s.smtp.username);
          setSmtpFrom(s.smtp.from);
          setSmtpTLSInsecure(s.smtp.tlsInsecure);
          setEnabled(s.enabled);
          setRateLimit(s.rateLimit);
          setRetentionDays(s.retentionDays);
        }
        setError(null);
      })
      .catch((err) => {
        setError(err.message ?? "Failed to fetch settings");
      })
      .finally(() => {
        setLoading(false);
      });
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `fetchSettings` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchSettings();
  }, []);

  function handleSave() {
    setSaving(true);
    setError(null);
    setSuccess(null);

    apiPut("/v1/alerts/settings", {
      enabled,
      rateLimit,
      retentionDays,
      smtp: {
        host: smtpHost,
        port: smtpPort,
        username: smtpUsername,
        password: smtpPassword,
        from: smtpFrom,
        tlsInsecure: smtpTLSInsecure,
      },
    })
      .then(() => {
        setSuccess("Settings saved.");
        setSmtpPassword("");
        fetchSettings();
      })
      .catch((err) => {
        setError(err.message ?? "Failed to save settings");
      })
      .finally(() => {
        setSaving(false);
      });
  }

  function handleTestEmail() {
    setTesting(true);
    setError(null);
    setSuccess(null);

    apiPost("/v1/alerts/test")
      .then(() => {
        setSuccess("Test email queued successfully.");
      })
      .catch((err) => {
        setError(err.message ?? "Failed to send test email");
      })
      .finally(() => {
        setTesting(false);
      });
  }

  if (loading) {
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

      {error && <ErrorBanner message={error} />}

      {success && (
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
          {success}
        </div>
      )}

      {/* General Settings */}
      <WidgetShell title="General">
        <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
            <input
              type="checkbox"
              id="alerting-enabled"
              checked={enabled}
              onChange={(e) => setEnabled(e.currentTarget.checked)}
              style={{
                width: "16px",
                height: "16px",
                accentColor: "var(--accent)",
                cursor: "pointer",
              }}
            />
            <label
              htmlFor="alerting-enabled"
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
              value={String(rateLimit)}
              onChange={(e) =>
                setRateLimit(parseInt(e.currentTarget.value, 10) || 120)
              }
            />
            <Input
              label="Retention (days)"
              type="number"
              value={String(retentionDays)}
              onChange={(e) =>
                setRetentionDays(parseInt(e.currentTarget.value, 10) || 30)
              }
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
              value={smtpHost}
              onChange={(e) => setSmtpHost(e.currentTarget.value)}
              placeholder="smtp.example.com"
            />
            <Input
              label="Port"
              type="number"
              value={String(smtpPort)}
              onChange={(e) =>
                setSmtpPort(parseInt(e.currentTarget.value, 10) || 587)
              }
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
              value={smtpUsername}
              onChange={(e) => setSmtpUsername(e.currentTarget.value)}
            />
            <Input
              label="Password"
              type="password"
              value={smtpPassword}
              onChange={(e) => setSmtpPassword(e.currentTarget.value)}
              placeholder={
                settings?.smtp.password
                  ? "**** (leave empty to keep current)"
                  : ""
              }
            />
          </div>
          <Input
            label="From Address"
            value={smtpFrom}
            onChange={(e) => setSmtpFrom(e.currentTarget.value)}
            placeholder="alerts@example.com"
          />
          <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
            <input
              type="checkbox"
              id="tls-insecure"
              checked={smtpTLSInsecure}
              onChange={(e) => setSmtpTLSInsecure(e.currentTarget.checked)}
              style={{
                width: "16px",
                height: "16px",
                accentColor: "var(--accent)",
                cursor: "pointer",
              }}
            />
            <label
              htmlFor="tls-insecure"
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
            <Button onClick={handleSave} disabled={saving}>
              {saving ? "Saving..." : "Save Settings"}
            </Button>
            <Button
              variant="secondary"
              onClick={handleTestEmail}
              disabled={testing}
            >
              {testing ? "Sending..." : "Send Test Email"}
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
              {settings?.webhookToken || "not configured"}
            </code>
          </div>
        </div>
      </WidgetShell>
    </div>
  );
}
