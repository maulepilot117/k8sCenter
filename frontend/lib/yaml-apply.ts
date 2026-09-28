/**
 * Shared primitives for islands that wrap the `/api/v1/yaml/{validate,apply}`
 * endpoints. Two consumers as of Phase K:
 *   - `islands/YamlApplyPage.tsx` (general-purpose paste-or-upload editor)
 *   - `islands/SecretStoreFromTemplateEditor.tsx` (template-driven editor)
 *
 * Each consumer keeps its own visual treatment (the general editor shows a
 * dense multi-doc table; the template editor shows a focused single-resource
 * list); this module owns only the types + state machine common to both.
 *
 * **Target pinning (D4 / AE2).** A successful preview records the cluster it
 * ran against. With `pinApplyToPreview`, the apply is addressed to that
 * cluster — header and `targetCluster`/`targetGeneration` query both — and
 * never to whatever the operator is looking at by the time they press Apply.
 * The server refuses a pin that disagrees with its target, so a mismatch
 * cannot apply anywhere. Without the option the hook keeps its original
 * unpinned apply, which is what the template editor relies on.
 */

import {
  type ReadonlySignal,
  type Signal,
  useComputed,
  useSignal,
  useSignalEffect,
} from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { ApiError, apiPostRaw, errorExtra } from "./api.ts";
import {
  type ClusterTarget,
  clusterEpoch,
  currentTarget,
  LOCAL_CLUSTER_ID,
  selectedCluster,
  selectedClusterGeneration,
  UNKNOWN_GENERATION,
} from "./cluster.ts";

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

/** One document's dry-run verdict from `/yaml/validate`. */
export interface ValidateDocument {
  index: number;
  kind: string;
  name: string;
  namespace?: string;
  valid: boolean;
  errors?: Array<{ field?: string; message: string }>;
}

/** Body of `/yaml/validate`: the verdicts plus the target they ran against. */
export interface ValidateResponse {
  documents: ValidateDocument[];
  valid: boolean;
  targetCluster: string;
  targetGeneration: string;
}

/** The target a preview ran against, which a pinned apply is held to. */
export interface YamlApplyPin {
  /** The operator's selection when the preview was issued. */
  readonly target: ClusterTarget;
  /** The server's identity for that target, echoed back on apply. */
  readonly targetCluster: string;
  readonly targetGeneration: string;
  /** Epoch milliseconds at which the preview succeeded. */
  readonly pinnedAt: number;
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
  /**
   * Require a successful preview before applying, and address the apply to
   * the previewed cluster rather than the current selection. Off by default
   * so existing consumers keep their unpinned apply.
   */
  pinApplyToPreview?: boolean;
}

export interface UseYamlApplyReturn {
  yamlContent: Signal<string>;
  applying: Signal<boolean>;
  validating: Signal<boolean>;
  error: Signal<string | null>;
  /** The last apply's outcome. Validate results go to `preview`. */
  result: Signal<ApplyResponse | null>;
  handleValidate: () => Promise<void>;
  handleApply: () => Promise<void>;
  /** The last successful preview's verdicts. */
  preview: ReadonlySignal<ValidateResponse | null>;
  /** The target the last successful preview ran against. */
  pin: ReadonlySignal<YamlApplyPin | null>;
  /**
   * The operator has since selected a different cluster than the pin. The
   * pin is unchanged: a pinned apply still goes to the previewed cluster.
   */
  pinStale: ReadonlySignal<boolean>;
  /** Forgets the preview and its pin; a pinned apply then needs a new one. */
  clearPin: () => void;
}

function normalizeClusterId(id: string | undefined): string {
  return id || LOCAL_CLUSTER_ID;
}

/**
 * Operator-facing text for an apply the server refused because its pin no
 * longer matches. Returns null for any other failure.
 */
