import { expect, test } from "bun:test";
import { resourceHref } from "./k8s-links.ts";

test("a Kubernetes kind links to its detail page", () => {
  expect(resourceHref("Service", "web", "api")).toBe(
    "/networking/services/web/api",
  );
  expect(resourceHref("PersistentVolumeClaim", "db", "data-0")).toBe(
    "/storage/pvcs/db/data-0",
  );
  expect(resourceHref("ExternalSecret", "db", "creds")).toBe(
    "/external-secrets/external-secrets/db/creds",
  );
  expect(resourceHref("Namespace", undefined, "db")).toBe(
    "/cluster/namespaces/db",
  );
});

test("an adapter slug links without being pluralized again", () => {
  expect(resourceHref("configmaps", "web", "settings")).toBe(
    "/config/configmaps/web/settings",
  );
});

test("a kind with no detail page, or no name, does not link", () => {
  expect(resourceHref("Application", "argocd", "web")).toBeNull();
  expect(resourceHref("Service", "web")).toBeNull();
});
