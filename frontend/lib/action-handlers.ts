/**
 * Resource action handlers — maps action IDs to API calls.
 * Used by ResourceTable's kebab menu.
 */
import { apiDelete, apiPost } from "@/lib/api.ts";

/** Actions available per resource kind. */
export const ACTIONS_BY_KIND: Record<string, string[]> = {
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
  actionId: string,
  // deno-lint-ignore no-explicit-any
  resource: any,
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
      const owners = resource?.metadata?.ownerReferences;
      const owner = owners?.length > 0 ? owners[0] : null;
      const msg = owner
        ? `This ${
          resource.kind ?? "resource"
        } is managed by ${owner.kind}/${owner.name} and will be recreated after deletion.`
        : `This will permanently delete "${resource?.metadata?.name}".`;
      return {
        label: "Delete",
        danger: true,
        confirm: "destructive",
        confirmMessage: msg,
      };
    }
    case "suspend": {
      const suspended = resource?.spec?.suspend === true;
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
  actionId: string,
  kind: string,
  namespace: string,
  name: string,
  params?: Record<string, unknown>,
): Promise<string> {
  const path = `/v1/resources/${kind}/${namespace}/${name}`;

  switch (actionId) {
    case "scale": {
      const replicas = params?.replicas as number;
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
      const suspend = params?.suspend as boolean;
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
