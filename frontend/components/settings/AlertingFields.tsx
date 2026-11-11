import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import Toggle from "@/components/ui/form/Toggle.tsx";

interface AlertingFieldsProps {
  alertEnabled: boolean;
  onAlertEnabledChange: (v: boolean) => void;
  smtpHost: string;
  onSmtpHostChange: (v: string) => void;
  smtpPort: number;
  onSmtpPortChange: (v: number) => void;
  smtpUser: string;
  onSmtpUserChange: (v: string) => void;
  smtpPass: string;
  onSmtpPassChange: (v: string) => void;
  smtpFrom: string;
  onSmtpFromChange: (v: string) => void;
  /** The rate limit field shows only when both of its props are passed. */
  alertRate?: number;
  onAlertRateChange?: (v: number) => void;
  /** The recipients field shows only when both of its props are passed. */
  alertRecipients?: string;
  onAlertRecipientsChange?: (v: string) => void;
  onDirty?: () => void;
}

export function AlertingFields({
  alertEnabled,
  onAlertEnabledChange,
  smtpHost,
  onSmtpHostChange,
  smtpPort,
  onSmtpPortChange,
  smtpUser,
  onSmtpUserChange,
  smtpPass,
  onSmtpPassChange,
  smtpFrom,
  onSmtpFromChange,
  alertRate,
  onAlertRateChange,
  alertRecipients,
  onAlertRecipientsChange,
  onDirty,
}: AlertingFieldsProps) {
  return (
    <>
      <div className="mb-4 flex items-center gap-3">
        <Toggle
          checked={alertEnabled}
          onChange={(v) => {
            onAlertEnabledChange(v);
            onDirty?.();
          }}
        />
        <span
          style={{
            fontSize: "13px",
            color: "var(--text-secondary)",
          }}
        >
          Enable email alerting
        </span>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="SMTP Host">
          <TextField
            value={smtpHost}
            onInput={(v) => {
              onSmtpHostChange(v);
              onDirty?.();
            }}
            placeholder="smtp.example.com"
          />
        </Field>
        <Field label="SMTP Port">
          <TextField
            value={String(smtpPort)}
            onInput={(v) => {
              onSmtpPortChange(parseInt(v, 10) || 587);
              onDirty?.();
            }}
          />
        </Field>
        <Field label="SMTP Username">
          <TextField
            value={smtpUser}
            onInput={(v) => {
              onSmtpUserChange(v);
              onDirty?.();
            }}
          />
        </Field>
        <Field label="SMTP Password">
          <TextField
            type="password"
            value={smtpPass}
            onInput={(v) => {
              onSmtpPassChange(v);
              onDirty?.();
            }}
            placeholder={smtpPass === "****" ? "****" : ""}
          />
        </Field>
        <Field label="From Address">
          <TextField
            value={smtpFrom}
            onInput={(v) => {
              onSmtpFromChange(v);
              onDirty?.();
            }}
            placeholder="alerts@example.com"
          />
        </Field>
        {alertRate !== undefined && onAlertRateChange && (
          <Field label="Rate Limit (per hour)">
            <TextField
              value={String(alertRate)}
              onInput={(v) => {
                onAlertRateChange(parseInt(v, 10) || 5);
                onDirty?.();
              }}
            />
          </Field>
        )}
        {alertRecipients !== undefined && onAlertRecipientsChange && (
          <div className="sm:col-span-2">
            <Field label="Recipients" hint="Comma-separated email addresses">
              <TextField
                value={alertRecipients}
                onInput={(v) => {
                  onAlertRecipientsChange(v);
                  onDirty?.();
                }}
                placeholder="admin@example.com, ops@example.com"
              />
            </Field>
          </div>
        )}
      </div>
    </>
  );
}
