/**
 * Persistence for an unknown-outcome tracked apply (see `useYamlApply`).
 *
 * When a tracked apply's response is lost, the operator's retry must reuse the
 * same operation id, even across a remount, reload or page navigation. The id
 * is mirrored to `sessionStorage` under a SHA-256 of the request key.
 *
 * What is stored: the id, the time it was first sent, and the digest. Never
 * the request key itself: it contains the submitted manifest, which may carry
 * Secret values, and the backend takes pains never to store a manifest. The
 * digest is SHA-256 rather than a short hash so two different requests cannot
 * share an entry, and it is computed in plain JS because `crypto.subtle`, like
 * `crypto.randomUUID`, does not exist on an HTTP-only deployment.
 *
 * Every storage access is guarded: with storage unavailable (private window,
 * blocked site data, SSR) the hook keeps its in-memory behaviour.
 */

/** An apply whose outcome is unknown, and the request its id was minted for. */
export interface PendingAttempt {
  id: string;
  /** Identifies the request: every input it carries, including the caller. */
  requestKey: string;
  /** When the id was first sent (epoch ms); bounds how long it is reusable. */
  at: number;
}

/**
 * How long an unknown-outcome id stays reusable. It matches the backend's
 * orphan-reconciliation grace: past it a receipt left open has been settled,
 * and re-applying the same content is a deliberate new change, not a retry.
 */
export const PENDING_ATTEMPT_TTL_MS = 10 * 60 * 1000;

/** Every pending-apply entry lives under this prefix. */
export const PENDING_STORAGE_PREFIX = "kubecenter:pending-apply:";

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

const rotr = (x: number, n: number) => (x >>> n) | (x << (32 - n));

/** SHA-256 of the UTF-8 encoding of `input`, as lowercase hex. Synchronous. */
export function sha256Hex(input: string): string {
  const msg = new TextEncoder().encode(input);
  const len = msg.length;
  const padded = new Uint8Array(((len + 9 + 63) >> 6) << 6);
  padded.set(msg);
  padded[len] = 0x80;
  const view = new DataView(padded.buffer);
  view.setUint32(padded.length - 8, Math.floor((len * 8) / 0x100000000));
  view.setUint32(padded.length - 4, (len * 8) >>> 0);

  const h = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
    0x1f83d9ab, 0x5be0cd19,
  ]);
  const w = new Uint32Array(64);
  for (let off = 0; off < padded.length; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(off + i * 4);
    for (let i = 16; i < 64; i++) {
      const s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >>> 3);
      const s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >>> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    let [a, b, c, d, e, f, g, hh] = h;
    for (let i = 0; i < 64; i++) {
      const t1 =
        hh +
        (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) +
        ((e & f) ^ (~e & g)) +
        K[i] +
        w[i];
      const t2 =
        (rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) +
        ((a & b) ^ (a & c) ^ (b & c));
      hh = g;
      g = f;
      f = e;
      e = (d + t1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (t1 + t2) >>> 0;
    }
    h[0] += a;
    h[1] += b;
    h[2] += c;
    h[3] += d;
    h[4] += e;
    h[5] += f;
    h[6] += g;
    h[7] += hh;
  }
  return Array.from(h, (x) => x.toString(16).padStart(8, "0")).join("");
}

/**
 * The unknown-outcome id stored for this request, if one is still within its
 * window and was stored for exactly this request. An expired, malformed or
 * mismatched entry is removed.
 */
export function readStoredAttempt(
  requestKey: string,
  now: number,
): PendingAttempt | null {
  const digest = sha256Hex(requestKey);
  const storageKey = PENDING_STORAGE_PREFIX + digest;
  try {
    const raw = globalThis.sessionStorage.getItem(storageKey);
    if (raw === null) return null;
    let parsed: unknown = null;
    try {
      parsed = JSON.parse(raw);
    } catch {
      // Corrupt entry: removed below.
    }
    if (parsed !== null && typeof parsed === "object") {
      const { id, at, digest: stored } = parsed as Record<string, unknown>;
      if (
        typeof id === "string" &&
        id !== "" &&
        typeof at === "number" &&
        stored === digest &&
        now - at >= 0 &&
        now - at < PENDING_ATTEMPT_TTL_MS
      ) {
        return { id, requestKey, at };
      }
    }
    globalThis.sessionStorage.removeItem(storageKey);
  } catch {
    // Storage unavailable or corrupt: fall back to memory only.
  }
  return null;
}

/** Persists an attempt so a remount can continue it. */
export function writeStoredAttempt(a: PendingAttempt): void {
  const digest = sha256Hex(a.requestKey);
  try {
    globalThis.sessionStorage.setItem(
      PENDING_STORAGE_PREFIX + digest,
      JSON.stringify({ id: a.id, at: a.at, digest }),
    );
  } catch {
    // Storage unavailable: the in-memory attempt still covers this mount.
  }
}

/** Forgets the stored attempt for this request. */
export function clearStoredAttempt(requestKey: string): void {
  try {
    globalThis.sessionStorage.removeItem(
      PENDING_STORAGE_PREFIX + sha256Hex(requestKey),
    );
  } catch {
    // Storage unavailable: nothing was persisted.
  }
}

/**
 * Forgets every stored attempt. Called on logout: an id minted for one
 * identity must not outlive that identity's session.
 */
export function clearPendingApplies(): void {
  try {
    const storage = globalThis.sessionStorage;
    const doomed: string[] = [];
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i);
      if (key?.startsWith(PENDING_STORAGE_PREFIX)) doomed.push(key);
    }
    for (const key of doomed) storage.removeItem(key);
  } catch {
    // Storage unavailable: nothing was persisted.
  }
}
