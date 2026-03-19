/**
 * Resource action handlers — maps action IDs to API calls.
 * Used by ResourceTable's kebab menu.
 */
import { apiDelete, apiPost } from "@/lib/api.ts";
import type { K8sResource } from "@/lib/k8s-types.ts";

/** Valid action identifiers. */
export type ActionId = "scale" | "restart" | "delete" | "suspend" | "trigger";

/** Actions available per resource kind. */
export const ACTIONS_BY_KIND: Record<string, ActionId[]> = {
  deployments: ["scale", "restart", "delete"],
  statefulsets: ["scale", "restart", "delete"],
  daemonsets: ["restart", "delete"],
  pods: ["delete"],
  jobs: ["suspend", "delete"],
  cronjobs: ["suspend", "trigger", "delete"],
};

/** Action metadata for UI rendering. */
export interface ActionMeta {
  label: string;
  danger?: boolean;
  /** "confirm" = simple OK/Cancel, "destructive" = type name to confirm */
  confirm?: "confirm" | "destructive";
  confirmMessage?: string;
}

/** Get display metadata for an action, considering the resource's current state. */
export function getActionMeta(
  actionId: ActionId,
  resource: K8sResource,
): ActionMeta {
  switch (actionId) {
    case "scale":
      return { label: "Scale" };
    case "restart":
      return {
        label: "Restart",
        confirm: "confirm",
        confirmMessage:
          "This will perform a rolling restart, cycling all pods.",
      };
    case "delete": {
      const owners = resource.metadata.ownerReferences;
      const owner = owners && owners.length > 0 ? owners[0] : null;
      const kind = (resource as { kind?: string }).kind ?? "resource";
      const msg = owner
        ? `This ${kind} is managed by ${owner.kind}/${owner.name} and will be recreated after deletion.`
        : `This will permanently delete "${resource.metadata.name}".`;
      return {
        label: "Delete",
        danger: true,
        confirm: "destructive",
        confirmMessage: msg,
      };
    }
    case "suspend": {
      const spec = resource.spec as { suspend?: boolean } | undefined;
      const suspended = spec?.suspend === true;
      return {
        label: suspended ? "Resume" : "Suspend",
        confirm: "confirm",
        confirmMessage: suspended
          ? "Resume scheduling/execution?"
          : "Suspend scheduling/execution?",
      };
    }
    case "trigger":
      return {
        label: "Trigger Job",
        confirm: "confirm",
        confirmMessage: "Create a new Job from this CronJob's template?",
      };
    default:
      return { label: actionId };
  }
}

/** Execute a resource action. Returns a result message on success, throws on error. */
export async function executeAction(
  actionId: ActionId,
  kind: string,
  namespace: string,
  name: string,
  params?: Record<string, unknown>,
): Promise<string> {
  const path = `/v1/resources/${kind}/${namespace}/${name}`;

  switch (actionId) {
    case "scale": {
      const replicas = params?.replicas;
      if (typeof replicas !== "number" || replicas < 0) {
        throw new Error("replicas must be a non-negative number");
      }
      await apiPost(`${path}/scale`, { replicas });
      return `Scaled to ${replicas} replicas`;
    }
    case "restart":
      await apiPost(`${path}/restart`);
      return "Rolling restart initiated";
    case "delete":
      await apiDelete(path);
      return `Deleted ${name}`;
    case "suspend": {
      const suspend = params?.suspend;
      if (typeof suspend !== "boolean") {
        throw new Error("suspend must be a boolean");
      }
      await apiPost(`${path}/suspend`, { suspend });
      return suspend ? "Suspended" : "Resumed";
    }
    case "trigger": {
      const res = await apiPost<{ metadata: { name: string } }>(
        `${path}/trigger`,
      );
      const jobName = res?.data?.metadata?.name ?? "unknown";
      return `Job "${jobName}" created`;
    }
    default:
      throw new Error(`Unknown action: ${actionId}`);
  }
}
