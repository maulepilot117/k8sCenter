import { useSignal } from"@preact/signals";
import { useEffect } from"preact/hooks";
import { IS_BROWSER } from"fresh/runtime";
import { apiGet, apiPost, apiPut } from"@/lib/api.ts";
import { Button } from"@/components/ui/Button.tsx";
import { Card } from"@/components/ui/Card.tsx";
import { Input } from"@/components/ui/Input.tsx";

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
 error.value = err.message ??"Failed to fetch settings";
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
 success.value ="Settings saved.";
 smtpPassword.value =""; // Clear password field after save
 fetchSettings();
 })
 .catch((err) => {
 error.value = err.message ??"Failed to save settings";
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
 success.value ="Test email queued successfully.";
 })
 .catch((err) => {
 error.value = err.message ??"Failed to send test email";
 })
 .finally(() => {
 testing.value = false;
 });
 }

 if (loading.value) {
 return (
 <div class="text-text-muted text-sm py-8 text-center">
 Loading...
 </div>
 );
 }

 return (
 <div class="space-y-6">
 {/* Warning banner */}
 <div class="bg-warning-dim border border-warning text-amber-800 text-warning rounded-lg p-4 text-sm">
 Settings configured here are stored in memory and will be lost on pod
 restart. Use environment variables for persistent configuration.
 </div>

 {error.value && (
 <div class="bg-danger-dim border border-danger text-danger rounded-lg p-4 text-sm">
 {error.value}
 </div>
 )}

 {success.value && (
 <div class="bg-success-dim border border-success text-success rounded-lg p-4 text-sm">
 {success.value}
 </div>
 )}

 {/* General Settings */}
 <Card title="General">
 <div class="p-4 space-y-4">
 <div class="flex items-center gap-3">
 <input
 type="checkbox"
 id="alerting-enabled"
 checked={enabled.value}
 onChange={(e) =>
 enabled.value = (e.target as HTMLInputElement).checked}
 class="h-4 w-4 rounded border-border-primary text-blue-600"
 />
 <label
 for="alerting-enabled"
 class="text-sm text-text-secondary"
 >
 Enable alerting (webhook receiver and email notifications)
 </label>
 </div>
 <div class="grid grid-cols-2 gap-4">
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
 </Card>

 {/* SMTP Configuration */}
 <Card title="SMTP Configuration">
 <div class="p-4 space-y-4">
 <div class="grid grid-cols-2 gap-4">
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
 <div class="grid grid-cols-2 gap-4">
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
 ?"**** (leave empty to keep current)"
 :""}
 />
 </div>
 <Input
 label="From Address"
 value={smtpFrom.value}
 onInput={(e) =>
 smtpFrom.value = (e.target as HTMLInputElement).value}
 placeholder="alerts@example.com"
 />
 <div class="flex items-center gap-3">
 <input
 type="checkbox"
 id="tls-insecure"
 checked={smtpTLSInsecure.value}
 onChange={(e) =>
 smtpTLSInsecure.value = (e.target as HTMLInputElement).checked}
 class="h-4 w-4 rounded border-border-primary text-blue-600"
 />
 <label
 for="tls-insecure"
 class="text-sm text-text-secondary"
 >
 Skip TLS verification (development only)
 </label>
 </div>
 <div class="flex gap-2">
 <Button onClick={handleSave} disabled={saving.value}>
 {saving.value ?"Saving..." :"Save Settings"}
 </Button>
 <Button
 variant="secondary"
 onClick={handleTestEmail}
 disabled={testing.value}
 >
 {testing.value ?"Sending..." :"Send Test Email"}
 </Button>
 </div>
 </div>
 </Card>

 {/* Webhook Configuration */}
 <Card title="Webhook Configuration">
 <div class="p-4 space-y-4">
 <p class="text-sm text-text-secondary">
 Configure Alertmanager to send alerts to k8sCenter using the
 following receiver configuration:
 </p>
 <div class="bg-base rounded-lg p-4 overflow-x-auto">
 <pre class="text-sm text-green-400 font-mono whitespace-pre">{`receivers:
 - name: 'kubecenter'
 webhook_configs:
 - send_resolved: true
 url: 'http://<kubecenter-backend>:8080/api/v1/alerts/webhook'
 http_config:
 authorization:
 type: Bearer
 credentials: '<webhook-token>'`}</pre>
 </div>
 <div class="flex items-center gap-2 text-sm text-text-secondary">
 <span>Webhook Token:</span>
 <code class="bg-elevated px-2 py-1 rounded text-xs">
 {settings.value?.webhookToken ||"not configured"}
 </code>
 </div>
 </div>
 </Card>
 </div>
 );
}
