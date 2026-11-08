/** @jsxImportSource react */
import type { ChangeEvent } from "react";

interface SearchBarProps {
  value: string;
  onInput: (value: string) => void;
  placeholder?: string;
}

export function SearchBar({
  value,
  onInput,
  placeholder = "Search resources...",
}: SearchBarProps) {
  return (
    <div className="relative">
      <svg
        className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2"
        style={{ color: "var(--text-muted)" }}
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
      >
        <circle cx="7" cy="7" r="4.5" />
        <path d="M10.5 10.5L14 14" />
      </svg>
      <input
        type="text"
        value={value}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          onInput(e.currentTarget.value)
        }
        placeholder={placeholder}
        data-search-input
        className="w-full rounded-md border py-2 pl-9 pr-3 text-sm focus:outline-none focus:ring-1 focus:ring-brand"
        style={{
          background: "var(--bg-surface)",
          color: "var(--text-primary)",
          borderColor: "var(--border-primary)",
        }}
      />
    </div>
  );
}
