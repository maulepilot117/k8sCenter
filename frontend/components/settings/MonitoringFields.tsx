/** @jsxImportSource react */
import { type Signal, signal } from "@preact/signals-core";
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface MonitoringFieldsProps {
  promUrl: Signal<string>;
  grafUrl: Signal<string>;
  grafToken: Signal<string>;
  monNs?: Signal<string>;
  onDirty?: () => void;
}

// Stand-in so the hook runs unconditionally when monNs is absent; the field is
// not rendered in that case.
const NO_TEXT = signal("");

export function MonitoringFields({
  promUrl,
  grafUrl,
  grafToken,
  monNs,
  onDirty,
}: MonitoringFieldsProps) {
  const prom = useSignalValue(promUrl);
  const graf = useSignalValue(grafUrl);
  const token = useSignalValue(grafToken);
  const ns = useSignalValue(monNs ?? NO_TEXT);

  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field label="Prometheus URL">
        <TextField
          value={prom}
          onInput={(v) => {
            promUrl.value = v;
            onDirty?.();
          }}
          placeholder="http://prometheus:9090"
        />
      </Field>
      <Field label="Grafana URL">
        <TextField
          value={graf}
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
          value={token}
          onInput={(v) => {
            grafToken.value = v;
            onDirty?.();
          }}
          placeholder={token === "****" ? "****" : "Enter token"}
        />
      </Field>
      {monNs && (
        <Field label="Monitoring Namespace">
          <TextField
            value={ns}
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
