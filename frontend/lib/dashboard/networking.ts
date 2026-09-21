/**
 * Derived state for the three networking cards: per-service golden signals,
 * Hubble flow verdicts, and the Gateway API inventory.
 *
 * Pure: no DOM, no fetch, no signals (D-10). Everything here is a judgement
 * rather than a field read, and every one of those judgements is a place the
 * obvious reading of the payload is wrong in the reassuring direction:
 *
 *  - `/v1/mesh/golden-signals` answers with ZEROS for a query Prometheus
 *    could not serve and names the failed queries alongside them, so the
 *    difference between "no errors" and "nobody could count the errors" is
 *    carried in a separate field that a card reading `errorRate` alone would
 *    never look at.
 *  - A Hubble verdict this build does not recognise is not a forwarded flow.
 *  - A Gateway with no `Programmed` condition has not been programmed.
 *
 * The three views share this module because they share a family and a unit,
 * not because they share logic -- they have almost none. What they do share is
 * the four small readers at the top, which are private here as they are in
 * every other module under this directory. Hoisting them is a real cleanup and
 * a separate one; nothing here is the place to start it.
 */
import { coveragePercent } from "./sync-state.ts";

function obj(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/** A finite number, or null. Deliberately NOT `Number(value) || 0`: a value
 * that is absent, a string or a NaN is not a zero, and the whole point of
 * these three cards is that a zero means something. */
function num(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function list(value: unknown): unknown[] | null {
  return Array.isArray(value) ? value : null;
}

/** Strings from an array field, skipping anything that is not one. */
function strings(value: unknown): string[] {
  const items = list(value);
  if (items === null) return [];
  return items.filter((v): v is string => typeof v === "string");
}

// ---------------------------------------------------------------------------
// Golden signals
// ---------------------------------------------------------------------------

/** The Service detail page, which is where the full golden-signals panel
 * lives. Both values are percent-encoded: this is a stored parameter becoming
 * a URL, and `params.ts` refusing a slash in a service name is the first
 * answer to that, not the only one. */
export function goldenSignalsServiceHref(
  namespace: string,
  service: string,
): string {
  return `/networking/services/${encodeURIComponent(
    namespace,
  )}/${encodeURIComponent(service)}`;
}

/**
 * Which PromQL identifier each rendered signal depends on.
 *
 * The route names the queries it could not answer in `missingQueries`, using
 * the identifiers `goldenSignalQueryNames` declares in
 * backend/internal/servicemesh/metrics.go. It still returns a number for each
 * of them -- Go's zero -- so a card that rendered the number without
 * consulting this list would print "0 req/s" and "0% errors" for a service
 * whose queries Prometheus refused, which is absence reading as good news one
 * level below where the shell can see it (R1).
 *
 * The error rate depends on TWO of them, because it is a ratio the backend
 * divides: either half missing makes the quotient meaningless, and a missing
 * denominator in particular yields a zero rate rather than an error.
 */
const SIGNAL_QUERIES = {
  rps: ["rps"],
  errorRate: ["errorNum", "errorDen"],
  p50: ["p50"],
  p95: ["p95"],
  p99: ["p99"],
} as const;

export interface GoldenSignalsView {
  /** False when the response was not the `{ status, signals }` envelope. */
  readable: boolean;
  namespace: string;
  service: string;
  mesh: string;
  /** False when the metrics subsystem could not be asked at all. */
  available: boolean;
  /** The route's own machine-readable reason, when `available` is false. */
  reason: string;
  rps: number | null;
  /** Whole percent. 0 and 100 are reserved for the absolutes -- see
   * `coveragePercent`, which exists for exactly this rounding problem. */
  errorPercent: number | null;
  p50Ms: number | null;
  p95Ms: number | null;
  p99Ms: number | null;
  /** The PromQL identifiers the route could not answer. */
  missing: string[];
}

function emptyGoldenSignals(readable: boolean): GoldenSignalsView {
  return {
    readable,
    namespace: "",
    service: "",
    mesh: "",
    available: false,
    reason: "",
    rps: null,
    errorPercent: null,
    p50Ms: null,
    p95Ms: null,
    p99Ms: null,
    missing: [],
  };
}

/**
 * The per-service signals, with every number the route could not actually
 * measure reported as absent rather than as zero.
 *
 * Three ways a number goes null, and they are three different sentences on
 * the card: the envelope was unreadable, the metrics backend was unavailable,
 * or that one query failed while the others succeeded. Only the last is
 * partial, and only it leaves the neighbouring numbers standing.
 */
export function goldenSignalsView(data: unknown): GoldenSignalsView {
  const envelope = obj(data);
  if (envelope === null) return emptyGoldenSignals(false);
  const signals = obj(envelope.signals);
  if (signals === null) return emptyGoldenSignals(false);

  const available = signals.available === true;
  const missing = strings(signals.missingQueries);
  const measured = (field: keyof typeof SIGNAL_QUERIES, raw: unknown) => {
    if (!available) return null;
    if (SIGNAL_QUERIES[field].some((q) => missing.includes(q))) return null;
    return num(raw);
  };

  return {
    readable: true,
    namespace: str(signals.namespace),
    service: str(signals.service),
    mesh: str(signals.mesh),
    available,
    reason: str(signals.reason),
    rps: measured("rps", signals.rps),
    errorPercent: coveragePercent(measured("errorRate", signals.errorRate)),
    p50Ms: measured("p50", signals.p50Ms),
    p95Ms: measured("p95", signals.p95Ms),
    p99Ms: measured("p99", signals.p99Ms),
    missing,
  };
}

// ---------------------------------------------------------------------------
// Hubble flows
// ---------------------------------------------------------------------------

export const HUBBLE_FLOWS_PAGE_HREF = "/networking/flows";

/**
 * How many flows one refresh asks Hubble for.
 *
 * Declared here rather than in the fetcher because the card's copy names it:
 * the percentages below are a share of THIS BATCH, not of the namespace's
 * traffic, and a number the request and the sentence took from two places
 * would eventually put a truthful figure under a false label.
 *
 * 500 rather than the route's 100 default and below its 1000 cap. The default
 * makes the blocked share noise on a busy namespace -- a hundred flows out of
 * a hundred thousand -- and the cap costs a gRPC stream drained to five figures
 * on every tick of every dashboard holding the card.
 */
export const HUBBLE_FLOW_BATCH = 500;

/**
 * The verdicts a flow is bucketed into.
 *
 * Four from Hubble plus `unknown`, which is not a Hubble value: it is where a
 * verdict this build does not recognise goes. Folding one into `forwarded`
 * would report unclassifiable traffic as traffic that got through, which is
 * the one direction this card must never round in.
 */
export const FLOW_VERDICTS = [
  "forwarded",
  "dropped",
  "error",
  "audit",
  "unknown",
] as const;
export type FlowVerdict = (typeof FLOW_VERDICTS)[number];

/**
 * The verdicts the card treats as traffic that did not get through.
 *
 * `audit` is deliberately absent. An audit verdict is a policy that WOULD have
 * dropped the flow reporting what it would have done, while the flow was
 * forwarded -- counting it as blocked would report working traffic as broken
 * and send an operator looking for an outage that is a dry run.
 */
const BLOCKED_VERDICTS: readonly FlowVerdict[] = ["dropped", "error"];

function verdictOf(value: unknown): FlowVerdict {
  const raw = str(value).toLowerCase();
  return (FLOW_VERDICTS as readonly string[]).includes(raw) && raw !== "unknown"
    ? (raw as FlowVerdict)
    : "unknown";
}

/**
 * How one end of a flow is named: pod first, then Service, then address.
 *
 * In that order because it is the order of usefulness to whoever reads the
 * card -- a pod name locates the workload, a Service name locates the
 * abstraction, an address locates neither but is all there is for traffic
 * entering or leaving the cluster. An end that answers none of the three is
 * not renderable, and the caller drops the row rather than printing a blank.
 */
function endpoint(flow: Record<string, unknown>, side: "src" | "dst"): string {
  const pod = str(flow[`${side}Pod`]);
  const namespace = str(flow[`${side}Namespace`]);
  if (pod !== "") return namespace === "" ? pod : `${namespace}/${pod}`;
  const service = str(flow[`${side}Service`]);
  if (service !== "") return service;
  return str(flow[`${side}IP`]);
}

export interface FlowRow {
  time: string;
  verdict: FlowVerdict;
  /** Hubble's own drop reason, when it gave one. */
  dropReason: string;
  source: string;
  destination: string;
  protocol: string;
  port: number | null;
}

export interface HubbleFlowsView {
  /** False when the response was not a list. */
  readable: boolean;
  /** Every flow the route returned, including the ones below. */
  total: number;
  counts: Readonly<Record<FlowVerdict, number>>;
  /** Dropped plus errored. Not audited -- see `BLOCKED_VERDICTS`. */
  blocked: number;
  /** Blocked as a whole percent of the batch, or null for an empty batch. */
  blockedPercent: number | null;
  rows: FlowRow[];
  shown: number;
  /** Entries that named neither end and could not be rendered. */
  unreadableRows: number;
}

function emptyFlowCounts(): Record<FlowVerdict, number> {
  return { forwarded: 0, dropped: 0, error: 0, audit: 0, unknown: 0 };
}

function emptyFlowsView(readable: boolean): HubbleFlowsView {
  return {
    readable,
    total: 0,
    counts: emptyFlowCounts(),
    blocked: 0,
    blockedPercent: null,
    rows: [],
    shown: 0,
    unreadableRows: 0,
  };
}

/** RFC 3339 to a sortable number. An unparseable stamp sorts oldest rather
 * than throwing: a flow with a bad timestamp is still a flow. */
function at(value: unknown): number {
  const parsed = Date.parse(str(value));
  return Number.isFinite(parsed) ? parsed : 0;
}

/**
 * One batch of flows, tallied by verdict and ranked worst-first.
 *
 * The batch is a sample, not a census -- the route caps it and the card says
 * so -- which is why the percentage is described as the share OF THIS BATCH
 * rather than of the namespace's traffic.
 *
 * Rows lead with what did not get through, because that is the question a
 * flow card on an overview dashboard answers. When nothing was blocked it
 * shows the traffic that IS flowing instead of an empty panel: "Hubble is
 * watching and everything is getting through" is worth a glance, and it is a
 * different statement from the unavailable state the shell renders when
 * Hubble is not installed at all.
 */
export function hubbleFlowsView(data: unknown, limit: number): HubbleFlowsView {
  const items = list(data);
  if (items === null) return emptyFlowsView(false);

  const counts = emptyFlowCounts();
  const readable: FlowRow[] = [];
  let unreadableRows = 0;

  for (const item of items) {
    const verdict = verdictOf(obj(item)?.verdict);
    counts[verdict]++;

    const flow = obj(item);
    if (flow === null) {
      unreadableRows++;
      continue;
    }
    const source = endpoint(flow, "src");
    const destination = endpoint(flow, "dst");
    if (source === "" || destination === "") {
      unreadableRows++;
      continue;
    }
    readable.push({
      time: str(flow.time),
      verdict,
      dropReason: str(flow.dropReason),
      source,
      destination,
      protocol: str(flow.protocol),
      port: num(flow.dstPort),
    });
  }

  const blocked = BLOCKED_VERDICTS.reduce((sum, v) => sum + counts[v], 0);
  const candidates = readable.filter((r) =>
    BLOCKED_VERDICTS.includes(r.verdict),
  );
  const rows = (candidates.length > 0 ? candidates : readable)
    .slice()
    .sort((a, b) => at(b.time) - at(a.time))
    .slice(0, Math.max(0, limit));

  return {
    readable: true,
    total: items.length,
    counts,
    blocked,
    blockedPercent:
      items.length === 0 ? null : coveragePercent(blocked / items.length),
    rows,
    shown: rows.length,
    unreadableRows,
  };
}

// ---------------------------------------------------------------------------
// Gateway API
// ---------------------------------------------------------------------------

export const GATEWAY_API_PAGE_HREF = "/networking/gateway-api";

/**
 * Whether a Gateway has been programmed, or null when nothing says.
 *
 * `Programmed` is the condition that means the data plane is actually
 * configured; `Accepted` only means the controller took the spec. A Gateway
 * the controller has not touched carries NEITHER, and the absence has to read
 * as unknown rather than as health -- a fresh Gateway with an empty condition
 * list looks exactly like a working one to any check that tests for a False.
 */
function gatewayReady(conditions: unknown): boolean | null {
  const items = list(conditions);
  if (items === null) return null;
  for (const type of ["Programmed", "Accepted"]) {
    for (const item of items) {
      const c = obj(item);
      if (c === null || str(c.type) !== type) continue;
      const status = str(c.status);
      if (status === "True") return true;
      if (status === "False") return false;
    }
  }
  return null;
}

export interface GatewayRow {
  namespace: string;
  name: string;
  className: string;
  listeners: number;
  /** The Gateway's own `status.listeners[].attachedRoutes` total, which is the
   * controller's count across EVERY route kind -- not a count of the
   * HTTPRoutes this card read. */
  attachedRoutes: number;
  /** null when the Gateway carries no Programmed or Accepted condition. */
  ready: boolean | null;
}

export interface GatewayRoutesView {
  /** False unless BOTH reads came back as lists. A card that rendered the
   * gateways it could read beside a route count it could not would report a
   * cluster with no readable routes as a cluster with no routes. */
  readable: boolean;
  gatewayCount: number;
  /** Gateways not known to be programmed, which includes the unknown ones. */
  notReady: number;
  /** HTTPRoutes only. The card's copy says so; see `gateway-httproutes` in
   * types.ts for why the other four kinds are not read. */
  routeCount: number;
  /** HTTPRoutes whose parents name no Gateway in the readable set. */
  unattached: number;
  rows: GatewayRow[];
  /** Entries that named no Gateway and could not be rendered. */
  unreadableRows: number;
}

function emptyGatewayView(readable: boolean): GatewayRoutesView {
  return {
    readable,
    gatewayCount: 0,
    notReady: 0,
    routeCount: 0,
    unattached: 0,
    rows: [],
    unreadableRows: 0,
  };
}

/** Problems first: unprogrammed, then routing nothing, then everything else.
 * An overview card is read top-down and stops early. */
function gatewayRank(row: GatewayRow): number {
  if (row.ready !== true) return 0;
  if (row.attachedRoutes === 0) return 1;
  return 2;
}

/**
 * Gateways and the HTTPRoutes pointed at them.
 *
 * Two numbers that look like they should be one, and are not. Each row's
 * `attachedRoutes` is the Gateway's own status total, written by the
 * controller across every route kind; `routeCount` is the HTTPRoutes this
 * card read. They disagree on a cluster using gRPC or TCP routes, and the
 * card labels each for what it is rather than picking whichever is larger.
 *
 * `unattached` is the finding neither number gives: a route whose parentRef
 * names a Gateway that is not in the set -- deleted, mistyped, or in a
 * namespace the caller cannot read. Its traffic goes nowhere and the route's
 * own page shows it as perfectly well-formed.
 */
export function gatewayRoutesView(
  gateways: unknown,
  routes: unknown,
  limit: number,
): GatewayRoutesView {
  const gwItems = list(gateways);
  const routeItems = list(routes);
  if (gwItems === null || routeItems === null) {
    return emptyGatewayView(false);
  }

  const rows: GatewayRow[] = [];
  const known = new Set<string>();
  let unreadableRows = 0;

  for (const item of gwItems) {
    const gw = obj(item);
    const name = str(gw?.name);
    if (gw === null || name === "") {
      unreadableRows++;
      continue;
    }
    const namespace = str(gw.namespace);
    known.add(`${namespace}/${name}`);
    rows.push({
      namespace,
      name,
      className: str(gw.gatewayClassName),
      listeners: list(gw.listeners)?.length ?? 0,
      attachedRoutes: num(gw.attachedRouteCount) ?? 0,
      ready: gatewayReady(gw.conditions),
    });
  }

  let unattached = 0;
  for (const item of routeItems) {
    const route = obj(item);
    if (route === null) continue;
    const ownNamespace = str(route.namespace);
    const refs = list(route.parentRefs) ?? [];
    const attached = refs.some((r) => {
      const ref = obj(r);
      if (ref === null) return false;
      // An empty kind is Gateway: the field is optional in the Gateway API and
      // defaults to it. A kind that names anything else is a reference to
      // something this card does not track, and counting it as attached would
      // hide a route that reaches no Gateway.
      const kind = str(ref.kind);
      if (kind !== "" && kind !== "Gateway") return false;
      const refName = str(ref.name);
      if (refName === "") return false;
      // An absent parentRef namespace means the route's own, which is the
      // Gateway API's default and the common case. Reading it as the empty
      // namespace instead would report every same-namespace route as an
      // orphan.
      const refNamespace = str(ref.namespace) || ownNamespace;
      return known.has(`${refNamespace}/${refName}`);
    });
    if (!attached) unattached++;
  }

  rows.sort(
    (a, b) =>
      gatewayRank(a) - gatewayRank(b) ||
      a.name.localeCompare(b.name) ||
      a.namespace.localeCompare(b.namespace),
  );

  return {
    readable: true,
    gatewayCount: gwItems.length,
    notReady: rows.filter((r) => r.ready !== true).length,
    routeCount: routeItems.length,
    unattached,
    rows: rows.slice(0, Math.max(0, limit)),
    unreadableRows,
  };
}
