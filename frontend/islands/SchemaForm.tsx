import { useSignal, useComputed } from "@preact/signals";
import { useEffect, useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost, apiPut, ApiError } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import type { SchemaProperty } from "@/lib/crd-types.ts";
import { formStateToYaml } from "@/lib/schema-to-yaml.ts";
import { stringify as yamlStringify, parse as yamlParse } from "yaml";
import { showToast } from "@/islands/ToastProvider.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import SchemaFormField from "@/islands/SchemaFormField.tsx";
import YamlPreview from "@/islands/YamlPreview.tsx";
import { inputStyle, selectStyle } from "@/lib/form-styles.ts";

// ── Types ───────────────────────────────────────────────────────────────

interface SchemaFormProps {
  group: string;
  resource: string;
  namespace?: string;
  name?: string;
  mode: "create" | "edit";
}

interface CRDFullObject {
  metadata: { name: string; resourceVersion: string };
  spec: {
    group: string;
    names: { kind: string; singular: string; plural: string };
    scope: "Namespaced" | "Cluster";
    versions: Array<{
      name: string;
      served: boolean;
      storage: boolean;
      schema?: {
        openAPIV3Schema?: SchemaProperty;
      };
    }>;
  };
}

type ViewMode = "form" | "yaml";

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

// ── Component ───────────────────────────────────────────────────────────

