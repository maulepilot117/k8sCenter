import { useSignal } from"@preact/signals";
import { useCallback } from"preact/hooks";
import { IS_BROWSER } from"fresh/runtime";
import { apiPost } from"@/lib/api.ts";
import { selectedNamespace } from"@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from"@/lib/wizard-constants.ts";
import { useNamespaces } from"@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from"@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from"@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from"@/components/wizard/WizardReviewStep.tsx";
import { Button } from"@/components/ui/Button.tsx";

interface KeyValueEntry {
 key: string;
 value: string;
}

interface ConfigMapFormState {
 name: string;
 namespace: string;
 entries: KeyValueEntry[];
}

const STEPS = [
 { title:"Configure" },
 { title:"Review" },
];

function initialState(): ConfigMapFormState {
 const ns = IS_BROWSER && selectedNamespace.value !=="all"
 ? selectedNamespace.value
 :"default";
 return {
 name:"",
 namespace: ns,
 entries: [{ key:"", value:"" }],
 };
}

export default function ConfigMapWizard() {
 const currentStep = useSignal(0);
 const form = useSignal<ConfigMapFormState>(initialState());
 const errors = useSignal<Record<string, string>>({});
 const dirty = useSignal(false);

 const namespaces = useNamespaces();

 const previewYaml = useSignal("");
 const previewLoading = useSignal(false);
 const previewError = useSignal<string | null>(null);

 useDirtyGuard(dirty);

 const updateField = useCallback((field: string, value: unknown) => {
 dirty.value = true;
 form.value = { ...form.value, [field]: value };
 }, []);

 const updateEntry = useCallback(
 (index: number, field:"key" |"value", val: string) => {
 dirty.value = true;
 const entries = [...form.value.entries];
 entries[index] = { ...entries[index], [field]: val };
 form.value = { ...form.value, entries };
 },
 [],
 );

 const addEntry = useCallback(() => {
 dirty.value = true;
 form.value = {
 ...form.value,
 entries: [...form.value.entries, { key:"", value:"" }],
 };
 }, []);

 const removeEntry = useCallback((index: number) => {
 dirty.value = true;
 const entries = form.value.entries.filter((_, i) => i !== index);
 form.value = {
 ...form.value,
 entries: entries.length > 0 ? entries : [{ key:"", value:"" }],
 };
 }, []);

 const validateStep = (): boolean => {
 const f = form.value;
 const errs: Record<string, string> = {};

 if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
 errs.name =
"Must be lowercase alphanumeric with hyphens, 1-63 characters";
 }
 if (!f.namespace) errs.namespace ="Required";

 const KEY_REGEX = /^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,251}[a-zA-Z0-9])?$/;
 const seenKeys = new Set<string>();
 let totalSize = 0;

 for (let i = 0; i < f.entries.length; i++) {
 const entry = f.entries[i];
 if (!entry.key && !entry.value) continue; // skip empty rows
 if (!entry.key) {
 errs[`entry_${i}_key`] ="Key is required";
 } else if (!KEY_REGEX.test(entry.key)) {
 errs[`entry_${i}_key`] =
"Must be alphanumeric with hyphens, underscores, or dots";
 } else if (seenKeys.has(entry.key)) {
 errs[`entry_${i}_key`] ="Duplicate key";
 } else {
 seenKeys.add(entry.key);
 }
 totalSize += entry.key.length + entry.value.length;
 }

 if (totalSize > 1024 * 1024) {
 errs.data ="Total data size must be less than 1MB";
 }

 errors.value = errs;
 return Object.keys(errs).length === 0;
 };

 const goNext = async () => {
 if (!validateStep()) return;
 currentStep.value = 1;
 await fetchPreview();
 };

 const goBack = () => {
 if (currentStep.value > 0) currentStep.value = 0;
 };

 const fetchPreview = async () => {
 previewLoading.value = true;
 previewError.value = null;

 const f = form.value;
 const data: Record<string, string> = {};
 for (const entry of f.entries) {
 if (entry.key) {
 data[entry.key] = entry.value;
 }
 }

 const payload = {
 name: f.name,
 namespace: f.namespace,
 data,
 };

 try {
 const resp = await apiPost<{ yaml: string }>(
"/v1/wizards/configmap/preview",
 payload,
 );
 previewYaml.value = resp.data.yaml;
 } catch (err) {
 previewError.value = err instanceof Error
 ? err.message
 :"Failed to generate preview";
 } finally {
 previewLoading.value = false;
 }
 };

 if (!IS_BROWSER) {
 return <div class="p-6">Loading wizard...</div>;
 }

 return (
 <div class="p-6">
 <div class="mb-6 flex items-center justify-between">
 <h1 class="text-2xl font-bold text-text-primary">
 Create ConfigMap
 </h1>
 <a
 href="/config/configmaps"
 class="text-sm text-text-muted hover:text-text-primary"
 >
 Cancel
 </a>
 </div>

 <WizardStepper
 steps={STEPS}
 currentStep={currentStep.value}
 onStepClick={(step) => {
 if (step < currentStep.value) currentStep.value = step;
 }}
 />

 <div class="mt-6">
 {currentStep.value === 0 && (
 <div class="mx-auto max-w-lg space-y-4">
 <div>
 <label class="block text-sm font-medium text-text-secondary">
 Name <span class="text-red-500">*</span>
 </label>
 <input
 type="text"
 value={form.value.name}
 onInput={(e) =>
 updateField("name", (e.target as HTMLInputElement).value)}
 class={WIZARD_INPUT_CLASS}
 placeholder="e.g. my-config"
 />
 {errors.value.name && (
 <p class="mt-1 text-xs text-red-500">{errors.value.name}</p>
 )}
 </div>

 <div>
 <label class="block text-sm font-medium text-text-secondary">
 Namespace <span class="text-red-500">*</span>
 </label>
 <select
 value={form.value.namespace}
 onChange={(e) =>
 updateField(
"namespace",
 (e.target as HTMLSelectElement).value,
 )}
 class={WIZARD_INPUT_CLASS}
 >
 {namespaces.value.map((ns) => (
 <option key={ns} value={ns}>{ns}</option>
 ))}
 </select>
 </div>

 <div>
 <label class="block text-sm font-medium text-text-secondary">
 Data
 </label>
 <div class="mt-2 space-y-3">
 {form.value.entries.map((entry, i) => (
 <div key={i} class="flex gap-2 items-start">
 <div class="flex-1">
 <input
 type="text"
 value={entry.key}
 onInput={(e) =>
 updateEntry(
 i,
"key",
 (e.target as HTMLInputElement).value,
 )}
 class={WIZARD_INPUT_CLASS}
 placeholder="Key"
 />
 {errors.value[`entry_${i}_key`] && (
 <p class="mt-1 text-xs text-red-500">
 {errors.value[`entry_${i}_key`]}
 </p>
 )}
 </div>
 <div class="flex-1">
 <textarea
 value={entry.value}
 onInput={(e) =>
 updateEntry(
 i,
"value",
 (e.target as HTMLTextAreaElement).value,
 )}
 class={WIZARD_INPUT_CLASS +" min-h-[38px] resize-y"}
 placeholder="Value"
 rows={1}
 />
 </div>
 <button
 type="button"
 onClick={() =>
 removeEntry(i)}
 class="mt-1 rounded p-2 text-text-muted hover:bg-danger-dim hover:text-red-500"
 title="Remove entry"
 >
 <svg
 class="h-4 w-4"
 fill="none"
 viewBox="0 0 24 24"
 stroke="currentColor"
 >
 <path
 stroke-linecap="round"
 stroke-linejoin="round"
 stroke-width={2}
 d="M6 18L18 6M6 6l12 12"
 />
 </svg>
 </button>
 </div>
 ))}
 </div>
 {errors.value.data && (
 <p class="mt-1 text-xs text-red-500">{errors.value.data}</p>
 )}
 <button
 type="button"
 onClick={addEntry}
 class="mt-2 text-sm text-brand hover:text-brand/80"
 >
 + Add entry
 </button>
 </div>
 </div>
 )}

 {currentStep.value === 1 && (
 <WizardReviewStep
 yaml={previewYaml.value}
 onYamlChange={(v) => {
 previewYaml.value = v;
 }}
 loading={previewLoading.value}
 error={previewError.value}
 detailBasePath="/config/configmaps"
 />
 )}
 </div>

 {currentStep.value === 0 && (
 <div class="mt-8 flex justify-end">
 <Button variant="primary" onClick={goNext}>
 Preview YAML
 </Button>
 </div>
 )}

 {currentStep.value === 1 && !previewLoading.value &&
 previewError.value === null && (
 <div class="mt-4 flex justify-start">
 <Button variant="ghost" onClick={goBack}>
 Back
 </Button>
 </div>
 )}
 </div>
 );
}
