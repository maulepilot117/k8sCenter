import { useSignal } from "@preact/signals";
import { useEffect, useState } from "preact/hooks";
import { Alert } from "@/components/ui/Alert.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { ApiError } from "@/lib/api.ts";
import { getReceipt, getVerification } from "@/lib/change-api.ts";
import {
  checkReasonText,
  checkStatusBadge,
  clusterDisplayName,
  executionExplanation,
  hiddenText,
  isFinalVerification,
  isOperationId,
  RECORDS_UNAVAILABLE,
  receiptHref,
  repairEligibility,
  repairHref,
  verificationPollDelayMs,
  verificationReasons,
} from "@/lib/change-copy.ts";
import type {
  CheckView,
  ReceiptAccess,
  ReceiptDetail,
  ReceiptObjectView,
  VerificationView,
} from "@/lib/change-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { ChangeStateBadges } from "@/src/components/changes/ChangeStateBadges.tsx";
import { OwnershipList } from "@/src/components/changes/OwnershipList.tsx";

/**
 * The durable record of one tracked apply (Release E U31, plan D6/D8).
 *
 * What it will not do, by design:
 *   - merge execution and verification into one badge;
 *   - render an inconclusive verification or an unknown outcome as success;
 *   - show anything about an object the reader can no longer access beyond
 *     its position in the request;
 *   - offer to reuse applied content (none is stored) or to write to Git.
 *
 * The root element is the same during SSR and after hydration (the island
 * renders its loading state on the server), so there is no placeholder root
 * to diverge from the hydrated one.
 */

const ROOT_CLASS = "flex flex-col gap-5";

type LoadError = { status: number | null; message: string };

const INVALID_ID = "This is not a valid change receipt id.";

function loadErrorFor(err: unknown): LoadError {
  if (err instanceof ApiError) {
    switch (err.status) {
      case 400:
        return { status: 400, message: INVALID_ID };
      case 404:
        return {
          status: 404,
          message:
            "Change receipt not found. It may not exist, or it may not be shared with you.",
        };
      case 503:
        return { status: 503, message: RECORDS_UNAVAILABLE };
    }
    return {
      status: err.status,
      message: "Could not load this change receipt.",
    };
  }
  return { status: null, message: "Could not load this change receipt." };
}

function verificationErrorText(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.status) {
      case 403:
        return "Live verification needs access to this receipt's cluster; remote clusters require the admin role. Showing the last recorded verification.";
      case 504:
        return "Verification timed out. Showing the last recorded verification.";
      case 503:
        return "Verification is unavailable right now: the change record store cannot be reached. Showing the last recorded verification.";
      case 400:
        return "This receipt cannot be verified. Showing the last recorded verification.";
    }
  }
  return "Verification could not run. Showing the last recorded verification.";
}

const ACCESS_LABEL: Record<ReceiptAccess, string> = {
  owner: "You made this change",
  grantee: "Shared with you",
  admin: "Visible to you as an administrator",
};

function When({ at }: { at?: string }) {
  if (!at) return <span class="text-text-muted">—</span>;
  return (
    <time dateTime={at} title={at}>
      {new Date(at).toLocaleString()} ({timeAgo(at)})
    </time>
  );
}

