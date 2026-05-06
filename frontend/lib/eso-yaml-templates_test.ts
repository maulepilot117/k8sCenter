import { assert, assertEquals } from "jsr:@std/assert@1";
import { parse as yamlParse } from "yaml";
import {
  READY_SECRET_STORE_PROVIDERS,
  type SecretStoreProvider,
  TEMPLATE_ONLY_PROVIDERS,
} from "./eso-types.ts";
import { ESO_YAML_TEMPLATES } from "./eso-yaml-templates.ts";

// Phase K invariant: a provider key may not appear in both the wizard-ready
// set and the template-only set. If a provider is being promoted from template
// to wizard, it must be removed from TEMPLATE_ONLY_PROVIDERS in the same edit.
Deno.test("no provider appears in both READY and TEMPLATE_ONLY sets", () => {
  const intersection: SecretStoreProvider[] = [];
  for (const p of READY_SECRET_STORE_PROVIDERS) {
    if (TEMPLATE_ONLY_PROVIDERS.has(p)) intersection.push(p);
  }
  assertEquals(
    intersection,
    [],
    `providers in both sets: ${intersection.join(", ")}`,
  );
});

// Phase K coverage: every key in TEMPLATE_ONLY_PROVIDERS must have a matching
// entry in ESO_YAML_TEMPLATES. The picker would render a clickable tile that
// dead-ends without this guarantee.
Deno.test("ESO_YAML_TEMPLATES covers every TEMPLATE_ONLY_PROVIDERS key", () => {
  const missing: SecretStoreProvider[] = [];
  for (const p of TEMPLATE_ONLY_PROVIDERS) {
    if (!ESO_YAML_TEMPLATES[p]) missing.push(p);
  }
  assertEquals(
    missing,
    [],
    `template-only providers without a registry entry: ${missing.join(", ")}`,
  );
});

// Phase K coverage in the other direction: every entry in ESO_YAML_TEMPLATES
// must be a TEMPLATE_ONLY provider. A registry entry for a wizard-ready
// provider would be dead code.
Deno.test("ESO_YAML_TEMPLATES has no entries for wizard-ready providers", () => {
  const stray: string[] = [];
  for (const key of Object.keys(ESO_YAML_TEMPLATES)) {
    if (!TEMPLATE_ONLY_PROVIDERS.has(key as SecretStoreProvider)) {
      stray.push(key);
    }
  }
  assertEquals(
    stray,
    [],
    `registry entries that are not template-only: ${stray.join(", ")}`,
  );
});

// Each template must parse as YAML and root to a SecretStore in the v1 API
// group. Catches typos in the inline template strings before they hit the
// /yaml/apply route.
Deno.test("each template parses and roots to kind: SecretStore apiVersion v1", () => {
  for (const [key, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    if (!tpl) continue;
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

// Each template must include at least one `# REPLACE:` marker so operators
// notice they need to fill placeholders before applying.
Deno.test("each template includes # REPLACE: markers", () => {
  for (const [key, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    if (!tpl) continue;
    assert(
      tpl.yaml.includes("# REPLACE:"),
      `template ${key} has no # REPLACE: markers — operators get no signal that placeholders need filling`,
    );
  }
});

// Each template must carry a docsURL so quarterly drift checks can be
// re-derived against upstream.
Deno.test("each template carries a non-empty docsURL", () => {
  for (const [key, tpl] of Object.entries(ESO_YAML_TEMPLATES)) {
    if (!tpl) continue;
    assert(
      tpl.docsURL.startsWith("https://"),
      `template ${key} docsURL must be an https URL`,
    );
  }
});
