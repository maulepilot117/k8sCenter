import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import type { SchemaProperty } from "@/lib/crd-types.ts";
import { parse, stringify } from "yaml";
import { inputStyle, labelStyle, selectStyle } from "@/lib/form-styles.ts";

// ── Types ───────────────────────────────────────────────────────────────

export interface SchemaFormFieldProps {
  name: string;
  path: string;
  schema: SchemaProperty;
  value: unknown;
  onChange: (path: string, value: unknown) => void;
  required?: boolean;
  depth: number;
}

const descStyle: Record<string, string> = {
  fontSize: "11px",
  color: "var(--text-muted)",
  margin: "2px 0 6px",
  lineHeight: "1.4",
};

const fieldsetHeaderStyle: Record<string, string | number> = {
  display: "flex",
  alignItems: "center",
  gap: "8px",
  background: "var(--bg-surface)",
  border: "1px solid var(--border-primary)",
  borderRadius: "8px",
  padding: "10px 14px",
  cursor: "pointer",
  width: "100%",
  fontSize: "13px",
  fontWeight: 500,
  color: "var(--text-primary)",
};

const addBtnStyle: Record<string, string | number> = {
  background: "none",
  border: "1px dashed var(--border-primary)",
  borderRadius: "6px",
  padding: "6px 12px",
  fontSize: "12px",
  color: "var(--accent)",
  cursor: "pointer",
};

const removeBtnStyle: Record<string, string | number> = {
  background: "none",
  border: "none",
  padding: "4px 8px",
  fontSize: "14px",
  color: "var(--error)",
  cursor: "pointer",
  lineHeight: 1,
  flexShrink: 0,
};

// ── Helpers ─────────────────────────────────────────────────────────────

function fieldId(path: string): string {
  return `field-${path.replace(/\./g, "-")}`;
}

function isPrimitive(schema: SchemaProperty | undefined): boolean {
  if (!schema) return true;
  const t = schema.type;
  return t === "string" || t === "integer" || t === "number" || t === "boolean";
}

// ── Component ───────────────────────────────────────────────────────────

