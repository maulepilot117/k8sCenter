/**
 * The shared-state primitive behind every store module (cluster, namespace,
 * auth, pins, counts, layout, toasts, the dashboard data sources).
 *
 * A small in-repo replacement for `@preact/signals-core`, keeping the part of
 * its API the stores use: `signal`, `computed`, `effect` and `batch`, and on a
 * signal `.value`, `.peek()` and `.subscribe()`. It is framework-free; a
 * component reads a store only through `useSignalValue`, which hands
 * `subscribe` and `peek` to React's `useSyncExternalStore`.
 *
 * How it works:
 *
 * - Reading `.value` inside a `computed` or `effect` records the signal as a
 *   dependency of that consumer. `.peek()` reads without recording.
 * - Every source carries a version that moves only when its value changes
 *   (`Object.is`). A consumer remembers the version it read and reruns only
 *   when one of them moved, so writing the same value wakes nobody, and a
 *   `computed` that recomputes to an equal value does not wake its readers.
 * - A write marks dependants stale and queues effects; effects run when the
 *   outermost `batch` (every write is its own batch) ends. A `computed` is
 *   recomputed lazily, when read, so an effect never sees one source updated
 *   beside another that is not.
 * - A `computed` is linked into its sources only while something is
 *   subscribed to it. An unsubscribed one holds no links, so nothing keeps it
 *   alive, and it revalidates on read instead.
 */

/** A signal-like value that can be read and subscribed to, not written. */
export interface ReadonlySignal<T> {
  /** The current value; inside a `computed` or `effect`, also a dependency. */
  readonly value: T;
  /** The current value, without recording a dependency. */
  peek(): T;
  /**
   * Calls `listener` with the current value now, and again with the new
   * value after each change, once the batch that made it has ended. Signals
   * the listener reads are not dependencies. Returns the unsubscribe
   * function.
   */
  subscribe(listener: (value: T) => void): () => void;
}

interface Source {
  _version: number;
  _refresh(): void;
  _addTarget(target: Consumer): void;
  _removeTarget(target: Consumer): void;
}

interface Consumer {
  /** Each source read in the last run, and the version it had then. */
  _sources: Map<Source, number>;
  /** Whether the consumer is linked into its sources' target sets. */
  _isLinked(): boolean;
  _notify(): void;
}

/** The consumer whose run is in progress, which reads are recorded against. */
let tracking: Consumer | null = null;
let batchDepth = 0;
/** Bumped on every signal write; lets an unlinked computed skip revalidation. */
let globalVersion = 0;
let pendingEffects: Effect[] = [];

/** Rounds of effects one batch may run before it is treated as a cycle. */
const MAX_EFFECT_ROUNDS = 100;

function track(source: Source): void {
  const consumer = tracking;
  if (consumer === null || consumer._sources.has(source)) return;
  consumer._sources.set(source, source._version);
  // Link at once rather than after the run, so a write the run itself makes
  // to a source it has already read still reaches it.
  if (consumer._isLinked()) source._addTarget(consumer);
}

/** Unlinks a consumer from the sources its previous run read and this one did not. */
function unlinkDropped(
  consumer: Consumer,
  previous: Map<Source, number>,
): void {
  if (!consumer._isLinked()) return;
  for (const source of previous.keys()) {
    if (!consumer._sources.has(source)) source._removeTarget(consumer);
  }
}

/** Whether any source moved since the consumer last read it. */
function sourcesChanged(consumer: Consumer): boolean {
  for (const [source, seen] of consumer._sources) {
    source._refresh();
    if (source._version !== seen) return true;
  }
  return false;
}

function startBatch(): void {
  batchDepth++;
}

