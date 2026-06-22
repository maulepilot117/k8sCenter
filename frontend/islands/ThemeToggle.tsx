import { useEffect } from "preact/hooks";
import { applyTheme, theme, toggleTheme } from "@/lib/theme.ts";

// Drop this into TopBarV2's RIGHT cluster, next to the notification bell.
export default function ThemeToggle() {
  useEffect(() => {
    applyTheme();
  }, []);

  const light = theme.value === "light";
  return (
    <button
      type="button"
      onClick={toggleTheme}
      title="Toggle theme"
      aria-label="Toggle light/dark theme"
      style={{
        width: "36px",
        height: "36px",
        borderRadius: "9px",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        border: "1px solid var(--border-subtle)",
        background: "var(--bg-elevated)",
        color: "var(--text-secondary)",
        cursor: "pointer",
      }}
    >
      {light
        ? (
          <svg
            width="16"
            height="16"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <circle cx="10" cy="10" r="3.6" />
            <path d="M10 1.5v2M10 16.5v2M3.2 3.2l1.4 1.4M15.4 15.4l1.4 1.4M1.5 10h2M16.5 10h2M3.2 16.8l1.4-1.4M15.4 4.6l1.4-1.4" />
          </svg>
        )
        : (
          <svg
            width="16"
            height="16"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M16 11.5A6.5 6.5 0 0 1 8.5 4a6.5 6.5 0 1 0 7.5 7.5Z" />
          </svg>
        )}
    </button>
  );
}