export default function SchemaForm({ group, resource, namespace, name, mode }: SchemaFormProps) {
  // State
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const crd = useSignal<CRDFullObject | null>(null);
  const specSchema = useSignal<SchemaProperty | null>(null);
  const storageVersion = useSignal("");
  const kind = useSignal("");
  const scope = useSignal<"Namespaced" | "Cluster">("Namespaced");
  const viewMode = useSignal<ViewMode>("form");
  const submitting = useSignal(false);
  const validating = useSignal(false);
  const validationResult = useSignal<{ ok: boolean; message: string } | null>(null);

  // Form state
  const formName = useSignal(name ?? "");
  const formNamespace = useSignal(namespace ?? (IS_BROWSER && selectedNamespace.value !== "all" ? selectedNamespace.value : "default"));
  const formLabels = useSignal<Array<{ key: string; value: string }>>([]);
  const formSpec = useSignal<Record<string, unknown>>({});
  const namespaces = useSignal<string[]>(["default"]);

  // ── Fetch CRD schema ─────────────────────────────────────────────────

  useEffect(() => {
    if (!IS_BROWSER) return;

    const fetchData = async () => {
      loading.value = true;
      error.value = null;

      try {
        // Check sessionStorage cache
        const cachePrefix = `${group}/${resource}/`;
        let crdData: CRDFullObject | null = null;

        try {
          for (let i = 0; i < sessionStorage.length; i++) {
            const key = sessionStorage.key(i);
            if (key?.startsWith(`crd-schema:${cachePrefix}`)) {
              crdData = JSON.parse(sessionStorage.getItem(key)!) as CRDFullObject;
              break;
            }
          }
        } catch {
          // sessionStorage unavailable
        }

        if (!crdData) {
          const res = await apiGet<CRDFullObject>(`/v1/extensions/crds/${group}/${resource}`);
          crdData = res.data;

          // Cache it
          try {
            const cacheKey = `crd-schema:${cachePrefix}${crdData.metadata.resourceVersion}`;
            sessionStorage.setItem(cacheKey, JSON.stringify(crdData));
          } catch {
            // quota exceeded or unavailable
          }
        }

        crd.value = crdData;
        kind.value = crdData.spec.names.kind;
        scope.value = crdData.spec.scope;

        // Find storage version
        const sv = crdData.spec.versions.find((v) => v.storage);
        if (!sv) {
          error.value = "No storage version found in CRD";
          return;
        }
        storageVersion.value = sv.name;

        // Extract spec schema
        const rootSchema = sv.schema?.openAPIV3Schema;
        if (rootSchema?.properties?.spec) {
          specSchema.value = rootSchema.properties.spec;
        } else if (rootSchema?.["x-kubernetes-preserve-unknown-fields"]) {
          // Graceful degradation tier 2: entire schema is preserve-unknown
          specSchema.value = null;
        } else {
          // Graceful degradation tier 3: no schema
          specSchema.value = null;
        }

        // Fetch namespaces for namespace selector
        try {
          const nsRes = await apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces");
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
            const meta = instance.metadata as Record<string, unknown> | undefined;
            if (meta?.labels && typeof meta.labels === "object") {
              formLabels.value = Object.entries(meta.labels as Record<string, string>).map(([k, v]) => ({ key: k, value: v }));
            }
            if (instance.spec && typeof instance.spec === "object") {
              formSpec.value = instance.spec as Record<string, unknown>;
            }
          }
        }
      } catch (err) {
        error.value = err instanceof ApiError ? err.detail ?? err.message : "Failed to load CRD schema";
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
    const parts = relPath.split(".");

    const next = { ...formSpec.value };
    if (parts.length === 1) {
      if (value === undefined) {
        delete next[parts[0]];
      } else {
        next[parts[0]] = value;
      }
    } else {
      // Deep set
      let current: Record<string, unknown> = next;
      for (let i = 0; i < parts.length - 1; i++) {
        const part = parts[i];
        if (!(part in current) || typeof current[part] !== "object" || current[part] === null || Array.isArray(current[part])) {
          // If the next part is a number, this should be an array
          const nextPart = parts[i + 1];
          if (/^\d+$/.test(nextPart)) {
            if (!Array.isArray(current[part])) {
              current[part] = [];
            }
            current = current[part] as unknown as Record<string, unknown>;
            continue;
          }
          current[part] = {};
        }
        current = current[part] as Record<string, unknown>;
      }
      const lastKey = parts[parts.length - 1];
      if (value === undefined) {
        delete current[lastKey];
      } else {
        current[lastKey] = value;
      }
    }

    formSpec.value = next;
  }, []);

  // ── Build YAML preview ───────────────────────────────────────────────

  const yamlPreview = useComputed(() => {
    const labels: Record<string, string> = {};
    for (const { key, value } of formLabels.value) {
      if (key) labels[key] = value;
    }
    const apiVersion = `${group}/${storageVersion.value}`;
    return formStateToYaml(
      apiVersion,
      kind.value,
      {
        name: formName.value,
        namespace: scope.value === "Namespaced" ? formNamespace.value : undefined,
        labels: Object.keys(labels).length > 0 ? labels : undefined,
      },
      formSpec.value,
    );
  });

  // ── Build the JSON body for API calls ─────────────────────────────────

  const buildBody = useCallback((): Record<string, unknown> => {
    const labels: Record<string, string> = {};
    for (const { key, value } of formLabels.value) {
      if (key) labels[key] = value;
    }
    const body: Record<string, unknown> = {
      apiVersion: `${group}/${storageVersion.value}`,
      kind: kind.value,
      metadata: {
        name: formName.value,
        ...(scope.value === "Namespaced" ? { namespace: formNamespace.value } : {}),
        ...(Object.keys(labels).length > 0 ? { labels } : {}),
      },
    };
    if (Object.keys(formSpec.value).length > 0) {
      body.spec = formSpec.value;
    }
    return body;
  }, [group, kind, storageVersion, formName, formNamespace, scope, formLabels, formSpec]);

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
        await apiPost(`/v1/extensions/resources/${group}/${resource}/${ns}`, body);
        showToast(`Created ${formName.value}`, "success");
        globalThis.location.href = `/extensions/${group}/${resource}`;
      } else {
        await apiPut(`/v1/extensions/resources/${group}/${resource}/${ns}/${name}`, body);
        showToast(`Updated ${formName.value}`, "success");
      }
    } catch (err) {
      const msg = err instanceof ApiError ? err.detail ?? err.message : "Operation failed";
      showToast(msg, "error");
    } finally {
      submitting.value = false;
    }
  }, [mode, group, resource, name, formName, formNamespace, scope, buildBody]);

  const handleValidate = useCallback(async () => {
    validating.value = true;
    validationResult.value = null;
    try {
      const body = buildBody();
      await apiPost(`/v1/extensions/resources/${group}/${resource}/-/validate`, body);
      validationResult.value = { ok: true, message: "Validation passed" };
    } catch (err) {
      const msg = err instanceof ApiError ? err.detail ?? err.message : "Validation failed";
      validationResult.value = { ok: false, message: msg };
    } finally {
      validating.value = false;
    }
  }, [group, resource, buildBody]);

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
        <p style={{ fontSize: "14px", color: "var(--error)", marginBottom: "12px" }}>
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

  const hasSpecSchema = specSchema.value !== null && specSchema.value.properties != null;
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
        <a href="/extensions" style={{ color: "var(--accent)", textDecoration: "none" }}>
          Extensions
        </a>
        <span>/</span>
        <a href="/extensions" style={{ color: "var(--accent)", textDecoration: "none" }}>
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
                background: viewMode.value === m ? "var(--accent)" : "transparent",
                color: viewMode.value === m ? "var(--bg-base)" : "var(--text-muted)",
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
            <div style={{ flex: 1, height: "1px", background: "var(--border-subtle)" }} />
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
              onInput={(e) => (formName.value = (e.target as HTMLInputElement).value)}
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
                onChange={(e) => (formNamespace.value = (e.target as HTMLSelectElement).value)}
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
          <div style={sectionHeaderStyle}>
            <span>Labels</span>
            <div style={{ flex: 1, height: "1px", background: "var(--border-subtle)" }} />
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: "6px", marginBottom: "16px" }}>
            {formLabels.value.map((label, idx) => (
              <div key={idx} style={{ display: "flex", gap: "6px", alignItems: "center" }}>
                <input
                  type="text"
                  placeholder="key"
                  style={{ ...inputStyle, flex: 1 }}
                  value={label.key}
                  onInput={(e) => {
                    const next = [...formLabels.value];
                    next[idx] = { ...next[idx], key: (e.target as HTMLInputElement).value };
                    formLabels.value = next;
                  }}
                />
                <input
                  type="text"
                  placeholder="value"
                  style={{ ...inputStyle, flex: 1 }}
                  value={label.value}
                  onInput={(e) => {
                    const next = [...formLabels.value];
                    next[idx] = { ...next[idx], value: (e.target as HTMLInputElement).value };
                    formLabels.value = next;
                  }}
                />
                <button
                  type="button"
                  onClick={() => {
                    formLabels.value = formLabels.value.filter((_, i) => i !== idx);
                  }}
                  style={{
                    background: "none",
                    border: "none",
                    padding: "4px 8px",
                    fontSize: "14px",
                    color: "var(--error)",
                    cursor: "pointer",
                    lineHeight: 1,
                    flexShrink: 0,
                  }}
                  title="Remove"
                >
                  &times;
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() => {
                formLabels.value = [...formLabels.value, { key: "", value: "" }];
              }}
              style={{
                background: "none",
                border: "1px dashed var(--border-primary)",
                borderRadius: "6px",
                padding: "6px 12px",
                fontSize: "12px",
                color: "var(--accent)",
                cursor: "pointer",
              }}
            >
              + Add Label
            </button>
          </div>

          {/* ── Spec Section ─────────────────────────────────────────── */}
          <div style={sectionHeaderStyle}>
            <span>Spec</span>
            <div style={{ flex: 1, height: "1px", background: "var(--border-subtle)" }} />
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
                <p style={{ fontSize: "12px", color: "var(--text-muted)", marginBottom: "8px" }}>
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
                  value={
                    Object.keys(formSpec.value).length > 0
                      ? (() => { try { return yamlStringify(formSpec.value); } catch { return ""; } })()
                      : ""
                  }
                  onInput={(e) => {
                    try {
                      const parsed = yamlParse((e.target as HTMLTextAreaElement).value);
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
            background: validationResult.value.ok ? "var(--success-dim)" : "var(--error-dim)",
            color: validationResult.value.ok ? "var(--success)" : "var(--error)",
            border: `1px solid ${validationResult.value.ok ? "var(--success)" : "var(--error)"}`,
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
