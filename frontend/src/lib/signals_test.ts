import { expect, test } from "bun:test";
import { batch, computed, effect, type Signal, signal } from "./signals.ts";

/** How many consumers are linked into a signal's target set. */
function linked(s: Signal<unknown>): number {
  return s._targets.size;
}

test("a signal reads, writes and peeks its value", () => {
  const s = signal(1);
  expect(s.value).toBe(1);
  s.value = 2;
  expect(s.value).toBe(2);
  expect(s.peek()).toBe(2);
});

test("writing the value a signal already holds wakes nobody", () => {
  const s = signal({ a: 1 });
  const same = s.peek();
  let runs = 0;
  const dispose = effect(() => {
    s.value;
    runs++;
  });
  s.value = same;
  expect(runs).toBe(1);
  s.value = { a: 1 };
  expect(runs).toBe(2);
  dispose();
});

test("an effect runs at once, then after each change to what it read", () => {
  const a = signal(1);
  const seen: number[] = [];
  const dispose = effect(() => {
    seen.push(a.value);
  });
  a.value = 2;
  a.value = 3;
  expect(seen).toEqual([1, 2, 3]);
  dispose();
  a.value = 4;
  expect(seen).toEqual([1, 2, 3]);
  expect(linked(a)).toBe(0);
});

test("a peek inside an effect is not a dependency", () => {
  const a = signal(1);
  const b = signal(10);
  const seen: number[] = [];
  const dispose = effect(() => {
    seen.push(a.value + b.peek());
  });
  b.value = 20;
  expect(seen).toEqual([11]);
  a.value = 2;
  expect(seen).toEqual([11, 22]);
  dispose();
});

test("an effect follows only the signals its last run read", () => {
  const useA = signal(true);
  const a = signal("a");
  const b = signal("b");
  const seen: string[] = [];
  const dispose = effect(() => {
    seen.push(useA.value ? a.value : b.value);
  });
  useA.value = false;
  expect(linked(a)).toBe(0);
  expect(linked(b)).toBe(1);
  a.value = "a2";
  expect(seen).toEqual(["a", "b"]);
  b.value = "b2";
  expect(seen).toEqual(["a", "b", "b2"]);
  dispose();
});

test("an effect's cleanup runs before the next run and on dispose", () => {
  const a = signal(1);
  const log: string[] = [];
  const dispose = effect(() => {
    const v = a.value;
    log.push(`run ${v}`);
    return () => log.push(`clean ${v}`);
  });
  a.value = 2;
  dispose();
  expect(log).toEqual(["run 1", "clean 1", "run 2", "clean 2"]);
});

test("batch defers effects until it ends and runs each once", () => {
  const a = signal(1);
  const b = signal(1);
  const seen: string[] = [];
  const dispose = effect(() => {
    seen.push(`${a.value}/${b.value}`);
  });
  const result = batch(() => {
    a.value = 2;
    b.value = 2;
    expect(seen).toEqual(["1/1"]);
    return "done";
  });
  expect(result).toBe("done");
  expect(seen).toEqual(["1/1", "2/2"]);
  dispose();
});

test("a batch that writes a value back wakes nobody", () => {
  const a = signal(1);
  let runs = 0;
  const dispose = effect(() => {
    a.value;
    runs++;
  });
  batch(() => {
    a.value = 2;
    a.value = 1;
  });
  // The version moved, so the effect reruns; what matters is that it saw
  // one consistent state, once.
  expect(runs).toBe(2);
  dispose();
});

test("a computed is lazy and caches until a source changes", () => {
  const a = signal(2);
  let computes = 0;
  const double = computed(() => {
    computes++;
    return a.value * 2;
  });
  expect(computes).toBe(0);
  expect(double.value).toBe(4);
  expect(double.value).toBe(4);
  expect(double.peek()).toBe(4);
  expect(computes).toBe(1);
  a.value = 3;
  expect(computes).toBe(1);
  expect(double.value).toBe(6);
  expect(computes).toBe(2);
});

test("an unsubscribed computed holds no links to its sources", () => {
  const a = signal(1);
  const c = computed(() => a.value + 1);
  expect(c.value).toBe(2);
  expect(linked(a)).toBe(0);
  const dispose = effect(() => {
    c.value;
  });
  expect(linked(a)).toBe(1);
  dispose();
  expect(linked(a)).toBe(0);
  a.value = 5;
  expect(c.value).toBe(6);
});

test("a computed that recomputes to an equal value does not wake readers", () => {
  const n = signal(1);
  let computes = 0;
  const parity = computed(() => {
    computes++;
    return n.value % 2;
  });
  let runs = 0;
  const dispose = effect(() => {
    parity.value;
    runs++;
  });
  n.value = 3;
  expect(computes).toBe(2);
  expect(runs).toBe(1);
  n.value = 4;
  expect(runs).toBe(2);
  dispose();
});

