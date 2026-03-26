import { useSignal } from"@preact/signals";
import { useCallback, useEffect } from"preact/hooks";
import { IS_BROWSER } from"fresh/runtime";
import { apiGet, apiPost } from"@/lib/api.ts";
import { selectedNamespace } from"@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from"@/lib/wizard-constants.ts";
import { useNamespaces } from"@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from"@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from"@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from"@/components/wizard/WizardReviewStep.tsx";
import { Button } from"@/components/ui/Button.tsx";

interface ScheduledSnapshotFormState {
 name: string;
 namespace: string;
 sourcePVC: string;
 volumeSnapshotClassName: string;
 schedulePreset: string;
 schedule: string;
 retentionCount: number;
}

interface PVCItem {
 metadata: { name: string; namespace: string };
 status?: { phase?: string };
}

interface SnapshotClassItem {
 metadata: { name: string };
 driver?: string;
}

const STEPS = [
 { title:"Source & Schedule" },
 { title:"Retention" },
 { title:"Review" },
];

const SCHEDULE_PRESETS: { label: string; value: string }[] = [
 { label:"Every hour", value:"0 * * * *" },
 { label:"Daily midnight", value:"0 0 * * *" },
 { label:"Weekly Sunday", value:"0 0 * * 0" },
 { label:"Custom", value:"" },
];

function cronToHuman(cron: string): string {
 const trimmed = cron.trim();
 if (trimmed ==="0 * * * *") return"Every hour, at minute 0";
 if (trimmed ==="0 0 * * *") return"Every day at midnight";
 if (trimmed ==="0 0 * * 0") return"Every Sunday at midnight";
 if (!trimmed) return"";
 return `Cron: ${trimmed}`;
}

function initialState(): ScheduledSnapshotFormState {
 const ns = IS_BROWSER && selectedNamespace.value !=="all"
 ? selectedNamespace.value
 :"default";
 return {
 name:"",
 namespace: ns,
 sourcePVC:"",
 volumeSnapshotClassName:"",
 schedulePreset:"0 0 * * *",
 schedule:"0 0 * * *",
 retentionCount: 5,
 };
}

