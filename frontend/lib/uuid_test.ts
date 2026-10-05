import { expect, test } from "bun:test";
import { uuidv4 } from "./uuid.ts";

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
