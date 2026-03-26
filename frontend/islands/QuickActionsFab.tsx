import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

interface QuickAction {
  label: string;
  href: string;
  icon: string;
}

const ACTIONS: QuickAction[] = [
  {
    label: "New Deployment",
    href: "/workloads/deployments/new",
    icon: "M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5",
  },
  {
    label: "New Service",
    href: "/networking/services/new",
    icon:
      "M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c-1.657 0-3-4.03-3-9s1.343-9 3-9m0 18c1.657 0 3-4.03 3-9s-1.343-9-3-9",
  },
  {
    label: "Apply YAML",
    href: "/tools/yaml-apply",
    icon: "M16 18l2-2-2-2M8 18l-2-2 2-2M12 10l-2 8",
  },
  {
    label: "Scale Resource",
    href: "#",
    icon:
      "M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15",
  },
];

export default function QuickActionsFab() {
  const expanded = useSignal(false);

  if (!IS_BROWSER) return null;

  function toggle() {
    expanded.value = !expanded.value;
  }

  function close() {
    expanded.value = false;
  }

  return (
    <div
      style={{
        position: "fixed",
        bottom: "24px",
        right: "24px",
        zIndex: 50,
        display: "flex",
        flexDirection: "column-reverse",
        alignItems: "flex-end",
        gap: "8px",
      }}
    >
      {/* Main FAB button */}
      <button
        type="button"
        onClick={toggle}
        style={{
          width: "48px",
          height: "48px",
          borderRadius: "14px",
          background:
            "linear-gradient(135deg, var(--accent), var(--accent-secondary))",
          color: "#fff",
          border: "none",
          cursor: "pointer",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          boxShadow: "0 4px 12px rgba(0,0,0,0.2)",
          transition: "transform 0.2s ease",
        }}
        aria-label={expanded.value
          ? "Close quick actions"
          : "Open quick actions"}
      >
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2.5"
          stroke-linecap="round"
          stroke-linejoin="round"
          style={{
            transition: "transform 0.2s ease",
            transform: expanded.value ? "rotate(45deg)" : "rotate(0deg)",
          }}
        >
          <path d="M12 5v14M5 12h14" />
        </svg>
      </button>

      {/* Action items */}
      {expanded.value && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "4px",
            alignItems: "flex-end",
          }}
        >
          {ACTIONS.map((action) => (
            <a
              key={action.label}
              href={action.href}
              onClick={close}
              style={{
                display: "flex",
                alignItems: "center",
                gap: "10px",
                padding: "10px 14px",
                borderRadius: "var(--radius)",
                background: "var(--bg-surface)",
                border: "1px solid var(--border-primary)",
                color: "var(--text-primary)",
                fontSize: "13px",
                fontWeight: 500,
                textDecoration: "none",
                whiteSpace: "nowrap",
                transition: "border-color 0.15s ease",
                boxShadow: "0 2px 8px rgba(0,0,0,0.1)",
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLElement).style.borderColor =
                  "var(--accent)";
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLElement).style.borderColor =
                  "var(--border-primary)";
              }}
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="var(--accent)"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
              >
                <path d={action.icon} />
              </svg>
              {action.label}
            </a>
          ))}
        </div>
      )}
    </div>
  );
}