export default function SchemaFormField(props: SchemaFormFieldProps) {
  const { name, path, schema, value, onChange, required, depth } = props;
  const id = fieldId(path);

  // At depth 5+, render YAML textarea for the entire subtree
  if (depth >= 5) {
    return (
      <YamlSubtreeField
        name={name}
        path={path}
        value={value}
        onChange={onChange}
        required={required}
        description={schema.description}
      />
    );
  }

  // x-kubernetes-int-or-string: text input
  if (schema["x-kubernetes-int-or-string"]) {
    return (
      <FieldWrapper
        name={name}
        path={path}
        required={required}
        description={schema.description}
      >
        <input
          id={id}
          type="text"
          style={inputStyle}
          value={value != null ? String(value) : ""}
          placeholder="integer or string"
          onInput={(e) => {
            const v = (e.target as HTMLInputElement).value;
            const num = Number(v);
            onChange(
              path,
              v !== "" && !isNaN(num) && String(num) === v ? num : v,
            );
          }}
        />
      </FieldWrapper>
    );
  }

  // x-kubernetes-preserve-unknown-fields: key-value editor
  if (schema["x-kubernetes-preserve-unknown-fields"] && !schema.properties) {
    return (
      <KeyValueEditor
        name={name}
        path={path}
        value={value as Record<string, string> | undefined}
        onChange={onChange}
        required={required}
        description={schema.description}
      />
    );
  }

  // oneOf / anyOf: variant selector
  if (schema.oneOf || schema.anyOf) {
    return (
      <VariantSelector
        name={name}
        path={path}
        schema={schema}
        value={value}
        onChange={onChange}
        required={required}
        depth={depth}
      />
    );
  }

  // additionalProperties (map type)
  if (
    schema.type === "object" &&
    !schema.properties &&
    schema.additionalProperties !== undefined &&
    schema.additionalProperties !== false
  ) {
    return (
      <KeyValueEditor
        name={name}
        path={path}
        value={value as Record<string, string> | undefined}
        onChange={onChange}
        required={required}
        description={schema.description}
      />
    );
  }

  // Object with properties: collapsible fieldset
  if (schema.type === "object" && schema.properties) {
    return (
      <ObjectFieldset
        name={name}
        path={path}
        schema={schema}
        value={value as Record<string, unknown> | undefined}
        onChange={onChange}
        required={required}
        depth={depth}
      />
    );
  }

  // Array
  if (schema.type === "array" && schema.items) {
    if (isPrimitive(schema.items)) {
      return (
        <PrimitiveArrayField
          name={name}
          path={path}
          schema={schema}
          value={value as unknown[] | undefined}
          onChange={onChange}
          required={required}
        />
      );
    }
    return (
      <ObjectArrayField
        name={name}
        path={path}
        schema={schema}
        value={value as Record<string, unknown>[] | undefined}
        onChange={onChange}
        required={required}
        depth={depth}
      />
    );
  }

  // Boolean
  if (schema.type === "boolean") {
    return (
      <FieldWrapper
        name={name}
        path={path}
        required={required}
        description={schema.description}
      >
        <select
          id={id}
          style={selectStyle}
          value={value === true ? "true" : value === false ? "false" : ""}
          onChange={(e) => {
            const v = (e.target as HTMLSelectElement).value;
            onChange(path, v === "" ? undefined : v === "true");
          }}
        >
          <option value="">-- select --</option>
          <option value="true">true</option>
          <option value="false">false</option>
        </select>
      </FieldWrapper>
    );
  }

  // Integer / Number
  if (schema.type === "integer" || schema.type === "number") {
    return (
      <FieldWrapper
        name={name}
        path={path}
        required={required}
        description={schema.description}
      >
        <input
          id={id}
          type="number"
          style={inputStyle}
          value={value != null ? String(value) : ""}
          min={schema.minimum}
          max={schema.maximum}
          step={schema.type === "integer" ? 1 : "any"}
          onInput={(e) => {
            const v = (e.target as HTMLInputElement).value;
            if (v === "") {
              onChange(path, undefined);
            } else {
              onChange(
                path,
                schema.type === "integer" ? parseInt(v, 10) : parseFloat(v),
              );
            }
          }}
        />
      </FieldWrapper>
    );
  }

  // String with enum
  if (schema.type === "string" && schema.enum) {
    if (schema.enum.length > 100) {
      // Large enum: text input with datalist
      const listId = `${id}-list`;
      return (
        <FieldWrapper
          name={name}
          path={path}
          required={required}
          description={schema.description}
        >
          <input
            id={id}
            type="text"
            list={listId}
            style={inputStyle}
            value={(value as string) ?? ""}
            onInput={(e) =>
              onChange(path, (e.target as HTMLInputElement).value || undefined)}
          />
          <datalist id={listId}>
            {schema.enum.map((opt) => <option key={opt} value={opt} />)}
          </datalist>
        </FieldWrapper>
      );
    }
    return (
      <FieldWrapper
        name={name}
        path={path}
        required={required}
        description={schema.description}
      >
        <select
          id={id}
          style={selectStyle}
          value={(value as string) ?? ""}
          onChange={(e) =>
            onChange(path, (e.target as HTMLSelectElement).value || undefined)}
        >
          <option value="">-- select --</option>
          {schema.enum!.map((opt) => (
            <option key={opt} value={opt}>{opt}</option>
          ))}
        </select>
      </FieldWrapper>
    );
  }

  // String (default, including format: date-time)
  const placeholder = schema.format === "date-time"
    ? "e.g. 2024-01-01T00:00:00Z"
    : schema.format
    ? `format: ${schema.format}`
    : "";

  return (
    <FieldWrapper
      name={name}
      path={path}
      required={required}
      description={schema.description}
    >
      <input
        id={id}
        type="text"
        style={inputStyle}
        value={(value as string) ?? ""}
        placeholder={placeholder}
        onInput={(e) =>
          onChange(path, (e.target as HTMLInputElement).value || undefined)}
      />
    </FieldWrapper>
  );
}

// ── FieldWrapper ────────────────────────────────────────────────────────

function FieldWrapper(
  { name, path, required, description, children }: {
    name: string;
    path: string;
    required?: boolean;
    description?: string;
    children: preact.ComponentChildren;
  },
) {
  const id = fieldId(path);
  return (
    <div style={{ marginBottom: "12px" }}>
      <label htmlFor={id} style={labelStyle}>
        {name}
        {required && (
          <span style={{ color: "var(--error)", marginLeft: "3px" }}>*</span>
        )}
      </label>
      {description && <p style={descStyle}>{description}</p>}
      {children}
    </div>
  );
}

// ── Object Fieldset (collapsible) ───────────────────────────────────────

