import { assertEquals } from "jsr:@std/assert@1";
import { singleSecretStoreHref } from "./secretstore-template-nav.ts";
import type { ApplyResponse } from "./yaml-apply.ts";

// Pure-function tests for the apply-result-to-href predicate. The 5-clause
// conditional that gates the post-apply navigation is exactly the kind of
// guard that drifts silently on refactor — the tests here are the safety net.

function singleResultResponse(
  overrides: {
    action?: string;
    kind?: string;
    namespace?: string | undefined;
    name?: string;
    failed?: number;
  } = {},
): ApplyResponse {
  const action = overrides.action ?? "created";
  const failed = overrides.failed ?? 0;
  // Distinguish "namespace key not in overrides" (use default "default") from
  // "namespace explicitly set to undefined" (test the missing-namespace path).
  const namespace = "namespace" in overrides ? overrides.namespace : "default";
  return {
    results: [
      {
        index: 0,
        kind: overrides.kind ?? "SecretStore",
        name: overrides.name ?? "vault-store",
        namespace,
        action,
      },
    ],
    summary: {
      total: 1,
      created: action === "created" ? 1 : 0,
      configured: action === "configured" ? 1 : 0,
      unchanged: action === "unchanged" ? 1 : 0,
      failed,
    },
  };
}

Deno.test("singleSecretStoreHref returns href when action=created", () => {
  const href = singleSecretStoreHref(
    singleResultResponse({ action: "created" }),
  );
  assertEquals(href, "/external-secrets/stores/default/vault-store");
});

Deno.test("singleSecretStoreHref returns href when action=configured", () => {
  const href = singleSecretStoreHref(
    singleResultResponse({ action: "configured" }),
  );
  assertEquals(href, "/external-secrets/stores/default/vault-store");
});

Deno.test("singleSecretStoreHref returns href when action=unchanged (re-apply succeeds)", () => {
  const href = singleSecretStoreHref(
    singleResultResponse({ action: "unchanged" }),
  );
  assertEquals(href, "/external-secrets/stores/default/vault-store");
});

Deno.test("singleSecretStoreHref returns null when action=failed", () => {
  const href = singleSecretStoreHref(
    singleResultResponse({ action: "failed", failed: 1 }),
  );
  assertEquals(href, null);
});

Deno.test("singleSecretStoreHref returns null when summary.failed > 0 even with create result", () => {
  const href = singleSecretStoreHref({
    results: [{
      index: 0,
      kind: "SecretStore",
      name: "vault-store",
      namespace: "default",
      action: "created",
    }],
    summary: {
      total: 1,
      created: 1,
      configured: 0,
      unchanged: 0,
      failed: 1,
    },
  });
  assertEquals(href, null);
});

Deno.test("singleSecretStoreHref returns null when kind is not SecretStore", () => {
  const href = singleSecretStoreHref(
    singleResultResponse({ kind: "ConfigMap" }),
  );
  assertEquals(href, null);
});

Deno.test("singleSecretStoreHref returns null when namespace is missing", () => {
  const href = singleSecretStoreHref(
    singleResultResponse({ namespace: undefined }),
  );
  assertEquals(href, null);
});

Deno.test("singleSecretStoreHref returns null when name is empty", () => {
  const href = singleSecretStoreHref(singleResultResponse({ name: "" }));
  assertEquals(href, null);
});

Deno.test("singleSecretStoreHref returns null for multi-doc applies", () => {
  const href = singleSecretStoreHref({
    results: [
      {
        index: 0,
        kind: "SecretStore",
        name: "store-a",
        namespace: "default",
        action: "created",
      },
      {
        index: 1,
        kind: "SecretStore",
        name: "store-b",
        namespace: "default",
        action: "created",
      },
    ],
    summary: {
      total: 2,
      created: 2,
      configured: 0,
      unchanged: 0,
      failed: 0,
    },
  });
  assertEquals(href, null);
});

Deno.test("singleSecretStoreHref returns null when total=0", () => {
  const href = singleSecretStoreHref({
    results: [],
    summary: {
      total: 0,
      created: 0,
      configured: 0,
      unchanged: 0,
      failed: 0,
    },
  });
  assertEquals(href, null);
});

Deno.test("singleSecretStoreHref interpolates namespace and name verbatim", () => {
  const href = singleSecretStoreHref(
    singleResultResponse({ namespace: "team-prod", name: "shared-vault" }),
  );
  assertEquals(href, "/external-secrets/stores/team-prod/shared-vault");
});
