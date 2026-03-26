import type { Signal } from"@preact/signals";
import { settingsInputClass } from"@/components/settings/shared.ts";

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
 <div class="mb-4">
 <label class="flex items-center gap-2 text-sm text-text-secondary">
 <input
 type="checkbox"
 checked={alertEnabled.value}
 onChange={(e) => {
 alertEnabled.value = (e.target as HTMLInputElement).checked;
 onDirty?.();
 }}
 />
 Enable email alerting
 </label>
 </div>
 <div class="grid gap-4 sm:grid-cols-2">
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 SMTP Host
 </label>
 <input
 type="text"
 value={smtpHost.value}
 onInput={(e) => {
 smtpHost.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 placeholder="smtp.example.com"
 class={settingsInputClass}
 />
 </div>
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 SMTP Port
 </label>
 <input
 type="number"
 value={smtpPort.value}
 onInput={(e) => {
 smtpPort.value = parseInt((e.target as HTMLInputElement).value) ||
 587;
 onDirty?.();
 }}
 class={settingsInputClass}
 />
 </div>
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 SMTP Username
 </label>
 <input
 type="text"
 value={smtpUser.value}
 onInput={(e) => {
 smtpUser.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 class={settingsInputClass}
 />
 </div>
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 SMTP Password
 </label>
 <input
 type="password"
 value={smtpPass.value}
 onInput={(e) => {
 smtpPass.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 placeholder={smtpPass.value ==="****" ?"****" :""}
 class={settingsInputClass}
 />
 </div>
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 From Address
 </label>
 <input
 type="email"
 value={smtpFrom.value}
 onInput={(e) => {
 smtpFrom.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 placeholder="alerts@example.com"
 class={settingsInputClass}
 />
 </div>
 {alertRate && (
 <div>
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 Rate Limit (per hour)
 </label>
 <input
 type="number"
 value={alertRate.value}
 onInput={(e) => {
 alertRate.value =
 parseInt((e.target as HTMLInputElement).value) || 5;
 onDirty?.();
 }}
 min="1"
 max="100"
 class={settingsInputClass}
 />
 </div>
 )}
 {alertRecipients && (
 <div class="sm:col-span-2">
 <label class="mb-1 block text-sm font-medium text-text-secondary">
 Recipients (comma-separated)
 </label>
 <input
 type="text"
 value={alertRecipients.value}
 onInput={(e) => {
 alertRecipients.value = (e.target as HTMLInputElement).value;
 onDirty?.();
 }}
 placeholder="admin@example.com, ops@example.com"
 class={settingsInputClass}
 />
 </div>
 )}
 </div>
 </>
 );
}
