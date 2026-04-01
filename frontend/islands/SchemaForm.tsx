import { useComputed, useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { ApiError, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import type { CRDInfo, SchemaProperty } from "@/lib/crd-types.ts";
import { formStateToYaml, safeDeepSet } from "@/lib/schema-to-yaml.ts";
import { parse as yamlParse, stringify as yamlStringify } from "yaml";
import { showToast } from "@/islands/ToastProvider.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import SchemaFormField from "@/islands/SchemaFormField.tsx";
import YamlPreview from "@/islands/YamlPreview.tsx";
import { inputStyle, selectStyle } from "@/lib/form-styles.ts";
import type { Signal } from "@preact/signals";

// ── Types ───────────────────────────────────────────────────────────────

interface SchemaFormProps {
  group: string;
  resource: string;
  namespace?: string;
  name?: string;
  mode: "create" | "edit";
}

interface CRDGetResponse {
  info: CRDInfo;
  schema: SchemaProperty | null;
}

type ViewMode = "form" | "yaml";

interface KVEntry {
  id: number;
  key: string;
  value: string;
}

// ── Helpers ─────────────────────────────────────────────────────────────

const DANGEROUS_KEYS = new Set(["__proto__", "constructor", "prototype"]);

/** Convert key-value entries to a Record, skipping empty and dangerous keys. */
function toRecord(
  entries: Array<{ key: string; value: string }>,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const { key, value } of entries) {
    if (key && !DANGEROUS_KEYS.has(key)) out[key] = value;
  }
  return out;
}

/** Find duplicate keys in a key-value entry list. O(n). */
function findDuplicateKeys(
  entries: Array<{ key: string }>,
): Set<string> {
  const seen = new Set<string>();
  const dupes = new Set<string>();
  for (const { key } of entries) {
    if (key !== "") {
      if (seen.has(key)) dupes.add(key);
      seen.add(key);
    }
  }
  return dupes;
}

let nextEntryId = 1;

function entriesToKV(
  record: Record<string, string>,
): KVEntry[] {
  return Object.entries(record).map(([k, v]) => ({
    id: nextEntryId++,
    key: k,
    value: v,
  }));
}

// ── Styles ──────────────────────────────────────────────────────────────

const sectionHeaderStyle: Record<string, string | number> = {
  display: "flex",
  alignItems: "center",
  gap: "8px",
  fontSize: "10px",
  fontWeight: 600,
  textTransform: "uppercase",
  letterSpacing: "0.08em",
  color: "var(--text-muted)",
  marginTop: "20px",
  marginBottom: "12px",
};

const btnPrimaryStyle: Record<string, string | number> = {
  padding: "8px 20px",
  fontSize: "13px",
  fontWeight: 500,
  borderRadius: "6px",
  border: "none",
  background: "var(--accent)",
  color: "var(--bg-base)",
  cursor: "pointer",
};

const btnSecondaryStyle: Record<string, string | number> = {
  padding: "8px 20px",
  fontSize: "13px",
  fontWeight: 500,
  borderRadius: "6px",
  border: "1px solid var(--border-primary)",
  background: "var(--bg-surface)",
  color: "var(--text-primary)",
  cursor: "pointer",
};

const kvContainerStyle: Record<string, string | number> = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
  marginBottom: "16px",
};

const kvRowStyle: Record<string, string | number> = {
  display: "flex",
  gap: "6px",
  alignItems: "flex-start",
};

const kvKeyStyle: Record<string, string | number> = {
  ...inputStyle,
  flex: 1,
};

const kvKeyErrorStyle: Record<string, string | number> = {
  ...inputStyle,
  flex: 1,
  borderColor: "var(--error)",
  boxShadow: "0 0 0 1px var(--error)",
};

const kvValueInputStyle: Record<string, string | number> = {
  ...inputStyle,
  flex: 1,
};

const kvValueTextareaStyle: Record<string, string | number> = {
  ...inputStyle,
  flex: 1,
  resize: "vertical",
  minHeight: "32px",
  lineHeight: "1.4",
};

const kvRemoveBtnStyle: Record<string, string | number> = {
  background: "none",
  border: "none",
  padding: "4px 8px",
  fontSize: "14px",
  color: "var(--error)",
  cursor: "pointer",
  lineHeight: 1,
  flexShrink: 0,
};

