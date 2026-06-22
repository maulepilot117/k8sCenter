import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";

type SecretType =
  | "Opaque"
  | "kubernetes.io/tls"
  | "kubernetes.io/basic-auth"
  | "kubernetes.io/dockerconfigjson";

interface KeyValuePair {
  key: string;
  value: string;
}

interface SecretFormState {
  name: string;
  namespace: string;
  type: SecretType;
  // Opaque: arbitrary key-value pairs
  opaqueEntries: KeyValuePair[];
  // TLS
  tlsCrt: string;
  tlsKey: string;
  // BasicAuth
  basicUsername: string;
  basicPassword: string;
  // DockerConfigJSON
  dockerRegistry: string;
  dockerUsername: string;
  dockerPassword: string;
  dockerEmail: string;
}

const SECRET_TYPES: { value: SecretType; label: string; desc: string }[] = [
  { value: "Opaque", label: "Opaque", desc: "Arbitrary key-value data" },
  {
    value: "kubernetes.io/tls",
    label: "TLS Certificate",
    desc: "TLS certificate and private key",
  },
  {
    value: "kubernetes.io/dockerconfigjson",
    label: "Docker Registry",
    desc: "Docker registry credentials",
  },
  {
    value: "kubernetes.io/basic-auth",
    label: "Basic Auth",
    desc: "Username and password",
  },
];

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Type & secret data" },
  { label: "Review", sub: "Preview & apply" },
];

function initialState(): SecretFormState {
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    type: "Opaque",
    opaqueEntries: [{ key: "", value: "" }],
    tlsCrt: "",
    tlsKey: "",
    basicUsername: "",
    basicPassword: "",
    dockerRegistry: "",
    dockerUsername: "",
    dockerPassword: "",
    dockerEmail: "",
  };
}

function buildDockerConfigJSON(
  registry: string,
  username: string,
  password: string,
  email: string,
): string {
  const auth = btoa(`${username}:${password}`);
  const config = {
    auths: {
      [registry || "https://index.docker.io/v1/"]: {
        username,
        password,
        email,
        auth,
      },
    },
  };
  return JSON.stringify(config);
}

/**
 * CRITICAL: buildDataMap sends actual secret values to the preview endpoint
 * for YAML generation only. The apply step uses SSA with stripSensitiveDataFields
 * enforced server-side — secret data/stringData fields are never overwritten
 * with masked "****" values. This wizard creates NEW secrets only; it never
 * seeds the editor from an existing secret's masked GET response.
 */
function buildDataMap(f: SecretFormState): Record<string, string> {
  switch (f.type) {
    case "Opaque": {
      const data: Record<string, string> = {};
      for (const entry of f.opaqueEntries) {
        if (entry.key.trim()) {
          data[entry.key.trim()] = entry.value;
        }
      }
      return data;
    }
    case "kubernetes.io/tls":
      return { "tls.crt": f.tlsCrt, "tls.key": f.tlsKey };
    case "kubernetes.io/basic-auth":
      return { username: f.basicUsername, password: f.basicPassword };
    case "kubernetes.io/dockerconfigjson":
      return {
        ".dockerconfigjson": buildDockerConfigJSON(
          f.dockerRegistry,
          f.dockerUsername,
          f.dockerPassword,
          f.dockerEmail,
        ),
      };
    default:
      return {};
  }
}

function buildManifest(f: SecretFormState): string {
  const keyCount = (() => {
    switch (f.type) {
      case "Opaque":
        return f.opaqueEntries.filter((e) => e.key.trim()).length;
      case "kubernetes.io/tls":
        return 2;
      case "kubernetes.io/basic-auth":
        return 2;
      case "kubernetes.io/dockerconfigjson":
        return 1;
      default:
        return 0;
    }
  })();
  return `apiVersion: v1\nkind: Secret\nmetadata:\n  name: ${
    f.name || "<name>"
  }\n  namespace: ${f.namespace}\ntype: ${f.type}\ndata:\n  # ${keyCount} key${
    keyCount !== 1 ? "s" : ""
  } (base64-encoded by server)`;
}

