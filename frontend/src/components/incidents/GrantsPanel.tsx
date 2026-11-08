/** @jsxImportSource react */
import { type FormEvent, useEffect, useRef, useState } from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError } from "@/lib/api.ts";
import {
  addGrant,
  listGrants,
  removeGrant,
  UnsafeGranteeIdError,
} from "@/lib/incident-api.ts";
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
  const [grants, setGrants] = useState<GrantView[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [granteeId, setGranteeId] = useState("");
  const [canAnnotate, setCanAnnotate] = useState(false);
  const [working, setWorking] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reloadSeq, setReloadSeq] = useState(0);
  // A second action can land before the render that shows `working`.
  const inFlight = useRef(false);
  /**
   * Counts grant changes made here. A list read that was issued before a
   * change may not include it, so its answer is discarded and the list is
   * read again rather than overwriting the change on screen.
   */
  const changes = useRef(0);

  useEffect(() => {
    // A bump of reloadSeq re-reads the list; the value itself is not used.
    void reloadSeq;
    const controller = new AbortController();
    const issuedAt = changes.current;
    setLoadError(null);
    listGrants(incidentId, controller.signal)
      .then((g) => {
        if (controller.signal.aborted) return;
        if (issuedAt !== changes.current) {
          setReloadSeq((n) => n + 1);
          return;
        }
        setGrants(g);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        setLoadError(
          simpleErrorText(
            err,
            "Could not load who this incident is shared with.",
          ),
        );
      });
    return () => controller.abort();
  }, [incidentId, reloadSeq]);

  const run = async (action: () => Promise<string | null>) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setWorking(true);
    setError(null);
    setMessage(null);
    try {
      setMessage(await action());
      changes.current++;
    } catch (err) {
      setError(
        err instanceof UnsafeGranteeIdError
          ? "This collaborator id cannot be removed from the UI."
          : simpleErrorText(err, "The change could not be saved."),
      );
      if (err instanceof ApiError && err.status === 404) {
        setReloadSeq((n) => n + 1);
      }
    } finally {
      inFlight.current = false;
      setWorking(false);
    }
  };

  const upsert = (g: GrantView) => {
    setGrants((prev) => [
      ...(prev ?? []).filter((x) => x.granteeId !== g.granteeId),
      g,
    ]);
  };

  const share = (e: FormEvent) => {
    e.preventDefault();
    const who = granteeId.trim();
    if (!who) {
      setError("Enter the user id to share with.");
      return;
    }
    void run(async () => {
      const g = await addGrant(incidentId, {
        granteeId: who,
        canAnnotate,
      });
      setGranteeId("");
      if (!g) {
        return "That is you. As the owner you already hold every right, so nothing changed.";
      }
      upsert(g);
      return `Shared with ${g.granteeId}.`;
    });
  };

  const list = grants;
  return (
    <section aria-labelledby="incident-grants-heading" className={PANEL}>
      <h2 id="incident-grants-heading" className={HEADING}>
        Sharing
      </h2>
      <p className="m-0 text-xs text-text-muted">
        People you share with see the evidence their own access allows; the rest
        appears to them as withheld.
      </p>
      {loadError && (
        <div role="alert">
          <Alert variant="error">{loadError}</Alert>
        </div>
      )}
      {list && list.length === 0 && (
        <p className="m-0 text-sm text-text-secondary">
          Not shared with anyone.
        </p>
      )}
      {list && list.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <caption className="sr-only">
              People this incident is shared with
            </caption>
            <thead>
              <tr className="text-xs uppercase tracking-wide text-text-muted">
                <th scope="col" className="px-2 py-1 font-semibold">
                  User
                </th>
                <th scope="col" className="px-2 py-1 font-semibold">
                  Can add notes
                </th>
                <th scope="col" className="px-2 py-1 font-semibold">
                  Since
                </th>
                <th scope="col" className="px-2 py-1 font-semibold">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((g) => (
                <tr key={g.granteeId} className="border-t border-border-subtle">
                  <td className="px-2 py-1 text-text-primary">{g.granteeId}</td>
                  <td className="px-2 py-1">
                    <input
                      type="checkbox"
                      aria-label={`${g.granteeId} can add notes`}
                      checked={g.canAnnotate}
                      aria-disabled={working}
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
                      className={FOCUS_RING}
                    />
                  </td>
                  <td className="px-2 py-1 text-text-secondary">
                    {timeAgo(g.createdAt)}
                  </td>
                  <td className="px-2 py-1 text-right">
                    <button
                      type="button"
                      aria-disabled={working}
                      onClick={() =>
                        void run(async () => {
                          await removeGrant(incidentId, g.granteeId);
                          setGrants((prev) =>
                            (prev ?? []).filter(
                              (x) => x.granteeId !== g.granteeId,
                            ),
                          );
                          return `Stopped sharing with ${g.granteeId}.`;
                        })
                      }
                      className={BUTTON_SECONDARY}
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
      <form onSubmit={share} className="flex flex-wrap items-end gap-3">
        <Input
          id="grant-grantee"
          label="User id"
          value={granteeId}
          onChange={(ev) => setGranteeId(ev.currentTarget.value)}
        />
        <label className="inline-flex items-center gap-2 pb-2 text-sm text-text-primary">
          <input
            type="checkbox"
            checked={canAnnotate}
            onChange={(ev) => setCanAnnotate(ev.currentTarget.checked)}
            className={FOCUS_RING}
          />
          Can add notes
        </label>
        <button
          type="submit"
          aria-disabled={working}
          className={BUTTON_SECONDARY}
        >
          Share
        </button>
      </form>
      <div role="status" aria-live="polite">
        {message && (
          <p className="m-0 text-sm text-text-secondary">{message}</p>
        )}
        {error && <Alert variant="error">{error}</Alert>}
      </div>
    </section>
  );
}