const kvAddBtnStyle: Record<string, string | number> = {
  background: "none",
  border: "1px dashed var(--border-primary)",
  borderRadius: "6px",
  padding: "6px 12px",
  fontSize: "12px",
  color: "var(--accent)",
  cursor: "pointer",
};

// ── Shared Key-Value Section ────────────────────────────────────────────

function KeyValueSection(
  { title, signal, addLabel, useTextarea }: {
    title: string;
    signal: Signal<KVEntry[]>;
    addLabel: string;
    useTextarea?: boolean;
  },
) {
  const entries = signal.value;
  const duplicateKeys = findDuplicateKeys(entries);

  return (
    <>
      <div style={sectionHeaderStyle}>
        <span>{title}</span>
        <div
          style={{
            flex: 1,
            height: "1px",
            background: "var(--border-subtle)",
          }}
        />
      </div>

      <div style={kvContainerStyle}>
        {entries.map((entry, idx) => {
          const isDuplicate = duplicateKeys.has(entry.key);
          return (
            <div key={entry.id} style={kvRowStyle}>
              <input
                type="text"
                placeholder="key"
                style={isDuplicate ? kvKeyErrorStyle : kvKeyStyle}
                value={entry.key}
                title={isDuplicate ? "Duplicate key" : undefined}
                onInput={(e) => {
                  const next = [...signal.value];
                  next[idx] = {
                    ...next[idx],
                    key: (e.target as HTMLInputElement).value,
                  };
                  signal.value = next;
                }}
              />
              {useTextarea
                ? (
                  <textarea
                    placeholder="value"
                    rows={1}
                    style={kvValueTextareaStyle}
                    value={entry.value}
                    onInput={(e) => {
                      const next = [...signal.value];
                      next[idx] = {
                        ...next[idx],
                        value: (e.target as HTMLTextAreaElement).value,
                      };
                      signal.value = next;
                    }}
                  />
                )
                : (
                  <input
                    type="text"
                    placeholder="value"
                    style={kvValueInputStyle}
                    value={entry.value}
                    onInput={(e) => {
                      const next = [...signal.value];
                      next[idx] = {
                        ...next[idx],
                        value: (e.target as HTMLInputElement).value,
                      };
                      signal.value = next;
                    }}
                  />
                )}
              <button
                type="button"
                onClick={() => {
                  signal.value = signal.value.filter((_, i) => i !== idx);
                }}
                style={kvRemoveBtnStyle}
                title="Remove"
              >
                &times;
              </button>
            </div>
          );
        })}
        <button
          type="button"
          onClick={() => {
            signal.value = [...signal.value, {
              id: nextEntryId++,
              key: "",
              value: "",
            }];
          }}
          style={kvAddBtnStyle}
        >
          {addLabel}
        </button>
      </div>
    </>
  );
}

// ── Component ───────────────────────────────────────────────────────────

