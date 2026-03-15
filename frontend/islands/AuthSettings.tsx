import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { Card } from "@/components/ui/Card.tsx";
import type { ProviderInfo } from "@/lib/k8s-types.ts";

type TabType = "providers" | "oidc" | "ldap";

/**
 * Admin UI for managing authentication providers.
 * Shows configured providers and allows testing OIDC/LDAP connections.
 */
export default function AuthSettings() {
  const activeTab = useSignal<TabType>("providers");
  const providers = useSignal<ProviderInfo[]>([]);
  const loading = useSignal(true);
  const testResult = useSignal("");
  const testLoading = useSignal(false);

  // Test form state (signals instead of document.getElementById)
  const oidcIssuerURL = useSignal("");
  const ldapURL = useSignal("");
  const ldapBindDN = useSignal("");

  useEffect(() => {
    if (!IS_BROWSER) return;
    loadProviders();
  }, []);

  async function loadProviders() {
    try {
      const res = await api<ProviderInfo[]>("/v1/auth/providers", {
        method: "GET",
      });
      providers.value = res.data;
    } catch {
      // fail silently
    } finally {
      loading.value = false;
    }
  }

  async function testOIDCConnection() {
    if (!oidcIssuerURL.value) return;
    testLoading.value = true;
    testResult.value = "";
    try {
      await api("/v1/settings/auth/test-oidc", {
        method: "POST",
        body: JSON.stringify({ issuerURL: oidcIssuerURL.value }),
      });
      testResult.value = "OIDC discovery successful";
    } catch (err) {
      testResult.value = `OIDC test failed: ${
        err instanceof Error ? err.message : "unknown error"
      }`;
    } finally {
      testLoading.value = false;
    }
  }

  async function testLDAPConnection() {
    if (!ldapURL.value || !ldapBindDN.value) return;
    testLoading.value = true;
    testResult.value = "";
    try {
      await api("/v1/settings/auth/test-ldap", {
        method: "POST",
        body: JSON.stringify({ url: ldapURL.value, bindDN: ldapBindDN.value }),
      });
      testResult.value = "LDAP connection successful";
    } catch (err) {
      testResult.value = `LDAP test failed: ${
        err instanceof Error ? err.message : "unknown error"
      }`;
    } finally {
      testLoading.value = false;
    }
  }

  if (loading.value) {
    return (
      <div class="flex items-center justify-center py-12">
        <div class="h-6 w-6 animate-spin rounded-full border-2 border-slate-300 border-t-blue-600" />
      </div>
    );
  }

  return (
    <div>
      {/* Tabs */}
      <div class="border-b border-slate-200 dark:border-slate-700">
        <nav class="-mb-px flex space-x-6">
          {(
            [
              ["providers", "Providers"],
              ["oidc", "OIDC"],
              ["ldap", "LDAP"],
            ] as const
          ).map(([key, label]) => (
            <button
              type="button"
              key={key}
              onClick={() => {
                activeTab.value = key;
                testResult.value = "";
              }}
              class={`border-b-2 pb-3 text-sm font-medium ${
                activeTab.value === key
                  ? "border-blue-500 text-blue-600 dark:text-blue-400"
                  : "border-transparent text-slate-500 hover:border-slate-300 hover:text-slate-700 dark:text-slate-400"
              }`}
            >
              {label}
            </button>
          ))}
        </nav>
      </div>

      {/* Tab content */}
      <div class="mt-6">
        {activeTab.value === "providers" && (
          <Card title="Configured Providers">
            <div class="divide-y divide-slate-200 dark:divide-slate-700">
              {providers.value.map((p) => (
                <div
                  key={p.id}
                  class="flex items-center justify-between py-3"
                >
                  <div>
                    <p class="text-sm font-medium text-slate-900 dark:text-white">
                      {p.displayName}
                    </p>
                    <p class="text-xs text-slate-500 dark:text-slate-400">
                      {p.type} &middot; {p.id}
                    </p>
                  </div>
                  <span
                    class={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${
                      p.type === "local"
                        ? "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400"
                        : p.type === "oidc"
                        ? "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400"
                        : "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400"
                    }`}
                  >
                    {p.type.toUpperCase()}
                  </span>
                </div>
              ))}
              {providers.value.length === 0 && (
                <p class="py-4 text-sm text-slate-500 dark:text-slate-400">
                  No providers configured.
                </p>
              )}
            </div>
          </Card>
        )}

        {activeTab.value === "oidc" && (
          <Card title="OIDC Provider Configuration">
            <p class="mb-4 text-sm text-slate-500 dark:text-slate-400">
              Configure OIDC providers via environment variables or the YAML
              config file. Use{" "}
              <code class="rounded bg-slate-100 px-1 dark:bg-slate-700">
                KUBECENTER_AUTH_OIDC_0_ISSUERURL
              </code>{" "}
              pattern for env vars.
            </p>
            <div class="space-y-3">
              <Input
                label="Test Issuer URL"
                type="url"
                placeholder="https://accounts.google.com"
                value={oidcIssuerURL.value}
                onInput={(e) => {
                  oidcIssuerURL.value = (e.target as HTMLInputElement).value;
                }}
              />
              <div class="flex items-center gap-3">
                <Button
                  type="button"
                  variant="secondary"
                  loading={testLoading.value}
                  onClick={testOIDCConnection}
                >
                  Test Discovery
                </Button>
                {testResult.value && (
                  <span
                    class={`text-sm ${
                      testResult.value.includes("successful")
                        ? "text-green-600 dark:text-green-400"
                        : "text-red-600 dark:text-red-400"
                    }`}
                  >
                    {testResult.value}
                  </span>
                )}
              </div>
            </div>
          </Card>
        )}

        {activeTab.value === "ldap" && (
          <Card title="LDAP Provider Configuration">
            <p class="mb-4 text-sm text-slate-500 dark:text-slate-400">
              Configure LDAP providers via environment variables or the YAML
              config file. Use{" "}
              <code class="rounded bg-slate-100 px-1 dark:bg-slate-700">
                KUBECENTER_AUTH_LDAP_0_URL
              </code>{" "}
              pattern for env vars.
            </p>
            <div class="space-y-3">
              <Input
                label="Test LDAP URL"
                type="url"
                placeholder="ldaps://ldap.example.com:636"
                value={ldapURL.value}
                onInput={(e) => {
                  ldapURL.value = (e.target as HTMLInputElement).value;
                }}
              />
              <Input
                label="Test Bind DN"
                type="text"
                placeholder="cn=readonly,dc=example,dc=com"
                value={ldapBindDN.value}
                onInput={(e) => {
                  ldapBindDN.value = (e.target as HTMLInputElement).value;
                }}
              />
              <div class="flex items-center gap-3">
                <Button
                  type="button"
                  variant="secondary"
                  loading={testLoading.value}
                  onClick={testLDAPConnection}
                >
                  Test Connection
                </Button>
                {testResult.value && (
                  <span
                    class={`text-sm ${
                      testResult.value.includes("successful")
                        ? "text-green-600 dark:text-green-400"
                        : "text-red-600 dark:text-red-400"
                    }`}
                  >
                    {testResult.value}
                  </span>
                )}
              </div>
            </div>
          </Card>
        )}
      </div>
    </div>
  );
}