export default function ChangeReceipt({
  id,
  msPerSecond = 1000,
}: {
  id: string;
  /**
   * Milliseconds per second of poll delay. Tests shrink it so the polling
   * loop can be driven through several iterations in real time; pages never
   * pass it.
   */
  msPerSecond?: number;
}) {
  const receipt = useSignal<ReceiptDetail | null>(null);
  const loadError = useSignal<LoadError | null>(null);
  const verification = useSignal<VerificationView | null>(null);
  const verifyError = useSignal<string | null>(null);
  const polling = useSignal(false);
  // Bumped by "Check again" to restart the load-and-poll cycle.
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    document.title = "Change receipt - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const wait = (ms: number) =>
      new Promise<void>((resolve) => {
        timer = setTimeout(resolve, ms);
      });

    async function run() {
      verifyError.value = null;
      // A live view from an earlier cycle must not shadow what this cycle
      // reads: the stored verdict may have been finalized meanwhile.
      verification.value = null;
      if (!isOperationId(id)) {
        loadError.value = { status: 400, message: INVALID_ID };
        return;
      }
      let rec: ReceiptDetail;
      try {
        rec = await getReceipt(id, signal);
      } catch (err) {
        if (signal.aborted) return;
        loadError.value = loadErrorFor(err);
        return;
      }
      if (signal.aborted) return;
      loadError.value = null;
      receipt.value = rec;

      // An apply still running is recorded document by document; re-read it
      // until it settles before asking for verification.
      while (rec.state === "applying") {
        polling.value = true;
        await wait(verificationPollDelayMs(undefined, msPerSecond));
        if (signal.aborted) return;
        try {
          rec = await getReceipt(id, signal);
        } catch (err) {
          if (signal.aborted) return;
          polling.value = false;
          verifyError.value = loadErrorFor(err).message;
          return;
        }
        if (signal.aborted) return;
        receipt.value = rec;
      }

      if (
        rec.state === "previewed" ||
        isFinalVerification(rec.verification.state)
      ) {
        polling.value = false;
        return;
      }

      // Stateless polling (D6): one verification pass per request, repeated
      // while the server says it is still verifying, stopping at the first
      // final state.
      polling.value = true;
      for (;;) {
        let view: VerificationView;
        try {
          view = await getVerification(id, signal);
        } catch (err) {
          if (signal.aborted) return;
          polling.value = false;
          verifyError.value = verificationErrorText(err);
          return;
        }
        if (signal.aborted) return;
        verification.value = view;
        if (isFinalVerification(view.state)) {
          polling.value = false;
          // Pick up the persisted verdict and verifiedAt.
          try {
            const settled = await getReceipt(id, signal);
            if (!signal.aborted) receipt.value = settled;
          } catch {
            // The verdict above is already on screen.
          }
          return;
        }
        await wait(
          verificationPollDelayMs(view.retryAfterSeconds, msPerSecond),
        );
        if (signal.aborted) return;
      }
    }

    void run();
    return () => {
      controller.abort();
      if (timer !== undefined) clearTimeout(timer);
      polling.value = false;
    };
  }, [id, attempt, msPerSecond]);

  const rec = receipt.value;
  const err = loadError.value;

  return (
    <div class={ROOT_CLASS}>
      <div class="flex flex-col gap-1">
        <a href="/changes" class="text-xs font-medium text-accent">
          ← All recorded changes
        </a>
        <h1 class="m-0 text-2xl font-bold tracking-tight text-text-primary">
          Change receipt
        </h1>
        <p class="m-0 break-all font-mono text-xs text-text-muted">{id}</p>
      </div>

      {err && (
        <div role="alert">
          <Alert variant="error">{err.message}</Alert>
        </div>
      )}

      {!err && !rec && (
        <p role="status" class="m-0 text-sm text-text-muted">
          Loading change receipt…
        </p>
      )}

      {rec && (
        <ReceiptBody
          receipt={rec}
          verification={verification.value}
          polling={polling.value}
          verifyError={verifyError.value}
          onCheckAgain={() => setAttempt((n) => n + 1)}
        />
      )}
    </div>
  );
}

