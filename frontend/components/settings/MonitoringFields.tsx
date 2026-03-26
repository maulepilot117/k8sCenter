import type { Signal } from"@preact/signals";
import { settingsInputClass } from"@/components/settings/shared.ts";

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
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 Prometheus URL
 </label>
 <input
 type="url"
 value={promUrl.value}
 onInput={(e) => {
 promUrl.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 placeholder="http://prometheus:9090"
 class={settingsInputClass}
 />
 </div>
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 Grafana URL
 </label>
 <input
 type="url"
 value={grafUrl.value}
 onInput={(e) => {
 grafUrl.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 placeholder="http://grafana:3000"
 class={settingsInputClass}
 />
 </div>
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 Grafana API Token
 </label>
 <input
 type="password"
 value={grafToken.value}
 onInput={(e) => {
 grafToken.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 placeholder={grafToken.value ==="****" ?"****" :"Enter token"}
 class={settingsInputClass}
 />
 </div>
 {monNs && (
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 Monitoring Namespace
 </label>
 <input
 type="text"
 value={monNs.value}
 onInput={(e) => {
 monNs.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 placeholder="monitoring"
 class={settingsInputClass}
 />
 </div>
 )}
 </div>
 );
}
