import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { DOMAIN_SECTIONS, SETTINGS_SECTION } from "@/lib/constants.ts";
import { fuzzySearch } from "@/lib/fuzzy-search.ts";
import type { SearchItem } from "@/lib/fuzzy-search.ts";

function buildSearchIndex(): SearchItem[] {
  const items: SearchItem[] = [];

  // Navigation items from DOMAIN_SECTIONS
  for (const section of DOMAIN_SECTIONS) {
    items.push({
      id: `nav-${section.id}`,
      type: "navigation",
      label: section.label,
      detail: section.href,
      href: section.href,
      icon: section.icon,
    });
    if (section.tabs) {
      for (const tab of section.tabs) {
        items.push({
          id: `nav-${section.id}-${tab.label}`,
          type: "navigation",
          label: tab.label,
          detail: `${section.label} > ${tab.label}`,
          href: tab.href,
          icon: section.icon,
        });
      }
    }
  }

  // Settings section
  if (SETTINGS_SECTION.tabs) {
    for (const tab of SETTINGS_SECTION.tabs) {
      items.push({
        id: `nav-settings-${tab.label}`,
        type: "navigation",
        label: tab.label,
        detail: `Settings > ${tab.label}`,
        href: tab.href,
        icon: "settings",
      });
    }
  }

  // Resource items derived from DOMAIN_SECTIONS tabs
  for (const section of DOMAIN_SECTIONS) {
    if (section.tabs) {
      for (const tab of section.tabs) {
        if (tab.kind) {
          items.push({
            id: `res-${tab.label}`,
            type: "resource",
            label: tab.label,
            detail: tab.href,
            href: tab.href,
          });
        }
      }
    }
  }
  // Quick actions
  const actions = [
    {
      label: "Create Deployment",
      href: "/workloads/deployments?action=create",
    },
    { label: "Create Service", href: "/networking/services?action=create" },
    { label: "Apply YAML", href: "/tools/yaml-apply" },
    { label: "Create ConfigMap", href: "/config/configmaps?action=create" },
    { label: "Create Secret", href: "/config/secrets?action=create" },
    { label: "Create Ingress", href: "/networking/ingresses?action=create" },
    { label: "Create HPA", href: "/scaling/hpas?action=create" },
    { label: "Create Namespace", href: "/cluster/namespaces?action=create" },
    { label: "Investigate Resource", href: "/observability/investigate" },
    { label: "Create Policy", href: "/security/create-policy" },
    { label: "View Policies", href: "/security/policies" },
    { label: "View Violations", href: "/security/violations" },
    { label: "View GitOps Applications", href: "/gitops/applications" },
    { label: "View ApplicationSets", href: "/gitops/applicationsets" },
    { label: "View GitOps Notifications", href: "/gitops/notifications" },
    { label: "View Vulnerabilities", href: "/security/vulnerabilities" },
    { label: "View Certificates", href: "/security/certificates" },
    {
      label: "View Expiring Certificates",
      href: "/security/certificates?status=expiring",
    },
    { label: "Create Certificate", href: "/security/certificates/new" },
    {
      label: "Create Issuer",
      href: "/security/certificates/issuers/new",
    },
    {
      label: "Create ClusterIssuer",
      href: "/security/certificates/cluster-issuers/new",
    },
    {
      label: "External Secrets Dashboard",
      href: "/external-secrets/dashboard",
    },
    {
      label: "View ExternalSecrets",
      href: "/external-secrets/external-secrets",
    },
    {
      label: "Create ExternalSecret",
      href: "/external-secrets/external-secrets/new",
    },
    {
      label: "View SecretStores",
      href: "/external-secrets/stores",
    },
    {
      label: "View ClusterSecretStores",
      href: "/external-secrets/cluster-stores",
    },
    {
      label: "View ExternalSecrets Chain",
      href: "/external-secrets/chain",
    },
    { label: "View Gateway API", href: "/networking/gateway-api" },
    { label: "View Service Mesh", href: "/networking/mesh" },
    { label: "View Mesh Routing", href: "/networking/mesh/routing" },
    { label: "View mTLS Posture", href: "/networking/mesh/mtls" },
    { label: "View Notifications", href: "/notifications" },
    { label: "Notification Channels", href: "/admin/notifications/channels" },
    { label: "Notification Rules", href: "/admin/notifications/rules" },
    { label: "View Namespace Limits", href: "/config/namespace-limits" },
    {
      label: "Create Namespace Limits",
      href: "/config/namespace-limits/new",
    },
  ];
  for (const a of actions) {
    items.push({
      id: `action-${a.label}`,
      type: "action",
      label: a.label,
      detail: a.href,
      href: a.href,
    });
  }

  return items;
}