function ReceiptBody({
  receipt: rec,
  verification,
  polling,
  verifyError,
  onCheckAgain,
}: {
  receipt: ReceiptDetail;
  verification: VerificationView | null;
  polling: boolean;
  verifyError: string | null;
  onCheckAgain: () => void;
}) {
  const verState = verification?.state ?? rec.verification.state;
  const checks = verification?.checks ?? rec.checks;
  const redactedChecks = verification?.redactedChecks ?? rec.redactedChecks;
  const explanation = executionExplanation(rec.state);
  const reasons =
    verState === "inconclusive" || verState === "verification_failed"
      ? verificationReasons(checks)
      : [];
  const s = rec.summary;
  const counts = [
    s.created > 0 && `${s.created} created`,
    s.configured > 0 && `${s.configured} configured`,
    s.unchanged > 0 && `${s.unchanged} unchanged`,
    s.failed > 0 && `${s.failed} failed`,
    s.notRecorded > 0 && `${s.notRecorded} without a recorded outcome`,
  ].filter(Boolean);

  return (
    <>
      <section
        aria-label="Change state"
        class="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4"
      >
        <ChangeStateBadges state={rec.state} verification={verState} />
        {explanation && (
          <p class="m-0 text-sm text-text-secondary">{explanation}</p>
        )}
        {reasons.length > 0 && (
          <div class="text-sm text-text-secondary">
            <p class="m-0 font-medium text-text-primary">
              {verState === "inconclusive"
                ? "Why verification is inconclusive:"
                : "Why verification failed:"}
            </p>
            <ul class="m-0 pl-5">
              {reasons.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </div>
        )}
        <p class="m-0 text-sm text-text-muted">
          {s.total} document{s.total === 1 ? "" : "s"}
          {counts.length > 0 ? `: ${counts.join(", ")}` : ""}
        </p>
      </section>

      <ReceiptFacts receipt={rec} />

      <ObjectsSection receipt={rec} />

      <section
        aria-labelledby="verification-heading"
        class="flex flex-col gap-2"
      >
        <div class="flex flex-wrap items-center gap-3">
          <h2
            id="verification-heading"
            class="m-0 text-base font-semibold text-text-primary"
          >
            Verification
          </h2>
          {polling && (
            <span role="status" class="text-xs text-text-muted">
              Checking the live objects…
            </span>
          )}
        </div>
        <p class="m-0 text-xs text-text-muted">
          Verification reads the live objects after the apply. It is separate
          from whether the API server accepted the change.
        </p>
        {verifyError && (
          <div
            role="status"
            class="flex flex-wrap items-center gap-3 text-sm text-warning"
          >
            <span>{verifyError}</span>
            <button
              type="button"
              onClick={onCheckAgain}
              class="cursor-pointer rounded-md border border-border-primary bg-transparent px-3 py-1 text-xs font-medium text-text-secondary"
            >
              Check again
            </button>
          </div>
        )}
        <ChecksList checks={checks} />
        {redactedChecks > 0 && (
          <p class="m-0 text-xs text-text-muted">
            {hiddenText("checks", redactedChecks)}
          </p>
        )}
      </section>

      <section aria-labelledby="ownership-heading" class="flex flex-col gap-2">
        <h2
          id="ownership-heading"
          class="m-0 text-base font-semibold text-text-primary"
        >
          GitOps ownership when applied
        </h2>
        {rec.ownership.length > 0 ? (
          <OwnershipList results={rec.ownership} />
        ) : (
          <p class="m-0 text-sm text-text-muted">
            No GitOps ownership was recorded for this change.
          </p>
        )}
        {rec.redactedOwnership > 0 && (
          <p class="m-0 text-xs text-text-muted">
            {hiddenText("ownership", rec.redactedOwnership)}
          </p>
        )}
      </section>

      <RepairSection receipt={rec} />
    </>
  );
}

function ReceiptFacts({ receipt: rec }: { receipt: ReceiptDetail }) {
  return (
    <dl class="m-0 grid grid-cols-1 gap-x-6 gap-y-2 rounded-lg border border-border-subtle bg-surface p-4 text-sm sm:grid-cols-[max-content_1fr]">
      <dt class="text-text-muted">Cluster</dt>
      <dd class="m-0 text-text-primary">{clusterDisplayName(rec.clusterId)}</dd>

      <dt class="text-text-muted">Cluster registration</dt>
      <dd class="m-0 text-text-primary">
        <span class="font-mono">{rec.clusterGeneration}</span>
        {rec.targetGenerationChanged && (
          <span class="mt-1 block text-warning">
            The cluster registration changed since this apply: the cluster was
            re-registered, so it may no longer be the cluster that was changed.
          </span>
        )}
      </dd>

      <dt class="text-text-muted">Content digest</dt>
      <dd class="m-0 break-all font-mono text-text-primary">
        {rec.contentDigest || (
          <span class="font-sans text-text-muted">
            Hidden: this change includes a Secret, and only its owner sees the
            digest.
          </span>
        )}
      </dd>

      <dt class="text-text-muted">Applied by</dt>
      <dd class="m-0 text-text-primary">
        {rec.ownerUsername} · {ACCESS_LABEL[rec.access] ?? rec.access}
      </dd>

      <dt class="text-text-muted">Documents</dt>
      <dd class="m-0 text-text-primary">
        {rec.documentCount}
        {rec.force && " · conflicts forced"}
        {rec.containsSecret && " · includes a Secret (content not stored)"}
      </dd>

      <dt class="text-text-muted">Recorded</dt>
      <dd class="m-0 text-text-primary">
        <When at={rec.createdAt} />
      </dd>

      <dt class="text-text-muted">Applying started</dt>
      <dd class="m-0 text-text-primary">
        <When at={rec.mutationStartedAt} />
      </dd>

      <dt class="text-text-muted">Completed</dt>
      <dd class="m-0 text-text-primary">
        <When at={rec.completedAt} />
      </dd>

      <dt class="text-text-muted">Verified</dt>
      <dd class="m-0 text-text-primary">
        <When at={rec.verifiedAt} />
      </dd>

      {rec.repairOf && (
        <>
          <dt class="text-text-muted">Repairs</dt>
          <dd class="m-0">
            <a href={receiptHref(rec.repairOf)} class="font-mono text-accent">
              {rec.repairOf}
            </a>
          </dd>
        </>
      )}
    </dl>
  );
}

function actionTone(action: string | undefined) {
  switch (action) {
    case "created":
      return "ok" as const;
    case "configured":
      return "info" as const;
    case "failed":
      return "crit" as const;
    default:
      return "neutral" as const;
  }
}

function ObjectRow({ o }: { o: ReceiptObjectView }) {
  const n = o.index + 1;
  if (o.redacted) {
    return (
      <tr class="border-t border-border-subtle">
        <td class="px-3 py-2 align-top text-text-muted">{n}</td>
        <td colSpan={4} class="px-3 py-2 text-text-muted">
          Object #{n} — you no longer have access to this resource
        </td>
      </tr>
    );
  }
  return (
    <tr class="border-t border-border-subtle">
      <td class="px-3 py-2 align-top text-text-muted">{n}</td>
      <td class="px-3 py-2 align-top text-text-secondary">{o.kind ?? "—"}</td>
      <td class="px-3 py-2 align-top text-text-secondary">
        {o.namespace || "—"}
      </td>
      <td class="px-3 py-2 align-top font-mono text-text-primary">
        {o.name ?? "—"}
      </td>
      <td class="px-3 py-2 align-top">
        <div class="flex flex-col gap-1">
          {o.action ? (
            <StatusBadge label={o.action} tone={actionTone(o.action)} />
          ) : (
            <StatusBadge label="no recorded outcome" tone="neutral" />
          )}
          {o.error && <span class="text-xs text-danger">{o.error}</span>}
        </div>
      </td>
    </tr>
  );
}

function ObjectsSection({ receipt: rec }: { receipt: ReceiptDetail }) {
  return (
    <section aria-labelledby="objects-heading" class="flex flex-col gap-2">
      <h2
        id="objects-heading"
        class="m-0 text-base font-semibold text-text-primary"
      >
        Objects
      </h2>
      {rec.objects.length > 0 ? (
        <div class="overflow-x-auto rounded-lg border border-border-subtle bg-surface">
          <table class="w-full border-collapse text-left text-sm">
            <caption class="sr-only">Objects in this change</caption>
            <thead>
              <tr class="text-xs uppercase tracking-wide text-text-muted">
                <th scope="col" class="px-3 py-2 font-semibold">
                  #
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Kind
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Namespace
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Name
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Result
                </th>
              </tr>
            </thead>
            <tbody>
              {rec.objects.map((o) => (
                <ObjectRow key={o.index} o={o} />
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p class="m-0 text-sm text-text-muted">
          No object outcome was recorded.
        </p>
      )}
      {rec.redactedObjects > 0 && (
        <p class="m-0 text-xs text-text-muted">
          {hiddenText("objects", rec.redactedObjects)}
        </p>
      )}
    </section>
  );
}

function checkSubject(c: CheckView): string | null {
  if (!c.source) return null;
  const where = c.source.namespace ? `${c.source.namespace}/` : "";
  return `${c.source.kind} ${where}${c.source.name}`;
}

function ChecksList({ checks }: { checks: CheckView[] }) {
  if (checks.length === 0) {
    return (
      <p class="m-0 text-sm text-text-muted">
        No verification check has run yet.
      </p>
    );
  }
  return (
    <ul
      aria-label="Verification checks"
      class="m-0 list-none divide-y divide-border-subtle overflow-hidden rounded-lg border border-border-subtle bg-surface p-0"
    >
      {checks.map((c, i) => {
        const badge = checkStatusBadge(c.status);
        const subject = c.redacted ? null : checkSubject(c);
        return (
          <li
            key={`${i}-${c.checkId}`}
            class="flex flex-col gap-1 px-3.5 py-2.5 text-sm"
          >
            <div class="flex flex-wrap items-center gap-2">
              <StatusBadge label={badge.label} tone={badge.tone} />
              {subject && (
                <span class="font-mono text-text-primary">{subject}</span>
              )}
              {c.redacted && (
                <span class="text-text-muted">
                  Details hidden: you no longer have access to this resource
                </span>
              )}
            </div>
            <p class="m-0 text-text-secondary">{checkReasonText(c.reason)}</p>
            {!c.redacted && c.message && (
              <p class="m-0 text-text-muted">{c.message}</p>
            )}
            {!c.redacted && c.remediation && (
              <p class="m-0 text-text-muted">{c.remediation}</p>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function RepairSection({ receipt: rec }: { receipt: ReceiptDetail }) {
  const eligibility = repairEligibility(rec);
  if (eligibility.kind === "not-applicable") return null;
  return (
    <section aria-labelledby="repair-heading" class="flex flex-col gap-2">
      <h2
        id="repair-heading"
        class="m-0 text-base font-semibold text-text-primary"
      >
        Retry
      </h2>
      {eligibility.kind === "secret" ? (
        <p class="m-0 text-sm text-text-secondary">
          The original content is not stored. Re-create the Secret manifest to
          retry.
        </p>
      ) : (
        <>
          <p class="m-0 text-sm text-text-secondary">
            k8sCenter does not store applied content, so a retry starts from
            manifests you supply. YAML Apply opens linked to this change; you
            validate and apply the current content as a new change.
          </p>
          <a
            href={repairHref(rec.operationId, rec.clusterId)}
            class="self-start rounded-md bg-accent px-4 py-2 text-sm font-semibold no-underline"
            style={{ color: "var(--bg-base)" }}
          >
            Retry failed objects
          </a>
        </>
      )}
    </section>
  );
}
