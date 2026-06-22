import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
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
      <div
        style={{ display: "flex", justifyContent: "center", padding: "48px 0" }}
      >
        <Spinner class="text-accent" />
      </div>
    );
  }

  const tabs: { key: TabType; label: string }[] = [
    { key: "providers", label: "Providers" },
    { key: "oidc", label: "OIDC" },
    { key: "ldap", label: "LDAP" },
  ];

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "0" }}>
      {/* Tab strip — underline style, accent active indicator */}
      <div
        style={{
          display: "flex",
          gap: "24px",
          borderBottom: "1px solid var(--border-primary)",
          marginBottom: "24px",
        }}
      >
        {tabs.map(({ key, label }) => (
          <button
            key={key}
            type="button"
            onClick={() => {
              activeTab.value = key;
              testResult.value = "";
            }}
            style={{
              padding: "0 0 12px",
              fontSize: "14px",
              fontWeight: activeTab.value === key ? 650 : 400,
              color: activeTab.value === key
                ? "var(--accent)"
                : "var(--text-muted)",
              border: "none",
              borderBottom: activeTab.value === key
                ? "2px solid var(--accent)"
                : "2px solid transparent",
              background: "none",
              cursor: "pointer",
              transition: "color 120ms ease, border-color 120ms ease",
              marginBottom: "-1px",
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {/* Providers tab */}
      {activeTab.value === "providers" && (
        <WidgetShell title="Configured Providers">
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "0",
            }}
          >
            {providers.value.map((p, i) => (
              <div
                key={p.id}
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  padding: "12px 0",
                  borderBottom: i < providers.value.length - 1
                    ? "1px solid var(--border-subtle)"
                    : "none",
                }}
              >
                <div>
                  <p
                    style={{
                      margin: 0,
                      fontSize: "13px",
                      fontWeight: 500,
                      color: "var(--text-primary)",
                    }}
                  >
                    {p.displayName}
                  </p>
                  <p
                    style={{
                      margin: "2px 0 0",
                      fontSize: "12px",
                      color: "var(--text-muted)",
                    }}
                  >
                    {p.type} · {p.id}
                  </p>
                </div>
                <span
                  style={{
                    borderRadius: "6px",
                    padding: "2px 8px",
                    fontSize: "11px",
                    fontWeight: 500,
                    background: p.type === "local"
                      ? "var(--success-dim)"
                      : "var(--accent-dim)",
                    color: p.type === "local"
                      ? "var(--success)"
                      : p.type === "oidc"
                      ? "var(--accent)"
                      : "var(--accent-2)",
                  }}
                >
                  {p.type.toUpperCase()}
                </span>
              </div>
            ))}
            {providers.value.length === 0 && (
              <p
                style={{
                  margin: 0,
                  padding: "16px 0",
                  fontSize: "13px",
                  color: "var(--text-muted)",
                }}
              >
                No providers configured.
              </p>
            )}
          </div>
        </WidgetShell>
      )}

      {/* OIDC tab */}
      {activeTab.value === "oidc" && (
        <WidgetShell title="OIDC Provider Configuration">
          <p
            style={{
              margin: "0 0 16px",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Configure OIDC providers via environment variables or the YAML
            config file. Use{" "}
            <code
              style={{
                borderRadius: "6px",
                padding: "1px 5px",
                background: "var(--bg-elevated)",
                fontSize: "12px",
                fontFamily: "var(--font-mono)",
                color: "var(--text-primary)",
              }}
            >
              KUBECENTER_AUTH_OIDC_0_ISSUERURL
            </code>{" "}
            pattern for env vars.
          </p>
          <div
            style={{ display: "flex", flexDirection: "column", gap: "12px" }}
          >
            <Input
              label="Test Issuer URL"
              type="url"
              placeholder="https://accounts.google.com"
              value={oidcIssuerURL.value}
              onInput={(e) => {
                oidcIssuerURL.value = (e.target as HTMLInputElement).value;
              }}
            />
            <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
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
                  style={{
                    fontSize: "13px",
                    color: testResult.value.includes("successful")
                      ? "var(--success)"
                      : "var(--error)",
                  }}
                >
                  {testResult.value}
                </span>
              )}
            </div>
          </div>
        </WidgetShell>
      )}

      {/* LDAP tab */}
      {activeTab.value === "ldap" && (
        <WidgetShell title="LDAP Provider Configuration">
          <p
            style={{
              margin: "0 0 16px",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Configure LDAP providers via environment variables or the YAML
            config file. Use{" "}
            <code
              style={{
                borderRadius: "6px",
                padding: "1px 5px",
                background: "var(--bg-elevated)",
                fontSize: "12px",
                fontFamily: "var(--font-mono)",
                color: "var(--text-primary)",
              }}
            >
              KUBECENTER_AUTH_LDAP_0_URL
            </code>{" "}
            pattern for env vars.
          </p>
          <div
            style={{ display: "flex", flexDirection: "column", gap: "12px" }}
          >
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
            <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
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
                  style={{
                    fontSize: "13px",
                    color: testResult.value.includes("successful")
                      ? "var(--success)"
                      : "var(--error)",
                  }}
                >
                  {testResult.value}
                </span>
              )}
            </div>
          </div>
        </WidgetShell>
      )}
    </div>
  );
}
