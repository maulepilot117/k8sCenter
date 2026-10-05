import { expect, test } from "bun:test";
import { sameOperationId, uuidv4 } from "./uuid.ts";

const V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

/** A Crypto with no randomUUID, like a non-secure context. */
function insecureCrypto(fill: (b: Uint8Array) => void) {
  return {
    getRandomValues: <T extends ArrayBufferView | null>(array: T): T => {
      fill(array as unknown as Uint8Array);
      return array;
    },
  };
}

test("uses randomUUID when the runtime has it", () => {
  const c = {
    randomUUID: () => "11111111-2222-4333-8444-555555555555" as const,
    getRandomValues: <T extends ArrayBufferView | null>(a: T): T => a,
  };
  expect(uuidv4(c)).toBe("11111111-2222-4333-8444-555555555555");
});

test("randomUUID output is lowercased", () => {
  const c = {
    randomUUID: () => "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE" as const,
    getRandomValues: <T extends ArrayBufferView | null>(a: T): T => a,
  };
  expect(uuidv4(c)).toBe("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee");
});

test("sameOperationId ignores case and rejects blanks", () => {
  const id = "6f1d3c52-4b1e-4f0a-9c53-0d7a2b8e1f64";
  expect(sameOperationId(id, id.toUpperCase())).toBe(true);
  expect(sameOperationId(id.toUpperCase(), id)).toBe(true);
  expect(sameOperationId(id, "0b6a8d21-77c4-4d5e-8a30-5e1c9f2b4d07")).toBe(
    false,
  );
  expect(sameOperationId(id, null)).toBe(false);
  expect(sameOperationId(undefined, id)).toBe(false);
  expect(sameOperationId("", "")).toBe(false);
});

test("the default source yields a v4 UUID", () => {
  expect(uuidv4()).toMatch(V4);
  expect(uuidv4()).not.toBe(uuidv4());
});

test("without randomUUID it builds a v4 UUID from getRandomValues", () => {
  const id = uuidv4(insecureCrypto((b) => b.fill(0xff)));
  // 0xff everywhere: version nibble forced to 4, variant bits to 10xx.
  expect(id).toBe("ffffffff-ffff-4fff-bfff-ffffffffffff");
  expect(id).toMatch(V4);
});

test("the fallback forces version and variant on all-zero input", () => {
  const id = uuidv4(insecureCrypto((b) => b.fill(0)));
  expect(id).toBe("00000000-0000-4000-8000-000000000000");
  expect(id).toMatch(V4);
});

test("the fallback is random per call and always v4-shaped", () => {
  const real = globalThis.crypto;
  const c = insecureCrypto((b) => real.getRandomValues(b));
  const ids = new Set<string>();
  for (let i = 0; i < 200; i++) {
    const id = uuidv4(c);
    expect(id).toMatch(V4);
    ids.add(id);
  }
  expect(ids.size).toBe(200);
});

test("with no random source at all it throws instead of guessing", () => {
  expect(() => uuidv4({} as never)).toThrow(/random source/);
});
