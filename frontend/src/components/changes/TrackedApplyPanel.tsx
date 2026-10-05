import {
  executionExplanation,
  receiptHref,
  shortId,
} from "@/lib/change-copy.ts";
import type { ApplyTracking } from "@/lib/change-types.ts";
import { sameOperationId } from "@/lib/uuid.ts";
import { ChangeStateBadges } from "./ChangeStateBadges.tsx";

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/**
 * What the change record says about the apply that just ran: a link to the
 * durable receipt, the two state badges, and every count or warning that
 * qualifies the outcome. Rendered under the per-document results, which keep
 * their legacy meaning.
 *
 * Nothing here offers a retry. A document that was never attempted is safe
 * to re-preview, but one whose outcome is unrecorded may already be on the
 * cluster, and an `unknown` outcome may have changed anything — each says
 * so instead of offering a button.
 */
export function TrackedApplyPanel({
  tracking,
  sentOperationId,
}: {
  /** The response's tracking block, when the server returned one. */
  tracking: ApplyTracking | undefined;
  /** The operation id this apply was sent under; null when untracked. */
  sentOperationId: string | null;
}) {
  const requested = sentOperationId !== null;
  if (!tracking) {
    if (!requested) return null;
    return (
      <div role="status">
        <p class="m-0 rounded-lg border border-border-subtle bg-surface px-4 py-3 text-sm text-warning">
          This apply was not recorded: the server returned no change record.
        </p>
      </div>
    );
  }

  const explanation = executionExplanation(tracking.state);
  const notes: string[] = [];
  // The server canonicalizes ids, so compare with sameOperationId, never ===.
  if (requested && !sameOperationId(tracking.operationId, sentOperationId)) {
    notes.push(
      "This record names a different operation id than this apply sent. Check the receipt before relying on it.",
    );
  }
  if (tracking.replayed) {
    notes.push(
      "This is the recorded outcome of an earlier attempt with the same operation id. Nothing was applied a second time.",
    );
  }
  if (tracking.notAttempted > 0) {
    notes.push(
      `${plural(tracking.notAttempted, "document was", "documents were")} not attempted because recording stopped. ${
        tracking.notAttempted === 1 ? "It was" : "They were"
      } never sent to the cluster; validate and apply again to retry.`,
    );
  }
  if (tracking.unrecorded > 0) {
    notes.push(
      `${plural(tracking.unrecorded, "document has", "documents have")} no recorded outcome. The cluster may already hold ${
        tracking.unrecorded === 1 ? "it" : "them"
      }: check the live objects before applying again.`,
    );
  }
  if (tracking.containsSecret) {
    notes.push(
      "This change includes a Secret. Its content is not stored, so it cannot be reused from the record.",
    );
  }

  return (
    <section
      aria-label="Change record"
      class="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4 text-sm"
    >
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="flex flex-wrap items-center gap-2">
          <span class="font-semibold text-text-primary">Change recorded</span>
          <ChangeStateBadges
            state={tracking.state}
            verification={tracking.verification.state}
          />
        </div>
        <a
          href={receiptHref(tracking.operationId)}
          class="font-medium text-accent"
        >
          View change receipt {shortId(tracking.operationId)}
        </a>
      </div>
      {explanation && <p class="m-0 text-text-secondary">{explanation}</p>}
      {notes.map((n) => (
        <p key={n} class="m-0 text-text-secondary">
          {n}
        </p>
      ))}
      {tracking.warnings.length > 0 && (
        <ul aria-label="Recording warnings" class="m-0 pl-5 text-warning">
          {tracking.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      )}
    </section>
  );
}
