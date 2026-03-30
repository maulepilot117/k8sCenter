import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { api } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";

interface SubNavTab {
  label: string;
  href: string;
  kind?: string;
  count?: boolean;
}

interface SubNavProps {
  tabs: SubNavTab[];
  currentPath: string;
}

function isActive(tabHref: string, currentPath: string): boolean {
  if (tabHref === currentPath) return true;
  // Section root paths (e.g., /networking, /workloads, /storage) should only
  // match exactly — not prefix-match against sub-pages like /networking/services.
  // A section root has exactly 1 path segment after the leading slash.
  const segments = tabHref.replace(/^\//, "").split("/");
  if (segments.length === 1) return false;
  // Prefix match: /workloads/deployments matches /workloads/deployments/ns/name
  if (
    currentPath.startsWith(tabHref) &&
    (currentPath.length === tabHref.length ||
      currentPath[tabHref.length] === "/")
  ) {
    return true;
  }
  return false;
}

export default function SubNav({ tabs, currentPath }: SubNavProps) {
  const counts = useSignal<Record<string, number | null>>({});
  const namespace = selectedNamespace.value;

  useEffect(() => {
    if (!IS_BROWSER) return;

    const countTabs = tabs.filter((t) => t.count && t.kind);
    if (countTabs.length === 0) return;

    // Reset counts for loading state
    const initial: Record<string, number | null> = {};
    for (const t of countTabs) {
      initial[t.kind!] = null;
    }
    counts.value = initial;

    // Single batch call replaces N individual ?limit=1 requests.
    // Debounce by 150ms to avoid rapid-fire fetches when namespace
    // changes quickly (e.g. keyboard navigation through the selector).
    const controller = new AbortController();
    const nsParam = namespace && namespace !== "all"
      ? `?namespace=${encodeURIComponent(namespace)}`
      : "";

    const timer = setTimeout(() => {
      api<Record<string, number>>(`/v1/resources/counts${nsParam}`, {
        method: "GET",
        signal: controller.signal,
      })
        .then((res) => {
          const updated: Record<string, number | null> = {};
          const data = res.data ?? {};
          for (const t of countTabs) {
            updated[t.kind!] = data[t.kind!] ?? 0;
          }
          counts.value = updated;
        })
        .catch((err) => {
          if (err.name === "AbortError") return;
          // Fallback: zero counts on error
          const zeroed: Record<string, number | null> = {};
          for (const t of countTabs) {
            zeroed[t.kind!] = 0;
          }
          counts.value = zeroed;
        });
    }, 150);

    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [namespace, tabs]);

  if (!IS_BROWSER) {
    return (
      <nav
        style={{
          height: "36px",
          borderBottom: "1px solid var(--border-subtle)",
          background: "var(--bg-base)",
        }}
      />
    );
  }

  return (
    <nav
      style={{
        display: "flex",
        alignItems: "stretch",
        gap: "2px",
        borderBottom: "1px solid var(--border-primary)",
        overflowX: "auto",
        marginBottom: "20px",
        flexShrink: 0,
      }}
    >
      {tabs.map((tab) => {
        const active = isActive(tab.href, currentPath);
        const count = tab.kind ? counts.value[tab.kind] : undefined;

        return (
          <a
            key={tab.href}
            href={tab.href}
            style={{
              display: "flex",
              alignItems: "center",
              gap: "6px",
              padding: "8px 16px",
              fontSize: "13px",
              fontWeight: 500,
              color: active ? "var(--accent)" : "var(--text-muted)",
              textDecoration: "none",
              borderBottom: active
                ? "2px solid var(--accent)"
                : "2px solid transparent",
              marginBottom: "-1px",
              whiteSpace: "nowrap",
              cursor: "pointer",
              transition: "color 150ms ease, border-color 150ms ease",
            }}
          >
            {tab.label}
            {tab.count && count !== undefined && count !== null && (
              <span
                style={{
                  fontSize: "11px",
                  fontFamily: "var(--font-mono, monospace)",
                  padding: "1px 6px",
                  borderRadius: "10px",
                  background: active
                    ? "var(--accent-dim)"
                    : "var(--bg-elevated)",
                  color: active ? "var(--accent)" : "var(--text-muted)",
                }}
              >
                {count}
              </span>
            )}
          </a>
        );
      })}
    </nav>
  );
}
