/**
 * The Prometheus instant-vector reader behind the `top-consumers` widget.
 *
 * The widget never writes PromQL and never carries a URL (D-8), and it never
 * reaches the raw `/monitoring/query` routes, which are admin-gated: it asks
 * the server-side slug registry for `cluster/top-consumers-cpu` or
 * `cluster/top-consumers-memory` and gets the standard vector envelope back
 * (R15). Turning that envelope into a ranked list is parsing, sorting and unit
 * arithmetic -- more than a field read -- so it lives here under test rather
 * than inside the component (D-10, KTD8).
 *
 * Defensive about its input for the reason the pod classifier is. The body
 * comes from Prometheus by way of the Go client's marshalling, and a shape
 * this build cannot read has to resolve to "we could not read it" rather than
 * to an empty list -- which on this card would render as a cluster where
 * nothing is using anything.
 */
import type { DataSourceKey } from "./types.ts";

/**
 * The two rankings the card switches between.
 *
 * A DISPLAY choice, not a stored parameter. A parameter would mean either a
 * second catalog entry or a `params` declaration, and both would put a view
 * toggle into the saved layout and into the server's per-widget parameter
 * validation -- for something that is a button. The widget declares both
 * sources and renders one; nothing about the toggle is persisted.
 */
export const CONSUMER_METRICS = ["cpu", "memory"] as const;
export type ConsumerMetric = (typeof CONSUMER_METRICS)[number];

/**
 * Which server-owned slug each metric reads.
 *
 * The mapping is here rather than in the component so the component holds no
 * route knowledge at all, and so a metric added without a slug is a type
 * error rather than an empty card.
 */
export const CONSUMER_SOURCE: Readonly<Record<ConsumerMetric, DataSourceKey>> =
  {
    cpu: "top-consumers-cpu",
    memory: "top-consumers-memory",
  };

/** Display labels for the toggle. */
export const CONSUMER_LABEL: Readonly<Record<ConsumerMetric, string>> = {
  cpu: "CPU",
  memory: "Memory",
};

export interface ConsumerRow {
  namespace: string;
  pod: string;
  /** Cores for `cpu`, mebibytes for `memory` -- the units the slug templates
   * produce. Always finite; unreadable samples never become rows. */
  value: number;
}

export interface TopConsumersView {
  /** Ranked highest first and capped at the caller's limit. */
  rows: ConsumerRow[];
  /**
   * The payload was a readable instant vector.
   *
   * False for a null body, a different `resultType`, or anything this build
   * cannot parse. Kept apart from an empty `rows` deliberately: Prometheus
   * answering with no series means nothing is running, and a body we could
   * not read means nothing is known. Collapsing the two would render the
   * second as the first, which is the "absence as good news" this release
   * exists to remove.
   */
  readable: boolean;
  /** Samples that were readable as samples but carried no usable value or no
   * pod label. Reported so the card can say its ranking is incomplete rather
   * than silently shortening. */
  dropped: number;
  /** Prometheus's own warnings for the query, e.g. partial results from a
   * federated store. Always an array; the field is null on most responses. */
  warnings: string[];
}

const UNREADABLE: TopConsumersView = {
  rows: [],
  readable: false,
  dropped: 0,
  warnings: [],
};

function obj(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : null;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/**
 * The numeric half of a `[timestamp, value]` sample pair.
 *
 * The Go client marshals `model.SampleValue` as a STRING, and writes a NaN as
 * the literal `"NaN"`. `Number("NaN")` is NaN and `Number("")` is 0, so both
 * are rejected explicitly: a pod ranked at zero sits at the bottom of the
 * list looking idle, which is a claim the payload did not make.
 */
function sampleValue(value: unknown): number | null {
  const raw = Array.isArray(value) ? value[1] : undefined;
  if (typeof raw === "number") return Number.isFinite(raw) ? raw : null;
  if (typeof raw !== "string" || raw.trim() === "") return null;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : null;
}

/**
 * The envelope from `GET /v1/monitoring/queries/cluster/top-consumers-*` to
 * the rows the card renders.
 *
 * Ranked here rather than trusted from the payload. `topk` orders the series
 * server-side, but JSON array order is not a contract and the template is the
 * server's to change -- and the one thing this card must get right is which
 * pod is at the top.
 */
export function rankConsumers(
  payload: unknown,
  limit: number,
): TopConsumersView {
  const body = obj(payload);
  if (body === null) return UNREADABLE;
  // An instant query, which is what the widget issues: no start/end, so the
  // handler runs `pc.Query` and the result type is a vector. A matrix here
  // means somebody made it a range query, and rendering one sample of it as
  // "current usage" would be a number nobody asked for.
  if (str(body.resultType) !== "vector") return UNREADABLE;
  if (!Array.isArray(body.result)) return UNREADABLE;

  const rows: ConsumerRow[] = [];
  let dropped = 0;
  for (const sample of body.result) {
    const s = obj(sample);
    const metric = obj(s?.metric);
    const pod = str(metric?.pod);
    const value = s === null ? null : sampleValue(s.value);
    if (pod === "" || value === null) {
      dropped++;
      continue;
    }
    rows.push({ namespace: str(metric?.namespace), pod, value });
  }

  rows.sort(
    (a, b) =>
      b.value - a.value ||
      a.namespace.localeCompare(b.namespace) ||
      a.pod.localeCompare(b.pod),
  );

  const warnings = Array.isArray(body.warnings)
    ? body.warnings.map(str).filter((w) => w !== "")
    : [];

  return { rows: rows.slice(0, limit), readable: true, dropped, warnings };
}

/**
 * One row's value in the units its metric actually carries.
 *
 * CPU is cores. Below one core it reads in millicores, because `0.12` on a
 * dashboard is harder to compare at a glance than `120m` and millicores are
 * the unit every `resources.requests` on the cluster is written in.
 *
 * Memory arrives in MEBIBYTES, not megabytes: the slug template divides bytes
 * by 1024 twice, whatever the registry's description calls it. Labelling it MB
 * would be off by 5% and would not match what `kubectl top` prints beside it.
 *
 * A non-finite value renders an em-dash rather than "NaN". `rankConsumers`
 * never produces one, so this is the belt to its braces.
 */
export function formatConsumerValue(
  metric: ConsumerMetric,
  value: number,
): string {
  if (!Number.isFinite(value)) return "—";
  if (metric === "cpu") {
    if (value < 1) return `${Math.round(value * 1000)}m`;
    return value.toFixed(2);
  }
  if (value >= 1024) return `${(value / 1024).toFixed(1)} GiB`;
  return `${Math.round(value)} MiB`;
}

/** The unit `formatConsumerValue` leaves off the CPU reading at or above one
 * core, so the card can print it once in a column header instead of on every
 * row. */
export const CONSUMER_UNIT: Readonly<Record<ConsumerMetric, string>> = {
  cpu: "cores",
  memory: "working set",
};

/** A row links to its pod's detail page (R7). Both segments are encoded: a
 * label value off Prometheus is not a value this build controls. */
export function consumerHref(row: ConsumerRow): string {
  return `/workloads/pods/${encodeURIComponent(row.namespace)}/${encodeURIComponent(row.pod)}`;
}
