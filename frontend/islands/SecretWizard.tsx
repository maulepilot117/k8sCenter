import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

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

const STEPS = [{ title: "Configure" }, { title: "Review" }];

const TEXTAREA_CLASS =
  "mt-1 w-full rounded-md border border-border-primary bg-elevated px-3 py-2 text-sm text-text-primary font-mono focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand text-text-primary";

function initialState(): SecretFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
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

export default function SecretWizard() {
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

  const buildDataMap = (): Record<string, string> => {
    const f = form.value;
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
  };

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
      data: buildDataMap(),
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

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  const f = form.value;

  return (
    <div class="p-6">
      <div class="mb-6 flex items-center justify-between">
        <h1 class="text-2xl font-bold text-text-primary">
          Create Secret
        </h1>
        <a
          href="/config/secrets"
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
            {/* Name */}
            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Name <span class="text-red-500">*</span>
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
                <p class="mt-1 text-xs text-red-500">{errors.value.name}</p>
              )}
            </div>

            {/* Namespace */}
            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Namespace <span class="text-red-500">*</span>
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

            {/* Type */}
            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Type
              </label>
              <div class="mt-2 space-y-2">
                {SECRET_TYPES.map((st) => (
                  <label
                    key={st.value}
                    class="flex items-center gap-3 rounded-md border border-border-primary px-3 py-2 cursor-pointer hover:bg-surface /50"
                  >
                    <input
                      type="radio"
                      name="secretType"
                      value={st.value}
                      checked={f.type === st.value}
                      onChange={() => updateField("type", st.value)}
                      class="text-brand focus:ring-brand"
                    />
                    <div>
                      <span class="text-sm font-medium text-text-secondary">
                        {st.label}
                      </span>
                      <span class="ml-2 text-xs text-text-muted">
                        {st.desc}
                      </span>
                    </div>
                  </label>
                ))}
              </div>
            </div>

            {/* Type-specific data inputs */}
            <div class="border-t border-border-primary pt-4">
              <h3 class="text-sm font-medium text-text-secondary mb-3">
                Data
              </h3>

              {/* Opaque: key-value editor */}
              {f.type === "Opaque" && (
                <div class="space-y-2">
                  {f.opaqueEntries.map((entry, i) => (
                    <div key={i} class="flex items-start gap-2">
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
                      />
                      <button
                        type="button"
                        onClick={() =>
                          removeOpaqueEntry(i)}
                        class="mt-1 p-2 text-text-muted hover:text-red-500"
                        title="Remove entry"
                      >
                        <svg
                          class="w-4 h-4"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke="currentColor"
                        >
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            stroke-width="2"
                            d="M6 18L18 6M6 6l12 12"
                          />
                        </svg>
                      </button>
                    </div>
                  ))}
                  <button
                    type="button"
                    onClick={addOpaqueEntry}
                    class="text-sm text-brand hover:text-brand/80 font-medium"
                  >
                    + Add Entry
                  </button>
                  {errors.value.opaqueEntries && (
                    <p class="mt-1 text-xs text-red-500">
                      {errors.value.opaqueEntries}
                    </p>
                  )}
                </div>
              )}

              {/* TLS: certificate + key textareas */}
              {f.type === "kubernetes.io/tls" && (
                <div class="space-y-3">
                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
                      Certificate (tls.crt) <span class="text-red-500">*</span>
                    </label>
                    <textarea
                      value={f.tlsCrt}
                      onInput={(e) =>
                        updateField(
                          "tlsCrt",
                          (e.target as HTMLTextAreaElement).value,
                        )}
                      class={TEXTAREA_CLASS}
                      rows={6}
                      placeholder="-----BEGIN CERTIFICATE-----&#10;..."
                    />
                    {errors.value.tlsCrt && (
                      <p class="mt-1 text-xs text-red-500">
                        {errors.value.tlsCrt}
                      </p>
                    )}
                  </div>
                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
                      Private Key (tls.key) <span class="text-red-500">*</span>
                    </label>
                    <textarea
                      value={f.tlsKey}
                      onInput={(e) =>
                        updateField(
                          "tlsKey",
                          (e.target as HTMLTextAreaElement).value,
                        )}
                      class={TEXTAREA_CLASS}
                      rows={6}
                      placeholder="-----BEGIN PRIVATE KEY-----&#10;..."
                    />
                    {errors.value.tlsKey && (
                      <p class="mt-1 text-xs text-red-500">
                        {errors.value.tlsKey}
                      </p>
                    )}
                  </div>
                </div>
              )}

              {/* BasicAuth: username + password */}
              {f.type === "kubernetes.io/basic-auth" && (
                <div class="space-y-3">
                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
                      Username <span class="text-red-500">*</span>
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
                      <p class="mt-1 text-xs text-red-500">
                        {errors.value.basicUsername}
                      </p>
                    )}
                  </div>
                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
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
                <div class="space-y-3">
                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
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
                    <label class="block text-sm font-medium text-text-secondary">
                      Username <span class="text-red-500">*</span>
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
                      <p class="mt-1 text-xs text-red-500">
                        {errors.value.dockerUsername}
                      </p>
                    )}
                  </div>
                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
                      Password <span class="text-red-500">*</span>
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
                      <p class="mt-1 text-xs text-red-500">
                        {errors.value.dockerPassword}
                      </p>
                    )}
                  </div>
                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
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
