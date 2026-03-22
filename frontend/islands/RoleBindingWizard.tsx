import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";
import type { LocalUser } from "@/lib/user-types.ts";

interface SubjectRow {
  kind: "User" | "Group" | "ServiceAccount";
  name: string;
  namespace: string;
}

interface BindingFormState {
  name: string;
  namespace: string;
  roleRefKind: "Role" | "ClusterRole";
  roleRefName: string;
  subjects: SubjectRow[];
}

interface RoleItem {
  metadata: { name: string; namespace?: string };
}

interface RoleBindingWizardProps {
  clusterScoped: boolean;
}

const STEPS = [
  { title: "Basics" },
  { title: "Role Reference" },
  { title: "Subjects" },
  { title: "Review" },
];

const DNS_LABEL_REGEX = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

function initialState(clusterScoped: boolean): BindingFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
  return {
    name: "",
    namespace: clusterScoped ? "" : ns,
    roleRefKind: "ClusterRole",
    roleRefName: "",
    subjects: [{ kind: "User", name: "", namespace: "" }],
  };
}

export default function RoleBindingWizard(
  { clusterScoped }: RoleBindingWizardProps,
) {
  const currentStep = useSignal(0);
  const form = useSignal<BindingFormState>(initialState(clusterScoped));
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  // Data for dropdowns
  const namespaces = useSignal<string[]>(["default"]);
  const roles = useSignal<RoleItem[]>([]);
  const clusterRoles = useSignal<RoleItem[]>([]);
  const localUsers = useSignal<LocalUser[]>([]);
  const showUserPicker = useSignal<number | null>(null);

  // Review step state
  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  // Fetch namespaces
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          namespaces.value = resp.data.map((ns) => ns.metadata.name).sort();
        }
      })
      .catch(() => {});
  }, []);

  // Fetch roles when namespace changes (for namespaced bindings)
  useEffect(() => {
    if (!IS_BROWSER) return;
    const ns = form.value.namespace;
    if (!clusterScoped && ns) {
      apiGet<RoleItem[]>(`/v1/resources/roles/${ns}`)
        .then((resp) => {
          roles.value = Array.isArray(resp.data) ? resp.data : [];
        })
        .catch(() => {
          roles.value = [];
        });
    }
  }, [form.value.namespace]);

  // Fetch cluster roles
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<RoleItem[]>("/v1/resources/clusterroles")
      .then((resp) => {
        clusterRoles.value = Array.isArray(resp.data) ? resp.data : [];
      })
      .catch(() => {});
  }, []);

  // beforeunload guard
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty.value) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const validateStep = (step: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name =
          "Must be lowercase alphanumeric with hyphens, 1-63 characters";
      }
      if (!clusterScoped && !f.namespace) {
        errs.namespace = "Required";
      }
    }

    if (step === 1) {
      if (!f.roleRefName) {
        errs.roleRefName = "A role must be selected";
      }
    }

    if (step === 2) {
      const validSubjects = f.subjects.filter((s) => s.name);
      if (validSubjects.length === 0) {
        errs.subjects = "At least one subject is required";
      }
      f.subjects.forEach((s, i) => {
        if (!s.name) {
          errs[`subjects[${i}].name`] = "Name is required";
        }
        if (s.kind === "ServiceAccount" && !s.namespace) {
          errs[`subjects[${i}].namespace`] =
            "Namespace is required for ServiceAccount";
        }
      });
    }

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (!validateStep(currentStep.value)) return;

    if (currentStep.value === 2) {
      // Moving to Review — fetch preview
      currentStep.value = 3;
      await fetchPreview();
    } else {
      currentStep.value = currentStep.value + 1;
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
      namespace: clusterScoped ? undefined : f.namespace,
      clusterScope: clusterScoped,
      roleRef: {
        kind: f.roleRefKind,
        name: f.roleRefName,
      },
      subjects: f.subjects.filter((s) => s.name).map((s) => ({
        kind: s.kind,
        name: s.name,
        namespace: s.kind === "ServiceAccount" ? s.namespace : undefined,
      })),
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/rolebinding/preview",
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

  const fetchLocalUsers = async () => {
    try {
      const resp = await apiGet<LocalUser[]>("/v1/users");
      localUsers.value = Array.isArray(resp.data) ? resp.data : [];
    } catch {
      localUsers.value = [];
    }
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  const cancelHref = clusterScoped
    ? "/rbac/clusterrolebindings"
    : "/rbac/rolebindings";
  const detailBasePath = clusterScoped
    ? "/rbac/clusterrolebindings"
    : "/rbac/rolebindings";

  return (
    <div class="p-6">
      <div class="mb-6 flex items-center justify-between">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create {clusterScoped ? "ClusterRoleBinding" : "RoleBinding"}
        </h1>
        <a
          href={cancelHref}
          class="text-sm text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
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
        {/* Step 1: Basics */}
        {currentStep.value === 0 && (
          <div class="mx-auto max-w-lg space-y-4">
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Name <span class="text-red-500">*</span>
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                placeholder="e.g. my-binding"
              />
              {errors.value.name && (
                <p class="mt-1 text-xs text-red-500">{errors.value.name}</p>
              )}
            </div>

            {!clusterScoped && (
              <div>
                <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                  Namespace <span class="text-red-500">*</span>
                </label>
                <select
                  value={form.value.namespace}
                  onChange={(e) =>
                    updateField(
                      "namespace",
                      (e.target as HTMLSelectElement).value,
                    )}
                  class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                >
                  {namespaces.value.map((ns) => (
                    <option key={ns} value={ns}>{ns}</option>
                  ))}
                </select>
                {errors.value.namespace && (
                  <p class="mt-1 text-xs text-red-500">
                    {errors.value.namespace}
                  </p>
                )}
              </div>
            )}
          </div>
        )}

        {/* Step 2: Role Reference */}
        {currentStep.value === 1 && (
          <div class="mx-auto max-w-lg space-y-4">
            {!clusterScoped && (
              <div>
                <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                  Role Type
                </label>
                <select
                  value={form.value.roleRefKind}
                  onChange={(e) => {
                    updateField(
                      "roleRefKind",
                      (e.target as HTMLSelectElement).value,
                    );
                    updateField("roleRefName", "");
                  }}
                  class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                >
                  <option value="ClusterRole">ClusterRole</option>
                  <option value="Role">Role (namespace-scoped)</option>
                </select>
              </div>
            )}

            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                {form.value.roleRefKind === "Role" ? "Role" : "ClusterRole"}
                {" "}
                <span class="text-red-500">*</span>
              </label>
              <select
                value={form.value.roleRefName}
                onChange={(e) =>
                  updateField(
                    "roleRefName",
                    (e.target as HTMLSelectElement).value,
                  )}
                class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
              >
                <option value="">Select a role...</option>
                {(form.value.roleRefKind === "Role"
                  ? roles.value
                  : clusterRoles.value)
                  .map((r) => (
                    <option key={r.metadata.name} value={r.metadata.name}>
                      {r.metadata.name}
                    </option>
                  ))}
              </select>
              {errors.value.roleRefName && (
                <p class="mt-1 text-xs text-red-500">
                  {errors.value.roleRefName}
                </p>
              )}
            </div>
          </div>
        )}

        {/* Step 3: Subjects */}
        {currentStep.value === 2 && (
          <div class="space-y-4">
            {errors.value.subjects && (
              <p class="text-sm text-red-500">{errors.value.subjects}</p>
            )}

            <div class="rounded-lg border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-800">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-slate-200 dark:border-slate-700">
                    <th class="px-4 py-2 text-left text-xs font-medium uppercase tracking-wider text-slate-500">
                      Kind
                    </th>
                    <th class="px-4 py-2 text-left text-xs font-medium uppercase tracking-wider text-slate-500">
                      Name
                    </th>
                    <th class="px-4 py-2 text-left text-xs font-medium uppercase tracking-wider text-slate-500">
                      Namespace
                    </th>
                    <th class="px-4 py-2 w-20"></th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                  {form.value.subjects.map((subject, idx) => (
                    <tr key={idx}>
                      <td class="px-4 py-2">
                        <select
                          value={subject.kind}
                          onChange={(e) => {
                            const subs = [...form.value.subjects];
                            subs[idx] = {
                              ...subs[idx],
                              kind: (e.target as HTMLSelectElement)
                                .value as SubjectRow["kind"],
                            };
                            updateField("subjects", subs);
                          }}
                          class="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                        >
                          <option value="User">User</option>
                          <option value="Group">Group</option>
                          <option value="ServiceAccount">ServiceAccount</option>
                        </select>
                      </td>
                      <td class="px-4 py-2">
                        <div class="flex gap-1">
                          <input
                            type="text"
                            value={subject.name}
                            onInput={(e) => {
                              const subs = [...form.value.subjects];
                              subs[idx] = {
                                ...subs[idx],
                                name: (e.target as HTMLInputElement).value,
                              };
                              updateField("subjects", subs);
                            }}
                            class="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                            placeholder="Subject name"
                          />
                          {subject.kind === "User" && (
                            <button
                              type="button"
                              onClick={async () => {
                                if (localUsers.value.length === 0) {
                                  await fetchLocalUsers();
                                }
                                showUserPicker.value =
                                  showUserPicker.value === idx
                                    ? null
                                    : idx;
                              }}
                              class="shrink-0 rounded border border-slate-300 px-2 py-1 text-xs text-slate-600 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-400 dark:hover:bg-slate-700"
                              title="Select from local users"
                            >
                              Local
                            </button>
                          )}
                        </div>
                        {showUserPicker.value === idx &&
                          localUsers.value.length > 0 && (
                          <div class="mt-1 rounded border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-800">
                            {localUsers.value.map((u) => (
                              <button
                                key={u.id}
                                type="button"
                                onClick={() => {
                                  const subs = [...form.value.subjects];
                                  subs[idx] = {
                                    ...subs[idx],
                                    name: u.k8sUsername,
                                  };
                                  updateField("subjects", subs);
                                  showUserPicker.value = null;
                                }}
                                class="block w-full px-3 py-1 text-left text-sm hover:bg-slate-50 dark:hover:bg-slate-700"
                              >
                                {u.k8sUsername}{" "}
                                <span class="text-xs text-slate-400">
                                  ({u.username})
                                </span>
                              </button>
                            ))}
                          </div>
                        )}
                        {errors.value[`subjects[${idx}].name`] && (
                          <p class="mt-1 text-xs text-red-500">
                            {errors.value[`subjects[${idx}].name`]}
                          </p>
                        )}
                      </td>
                      <td class="px-4 py-2">
                        {subject.kind === "ServiceAccount"
                          ? (
                            <div>
                              <input
                                type="text"
                                value={subject.namespace}
                                onInput={(e) => {
                                  const subs = [...form.value.subjects];
                                  subs[idx] = {
                                    ...subs[idx],
                                    namespace:
                                      (e.target as HTMLInputElement).value,
                                  };
                                  updateField("subjects", subs);
                                }}
                                class="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                                placeholder="Namespace"
                              />
                              {errors.value[`subjects[${idx}].namespace`] && (
                                <p class="mt-1 text-xs text-red-500">
                                  {errors.value[`subjects[${idx}].namespace`]}
                                </p>
                              )}
                            </div>
                          )
                          : <span class="text-xs text-slate-400">N/A</span>}
                      </td>
                      <td class="px-4 py-2 text-right">
                        {form.value.subjects.length > 1 && (
                          <button
                            type="button"
                            onClick={() => {
                              const subs = form.value.subjects.filter(
                                (_, i) => i !== idx,
                              );
                              updateField("subjects", subs);
                            }}
                            class="rounded p-1 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
                            title="Remove subject"
                          >
                            <svg
                              class="h-4 w-4"
                              fill="none"
                              viewBox="0 0 24 24"
                              stroke="currentColor"
                              stroke-width="2"
                            >
                              <path
                                stroke-linecap="round"
                                stroke-linejoin="round"
                                d="M6 18L18 6M6 6l12 12"
                              />
                            </svg>
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <button
              type="button"
              onClick={() => {
                updateField("subjects", [
                  ...form.value.subjects,
                  { kind: "User", name: "", namespace: "" },
                ]);
              }}
              class="inline-flex items-center gap-1 rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-600 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-400 dark:hover:bg-slate-700"
            >
              <svg
                class="h-4 w-4"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                stroke-width="2"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M12 4v16m8-8H4"
                />
              </svg>
              Add Subject
            </button>
          </div>
        )}

        {/* Step 4: Review */}
        {currentStep.value === 3 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath={detailBasePath}
          />
        )}
      </div>

      {/* Navigation buttons */}
      {currentStep.value < 3 && (
        <div class="mt-8 flex justify-between">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === 2 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === 3 && !previewLoading.value &&
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
