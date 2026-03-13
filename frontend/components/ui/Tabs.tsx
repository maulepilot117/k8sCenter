import type { ComponentChildren } from "preact";
import { useCallback, useRef, useState } from "preact/hooks";

export interface TabDef {
  id: string;
  label: string;
  content: () => ComponentChildren;
}

interface TabsProps {
  tabs: TabDef[];
  activeTab: string;
  onTabChange: (id: string) => void;
}

/**
 * Accessible tab component with ARIA roles and keyboard navigation.
 * Panels are lazily mounted on first activation and kept mounted after.
 */
export function Tabs({ tabs, activeTab, onTabChange }: TabsProps) {
  // Track which tabs have been mounted (for lazy rendering)
  const [mounted, setMounted] = useState<Set<string>>(
    () => new Set([activeTab]),
  );
  const tabRefs = useRef<Map<string, HTMLButtonElement>>(new Map());

  const handleTabChange = useCallback(
    (id: string) => {
      onTabChange(id);
      setMounted((prev) => {
        if (prev.has(id)) return prev;
        const next = new Set(prev);
        next.add(id);
        return next;
      });
    },
    [onTabChange],
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      const currentIndex = tabs.findIndex((t) => t.id === activeTab);
      let nextIndex: number | null = null;

      if (e.key === "ArrowRight" || e.key === "ArrowDown") {
        e.preventDefault();
        nextIndex = (currentIndex + 1) % tabs.length;
      } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
        e.preventDefault();
        nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
      } else if (e.key === "Home") {
        e.preventDefault();
        nextIndex = 0;
      } else if (e.key === "End") {
        e.preventDefault();
        nextIndex = tabs.length - 1;
      }

      if (nextIndex !== null) {
        const nextTab = tabs[nextIndex];
        handleTabChange(nextTab.id);
        tabRefs.current.get(nextTab.id)?.focus();
      }
    },
    [tabs, activeTab, handleTabChange],
  );

  return (
    <div>
      {/* Tab list */}
      <div
        role="tablist"
        aria-orientation="horizontal"
        class="flex border-b border-slate-200 dark:border-slate-700"
        onKeyDown={handleKeyDown}
      >
        {tabs.map((tab) => {
          const isActive = tab.id === activeTab;
          return (
            <button
              key={tab.id}
              ref={(el) => {
                if (el) tabRefs.current.set(tab.id, el);
              }}
              role="tab"
              id={`tab-${tab.id}`}
              aria-selected={isActive}
              aria-controls={`panel-${tab.id}`}
              tabIndex={isActive ? 0 : -1}
              type="button"
              onClick={() => handleTabChange(tab.id)}
              class={`px-4 py-2.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-brand focus-visible:ring-offset-2 dark:focus-visible:ring-offset-slate-900 ${
                isActive
                  ? "border-b-2 border-brand text-brand"
                  : "text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200"
              }`}
            >
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Tab panels */}
      {tabs.map((tab) => {
        const isActive = tab.id === activeTab;
        const isMounted = mounted.has(tab.id);
        if (!isMounted) return null;

        return (
          <div
            key={tab.id}
            role="tabpanel"
            id={`panel-${tab.id}`}
            aria-labelledby={`tab-${tab.id}`}
            hidden={!isActive}
            tabIndex={0}
            class="focus:outline-none"
          >
            {tab.content()}
          </div>
        );
      })}
    </div>
  );
}
