/** @jsxImportSource react */
import type { Signal } from "@preact/signals-core";
import { type ReactNode, useEffect, useRef } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { LiveBadge } from "@/components/ui/LiveBadge.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { EventSourceRef } from "@/lib/notification-types.ts";
import { PAGE_SIZE } from "@/lib/notification-types.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

// ---------------------------------------------------------------------------
// NotificationPageHeader
// ---------------------------------------------------------------------------

interface PageHeaderProps {
  kind: string;
  description: string;
  loading: boolean;
  notAvailable: boolean;
  refreshing: boolean;
  onRefresh: () => void;
  onCreate: () => void;
}

export function NotificationPageHeader({
  kind,
  description,
  loading,
  notAvailable,
  refreshing,
  onRefresh,
  onCreate,
}: PageHeaderProps) {
  return (
    <>
      <div className="flex items-center justify-between mb-1">
        <div className="flex items-center gap-2">
          <h1 className="text-2xl font-bold text-text-primary">{kind}s</h1>
          <LiveBadge />
        </div>
        <div className="flex items-center gap-2">
          {!loading && (
            <>
              <Button
                variant="primary"
                onClick={onCreate}
                disabled={notAvailable}
              >
                Create {kind}
              </Button>
              <Button variant="ghost" onClick={onRefresh} disabled={refreshing}>
                {refreshing ? "Refreshing..." : "Refresh"}
              </Button>
            </>
          )}
        </div>
      </div>
      <p className="text-sm text-text-muted mb-6">{description}</p>
    </>
  );
}

// ---------------------------------------------------------------------------
// NotificationUnavailableBanner
// ---------------------------------------------------------------------------

interface UnavailableBannerProps {
  visible: boolean;
  resourceLabel: string;
}

