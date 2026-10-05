/**
 * RFC 4122 version 4 UUIDs that work in an insecure context.
 *
 * `crypto.randomUUID` exists only in a secure context (HTTPS or localhost), and
 * is `undefined` on the HTTP-only homelab deployment this repo ships values for
 * (see the same trap recorded in `lib/dashboard/placement.ts`). `getRandomValues`
 * is available everywhere, so the fallback builds the UUID from it. Never
 * `Math.random`: the backend validates tracked-apply operation ids with
 * `uuid.Parse` plus `Version() == 4`, and an id that is also a retry key must
 * come from a CSPRNG.
 */

/** The slice of `Crypto` this module needs; injectable for tests. */
export type UuidCrypto = Partial<Pick<Crypto, "randomUUID">> &
  Pick<Crypto, "getRandomValues">;

/**
 * Returns a version 4 UUID (version nibble 4, variant bits 10xx). Uses
 * `randomUUID` when the runtime has it, else builds one from
 * `getRandomValues`. Throws when neither exists, rather than degrading to a
 * predictable id.
 */
export function uuidv4(c: UuidCrypto | undefined = globalThis.crypto): string {
  if (!c || typeof c.getRandomValues !== "function") {
    throw new Error("No secure random source is available to generate an id");
  }
  if (typeof c.randomUUID === "function") return c.randomUUID();

  const b = c.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const hex = Array.from(b, (x) => x.toString(16).padStart(2, "0"));
  return [
    hex.slice(0, 4).join(""),
    hex.slice(4, 6).join(""),
    hex.slice(6, 8).join(""),
    hex.slice(8, 10).join(""),
    hex.slice(10, 16).join(""),
  ].join("-");
}
