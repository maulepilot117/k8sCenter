import { expect, test } from "bun:test";
import { decodeSecretValue, sanitizeSecretValue } from "./secret-reveal.ts";

const b64 = (s: string) =>
  btoa(String.fromCharCode(...new TextEncoder().encode(s)));

// Built from code points so the source carries no invisible characters.
const RLO = String.fromCodePoint(0x202e);
const ZWSP = String.fromCodePoint(0x200b);
const BEL = String.fromCodePoint(0x07);
const ESC = String.fromCodePoint(0x1b);
const FF = String.fromCodePoint(0x0c);
const BACKSLASH = String.fromCodePoint(0x5c);
const esc = (hex: string) => `${BACKSLASH}u${hex}`;

test("a text value is shown decoded", () => {
  expect(decodeSecretValue(b64("hunter2"))).toEqual({
    text: "hunter2",
    binary: false,
  });
  expect(decodeSecretValue(b64("pässwörd\nline 2"))).toEqual({
    text: "pässwörd\nline 2",
    binary: false,
  });
});

test("bytes that are not UTF-8 stay base64 and are marked binary", () => {
  const raw = btoa(String.fromCharCode(0xff, 0xfe, 0x00, 0x81));
  expect(decodeSecretValue(raw)).toEqual({ text: raw, binary: true });
});

test("malformed base64 is shown as received", () => {
  expect(decodeSecretValue("not base64!")).toEqual({
    text: "not base64!",
    binary: true,
  });
});

test("malformed base64 is escaped before it is shown", () => {
  expect(decodeSecretValue(`ab${RLO}cd${ZWSP}ef${ESC}gh`)).toEqual({
    text: `ab${esc("202e")}cd${esc("200b")}ef${esc("001b")}gh`,
    binary: true,
  });
});

test("base64 of non-UTF-8 bytes is escaped before it is shown", () => {
  // atob skips ASCII whitespace, so a form feed survives decoding into the
  // binary fallback; it must not be displayed raw.
  const raw = `/w${FF}==`;
  expect(decodeSecretValue(raw)).toEqual({
    text: `/w${esc("000c")}==`,
    binary: true,
  });
});

test("BiDi, zero-width and control characters are escaped", () => {
  expect(sanitizeSecretValue(`pass${RLO}drow`)).toBe(`pass${esc("202e")}drow`);
  expect(sanitizeSecretValue(`a${ZWSP}b${BEL}c`)).toBe(
    `a${esc("200b")}b${esc("0007")}c`,
  );
  expect(sanitizeSecretValue("tab\tnew\nline")).toBe("tab\tnew\nline");
  expect(decodeSecretValue(b64(`x${RLO}y`)).text).toBe(`x${esc("202e")}y`);
});