function endBatch(): void {
  if (batchDepth > 1) {
    batchDepth--;
    return;
  }
  // Effects run while the depth is still 1, so their own writes queue into
  // the next round instead of flushing re-entrantly.
  let error: unknown;
  let failed = false;
  try {
    for (let round = 0; pendingEffects.length > 0; round++) {
      if (round === MAX_EFFECT_ROUNDS) {
        // Unmark what is dropped, or `_notify` would treat it as still
        // queued and never schedule it again.
        for (const dropped of pendingEffects) dropped._queued = false;
        pendingEffects = [];
        throw new Error("signals: effects kept re-triggering each other");
      }
      const queued = pendingEffects;
      pendingEffects = [];
      for (const effect of queued) {
        effect._queued = false;
        try {
          effect._run();
        } catch (err) {
          if (!failed) {
            failed = true;
            error = err;
          }
        }
      }
    }
  } finally {
    batchDepth--;
  }
  if (failed) throw error;
}

/**
 * Runs `fn` with every write inside it deferred: effects and subscribers run
 * once, after `fn` returns, and see all of its writes together.
 */
export function batch<T>(fn: () => T): T {
  startBatch();
  try {
    return fn();
  } finally {
    endBatch();
  }
}

function untracked<T>(fn: () => T): T {
  const previous = tracking;
  tracking = null;
  try {
    return fn();
  } finally {
    tracking = previous;
  }
}

function subscribeTo<T>(
  source: ReadonlySignal<T>,
  listener: (value: T) => void,
): () => void {
  return effect(() => {
    const value = source.value;
    untracked(() => listener(value));
  });
}

/** A writable value. Create one with `signal()`. */
export class Signal<T> implements ReadonlySignal<T>, Source {
  _value: T;
  _version = 0;
  _targets = new Set<Consumer>();

  constructor(value: T) {
    this._value = value;
  }

  get value(): T {
    track(this);
    return this._value;
  }

  set value(next: T) {
    if (tracking instanceof Computed) {
      throw new Error("signals: a computed must not write a signal");
    }
    if (Object.is(next, this._value)) return;
    this._value = next;
    this._version++;
    globalVersion++;
    startBatch();
    try {
      for (const target of [...this._targets]) target._notify();
    } finally {
      endBatch();
    }
  }

  peek(): T {
    return this._value;
  }

  subscribe(listener: (value: T) => void): () => void {
    return subscribeTo(this, listener);
  }

  _refresh(): void {
    // A signal is always current.
  }

  _addTarget(target: Consumer): void {
    this._targets.add(target);
  }

  _removeTarget(target: Consumer): void {
    this._targets.delete(target);
  }
}

/** A value derived from other signals. Create one with `computed()`. */
class Computed<T> implements ReadonlySignal<T>, Source, Consumer {
  _fn: () => T;
  _value: T | undefined = undefined;
  /**
   * What the last run of `fn` threw, held until a later run succeeds. Like
   * signals-core, a throw is a result: it is cached and rethrown on every
   * read, it moves the version, and the computed keeps passing notifications
   * on, so readers recover once its inputs do.
   */
  _error: unknown = undefined;
  _hasError = false;
  /** Whether `fn` has run to a result (a value or an error) at least once. */
  _ran = false;
  _version = 0;
  _targets = new Set<Consumer>();
  _sources = new Map<Source, number>();
  /** Linked and told a source changed since it last validated. */
  _stale = true;
  /** `globalVersion` when it last validated; equal means nothing was written since. */
  _validatedAt = -1;
  _computing = false;

  constructor(fn: () => T) {
    this._fn = fn;
  }

  get value(): T {
    this._refresh();
    track(this);
    return this._result();
  }

  peek(): T {
    this._refresh();
    return this._result();
  }

  _result(): T {
    if (this._hasError) throw this._error;
    return this._value as T;
  }

  subscribe(listener: (value: T) => void): () => void {
    return subscribeTo(this, listener);
  }

  _isLinked(): boolean {
    return this._targets.size > 0;
  }

  _notify(): void {
    if (this._stale) return;
    this._stale = true;
    for (const target of [...this._targets]) target._notify();
  }

  _refresh(): void {
    if (this._computing) {
      throw new Error("signals: a computed depends on itself");
    }
    if (this._ran) {
      if (this._isLinked() && !this._stale) return;
      if (this._validatedAt === globalVersion) {
        this._stale = false;
        return;
      }
      if (!this._recheck()) {
        this._validatedAt = globalVersion;
        this._stale = false;
        return;
      }
    }
    this._recompute();
  }

