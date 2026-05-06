/**
 * Shared primitives for islands that wrap the `/api/v1/yaml/{validate,apply}`
 * endpoints. Two consumers as of Phase K:
 *   - `islands/YamlApplyPage.tsx` (general-purpose paste-or-upload editor)
 *   - `islands/SecretStoreFromTemplateEditor.tsx` (template-driven editor)
 *
 * Each consumer keeps its own visual treatment (the general editor shows a
 * dense multi-doc table; the template editor shows a focused single-resource
 * list); this module owns only the types + state machine common to both.
 */

import { type Signal, useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { apiPostRaw } from "./api.ts";

export interface ApplyResult {
  index: number;
  kind: string;
  name: string;
  namespace?: string;
  /** "created" | "configured" | "unchanged" | "failed" */
  action: string;
  error?: string;
}

export interface ApplyResponse {
  results: ApplyResult[];
  summary: {
    total: number;
    created: number;
    configured: number;
    unchanged: number;
    failed: number;
  };
}

export interface UseYamlApplyOptions {
  /**
   * When set, an apply call appends `?force=true` to the apply URL. The
   * upstream `/yaml/apply` route honors this flag for SSA conflict resolution.
   */
  forceConflicts?: Signal<boolean>;
  /**
   * Called after a successful apply with the parsed response. Lets the caller
   * trigger side effects (e.g., navigate to the resulting resource's detail
   * page) without re-implementing the apply state machine.
   */
  onApplySuccess?: (res: ApplyResponse) => void;
}

export interface UseYamlApplyReturn {
  yamlContent: Signal<string>;
  applying: Signal<boolean>;
  validating: Signal<boolean>;
  error: Signal<string | null>;
  result: Signal<ApplyResponse | null>;
  handleValidate: () => Promise<void>;
  handleApply: () => Promise<void>;
}

/**
 * Hook that owns the full validate/apply state machine for a single YAML
 * editor instance. Caller passes the initial YAML; this hook returns signals
 * for editor binding, plus stable handlers wired to the api module.
 */
export function useYamlApply(
  initialYaml: string,
  options: UseYamlApplyOptions = {},
): UseYamlApplyReturn {
  const yamlContent = useSignal(initialYaml);
  const applying = useSignal(false);
  const validating = useSignal(false);
  const error = useSignal<string | null>(null);
  const result = useSignal<ApplyResponse | null>(null);

  const handleValidate = useCallback(async () => {
    if (applying.value || validating.value) return;
    validating.value = true;
    error.value = null;
    result.value = null;
    try {
      const res = await apiPostRaw<ApplyResponse>(
        "/v1/yaml/validate",
        yamlContent.value,
      );
      result.value = res.data;
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Validation failed";
    } finally {
      validating.value = false;
    }
  }, []);

  const handleApply = useCallback(async () => {
    if (applying.value || validating.value) return;
    applying.value = true;
    error.value = null;
    result.value = null;
    try {
      const queryStr = options.forceConflicts?.value ? "?force=true" : "";
      const res = await apiPostRaw<ApplyResponse>(
        `/v1/yaml/apply${queryStr}`,
        yamlContent.value,
      );
      result.value = res.data;
      options.onApplySuccess?.(res.data);
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Apply failed";
    } finally {
      applying.value = false;
    }
  }, []);

  return {
    yamlContent,
    applying,
    validating,
    error,
    result,
    handleValidate,
    handleApply,
  };
}
