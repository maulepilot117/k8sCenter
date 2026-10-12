import { Alert } from "@/components/ui/Alert.tsx";
import type { PanelState } from "@/lib/networking-state.ts";

/**
 * Shows a networking panel's failure state with the one recovery control it
 * allows: Try again, Reload, Reconnect for an ended live stream, or none for
 * a refusal (#608 KTD14).
 */
export function PanelStateNotice({
  state,
  onRetry,
  onReconnect,
}: {
  state: PanelState;
  /** Called by "Try again". Omit to hide the control. */
  onRetry?: () => void;
  /** Called by "Reconnect". Omit to hide the control. */
  onReconnect?: () => void;
}) {
  return (
    <div role="status" class="flex flex-col gap-2">
      <Alert variant={state.kind === "forbidden" ? "info" : "warning"}>
        {state.message}
      </Alert>
      {state.action === "retry" && onRetry && (
        <button
          type="button"
          class="self-start text-sm text-accent hover:underline"
          onClick={onRetry}
        >
          Try again
        </button>
      )}
      {state.action === "reconnect" && onReconnect && (
        <button
          type="button"
          class="self-start text-sm text-accent hover:underline"
          onClick={onReconnect}
        >
          Reconnect
        </button>
      )}
      {state.action === "reload" && (
        <button
          type="button"
          class="self-start text-sm text-accent hover:underline"
          onClick={() => globalThis.location.reload()}
        >
          Reload
        </button>
      )}
    </div>
  );
}
