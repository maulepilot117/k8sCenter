import { useSignal } from "@preact/signals";
import { NAV_SECTIONS } from "@/lib/constants.ts";
import { ResourceIcon } from "@/components/k8s/ResourceIcon.tsx";

interface SidebarProps {
  currentPath: string;
}

export default function Sidebar({ currentPath }: SidebarProps) {
  const collapsed = useSignal<Record<string, boolean>>({});

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
        <svg
          width="24"
          height="24"
          viewBox="0 0 24 24"
          fill="none"
          class="text-brand"
        >
          <path
            d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
        <span class="text-lg font-semibold text-white">KubeCenter</span>
      </div>

      {/* Navigation */}
      <nav class="flex-1 overflow-y-auto py-2">
        {NAV_SECTIONS.map((section) => (
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
        KubeCenter v0.1.0
      </div>
    </aside>
  );
}
