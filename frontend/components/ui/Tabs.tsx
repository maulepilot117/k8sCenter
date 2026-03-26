import type { ComponentChildren } from "preact";
import { useCallback } from "preact/hooks";

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
 * Only the active tab's content is rendered.
 */
export function Tabs({ tabs, activeTab, onTabChange }: TabsProps) {
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
        onTabChange(nextTab.id);
        document.getElementById(`tab-${nextTab.id}`)?.focus();
      }
    },
    [tabs, activeTab, onTabChange],
  );

  const activeTabDef = tabs.find((t) => t.id === activeTab);

  return (
    <div>
      {/* Tab list */}
      <div
        role="tablist"
        aria-orientation="horizontal"
        class="flex border-b"
        style={{ borderColor: "var(--border-primary)" }}
        onKeyDown={handleKeyDown}
      >
        {tabs.map((tab) => {
          const isActive = tab.id === activeTab;
          return (
            <button
              key={tab.id}
              role="tab"
              id={`tab-${tab.id}`}
              aria-selected={isActive}
              aria-controls={`panel-${tab.id}`}
              tabIndex={isActive ? 0 : -1}
              type="button"
              onClick={() => onTabChange(tab.id)}
              class={`px-4 py-2.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-brand ${
                isActive ? "border-b-2" : ""
              }`}
              style={isActive
                ? {
                  borderColor: "var(--accent)",
                  color: "var(--accent)",
                }
                : {
                  color: "var(--text-muted)",
                }}
            >
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Active tab panel */}
      {activeTabDef && (
        <div
          role="tabpanel"
          id={`panel-${activeTabDef.id}`}
          aria-labelledby={`tab-${activeTabDef.id}`}
          tabIndex={0}
          class="focus:outline-none"
        >
          {activeTabDef.content()}
        </div>
      )}
    </div>
  );
}