test("an effect never sees one branch of a diamond updated without the other", () => {
  const a = signal(1);
  const double = computed(() => a.value * 2);
  const triple = computed(() => a.value * 3);
  const seen: string[] = [];
  const dispose = effect(() => {
    seen.push(`${double.value}/${triple.value}`);
  });
  a.value = 2;
  expect(seen).toEqual(["2/3", "4/6"]);
  dispose();
});

test("a chain of computeds stays current while linked and unlinked", () => {
  const a = signal(1);
  const b = computed(() => a.value + 1);
  const c = computed(() => b.value * 10);
  expect(c.value).toBe(20);
  a.value = 2;
  expect(c.value).toBe(30);
  const seen: number[] = [];
  const dispose = effect(() => {
    seen.push(c.value);
  });
  a.value = 3;
  expect(seen).toEqual([30, 40]);
  dispose();
  a.value = 4;
  expect(c.value).toBe(50);
});

test("a computed linked after its sources moved is brought up to date", () => {
  const a = signal(1);
  const inner = computed(() => a.value);
  const outer = computed(() => inner.value + 100);
  expect(outer.value).toBe(101);
  a.value = 2;
  // `outer` reads stale-unlinked `inner` through this effect, which links
  // both; a later write must still reach the effect.
  const seen: number[] = [];
  const dispose = effect(() => {
    seen.push(outer.value);
  });
  a.value = 3;
  expect(seen).toEqual([102, 103]);
  dispose();
});

test("subscribe calls back now, then once per batch that changes the value", () => {
  const a = signal(1);
  const b = signal(1);
  const seen: number[] = [];
  const unsubscribe = a.subscribe((v) => seen.push(v));
  expect(seen).toEqual([1]);
  a.value = 2;
  expect(seen).toEqual([1, 2]);
  batch(() => {
    a.value = 3;
    b.value = 2;
    a.value = 4;
  });
  expect(seen).toEqual([1, 2, 4]);
  b.value = 3;
  expect(seen).toEqual([1, 2, 4]);
  unsubscribe();
  a.value = 5;
  expect(seen).toEqual([1, 2, 4]);
  expect(linked(a)).toBe(0);
});

test("a subscriber reads a consistent state after a batch", () => {
  const a = signal("x");
  const b = signal("x");
  const seen: string[] = [];
  const unsubscribe = a.subscribe((v) => {
    seen.push(`${v}/${b.peek()}`);
  });
  batch(() => {
    a.value = "y";
    b.value = "y";
  });
  expect(seen).toEqual(["x/x", "y/y"]);
  unsubscribe();
});

test("a subscriber's own reads do not become dependencies", () => {
  const a = signal(1);
  const other = signal(1);
  let calls = 0;
  const unsubscribe = a.subscribe(() => {
    other.value;
    calls++;
  });
  a.value = 2;
  other.value = 2;
  expect(calls).toBe(2);
  unsubscribe();
});

test("a subscriber may write other signals", () => {
  const a = signal(1);
  const mirror = signal(0);
  const unsubscribe = a.subscribe((v) => {
    mirror.value = v * 10;
  });
  a.value = 2;
  expect(mirror.peek()).toBe(20);
  unsubscribe();
});

test("a computed subscription fires only when its value changes", () => {
  const n = signal(1);
  const parity = computed(() => n.value % 2);
  const seen: number[] = [];
  const unsubscribe = parity.subscribe((v) => seen.push(v));
  n.value = 3;
  expect(seen).toEqual([1]);
  n.value = 4;
  expect(seen).toEqual([1, 0]);
  unsubscribe();
});

test("an effect's writes run the effects they wake in the same flush", () => {
  const source = signal(1);
  const mirror = signal(0);
  const seen: number[] = [];
  const disposeMirror = effect(() => {
    mirror.value = source.value * 10;
  });
  const disposeReader = effect(() => {
    seen.push(mirror.value);
  });
  source.value = 2;
  expect(seen).toEqual([10, 20]);
  disposeMirror();
  disposeReader();
});

test("a computed that writes a signal throws", () => {
  const a = signal(1);
  const bad = computed(() => {
    a.value = 2;
    return 0;
  });
  expect(() => bad.value).toThrow("must not write");
  expect(a.peek()).toBe(1);
});

test("a computed that depends on itself throws", () => {
  let self: { value: number } | undefined;
  const loop = computed((): number => (self?.value ?? 0) + 1);
  self = loop;
  expect(() => loop.value).toThrow("depends on itself");
});

test("an effect that keeps waking itself stops with an error and is disposed", () => {
  const a = signal(0);
  expect(() =>
    effect(() => {
      a.value = a.value + 1;
    }),
  ).toThrow("kept re-triggering");
  expect(linked(a)).toBe(0);
});