export function NotificationUnavailableBanner({
  visible,
  resourceLabel,
}: UnavailableBannerProps) {
  if (!visible) return null;
  return (
    <div
      className="mb-6 rounded-lg border p-4 bg-elevated"
      style={{ borderColor: "var(--warning)" }}
    >
      <p className="text-sm font-medium" style={{ color: "var(--warning)" }}>
        Flux notification-controller not detected
      </p>
      <p className="text-xs text-text-muted mt-1">
        Install the Flux notification-controller to manage notification{" "}
        {resourceLabel}.{" "}
        <a
          href="https://fluxcd.io/docs/components/notification/"
          target="_blank"
          rel="noopener noreferrer"
          className="text-brand hover:underline"
        >
          Learn more &rarr;
        </a>
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// NotificationSearchBar
// ---------------------------------------------------------------------------

interface SearchBarProps {
  search: Signal<string>;
  page: Signal<number>;
  filteredCount: number;
  totalCount: number;
  resourceLabel: string;
  placeholder: string;
}

export function NotificationSearchBar({
  search,
  page,
  filteredCount,
  totalCount,
  resourceLabel,
  placeholder,
}: SearchBarProps) {
  const searchValue = useSignalValue(search);
  return (
    <div className="mb-4 flex flex-wrap items-center gap-4">
      <div className="flex-1 max-w-xs">
        <SearchBar
          value={searchValue}
          onInput={(v) => {
            search.value = v;
            page.value = 1;
          }}
          placeholder={placeholder}
        />
      </div>
      <span className="text-xs text-text-muted">
        {filteredCount} of {totalCount} {resourceLabel}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// NotificationLoadingSpinner
// ---------------------------------------------------------------------------

export function NotificationLoadingSpinner({ loading }: { loading: boolean }) {
  if (!loading) return null;
  return (
    <div className="flex justify-center py-12">
      <Spinner className="text-brand" />
    </div>
  );
}

// ---------------------------------------------------------------------------
// NotificationPagination
// ---------------------------------------------------------------------------

interface PaginationProps {
  loading: boolean;
  error: string | null;
  filteredCount: number;
  page: Signal<number>;
  totalPages: number;
  resourceLabel: string;
}

export function NotificationPagination({
  loading,
  error,
  filteredCount,
  page,
  totalPages,
  resourceLabel,
}: PaginationProps) {
  const pageValue = useSignalValue(page);
  if (loading || error || filteredCount <= PAGE_SIZE) return null;
  return (
    <div className="mt-4 flex items-center justify-between">
      <p className="text-sm text-text-muted">
        {filteredCount} {resourceLabel} &middot; Page {pageValue} of{" "}
        {totalPages}
      </p>
      <div className="flex gap-2">
        <Button
          variant="ghost"
          onClick={() => {
            page.value--;
          }}
          disabled={pageValue <= 1}
        >
          Previous
        </Button>
        <Button
          variant="ghost"
          onClick={() => {
            page.value++;
          }}
          disabled={pageValue >= totalPages}
        >
          Next
        </Button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// NotificationEmptyState
// ---------------------------------------------------------------------------

interface EmptyStateProps {
  loading: boolean;
  error: string | null;
  filteredCount: number;
  totalCount: number;
  notAvailable: boolean;
  kind: string;
  onCreate: () => void;
}

export function NotificationEmptyState({
  loading,
  error,
  filteredCount,
  totalCount,
  notAvailable,
  kind,
  onCreate,
}: EmptyStateProps) {
  if (loading || error || filteredCount > 0 || notAvailable) return null;
  return (
    <div className="text-center py-12 rounded-lg border border-border-primary bg-elevated">
      <p className="text-text-muted mb-4">
        {totalCount === 0
          ? `No notification ${kind.toLowerCase()}s configured.`
          : `No ${kind.toLowerCase()}s match your filters.`}
      </p>
      {totalCount === 0 && (
        <Button variant="primary" onClick={onCreate}>
          Create {kind}
        </Button>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// NotificationDeleteDialog
// ---------------------------------------------------------------------------

interface DeleteDialogProps {
  target: { name: string; namespace: string } | null;
  loading: boolean;
  kind: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export function NotificationDeleteDialog({
  target,
  loading,
  kind,
  onConfirm,
  onCancel,
}: DeleteDialogProps) {
  if (!target) return null;
  return (
    <ConfirmDialog
      title={`Delete ${target.name}`}
      message={`This will permanently delete the notification ${kind.toLowerCase()} "${target.name}" in namespace "${target.namespace}".`}
      confirmLabel="Delete"
      danger
      loading={loading}
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  );
}

// ---------------------------------------------------------------------------
// NotificationFormShell
// ---------------------------------------------------------------------------

interface FormShellProps {
  id: string;
  title: string;
  submitting: boolean;
  error: string | null;
  onSubmit: () => void;
  onCancel: () => void;
  wide?: boolean;
  children: ReactNode;
}

export function NotificationFormShell({
  id,
  title,
  submitting,
  error,
  onSubmit,
  onCancel,
  wide,
  children,
}: FormShellProps) {
  const firstInputRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    globalThis.addEventListener("keydown", handler);

    // Auto-focus first input inside shell
    const input = firstInputRef.current?.querySelector<HTMLElement>(
      "input:not([disabled]), select:not([disabled])",
    );
    input?.focus();

    return () => globalThis.removeEventListener("keydown", handler);
  }, [onCancel]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center glass-scrim"
      onClick={onCancel}
    >
      <div
        ref={firstInputRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${id}-title`}
        className={`w-full glass-elevated rounded-2xl p-6 ${
          wide ? "max-w-lg max-h-[90vh] overflow-y-auto" : "max-w-md"
        }`}
        onClick={(e) => e.stopPropagation()}
      >
        <h3
          id={`${id}-title`}
          className="text-lg font-semibold text-text-primary mb-4"
        >
          {title}
        </h3>

        {error && <p className="text-sm text-danger mb-3">{error}</p>}

        <div className="space-y-3">{children}</div>

        <div className="mt-6 flex justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            className="rounded-md border border-border-primary px-4 py-2 text-sm font-medium text-text-secondary hover:bg-hover"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={submitting}
            onClick={onSubmit}
            className="rounded-md px-4 py-2 text-sm font-medium disabled:opacity-50 bg-brand hover:bg-brand/90"
            style={{ color: "var(--bg-base)" }}
          >
            {submitting
              ? "..."
              : title.startsWith("Edit")
                ? "Update"
                : "Create"}
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ActionsDropdown
// ---------------------------------------------------------------------------

interface ActionsDropdownProps {
  itemKey: string;
  suspended: boolean;
  openDropdown: Signal<string | null>;
  onEdit: () => void;
  onSuspendToggle: () => void;
  onDelete: () => void;
}

export function ActionsDropdown({
  itemKey,
  suspended,
  openDropdown,
  onEdit,
  onSuspendToggle,
  onDelete,
}: ActionsDropdownProps) {
  const openKey = useSignalValue(openDropdown);
  return (
    <div className="relative">
      <button
        type="button"
        className="rounded px-2 py-1 text-xs font-medium text-text-secondary hover:bg-hover"
        onClick={(e) => {
          e.stopPropagation();
          openDropdown.value = openDropdown.value === itemKey ? null : itemKey;
        }}
      >
        &hellip;
      </button>
      {openKey === itemKey && (
        <div
          className="absolute right-0 z-40 mt-1 w-40 glass-elevated rounded-2xl"
          onClick={(e) => e.stopPropagation()}
        >
          <button
            type="button"
            className="w-full text-left px-3 py-2 text-sm text-text-primary hover:bg-hover"
            onClick={() => {
              openDropdown.value = null;
              onEdit();
            }}
          >
            Edit
          </button>
          <button
            type="button"
            className="w-full text-left px-3 py-2 text-sm text-text-primary hover:bg-hover"
            onClick={() => {
              openDropdown.value = null;
              onSuspendToggle();
            }}
          >
            {suspended ? "Resume" : "Suspend"}
          </button>
          <button
            type="button"
            className="w-full text-left px-3 py-2 text-sm hover:bg-hover"
            style={{ color: "var(--error)" }}
            onClick={() => {
              openDropdown.value = null;
              onDelete();
            }}
          >
            Delete
          </button>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// CountBadge (merged SourceCountBadge + ResourceCountBadge)
// ---------------------------------------------------------------------------

interface CountBadgeProps {
  items: EventSourceRef[];
  label: string;
}

export function CountBadge({ items, label }: CountBadgeProps) {
  const count = items.length;
  if (count === 0) return <span className="text-xs text-text-muted">-</span>;

  const tooltip = items.map((i) => `${i.kind}/${i.name}`).join("\n");

  return (
    <span
      className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-accent/10 text-accent cursor-default"
      title={tooltip}
    >
      {count} {count === 1 ? label : `${label}s`}
    </span>
  );
}
