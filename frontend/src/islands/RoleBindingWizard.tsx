/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import type { LocalUser } from "@/lib/user-types.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import {
  DEFAULT_NAMESPACE,
  useAdoptSelectedNamespace,
} from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";

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
  onClose?: () => void;
}

const MAX_SUBJECTS = 50;

const STEPS: WizardStep[] = [
  { label: "Basics", sub: "Name & namespace" },
  { label: "Role", sub: "Role reference" },
  { label: "Subjects", sub: "Users, groups & service accounts" },
  { label: "Review", sub: "Preview & apply" },
];

function initialState(clusterScoped: boolean): BindingFormState {
  return {
    name: "",
    namespace: clusterScoped ? "" : DEFAULT_NAMESPACE,
    roleRefKind: "ClusterRole",
    roleRefName: "",
    subjects: [{ kind: "User", name: "", namespace: "" }],
  };
}

export default function RoleBindingWizard({
  clusterScoped,
  onClose,
}: RoleBindingWizardProps) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<BindingFormState>(() =>
    initialState(clusterScoped),
  );
  useAdoptSelectedNamespace(
    (namespace) => setForm((prev) => ({ ...prev, namespace })),
    !clusterScoped,
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const formRef = useLatest(form);
  const isClient = useIsClient();

  // Data for dropdowns
  const [namespaceList] = useNamespaces();
  const [roles, setRoles] = useState<RoleItem[]>([]);
  const [clusterRoles, setClusterRoles] = useState<RoleItem[]>([]);
  const [localUsers, setLocalUsers] = useState<LocalUser[]>([]);
  const localUsersFetched = useRef(false);
  const [showUserPicker, setShowUserPicker] = useState<number | null>(null);
  const subjectKeys = useRowKeys(form.subjects.length);

  // Review step state
  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  // Fetch roles when namespace changes (for namespaced bindings)
  const formNamespace = form.namespace;
  useEffect(() => {
    if (!isClient) return;
    if (!clusterScoped && formNamespace) {
      let cancelled = false;
      apiGet<RoleItem[]>(`/v1/resources/roles/${formNamespace}?limit=500`)
        .then((resp) => {
          if (!cancelled) {
            setRoles(Array.isArray(resp.data) ? resp.data : []);
          }
        })
        .catch(() => {
          if (!cancelled) setRoles([]);
        });
      return () => {
        cancelled = true;
      };
    }
  }, [isClient, clusterScoped, formNamespace]);

  // Fetch cluster roles
  useEffect(() => {
    if (!isClient) return;
    apiGet<RoleItem[]>("/v1/resources/clusterroles?limit=500")
      .then((resp) => {
        setClusterRoles(Array.isArray(resp.data) ? resp.data : []);
      })
      .catch(() => {});
  }, [isClient]);

  useDirtyGuard(dirty);

  const patchForm = useCallback((patch: Partial<BindingFormState>) => {
    setDirty(true);
    setForm((prev) => ({ ...prev, ...patch }));
  }, []);

  const updateField = useCallback(
    (field: string, value: unknown) => {
      patchForm({ [field]: value });
    },
    [patchForm],
  );

  const updateSubject = (idx: number, patch: Partial<SubjectRow>) => {
    setDirty(true);
    setForm((prev) => {
      const subjects = [...prev.subjects];
      subjects[idx] = { ...subjects[idx], ...patch };
      return { ...prev, subjects };
    });
  };

  const addSubject = () => {
    setDirty(true);
    setForm((prev) =>
      prev.subjects.length >= MAX_SUBJECTS
        ? prev
        : {
            ...prev,
            subjects: [
              ...prev.subjects,
              { kind: "User", name: "", namespace: "" },
            ],
          },
    );
  };

  const removeSubject = (idx: number) => {
    setDirty(true);
    setShowUserPicker(null);
    setForm((prev) => ({
      ...prev,
      subjects: prev.subjects.filter((_, i) => i !== idx),
    }));
  };

  const validateStep = (step: number): boolean => {
    const f = formRef.current;
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

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (!validateStep(currentStep)) return;

    if (currentStep === 2) {
      // Moving to Review — fetch preview
      setCurrentStep(3);
      await fetchPreview();
    } else {
      setCurrentStep((prev) => prev + 1);
    }
  };

  const goBack = () => {
    if (currentStep > 0) {
      setCurrentStep((prev) => prev - 1);
    }
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const f = formRef.current;
    const payload = {
      name: f.name,
      namespace: clusterScoped ? undefined : f.namespace,
      clusterScope: clusterScoped,
      roleRef: {
        kind: f.roleRefKind,
        name: f.roleRefName,
      },
      subjects: f.subjects
        .filter((s) => s.name)
        .map((s) => ({
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
      if (gen !== previewGen.current) return;
      setPreviewYaml(resp.data.yaml);
    } catch (err) {
      if (gen !== previewGen.current) return;
      setPreviewError(
        err instanceof Error ? err.message : "Failed to generate preview",
      );
    } finally {
      if (gen === previewGen.current) setPreviewLoading(false);
    }
  };

  const fetchLocalUsers = async () => {
    if (localUsersFetched.current) return;
    localUsersFetched.current = true;
    try {
      const resp = await apiGet<LocalUser[]>("/v1/users");
      setLocalUsers(Array.isArray(resp.data) ? resp.data : []);
    } catch {
      setLocalUsers([]);
    }
  };

  const basePath = clusterScoped
    ? "/rbac/clusterrolebindings"
    : "/rbac/rolebindings";

  return (
    <WizardShell
      title={`Create ${clusterScoped ? "ClusterRoleBinding" : "RoleBinding"}`}
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={goBack}
      onNext={async () => {
        if (currentStep === 3) {
          close();
        } else {
          await goNext();
        }
      }}
      nextLabel={currentStep === 3 ? "Close" : "Continue"}
      yaml={previewYaml || undefined}
    >
      {/* Step 1: Basics */}
      {currentStep === 0 && (
        <div className="mx-auto max-w-lg space-y-4">
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Name <span className="text-error">*</span>
            </label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => updateField("name", e.currentTarget.value)}
              className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
              placeholder="e.g. my-binding"
            />
            {errors.name && (
              <p className="mt-1 text-xs text-error">{errors.name}</p>
            )}
          </div>

          {!clusterScoped && (
            <div>
              <label className="block text-sm font-medium text-text-secondary">
                Namespace <span className="text-error">*</span>
              </label>
              <select
                value={form.namespace}
                onChange={(e) =>
                  updateField("namespace", e.currentTarget.value)
                }
                className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
              >
                {namespaceList.map((ns) => (
                  <option key={ns} value={ns}>
                    {ns}
                  </option>
                ))}
              </select>
              {errors.namespace && (
                <p className="mt-1 text-xs text-error">{errors.namespace}</p>
              )}
            </div>
          )}
        </div>
      )}

      {/* Step 2: Role Reference */}
      {currentStep === 1 && (
        <div className="mx-auto max-w-lg space-y-4">
          {!clusterScoped && (
            <div>
              <label className="block text-sm font-medium text-text-secondary">
                Role Type
              </label>
              <select
                value={form.roleRefKind}
                onChange={(e) => {
                  patchForm({
                    roleRefKind: e.currentTarget
                      .value as BindingFormState["roleRefKind"],
                    roleRefName: "",
                  });
                }}
                className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
              >
                <option value="ClusterRole">ClusterRole</option>
                <option value="Role">Role (namespace-scoped)</option>
              </select>
            </div>
          )}

          <div>
            <label className="block text-sm font-medium text-text-secondary">
              {form.roleRefKind === "Role" ? "Role" : "ClusterRole"}
              {""}
              <span className="text-error">*</span>
            </label>
            <select
              value={form.roleRefName}
              onChange={(e) =>
                updateField("roleRefName", e.currentTarget.value)
              }
              className="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
            >
              <option value="">Select a role...</option>
              {(form.roleRefKind === "Role" ? roles : clusterRoles).map((r) => (
                <option key={r.metadata.name} value={r.metadata.name}>
                  {r.metadata.name}
                </option>
              ))}
            </select>
            {errors.roleRefName && (
              <p className="mt-1 text-xs text-error">{errors.roleRefName}</p>
            )}
          </div>
        </div>
      )}

      {/* Step 3: Subjects */}
      {currentStep === 2 && (
        <div className="space-y-4">
          {errors.subjects && (
            <p className="text-sm text-error">{errors.subjects}</p>
          )}

          <div className="rounded-lg border border-border-primary bg-surface">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary">
                  <th className="px-4 py-2 text-left text-xs font-medium uppercase tracking-wider text-text-muted">
                    Kind
                  </th>
                  <th className="px-4 py-2 text-left text-xs font-medium uppercase tracking-wider text-text-muted">
                    Name
                  </th>
                  <th className="px-4 py-2 text-left text-xs font-medium uppercase tracking-wider text-text-muted">
                    Namespace
                  </th>
                  <th className="px-4 py-2 w-20"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {form.subjects.map((subject, idx) => (
                  <tr key={subjectKeys.keyAt(idx)}>
                    <td className="px-4 py-2">
                      <select
                        value={subject.kind}
                        onChange={(e) =>
                          updateSubject(idx, {
                            kind: e.currentTarget.value as SubjectRow["kind"],
                          })
                        }
                        className="w-full rounded border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
                      >
                        <option value="User">User</option>
                        <option value="Group">Group</option>
                        <option value="ServiceAccount">ServiceAccount</option>
                      </select>
                    </td>
                    <td className="px-4 py-2">
                      <div className="flex gap-1">
                        <input
                          type="text"
                          value={subject.name}
                          onChange={(e) =>
                            updateSubject(idx, {
                              name: e.currentTarget.value,
                            })
                          }
                          className="w-full rounded border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
                          placeholder="Subject name"
                        />
                        {subject.kind === "User" && (
                          <button
                            type="button"
                            onClick={async () => {
                              await fetchLocalUsers();
                              setShowUserPicker((prev) =>
                                prev === idx ? null : idx,
                              );
                            }}
                            className="shrink-0 rounded border border-border-primary px-2 py-1 text-xs text-text-secondary hover:bg-surface text-text-muted"
                            title="Select from local users"
                          >
                            Local
                          </button>
                        )}
                      </div>
                      {showUserPicker === idx && localUsers.length > 0 && (
                        <div className="mt-1 rounded border border-border-primary bg-surface shadow-sm">
                          {localUsers.map((u) => (
                            <button
                              key={u.id}
                              type="button"
                              onClick={() => {
                                updateSubject(idx, {
                                  name: u.k8sUsername,
                                });
                                setShowUserPicker(null);
                              }}
                              className="block w-full px-3 py-1 text-left text-sm hover:bg-hover"
                            >
                              {u.k8sUsername}
                              {""}
                              <span className="text-xs text-text-muted">
                                ({u.username})
                              </span>
                            </button>
                          ))}
                        </div>
                      )}
                      {errors[`subjects[${idx}].name`] && (
                        <p className="mt-1 text-xs text-error">
                          {errors[`subjects[${idx}].name`]}
                        </p>
                      )}
                    </td>
                    <td className="px-4 py-2">
                      {subject.kind === "ServiceAccount" ? (
                        <div>
                          <input
                            type="text"
                            value={subject.namespace}
                            onChange={(e) =>
                              updateSubject(idx, {
                                namespace: e.currentTarget.value,
                              })
                            }
                            className="w-full rounded border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
                            placeholder="Namespace"
                          />
                          {errors[`subjects[${idx}].namespace`] && (
                            <p className="mt-1 text-xs text-error">
                              {errors[`subjects[${idx}].namespace`]}
                            </p>
                          )}
                        </div>
                      ) : (
                        <span className="text-xs text-text-muted">N/A</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right">
                      {form.subjects.length > 1 && (
                        <button
                          type="button"
                          onClick={() => {
                            subjectKeys.removeAt(idx);
                            removeSubject(idx);
                          }}
                          className="rounded p-1 text-error hover:bg-danger-dim"
                          title="Remove subject"
                        >
                          <svg
                            className="h-4 w-4"
                            fill="none"
                            viewBox="0 0 24 24"
                            stroke="currentColor"
                            strokeWidth="2"
                          >
                            <path
                              strokeLinecap="round"
                              strokeLinejoin="round"
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
            disabled={form.subjects.length >= MAX_SUBJECTS}
            onClick={() => {
              addSubject();
            }}
            className="inline-flex items-center gap-1 rounded-md border border-border-primary px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-surface disabled:cursor-not-allowed disabled:opacity-40 text-text-muted"
          >
            <svg
              className="h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth="2"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M12 4v16m8-8H4"
              />
            </svg>
            Add Subject
          </button>
        </div>
      )}

      {/* Step 4: Review */}
      {currentStep === 3 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath={basePath}
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