export default function SecretWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const currentStep = useSignal(0);
  const form = useSignal<SecretFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  const updateField = useCallback(
    (field: string, value: unknown) => {
      dirty.value = true;
      form.value = { ...form.value, [field]: value };
    },
    [],
  );

  const updateOpaqueEntry = useCallback(
    (index: number, field: "key" | "value", value: string) => {
      dirty.value = true;
      const entries = [...form.value.opaqueEntries];
      entries[index] = { ...entries[index], [field]: value };
      form.value = { ...form.value, opaqueEntries: entries };
    },
    [],
  );

  const addOpaqueEntry = useCallback(() => {
    dirty.value = true;
    form.value = {
      ...form.value,
      opaqueEntries: [...form.value.opaqueEntries, { key: "", value: "" }],
    };
  }, []);

  const removeOpaqueEntry = useCallback((index: number) => {
    dirty.value = true;
    const entries = form.value.opaqueEntries.filter((_, i) => i !== index);
    form.value = {
      ...form.value,
      opaqueEntries: entries.length > 0 ? entries : [{ key: "", value: "" }],
    };
  }, []);

  const validateStep = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";

    switch (f.type) {
      case "Opaque": {
        const hasData = f.opaqueEntries.some((e) => e.key.trim() !== "");
        if (!hasData) {
          errs.opaqueEntries = "At least one key-value pair is required";
        }
        break;
      }
      case "kubernetes.io/tls":
        if (!f.tlsCrt.trim()) errs.tlsCrt = "Certificate is required";
        if (!f.tlsKey.trim()) errs.tlsKey = "Private key is required";
        break;
      case "kubernetes.io/basic-auth":
        if (!f.basicUsername.trim()) {
          errs.basicUsername = "Username is required";
        }
        break;
      case "kubernetes.io/dockerconfigjson":
        if (!f.dockerUsername.trim()) {
          errs.dockerUsername = "Username is required";
        }
        if (!f.dockerPassword.trim()) {
          errs.dockerPassword = "Password is required";
        }
        break;
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
    const payload = {
      name: f.name,
      namespace: f.namespace,
      type: f.type,
      data: buildDataMap(f),
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/secret/preview",
        payload,
      );
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      previewLoading.value = false;
    }
  };

  const f = form.value;

  return (
    <WizardShell
      title="Create Secret"
      subtitle={`Step ${currentStep.value + 1} of 2 · namespace ${f.namespace}`}
      icon={
        <svg
          width="21"
          height="21"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M8 11a3 3 0 1 0 6 0 3 3 0 0 0-6 0z" />
          <path d="M11 14v3M9 16h4" />
          <path d="M4 4h12v6a8 8 0 0 1-12 0V4z" />
        </svg>
      }
      steps={STEPS}
      current={currentStep.value}
      onStep={(i) => {
        if (i < currentStep.value) currentStep.value = i;
      }}
      onCancel={close}
      onBack={goBack}
      onNext={currentStep.value === 0 ? goNext : close}
      nextLabel={currentStep.value === 0 ? "Continue" : "Close"}
      yaml={currentStep.value === 0 ? buildManifest(f) : undefined}
    >
      {currentStep.value === 0 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "18px",
            maxWidth: "480px",
          }}
        >
          {/* Name */}
          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "5px",
              }}
            >
              Name <span style={{ color: "var(--danger)" }}>*</span>
            </label>
            <input
              type="text"
              value={f.name}
              onInput={(e) =>
                updateField("name", (e.target as HTMLInputElement).value)}
              class={WIZARD_INPUT_CLASS}
              placeholder="e.g. my-secret"
            />
            {errors.value.name && (
              <p
                style={{
                  marginTop: "4px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.name}
              </p>
            )}
          </div>

          {/* Namespace */}
          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "5px",
              }}
            >
              Namespace <span style={{ color: "var(--danger)" }}>*</span>
            </label>
            <select
              value={f.namespace}
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

          {/* Type selector */}
          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "8px",
              }}
            >
              Type
            </label>
            <div
              style={{ display: "flex", flexDirection: "column", gap: "6px" }}
            >
              {SECRET_TYPES.map((st) => (
                <label
                  key={st.value}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "10px",
                    borderRadius: "8px",
                    border: `1px solid ${
                      f.type === st.value
                        ? "var(--accent)"
                        : "var(--border-subtle)"
                    }`,
                    padding: "9px 12px",
                    cursor: "pointer",
                    background: f.type === st.value
                      ? "var(--accent-dim)"
                      : "transparent",
                    transition: "background 0.12s, border-color 0.12s",
                  }}
                >
                  <input
                    type="radio"
                    name="secretType"
                    value={st.value}
                    checked={f.type === st.value}
                    onChange={() => updateField("type", st.value)}
                    style={{ accentColor: "var(--accent)" }}
                  />
                  <span>
                    <span
                      style={{
                        fontSize: "13px",
                        fontWeight: 600,
                        color: "var(--text-primary)",
                      }}
                    >
                      {st.label}
                    </span>
                    <span
                      style={{
                        marginLeft: "8px",
                        fontSize: "11.5px",
                        color: "var(--text-muted)",
                      }}
                    >
                      {st.desc}
                    </span>
                  </span>
                </label>
              ))}
            </div>
          </div>

          {/* Type-specific data inputs */}
          <div
            style={{
              borderTop: "1px solid var(--border-subtle)",
              paddingTop: "16px",
            }}
          >
            <h3
              style={{
                margin: "0 0 12px",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
              }}
            >
              Data
            </h3>

            {/* Opaque: key-value editor */}
            {f.type === "Opaque" && (
              <div
                style={{ display: "flex", flexDirection: "column", gap: "6px" }}
              >
                {f.opaqueEntries.map((entry, i) => (
                  <div
                    key={i}
                    style={{
                      display: "flex",
                      alignItems: "flex-start",
                      gap: "8px",
                    }}
                  >
                    <input
                      type="text"
                      value={entry.key}
                      onInput={(e) =>
                        updateOpaqueEntry(
                          i,
                          "key",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="Key"
                      style={{ flex: 1 }}
                    />
                    <input
                      type="password"
                      value={entry.value}
                      onInput={(e) =>
                        updateOpaqueEntry(
                          i,
                          "value",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="Value"
                      style={{ flex: 1 }}
                    />
                    <button
                      type="button"
                      onClick={() => removeOpaqueEntry(i)}
                      style={{
                        marginTop: "2px",
                        padding: "6px",
                        borderRadius: "6px",
                        border: "none",
                        cursor: "pointer",
                        background: "transparent",
                        color: "var(--text-muted)",
                        flexShrink: 0,
                      }}
                      title="Remove entry"
                    >
                      <svg
                        width="14"
                        height="14"
                        fill="none"
                        viewBox="0 0 20 20"
                        stroke="currentColor"
                        stroke-width="1.8"
                        stroke-linecap="round"
                      >
                        <path d="M5 5l10 10M15 5L5 15" />
                      </svg>
                    </button>
                  </div>
                ))}
                <button
                  type="button"
                  onClick={addOpaqueEntry}
                  style={{
                    alignSelf: "flex-start",
                    marginTop: "4px",
                    fontSize: "12.5px",
                    fontWeight: 600,
                    color: "var(--accent)",
                    background: "transparent",
                    border: "none",
                    cursor: "pointer",
                    padding: 0,
                  }}
                >
                  + Add Entry
                </button>
                {errors.value.opaqueEntries && (
                  <p
                    style={{
                      fontSize: "11px",
                      color: "var(--danger)",
                    }}
                  >
                    {errors.value.opaqueEntries}
                  </p>
                )}
              </div>
            )}

            {/* TLS: certificate + key textareas */}
            {f.type === "kubernetes.io/tls" && (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "12px",
                }}
              >
                <div>
                  <label
                    style={{
                      display: "block",
                      fontSize: "12.5px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                      marginBottom: "5px",
                    }}
                  >
                    Certificate (tls.crt){" "}
                    <span style={{ color: "var(--danger)" }}>*</span>
                  </label>
                  <textarea
                    value={f.tlsCrt}
                    onInput={(e) =>
                      updateField(
                        "tlsCrt",
                        (e.target as HTMLTextAreaElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    rows={6}
                    placeholder="-----BEGIN CERTIFICATE-----&#10;..."
                    style={{ fontFamily: "var(--font-mono)", fontSize: "11px" }}
                  />
                  {errors.value.tlsCrt && (
                    <p
                      style={{
                        marginTop: "4px",
                        fontSize: "11px",
                        color: "var(--danger)",
                      }}
                    >
                      {errors.value.tlsCrt}
                    </p>
                  )}
                </div>
                <div>
                  <label
                    style={{
                      display: "block",
                      fontSize: "12.5px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                      marginBottom: "5px",
                    }}
                  >
                    Private Key (tls.key){" "}
                    <span style={{ color: "var(--danger)" }}>*</span>
                  </label>
                  <textarea
                    value={f.tlsKey}
                    onInput={(e) =>
                      updateField(
                        "tlsKey",
                        (e.target as HTMLTextAreaElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    rows={6}
                    placeholder="-----BEGIN PRIVATE KEY-----&#10;..."
                    style={{ fontFamily: "var(--font-mono)", fontSize: "11px" }}
                  />
                  {errors.value.tlsKey && (
                    <p
                      style={{
                        marginTop: "4px",
                        fontSize: "11px",
                        color: "var(--danger)",
                      }}
                    >
                      {errors.value.tlsKey}
                    </p>
                  )}
                </div>
              </div>
            )}

            {/* BasicAuth: username + password */}
            {f.type === "kubernetes.io/basic-auth" && (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "12px",
                }}
              >
                <div>
                  <label
                    style={{
                      display: "block",
                      fontSize: "12.5px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                      marginBottom: "5px",
                    }}
                  >
                    Username <span style={{ color: "var(--danger)" }}>*</span>
                  </label>
                  <input
                    type="text"
                    value={f.basicUsername}
                    onInput={(e) =>
                      updateField(
                        "basicUsername",
                        (e.target as HTMLInputElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    placeholder="Username"
                  />
                  {errors.value.basicUsername && (
                    <p
                      style={{
                        marginTop: "4px",
                        fontSize: "11px",
                        color: "var(--danger)",
                      }}
                    >
                      {errors.value.basicUsername}
                    </p>
                  )}
                </div>
                <div>
                  <label
                    style={{
                      display: "block",
                      fontSize: "12.5px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                      marginBottom: "5px",
                    }}
                  >
                    Password
                  </label>
                  <input
                    type="password"
                    value={f.basicPassword}
                    onInput={(e) =>
                      updateField(
                        "basicPassword",
                        (e.target as HTMLInputElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    placeholder="Password"
                  />
                </div>
              </div>
            )}

            {/* DockerConfigJSON: registry, username, password, email */}
            {f.type === "kubernetes.io/dockerconfigjson" && (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "12px",
                }}
              >
                <div>
                  <label
                    style={{
                      display: "block",
                      fontSize: "12.5px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                      marginBottom: "5px",
                    }}
                  >
                    Registry URL
                  </label>
                  <input
                    type="text"
                    value={f.dockerRegistry}
                    onInput={(e) =>
                      updateField(
                        "dockerRegistry",
                        (e.target as HTMLInputElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    placeholder="https://index.docker.io/v1/"
                  />
                </div>
                <div>
                  <label
                    style={{
                      display: "block",
                      fontSize: "12.5px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                      marginBottom: "5px",
                    }}
                  >
                    Username <span style={{ color: "var(--danger)" }}>*</span>
                  </label>
                  <input
                    type="text"
                    value={f.dockerUsername}
                    onInput={(e) =>
                      updateField(
                        "dockerUsername",
                        (e.target as HTMLInputElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    placeholder="Username"
                  />
                  {errors.value.dockerUsername && (
                    <p
                      style={{
                        marginTop: "4px",
                        fontSize: "11px",
                        color: "var(--danger)",
                      }}
                    >
                      {errors.value.dockerUsername}
                    </p>
                  )}
                </div>
                <div>
                  <label
                    style={{
                      display: "block",
                      fontSize: "12.5px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                      marginBottom: "5px",
                    }}
                  >
                    Password <span style={{ color: "var(--danger)" }}>*</span>
                  </label>
                  <input
                    type="password"
                    value={f.dockerPassword}
                    onInput={(e) =>
                      updateField(
                        "dockerPassword",
                        (e.target as HTMLInputElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    placeholder="Password"
                  />
                  {errors.value.dockerPassword && (
                    <p
                      style={{
                        marginTop: "4px",
                        fontSize: "11px",
                        color: "var(--danger)",
                      }}
                    >
                      {errors.value.dockerPassword}
                    </p>
                  )}
                </div>
                <div>
                  <label
                    style={{
                      display: "block",
                      fontSize: "12.5px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                      marginBottom: "5px",
                    }}
                  >
                    Email
                  </label>
                  <input
                    type="email"
                    value={f.dockerEmail}
                    onInput={(e) =>
                      updateField(
                        "dockerEmail",
                        (e.target as HTMLInputElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    placeholder="user@example.com"
                  />
                </div>
              </div>
            )}
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
          detailBasePath="/config/secrets"
        />
      )}
    </WizardShell>
  );
}