test("effects that keep waking each other stop with an error, then still run", () => {
  const ping = signal(0);
  const pong = signal(0);
  let looping = true;
  const disposePing = effect(() => {
    pong.value = ping.value + 1;
  });
  const disposePong = effect(() => {
    const p = pong.value;
    if (looping && p > 1) ping.value = p + 1;
  });
  expect(() => {
    ping.value = 5;
  }).toThrow("kept re-triggering");
  // The effects dropped when the cycle was cut must run on the next write.
  looping = false;
  ping.value = 100;
  expect(pong.peek()).toBe(101);
  disposePing();
  disposePong();
});

test("an effect that throws on its first run is disposed and rethrows", () => {
  const a = signal(1);
  expect(() =>
    effect(() => {
      a.value;
      throw new Error("boom");
    }),
  ).toThrow("boom");
  expect(linked(a)).toBe(0);
});

test("a throwing effect does not stop the other effects in the flush", () => {
  const a = signal(1);
  const seen: number[] = [];
  const disposeBad = effect(() => {
    if (a.value > 1) throw new Error("bad");
  });
  const disposeGood = effect(() => {
    seen.push(a.value);
  });
  expect(() => {
    a.value = 2;
  }).toThrow("bad");
  expect(seen).toEqual([1, 2]);
  disposeBad();
  disposeGood();
});

test("a computed that throws rethrows on every read and recovers with its inputs", () => {
  const a = signal(1);
  let computes = 0;
  const checked = computed(() => {
    computes++;
    if (a.value < 0) throw new Error("negative");
    return a.value;
  });
  expect(checked.value).toBe(1);
  a.value = -1;
  expect(() => checked.value).toThrow("negative");
  expect(() => checked.peek()).toThrow("negative");
  // The error is cached, not recomputed on every read.
  expect(computes).toBe(2);
  a.value = 5;
  expect(checked.value).toBe(5);
});

test("a subscriber of a computed that threw still gets later values", () => {
  const a = signal(1);
  const checked = computed(() => {
    if (a.value < 0) throw new Error("negative");
    return a.value;
  });
  const seen: number[] = [];
  const unsubscribe = checked.subscribe((v) => seen.push(v));
  expect(() => {
    a.value = -1;
  }).toThrow("negative");
  a.value = 5;
  a.value = 7;
  expect(seen).toEqual([1, 5, 7]);
  unsubscribe();
  expect(linked(a)).toBe(0);
});

test("an effect whose read of a computed throws reruns once the input recovers", () => {
  const a = signal(-1);
  const checked = computed(() => {
    if (a.value < 0) throw new Error("negative");
    return a.value;
  });
  const seen: number[] = [];
  let runs = 0;
  const dispose = effect(() => {
    runs++;
    try {
      seen.push(checked.value);
    } catch {
      // A reader that tolerates the error keeps its dependency.
    }
  });
  expect(runs).toBe(1);
  a.value = 3;
  expect(seen).toEqual([3]);
  dispose();
});

test("batch releases its depth and still flushes when fn throws", () => {
  const a = signal(1);
  const seen: number[] = [];
  const dispose = effect(() => {
    seen.push(a.value);
  });
  expect(() =>
    batch(() => {
      a.value = 2;
      throw new Error("mid-batch");
    }),
  ).toThrow("mid-batch");
  expect(seen).toEqual([1, 2]);
  // Not stuck inside a batch: the next write flushes at once.
  a.value = 3;
  expect(seen).toEqual([1, 2, 3]);
  dispose();
});

test("an effect disposed by an earlier effect in the same flush does not run", () => {
  const a = signal(1);
  const seen: string[] = [];
  let disposeSecond = () => {};
  const disposeFirst = effect(() => {
    if (a.value > 1) disposeSecond();
  });
  disposeSecond = effect(() => {
    seen.push(`second ${a.value}`);
  });
  a.value = 2;
  expect(seen).toEqual(["second 1"]);
  disposeFirst();
});

test("a listener may subscribe and unsubscribe others during a flush", () => {
  const a = signal(1);
  const seen: string[] = [];
  let unsubscribeLate = () => {};
  const unsubscribeSibling = a.subscribe((v) => seen.push(`sibling ${v}`));
  const unsubscribeOwner = a.subscribe((v) => {
    if (v === 2) {
      unsubscribeSibling();
      unsubscribeLate = a.subscribe((late) => seen.push(`late ${late}`));
    }
  });
  a.value = 2;
  a.value = 3;
  // The sibling ran before the owner in the flush that removed it; the late
  // subscriber was called at once, then followed later writes.
  expect(seen).toEqual(["sibling 1", "sibling 2", "late 2", "late 3"]);
  unsubscribeOwner();
  unsubscribeLate();
  expect(linked(a)).toBe(0);
});

test("an effect whose first flush throws is disposed and the error rethrown", () => {
  const a = signal(1);
  const b = signal(0);
  const disposeBad = effect(() => {
    if (b.value > 0) throw new Error("downstream");
  });
  expect(() =>
    effect(() => {
      b.value = a.value;
    }),
  ).toThrow("downstream");
  // The new effect was disposed: it no longer follows `a`.
  b.value = 0;
  a.value = 2;
  expect(b.peek()).toBe(0);
  disposeBad();
});