  /** `sourcesChanged`, with the computed marked busy so a cycle is caught. */
  _recheck(): boolean {
    this._computing = true;
    try {
      return sourcesChanged(this);
    } finally {
      this._computing = false;
    }
  }

  _recompute(): void {
    const previous = this._sources;
    this._sources = new Map();
    const outer = tracking;
    tracking = this;
    this._computing = true;
    try {
      const next = this._fn();
      if (!this._ran || this._hasError || !Object.is(next, this._value)) {
        this._value = next;
        this._version++;
      }
      this._hasError = false;
      this._error = undefined;
    } catch (err) {
      this._value = undefined;
      this._error = err;
      this._hasError = true;
      this._version++;
    } finally {
      this._ran = true;
      this._validatedAt = globalVersion;
      this._stale = false;
      this._computing = false;
      tracking = outer;
      unlinkDropped(this, previous);
    }
  }

  _addTarget(target: Consumer): void {
    const linking = this._targets.size === 0;
    this._targets.add(target);
    if (!linking) return;
    // Becoming linked: link into the sources, then bring the value up to
    // date. No notification reached it while it was unlinked, and a linked
    // computed that is stale passes no notification on until it is read.
    for (const source of this._sources.keys()) source._addTarget(this);
    this._stale = true;
    this._refresh();
  }

  _removeTarget(target: Consumer): void {
    if (!this._targets.delete(target) || this._targets.size > 0) return;
    for (const source of this._sources.keys()) source._removeTarget(this);
  }
}

/** An effect body; it may return a cleanup to run before its next run. */
type EffectFn = () => undefined | (() => void);

class Effect implements Consumer {
  _fn: EffectFn;
  _sources = new Map<Source, number>();
  _cleanup: (() => void) | undefined = undefined;
  _ran = false;
  _queued = false;
  _disposed = false;

  constructor(fn: EffectFn) {
    this._fn = fn;
  }

  _isLinked(): boolean {
    return !this._disposed;
  }

  _notify(): void {
    if (this._queued || this._disposed) return;
    this._queued = true;
    pendingEffects.push(this);
  }

  _run(): void {
    if (this._disposed) return;
    if (this._ran && !sourcesChanged(this)) return;
    this._ran = true;
    this._runCleanup();
    const previous = this._sources;
    this._sources = new Map();
    const outer = tracking;
    tracking = this;
    startBatch();
    try {
      const cleanup = this._fn();
      this._cleanup = typeof cleanup === "function" ? cleanup : undefined;
    } finally {
      tracking = outer;
      unlinkDropped(this, previous);
      endBatch();
    }
  }

  _runCleanup(): void {
    const cleanup = this._cleanup;
    this._cleanup = undefined;
    if (cleanup) untracked(cleanup);
  }

  _dispose(): void {
    if (this._disposed) return;
    this._disposed = true;
    for (const source of this._sources.keys()) source._removeTarget(this);
    this._sources.clear();
    this._runCleanup();
  }
}

/** Creates a writable signal holding `value`. */
export function signal<T>(value: T): Signal<T> {
  return new Signal(value);
}

/**
 * Creates a read-only signal whose value is `fn()`, recomputed when a signal
 * it read changes. `fn` must not write signals.
 */
export function computed<T>(fn: () => T): ReadonlySignal<T> {
  return new Computed(fn);
}

/**
 * Runs `fn` now, and again after any signal it read changes. `fn` may return
 * a cleanup, which runs before the next run and on dispose. Returns the
 * dispose function. If the first run, or the flush it starts, throws, the
 * effect is disposed and the error rethrown.
 */
export function effect(fn: EffectFn): () => void {
  const e = new Effect(fn);
  try {
    batch(() => e._run());
  } catch (err) {
    // The caller never receives a dispose function, so nothing else could
    // stop this effect.
    e._dispose();
    throw err;
  }
  return () => e._dispose();
}
