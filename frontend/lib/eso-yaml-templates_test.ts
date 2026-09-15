import { expect, test } from "bun:test";
import { parse as yamlParse } from "yaml";
import {
  READY_SECRET_STORE_PROVIDERS,
  type SecretStoreProvider,
  TEMPLATE_ONLY_PROVIDERS,
  type TemplateOnlyProvider,
} from "./eso-types.ts";
import { ESO_YAML_TEMPLATES } from "./eso-yaml-templates.ts";

// Phase K invariant: a provider key may not appear in both the wizard-ready
// set and the template-only set. If a provider is being promoted from template
// to wizard, it must be removed from TEMPLATE_ONLY_PROVIDERS in the same edit.
test("no provider appears in both READY and TEMPLATE_ONLY sets", () => {
  const intersection: SecretStoreProvider[] = [];
  for (const p of READY_SECRET_STORE_PROVIDERS) {
    if (TEMPLATE_ONLY_PROVIDERS.has(p as TemplateOnlyProvider)) {
      intersection.push(p);
    }
  }
  expect(intersection).toEqual([]);
});

// Type-level coverage is enforced by `Record<TemplateOnlyProvider, ESOTemplate>`,
// but a runtime check still catches an entry being deleted (or commented out)
// without the type also being narrowed.
test("ESO_YAML_TEMPLATES covers every TEMPLATE_ONLY_PROVIDERS key", () => {
  const missing: TemplateOnlyProvider[] = [];
  for (const p of TEMPLATE_ONLY_PROVIDERS) {
    if (!ESO_YAML_TEMPLATES[p]) missing.push(p);
  }
  expect(missing).toEqual([]);
});

// Each template must parse as YAML and root to a SecretStore in the v1 API
// group. Catches typos in the inline template strings before they hit the
// /yaml/apply route.
test("each template parses and roots to kind: SecretStore apiVersion v1", () => {
  for (const [key, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    let parsed: unknown;
    try {
      parsed = yamlParse(tpl.yaml);
    } catch (err) {
      throw new Error(`template ${key} failed to parse: ${err}`);
    }
    expect(parsed !== null && typeof parsed === "object").toBe(true);
    const obj = parsed as Record<string, unknown>;
    expect(obj.kind).toBe("SecretStore");
    expect(obj.apiVersion).toBe("external-secrets.io/v1");
  }
});

// The parsed YAML's `spec.provider` must contain exactly one key, and that
// key must match the registry key. Catches copy-paste bugs like a `pulumi`
// registry entry whose YAML uses `spec.provider.akeyless` (because someone
// copied the akeyless template and forgot to swap the provider key).
test("each template's spec.provider key matches the registry key", () => {
  for (const [registryKey, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    const parsed = yamlParse(tpl.yaml) as Record<string, unknown>;
    const spec = parsed.spec as Record<string, unknown> | undefined;
    expect(spec !== undefined && typeof spec === "object").toBe(true);
    const provider = spec?.provider as Record<string, unknown> | undefined;
    expect(provider !== undefined && typeof provider === "object").toBe(true);
    const providerKeys = Object.keys(provider ?? {});
    expect(providerKeys.length).toBe(1);
    expect(providerKeys[0]).toBe(registryKey);
  }
});

// Each template must include multiple `# REPLACE:` markers — a single
// throwaway marker would let a half-finished template pass review. Two is a
// cheap floor that still catches the empty / one-marker degraded case.
test("each template includes at least 2 # REPLACE: markers", () => {
  for (const [, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    const matches = tpl.yaml.match(/# REPLACE:/g) ?? [];
    expect(matches.length >= 2).toBe(true);
  }
});

// Each template must carry a docsURL so quarterly drift checks can be
// re-derived against upstream.
test("each template carries a non-empty https docsURL", () => {
  for (const [, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    expect(tpl.docsURL.startsWith("https://")).toBe(true);
  }
});
