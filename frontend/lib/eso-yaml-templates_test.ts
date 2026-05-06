import { assert, assertEquals } from "jsr:@std/assert@1";
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
Deno.test("no provider appears in both READY and TEMPLATE_ONLY sets", () => {
  const intersection: SecretStoreProvider[] = [];
  for (const p of READY_SECRET_STORE_PROVIDERS) {
    if (TEMPLATE_ONLY_PROVIDERS.has(p as TemplateOnlyProvider)) {
      intersection.push(p);
    }
  }
  assertEquals(
    intersection,
    [],
    `providers in both sets: ${intersection.join(", ")}`,
  );
});

// Type-level coverage is enforced by `Record<TemplateOnlyProvider, ESOTemplate>`,
// but a runtime check still catches an entry being deleted (or commented out)
// without the type also being narrowed.
Deno.test("ESO_YAML_TEMPLATES covers every TEMPLATE_ONLY_PROVIDERS key", () => {
  const missing: TemplateOnlyProvider[] = [];
  for (const p of TEMPLATE_ONLY_PROVIDERS) {
    if (!ESO_YAML_TEMPLATES[p]) missing.push(p);
  }
  assertEquals(
    missing,
    [],
    `template-only providers without a registry entry: ${missing.join(", ")}`,
  );
});

// Each template must parse as YAML and root to a SecretStore in the v1 API
// group. Catches typos in the inline template strings before they hit the
// /yaml/apply route.
Deno.test("each template parses and roots to kind: SecretStore apiVersion v1", () => {
  for (const [key, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    let parsed: unknown;
    try {
      parsed = yamlParse(tpl.yaml);
    } catch (err) {
      throw new Error(`template ${key} failed to parse: ${err}`);
    }
    assert(
      parsed && typeof parsed === "object",
      `template ${key} did not parse to an object`,
    );
    const obj = parsed as Record<string, unknown>;
    assertEquals(
      obj.kind,
      "SecretStore",
      `template ${key} kind must be SecretStore`,
    );
    assertEquals(
      obj.apiVersion,
      "external-secrets.io/v1",
      `template ${key} apiVersion must be external-secrets.io/v1`,
    );
  }
});

// The parsed YAML's `spec.provider` must contain exactly one key, and that
// key must match the registry key. Catches copy-paste bugs like a `pulumi`
// registry entry whose YAML uses `spec.provider.akeyless` (because someone
// copied the akeyless template and forgot to swap the provider key).
Deno.test("each template's spec.provider key matches the registry key", () => {
  for (const [registryKey, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    const parsed = yamlParse(tpl.yaml) as Record<string, unknown>;
    const spec = parsed.spec as Record<string, unknown> | undefined;
    assert(
      spec && typeof spec === "object",
      `template ${registryKey} missing spec`,
    );
    const provider = spec.provider as Record<string, unknown> | undefined;
    assert(
      provider && typeof provider === "object",
      `template ${registryKey} missing spec.provider`,
    );
    const providerKeys = Object.keys(provider);
    assertEquals(
      providerKeys.length,
      1,
      `template ${registryKey} must have exactly one provider key under spec.provider, got ${providerKeys.length}: ${
        providerKeys.join(", ")
      }`,
    );
    assertEquals(
      providerKeys[0],
      registryKey,
      `template ${registryKey} declares spec.provider.${
        providerKeys[0]
      } — provider key must match the registry key`,
    );
  }
});

// Each template must include multiple `# REPLACE:` markers — a single
// throwaway marker would let a half-finished template pass review. Two is a
// cheap floor that still catches the empty / one-marker degraded case.
Deno.test("each template includes at least 2 # REPLACE: markers", () => {
  for (const [key, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    const matches = tpl.yaml.match(/# REPLACE:/g) ?? [];
    assert(
      matches.length >= 2,
      `template ${key} has ${matches.length} # REPLACE: markers — operators get insufficient signal that placeholders need filling`,
    );
  }
});

// Each template must carry a docsURL so quarterly drift checks can be
// re-derived against upstream.
Deno.test("each template carries a non-empty https docsURL", () => {
  for (const [key, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    assert(
      tpl.docsURL.startsWith("https://"),
      `template ${key} docsURL must be an https URL`,
    );
  }
});
