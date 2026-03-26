import { useComputed, useSignal } from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { NAV_SECTIONS } from "@/lib/constants.ts";
import { ResourceIcon } from "@/components/k8s/ResourceIcon.tsx";
import { Logo } from "@/components/ui/Logo.tsx";
import { getAccessToken } from "@/lib/api.ts";
import { fetchCurrentUser, useAuth } from "@/lib/auth.ts";

interface SidebarProps {
  currentPath: string;
}

export default function Sidebar({ currentPath }: SidebarProps) {
  const { user } = useAuth();
  // Reactive — re-evaluates when user signal changes (e.g., after login)
  const userIsAdmin = useComputed(() =>
    user.value?.roles?.includes("admin") ?? false
  );
  const collapsed = useSignal<Record<string, boolean>>({});
  const appVersion = useSignal("");
  const navRef = useRef<HTMLElement>(null);

  // Restore nav scroll position after hydration.
  // Use requestAnimationFrame to ensure the nav has its full rendered height
  // before setting scrollTop (otherwise it gets clamped to 0).
  useEffect(() => {
    if (!IS_BROWSER || !navRef.current) return;
    const saved = sessionStorage.getItem("sidebar-scroll");
    if (saved) {
      const pos = parseInt(saved, 10);
      // Defer to next frame so the browser has laid out the full nav content
      requestAnimationFrame(() => {
        if (navRef.current) navRef.current.scrollTop = pos;
      });
    }
  }, []);

  // Save scroll position on every scroll and before navigation
  const saveScrollPos = useCallback(() => {
    if (navRef.current) {
      sessionStorage.setItem(
        "sidebar-scroll",
        String(navRef.current.scrollTop),
      );
    }
  }, []);

  // Also save on beforeunload (catches navigation that doesn't trigger scroll)
  useEffect(() => {
    if (!IS_BROWSER) return;
    globalThis.addEventListener("beforeunload", saveScrollPos);
    return () => globalThis.removeEventListener("beforeunload", saveScrollPos);
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;

    async function init() {
      // Wait for auth token to be available (set after login/refresh)
      for (let i = 0; i < 20; i++) {
        if (getAccessToken()) break;
        await new Promise((r) => setTimeout(r, 500));
        if (cancelled) return;
      }
      const token = getAccessToken();
      if (!token) return;

      // Fetch user info (populates admin role for Settings visibility)
      if (!user.value) {
        await fetchCurrentUser();
        if (cancelled) return;
      }

      // Fetch version info
      try {
        const res = await fetch("/api/v1/cluster/info", {
          headers: {
            "Authorization": `Bearer ${token}`,
            "X-Requested-With": "XMLHttpRequest",
          },
        });
        if (!res.ok) return;
        const body = await res.json();
        if (!cancelled && body.data?.kubecenter?.version) {
          appVersion.value = body.data.kubecenter.version;
        }
      } catch {
        // best-effort
      }
    }

    init();
    return () => {
      cancelled = true;
    };
  }, []);

  function toggleSection(title: string) {
    collapsed.value = {
      ...collapsed.value,
      [title]: !collapsed.value[title],
    };
  }

  return (
    <aside class="flex h-full w-60 flex-col bg-sidebar text-slate-300 shrink-0">
      {/* Logo */}
      <div class="flex h-14 items-center gap-2 px-4 border-b border-slate-700">
        <Logo size={24} />
        <span class="text-lg font-semibold text-white">k8sCenter</span>
      </div>

      {/* Navigation */}
      <nav
        ref={navRef}
        onScroll={saveScrollPos}
        class="flex-1 overflow-y-auto py-2"
      >
        {NAV_SECTIONS.filter((section) =>
          // Hide "Settings" section for non-admin users
          section.title !== "Settings" || userIsAdmin.value
        ).map((section) => (
          <div key={section.title} class="mb-1">
            <button
              type="button"
              onClick={() => toggleSection(section.title)}
              class="flex w-full items-center justify-between px-4 py-1.5 text-xs font-semibold uppercase tracking-wider text-slate-500 hover:text-slate-300"
            >
              {section.title}
              <svg
                class={`h-3 w-3 transition-transform ${
                  collapsed.value[section.title] ? "-rotate-90" : ""
                }`}
                viewBox="0 0 12 12"
                fill="currentColor"
              >
                <path d="M3 4.5l3 3 3-3" />
              </svg>
            </button>
            {!collapsed.value[section.title] && (
              <ul>
                {section.items.map((item) => {
                  const isActive = currentPath === item.href ||
                    (item.href !== "/" &&
                      currentPath.startsWith(item.href + "/"));
                  return (
                    <li key={item.href}>
                      <a
                        href={item.href}
                        class={`flex items-center gap-2.5 px-4 py-1.5 text-sm transition-colors ${
                          isActive
                            ? "bg-sidebar-active/20 text-white border-r-2 border-sidebar-active"
                            : "hover:bg-sidebar-hover hover:text-white"
                        }`}
                      >
                        <ResourceIcon
                          kind={item.icon}
                          size={16}
                          class={isActive
                            ? "text-sidebar-active"
                            : "text-slate-400"}
                        />
                        {item.label}
                      </a>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        ))}
      </nav>

      {/* Version */}
      <div class="border-t border-slate-700 px-4 py-2 text-xs text-slate-500">
        k8sCenter {appVersion.value}
      </div>
    </aside>
  );
}
