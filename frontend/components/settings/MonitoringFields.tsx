import type { Signal } from "@preact/signals";
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";

interface MonitoringFieldsProps {
  promUrl: Signal<string>;
  grafUrl: Signal<string>;
  grafToken: Signal<string>;
  monNs?: Signal<string>;
  onDirty?: () => void;
}

export function MonitoringFields(
  { promUrl, grafUrl, grafToken, monNs, onDirty }: MonitoringFieldsProps,
) {
  return (
    <div class="grid gap-4 sm:grid-cols-2">
      <Field label="Prometheus URL">
        <TextField
          value={promUrl.value}
          onInput={(v) => {
            promUrl.value = v;
            onDirty?.();
          }}
          placeholder="http://prometheus:9090"
        />
      </Field>
      <Field label="Grafana URL">
        <TextField
          value={grafUrl.value}
          onInput={(v) => {
            grafUrl.value = v;
            onDirty?.();
          }}
          placeholder="http://grafana:3000"
        />
      </Field>
      <Field label="Grafana API Token">
        <TextField
          type="password"
          value={grafToken.value}
          onInput={(v) => {
            grafToken.value = v;
            onDirty?.();
          }}
          placeholder={grafToken.value === "****" ? "****" : "Enter token"}
        />
      </Field>
      {monNs && (
        <Field label="Monitoring Namespace">
          <TextField
            value={monNs.value}
            onInput={(v) => {
              monNs.value = v;
              onDirty?.();
            }}
            placeholder="monitoring"
          />
        </Field>
      )}
    </div>
  );
}
