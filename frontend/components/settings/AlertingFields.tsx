import type { Signal } from "@preact/signals";
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import Toggle from "@/components/ui/form/Toggle.tsx";

interface AlertingFieldsProps {
  alertEnabled: Signal<boolean>;
  smtpHost: Signal<string>;
  smtpPort: Signal<number>;
  smtpUser: Signal<string>;
  smtpPass: Signal<string>;
  smtpFrom: Signal<string>;
  alertRate?: Signal<number>;
  alertRecipients?: Signal<string>;
  onDirty?: () => void;
}

export function AlertingFields(
  {
    alertEnabled,
    smtpHost,
    smtpPort,
    smtpUser,
    smtpPass,
    smtpFrom,
    alertRate,
    alertRecipients,
    onDirty,
  }: AlertingFieldsProps,
) {
  return (
    <>
      <div class="mb-4 flex items-center gap-3">
        <Toggle
          checked={alertEnabled.value}
          onChange={(v) => {
            alertEnabled.value = v;
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
      <div class="grid gap-4 sm:grid-cols-2">
        <Field label="SMTP Host">
          <TextField
            value={smtpHost.value}
            onInput={(v) => {
              smtpHost.value = v;
              onDirty?.();
            }}
            placeholder="smtp.example.com"
          />
        </Field>
        <Field label="SMTP Port">
          <TextField
            value={String(smtpPort.value)}
            onInput={(v) => {
              smtpPort.value = parseInt(v) || 587;
              onDirty?.();
            }}
          />
        </Field>
        <Field label="SMTP Username">
          <TextField
            value={smtpUser.value}
            onInput={(v) => {
              smtpUser.value = v;
              onDirty?.();
            }}
          />
        </Field>
        <Field label="SMTP Password">
          <TextField
            value={smtpPass.value}
            onInput={(v) => {
              smtpPass.value = v;
              onDirty?.();
            }}
            placeholder={smtpPass.value === "****" ? "****" : ""}
          />
        </Field>
        <Field label="From Address">
          <TextField
            value={smtpFrom.value}
            onInput={(v) => {
              smtpFrom.value = v;
              onDirty?.();
            }}
            placeholder="alerts@example.com"
          />
        </Field>
        {alertRate && (
          <Field label="Rate Limit (per hour)">
            <TextField
              value={String(alertRate.value)}
              onInput={(v) => {
                alertRate.value = parseInt(v) || 5;
                onDirty?.();
              }}
            />
          </Field>
        )}
        {alertRecipients && (
          <div class="sm:col-span-2">
            <Field
              label="Recipients"
              hint="Comma-separated email addresses"
            >
              <TextField
                value={alertRecipients.value}
                onInput={(v) => {
                  alertRecipients.value = v;
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
