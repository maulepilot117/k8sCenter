import { useEffect, useRef, useState } from "preact/hooks";
import { Input } from "@/components/ui/Input.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type {
  ExternalSecretWizardForm,
  ExternalSecretWizardStoreOption,
} from "@/islands/ExternalSecretWizard.tsx";

interface ExternalSecretFormProps {
  form: ExternalSecretWizardForm;
  errors: Record<string, string>;
  namespaces: string[];
  stores: ExternalSecretWizardStoreOption[];
  storesLoading: boolean;
  onUpdate: (field: keyof ExternalSecretWizardForm, value: unknown) => void;
  onUpdateData: (
    index: number,
    field: "secretKey" | "key" | "property",
    value: string,
  ) => void;
  onAddDataItem: () => void;
  onRemoveDataItem: (index: number) => void;
  onPathFieldTouched: (index: number) => void;
}

const PATH_DEBOUNCE_MS = 300;

interface PathDiscoveryState {
  supported: boolean;
  provider: string;
  paths: string[];
  loading: boolean;
  error: string | null;
}

/** Helper text shown below the path field when path-discovery is unsupported. */
const FREE_TEXT_HELPER =
  "This provider doesn't expose path discovery. Enter the remote key path manually (e.g. `secret/data/myapp/db`).";

