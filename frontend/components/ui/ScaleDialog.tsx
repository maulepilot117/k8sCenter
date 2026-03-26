interface ScaleDialogProps {
  resourceName: string;
  currentReplicas: number | undefined;
  value: number;
  onValueChange: (v: number) => void;
  loading: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ScaleDialog({
  resourceName,
  currentReplicas,
  value,
  onValueChange,
  loading,
  onConfirm,
  onCancel,
}: ScaleDialogProps) {
  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-label={`Scale ${resourceName}`}
      onClick={onCancel}
    >
      <div
        class="w-full max-w-sm rounded-lg bg-surface p-6 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 class="text-lg font-semibold text-text-primary">
          Scale {resourceName}
        </h3>
        <div class="mt-4">
          <label class="block text-sm text-text-secondary">
            Replicas
          </label>
          <input
            type="number"
            min="0"
            max="1000"
            value={value}
            onInput={(e) => {
              const raw = parseInt((e.target as HTMLInputElement).value);
              onValueChange(
                Number.isNaN(raw) ? 0 : Math.min(Math.max(raw, 0), 1000),
              );
            }}
            class="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
          />
          <p class="mt-1 text-xs text-text-muted">
            Current: {currentReplicas ?? "?"}
          </p>
        </div>
        <div class="mt-6 flex justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            class="rounded-md border border-border-primary px-4 py-2 text-sm font-medium text-text-secondary hover:bg-hover"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={loading}
            onClick={onConfirm}
            class="rounded-md bg-brand px-4 py-2 text-sm font-medium text-white hover:bg-brand/90 disabled:opacity-50"
          >
            {loading ? "..." : "Scale"}
          </button>
        </div>
      </div>
    </div>
  );
}
