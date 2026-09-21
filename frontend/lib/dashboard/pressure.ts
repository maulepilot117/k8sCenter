/**
 * The derived state behind the three reliability widgets: `quota-pressure`,
 * `node-conditions` and `storage-capacity`.
 *
 * All three answer a question their payload has no field for. Which namespace
 * is closest to running out of quota is a ranking over whichever resources
 * each quota happens to constrain -- and one that has to leave out the
 * namespaces with no quota at all, which the limits route returns alongside
 * the rest. Which nodes need attention is four specific conditions out of the
 * dozen a Node object carries. Which volumes are nearly full is a Prometheus
 * instant vector that has to be parsed before it is anything. None of that is
 * a field read, so it lives here under test rather than inside the components
 * (D-10, KTD8).
 *
 * They share a module because they share the judgement: `pressureLevel` is the
 * one place that turns a percentage into ok/warning/critical, and a quota bar
 * and a volume bar that disagreed about what 90% means would be two cards on
 * one dashboard contradicting each other.
 *
 * The direction every function errs in is the one the rest of the dashboard
 * errs in. A payload this build cannot read resolves to "we could not read
 * it", never to an empty list -- an empty list on these three cards reads as a
 * cluster with no quota pressure, no node under pressure and no volume near
 * full, which is exactly the absence-as-good-news this release exists to
 * remove. A node whose Ready condition is missing is reported as not
 * reporting, not as healthy.
 *
 * Total, in the sense the pod classifier is total: every argument may be null,
 * missing or the wrong shape, and none of it throws. One unreadable item
 * degrades to a counter rather than blanking a card describing the readable
 * rest.
 */
import type { PageCoverage } from "./page-coverage.ts";
import { coverage } from "./page-coverage.ts";
import type { ResourceListPage } from "./wire-types.ts";

// --------------------------------------------------------------------------
// Shared
// --------------------------------------------------------------------------

/** How full something is, in the three bands every card in this family uses. */
export type PressureLevel = "ok" | "warning" | "critical";

/**
 * The default bands, as percentages.
 *
 * They are the backend's own defaults for quota (`DefaultWarnThreshold` and
 * `DefaultCriticalThreshold` in backend/internal/limits/types.go, expressed
 * there as fractions and converted to percentages by
 * `ParseThresholdAnnotations`). Reusing them for volumes as well is
 * deliberate: an operator reading one dashboard should not have to remember
 * that amber means 80% on one card and something else on the next.
 *
 * These are NOT pinned cross-language, because nothing here is sent to the
 * server. A quota row arrives already carrying the status the backend computed
 * under whatever `k8scenter.io/warn-threshold` the operator set, and
 * `quotaPressureView` prefers that over these -- see its `level` field.
 */
export const PRESSURE_WARNING_PERCENT = 80;
export const PRESSURE_CRITICAL_PERCENT = 95;

/**
 * The band a percentage falls in, thresholds inclusive.
 *
 * Only ever called with a finite number: a row whose value could not be read
 * is dropped upstream rather than passed through here. That matters, because
 * `NaN >= 95` is false and an unreadable value would therefore come back "ok"
 * -- a green bar for a volume nobody can measure. Dropping it upstream is what
 * keeps that from being representable.
 */
export function pressureLevel(percent: number): PressureLevel {
  if (percent >= PRESSURE_CRITICAL_PERCENT) return "critical";
  if (percent >= PRESSURE_WARNING_PERCENT) return "warning";
  return "ok";
}