export function ExternalSecretForm({
  form,
  errors,
  namespaces,
  stores,
  storesLoading,
  onUpdate,
  onUpdateData,
  onAddDataItem,
  onRemoveDataItem,
  onPathFieldTouched,
}: ExternalSecretFormProps) {
  // Path-discovery state — keyed by data-item index. The Kubernetes provider
  // typeahead populates `paths`; other providers leave `supported` false so
  // the row renders a free-text input with helper text.
  const [discovery, setDiscovery] = useState<PathDiscoveryState>({
    supported: false,
    provider: "",
    paths: [],
    loading: false,
    error: null,
  });

  // Resolve the selected store (by name + kind) to surface its provider so the
  // form can decide whether to enable typeahead.
  const selectedStore = stores.find(
    (s) => s.name === form.storeRefName && s.kind === form.storeRefKind,
  );

  // Track the latest fetch sequence so a stale response cannot overwrite a
  // newer one. AbortController cancels in-flight fetches when the user keeps
  // typing.
  const fetchSeq = useRef(0);
  const abortRef = useRef<AbortController | null>(null);
  const debounceHandle = useRef<number | null>(null);

  // Probe path-discovery support whenever the selected store changes. A
  // kubernetes-provider store flips `supported=true` and seeds an initial
  // (prefix-empty) listing; other providers report `supported=false`.
  useEffect(() => {
    if (!selectedStore || selectedStore.kind !== "SecretStore") {
      // ClusterSecretStore + missing store: typeahead off until the user picks
      // a namespaced store. ClusterSecretStores can't be probed by this
      // endpoint (it requires a namespace).
      setDiscovery({
        supported: false,
        provider: selectedStore?.provider ?? "",
        paths: [],
        loading: false,
        error: null,
      });
      return;
    }
    let cancelled = false;
    setDiscovery((d) => ({ ...d, loading: true, error: null }));
    esoApi
      .listStorePaths(selectedStore.namespace ?? "", selectedStore.name)
      .then((resp) => {
        if (cancelled) return;
        setDiscovery({
          supported: !!resp.data.supported,
          provider: resp.data.provider ?? "",
          paths: resp.data.paths ?? [],
          loading: false,
          error: null,
        });
      })
      .catch(() => {
        if (cancelled) return;
        // Degrade to free-text on error — surface a notice so the user knows
        // why the typeahead isn't kicking in.
        setDiscovery({
          supported: false,
          provider: selectedStore.provider,
          paths: [],
          loading: false,
          error: "Couldn't load paths — enter manually",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [selectedStore?.name, selectedStore?.namespace, selectedStore?.kind]);

  // Re-fetch with a debounced prefix when the user types in any data-item
  // path field on a Kubernetes-provider store.
  function handlePathInput(index: number, value: string) {
    onUpdateData(index, "key", value);
    onPathFieldTouched(index);

    if (
      !discovery.supported || !selectedStore ||
      selectedStore.kind !== "SecretStore"
    ) {
      return;
    }

    if (debounceHandle.current !== null) {
      clearTimeout(debounceHandle.current);
    }
    debounceHandle.current = setTimeout(() => {
      debounceHandle.current = null;

      // Cancel previous in-flight fetch.
      if (abortRef.current) abortRef.current.abort();
      abortRef.current = new AbortController();
      const seq = ++fetchSeq.current;

      setDiscovery((d) => ({ ...d, loading: true, error: null }));
      esoApi
        .listStorePaths(
          selectedStore.namespace ?? "",
          selectedStore.name,
          value,
        )
        .then((resp) => {
          if (seq !== fetchSeq.current) return;
          setDiscovery({
            supported: !!resp.data.supported,
            provider: resp.data.provider ?? "",
            paths: resp.data.paths ?? [],
            loading: false,
            error: null,
          });
        })
        .catch(() => {
          if (seq !== fetchSeq.current) return;
          setDiscovery({
            supported: false,
            provider: selectedStore.provider,
            paths: [],
            loading: false,
            error: "Couldn't load paths — enter manually",
          });
        });
    }, PATH_DEBOUNCE_MS);
  }

  // Cleanup pending timer + abort controller on unmount.
  useEffect(() => {
    return () => {
      if (debounceHandle.current !== null) clearTimeout(debounceHandle.current);
      if (abortRef.current) abortRef.current.abort();
    };
  }, []);

  // Auto-derive targetSecretName from name until user touches it.
  function handleNameChange(value: string) {
    onUpdate("name", value);
    if (!form.targetSecretNameTouched) {
      onUpdate("targetSecretName", value);
    }
  }

  return (
    <div class="space-y-5">
      <div class="grid grid-cols-2 gap-4">
        <Input
          id="es-name"
          label="Name"
          required
          value={form.name}
          onInput={(e) =>
            handleNameChange((e.target as HTMLInputElement).value)}
          placeholder="my-app-config"
          error={errors.name}
        />
        <NamespaceSelect
          value={form.namespace}
          namespaces={namespaces}
          error={errors.namespace}
          onChange={(ns) => onUpdate("namespace", ns)}
        />
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div class="space-y-1">
          <label
            for="es-store-kind"
            class="block text-sm font-medium text-text-secondary"
          >
            Store kind
          </label>
          <select
            id="es-store-kind"
            class="block w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
            value={form.storeRefKind}
            onChange={(e) =>
              onUpdate(
                "storeRefKind",
                (e.target as HTMLSelectElement).value as
                  | "SecretStore"
                  | "ClusterSecretStore",
              )}
          >
            <option value="SecretStore">SecretStore</option>
            <option value="ClusterSecretStore">ClusterSecretStore</option>
          </select>
        </div>

        <div class="space-y-1">
          <label
            for="es-store-name"
            class="block text-sm font-medium text-text-secondary"
          >
            Store
            <span aria-hidden="true" class="text-danger ml-0.5">*</span>
          </label>
          <select
            id="es-store-name"
            class="block w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
            value={form.storeRefName}
            onChange={(e) =>
              onUpdate("storeRefName", (e.target as HTMLSelectElement).value)}
            aria-invalid={errors["storeRef.name"] ? "true" : undefined}
            aria-describedby={errors["storeRef.name"]
              ? "es-store-name-error"
              : undefined}
            disabled={storesLoading}
          >
            <option value="">
              {storesLoading ? "Loading…" : "Select a store"}
            </option>
            {stores
              .filter((s) => s.kind === form.storeRefKind)
              .map((s) => (
                <option
                  key={`${s.kind}:${s.namespace ?? ""}:${s.name}`}
                  value={s.name}
                >
                  {s.name}
                  {s.namespace ? ` (${s.namespace})` : ""}
                  {s.provider ? ` — ${s.provider}` : ""}
                </option>
              ))}
          </select>
          {errors["storeRef.name"] && (
            <p id="es-store-name-error" class="text-sm text-danger">
              {errors["storeRef.name"]}
            </p>
          )}
        </div>
      </div>

      <div class="grid grid-cols-2 gap-4">
        <Input
          id="es-target"
          label="Target Secret name"
          required
          value={form.targetSecretName}
          onInput={(e) => {
            onUpdate(
              "targetSecretName",
              (e.target as HTMLInputElement).value,
            );
            onUpdate("targetSecretNameTouched", true);
          }}
          placeholder={form.name || "my-app-config"}
          description="The Kubernetes Secret ESO will create or update."
          error={errors.targetSecretName}
        />
        <Input
          id="es-refresh"
          label="Refresh interval"
          value={form.refreshInterval}
          onInput={(e) =>
            onUpdate(
              "refreshInterval",
              (e.target as HTMLInputElement).value,
            )}
          placeholder="1h"
          description="Go duration. Leave blank to use ESO's default. Use `0` to disable polling."
          error={errors.refreshInterval}
        />
      </div>

      <div class="space-y-3">
        <div class="flex items-center justify-between">
          <h3 class="text-sm font-semibold text-text-primary">Data items</h3>
          <Button variant="ghost" onClick={onAddDataItem}>
            + Add data item
          </Button>
        </div>
        {errors.data && <p class="text-sm text-danger">{errors.data}</p>}

        {form.data.map((item, idx) => (
          <div
            key={idx}
            class="rounded-md border border-border-primary p-4 space-y-3"
          >
            <div class="flex items-center justify-between">
              <span class="text-xs uppercase tracking-wide text-text-muted">
                Data item {idx + 1}
              </span>
              {form.data.length > 1 && (
                <button
                  type="button"
                  class="text-xs text-text-muted hover:text-danger"
                  onClick={() => onRemoveDataItem(idx)}
                >
                  Remove
                </button>
              )}
            </div>
            <div class="grid grid-cols-2 gap-3">
              <Input
                id={`es-data-${idx}-secretkey`}
                label="Secret key"
                required
                value={item.secretKey}
                onInput={(e) =>
                  onUpdateData(
                    idx,
                    "secretKey",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="DB_PASSWORD"
                error={errors[`data[${idx}].secretKey`]}
              />
              <Input
                id={`es-data-${idx}-property`}
                label="Property (optional)"
                value={item.property}
                onInput={(e) =>
                  onUpdateData(
                    idx,
                    "property",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="db_password"
                description="Sub-key inside the remote object. Leave blank to use the whole value."
              />
            </div>
            <div class="space-y-1">
              <label
                for={`es-data-${idx}-key`}
                class="block text-sm font-medium text-text-secondary"
              >
                Remote key
                <span aria-hidden="true" class="text-danger ml-0.5">*</span>
              </label>
              <input
                id={`es-data-${idx}-key`}
                type="text"
                list={discovery.supported ? `es-data-${idx}-paths` : undefined}
                value={item.key}
                onInput={(e) =>
                  handlePathInput(idx, (e.target as HTMLInputElement).value)}
                placeholder={discovery.supported
                  ? "Start typing to search…"
                  : "secret/data/myapp"}
                class="block w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-brand/50"
                aria-invalid={errors[`data[${idx}].remoteRef.key`]
                  ? "true"
                  : undefined}
                aria-describedby={`es-data-${idx}-hint`}
              />
              {discovery.supported && (
                <datalist id={`es-data-${idx}-paths`}>
                  {discovery.paths.map((p) => <option key={p} value={p} />)}
                </datalist>
              )}
              <p id={`es-data-${idx}-hint`} class="text-xs text-text-muted">
                {discovery.loading
                  ? "Loading paths…"
                  : discovery.error
                  ? discovery.error
                  : discovery.supported
                  ? discovery.paths.length === 0
                    ? "No paths found in this namespace"
                    : `${discovery.paths.length} path${
                      discovery.paths.length === 1 ? "" : "s"
                    } available`
                  : FREE_TEXT_HELPER}
              </p>
              {errors[`data[${idx}].remoteRef.key`] && (
                <p class="text-sm text-danger">
                  {errors[`data[${idx}].remoteRef.key`]}
                </p>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
