import { describe, expect, test } from "bun:test";
import {
  CONSUMER_METRICS,
  CONSUMER_SOURCE,
  consumerHref,
  formatConsumerValue,
  rankConsumers,
} from "./top-consumers.ts";

// The Prometheus instant-vector reader behind `top-consumers`.
//
// The widget never writes PromQL (D-8) and never reaches the raw /query route
// (R15): it asks the server-side slug registry for `cluster/top-consumers-cpu`
// or `cluster/top-consumers-memory` and gets back the standard vector
// envelope. Turning that envelope into a ranked list is parsing plus sorting
// plus unit arithmetic -- more than a field read -- so it lives here under
// test rather than inside the component (D-10, KTD8).
//
// Everything below is defensive about its input for the reason the pod
// classifier is: the body comes from Prometheus by way of the Go client's
// marshalling, and a shape this build cannot read must resolve to "we could
// not read it" rather than to an empty list, which on this card would render
// as a cluster where nothing is using anything.

function vector(samples: Array<[string, string, string]>): unknown {
  return {
    resultType: "vector",
    result: samples.map(([namespace, pod, value]) => ({
      metric: { namespace, pod },
      value: [1700000000, value],
    })),
    warnings: null,
  };
}

describe("rankConsumers", () => {
  test("reads an instant vector into a ranked list", () => {
    const view = rankConsumers(
      vector([
        ["prod", "api-1", "0.5"],
        ["prod", "worker-2", "2.25"],
        ["kube-system", "coredns-a", "0.1"],
      ]),
      10,
    );
    expect(view.readable).toBe(true);
    expect(view.rows.map((r) => r.pod)).toEqual([
      "worker-2",
      "api-1",
      "coredns-a",
    ]);
    expect(view.rows[0].value).toBe(2.25);
    expect(view.rows[0].namespace).toBe("prod");
  });

  test("ranks highest first even though topk already sorted", () => {
    // JSON array order is not a contract, and the server is free to change
    // the template. Ranking here rather than trusting the payload is what
    // keeps the top row the top consumer.
    const view = rankConsumers(
      vector([
        ["a", "low", "1"],
        ["a", "high", "9"],
      ]),
      10,
    );
    expect(view.rows.map((r) => r.pod)).toEqual(["high", "low"]);
  });

  test("a tie orders by namespace then pod so refreshes do not reshuffle", () => {
    const view = rankConsumers(
      vector([
        ["b", "x", "1"],
        ["a", "z", "1"],
      ]),
      10,
    );
    expect(view.rows.map((r) => `${r.namespace}/${r.pod}`)).toEqual([
      "a/z",
      "b/x",
    ]);
  });

  test("the list is capped at the caller's limit", () => {
    const view = rankConsumers(
      vector([
        ["a", "1", "5"],
        ["a", "2", "4"],
        ["a", "3", "3"],
      ]),
      2,
    );
    expect(view.rows).toHaveLength(2);
  });

  test("an empty vector is readable and empty, not unreadable", () => {
    // Prometheus is up and answered; there is simply no series. That is a
    // different claim from a body we could not parse, and the card says so.
    const view = rankConsumers(vector([]), 10);
    expect(view.readable).toBe(true);
    expect(view.rows).toEqual([]);
  });

  test("a null or missing body is not readable", () => {
    for (const bad of [null, undefined, 3, "x", {}, { result: [] }]) {
      const view = rankConsumers(bad, 10);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("a result type other than vector is not readable", () => {
    // A matrix here means the query ran as a range query. Rendering its first
    // sample as "current usage" would be a number nobody asked for.
    const view = rankConsumers({ resultType: "matrix", result: [] }, 10);
    expect(view.readable).toBe(false);
  });

  test("samples with an unreadable value are dropped, not zeroed", () => {
    // Prometheus writes NaN as the literal string "NaN". A pod ranked at zero
    // would sit at the bottom of the list looking idle.
    const view = rankConsumers(
      vector([
        ["a", "good", "1.5"],
        ["a", "nan", "NaN"],
        ["a", "junk", "not-a-number"],
      ]),
      10,
    );
    expect(view.rows.map((r) => r.pod)).toEqual(["good"]);
    expect(view.dropped).toBe(2);
  });

  test("a sample with no pod label is dropped", () => {
    const view = rankConsumers(
      {
        resultType: "vector",
        result: [{ metric: { namespace: "a" }, value: [1, "2"] }],
      },
      10,
    );
    expect(view.rows).toEqual([]);
    expect(view.readable).toBe(true);
  });

  test("a numeric sample value is accepted as well as a string", () => {
    // The Go client marshals model.SampleValue as a string; a hand-written
    // proxy or a future change may not.
    const view = rankConsumers(
      {
        resultType: "vector",
        result: [{ metric: { namespace: "a", pod: "p" }, value: [1, 4] }],
      },
      10,
    );
    expect(view.rows[0].value).toBe(4);
  });

  test("Prometheus warnings are carried through", () => {
    const view = rankConsumers(
      { resultType: "vector", result: [], warnings: ["partial data"] },
      10,
    );
    expect(view.warnings).toEqual(["partial data"]);
  });

  test("a null warnings field is an empty list, not a throw", () => {
    expect(rankConsumers(vector([]), 10).warnings).toEqual([]);
  });
});

describe("formatConsumerValue", () => {
  test("CPU below a core reads in millicores", () => {
    expect(formatConsumerValue("cpu", 0.123)).toBe("123m");
    expect(formatConsumerValue("cpu", 0)).toBe("0m");
  });

  test("CPU at or above a core reads in cores", () => {
    expect(formatConsumerValue("cpu", 1)).toBe("1.00");
    expect(formatConsumerValue("cpu", 2.256)).toBe("2.26");
  });

  test("memory arrives in MiB and reads in MiB below a gibibyte", () => {
    // The slug template divides bytes by 1024 twice, so the unit is MiB and
    // not the MB the registry's description calls it.
    expect(formatConsumerValue("memory", 512.4)).toBe("512 MiB");
  });

  test("memory at or above a gibibyte reads in GiB", () => {
    expect(formatConsumerValue("memory", 1024)).toBe("1.0 GiB");
    expect(formatConsumerValue("memory", 3891.2)).toBe("3.8 GiB");
  });

  test("a non-finite value renders an em-dash rather than NaN", () => {
    expect(formatConsumerValue("cpu", Number.NaN)).toBe("—");
    expect(formatConsumerValue("memory", Number.POSITIVE_INFINITY)).toBe("—");
  });
});

describe("the metric toggle", () => {
  test("each metric names exactly one server-owned slug source (R15)", () => {
    expect(CONSUMER_METRICS).toEqual(["cpu", "memory"]);
    expect(CONSUMER_SOURCE).toEqual({
      cpu: "top-consumers-cpu",
      memory: "top-consumers-memory",
    });
  });
});

describe("consumerHref", () => {
  test("a row links to its pod's detail page (R7)", () => {
    expect(consumerHref({ namespace: "prod", pod: "api-1", value: 1 })).toBe(
      "/workloads/pods/prod/api-1",
    );
  });

  test("path segments are encoded rather than interpolated raw", () => {
    expect(consumerHref({ namespace: "a/b", pod: "c d", value: 1 })).toBe(
      "/workloads/pods/a%2Fb/c%20d",
    );
  });
});
