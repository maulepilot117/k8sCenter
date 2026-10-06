import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { Alert } from "@/components/ui/Alert.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError } from "@/lib/api.ts";
import { addGrant, listGrants, removeGrant } from "@/lib/incident-api.ts";
import type { GrantView } from "@/lib/incident-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { simpleErrorText } from "./errors.ts";
import { BUTTON_SECONDARY, FOCUS_RING, HEADING, PANEL } from "./ui.tsx";

/**
 * The owner's sharing panel: who holds a grant, whether each may add notes,
 * and adding or removing one. Granting to oneself is the server's 204 no-op
 * and is reported as such.
 */
export function GrantsPanel({ incidentId }: { incidentId: string }) {
  const grants = useSignal<GrantView[] | null>(null);
  const loadError = useSignal<string | null>(null);
  const granteeId = useSignal("");
  const canAnnotate = useSignal(false);
  const working = useSignal(false);
  const message = useSignal<string | null>(null);
  const error = useSignal<string | null>(null);
  const reloadSeq = useSignal(0);
  /**
   * Counts grant changes made here. A list read that was issued before a
   * change may not include it, so its answer is discarded and the list is
   * read again rather than overwriting the change on screen.
   */
  const changes = useRef(0);

  const seq = reloadSeq.value;
  useEffect(() => {
    const controller = new AbortController();
    const issuedAt = changes.current;
    loadError.value = null;
    listGrants(incidentId, controller.signal)
      .then((g) => {
        if (controller.signal.aborted) return;
        if (issuedAt !== changes.current) {
          reloadSeq.value = reloadSeq.peek() + 1;
          return;
        }
        grants.value = g;
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        loadError.value = simpleErrorText(
          err,
          "Could not load who this incident is shared with.",
        );
      });
    return () => controller.abort();
  }, [incidentId, seq]);

  const run = async (action: () => Promise<string | null>) => {
    if (working.value) return;
    working.value = true;
    error.value = null;
    message.value = null;
    try {
      message.value = await action();
      changes.current++;
    } catch (err) {
      error.value = simpleErrorText(err, "The change could not be saved.");
      if (err instanceof ApiError && err.status === 404) {
        reloadSeq.value = reloadSeq.peek() + 1;
      }
    } finally {
      working.value = false;
    }
  };

  const upsert = (g: GrantView) => {
    const rest = (grants.value ?? []).filter(
      (x) => x.granteeId !== g.granteeId,
    );
    grants.value = [...rest, g];
  };

  const share = (e: Event) => {
    e.preventDefault();
    const who = granteeId.value.trim();
    if (!who) {
      error.value = "Enter the user id to share with.";
      return;
    }
    void run(async () => {
      const g = await addGrant(incidentId, {
        granteeId: who,
        canAnnotate: canAnnotate.value,
      });
      granteeId.value = "";
      if (!g) {
        return "That is you. As the owner you already hold every right, so nothing changed.";
      }
      upsert(g);
      return `Shared with ${g.granteeId}.`;
    });
  };

  const list = grants.value;
  return (
    <section aria-labelledby="incident-grants-heading" class={PANEL}>
      <h2 id="incident-grants-heading" class={HEADING}>
        Sharing
      </h2>
      <p class="m-0 text-xs text-text-muted">
        People you share with see the evidence their own access allows; the rest
        appears to them as withheld.
      </p>
      {loadError.value && (
        <div role="alert">
          <Alert variant="error">{loadError.value}</Alert>
        </div>
      )}
      {list && list.length === 0 && (
        <p class="m-0 text-sm text-text-secondary">Not shared with anyone.</p>
      )}
      {list && list.length > 0 && (
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-left text-sm">
            <caption class="sr-only">
              People this incident is shared with
            </caption>
            <thead>
              <tr class="text-xs uppercase tracking-wide text-text-muted">
                <th scope="col" class="px-2 py-1 font-semibold">
                  User
                </th>
                <th scope="col" class="px-2 py-1 font-semibold">
                  Can add notes
                </th>
                <th scope="col" class="px-2 py-1 font-semibold">
                  Since
                </th>
                <th scope="col" class="px-2 py-1 font-semibold">
                  <span class="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((g) => (
                <tr key={g.granteeId} class="border-t border-border-subtle">
                  <td class="px-2 py-1 text-text-primary">{g.granteeId}</td>
                  <td class="px-2 py-1">
                    <input
                      type="checkbox"
                      aria-label={`${g.granteeId} can add notes`}
                      checked={g.canAnnotate}
                      aria-disabled={working.value}
                      onChange={(ev) => {
                        const want = ev.currentTarget.checked;
                        ev.currentTarget.checked = g.canAnnotate;
                        void run(async () => {
                          const updated = await addGrant(incidentId, {
                            granteeId: g.granteeId,
                            canAnnotate: want,
                          });
                          if (updated) upsert(updated);
                          return null;
                        });
                      }}
                      class={FOCUS_RING}
                    />
                  </td>
                  <td class="px-2 py-1 text-text-secondary">
                    {timeAgo(g.createdAt)}
                  </td>
                  <td class="px-2 py-1 text-right">
                    <button
                      type="button"
                      aria-disabled={working.value}
                      onClick={() =>
                        void run(async () => {
                          await removeGrant(incidentId, g.granteeId);
                          grants.value = (grants.value ?? []).filter(
                            (x) => x.granteeId !== g.granteeId,
                          );
                          return `Stopped sharing with ${g.granteeId}.`;
                        })
                      }
                      class={BUTTON_SECONDARY}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <form onSubmit={share} class="flex flex-wrap items-end gap-3">
        <Input
          id="grant-grantee"
          label="User id"
          value={granteeId.value}
          onInput={(ev) => {
            granteeId.value = ev.currentTarget.value;
          }}
        />
        <label class="inline-flex items-center gap-2 pb-2 text-sm text-text-primary">
          <input
            type="checkbox"
            checked={canAnnotate.value}
            onChange={(ev) => {
              canAnnotate.value = ev.currentTarget.checked;
            }}
            class={FOCUS_RING}
          />
          Can add notes
        </label>
        <button
          type="submit"
          aria-disabled={working.value}
          class={BUTTON_SECONDARY}
        >
          Share
        </button>
      </form>
      <div role="status" aria-live="polite">
        {message.value && (
          <p class="m-0 text-sm text-text-secondary">{message.value}</p>
        )}
        {error.value && <Alert variant="error">{error.value}</Alert>}
      </div>
    </section>
  );
}
