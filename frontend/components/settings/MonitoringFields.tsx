/** @jsxImportSource react */
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";

interface MonitoringFieldsProps {
  promUrl: string;
  onPromUrlChange: (v: string) => void;
  grafUrl: string;
  onGrafUrlChange: (v: string) => void;
  grafToken: string;
  onGrafTokenChange: (v: string) => void;
  /** The namespace field shows only when both of its props are passed. */
  monNs?: string;
  onMonNsChange?: (v: string) => void;
  onDirty?: () => void;
}

export function MonitoringFields({
  promUrl,
  onPromUrlChange,
  grafUrl,
  onGrafUrlChange,
  grafToken,
  onGrafTokenChange,
  monNs,
  onMonNsChange,
  onDirty,
}: MonitoringFieldsProps) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field label="Prometheus URL">
        <TextField
          value={promUrl}
          onInput={(v) => {
            onPromUrlChange(v);
            onDirty?.();
          }}
          placeholder="http://prometheus:9090"
        />
      </Field>
      <Field label="Grafana URL">
        <TextField
          value={grafUrl}
          onInput={(v) => {
            onGrafUrlChange(v);
            onDirty?.();
          }}
          placeholder="http://grafana:3000"
        />
      </Field>
      <Field label="Grafana API Token">
        <TextField
          type="password"
          value={grafToken}
          onInput={(v) => {
            onGrafTokenChange(v);
            onDirty?.();
          }}
          placeholder={grafToken === "****" ? "****" : "Enter token"}
        />
      </Field>
      {monNs !== undefined && onMonNsChange && (
        <Field label="Monitoring Namespace">
          <TextField
            value={monNs}
            onInput={(v) => {
              onMonNsChange(v);
              onDirty?.();
            }}
            placeholder="monitoring"
          />
        </Field>
      )}
    </div>
  );
}