export default function SchemaForm(
  { group, resource, namespace, name, mode }: SchemaFormProps,
) {
  // State
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const crd = useSignal<CRDGetResponse | null>(null);
  const specSchema = useSignal<SchemaProperty | null>(null);
  const storageVersion = useSignal("");
  const kind = useSignal("");
  const scope = useSignal<"Namespaced" | "Cluster">("Namespaced");
  const viewMode = useSignal<ViewMode>("form");
  const submitting = useSignal(false);
  const validating = useSignal(false);
  const validationResult = useSignal<{ ok: boolean; message: string } | null>(
    null,
  );

  // Form state
  const formName = useSignal(name ?? "");
  const formNamespace = useSignal(
    namespace ??
      (IS_BROWSER && selectedNamespace.value !== "all"
        ? selectedNamespace.value
        : "default"),
  );
  const formLabels = useSignal<KVEntry[]>([]);
  const formAnnotations = useSignal<KVEntry[]>([]);
  const formSpec = useSignal<Record<string, unknown>>({});
  const resourceVersion = useSignal<string>("");
  const namespaces = useSignal<string[]>(["default"]);

  // ── Fetch CRD schema ─────────────────────────────────────────────────

  useEffect(() => {
    if (!IS_BROWSER) return;

    const fetchData = async () => {
      loading.value = true;
      error.value = null;

      try {
        // Fetch CRD info + schema from backend
        const res = await apiGet<CRDGetResponse>(
          `/v1/extensions/crds/${group}/${resource}`,
        );
        const crdResp = res.data;
        if (!crdResp?.info) {
          error.value = "CRD not found";
          return;
        }

        crd.value = crdResp;
        kind.value = crdResp.info.kind;
        scope.value = crdResp.info.scope;
        storageVersion.value = crdResp.info.version;

        // Extract spec schema from the root schema
        const rootSchema = crdResp.schema as SchemaProperty | null;
        if (rootSchema?.properties?.spec) {
          specSchema.value = rootSchema.properties.spec;
        } else if (
          rootSchema?.["x-kubernetes-preserve-unknown-fields"]
        ) {
          specSchema.value = null;
        } else {
          specSchema.value = null;
        }

        // Fetch namespaces for namespace selector
        try {
          const nsRes = await apiGet<Array<{ metadata: { name: string } }>>(
            "/v1/resources/namespaces",
          );
          if (nsRes.data) {
            namespaces.value = nsRes.data.map((ns) => ns.metadata.name);
          }
        } catch {
          // keep default
        }

        // In edit mode, fetch the instance
        if (mode === "edit" && name) {
          const nsPath = namespace ? `/${namespace}` : "";
          const instanceRes = await apiGet<Record<string, unknown>>(
            `/v1/extensions/resources/${group}/${resource}${nsPath}/${name}`,
          );
          const instance = instanceRes.data;
          if (instance) {
            const meta = instance.metadata as
              | Record<string, unknown>
              | undefined;
            if (meta?.labels && typeof meta.labels === "object") {
              formLabels.value = entriesToKV(
                meta.labels as Record<string, string>,
              );
            }
            if (meta?.annotations && typeof meta.annotations === "object") {
              formAnnotations.value = entriesToKV(
                meta.annotations as Record<string, string>,
              );
            }
            if (meta?.resourceVersion) {
              resourceVersion.value = meta.resourceVersion as string;
            }
            if (instance.spec && typeof instance.spec === "object") {
              formSpec.value = instance.spec as Record<string, unknown>;
            }
          }
        }
      } catch (err) {
        error.value = err instanceof ApiError
          ? err.detail ?? err.message
          : "Failed to load CRD schema";
      } finally {
        loading.value = false;
      }
    };

    fetchData();
  }, [group, resource, mode, name, namespace]);

  // ── Form state change handler ────────────────────────────────────────

  const handleSpecChange = useCallback((path: string, value: unknown) => {
    // path is like "spec.issuerRef.name" — strip the "spec." prefix
    const relPath = path.startsWith("spec.") ? path.slice(5) : path;
    formSpec.value = safeDeepSet(formSpec.value, relPath, value);
  }, []);

  // ── Build YAML preview (only when YAML tab is active) ────────────────

  const yamlPreview = useComputed(() => {
    if (viewMode.value !== "yaml") return "";
    const labels = toRecord(formLabels.value);
    const annotations = toRecord(formAnnotations.value);
    const apiVersion = `${group}/${storageVersion.value}`;
    return formStateToYaml(
      apiVersion,
      kind.value,
      {
        name: formName.value,
        namespace: scope.value === "Namespaced"
          ? formNamespace.value
          : undefined,
        labels: Object.keys(labels).length > 0 ? labels : undefined,
        annotations: Object.keys(annotations).length > 0
          ? annotations
          : undefined,
      },
      formSpec.value,
    );
  });

  // ── Build the JSON body for API calls ─────────────────────────────────
  // Signal refs are stable (never change identity), so deps are effectively [].

  const buildBody = useCallback((): Record<string, unknown> => {
    const labels = toRecord(formLabels.value);
    const annotations = toRecord(formAnnotations.value);
    const body: Record<string, unknown> = {
      apiVersion: `${group}/${storageVersion.value}`,
      kind: kind.value,
      metadata: {
        name: formName.value,
        ...(scope.value === "Namespaced"
          ? { namespace: formNamespace.value }
          : {}),
        ...(Object.keys(labels).length > 0 ? { labels } : {}),
        ...(Object.keys(annotations).length > 0 ? { annotations } : {}),
        ...(mode === "edit" && resourceVersion.value
          ? { resourceVersion: resourceVersion.value }
          : {}),
      },
    };
    if (Object.keys(formSpec.value).length > 0) {
      body.spec = formSpec.value;
    }
    return body;
  }, []);

  // ── Actions ──────────────────────────────────────────────────────────

  const handleSubmit = useCallback(async () => {
    if (!formName.value.trim()) {
      showToast("Name is required", "error");
      return;
    }

    submitting.value = true;
    try {
      const body = buildBody();
      const ns = scope.value === "Namespaced" ? formNamespace.value : "_";

      if (mode === "create") {
        await apiPost(
          `/v1/extensions/resources/${group}/${resource}/${ns}`,
          body,
        );
        showToast(`Created ${formName.value}`, "success");
        globalThis.location.href = `/extensions/${group}/${resource}`;
      } else {
        const res = await apiPut<Record<string, unknown>>(
          `/v1/extensions/resources/${group}/${resource}/${ns}/${name}`,
          body,
        );
        // Update resourceVersion so subsequent saves don't 409
        const resMeta = res.data?.metadata as
          | Record<string, unknown>
          | undefined;
        if (resMeta?.resourceVersion) {
          resourceVersion.value = resMeta.resourceVersion as string;
        }
        showToast(`Updated ${formName.value}`, "success");
      }
    } catch (err) {
      const msg = err instanceof ApiError
        ? err.detail ?? err.message
        : "Operation failed";
      showToast(msg, "error");
    } finally {
      submitting.value = false;
    }
  }, []);

  const handleValidate = useCallback(async () => {
    validating.value = true;
    validationResult.value = null;
    try {
      const body = buildBody();
      await apiPost(
        `/v1/extensions/resources/${group}/${resource}/-/validate`,
        body,
      );
      validationResult.value = { ok: true, message: "Validation passed" };
    } catch (err) {
      const msg = err instanceof ApiError
        ? err.detail ?? err.message
        : "Validation failed";
      validationResult.value = { ok: false, message: msg };
    } finally {
      validating.value = false;
    }
  }, []);

  // ── SSR placeholder ──────────────────────────────────────────────────

  if (!IS_BROWSER) {
    return <div style={{ minHeight: "400px" }} />;
  }

  // ── Loading state ────────────────────────────────────────────────────

  if (loading.value) {
    return (
      <div>
        <Skeleton class="h-5 w-48 mb-2" />
        <Skeleton class="h-4 w-72 mb-6" />
        <Skeleton class="h-10 w-full mb-4" />
        <Skeleton class="h-64 w-full rounded-lg" />
      </div>
    );
  }

  // ── Error state ──────────────────────────────────────────────────────

  if (error.value) {
    return (
      <div
        style={{
          padding: "32px",
          textAlign: "center",
          color: "var(--text-muted)",
        }}
      >
        <p
          style={{
            fontSize: "14px",
            color: "var(--error)",
            marginBottom: "12px",
          }}
        >
          {error.value}
        </p>
        <button
          type="button"
          onClick={() => globalThis.location.reload()}
          style={btnSecondaryStyle}
        >
          Retry
        </button>
      </div>
    );
  }

  // ── Determine schema tier ────────────────────────────────────────────

  const hasSpecSchema = specSchema.value !== null &&
    specSchema.value.properties != null;
  const specProperties = hasSpecSchema ? specSchema.value!.properties! : {};
  const specRequired = hasSpecSchema ? (specSchema.value!.required ?? []) : [];

  // ── Render ───────────────────────────────────────────────────────────

  return (
    <div style={{ maxWidth: "800px" }}>
      {/* Breadcrumbs */}
      <nav
        style={{
          display: "flex",
          alignItems: "center",
          gap: "6px",
          fontSize: "12px",
          color: "var(--text-muted)",
          marginBottom: "12px",
        }}
      >
        <a
          href="/extensions"
          style={{ color: "var(--accent)", textDecoration: "none" }}
        >
          Extensions
        </a>
        <span>/</span>
        <a
          href="/extensions"
          style={{ color: "var(--accent)", textDecoration: "none" }}
        >
          {group}
        </a>
        <span>/</span>
        <a
          href={`/extensions/${group}/${resource}`}
          style={{ color: "var(--accent)", textDecoration: "none" }}
        >
          {kind.value}
        </a>
        <span>/</span>
        <span style={{ color: "var(--text-primary)" }}>
          {mode === "create" ? "New" : name}
        </span>
      </nav>

      {/* Header */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          marginBottom: "20px",
        }}
      >
        <div>
          <h1
            style={{
              fontSize: "18px",
              fontWeight: 600,
              color: "var(--text-primary)",
              margin: 0,
              letterSpacing: "-0.02em",
            }}
          >
            {mode === "create" ? `New ${kind.value}` : `Edit ${name}`}
          </h1>
          <p
            style={{
              fontSize: "12px",
              color: "var(--text-muted)",
              margin: "2px 0 0",
            }}
          >
            {resource}.{group}/{storageVersion.value}
          </p>
        </div>

        {/* Form/YAML toggle */}
        <div
          style={{
            display: "flex",
            gap: "2px",
            background: "var(--bg-surface)",
            borderRadius: "6px",
            border: "1px solid var(--border-primary)",
            padding: "2px",
          }}
        >
          {(["form", "yaml"] as const).map((m) => (
            <button
              type="button"
              key={m}
              onClick={() => (viewMode.value = m)}
              style={{
                padding: "4px 12px",
                fontSize: "11px",
                fontWeight: 500,
                borderRadius: "4px",
                border: "none",
                cursor: "pointer",
                background: viewMode.value === m
                  ? "var(--accent)"
                  : "transparent",
                color: viewMode.value === m
                  ? "var(--bg-base)"
                  : "var(--text-muted)",
              }}
            >
              {m === "form" ? "Form" : "YAML"}
            </button>
          ))}
        </div>
      </div>

      {/* YAML preview mode */}
      {viewMode.value === "yaml" && <YamlPreview yaml={yamlPreview.value} />}

      {/* Form mode */}
      {viewMode.value === "form" && (
        <div>
          {/* ── Metadata Section ─────────────────────────────────────── */}
          <div style={sectionHeaderStyle}>
            <span>Metadata</span>
            <div
              style={{
                flex: 1,
                height: "1px",
                background: "var(--border-subtle)",
              }}
            />
          </div>

          <div style={{ marginBottom: "12px" }}>
            <label
              htmlFor="field-metadata-name"
              style={{
                display: "block",
                fontSize: "12px",
                fontWeight: 500,
                color: "var(--text-secondary)",
                marginBottom: "4px",
              }}
            >
              Name <span style={{ color: "var(--error)" }}>*</span>
            </label>
            <input
              id="field-metadata-name"
              type="text"
              style={inputStyle}
              value={formName.value}
              disabled={mode === "edit"}
              onInput={(
                e,
              ) => (formName.value = (e.target as HTMLInputElement).value)}
              placeholder="my-resource"
            />
          </div>

          {scope.value === "Namespaced" && (
            <div style={{ marginBottom: "12px" }}>
              <label
                htmlFor="field-metadata-namespace"
                style={{
                  display: "block",
                  fontSize: "12px",
                  fontWeight: 500,
                  color: "var(--text-secondary)",
                  marginBottom: "4px",
                }}
              >
                Namespace <span style={{ color: "var(--error)" }}>*</span>
              </label>
              <select
                id="field-metadata-namespace"
                style={selectStyle}
                value={formNamespace.value}
                disabled={mode === "edit"}
                onChange={(
                  e,
                ) => (formNamespace.value =
                  (e.target as HTMLSelectElement).value)}
              >
                {namespaces.value.map((ns) => (
                  <option key={ns} value={ns}>
                    {ns}
                  </option>
                ))}
              </select>
            </div>
          )}

          {/* ── Labels Section ───────────────────────────────────────── */}
          <KeyValueSection
            title="Labels"
            signal={formLabels}
            addLabel="+ Add Label"
          />

          {/* ── Annotations Section ─────────────────────────────────── */}
          <KeyValueSection
            title="Annotations"
            signal={formAnnotations}
            addLabel="+ Add Annotation"
            useTextarea
          />

          {/* ── Spec Section ─────────────────────────────────────────── */}
          <div style={sectionHeaderStyle}>
            <span>Spec</span>
            <div
              style={{
                flex: 1,
                height: "1px",
                background: "var(--border-subtle)",
              }}
            />
          </div>

          {hasSpecSchema
            ? (
              <div>
                {Object.entries(specProperties).map(([key, propSchema]) => (
                  <SchemaFormField
                    key={key}
                    name={key}
                    path={`spec.${key}`}
                    schema={propSchema}
                    value={formSpec.value[key]}
                    onChange={handleSpecChange}
                    required={specRequired.includes(key)}
                    depth={0}
                  />
                ))}
              </div>
            )
            : (
              // Graceful degradation: YAML textarea
              <div style={{ marginBottom: "16px" }}>
                <p
                  style={{
                    fontSize: "12px",
                    color: "var(--text-muted)",
                    marginBottom: "8px",
                  }}
                >
                  {specSchema.value === null && !error.value
                    ? "This CRD does not define a structured schema. Enter the spec as YAML."
                    : "Enter the spec as YAML."}
                </p>
                <textarea
                  style={{
                    ...inputStyle,
                    minHeight: "200px",
                    resize: "vertical",
                    lineHeight: "1.5",
                  }}
                  value={Object.keys(formSpec.value).length > 0
                    ? (() => {
                      try {
                        return yamlStringify(formSpec.value);
                      } catch {
                        return "";
                      }
                    })()
                    : ""}
                  onInput={(e) => {
                    try {
                      const parsed = yamlParse(
                        (e.target as HTMLTextAreaElement).value,
                      );
                      if (parsed && typeof parsed === "object") {
                        formSpec.value = parsed as Record<string, unknown>;
                      }
                    } catch {
                      // invalid YAML
                    }
                  }}
                  placeholder="# Enter spec YAML..."
                  spellcheck={false}
                />
              </div>
            )}
        </div>
      )}

      {/* ── Validation Result ──────────────────────────────────────── */}
      {validationResult.value && (
        <div
          style={{
            padding: "10px 14px",
            marginBottom: "16px",
            borderRadius: "6px",
            fontSize: "12px",
            background: validationResult.value.ok
              ? "var(--success-dim)"
              : "var(--error-dim)",
            color: validationResult.value.ok
              ? "var(--success)"
              : "var(--error)",
            border: `1px solid ${
              validationResult.value.ok ? "var(--success)" : "var(--error)"
            }`,
          }}
        >
          {validationResult.value.message}
        </div>
      )}

      {/* ── Action Bar ─────────────────────────────────────────────── */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "8px",
          borderTop: "1px solid var(--border-primary)",
          paddingTop: "16px",
          marginTop: "8px",
        }}
      >
        <button
          type="button"
          onClick={handleSubmit}
          disabled={submitting.value}
          style={{
            ...btnPrimaryStyle,
            opacity: submitting.value ? 0.6 : 1,
            cursor: submitting.value ? "default" : "pointer",
          }}
        >
          {submitting.value
            ? (mode === "create" ? "Creating..." : "Updating...")
            : (mode === "create" ? "Create" : "Update")}
        </button>

        <button
          type="button"
          onClick={handleValidate}
          disabled={validating.value}
          style={{
            ...btnSecondaryStyle,
            opacity: validating.value ? 0.6 : 1,
            cursor: validating.value ? "default" : "pointer",
          }}
        >
          {validating.value ? "Validating..." : "Validate"}
        </button>

        {viewMode.value === "form" && (
          <button
            type="button"
            onClick={() => (viewMode.value = "yaml")}
            style={btnSecondaryStyle}
          >
            View YAML
          </button>
        )}

        <a
          href={`/extensions/${group}/${resource}`}
          style={{
            ...btnSecondaryStyle,
            textDecoration: "none",
            display: "inline-flex",
            alignItems: "center",
          }}
        >
          Cancel
        </a>
      </div>
    </div>
  );
}
