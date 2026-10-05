import {
  MAX_OWNERSHIP_REFS,
  type OwnershipState,
} from "@/lib/change-tracking.ts";
import { OwnershipList } from "./OwnershipList.tsx";

/**
 * The per-object GitOps ownership of the previewed objects. A failed or
 * partial check says what is unknown; it never reads as "not managed".
 */
export function OwnershipSection({ state }: { state: OwnershipState }) {
  if (state.status === "idle") return null;
  return (
    <section aria-labelledby="ownership-heading" class="flex flex-col gap-2">
      <h2
        id="ownership-heading"
        class="m-0 text-sm font-semibold text-text-primary"
      >
        GitOps ownership
      </h2>
      {state.status === "loading" && (
        <p role="status" class="m-0 text-sm text-text-muted">
          Checking which GitOps controllers manage these objects…
        </p>
      )}
      {state.status === "error" && (
        <p role="status" class="m-0 text-sm text-warning">
          {state.message}
        </p>
      )}
      {state.status === "ready" && (
        <>
          <OwnershipList results={state.results} />
          {state.omitted > 0 && (
            <p class="m-0 text-xs text-text-muted">
              Ownership was checked for the first {MAX_OWNERSHIP_REFS} objects
              only; {state.omitted} more {state.omitted === 1 ? "was" : "were"}{" "}
              not checked.
            </p>
          )}
        </>
      )}
    </section>
  );
}