function obj(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : null;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/** A finite number, or null. Null rather than a fallback, throughout: a
 * utilization we could not read is not zero, and every consumer below branches
 * on the difference instead of ranking a guess. */
function num(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

// --------------------------------------------------------------------------
// Quota pressure
// --------------------------------------------------------------------------

/** The full page the quota card links to (R7). */
export const QUOTA_PAGE_HREF = "/config/namespace-limits";

/** A row links to its namespace's detail page. The namespace-limits page has
 * no per-namespace route, so the deep link goes to the namespace itself and
 * the card's footer carries the link to the limits page. */
export function namespaceHref(namespace: string): string {
  return `/cluster/namespaces/${encodeURIComponent(namespace)}`;
}

export interface QuotaPressureRow {
  namespace: string;
  /**
   * How close this namespace sits to its quota, as a percentage, across
   * whichever resources the quota actually constrains.
   *
   * This is the backend's `highestUtilization`: the maximum over every entry
   * in every ResourceQuota's `status.hard`. A quota that constrains only
   * `pods` ranks on pods; one that constrains CPU, memory and six object
   * counts ranks on whichever of the eight is tightest. Averaging them would
   * bury the one that is about to start refusing admissions.
   */
  percent: number;
  /**
   * The band, preferring the backend's own verdict.
   *
   * The limits handler computes a status per namespace against the thresholds
   * the operator set on the quota (`k8scenter.io/warn-threshold` and
   * `k8scenter.io/critical-threshold`), so a namespace an operator asked to be
   * warned about at 50% arrives already labelled `warning`. Re-deriving from
   * the defaults would overrule a threshold somebody set on purpose, which is
   * why the payload's status wins when it is one this build recognises.
   */
  level: PressureLevel;
  quotaCount: number;
  /** The CPU and memory dimensions specifically, when the quota constrains
   * them. Null when it does not -- and null is not zero: a quota that says
   * nothing about CPU is not a namespace using no CPU. */
  cpuPercent: number | null;
  memoryPercent: number | null;
}

export interface QuotaPressureView {
  /** Ranked tightest first and capped at the caller's limit. */
  rows: QuotaPressureRow[];
  /**
   * The payload was a readable list.
   *
   * False for a null body or anything that is not an array. Kept apart from an
   * empty `rows` deliberately: a cluster with no quotas anywhere is a real and
   * reportable state, and a body we could not read is not it.
   */
  readable: boolean;
  /** Namespaces in the payload that have at least one quota. The denominator
   * the card reports against. */
  quotaed: number;
  /**
   * Namespaces in the payload with no quota at all.
   *
   * They are in the response -- the handler lists any namespace carrying a
   * quota OR a LimitRange -- and they are NOT in the ranking. A namespace with
   * no quota has no proportion to be close to, and ranking it at zero would
   * assert it is comfortably inside a limit it does not have. Reported here so
   * the card can say how much of the cluster is ungoverned rather than
   * silently dropping them.
   */
  unquotaed: number;
  /** Quota'd namespaces whose pressure could not be read. Counted in
   * `quotaed`, absent from `rows`, reported so the card does not quietly
   * shorten. */
  unreadableRows: number;
  /** Quota'd namespaces at warning or above, counted across the whole payload
   * rather than the capped list, so the headline does not shrink when the list
   * does. */
  atRisk: number;
}

function quotaLevel(status: unknown, percent: number): PressureLevel {
  const s = str(status);
  if (s === "ok" || s === "warning" || s === "critical") return s;
  return pressureLevel(percent);
}

/**
 * `GET /v1/limits/namespaces` to the namespaces closest to their quota.
 *
 * Re-ranked here rather than trusted from the payload. The handler does sort
 * by `highestUtilization` descending, but the response also contains the
 * namespaces this card must not show, the sort is the handler's to change, and
 * the one thing this card must get right is which namespace is at the top.
 */
export function quotaPressureView(
  payload: unknown,
  limit: number,
): QuotaPressureView {
  if (!Array.isArray(payload)) {
    return {
      rows: [],
      readable: false,
      quotaed: 0,
      unquotaed: 0,
      unreadableRows: 0,
      atRisk: 0,
    };
  }

  const rows: QuotaPressureRow[] = [];
  let quotaed = 0;
  let unquotaed = 0;
  let unreadableRows = 0;

  for (const entry of payload) {
    const e = obj(entry);
    const namespace = str(e?.namespace);
    if (e === null || namespace === "") continue;

    if (e.hasQuota !== true) {
      unquotaed++;
      continue;
    }
    quotaed++;

    const percent = num(e.highestUtilization);
    if (percent === null) {
      unreadableRows++;
      continue;
    }

    rows.push({
      namespace,
      percent,
      level: quotaLevel(e.status, percent),
      quotaCount: num(e.quotaCount) ?? 0,
      cpuPercent: num(e.cpuUsedPercent),
      memoryPercent: num(e.memoryUsedPercent),
    });
  }

  rows.sort(
    (a, b) => b.percent - a.percent || a.namespace.localeCompare(b.namespace),
  );

  return {
    rows: rows.slice(0, limit),
    readable: true,
    quotaed,
    unquotaed,
    unreadableRows,
    atRisk: rows.filter((r) => r.level !== "ok").length,
  };
}

// --------------------------------------------------------------------------
// Node conditions
// --------------------------------------------------------------------------

/** The full page the node card links to (R7). */
export const NODE_PAGE_HREF = "/cluster/nodes";

export function nodeHref(name: string): string {
  return `${NODE_PAGE_HREF}/${encodeURIComponent(name)}`;
}

/**
 * The four findings this card reports, worst first.
 *
 * A Node carries a dozen conditions and a cluster with an add-on or two
 * carries more. Listing all of them turns a findings card into a condition
 * dump that happens to be grouped by node, and the full list is already on the
 * node page, which is where an operator goes next. These four are the ones
 * that mean the node cannot be relied on to keep running what is on it: the
 * kubelet reports the three pressures by evicting pods, and a not-ready node
 * is already not taking any.
 *
 * The order is the render order and the ranking's tiebreak, so it is declared
 * once here rather than at each use.
 */
export const NODE_CONDITION_KINDS = [
  "not-ready",
  "memory-pressure",
  "disk-pressure",
  "pid-pressure",
] as const;
export type NodeConditionKind = (typeof NODE_CONDITION_KINDS)[number];

export const NODE_CONDITION_LABEL: Readonly<Record<NodeConditionKind, string>> =
  {
    "not-ready": "Not Ready",
    "memory-pressure": "Memory Pressure",
    "disk-pressure": "Disk Pressure",
    "pid-pressure": "PID Pressure",
  };

/** Which `status.conditions[].type` maps to which pressure finding. A pressure
 * condition is a finding when its status is `True`; `Ready` is the one that
 * inverts, so it is handled apart. */
const PRESSURE_CONDITION: Readonly<Record<string, NodeConditionKind>> = {
  MemoryPressure: "memory-pressure",
  DiskPressure: "disk-pressure",
  PIDPressure: "pid-pressure",
};

export interface NodeIssue {
  name: string;
  /** The findings on this node, in `NODE_CONDITION_KINDS` order. Never
   * empty -- a node with no findings is not a `NodeIssue`. */
  conditions: NodeConditionKind[];
  /**
   * `Ready` was neither `True` nor `False`, or was absent entirely.
   *
   * A different thing from a node that reported itself NotReady, and worth
   * separating: a NotReady node is answering and saying it is unwell, while a
   * silent one has stopped answering and the control plane is reporting the
   * last thing it heard. The operator chasing the first looks at the workload;
   * the one chasing the second looks at the machine.
   */
  silent: boolean;
}

export interface NodeConditionsView extends PageCoverage {
  /** Ranked worst first and capped at the caller's limit. */
  nodes: NodeIssue[];
  /** Nodes on the page with at least one finding, counted across the whole
   * page rather than the capped list. */
  affected: number;
  /** Nodes on the page with none. This is what lets the card say "all 40
   * nodes are ready" instead of rendering an empty list. */
  clear: number;
  /** Page entries that were not readable as a node at all. Reported rather
   * than silently treated as clear, which would be absence as good news. */
  unreadable: number;
}

/** One raw Node object to a finding, or null when it has nothing to report. */
function classifyNode(raw: unknown): NodeIssue | null {
  const node = obj(raw);
  const name = str(obj(node?.metadata)?.name);
  if (node === null || name === "") return null;

  const conditions = obj(node.status)?.conditions;
  const list = Array.isArray(conditions) ? conditions : [];

  const found = new Set<NodeConditionKind>();
  // Absent until proven present. A Node object this build cannot read a Ready
  // condition off is reported as not reporting, not as ready -- a visible
  // claim somebody corrects, rather than an invisible one nobody sees.
  let ready: string | null = null;

  for (const entry of list) {
    const c = obj(entry);
    const type = str(c?.type);
    const status = str(c?.status);
    if (type === "Ready") {
      ready = status;
      continue;
    }
    const kind = PRESSURE_CONDITION[type];
    if (kind !== undefined && status === "True") found.add(kind);
  }

  const silent = ready !== "True" && ready !== "False";
  if (ready !== "True") found.add("not-ready");

  if (found.size === 0) return null;
  return {
    name,
    conditions: NODE_CONDITION_KINDS.filter((k) => found.has(k)),
    silent,
  };
}

/**
 * `GET /v1/resources/nodes` to the nodes that need attention.
 *
 * The route kind is `nodes`, which is both the adapter's `Kind()` and the
 * long resource name, so this one does not have the `hpas`/`pdbs` trap.
 */
export function nodeConditionsView(
  page: ResourceListPage | null | undefined,
  limit: number,
): NodeConditionsView {
  const items = Array.isArray(page?.items) ? page.items : [];

  const issues: NodeIssue[] = [];
  let clear = 0;
  let unreadable = 0;

  for (const raw of items) {
    const node = obj(raw);
    if (node === null || str(obj(node.metadata)?.name) === "") {
      unreadable++;
      continue;
    }
    const issue = classifyNode(node);
    if (issue === null) clear++;
    else issues.push(issue);
  }

  issues.sort(
    (a, b) =>
      Number(b.silent) - Number(a.silent) ||
      // Not-ready outranks any number of pressures: a node that is not taking
      // pods is already not doing its job, where a node under disk pressure is
      // still serving while it evicts.
      Number(b.conditions.includes("not-ready")) -
        Number(a.conditions.includes("not-ready")) ||
      b.conditions.length - a.conditions.length ||
      a.name.localeCompare(b.name),
  );

  return {
    // Counted over the nodes this page carried that were readable as nodes.
    // The route caps a page at 500, and a 900-node cluster's findings card has
    // to say it is describing the first 500.
    ...coverage(page, issues.length + clear),
    nodes: issues.slice(0, limit),
    affected: issues.length,
    clear,
    unreadable,
  };
}

// --------------------------------------------------------------------------
// Storage capacity
// --------------------------------------------------------------------------

/** The full page the storage card links to (R7). The backend has no bare
 * `/storage` overview route -- the family mounts drivers, classes, snapshots,
 * snapshot-classes and presets under its own prefix -- but the frontend does
 * have an overview page, and that is what an operator wants next. */
export const STORAGE_PAGE_HREF = "/storage/overview";

/** A volume row links to its PVC's detail page. Both segments are encoded: a
 * label value off Prometheus is not a value this build controls. */
export function claimHref(row: VolumePressureRow): string {
  return `/storage/pvcs/${encodeURIComponent(row.namespace)}/${encodeURIComponent(row.claim)}`;
}

export interface StorageClassRow {
  name: string;
  provisioner: string;
  isDefault: boolean;
}

export interface VolumePressureRow {
  namespace: string;
  claim: string;
  /** Percent of capacity used. Can exceed 100 -- a filesystem's reserved
   * blocks make `used > capacity` reportable -- and is not clamped here,
   * because a volume that is over is a different finding from one that is
   * exactly full. */
  percent: number;
  level: PressureLevel;
}

export interface StorageCapacityView {
  /** Capped at the caller's limit, in the order the handler sorted them
   * (by name). */
  classes: StorageClassRow[];
  classTotal: number;
  /** The classes read was a readable list. This is the card's REQUIRED half:
   * false here means the card has nothing, and the widget host has usually
   * already resolved that to a loading, error or permission state. */
  classesReadable: boolean;
  /** The class marked default, or null when the cluster has none -- which is
   * worth saying, because a PVC with no `storageClassName` on such a cluster
   * stays Pending forever. */
  defaultClass: string | null;
  /** Ranked fullest first and capped at the caller's limit. */
  volumes: VolumePressureRow[];
  /** Readable samples before the cap. */
  volumeTotal: number;
  /**
   * The slug payload was a readable instant vector.
   *
   * This is the card's OPTIONAL half, and the distinction it turns on. False
   * covers a null body (not fetched yet, or failed) and anything this build
   * cannot parse; true with an empty `volumes` means Prometheus answered and
   * no volume is reporting stats. Collapsing the two would render a
   * Prometheus outage as a cluster where nothing is close to full.
   */
  volumesReadable: boolean;
  /** Samples readable as samples but carrying no claim label or no usable
   * value. Reported so the card can say its ranking is incomplete rather than
   * silently shortening. */
  volumesDropped: number;
  /** Prometheus's own warnings for the query, e.g. partial results from a
   * federated store. Always an array; the field is null on most responses. */
  volumeWarnings: string[];
}

/**
 * The numeric half of a `[timestamp, value]` sample pair.
 *
 * The twin of `sampleValue` in top-consumers.ts, and copied rather than
 * imported because the two parsers differ in the label they key on -- these
 * series carry `persistentvolumeclaim` and no `pod`, so that module's reader
 * would drop every sample. A third Prometheus vector reader is the point at
 * which the parsing should move to a shared module.
 *
 * The Go client marshals `model.SampleValue` as a STRING and writes a NaN as
 * the literal `"NaN"`. `Number("NaN")` is NaN and `Number("")` is 0, so both
 * are rejected explicitly: a volume ranked at zero sits at the bottom of the
 * list looking empty, which is a claim the payload did not make.
 */
function sampleValue(value: unknown): number | null {
  const raw = Array.isArray(value) ? value[1] : undefined;
  if (typeof raw === "number") return Number.isFinite(raw) ? raw : null;
  if (typeof raw !== "string" || raw.trim() === "") return null;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : null;
}

/**
 * The two reads behind `storage-capacity`, composed.
 *
 * There is no single storage overview endpoint to ask, so the card is built
 * from the class inventory the storage family does serve plus the cluster-wide
 * `cluster/storage-capacity` slug -- a named, server-owned PromQL template
 * (D-8, R15), never query text from here. The classes read is required and the
 * slug is optional, so a Prometheus that is down, absent or refused leaves the
 * inventory on screen with the volume half explicitly missing.
 */
export function storageCapacityView(
  classesPayload: unknown,
  capacityPayload: unknown,
  limit: number,
): StorageCapacityView {
  const classesReadable = Array.isArray(classesPayload);
  const classes: StorageClassRow[] = [];
  let defaultClass: string | null = null;

  if (classesReadable) {
    for (const entry of classesPayload) {
      const c = obj(entry);
      const name = str(c?.name);
      if (c === null || name === "") continue;
      const isDefault = c.isDefault === true;
      if (isDefault && defaultClass === null) defaultClass = name;
      classes.push({ name, provisioner: str(c.provisioner), isDefault });
    }
  }

  const volumes: VolumePressureRow[] = [];
  let volumesDropped = 0;
  let volumeWarnings: string[] = [];
  const body = obj(capacityPayload);
  // An instant query, which is what the widget issues: no start/end, so the
  // handler runs `pc.Query` and the result type is a vector. A matrix here
  // means somebody made it a range query, and rendering one sample of it as
  // "how full this volume is" would be a number nobody asked for.
  const volumesReadable =
    body !== null &&
    str(body.resultType) === "vector" &&
    Array.isArray(body.result);

  if (volumesReadable) {
    for (const sample of body.result as unknown[]) {
      const s = obj(sample);
      const metric = obj(s?.metric);
      const claim = str(metric?.persistentvolumeclaim);
      const value = s === null ? null : sampleValue(s.value);
      if (claim === "" || value === null) {
        volumesDropped++;
        continue;
      }
      volumes.push({
        namespace: str(metric?.namespace),
        claim,
        percent: value,
        level: pressureLevel(value),
      });
    }

    volumeWarnings = Array.isArray(body.warnings)
      ? body.warnings.map(str).filter((w) => w !== "")
      : [];
  }

  // Ranked here rather than trusted from the payload. `topk` orders the series
  // server-side, but JSON array order is not a contract and the template is
  // the server's to change -- and the one thing this card must get right is
  // which volume is closest to full.
  volumes.sort(
    (a, b) =>
      b.percent - a.percent ||
      a.namespace.localeCompare(b.namespace) ||
      a.claim.localeCompare(b.claim),
  );

  return {
    classes: classes.slice(0, limit),
    classTotal: classes.length,
    classesReadable,
    defaultClass,
    volumes: volumes.slice(0, limit),
    volumeTotal: volumes.length,
    volumesReadable,
    volumesDropped,
    volumeWarnings,
  };
}
