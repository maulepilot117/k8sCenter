/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

const shortcuts = [
  { key: "?", description: "Show keyboard shortcuts" },
  { key: "/", description: "Focus search" },
  { key: "Escape", description: "Close modal / blur focus" },
];

/**
 * Global keyboard shortcut handler + help modal.
 * Mount once in _layout.tsx.
 */
export default function KeyboardShortcuts() {
  const [showHelp, setShowHelp] = useState(false);

  useEffect(() => {
    if (!IS_BROWSER) return;

    function handler(e: KeyboardEvent) {
      // Someone closer to the key already acted on it. These are last-resort
      // shortcuts on the window, so a component that consumed the key owns it:
      // the dashboard grid, for one, takes Escape to leave edit mode and then
      // puts focus on its own toggle, which the blur below would undo.
      if (e.defaultPrevented) return;
      // Ignore when typing in inputs, including contenteditable editors
      // (the YAML editor), whose keys are text, not shortcuts.
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
      if (target?.isContentEditable) return;

      switch (e.key) {
        case "?":
          e.preventDefault();
          setShowHelp((v) => !v);
          break;
        case "/":
          e.preventDefault();
          {
            const search = document.querySelector<HTMLInputElement>(
              "[data-search-input]",
            );
            search?.focus();
          }
          break;
        case "Escape":
          setShowHelp(false);
          (document.activeElement as HTMLElement)?.blur?.();
          break;
      }
    }

    globalThis.addEventListener("keydown", handler);
    return () => globalThis.removeEventListener("keydown", handler);
  }, []);

  if (!showHelp) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center glass-scrim"
      onClick={() => {
        setShowHelp(false);
      }}
    >
      <div
        className="w-full max-w-sm glass-elevated rounded-2xl p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="mb-4 text-lg font-semibold text-text-primary">
          Keyboard Shortcuts
        </h2>
        <div className="space-y-2">
          {shortcuts.map((s) => (
            <div
              key={s.key}
              className="flex items-center justify-between text-sm"
            >
              <span className="text-text-secondary">{s.description}</span>
              <kbd className="rounded bg-elevated px-2 py-0.5 font-mono text-xs text-text-secondary">
                {s.key}
              </kbd>
            </div>
          ))}
        </div>
        <p className="mt-4 text-xs text-text-muted">
          Press <kbd className="font-mono">Escape</kbd> to close
        </p>
      </div>
    </div>
  );
}
