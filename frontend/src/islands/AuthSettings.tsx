import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { api } from "@/lib/api.ts";
import type { ProviderInfo } from "@/lib/k8s-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

type TabType = "providers" | "oidc" | "ldap";

/**
 * Admin UI for managing authentication providers.
 * Shows configured providers and allows testing OIDC/LDAP connections.
 */
export default function AuthSettings() {
  const [activeTab, setActiveTab] = useState<TabType>("providers");
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [testResult, setTestResult] = useState("");
  const [testLoading, setTestLoading] = useState(false);

  const [oidcIssuerURL, setOidcIssuerURL] = useState("");
  const [ldapURL, setLdapURL] = useState("");
  const [ldapBindDN, setLdapBindDN] = useState("");

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `loadProviders` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    loadProviders();
  }, []);

  async function loadProviders() {
    try {
      const res = await api<ProviderInfo[]>("/v1/auth/providers", {
        method: "GET",
      });
      setProviders(res.data);
    } catch {
      // fail silently
    } finally {
      setLoading(false);
    }
  }

  async function testOIDCConnection() {
    if (!oidcIssuerURL) return;
    setTestLoading(true);
    setTestResult("");
    try {
      await api("/v1/settings/auth/test-oidc", {
        method: "POST",
        body: JSON.stringify({ issuerURL: oidcIssuerURL }),
      });
      setTestResult("OIDC discovery successful");
    } catch (err) {
      setTestResult(
        `OIDC test failed: ${
          err instanceof Error ? err.message : "unknown error"
        }`,
      );
    } finally {
      setTestLoading(false);
    }
  }

  async function testLDAPConnection() {
    if (!ldapURL || !ldapBindDN) return;
    setTestLoading(true);
    setTestResult("");
    try {
      await api("/v1/settings/auth/test-ldap", {
        method: "POST",
        body: JSON.stringify({ url: ldapURL, bindDN: ldapBindDN }),
      });
      setTestResult("LDAP connection successful");
    } catch (err) {
      setTestResult(
        `LDAP test failed: ${
          err instanceof Error ? err.message : "unknown error"
        }`,
      );
    } finally {
      setTestLoading(false);
    }
  }

  if (loading) {
    return (
      <div
        style={{ display: "flex", justifyContent: "center", padding: "48px 0" }}
      >
        <Spinner className="text-accent" />
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
              setActiveTab(key);
              setTestResult("");
            }}
            style={{
              padding: "0 0 12px",
              fontSize: "14px",
              fontWeight: activeTab === key ? 650 : 400,
              color: activeTab === key ? "var(--accent)" : "var(--text-muted)",
              border: "none",
              borderBottom:
                activeTab === key
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
      {activeTab === "providers" && (
        <WidgetShell title="Configured Providers">
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "0",
            }}
          >
            {providers.map((p, i) => (
              <div
                key={p.id}
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  padding: "12px 0",
                  borderBottom:
                    i < providers.length - 1
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
                    background:
                      p.type === "local"
                        ? "var(--success-dim)"
                        : "var(--accent-dim)",
                    color:
                      p.type === "local"
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
            {providers.length === 0 && (
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
      {activeTab === "oidc" && (
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
              value={oidcIssuerURL}
              onChange={(e) => {
                setOidcIssuerURL(e.currentTarget.value);
              }}
            />
            <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
              <Button
                type="button"
                variant="secondary"
                loading={testLoading}
                onClick={testOIDCConnection}
              >
                Test Discovery
              </Button>
              {testResult && (
                <span
                  style={{
                    fontSize: "13px",
                    color: testResult.includes("successful")
                      ? "var(--success)"
                      : "var(--error)",
                  }}
                >
                  {testResult}
                </span>
              )}
            </div>
          </div>
        </WidgetShell>
      )}

      {/* LDAP tab */}
      {activeTab === "ldap" && (
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
              value={ldapURL}
              onChange={(e) => {
                setLdapURL(e.currentTarget.value);
              }}
            />
            <Input
              label="Test Bind DN"
              type="text"
              placeholder="cn=readonly,dc=example,dc=com"
              value={ldapBindDN}
              onChange={(e) => {
                setLdapBindDN(e.currentTarget.value);
              }}
            />
            <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
              <Button
                type="button"
                variant="secondary"
                loading={testLoading}
                onClick={testLDAPConnection}
              >
                Test Connection
              </Button>
              {testResult && (
                <span
                  style={{
                    fontSize: "13px",
                    color: testResult.includes("successful")
                      ? "var(--success)"
                      : "var(--error)",
                  }}
                >
                  {testResult}
                </span>
              )}
            </div>
          </div>
        </WidgetShell>
      )}
    </div>
  );
}
