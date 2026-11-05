import { describe, expect, test } from "bun:test";
import { sameOrigin, urlOrigin } from "./origin.ts";

// The same cases as backend/internal/urlorigin/origin_test.go `TestOf`: the
// form must reach the server's answer, or it prompts for a token the server
// would keep, or skips one the server refuses.
describe("urlOrigin matches the server's urlorigin.Of", () => {
  const cases: Array<[string, string, string]> = [
    ["plain https", "https://am.example.com", "https://am.example.com"],
    [
      "uppercase scheme and host",
      "HTTPS://AM.Example.COM",
      "https://am.example.com",
    ],
    [
      "path prefix dropped",
      "https://am.example.com/alertmanager/",
      "https://am.example.com",
    ],
    [
      "explicit port kept",
      "https://am.example.com:9093",
      "https://am.example.com:9093",
    ],
    [
      "default port kept as given",
      "https://am.example.com:443",
      "https://am.example.com:443",
    ],
    [
      "query and fragment dropped",
      "https://am.example.com/x?a=1#f",
      "https://am.example.com",
    ],
    [
      "credentials dropped",
      "https://user:pw@am.example.com",
      "https://am.example.com",
    ],
    [
      "ipv6 literal with port",
      "https://[2001:DB8::1]:9093",
      "https://[2001:db8::1]:9093",
    ],
    ["empty", "", ""],
    ["no scheme", "//am.example.com", ""],
    ["no host", "https:///path", ""],
    ["opaque", "am.example.com:9093", ""],
    ["unparsable", "https://am.example.com:port%zz", ""],
  ];
  for (const [name, raw, want] of cases) {
    test(name, () => {
      expect(urlOrigin(raw)).toBe(want);
    });
  }

  test("surrounding whitespace is ignored, as the form trims the URL it sends", () => {
    expect(urlOrigin("  https://loki.example.com/x  ")).toBe(
      "https://loki.example.com",
    );
  });
});

describe("sameOrigin", () => {
  test("another path on the same host is the same origin", () => {
    expect(
      sameOrigin("https://loki.example.com", "https://loki.example.com/gw"),
    ).toBe(true);
  });

  test("an explicit default port is a different origin", () => {
    expect(
      sameOrigin("https://loki.example.com", "https://loki.example.com:443"),
    ).toBe(false);
  });

  test("another host is a different origin", () => {
    expect(
      sameOrigin("https://loki.example.com", "https://other.example.com"),
    ).toBe(false);
  });

  test("an unusable URL on either side is never the same origin", () => {
    expect(sameOrigin("", "")).toBe(false);
    expect(sameOrigin("https://loki.example.com", "loki.example.com")).toBe(
      false,
    );
    expect(sameOrigin("not a url", "not a url")).toBe(false);
  });
});
