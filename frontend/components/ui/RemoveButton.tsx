interface RemoveButtonProps {
  onClick: () => void;
  title?: string;
  class?: string;
}

/**
 * Shared"X" icon button for removing items from lists.
 * Used across wizard step components for labels, ports, env vars, selectors.
 */
export function RemoveButton(
  { onClick, title = "Remove", class: className }: RemoveButtonProps,
) {
  return (
    <button
      type="button"
      onClick={onClick}
      class={`p-1 text-text-muted hover:text-danger ${className ?? ""}`}
      title={title}
    >
      <svg
        class="w-4 h-4"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          stroke-width="2"
          d="M6 18L18 6M6 6l12 12"
        />
      </svg>
    </button>
  );
}