function ObjectFieldset(
  { name, path, schema, value, onChange, required, depth }: {
    name: string;
    path: string;
    schema: SchemaProperty;
    value: Record<string, unknown> | undefined;
    onChange: (path: string, value: unknown) => void;
    required?: boolean;
    depth: number;
  },
) {
  // Required objects start expanded, optional start collapsed (lazy mount)
  const expanded = useSignal(!!required);
  const props = schema.properties!;
  const requiredFields = schema.required ?? [];

  const toggle = useCallback(() => {
    expanded.value = !expanded.value;
  }, []);

  const badge = required ? "required" : "optional";
  const badgeColor = required ? "var(--warning)" : "var(--text-muted)";

  return (
    <div style={{ marginBottom: "12px" }}>
      <button
        type="button"
        onClick={toggle}
        style={fieldsetHeaderStyle}
      >
        <span style={{ fontSize: "10px", width: "14px", flexShrink: 0 }}>
          {expanded.value ? "\u25BC" : "\u25B6"}
        </span>
        <span style={{ flex: 1, textAlign: "left" }}>{name}</span>
        <span
          style={{
            fontSize: "10px",
            padding: "2px 6px",
            borderRadius: "4px",
            background: "var(--bg-elevated)",
            color: badgeColor,
            fontWeight: 500,
          }}
        >
          {badge}
        </span>
        <span style={{ fontSize: "10px", color: "var(--text-muted)" }}>
          object
        </span>
      </button>
      {schema.description && (
        <p style={{ ...descStyle, marginLeft: "14px" }}>{schema.description}</p>
      )}
      {expanded.value && (
        <div
          style={{
            marginTop: "8px",
            marginLeft: "16px",
            paddingLeft: "12px",
            borderLeft: "2px solid var(--border-subtle)",
          }}
        >
          {Object.entries(props).map(([key, propSchema]) => (
            <SchemaFormField
              key={key}
              name={key}
              path={`${path}.${key}`}
              schema={propSchema}
              value={value?.[key]}
              onChange={onChange}
              required={requiredFields.includes(key)}
              depth={depth + 1}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ── Primitive Array ─────────────────────────────────────────────────────

function PrimitiveArrayField(
  { name, path, schema, value, onChange, required }: {
    name: string;
    path: string;
    schema: SchemaProperty;
    value: unknown[] | undefined;
    onChange: (path: string, value: unknown) => void;
    required?: boolean;
  },
) {
  const items = value ?? [];

  const addItem = useCallback(() => {
    onChange(path, [...items, ""]);
  }, [path, items, onChange]);

  const removeItem = useCallback(
    (idx: number) => {
      const next = items.filter((_, i) => i !== idx);
      onChange(path, next.length > 0 ? next : undefined);
    },
    [path, items, onChange],
  );

  const updateItem = useCallback(
    (idx: number, val: string) => {
      const next = [...items];
      // Coerce to number if the item schema says integer/number
      const itemType = schema.items?.type;
      if (itemType === "integer") {
        next[idx] = val === "" ? "" : parseInt(val, 10);
      } else if (itemType === "number") {
        next[idx] = val === "" ? "" : parseFloat(val);
      } else {
        next[idx] = val;
      }
      onChange(path, next);
    },
    [path, items, onChange, schema.items?.type],
  );

  return (
    <FieldWrapper
      name={name}
      path={path}
      required={required}
      description={schema.description}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
        {items.map((item, idx) => (
          <div
            key={idx}
            style={{ display: "flex", gap: "6px", alignItems: "center" }}
          >
            <input
              type={schema.items?.type === "integer" ||
                  schema.items?.type === "number"
                ? "number"
                : "text"}
              style={{ ...inputStyle, flex: 1 }}
              value={item != null ? String(item) : ""}
              onInput={(e) =>
                updateItem(idx, (e.target as HTMLInputElement).value)}
            />
            <button
              type="button"
              onClick={() => removeItem(idx)}
              style={removeBtnStyle}
              title="Remove"
            >
              &times;
            </button>
          </div>
        ))}
        <button type="button" onClick={addItem} style={addBtnStyle}>
          + Add
        </button>
      </div>
    </FieldWrapper>
  );
}

// ── Object Array ────────────────────────────────────────────────────────

function ObjectArrayField(
  { name, path, schema, value, onChange, required, depth }: {
    name: string;
    path: string;
    schema: SchemaProperty;
    value: Record<string, unknown>[] | undefined;
    onChange: (path: string, value: unknown) => void;
    required?: boolean;
    depth: number;
  },
) {
  const items = value ?? [];

  const addItem = useCallback(() => {
    onChange(path, [...items, {}]);
  }, [path, items, onChange]);

  const removeItem = useCallback(
    (idx: number) => {
      const next = items.filter((_, i) => i !== idx);
      onChange(path, next.length > 0 ? next : undefined);
    },
    [path, items, onChange],
  );

  // Each array item's onChange must splice into the array
  const updateItem = useCallback(
    (idx: number, _fieldPath: string, fieldValue: unknown) => {
      // _fieldPath is the full dot path like "spec.rules.0.host"
      // We need to extract the relative key within this item
      const prefix = `${path}.${idx}.`;
      const relKey = _fieldPath.startsWith(prefix)
        ? _fieldPath.slice(prefix.length)
        : _fieldPath;

      const next = [...items];
      const item = { ...next[idx] };

      // Handle nested paths within the item
      const parts = relKey.split(".");
      if (parts.length === 1) {
        if (fieldValue === undefined) {
          delete item[parts[0]];
        } else {
          item[parts[0]] = fieldValue;
        }
      } else {
        // Deep set
        let current: Record<string, unknown> = item;
        for (let i = 0; i < parts.length - 1; i++) {
          if (!(parts[i] in current) || typeof current[parts[i]] !== "object") {
            current[parts[i]] = {};
          }
          current = current[parts[i]] as Record<string, unknown>;
        }
        if (fieldValue === undefined) {
          delete current[parts[parts.length - 1]];
        } else {
          current[parts[parts.length - 1]] = fieldValue;
        }
      }

      next[idx] = item;
      onChange(path, next);
    },
    [path, items, onChange],
  );

  const itemSchema = schema.items!;

  return (
    <FieldWrapper
      name={name}
      path={path}
      required={required}
      description={schema.description}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
        {items.map((item, idx) => (
          <ArrayItemFieldset
            key={idx}
            index={idx}
            path={`${path}.${idx}`}
            schema={itemSchema}
            value={item}
            onChange={(fieldPath, fieldValue) =>
              updateItem(idx, fieldPath, fieldValue)}
            onRemove={() => removeItem(idx)}
            depth={depth}
          />
        ))}
        <button type="button" onClick={addItem} style={addBtnStyle}>
          + Add {name}
        </button>
      </div>
    </FieldWrapper>
  );
}

function ArrayItemFieldset(
  { index, path, schema, value, onChange, onRemove, depth }: {
    index: number;
    path: string;
    schema: SchemaProperty;
    value: Record<string, unknown>;
    onChange: (path: string, value: unknown) => void;
    onRemove: () => void;
    depth: number;
  },
) {
  const expanded = useSignal(true);
  const props = schema.properties ?? {};
  const requiredFields = schema.required ?? [];

  return (
    <div
      style={{
        border: "1px solid var(--border-subtle)",
        borderRadius: "8px",
        overflow: "hidden",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "8px",
          padding: "8px 12px",
          background: "var(--bg-surface)",
          cursor: "pointer",
        }}
        onClick={() => (expanded.value = !expanded.value)}
      >
        <span style={{ fontSize: "10px", width: "14px" }}>
          {expanded.value ? "\u25BC" : "\u25B6"}
        </span>
        <span
          style={{ flex: 1, fontSize: "12px", color: "var(--text-secondary)" }}
        >
          Item {index + 1}
        </span>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          style={{ ...removeBtnStyle, fontSize: "12px" }}
        >
          Remove
        </button>
      </div>
      {expanded.value && (
        <div style={{ padding: "12px", paddingTop: "8px" }}>
          {/* If the schema defines properties, render them as fields */}
          {Object.keys(props).length > 0
            ? Object.entries(props).map(([key, propSchema]) => (
              <SchemaFormField
                key={key}
                name={key}
                path={`${path}.${key}`}
                schema={propSchema}
                value={value[key]}
                onChange={onChange}
                required={requiredFields.includes(key)}
                depth={depth + 1}
              />
            ))
            : (
              // No properties schema — fall back to YAML textarea
              <YamlSubtreeField
                name={`Item ${index + 1}`}
                path={path}
                value={value}
                onChange={onChange}
              />
            )}
        </div>
      )}
    </div>
  );
}

// ── Key-Value Editor ────────────────────────────────────────────────────

function KeyValueEditor(
  { name, path, value, onChange, required, description }: {
    name: string;
    path: string;
    value: Record<string, string> | undefined;
    onChange: (path: string, value: unknown) => void;
    required?: boolean;
    description?: string;
  },
) {
  const entries = Object.entries(value ?? {});

  const addEntry = useCallback(() => {
    onChange(path, { ...(value ?? {}), "": "" });
  }, [path, value, onChange]);

  const updateKey = useCallback(
    (_oldKey: string, newKey: string, idx: number) => {
      const current = { ...(value ?? {}) };
      const allEntries = Object.entries(current);
      // Replace the entry at the given index
      allEntries[idx] = [newKey, allEntries[idx]?.[1] ?? ""];
      const result: Record<string, string> = {};
      for (const [k, v] of allEntries) {
        result[k] = v;
      }
      onChange(path, result);
    },
    [path, value, onChange],
  );

  const updateValue = useCallback(
    (key: string, val: string) => {
      onChange(path, { ...(value ?? {}), [key]: val });
    },
    [path, value, onChange],
  );

  const removeEntry = useCallback(
    (key: string) => {
      const current = { ...(value ?? {}) };
      delete current[key];
      onChange(path, Object.keys(current).length > 0 ? current : undefined);
    },
    [path, value, onChange],
  );

  return (
    <FieldWrapper
      name={name}
      path={path}
      required={required}
      description={description}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
        {entries.map(([k, v], idx) => (
          <div
            key={idx}
            style={{ display: "flex", gap: "6px", alignItems: "center" }}
          >
            <input
              type="text"
              placeholder="key"
              style={{ ...inputStyle, flex: 1 }}
              value={k}
              onInput={(e) =>
                updateKey(k, (e.target as HTMLInputElement).value, idx)}
            />
            <input
              type="text"
              placeholder="value"
              style={{ ...inputStyle, flex: 1 }}
              value={v}
              onInput={(e) =>
                updateValue(k, (e.target as HTMLInputElement).value)}
            />
            <button
              type="button"
              onClick={() => removeEntry(k)}
              style={removeBtnStyle}
              title="Remove"
            >
              &times;
            </button>
          </div>
        ))}
        <button type="button" onClick={addEntry} style={addBtnStyle}>
          + Add
        </button>
      </div>
    </FieldWrapper>
  );
}

// ── Variant Selector (oneOf / anyOf) ────────────────────────────────────

function VariantSelector(
  { name, path, schema, value, onChange, required, depth }: {
    name: string;
    path: string;
    schema: SchemaProperty;
    value: unknown;
    onChange: (path: string, value: unknown) => void;
    required?: boolean;
    depth: number;
  },
) {
  const variants = schema.oneOf ?? schema.anyOf ?? [];
  const selected = useSignal(0);

  // Build labels for each variant
  const labels = variants.map((v, i) => {
    if (v.type) return v.type;
    if (v.properties) {
      const keys = Object.keys(v.properties).slice(0, 3).join(", ");
      return keys || `Variant ${i + 1}`;
    }
    return `Variant ${i + 1}`;
  });

  const selectedSchema = variants[selected.value];

  return (
    <FieldWrapper
      name={name}
      path={path}
      required={required}
      description={schema.description}
    >
      <select
        style={{ ...selectStyle, marginBottom: "8px" }}
        value={selected.value}
        onChange={(e) => {
          selected.value = parseInt((e.target as HTMLSelectElement).value, 10);
          // Clear the value when switching variants
          onChange(path, undefined);
        }}
      >
        {labels.map((label, i) => (
          <option key={i} value={i}>
            {label}
          </option>
        ))}
      </select>
      {selectedSchema && (
        <SchemaFormField
          name={`${name} value`}
          path={path}
          schema={selectedSchema}
          value={value}
          onChange={onChange}
          required={required}
          depth={depth + 1}
        />
      )}
    </FieldWrapper>
  );
}

// ── YAML Subtree (depth 5+ fallback) ────────────────────────────────────

function YamlSubtreeField(
  { name, path, value, onChange, required, description }: {
    name: string;
    path: string;
    value: unknown;
    onChange: (path: string, value: unknown) => void;
    required?: boolean;
    description?: string;
  },
) {
  // Convert current value to YAML for display
  const yamlText = useSignal(
    value != null && typeof value === "object"
      ? stringify(value)
      : value != null
      ? String(value)
      : "",
  );

  const handleChange = useCallback(
    (e: Event) => {
      const text = (e.target as HTMLTextAreaElement).value;
      yamlText.value = text;
      try {
        const parsed = parse(text);
        onChange(path, parsed);
      } catch {
        // Invalid YAML — keep the text but don't update form state
      }
    },
    [path, onChange],
  );

  return (
    <FieldWrapper
      name={name}
      path={path}
      required={required}
      description={description}
    >
      <textarea
        id={fieldId(path)}
        style={{
          ...inputStyle,
          minHeight: "120px",
          resize: "vertical",
          lineHeight: "1.5",
        }}
        value={yamlText.value}
        onInput={handleChange}
        placeholder="# Enter YAML..."
        spellcheck={false}
      />
    </FieldWrapper>
  );
}
