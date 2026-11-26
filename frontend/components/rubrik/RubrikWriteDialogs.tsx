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

import {
  type MutableRefObject,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
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

/** objectLabel for mid-sentence use: only the kind is lower case, never the name. */
function objectPhrase(o: RubrikObject): string {
  return o.family === "protectionSet"
    ? `protection set ${o.name}`
    : `namespace ${o.name}`;
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
 * The operation id of each request not yet answered, by request key. It is
 * held by the panel, not the dialog, so a request re-sent after the dialog
 * was closed and opened again (say after a network error) keeps its id, and
 * the server answers it from its record instead of running it twice.
 */
export type OperationIds = MutableRefObject<Map<string, string>>;

export function useOperationIds(): OperationIds {
  return useRef(new Map<string, string>());
}

/**
 * Sends one write under the id `ids` holds for its request `key`, minting
 * one the first time. A changed request is a new key and a new id. The id is
 * dropped once the server has answered with an operation.
 */
function useOperationSubmit(
  ids: OperationIds,
  onStarted: (op: RubrikOperation) => void,
) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(
    key: string,
    send: (operationId: string) => Promise<{ data: RubrikOperation }>,
  ): Promise<{ ok: boolean; reason?: string }> {
    let id = ids.current.get(key);
    if (!id) {
      id = uuidv4();
      ids.current.set(key, id);
    }
    setBusy(true);
    setError(null);
    try {
      const res = await send(id);
      ids.current.delete(key);
      onStarted(res.data);
      return { ok: true };
    } catch (err) {
      setError(writeError(err));
      return {
        ok: false,
        reason: err instanceof ApiError ? err.reason : undefined,
      };
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, submit };
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
  // Held in a ref so a parent re-render (a new inline onClose) does not run
  // the effect again and pull focus back to the first field.
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    // Focus returns to what opened the dialog when it closes.
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close.current();
    };
    globalThis.addEventListener("keydown", onKey);
    box.current
      ?.querySelector<HTMLElement>("input, select, button:not([disabled])")
      ?.focus();
    return () => {
      globalThis.removeEventListener("keydown", onKey);
      if (opener?.isConnected) opener.focus();
    };
  }, []);
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
  operationIds,
  onStarted,
}: {
  namespace: string;
  detail: RubrikNamespaceDetail;
  writesEnabled: boolean;
  operationIds: OperationIds;
  onStarted: (op: RubrikOperation) => void;
}) {
  const [open, setOpen] = useState<"snapshot" | "sla" | null>(null);
  if (!writesEnabled) return null;
  const row = detail.namespace;
  const snapshotTargets = row.canSnapshot ? targets(detail, "canSnapshot") : [];
  const slaTargets = row.canChangeSla ? targets(detail, "canChangeSla") : [];

  if (snapshotTargets.length === 0 && slaTargets.length === 0) {
    // The note is for one case only: no object here may be acted on by a
    // non-admin at all (no action target), because what protects the
    // namespace is a set covering several. Missing grants or an operation
    // in flight on a target show no note.
    const managed =
      row.actionTargets.length === 0 &&
      detail.objects.some((o) => !o.relic && multiNamespace(o));
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
          operationIds={operationIds}
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
          operationIds={operationIds}
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
  operationIds,
  onStarted,
  onClose,
}: {
  namespace: string;
  objects: RubrikObject[];
  operationIds: OperationIds;
  onStarted: (op: RubrikOperation) => void;
  onClose: () => void;
}) {
  const [objectId, setObjectId] = useState(
    objects.length === 1 ? objects[0].id : "",
  );
  const [slaId, setSlaId] = useState<string | null>(null);
  // Objects the server said need an SLA picked (sla_required): its view may
  // be newer than the page's. Kept per object, so the picker stays while a
  // retry is sent and does not follow the user to another object.
  const [serverNeedsSla, setServerNeedsSla] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const { busy, error, submit } = useOperationSubmit(operationIds, onStarted);
  const object = objects.find((o) => o.id === objectId);
  // RSC needs an SLA domain for an on-demand snapshot. An object with none
  // takes one picked here.
  const needSla =
    !!object &&
    (!slaProtects(object.effectiveSla) || serverNeedsSla.has(object.id));
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
              void submit(
                `snapshot|${namespace}|${object.id}|${sla}`,
                (operationId) =>
                  apiPost<RubrikOperation>(
                    `/v1/rubrik/namespaces/${encodeURIComponent(namespace)}/snapshots`,
                    {
                      operationId,
                      objectId: object.id,
                      ...(needSla ? { slaId: sla } : {}),
                    },
                  ),
              ).then((res) => {
                if (res.reason === "sla_required") {
                  setServerNeedsSla((prev) => new Set(prev).add(object.id));
                }
              });
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
          The snapshot runs under the SLA domain of {objectPhrase(object)},{" "}
          {slaLabel(object.effectiveSla)}, and expires by its rules.
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
  operationIds,
  onStarted,
  onClose,
}: {
  namespace: string;
  objects: RubrikObject[];
  /** Every object covering the namespace that the user sees. */
  all: RubrikObject[];
  hiddenObjects: boolean;
  operationIds: OperationIds;
  onStarted: (op: RubrikOperation) => void;
  onClose: () => void;
}) {
  const [objectId, setObjectId] = useState(
    objects.length === 1 ? objects[0].id : "",
  );
  // null: nothing chosen yet; "": remove the SLA domain.
  const [slaId, setSlaId] = useState<string | null>(null);
  const { busy, error, submit } = useOperationSubmit(operationIds, onStarted);
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
              void submit(
                `sla|${namespace}|${object.id}|${slaId}`,
                (operationId) =>
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
              New SLA domain for {objectPhrase(object)}
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
                  {others.map(objectPhrase).join(", ")}. Existing snapshots are
                  kept.
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

/**
 * The namespaces a restore into `target` writes into, as the server's
 * restoreInto decides: the target alone for a per-namespace object, every
 * namespace a protection set protects for a set (it restores in place).
 */
function restoreInto(o: RubrikObject, target: string): string[] {
  if (o.family !== "protectionSet") return [target];
  const ns = [...new Set(o.coverage.namespaces ?? [])].sort();
  return ns.includes(target) ? ns : [target];
}

export function RubrikRestoreDialog({
  namespace,
  object,
  snapshot,
  operationIds,
  onStarted,
  onClose,
}: {
  namespace: string;
  object: RubrikObject;
  snapshot: RubrikSnapshot;
  operationIds: OperationIds;
  onStarted: (op: RubrikOperation) => void;
  onClose: () => void;
}) {
  const [info, setInfo] = useState<RubrikRestoreTargets | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [target, setTarget] = useState("");
  const [confirming, setConfirming] = useState(false);
  const { busy, error, submit } = useOperationSubmit(operationIds, onStarted);

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
  const into = target ? restoreInto(object, target) : [];
  const several = into.length > 1;

  if (confirming && target) {
    return (
      <ConfirmDialog
        title="Restore snapshot"
        danger
        confirmLabel={busy ? "Restoring…" : "Restore"}
        loading={busy}
        typeToConfirm={target}
        message={
          several ? (
            <>
              Restore the snapshot of {objectPhrase(object)} taken {taken} in
              place into every namespace the set protects:{" "}
              <strong>{into.join(", ")}</strong>. Existing objects with the same
              names in each of these namespaces may be replaced. Type {target}{" "}
              to confirm.
            </>
          ) : (
            <>
              Restore the snapshot of {objectPhrase(object)} taken {taken} into{" "}
              <strong>{target}</strong>. {info?.overwriteNotice} Type the
              namespace name to confirm.
            </>
          )
        }
        onConfirm={() => {
          void submit(
            `restore|${object.id}|${snapshot.id}|${target}`,
            (operationId) =>
              apiPost<RubrikOperation>(RESTORES_URL, {
                operationId,
                objectId: object.id,
                snapshotId: snapshot.id,
                targetNamespace: target,
              }),
          ).then((res) => {
            if (!res.ok) setConfirming(false);
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
        Snapshot of {objectPhrase(object)}, taken {taken}.
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
      {several ? (
        <Alert variant="warning">
          This restores in place into every namespace the set protects:{" "}
          {into.join(", ")}. Existing objects with the same names in each of
          them may be replaced.
        </Alert>
      ) : (
        info?.overwriteNotice &&
        list.length > 0 && (
          <Alert variant="warning">{info.overwriteNotice}</Alert>
        )
      )}
      {error && (
        <div role="alert">
          <Alert variant="error">{error}</Alert>
        </div>
      )}
    </Modal>
  );
}
