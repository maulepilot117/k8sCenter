import { expect, test } from "bun:test";
import { SECRET_VALUE_MASK as M, maskSecretYaml } from "./secret-yaml-mask.ts";

test("masks each stringData value and keeps keys, metadata and type", () => {
  const yaml = [
    "apiVersion: v1",
    "kind: Secret",
    "metadata:",
    "  name: db",
    "  namespace: apps",
    "stringData:",
    "  password: hunter2",
    "  username: admin",
    "type: Opaque",
    "",
  ].join("\n");
  expect(maskSecretYaml(yaml)).toBe(
    [
      "apiVersion: v1",
      "kind: Secret",
      "metadata:",
      "  name: db",
      "  namespace: apps",
      "stringData:",
      `  password: ${M}`,
      `  username: ${M}`,
      "type: Opaque",
      "",
    ].join("\n"),
  );
});

test("drops every line of a block value and of a wrapped long value", () => {
  const yaml = [
    "kind: Secret",
    "stringData:",
    '  .dockerconfigjson: \'{"auths":{"registry.example.com":{"username":"u",',
    '    "password":"s3cret"}}}\'',
    "  tls.crt: |",
    "    -----BEGIN CERTIFICATE-----",
    "",
    "    MIIBszCCAVmgAwIBAgIU",
    "    -----END CERTIFICATE-----",
    "  tls.key: |-",
    "    PRIVATE",
    "type: kubernetes.io/tls",
  ].join("\n");
  const masked = maskSecretYaml(yaml);
  expect(masked).toBe(
    [
      "kind: Secret",
      "stringData:",
      `  .dockerconfigjson: ${M}`,
      `  tls.crt: ${M}`,
      `  tls.key: ${M}`,
      "type: kubernetes.io/tls",
    ].join("\n"),
  );
  for (const secret of ["s3cret", "MIIB", "PRIVATE", "registry.example.com"]) {
    expect(masked).not.toContain(secret);
  }
});

test("masks a data map too, and a quoted key keeps its quotes", () => {
  const yaml = 'data:\n  "a:b": c2VjcmV0\nkind: Secret';
  expect(maskSecretYaml(yaml)).toBe(`data:\n  "a:b": ${M}\nkind: Secret`);
});

test("leaves a manifest without a value map unchanged", () => {
  const yaml = "kind: Secret\nmetadata:\n  name: x\ntype: Opaque\n";
  expect(maskSecretYaml(yaml)).toBe(yaml);
});