export default function ScheduledSnapshotWizard() {
 const currentStep = useSignal(0);
 const form = useSignal<ScheduledSnapshotFormState>(initialState());
 const errors = useSignal<Record<string, string>>({});
 const dirty = useSignal(false);

 const namespaces = useNamespaces();
 const pvcs = useSignal<PVCItem[]>([]);
 const snapshotClasses = useSignal<SnapshotClassItem[]>([]);

 const snapshotsAvailable = useSignal(true);

 const previewYaml = useSignal("");
 const previewLoading = useSignal(false);
 const previewError = useSignal<string | null>(null);

 // Fetch PVCs when namespace changes (bound only)
 useEffect(() => {
 if (!IS_BROWSER) return;
 const ns = form.value.namespace;
 if (!ns) return;
 apiGet<PVCItem[]>(`/v1/resources/pvcs/${ns}?limit=500`)
 .then((resp) => {
 if (Array.isArray(resp.data)) {
 pvcs.value = resp.data.filter((p) => p.status?.phase ==="Bound");
 // Reset sourcePVC if it's not in the new list
 if (
 form.value.sourcePVC &&
 !pvcs.value.some((p) => p.metadata.name === form.value.sourcePVC)
 ) {
 form.value = { ...form.value, sourcePVC:"" };
 }
 }
 })
 .catch(() => {
 pvcs.value = [];
 });
 }, [form.value.namespace]);

 // Fetch snapshot classes
 useEffect(() => {
 if (!IS_BROWSER) return;
 apiGet<
 { data: SnapshotClassItem[]; metadata: { available: boolean } }
 >("/v1/storage/snapshot-classes")
 .then((resp) => {
 if (resp.data?.metadata?.available === false) {
 snapshotsAvailable.value = false;
 return;
 }
 const classes = resp.data?.data;
 if (Array.isArray(classes)) {
 snapshotClasses.value = classes;
 if (classes.length > 0 && !form.value.volumeSnapshotClassName) {
 form.value = {
 ...form.value,
 volumeSnapshotClassName: classes[0].name,
 };
 }
 }
 })
 .catch(() => {});
 }, []);

 useDirtyGuard(dirty);

 const updateField = useCallback((field: string, value: unknown) => {
 dirty.value = true;
 form.value = { ...form.value, [field]: value };
 }, []);

 const validateStep0 = (): boolean => {
 const f = form.value;
 const errs: Record<string, string> = {};

 if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
 errs.name =
"Must be lowercase alphanumeric with hyphens, 1-63 characters";
 }
 if (!f.namespace) errs.namespace ="Required";
 if (!f.sourcePVC) errs.sourcePVC ="Required";
 if (!f.volumeSnapshotClassName) {
 errs.volumeSnapshotClassName ="Required";
 }
 if (!f.schedule.trim()) errs.schedule ="Required";

 errors.value = errs;
 return Object.keys(errs).length === 0;
 };

 const validateStep1 = (): boolean => {
 const f = form.value;
 const errs: Record<string, string> = {};

 if (f.retentionCount < 1 || f.retentionCount > 100) {
 errs.retentionCount ="Must be between 1 and 100";
 }

 errors.value = errs;
 return Object.keys(errs).length === 0;
 };

 const goNext = async () => {
 if (currentStep.value === 0) {
 if (!validateStep0()) return;
 currentStep.value = 1;
 } else if (currentStep.value === 1) {
 if (!validateStep1()) return;
 currentStep.value = 2;
 await fetchPreview();
 }
 };

 const goBack = () => {
 if (currentStep.value > 0) {
 currentStep.value = currentStep.value - 1;
 }
 };

 const fetchPreview = async () => {
 previewLoading.value = true;
 previewError.value = null;

 const f = form.value;
 const payload = {
 name: f.name,
 namespace: f.namespace,
 sourcePVC: f.sourcePVC,
 volumeSnapshotClassName: f.volumeSnapshotClassName,
 schedule: f.schedule,
 retentionCount: f.retentionCount,
 };

 try {
 const resp = await apiPost<{ yaml: string }>(
"/v1/wizards/scheduled-snapshot/preview",
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

 if (!snapshotsAvailable.value) {
 return (
 <div class="p-6">
 <div class="rounded-lg border border-warning bg-warning-dim p-6 text-center">
 <p class="text-lg font-medium text-warning">
 VolumeSnapshot CRDs Not Installed
 </p>
 <p class="mt-2 text-sm text-warning">
 This cluster does not have the snapshot.storage.k8s.io CRDs
 installed. VolumeSnapshot support is required for scheduled
 snapshots.
 </p>
 <a
 href="/storage/snapshots"
 class="mt-4 inline-block text-sm text-amber-600 hover:text-amber-800 text-warning"
 >
 Back to Snapshots
 </a>
 </div>
 </div>
 );
 }

 return (
 <div class="p-6">
 <div class="mb-6 flex items-center justify-between">
 <h1 class="text-2xl font-bold text-text-primary">
 Schedule Snapshot
 </h1>
 <a
 href="/storage/snapshots"
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
 {/* Step 0: Source & Schedule */}
 {currentStep.value === 0 && (
 <div class="mx-auto max-w-lg space-y-4">
 <div>
 <label class="block text-sm font-medium text-text-secondary">
 Schedule Name <span class="text-red-500">*</span>
 </label>
 <input
 type="text"
 value={form.value.name}
 onInput={(e) =>
 updateField("name", (e.target as HTMLInputElement).value)}
 class={WIZARD_INPUT_CLASS}
 placeholder="e.g. daily-db-backup"
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
 Source PVC <span class="text-red-500">*</span>
 </label>
 {pvcs.value.length === 0
 ? (
 <p class="mt-1 text-sm text-text-muted">
 No bound PVCs found in namespace {form.value.namespace}
 </p>
 )
 : (
 <select
 value={form.value.sourcePVC}
 onChange={(e) =>
 updateField(
"sourcePVC",
 (e.target as HTMLSelectElement).value,
 )}
 class={WIZARD_INPUT_CLASS}
 >
 <option value="">Select a PVC...</option>
 {pvcs.value.map((p) => (
 <option key={p.metadata.name} value={p.metadata.name}>
 {p.metadata.name}
 </option>
 ))}
 </select>
 )}
 {errors.value.sourcePVC && (
 <p class="mt-1 text-xs text-red-500">
 {errors.value.sourcePVC}
 </p>
 )}
 </div>

 <div>
 <label class="block text-sm font-medium text-text-secondary">
 VolumeSnapshotClass <span class="text-red-500">*</span>
 </label>
 {snapshotClasses.value.length === 0
 ? (
 <p class="mt-1 text-sm text-text-muted">
 No VolumeSnapshotClasses found in cluster
 </p>
 )
 : (
 <select
 value={form.value.volumeSnapshotClassName}
 onChange={(e) =>
 updateField(
"volumeSnapshotClassName",
 (e.target as HTMLSelectElement).value,
 )}
 class={WIZARD_INPUT_CLASS}
 >
 <option value="">Select a snapshot class...</option>
 {snapshotClasses.value.map((sc) => (
 <option
 key={sc.metadata.name}
 value={sc.metadata.name}
 >
 {sc.metadata.name}
 {sc.driver ? ` (${sc.driver})` :""}
 </option>
 ))}
 </select>
 )}
 {errors.value.volumeSnapshotClassName && (
 <p class="mt-1 text-xs text-red-500">
 {errors.value.volumeSnapshotClassName}
 </p>
 )}
 </div>

 <div>
 <label class="block text-sm font-medium text-text-secondary">
 Schedule <span class="text-red-500">*</span>
 </label>
 <div class="mt-2 flex flex-wrap gap-2">
 {SCHEDULE_PRESETS.map((preset) => (
 <button
 key={preset.label}
 type="button"
 onClick={() => {
 if (preset.value) {
 updateField("schedule", preset.value);
 updateField("schedulePreset", preset.value);
 } else {
 updateField("schedulePreset","");
 }
 }}
 class={`rounded-md border px-3 py-1.5 text-sm transition-colors ${
 (preset.value &&
 form.value.schedulePreset === preset.value) ||
 (!preset.value && form.value.schedulePreset ==="")
 ?"border-brand bg-brand/10 text-brand font-medium"
 :"border-border-primary text-text-muted hover:border-border-primary"
 }`}
 >
 {preset.label}
 </button>
 ))}
 </div>
 {form.value.schedulePreset ==="" && (
 <input
 type="text"
 value={form.value.schedule}
 onInput={(e) =>
 updateField(
"schedule",
 (e.target as HTMLInputElement).value,
 )}
 class={`${WIZARD_INPUT_CLASS} mt-2`}
 placeholder="e.g. 0 */6 * * *"
 />
 )}
 {form.value.schedule && (
 <p class="mt-1 text-xs text-text-muted">
 {cronToHuman(form.value.schedule)}
 </p>
 )}
 {errors.value.schedule && (
 <p class="mt-1 text-xs text-red-500">
 {errors.value.schedule}
 </p>
 )}
 </div>
 </div>
 )}

 {/* Step 1: Retention */}
 {currentStep.value === 1 && (
 <div class="mx-auto max-w-lg space-y-4">
 <div>
 <label class="block text-sm font-medium text-text-secondary">
 Retention Count
 </label>
 <input
 type="number"
 min={1}
 max={100}
 value={form.value.retentionCount}
 onInput={(e) => {
 const val = parseInt(
 (e.target as HTMLInputElement).value,
 10,
 );
 if (!isNaN(val)) updateField("retentionCount", val);
 }}
 class="mt-1 w-32 rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
 />
 {errors.value.retentionCount && (
 <p class="mt-1 text-xs text-red-500">
 {errors.value.retentionCount}
 </p>
 )}
 <p class="mt-2 text-sm text-text-muted">
 The number of most recent snapshots to keep. Older snapshots
 will be automatically deleted after each scheduled run.
 </p>
 </div>

 <div class="rounded-md border border-accent bg-accent-dim p-4">
 <p class="text-sm text-accent">
 A CronJob will be created that runs on schedule{""}
 <code class="rounded bg-accent-dim px-1">
 {form.value.schedule}
 </code>, creates a VolumeSnapshot of{""}
 <strong>{form.value.sourcePVC ||"(PVC)"}</strong>, and deletes
 any snapshots beyond the retention count of{""}
 <strong>{form.value.retentionCount}</strong>.
 </p>
 <p class="mt-2 text-sm text-accent">
 This also creates a ServiceAccount, Role, and RoleBinding with
 the minimum permissions needed.
 </p>
 </div>
 </div>
 )}

 {/* Step 2: Review */}
 {currentStep.value === 2 && (
 <WizardReviewStep
 yaml={previewYaml.value}
 onYamlChange={(v) => {
 previewYaml.value = v;
 }}
 loading={previewLoading.value}
 error={previewError.value}
 detailBasePath="/workloads/cronjobs"
 />
 )}
 </div>

 {/* Navigation buttons */}
 {currentStep.value < 2 && (
 <div class="mt-8 flex justify-between">
 {currentStep.value > 0
 ? (
 <Button variant="ghost" onClick={goBack}>
 Back
 </Button>
 )
 : <div />}
 <Button variant="primary" onClick={goNext}>
 {currentStep.value === 1 ?"Preview YAML" :"Next"}
 </Button>
 </div>
 )}

 {currentStep.value === 2 && !previewLoading.value &&
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