function pinRefusalMessage(err: unknown): string | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null;
  if (err.reason === "cluster_pin_mismatch") {
    const pinned = errorExtra(err, "pinnedClusterId") ?? "unknown";
    const requested = errorExtra(err, "requestClusterId") ?? "unknown";
    return `Apply refused: the preview ran against cluster "${pinned}", but this request was sent to "${requested}". Nothing was applied. Validate again to apply.`;
  }
  if (err.reason === "cluster_generation_mismatch") {
    return "Apply refused: the cluster you previewed has been re-registered since the preview. Nothing was applied. Validate again to apply.";
  }
  return null;
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
  const preview = useSignal<ValidateResponse | null>(null);
  const pin = useSignal<YamlApplyPin | null>(null);
  const validateAbort = useRef<{
    controller: AbortController;
    issuedEpoch: number;
  } | null>(null);

  // A pin taken while the selection was still restored under the unknown
  // sentinel cannot be stale on generation alone: the id is unchanged, and a
  // re-registration always mints a new id, so learning the real generation
  // later is not a move. (The server's targetGeneration is not compared here:
  // it and the cluster list's createdAt format the same instant differently
  // unless the backend runs in UTC. The apply is enforced on it server-side.)
  const pinStale = useComputed(() => {
    const p = pin.value;
    if (!p) return false;
    if (p.target.clusterId !== selectedCluster.value) return true;
    return (
      p.target.generation !== UNKNOWN_GENERATION &&
      p.target.generation !== selectedClusterGeneration.value
    );
  });

  // A switch makes an in-flight preview describe a cluster that is no longer
  // on screen: cancel it rather than let it land late. An in-flight apply is
  // deliberately left alone — aborting the fetch cannot undo a server-side
  // apply, only hide its outcome, and that outcome is labelled with its pin.
  useSignalEffect(() => {
    const epoch = clusterEpoch.value;
    const ctl = validateAbort.current;
    if (ctl && ctl.issuedEpoch !== epoch) ctl.controller.abort();
  });
  useEffect(() => () => validateAbort.current?.controller.abort(), []);

  const clearPin = useCallback(() => {
    pin.value = null;
    preview.value = null;
  }, []);

  const handleValidate = useCallback(async () => {
    if (applying.value || validating.value) return;
    const target = currentTarget();
    const controller = new AbortController();
    const inFlight = { controller, issuedEpoch: target.epoch };
    validateAbort.current = inFlight;
    validating.value = true;
    error.value = null;
    result.value = null;
    preview.value = null;
    pin.value = null;
    try {
      const res = await apiPostRaw<ValidateResponse>(
        "/v1/yaml/validate",
        yamlContent.value,
        "text/yaml",
        { clusterId: target.clusterId, signal: controller.signal },
      );
      // The operator switched while this was in flight. Whatever the answer
      // says, it describes a cluster that is no longer on screen.
      if (clusterEpoch.peek() !== target.epoch) return;
      const data = res.data;
      if (
        normalizeClusterId(data.targetCluster) !==
        normalizeClusterId(target.clusterId)
      ) {
        error.value = `Validation ran against cluster "${data.targetCluster}" instead of "${target.clusterId}". Validate again.`;
        return;
      }
      preview.value = data;
      pin.value = {
        target,
        targetCluster: data.targetCluster,
        targetGeneration: data.targetGeneration,
        pinnedAt: Date.now(),
      };
    } catch (err) {
      if (controller.signal.aborted || clusterEpoch.peek() !== target.epoch) {
        return;
      }
      error.value = err instanceof Error ? err.message : "Validation failed";
    } finally {
      if (validateAbort.current === inFlight) validateAbort.current = null;
      validating.value = false;
    }
  }, []);

  const handleApply = useCallback(async () => {
    if (applying.value || validating.value) return;
    const pinned = pin.peek();
    if (options.pinApplyToPreview && !pinned) {
      error.value =
        "Validate first, so the apply is tied to a reviewed cluster.";
      return;
    }
    applying.value = true;
    error.value = null;
    result.value = null;
    try {
      const query = new URLSearchParams();
      if (options.forceConflicts?.value) query.set("force", "true");
      if (options.pinApplyToPreview && pinned) {
        query.set("targetCluster", pinned.targetCluster);
        query.set("targetGeneration", pinned.targetGeneration);
      }
      const queryStr = query.size > 0 ? `?${query}` : "";
      const res = await apiPostRaw<ApplyResponse>(
        `/v1/yaml/apply${queryStr}`,
        yamlContent.value,
        "text/yaml",
        options.pinApplyToPreview && pinned
          ? { clusterId: pinned.target.clusterId }
          : undefined,
      );
      result.value = res.data;
      options.onApplySuccess?.(res.data);
    } catch (err) {
      const refusal = pinRefusalMessage(err);
      if (refusal) {
        // A refused pin can never succeed on retry. Drop it so the only way
        // forward is a fresh preview against the cluster actually targeted.
        pin.value = null;
        preview.value = null;
        error.value = refusal;
      } else {
        error.value = err instanceof Error ? err.message : "Apply failed";
      }
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
    preview,
    pin,
    pinStale,
    clearPin,
  };
}
