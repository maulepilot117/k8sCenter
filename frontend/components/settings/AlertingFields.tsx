/** @jsxImportSource react */
import { type Signal, signal } from "@preact/signals-core";
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import Toggle from "@/components/ui/form/Toggle.tsx";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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

// Stand-ins so the hooks below run unconditionally when an optional signal is
// absent; the matching field is not rendered in that case.
const NO_NUMBER = signal(0);
const NO_TEXT = signal("");

export function AlertingFields({
  alertEnabled,
  smtpHost,
  smtpPort,
  smtpUser,
  smtpPass,
  smtpFrom,
  alertRate,
  alertRecipients,
  onDirty,
}: AlertingFieldsProps) {
  const enabled = useSignalValue(alertEnabled);
  const host = useSignalValue(smtpHost);
  const port = useSignalValue(smtpPort);
  const user = useSignalValue(smtpUser);
  const pass = useSignalValue(smtpPass);
  const from = useSignalValue(smtpFrom);
  const rate = useSignalValue(alertRate ?? NO_NUMBER);
  const recipients = useSignalValue(alertRecipients ?? NO_TEXT);

  return (
    <>
      <div className="mb-4 flex items-center gap-3">
        <Toggle
          checked={enabled}
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
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="SMTP Host">
          <TextField
            value={host}
            onInput={(v) => {
              smtpHost.value = v;
              onDirty?.();
            }}
            placeholder="smtp.example.com"
          />
        </Field>
        <Field label="SMTP Port">
          <TextField
            value={String(port)}
            onInput={(v) => {
              smtpPort.value = parseInt(v, 10) || 587;
              onDirty?.();
            }}
          />
        </Field>
        <Field label="SMTP Username">
          <TextField
            value={user}
            onInput={(v) => {
              smtpUser.value = v;
              onDirty?.();
            }}
          />
        </Field>
        <Field label="SMTP Password">
          <TextField
            type="password"
            value={pass}
            onInput={(v) => {
              smtpPass.value = v;
              onDirty?.();
            }}
            placeholder={pass === "****" ? "****" : ""}
          />
        </Field>
        <Field label="From Address">
          <TextField
            value={from}
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
              value={String(rate)}
              onInput={(v) => {
                alertRate.value = parseInt(v, 10) || 5;
                onDirty?.();
              }}
            />
          </Field>
        )}
        {alertRecipients && (
          <div className="sm:col-span-2">
            <Field label="Recipients" hint="Comma-separated email addresses">
              <TextField
                value={recipients}
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