const TYPE_LABELS: Record<string, string> = {
  action: "Quick Actions",
  resource: "Resources",
  navigation: "Navigation",
};

const TYPE_ORDER: string[] = ["action", "resource", "navigation"];

export default function CommandPalette() {
  const open = useSignal(false);
  const query = useSignal("");
  const selectedIndex = useSignal(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const searchIndex = useRef<SearchItem[]>(buildSearchIndex());
  const results = fuzzySearch(searchIndex.current, query.value);

  // Group results by type in defined order
  const grouped: { type: string; items: SearchItem[] }[] = [];
  for (const type of TYPE_ORDER) {
    const items = results.filter((r) => r.type === type);
    if (items.length > 0) {
      grouped.push({ type, items });
    }
  }

  // Flat list for keyboard navigation
  const flatResults = grouped.flatMap((g) => g.items);

  function openPalette() {
    open.value = true;
    query.value = "";
    selectedIndex.value = 0;
    // Focus input on next tick
    setTimeout(() => inputRef.current?.focus(), 10);
  }

  function closePalette() {
    open.value = false;
    query.value = "";
  }

  function selectItem(item: SearchItem) {
    closePalette();
    if (item.action) {
      item.action();
    } else if (item.href) {
      globalThis.location.href = item.href;
    }
  }

  // Keyboard shortcut: Cmd+K / Ctrl+K
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        if (open.value) {
          closePalette();
        } else {
          openPalette();
        }
      }
    };
    globalThis.addEventListener("keydown", handleKeyDown);
    return () => globalThis.removeEventListener("keydown", handleKeyDown);
  }, []);

  // Listen for custom event from TopBarV2 search trigger
  useEffect(() => {
    const handleOpen = () => openPalette();
    globalThis.addEventListener(
      "open-command-palette",
      handleOpen,
    );
    return () =>
      globalThis.removeEventListener(
        "open-command-palette",
        handleOpen,
      );
  }, []);

  if (!IS_BROWSER) return null;

  if (!open.value) return null;

  const handleInputKeyDown = (e: KeyboardEvent) => {
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        selectedIndex.value = Math.min(
          selectedIndex.value + 1,
          flatResults.length - 1,
        );
        break;
      case "ArrowUp":
        e.preventDefault();
        selectedIndex.value = Math.max(selectedIndex.value - 1, 0);
        break;
      case "Enter":
        e.preventDefault();
        if (flatResults[selectedIndex.value]) {
          selectItem(flatResults[selectedIndex.value]);
        }
        break;
      case "Escape":
        e.preventDefault();
        closePalette();
        break;
    }
  };

  // Track cumulative index across groups
  let cumulativeIndex = 0;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Command palette"
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 9999,
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "center",
        paddingTop: "min(20vh, 120px)",
        background: "rgba(0,0,0,0.5)",
        backdropFilter: "blur(4px)",
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) closePalette();
      }}
    >
      <div
        style={{
          width: "100%",
          maxWidth: "560px",
          background: "var(--bg-surface)",
          border: "1px solid var(--border-primary)",
          borderRadius: "12px",
          boxShadow: "0 16px 48px rgba(0,0,0,0.4)",
          overflow: "hidden",
        }}
      >
        {/* Search input */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "10px",
            padding: "12px 16px",
            borderBottom: "1px solid var(--border-subtle)",
          }}
        >
          <svg
            width="18"
            height="18"
            viewBox="0 0 20 20"
            fill="none"
            stroke="var(--text-muted)"
            stroke-width="2"
            stroke-linecap="round"
          >
            <circle cx="9" cy="9" r="6" />
            <path d="M13.5 13.5L17 17" />
          </svg>
          <input
            ref={inputRef}
            type="text"
            placeholder="Search commands, resources, pages..."
            value={query.value}
            role="combobox"
            aria-expanded="true"
            aria-haspopup="listbox"
            aria-controls="cmd-results"
            aria-activedescendant={flatResults[selectedIndex.value]?.id}
            onInput={(e) => {
              query.value = (e.target as HTMLInputElement).value;
              selectedIndex.value = 0;
            }}
            onKeyDown={handleInputKeyDown}
            style={{
              flex: 1,
              background: "transparent",
              border: "none",
              outline: "none",
              color: "var(--text-primary)",
              fontSize: "14px",
            }}
          />
          <kbd
            style={{
              fontSize: "11px",
              color: "var(--text-muted)",
              border: "1px solid var(--border-subtle)",
              borderRadius: "4px",
              padding: "2px 6px",
            }}
          >
            esc
          </kbd>
        </div>

        {/* Results */}
        <div
          id="cmd-results"
          role="listbox"
          style={{
            maxHeight: "360px",
            overflowY: "auto",
            padding: "8px 0",
          }}
        >
          {flatResults.length === 0 && (
            <div
              style={{
                padding: "24px 16px",
                textAlign: "center",
                color: "var(--text-muted)",
                fontSize: "13px",
              }}
            >
              No results found
            </div>
          )}
          {grouped.map((group) => {
            const groupItems = group.items.map((item) => {
              const index = cumulativeIndex++;
              const isSelected = index === selectedIndex.value;
              return (
                <button
                  key={item.id}
                  type="button"
                  id={item.id}
                  role="option"
                  aria-selected={index === selectedIndex.value}
                  onClick={() => selectItem(item)}
                  onMouseEnter={() => {
                    selectedIndex.value = index;
                  }}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "10px",
                    width: "100%",
                    padding: "8px 16px",
                    background: isSelected
                      ? "var(--accent-dim)"
                      : "transparent",
                    border: "none",
                    color: isSelected
                      ? "var(--text-primary)"
                      : "var(--text-secondary)",
                    fontSize: "13px",
                    cursor: "pointer",
                    textAlign: "left",
                    borderRadius: 0,
                  }}
                >
                  <span
                    style={{
                      flex: 1,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {item.label}
                  </span>
                  {item.detail && (
                    <span
                      style={{
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        flexShrink: 0,
                      }}
                    >
                      {item.detail}
                    </span>
                  )}
                </button>
              );
            });

            return (
              <div key={group.type}>
                <div
                  style={{
                    padding: "6px 16px 4px",
                    fontSize: "11px",
                    fontWeight: 600,
                    color: "var(--text-muted)",
                    textTransform: "uppercase",
                    letterSpacing: "0.05em",
                  }}
                >
                  {TYPE_LABELS[group.type] ?? group.type}
                </div>
                {groupItems}
              </div>
            );
          })}
        </div>

        {/* Footer with keyboard hints */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "16px",
            padding: "8px 16px",
            borderTop: "1px solid var(--border-subtle)",
            fontSize: "11px",
            color: "var(--text-muted)",
          }}
        >
          <span style={{ display: "flex", alignItems: "center", gap: "4px" }}>
            <kbd
              style={{
                border: "1px solid var(--border-subtle)",
                borderRadius: "3px",
                padding: "1px 4px",
                fontSize: "10px",
              }}
            >
              {"\u2191\u2193"}
            </kbd>
            Navigate
          </span>
          <span style={{ display: "flex", alignItems: "center", gap: "4px" }}>
            <kbd
              style={{
                border: "1px solid var(--border-subtle)",
                borderRadius: "3px",
                padding: "1px 4px",
                fontSize: "10px",
              }}
            >
              {"\u21B5"}
            </kbd>
            Select
          </span>
          <span style={{ display: "flex", alignItems: "center", gap: "4px" }}>
            <kbd
              style={{
                border: "1px solid var(--border-subtle)",
                borderRadius: "3px",
                padding: "1px 4px",
                fontSize: "10px",
              }}
            >
              esc
            </kbd>
            Close
          </span>
        </div>
      </div>
    </div>
  );
}
