import {
  type Dispatch,
  type SetStateAction,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import { parse as yamlParse, stringify as yamlStringify } from "yaml";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import { ApiError, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import type { CRDInfo, SchemaProperty } from "@/lib/crd-types.ts";
import { inputStyle, selectStyle } from "@/lib/form-styles.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { formStateToYaml, safeDeepSet } from "@/lib/schema-to-yaml.ts";
import SchemaFormField from "@/src/islands/SchemaFormField.tsx";
import YamlPreview from "@/src/islands/YamlPreview.tsx";
import { selectedNamespaceOrDefault } from "@/src/lib/namespace.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

/** The root every return shares. Hydration keeps the server-rendered root's
 * own props, so each branch styles an inner box instead. */
const FORM_ROOT_STYLE = { maxWidth: "800px" };

/** A field that identifies an existing object: shown, but visibly not
 * editable. Changing name or namespace on apply would create a copy, not a
 * rename. */
function lockedStyle(base: Record<string, string>): Record<string, string> {
  return { ...base, opacity: "0.6", cursor: "not-allowed" };
}

// ── Types ───────────────────────────────────────────────────────────────

interface SchemaFormProps {
  group: string;
  resource: string;
  namespace?: string;
  name?: string;
  mode: "create" | "edit";
  onClose?: () => void;
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
function findDuplicateKeys(entries: Array<{ key: string }>): Set<string> {
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

function entriesToKV(record: Record<string, string>): KVEntry[] {
  return Object.entries(record).map(([k, v]) => ({
    id: nextEntryId++,
    key: k,
    value: v,
  }));
}

/** The spec as YAML for the fallback textarea; empty when there is none. */
function stringifySpec(spec: Record<string, unknown>): string {
  if (Object.keys(spec).length === 0) return "";
  try {
    return yamlStringify(spec);
  } catch {
    return "";
  }
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

function KeyValueSection({
  title,
  entries,
  setEntries,
  addLabel,
  useTextarea,
}: {
  title: string;
  entries: KVEntry[];
  setEntries: Dispatch<SetStateAction<KVEntry[]>>;
  addLabel: string;
  useTextarea?: boolean;
}) {
  const patchEntry = (idx: number, patch: Partial<KVEntry>) => {
    setEntries((prev) =>
      prev.map((entry, i) => (i === idx ? { ...entry, ...patch } : entry)),
    );
  };
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
                onChange={(e) =>
                  patchEntry(idx, { key: e.currentTarget.value })
                }
              />
              {useTextarea ? (
                <textarea
                  placeholder="value"
                  rows={1}
                  style={kvValueTextareaStyle}
                  value={entry.value}
                  onChange={(e) =>
                    patchEntry(idx, { value: e.currentTarget.value })
                  }
                />
              ) : (
                <input
                  type="text"
                  placeholder="value"
                  style={kvValueInputStyle}
                  value={entry.value}
                  onChange={(e) =>
                    patchEntry(idx, { value: e.currentTarget.value })
                  }
                />
              )}
              <button
                type="button"
                onClick={() => {
                  setEntries((prev) => prev.filter((_, i) => i !== idx));
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
            setEntries((prev) => [
              ...prev,
              {
                id: nextEntryId++,
                key: "",
                value: "",
              },
            ]);
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

export default function SchemaForm({
  group,
  resource,
  namespace,
  name,
  mode,
  onClose,
}: SchemaFormProps) {
  const close = onClose ?? (() => globalThis.history.back());
  const isClient = useIsClient();
  // State
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [specSchema, setSpecSchema] = useState<SchemaProperty | null>(null);
  const [storageVersion, setStorageVersion] = useState("");
  const [kind, setKind] = useState("");
  const [scope, setScope] = useState<"Namespaced" | "Cluster">("Namespaced");
  const [viewMode, setViewMode] = useState<ViewMode>("form");
  const [submitting, setSubmitting] = useState(false);
  const [validating, setValidating] = useState(false);
  const [validationResult, setValidationResult] = useState<{
    ok: boolean;
    message: string;
  } | null>(null);

  // Form state
  const [formName, setFormName] = useState(name ?? "");
  const [formNamespace, setFormNamespace] = useState(
    namespace ?? selectedNamespaceOrDefault(),
  );
  const [formLabels, setFormLabels] = useState<KVEntry[]>([]);
  const [formAnnotations, setFormAnnotations] = useState<KVEntry[]>([]);
  const [formSpec, setFormSpec] = useState<Record<string, unknown>>({});
  // The spec textarea shown when the CRD has no structured schema keeps what
  // was typed, valid YAML or not; it shows the parsed spec until typing starts.
  const [specText, setSpecText] = useState<string | null>(null);
  const [resourceVersion, setResourceVersion] = useState("");
  const [namespaces] = useNamespaces();

  // ── Fetch CRD schema ─────────────────────────────────────────────────

  useEffect(() => {
    const fetchData = async () => {
      setLoading(true);
      setError(null);

      try {
        // Fetch CRD info + schema from backend
        const res = await apiGet<CRDGetResponse>(
          `/v1/extensions/crds/${group}/${resource}`,
        );
        const crdResp = res.data;
        if (!crdResp?.info) {
          setError("CRD not found");
          return;
        }

        setKind(crdResp.info.kind);
        setScope(crdResp.info.scope);
        setStorageVersion(crdResp.info.version);

        // Extract spec schema from the root schema
        const rootSchema = crdResp.schema as SchemaProperty | null;
        setSpecSchema(rootSchema?.properties?.spec ?? null);

        // In edit mode, fetch the instance
        if (mode === "edit" && name) {
          const ns = namespace || "_";
          const instanceRes = await apiGet<Record<string, unknown>>(
            `/v1/extensions/resources/${group}/${resource}/${ns}/${name}`,
          );
          const instance = instanceRes.data;
          if (instance) {
            const meta = instance.metadata as
              | Record<string, unknown>
              | undefined;
            if (meta?.labels && typeof meta.labels === "object") {
              setFormLabels(entriesToKV(meta.labels as Record<string, string>));
            }
            if (meta?.annotations && typeof meta.annotations === "object") {
              setFormAnnotations(
                entriesToKV(meta.annotations as Record<string, string>),
              );
            }
            if (meta?.resourceVersion) {
              setResourceVersion(meta.resourceVersion as string);
            }
            if (instance.spec && typeof instance.spec === "object") {
              setFormSpec(instance.spec as Record<string, unknown>);
            }
          }
        }
      } catch (err) {
        setError(
          err instanceof ApiError
            ? (err.detail ?? err.message)
            : "Failed to load CRD schema",
        );
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, [group, resource, mode, name, namespace]);

  // ── Form state change handler ────────────────────────────────────────

  const handleSpecChange = useCallback((path: string, value: unknown) => {
    // path is like "spec.issuerRef.name" — strip the "spec." prefix
    const relPath = path.startsWith("spec.") ? path.slice(5) : path;
    setFormSpec((prev) => safeDeepSet(prev, relPath, value));
  }, []);

  // ── Build YAML preview (only when YAML tab is active) ────────────────

  const yamlPreview = useMemo(() => {
    if (viewMode !== "yaml") return "";
    const labels = toRecord(formLabels);
    const annotations = toRecord(formAnnotations);
    const apiVersion = `${group}/${storageVersion}`;
    return formStateToYaml(
      apiVersion,
      kind,
      {
        name: formName,
        namespace: scope === "Namespaced" ? formNamespace : undefined,
        labels: Object.keys(labels).length > 0 ? labels : undefined,
        annotations:
          Object.keys(annotations).length > 0 ? annotations : undefined,
      },
      formSpec,
    );
  }, [
    viewMode,
    group,
    storageVersion,
    kind,
    scope,
    formName,
    formNamespace,
    formLabels,
    formAnnotations,
    formSpec,
  ]);

  // ── Build the JSON body for API calls ─────────────────────────────────

  const buildBody = (): Record<string, unknown> => {
    const labels = toRecord(formLabels);
    const annotations = toRecord(formAnnotations);
    const body: Record<string, unknown> = {
      apiVersion: `${group}/${storageVersion}`,
      kind,
      metadata: {
        name: formName,
        ...(scope === "Namespaced" ? { namespace: formNamespace } : {}),
        ...(Object.keys(labels).length > 0 ? { labels } : {}),
        ...(Object.keys(annotations).length > 0 ? { annotations } : {}),
        ...(mode === "edit" && resourceVersion ? { resourceVersion } : {}),
      },
    };
    if (Object.keys(formSpec).length > 0) {
      body.spec = formSpec;
    }
    return body;
  };

  // ── Actions ──────────────────────────────────────────────────────────

  const handleSubmit = async () => {
    if (!formName.trim()) {
      showToast("Name is required", "error");
      return;
    }

    setSubmitting(true);
    try {
      const body = buildBody();
      const ns = scope === "Namespaced" ? formNamespace : "_";

      if (mode === "create") {
        await apiPost(
          `/v1/extensions/resources/${group}/${resource}/${ns}`,
          body,
        );
        showToast(`Created ${formName}`, "success");
        close();
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
          setResourceVersion(resMeta.resourceVersion as string);
        }
        showToast(`Updated ${formName}`, "success");
      }
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? (err.detail ?? err.message)
          : "Operation failed";
      showToast(msg, "error");
    } finally {
      setSubmitting(false);
    }
  };

  const handleValidate = async () => {
    setValidating(true);
    setValidationResult(null);
    try {
      const body = buildBody();
      await apiPost(
        `/v1/extensions/resources/${group}/${resource}/-/validate`,
        body,
      );
      setValidationResult({ ok: true, message: "Validation passed" });
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? (err.detail ?? err.message)
          : "Validation failed";
      setValidationResult({ ok: false, message: msg });
    } finally {
      setValidating(false);
    }
  };

  // ── SSR placeholder ──────────────────────────────────────────────────

  if (!isClient) {
    return (
      <div style={FORM_ROOT_STYLE}>
        <div style={{ minHeight: "400px" }} />
      </div>
    );
  }

  // ── Loading state ────────────────────────────────────────────────────

  if (loading) {
    return (
      <div style={FORM_ROOT_STYLE}>
        <div>
          <Skeleton className="h-5 w-48 mb-2" />
          <Skeleton className="h-4 w-72 mb-6" />
          <Skeleton className="h-10 w-full mb-4" />
          <Skeleton className="h-64 w-full rounded-lg" />
        </div>
      </div>
    );
  }

  // ── Error state ──────────────────────────────────────────────────────

  if (error) {
    return (
      <div style={FORM_ROOT_STYLE}>
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
            {error}
          </p>
          <button
            type="button"
            onClick={() => globalThis.location.reload()}
            style={btnSecondaryStyle}
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  // ── Determine schema tier ────────────────────────────────────────────

  const specProperties = specSchema?.properties ?? null;
  const specRequired = specSchema?.required ?? [];

  // ── Render ───────────────────────────────────────────────────────────

  return (
    <div style={FORM_ROOT_STYLE}>
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
          {kind}
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
            {mode === "create" ? `New ${kind}` : `Edit ${name}`}
          </h1>
          <p
            style={{
              fontSize: "12px",
              color: "var(--text-muted)",
              margin: "2px 0 0",
            }}
          >
            {resource}.{group}/{storageVersion}
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
              onClick={() => setViewMode(m)}
              style={{
                padding: "4px 12px",
                fontSize: "11px",
                fontWeight: 500,
                borderRadius: "4px",
                border: "none",
                cursor: "pointer",
                background: viewMode === m ? "var(--accent)" : "transparent",
                color: viewMode === m ? "var(--bg-base)" : "var(--text-muted)",
              }}
            >
              {m === "form" ? "Form" : "YAML"}
            </button>
          ))}
        </div>
      </div>

      {/* YAML preview mode */}
      {viewMode === "yaml" && <YamlPreview yaml={yamlPreview} />}

      {/* Form mode */}
      {viewMode === "form" && (
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
              style={mode === "edit" ? lockedStyle(inputStyle) : inputStyle}
              value={formName}
              disabled={mode === "edit"}
              onChange={(e) => setFormName(e.currentTarget.value)}
              placeholder="my-resource"
            />
          </div>

          {scope === "Namespaced" && (
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
                style={mode === "edit" ? lockedStyle(selectStyle) : selectStyle}
                value={formNamespace}
                disabled={mode === "edit"}
                onChange={(e) => setFormNamespace(e.currentTarget.value)}
              >
                {namespaces.map((ns) => (
                  <option key={ns} value={ns}>
                    {ns}
                  </option>
                ))}
              </select>
            </div>
          )}

          {mode === "edit" && (
            <p
              style={{
                fontSize: "12px",
                color: "var(--text-muted)",
                margin: "0 0 12px",
              }}
            >
              Name and namespace identify this object and can't be changed. To
              move or rename it, create a new one.
            </p>
          )}

          {/* ── Labels Section ───────────────────────────────────────── */}
          <KeyValueSection
            title="Labels"
            entries={formLabels}
            setEntries={setFormLabels}
            addLabel="+ Add Label"
          />

          {/* ── Annotations Section ─────────────────────────────────── */}
          <KeyValueSection
            title="Annotations"
            entries={formAnnotations}
            setEntries={setFormAnnotations}
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

          {specProperties ? (
            <div>
              {Object.entries(specProperties).map(([key, propSchema]) => (
                <SchemaFormField
                  key={key}
                  name={key}
                  path={`spec.${key}`}
                  schema={propSchema}
                  value={formSpec[key]}
                  onChange={handleSpecChange}
                  required={specRequired.includes(key)}
                  depth={0}
                />
              ))}
            </div>
          ) : (
            // Graceful degradation: YAML textarea
            <div style={{ marginBottom: "16px" }}>
              <p
                style={{
                  fontSize: "12px",
                  color: "var(--text-muted)",
                  marginBottom: "8px",
                }}
              >
                {specSchema === null && !error
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
                value={specText ?? stringifySpec(formSpec)}
                onChange={(e) => {
                  const text = e.currentTarget.value;
                  setSpecText(text);
                  try {
                    const parsed = yamlParse(text);
                    if (parsed && typeof parsed === "object") {
                      setFormSpec(parsed as Record<string, unknown>);
                    }
                  } catch {
                    // invalid YAML
                  }
                }}
                placeholder="# Enter spec YAML..."
                spellCheck={false}
              />
            </div>
          )}
        </div>
      )}

      {/* ── Validation Result ──────────────────────────────────────── */}
      {validationResult && (
        <div
          style={{
            padding: "10px 14px",
            marginBottom: "16px",
            borderRadius: "6px",
            fontSize: "12px",
            background: validationResult.ok
              ? "var(--success-dim)"
              : "var(--error-dim)",
            color: validationResult.ok ? "var(--success)" : "var(--error)",
            border: `1px solid ${
              validationResult.ok ? "var(--success)" : "var(--error)"
            }`,
          }}
        >
          {validationResult.message}
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
          disabled={submitting}
          style={{
            ...btnPrimaryStyle,
            opacity: submitting ? 0.6 : 1,
            cursor: submitting ? "default" : "pointer",
          }}
        >
          {submitting
            ? mode === "create"
              ? "Creating..."
              : "Updating..."
            : mode === "create"
              ? "Create"
              : "Update"}
        </button>

        <button
          type="button"
          onClick={handleValidate}
          disabled={validating}
          style={{
            ...btnSecondaryStyle,
            opacity: validating ? 0.6 : 1,
            cursor: validating ? "default" : "pointer",
          }}
        >
          {validating ? "Validating..." : "Validate"}
        </button>

        {viewMode === "form" && (
          <button
            type="button"
            onClick={() => setViewMode("yaml")}
            style={btnSecondaryStyle}
          >
            View YAML
          </button>
        )}

        <button type="button" onClick={close} style={btnSecondaryStyle}>
          Cancel
        </button>
      </div>
    </div>
  );
}
