/** Rubrik write dialogs: on-demand snapshot, SLA change and restore (U14).
 *
 * Which controls show comes from the server's per-action flags
 * (`canSnapshot`, `canChangeSla`, `canRestore`, U6). They already fold in
 * the user's grants, the writes setting, open operations and the admin-only
 * rules, so nothing here re-derives a permission. The server checks every
 * request again.
 *
 * Every submission carries an operation id. A retry of the same submission
 * (after a network error, say) reuses it, so the server answers it from its
 * record and it can never run twice; changing what is asked for starts a new
 * id.
 */

import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { ApiError, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import type {
  RubrikNamespaceDetail,
  RubrikObject,
  RubrikObjectDetail,
  RubrikOperation,
  RubrikRestoreTargets,
  RubrikSlaDomain,
  RubrikSlaDomainList,
  RubrikSlaRef,
  RubrikSnapshot,
} from "@/lib/rubrik-types.ts";
import { uuidv4 } from "@/lib/uuid.ts";

// Full literals: e2e/tests/api-routes.spec.ts checks every quoted "/v1/..."
// path is mounted.
const SLA_DOMAINS_URL = "/v1/rubrik/sla-domains";
const RESTORES_URL = "/v1/rubrik/restores";

const SELECT_CLASS =
  "w-full rounded-[9px] border border-border-subtle bg-elevated px-3 py-2 text-[13.5px] text-text-primary outline-none focus:border-accent";
const LABEL_CLASS =
  "mb-1.5 block text-[12.5px] font-semibold text-text-secondary";

// RSC's pseudo SLA domains for "not protected" (model.go unprotectedSLAIDs).
// Used only to decide whether the snapshot dialog asks for an SLA up front;
// the server refuses with sla_required when it disagrees, and the dialog
// then asks.
const UNPROTECTED_SLA_IDS = [
  "",
  "UNPROTECTED",
  "DO_NOT_PROTECT",
  "DONOTPROTECT",
  "NO_SLA",
];

function slaProtects(sla: RubrikSlaRef | undefined): boolean {
  const id = (sla?.id ?? "").trim().toUpperCase();
  return !UNPROTECTED_SLA_IDS.includes(id);
}

function slaLabel(sla: RubrikSlaRef | undefined): string {
  return slaProtects(sla) ? sla?.name || sla?.id || "" : "No SLA domain";
}

export function objectLabel(o: RubrikObject): string {
  return o.family === "protectionSet"
    ? `Protection set ${o.name}`
    : `Namespace ${o.name}`;
}

/** The server's refusal in the page's words. */
export function writeError(err: unknown): string {
  if (!(err instanceof ApiError)) {
    return "The request did not reach k8sCenter. Submitting again sends the same request, so it cannot run twice.";
  }
  if (err.reason === "operation_in_flight") {
    return "An operation on this object is already running. Wait for it to finish, then try again.";
  }
  const text = (err.detail ?? "").trim();
  if (!text) return "The request failed.";
  const sentence = text[0].toUpperCase() + text.slice(1);
  return /[.!?]$/.test(sentence) ? sentence : `${sentence}.`;
}

/**
 * Sends one write with an operation id that stays the same across retries
 * of the same request (`key`) and changes when the request changes.
 */
function useOperationSubmit(onStarted: (op: RubrikOperation) => void) {
  const op = useRef<{ key: string; id: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reason, setReason] = useState<string | undefined>();

  async function submit(
    key: string,
    send: (operationId: string) => Promise<{ data: RubrikOperation }>,
  ): Promise<boolean> {
    if (op.current?.key !== key) op.current = { key, id: uuidv4() };
    setBusy(true);
    setError(null);
    setReason(undefined);
    try {
      const res = await send(op.current.id);
      op.current = null;
      onStarted(res.data);
      return true;
    } catch (err) {
      setError(writeError(err));
      setReason(err instanceof ApiError ? err.reason : undefined);
      return false;
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, reason, submit };
}

/** The SLA domains the service account can see, loaded once. */
function useSlaDomains(wanted: boolean) {
  const [domains, setDomains] = useState<RubrikSlaDomain[] | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  useEffect(() => {
    if (!wanted || domains !== null) return;
    let cancelled = false;
    apiGet<RubrikSlaDomainList>(SLA_DOMAINS_URL)
      .then((res) => {
        if (!cancelled) setDomains(res.data.slaDomains ?? []);
      })
      .catch((err) => {
        if (!cancelled) setFailed(writeError(err));
      });
    return () => {
      cancelled = true;
    };
  }, [wanted, domains]);
  return { domains, failed };
}

// ---------------------------------------------------------------------------
// Modal shell

function Modal({
  title,
  onClose,
  children,
  footer,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer: ReactNode;
}) {
  const box = useRef<HTMLDivElement>(null);
  const titleId = useId();
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    globalThis.addEventListener("keydown", onKey);
    box.current
      ?.querySelector<HTMLElement>("input, select, button:not([disabled])")
      ?.focus();
    return () => globalThis.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div
      className="glass-scrim fixed inset-0 z-50 flex items-center justify-center p-4"
      onClick={onClose}
    >
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="glass-elevated flex max-h-[90vh] w-full max-w-lg flex-col gap-3 overflow-y-auto rounded-2xl p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <h3
          id={titleId}
          className="m-0 text-lg font-semibold text-text-primary"
        >
          {title}
        </h3>
        {children}
        <div className="flex justify-end gap-2">{footer}</div>
      </div>
    </div>
  );
}

/** Radio list of the objects an action may target, each with its SLA (R15). */
function ObjectPicker({
  name,
  objects,
  value,
  onChange,
}: {
  name: string;
  objects: RubrikObject[];
  value: string;
  onChange: (id: string) => void;
}) {
  return (
    <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
      <legend className={LABEL_CLASS}>
        More than one object protects this namespace. Choose one.
      </legend>
      {objects.map((o) => (
        <label
          key={o.id}
          className="flex cursor-pointer items-center gap-2 text-[13.5px] text-text-primary"
        >
          <input
            type="radio"
            name={name}
            value={o.id}
            checked={value === o.id}
            onChange={() => onChange(o.id)}
          />
          {objectLabel(o)} · SLA {slaLabel(o.effectiveSla)}
        </label>
      ))}
    </fieldset>
  );
}

function SlaSelect({
  id,
  domains,
  failed,
  value,
  onChange,
  allowNone,
}: {
  id: string;
  domains: RubrikSlaDomain[] | null;
  failed: string | null;
  value: string | null;
  onChange: (v: string | null) => void;
  allowNone?: boolean;
}) {
  if (failed) {
    return (
      <Alert variant="error">
        The SLA domains could not be loaded: {failed}
      </Alert>
    );
  }
  if (domains === null) {
    return <p className="m-0 text-sm text-text-muted">Loading SLA domains…</p>;
  }
  return (
    <select
      id={id}
      className={SELECT_CLASS}
      value={value === null ? "__choose__" : value === "" ? "__none__" : value}
      onChange={(e) => {
        const v = e.currentTarget.value;
        onChange(v === "__choose__" ? null : v === "__none__" ? "" : v);
      }}
    >
      <option value="__choose__">Choose an SLA domain</option>
      {allowNone && (
        <option value="__none__">No SLA domain (stop this protection)</option>
      )}
      {domains.map((d) => (
        <option key={d.id} value={d.id}>
          {d.name}
        </option>
      ))}
    </select>
  );
}

// ---------------------------------------------------------------------------
// Namespace controls: snapshot and SLA

/** The objects `flag` allows the action on, in the server's target order. */
function targets(
  detail: RubrikNamespaceDetail,
  flag: "canSnapshot" | "canChangeSla",
): RubrikObjectDetail[] {
  const byId = new Map(detail.objects.map((o) => [o.id, o]));
  return detail.namespace.actionTargets
    .map((id) => byId.get(id))
    .filter((o): o is RubrikObjectDetail => !!o && o[flag]);
}

/** Whether an object is a protection set covering more than one namespace. */
function multiNamespace(o: RubrikObject): boolean {
  return (
    o.family === "protectionSet" &&
    (!!o.coverage.all ||
      !!o.coverage.unresolved ||
      (o.coverage.namespaces?.length ?? 0) > 1)
  );
}

export function RubrikWriteControls({
  namespace,
  detail,
  writesEnabled,
  onStarted,
}: {
  namespace: string;
  detail: RubrikNamespaceDetail;
  writesEnabled: boolean;
  onStarted: (op: RubrikOperation) => void;
}) {
  const [open, setOpen] = useState<"snapshot" | "sla" | null>(null);
  if (!writesEnabled) return null;
  const row = detail.namespace;
  const snapshotTargets = row.canSnapshot ? targets(detail, "canSnapshot") : [];
  const slaTargets = row.canChangeSla ? targets(detail, "canChangeSla") : [];

  if (snapshotTargets.length === 0 && slaTargets.length === 0) {
    // Only a set covering several namespaces is managed by an admin; any
    // other reason (grants, an operation in flight) shows no note.
    const managed = detail.objects.some((o) => !o.relic && multiNamespace(o));
    return managed ? (
      <p className="m-0 text-[13px] text-text-muted">
        Managed by an admin: this namespace is protected by a set that covers
        several namespaces.
      </p>
    ) : null;
  }

  const done = (op: RubrikOperation) => {
    setOpen(null);
    onStarted(op);
  };
  return (
    <div className="flex flex-wrap gap-2">
      {snapshotTargets.length > 0 && (
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={() => setOpen("snapshot")}
        >
          Take snapshot
        </Button>
      )}
      {slaTargets.length > 0 && (
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={() => setOpen("sla")}
        >
          Change SLA
        </Button>
      )}
      {open === "snapshot" && (
        <SnapshotDialog
          namespace={namespace}
          objects={snapshotTargets}
          onStarted={done}
          onClose={() => setOpen(null)}
        />
      )}
      {open === "sla" && (
        <SlaDialog
          namespace={namespace}
          objects={slaTargets}
          all={detail.objects}
          hiddenObjects={!!row.hiddenObjects}
          onStarted={done}
          onClose={() => setOpen(null)}
        />
      )}
    </div>
  );
}

function SnapshotDialog({
  namespace,
  objects,
  onStarted,
  onClose,
}: {
  namespace: string;
  objects: RubrikObject[];
  onStarted: (op: RubrikOperation) => void;
  onClose: () => void;
}) {
  const [objectId, setObjectId] = useState(
    objects.length === 1 ? objects[0].id : "",
  );
  const [slaId, setSlaId] = useState<string | null>(null);
  const { busy, error, reason, submit } = useOperationSubmit(onStarted);
  const object = objects.find((o) => o.id === objectId);
  // RSC needs an SLA domain for an on-demand snapshot. An object with none
  // takes one picked here; the server's sla_required also asks for one.
  const needSla =
    !!object &&
    (!slaProtects(object.effectiveSla) || reason === "sla_required");
  const { domains, failed } = useSlaDomains(needSla);
  const ready = !!object && (!needSla || !!slaId);

  return (
    <Modal
      title={`Take a snapshot of ${namespace}`}
      onClose={onClose}
      footer={
        <>
          <Button type="button" variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="primary"
            size="sm"
            disabled={!ready || busy}
            onClick={() => {
              if (!object) return;
              const sla = needSla ? (slaId ?? "") : "";
              void submit(`${object.id}|${sla}`, (operationId) =>
                apiPost<RubrikOperation>(
                  `/v1/rubrik/namespaces/${encodeURIComponent(namespace)}/snapshots`,
                  {
                    operationId,
                    objectId: object.id,
                    ...(needSla ? { slaId: sla } : {}),
                  },
                ),
              );
            }}
          >
            {busy ? "Starting…" : "Take snapshot"}
          </Button>
        </>
      }
    >
      {objects.length > 1 && (
        <ObjectPicker
          name="rubrik-snapshot-object"
          objects={objects}
          value={objectId}
          onChange={setObjectId}
        />
      )}
      {object && !needSla && (
        <p className="m-0 text-[13.5px] text-text-secondary">
          The snapshot runs under {objectLabel(object).toLowerCase()}'s SLA
          domain, {slaLabel(object.effectiveSla)}, and expires by its rules.
        </p>
      )}
      {needSla && (
        <div>
          <label className={LABEL_CLASS} htmlFor="rubrik-snapshot-sla">
            This object has no SLA domain. Choose the one the snapshot is kept
            under.
          </label>
          <SlaSelect
            id="rubrik-snapshot-sla"
            domains={domains}
            failed={failed}
            value={slaId}
            onChange={setSlaId}
          />
        </div>
      )}
      {error && (
        <div role="alert">
          <Alert variant="error">{error}</Alert>
        </div>
      )}
    </Modal>
  );
}

function SlaDialog({
  namespace,
  objects,
  all,
  hiddenObjects,
  onStarted,
  onClose,
}: {
  namespace: string;
  objects: RubrikObject[];
  /** Every object covering the namespace that the user sees. */
  all: RubrikObject[];
  hiddenObjects: boolean;
  onStarted: (op: RubrikOperation) => void;
  onClose: () => void;
}) {
  const [objectId, setObjectId] = useState(
    objects.length === 1 ? objects[0].id : "",
  );
  // null: nothing chosen yet; "": remove the SLA domain.
  const [slaId, setSlaId] = useState<string | null>(null);
  const { busy, error, submit } = useOperationSubmit(onStarted);
  const { domains, failed } = useSlaDomains(true);
  const object = objects.find((o) => o.id === objectId);
  const current = object?.effectiveSla;
  const unchanged =
    slaId !== null &&
    (slaId === ""
      ? !slaProtects(current)
      : slaId.toLowerCase() === (current?.id ?? "").toLowerCase());
  const ready = !!object && slaId !== null && !unchanged;
  const newName =
    slaId === null
      ? ""
      : slaId === ""
        ? "No SLA domain"
        : (domains?.find((d) => d.id === slaId)?.name ?? slaId);
  const others = all.filter(
    (o) => o.id !== objectId && !o.relic && slaProtects(o.effectiveSla),
  );

  return (
    <Modal
      title={`Change the SLA domain for ${namespace}`}
      onClose={onClose}
      footer={
        <>
          <Button type="button" variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant={slaId === "" ? "danger" : "primary"}
            size="sm"
            disabled={!ready || busy}
            onClick={() => {
              if (!object || slaId === null) return;
              void submit(`${object.id}|${slaId}`, (operationId) =>
                apiPut<RubrikOperation>(
                  `/v1/rubrik/namespaces/${encodeURIComponent(namespace)}/sla`,
                  { operationId, objectId: object.id, slaId },
                ),
              );
            }}
          >
            {busy
              ? "Saving…"
              : slaId === ""
                ? "Remove SLA domain"
                : "Change SLA domain"}
          </Button>
        </>
      }
    >
      {objects.length > 1 && (
        <ObjectPicker
          name="rubrik-sla-object"
          objects={objects}
          value={objectId}
          onChange={setObjectId}
        />
      )}
      {object && (
        <>
          <div>
            <label className={LABEL_CLASS} htmlFor="rubrik-sla-new">
              New SLA domain for {objectLabel(object).toLowerCase()}
            </label>
            <SlaSelect
              id="rubrik-sla-new"
              domains={domains}
              failed={failed}
              value={slaId}
              onChange={setSlaId}
              allowNone
            />
          </div>
          {slaId !== null && (
            <p
              data-testid="rubrik-sla-change"
              className="m-0 text-[13.5px] text-text-secondary"
            >
              {slaLabel(current)} → {newName}
              {unchanged && " (no change)"}
            </p>
          )}
          {slaId === "" && !unchanged && (
            <Alert variant="warning">
              {others.length > 0 ? (
                <>
                  {namespace} is still protected by{" "}
                  {others.map(objectLabel).join(", ").toLowerCase()}. Existing
                  snapshots are kept.
                </>
              ) : (
                <>
                  Protection of {namespace} stops: no other object you can see
                  protects it.
                  {hiddenObjects &&
                    " Objects you cannot see also cover it and may still protect it."}{" "}
                  Existing snapshots are kept.
                </>
              )}
            </Alert>
          )}
          {slaId !== null && slaId !== "" && (
            <p className="m-0 text-xs text-text-muted">
              Existing snapshots are kept under their current retention.
            </p>
          )}
        </>
      )}
      {error && (
        <div role="alert">
          <Alert variant="error">{error}</Alert>
        </div>
      )}
    </Modal>
  );
}

// ---------------------------------------------------------------------------
// Restore

const RESTORE_REFUSAL: Record<string, string> = {
  admin_only:
    "Only admins can restore from this object: its snapshots may hold cluster-wide resources, or it covers several namespaces.",
  no_grant: "You do not have the Rubrik restore grant this restore needs.",
  writes_disabled: "Rubrik write actions are turned off.",
  set_scope_unknown:
    "This protection set does not resolve to a list of namespaces. Restore it from RSC.",
  source_unresolved:
    "This object does not name its namespace. Restore it from RSC.",
};

/** Whether a snapshot can be offered for a restore at all. */
export function restorable(s: RubrikSnapshot): boolean {
  return !s.expired && !s.corrupted && !s.quarantined;
}

export function RubrikRestoreDialog({
  namespace,
  object,
  snapshot,
  onStarted,
  onClose,
}: {
  namespace: string;
  object: RubrikObject;
  snapshot: RubrikSnapshot;
  onStarted: (op: RubrikOperation) => void;
  onClose: () => void;
}) {
  const [info, setInfo] = useState<RubrikRestoreTargets | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [target, setTarget] = useState("");
  const [confirming, setConfirming] = useState(false);
  const { busy, error, submit } = useOperationSubmit(onStarted);

  useEffect(() => {
    let cancelled = false;
    apiGet<RubrikRestoreTargets>(
      `/v1/rubrik/namespaces/${encodeURIComponent(namespace)}/restore-targets?objectId=${encodeURIComponent(object.id)}`,
    )
      .then((res) => {
        if (cancelled) return;
        setInfo(res.data);
        const list = res.data.targets ?? [];
        setTarget(
          list.includes(res.data.sourceNamespace)
            ? res.data.sourceNamespace
            : (list[0] ?? ""),
        );
      })
      .catch((err) => {
        if (!cancelled) setLoadError(writeError(err));
      });
    return () => {
      cancelled = true;
    };
  }, [namespace, object.id]);

  const taken = new Date(snapshot.time).toLocaleString();

  if (confirming && target) {
    return (
      <ConfirmDialog
        title="Restore snapshot"
        danger
        confirmLabel={busy ? "Restoring…" : "Restore"}
        loading={busy}
        typeToConfirm={target}
        message={
          <>
            Restore the snapshot of {objectLabel(object).toLowerCase()} taken{" "}
            {taken} into <strong>{target}</strong>. {info?.overwriteNotice} Type
            the namespace name to confirm.
          </>
        }
        onConfirm={() => {
          void submit(`${object.id}|${snapshot.id}|${target}`, (operationId) =>
            apiPost<RubrikOperation>(RESTORES_URL, {
              operationId,
              objectId: object.id,
              snapshotId: snapshot.id,
              targetNamespace: target,
            }),
          ).then((ok) => {
            if (!ok) setConfirming(false);
          });
        }}
        onCancel={() => setConfirming(false)}
      />
    );
  }

  const list = info?.targets ?? [];
  return (
    <Modal
      title="Restore a snapshot"
      onClose={onClose}
      footer={
        <>
          <Button type="button" variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="danger"
            size="sm"
            disabled={!target || busy}
            onClick={() => setConfirming(true)}
          >
            Continue
          </Button>
        </>
      }
    >
      <p className="m-0 text-[13.5px] text-text-secondary">
        Snapshot of {objectLabel(object).toLowerCase()}, taken {taken}.
      </p>
      {loadError && <Alert variant="error">{loadError}</Alert>}
      {!info && !loadError && (
        <p className="m-0 text-sm text-text-muted">Loading restore targets…</p>
      )}
      {info?.refusal && (
        <Alert variant="info">
          {RESTORE_REFUSAL[info.refusal] ??
            "A restore from this object cannot be offered."}
        </Alert>
      )}
      {info && !info.refusal && list.length === 0 && (
        <Alert variant="info">
          There is no namespace you can restore into. A restore needs the Rubrik
          restore grant in the namespace it writes into.
        </Alert>
      )}
      {info && list.length > 0 && (
        <div>
          <label className={LABEL_CLASS} htmlFor="rubrik-restore-target">
            Restore into
          </label>
          <select
            id="rubrik-restore-target"
            className={SELECT_CLASS}
            value={target}
            onChange={(e) => setTarget(e.currentTarget.value)}
          >
            {list.map((ns) => (
              <option key={ns} value={ns}>
                {ns}
              </option>
            ))}
          </select>
          {info.inPlaceOnly && (
            <p className="mt-1.5 text-xs text-text-muted">
              A protection-set snapshot restores only into the namespaces the
              set protects.
            </p>
          )}
        </div>
      )}
      {info?.overwriteNotice && list.length > 0 && (
        <Alert variant="warning">{info.overwriteNotice}</Alert>
      )}
      {error && (
        <div role="alert">
          <Alert variant="error">{error}</Alert>
        </div>
      )}
    </Modal>
  );
}
