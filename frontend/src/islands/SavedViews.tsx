/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import {
  applyViewState,
  captureViewState,
  MAX_RECORD_NAME_LEN,
  MAX_SAVED_VIEWS,
  type TableViewState,
} from "@/lib/preference-types.ts";
import {
  type PreferenceReason,
  preferenceReason,
  preferencesApi,
  type SavedViewRecord,
} from "@/lib/preferences.ts";
import { selectedCluster } from "@/src/lib/cluster.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface SavedViewsProps {
  /** Adapter kind slug, e.g. "pods". Matches ResourceTableIslandProps.kind. */
  resourceKind: string;
  /** Current table state, read live so "Save" captures what is on screen. */
  current: TableViewState;
  /** Applies a restored view. ResourceTable owns the actual signal writes. */
  onApply: (state: TableViewState, warnings: string[]) => void;
}

/** What the control is doing right now. Only one of these at a time. */
type Mode =
  | { kind: "idle" }
  | { kind: "saving" }
  | { kind: "renaming"; record: SavedViewRecord }
  /** A save collided with an existing name; the user chooses overwrite or cancel. */
  | { kind: "overwrite"; name: string; existing: SavedViewRecord };

/**
 * Human text for the reason codes this control can provoke. Anything not named
 * here keeps the server's own message: inventing friendlier copy for a reason
 * we did not anticipate would describe the wrong failure.
 */
function reasonMessage(reason: PreferenceReason | undefined): string | null {
  switch (reason) {
    case "database_unavailable":
      return "Saved views need a database, and this deployment has none configured.";
    case "limit_reached":
      return `You have reached the maximum of ${MAX_SAVED_VIEWS} saved views. Delete one to save another.`;
    case "revision_conflict":
      return "This view changed somewhere else since you opened it. The list has been refreshed -- try again.";
    case "invalid_name":
      return "That name is empty or contains characters the server will not store.";
    case "identity_too_long":
      return "Your account identity is longer than saved views support. An operator has to shorten the mapped identity attribute.";
    case "unknown_resource_kind":
    case "unsupported_schema_version":
    case "invalid_config":
      return "This view no longer describes something this server can store.";
    default:
      return null;
  }
}

export default function SavedViews({
  resourceKind,
  current,
  onApply,
}: SavedViewsProps) {
  const [views, setViews] = useState<SavedViewRecord[]>([]);
  const [loading, setLoading] = useState(true);
  /**
   * Why the list is not usable, when it is not. Kept separate from `views` so
   * a failed load never renders as "you have no saved views" -- an absent
   * observation must not look like an empty one.
   */
  const [unavailable, setUnavailable] = useState<PreferenceReason | undefined>(
    undefined,
  );
  const [loadFailed, setLoadFailed] = useState(false);
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<Mode>({ kind: "idle" });
  const [nameDraft, setNameDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<SavedViewRecord | null>(
    null,
  );

  const listAbort = useRef<AbortController | null>(null);
  // The list as last loaded. A write handler reads it after awaiting a reload,
  // when the `views` it closed over is already out of date.
  const viewsRef = useRef<SavedViewRecord[]>([]);
  const cluster = useSignalValue(selectedCluster);

  const load = useCallback(async () => {
    // Cancel any in-flight list so a slow response for the previous cluster
    // cannot land late and render another cluster's views as this one's.
    listAbort.current?.abort();
    const ac = new AbortController();
    listAbort.current = ac;

    setLoading(true);
    try {
      const records = await preferencesApi.listViews(ac.signal);
      if (ac.signal.aborted) return;
      viewsRef.current = records;
      setViews(records);
      setUnavailable(undefined);
      setLoadFailed(false);
    } catch (err) {
      if (
        ac.signal.aborted ||
        (err instanceof DOMException && err.name === "AbortError")
      ) {
        return;
      }
      setUnavailable(preferenceReason(err));
      setLoadFailed(true);
    } finally {
      if (listAbort.current === ac) {
        setLoading(false);
      }
    }
  }, []);

  // `subscribe` runs the callback now (the first load) and on every cluster
  // switch, so the list is always the active cluster's.
  useEffect(() => {
    const unsubscribe = selectedCluster.subscribe(() => {
      load();
    });
    return () => {
      unsubscribe();
      listAbort.current?.abort();
    };
  }, [load]);

  // Views for this table on the active cluster -- the only ones that can be
  // applied. A record is addressed by (cluster, kind); both must match.
  const mine = views.filter(
    (v) => v.clusterId === cluster && v.config.resourceKind === resourceKind,
  );
  // Same table, a different cluster. Shown disabled rather than hidden, so a
  // view the user knows they saved does not silently disappear when they
  // switch clusters (R3).
  const elsewhere = views.filter(
    (v) => v.clusterId !== cluster && v.config.resourceKind === resourceKind,
  );

  const resetMode = () => {
    setMode({ kind: "idle" });
    setNameDraft("");
    setActionError(null);
  };

  const closeMenu = () => {
    setOpen(false);
    resetMode();
  };

  /** Routes a failed write to the banner inside the menu. */
  const reportError = (err: unknown, fallback: string) => {
    const reason = preferenceReason(err);
    setActionError(
      reasonMessage(reason) ?? (err instanceof Error ? err.message : fallback),
    );
  };

  const apply = (record: SavedViewRecord) => {
    const { state, warnings } = applyViewState(record.config);
    onApply(state, warnings);
    closeMenu();
  };

  const save = async () => {
    const name = nameDraft.trim();
    if (!name) {
      setActionError("Give the view a name.");
      return;
    }
    setBusy(true);
    setActionError(null);
    try {
      await preferencesApi.createView(name, captureViewState(current));
      await load();
      showToast(`Saved view "${name}"`, "success");
      resetMode();
    } catch (err) {
      if (preferenceReason(err) === "duplicate_name") {
        // Never silently overwrite: the user named a view that already
        // exists, and replacing their stored scope without asking is a
        // destructive act they did not request.
        //
        // Re-list before looking the record up. The server found a collision,
        // so one exists; if the cached list does not have it — the colliding
        // view was saved in another tab, or after this menu loaded — then
        // searching the cache would miss it and the user would get a generic
        // failure instead of the choice they are owed.
        await load();
        const existing = viewsRef.current.find(
          (v) =>
            v.clusterId === cluster &&
            v.config.resourceKind === resourceKind &&
            v.name.trim().toLowerCase() === name.toLowerCase(),
        );
        if (existing) {
          setMode({ kind: "overwrite", name, existing });
          setBusy(false);
          return;
        }
      }
      reportError(err, "Failed to save the view.");
    } finally {
      setBusy(false);
    }
  };

  const overwrite = async (name: string, existing: SavedViewRecord) => {
    setBusy(true);
    setActionError(null);
    try {
      await preferencesApi.updateView(
        existing.id,
        name,
        existing.revision,
        captureViewState(current),
      );
      await load();
      showToast(`Replaced view "${name}"`, "success");
      resetMode();
    } catch (err) {
      if (preferenceReason(err) === "revision_conflict") {
        // Someone changed this record since we listed it. Re-list so the user
        // sees the current state, and let them decide again -- an automatic
        // retry would overwrite the change they have not seen yet.
        await load();
      }
      reportError(err, "Failed to replace the view.");
    } finally {
      setBusy(false);
    }
  };

  const rename = async (record: SavedViewRecord) => {
    const name = nameDraft.trim();
    if (!name) {
      setActionError("Give the view a name.");
      return;
    }
    setBusy(true);
    setActionError(null);
    try {
      await preferencesApi.updateView(
        record.id,
        name,
        record.revision,
        record.config,
      );
      await load();
      showToast(`Renamed to "${name}"`, "success");
      resetMode();
    } catch (err) {
      if (preferenceReason(err) === "revision_conflict") {
        await load();
      }
      reportError(err, "Failed to rename the view.");
    } finally {
      setBusy(false);
    }
  };

  const remove = async (record: SavedViewRecord) => {
    setBusy(true);
    try {
      await preferencesApi.deleteView(record.id);
      await load();
      showToast(`Deleted view "${record.name}"`, "success");
    } catch (err) {
      reportError(err, "Failed to delete the view.");
    } finally {
      setBusy(false);
      setDeleteTarget(null);
    }
  };

  const buttonStyle = {
    padding: "4px 10px",
    borderRadius: "9px",
    fontSize: "11px",
    fontWeight: 600,
    background: "var(--bg-elevated)",
    border: "1px solid var(--border-primary)",
    color: "var(--text-muted)",
    cursor: "pointer",
    transition: "all 120ms ease",
    whiteSpace: "nowrap" as const,
  };

  // A deployment with no preference database cannot save anything. Say so
  // once, in place of the control, rather than offering a button that always
  // fails -- and never fall back to localStorage, which would look like it
  // worked until the user opened another browser.
  if (unavailable === "database_unavailable") {
    return (
      <span
        data-testid="saved-views-unavailable"
        style={{
          fontSize: "11px",
          color: "var(--text-muted)",
          whiteSpace: "nowrap",
        }}
        title={reasonMessage("database_unavailable") ?? ""}
      >
        Saved views unavailable
      </span>
    );
  }

  const mineLabel = loading ? "Views" : `Views (${mine.length})`;

  return (
    <div style={{ position: "relative" }} data-testid="saved-views">
      <button
        type="button"
        data-testid="saved-views-toggle"
        aria-expanded={open}
        aria-haspopup="menu"
        // "Views (0)" is short enough to fit the toolbar and says nothing about
        // what the control is for. The accessible name and the tooltip carry
        // the verb, so the affordance is discoverable without widening the
        // button.
        aria-label={`${mineLabel} — saved views; save the current filters as a view`}
        title="Save the current filters as a view"
        onClick={() => {
          const next = !open;
          setOpen(next);
          if (next) {
            // Refresh on open, not just on mount. These records are shared
            // across every tab and session the user has open, so a list
            // fetched when the page loaded can be arbitrarily old by the time
            // someone opens the menu -- showing a view they deleted elsewhere,
            // or missing one they just saved.
            load();
          } else {
            resetMode();
          }
        }}
        style={{
          ...buttonStyle,
          background: open
            ? "color-mix(in srgb, var(--accent) 14%, transparent)"
            : "var(--bg-elevated)",
          border: `1px solid ${
            open ? "var(--accent)" : "var(--border-primary)"
          }`,
          color: open ? "var(--accent)" : "var(--text-muted)",
        }}
      >
        {mineLabel}
      </button>

      {open && (
        <div
          role="menu"
          data-testid="saved-views-menu"
          style={{
            position: "absolute",
            top: "calc(100% + 6px)",
            left: 0,
            zIndex: 40,
            minWidth: "280px",
            maxWidth: "360px",
            padding: "8px",
            borderRadius: "11px",
            background: "var(--bg-elevated)",
            border: "1px solid var(--border-primary)",
            boxShadow: "0 12px 32px rgba(0, 0, 0, 0.32)",
          }}
        >
          {loadFailed && (
            <div
              data-testid="saved-views-list-error"
              style={{
                padding: "6px 8px",
                marginBottom: "6px",
                borderRadius: "7px",
                fontSize: "11px",
                color: "var(--error)",
                background: "color-mix(in srgb, var(--error) 10%, transparent)",
              }}
            >
              {reasonMessage(unavailable) ?? "Could not load your saved views."}{" "}
              <button
                type="button"
                onClick={load}
                style={{
                  ...buttonStyle,
                  padding: "2px 8px",
                  marginLeft: "4px",
                }}
              >
                Retry
              </button>
            </div>
          )}

          {!loadFailed && loading && (
            <div
              style={{
                padding: "6px 8px",
                fontSize: "11px",
                color: "var(--text-muted)",
              }}
            >
              Loading...
            </div>
          )}

          {!loadFailed && !loading && mine.length === 0 && (
            <div
              data-testid="saved-views-empty"
              style={{
                padding: "6px 8px",
                fontSize: "11px",
                color: "var(--text-muted)",
              }}
            >
              No saved views for this table yet.
              <div
                data-testid="saved-views-empty-hint"
                style={{
                  marginTop: "4px",
                  // Set explicitly rather than inheriting the muted parent:
                  // --text-muted computes 3.48:1 on this menu surface and
                  // 3.14:1 in the light theme, below the 4.5:1 WCAG 2.2 AA
                  // minimum. The instruction has to be the readable part.
                  color: "var(--text-secondary)",
                }}
              >
                Set the filters you want, then choose{" "}
                <strong>Save current view</strong> below.
              </div>
            </div>
          )}

          {mine.map((record) => (
            <div
              key={record.id}
              data-testid="saved-view-row"
              data-view-name={record.name}
              style={{
                display: "flex",
                alignItems: "center",
                gap: "6px",
                padding: "4px 6px",
                borderRadius: "7px",
              }}
            >
              {mode.kind === "renaming" && mode.record.id === record.id ? (
                <>
                  <input
                    type="text"
                    value={nameDraft}
                    maxLength={MAX_RECORD_NAME_LEN}
                    data-testid="saved-view-rename-input"
                    onChange={(e) => setNameDraft(e.currentTarget.value)}
                    style={{
                      flex: "1 1 auto",
                      minWidth: 0,
                      padding: "3px 6px",
                      fontSize: "12px",
                      borderRadius: "6px",
                      background: "var(--bg-base)",
                      border: "1px solid var(--border-primary)",
                      color: "var(--text-primary)",
                    }}
                  />
                  <button
                    type="button"
                    disabled={busy}
                    data-testid="saved-view-rename-confirm"
                    onClick={() => rename(record)}
                    style={buttonStyle}
                  >
                    Save
                  </button>
                  <button type="button" onClick={resetMode} style={buttonStyle}>
                    Cancel
                  </button>
                </>
              ) : (
                <>
                  <button
                    type="button"
                    role="menuitem"
                    data-testid="saved-view-apply"
                    onClick={() => apply(record)}
                    title={`${
                      record.config.namespace || "All namespaces"
                    } - sorted by ${record.config.sortKey}`}
                    style={{
                      flex: "1 1 auto",
                      minWidth: 0,
                      textAlign: "left",
                      padding: "3px 6px",
                      fontSize: "12px",
                      fontWeight: 600,
                      background: "transparent",
                      border: "none",
                      color: "var(--text-primary)",
                      cursor: "pointer",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {record.name}
                  </button>
                  <button
                    type="button"
                    data-testid="saved-view-rename"
                    onClick={() => {
                      setNameDraft(record.name);
                      setActionError(null);
                      setMode({ kind: "renaming", record });
                    }}
                    style={{ ...buttonStyle, padding: "2px 8px" }}
                  >
                    Rename
                  </button>
                  <button
                    type="button"
                    data-testid="saved-view-delete"
                    onClick={() => setDeleteTarget(record)}
                    style={{ ...buttonStyle, padding: "2px 8px" }}
                  >
                    Delete
                  </button>
                </>
              )}
            </div>
          ))}

          {elsewhere.length > 0 && (
            <div
              data-testid="saved-views-other-clusters"
              style={{
                marginTop: "6px",
                paddingTop: "6px",
                borderTop: "1px solid var(--border-primary)",
              }}
            >
              <div
                style={{
                  padding: "2px 6px",
                  fontSize: "10px",
                  fontWeight: 700,
                  letterSpacing: "0.04em",
                  textTransform: "uppercase",
                  color: "var(--text-muted)",
                }}
              >
                Saved on another cluster
              </div>
              {elsewhere.map((record) => (
                <div
                  key={record.id}
                  data-testid="saved-view-other-cluster"
                  data-cluster-id={record.clusterId}
                  title={`Saved on cluster "${record.clusterId}". Switch to that cluster to open it.`}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: "6px",
                    padding: "3px 6px",
                    fontSize: "12px",
                    color: "var(--text-muted)",
                    cursor: "not-allowed",
                  }}
                >
                  <span
                    style={{
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {record.name}
                  </span>
                  <span style={{ fontSize: "10px", flexShrink: 0 }}>
                    {record.clusterId}
                  </span>
                </div>
              ))}
            </div>
          )}

          <div
            style={{
              marginTop: "6px",
              paddingTop: "6px",
              borderTop: "1px solid var(--border-primary)",
            }}
          >
            {mode.kind === "overwrite" ? (
              <div data-testid="saved-view-overwrite">
                <div
                  style={{
                    padding: "2px 6px 6px",
                    fontSize: "11px",
                    color: "var(--text-muted)",
                  }}
                >
                  A view named "{mode.name}" already exists. Replace its filters
                  and sort with what is on screen?
                </div>
                <div style={{ display: "flex", gap: "6px" }}>
                  <button
                    type="button"
                    disabled={busy}
                    data-testid="saved-view-overwrite-confirm"
                    onClick={() => {
                      if (mode.kind === "overwrite") {
                        overwrite(mode.name, mode.existing);
                      }
                    }}
                    style={buttonStyle}
                  >
                    Replace
                  </button>
                  <button type="button" onClick={resetMode} style={buttonStyle}>
                    Cancel
                  </button>
                </div>
              </div>
            ) : mode.kind === "saving" ? (
              <div style={{ display: "flex", gap: "6px" }}>
                <input
                  type="text"
                  value={nameDraft}
                  maxLength={MAX_RECORD_NAME_LEN}
                  placeholder="Name this view"
                  data-testid="saved-view-name-input"
                  onChange={(e) => setNameDraft(e.currentTarget.value)}
                  style={{
                    flex: "1 1 auto",
                    minWidth: 0,
                    padding: "3px 6px",
                    fontSize: "12px",
                    borderRadius: "6px",
                    background: "var(--bg-base)",
                    border: "1px solid var(--border-primary)",
                    color: "var(--text-primary)",
                  }}
                />
                <button
                  type="button"
                  disabled={busy}
                  data-testid="saved-view-save-confirm"
                  onClick={save}
                  style={buttonStyle}
                >
                  Save
                </button>
                <button type="button" onClick={resetMode} style={buttonStyle}>
                  Cancel
                </button>
              </div>
            ) : (
              <button
                type="button"
                data-testid="saved-view-save"
                onClick={() => {
                  setNameDraft("");
                  setActionError(null);
                  setMode({ kind: "saving" });
                }}
                style={{ ...buttonStyle, width: "100%" }}
              >
                Save current view
              </button>
            )}

            {actionError && (
              <div
                data-testid="saved-views-action-error"
                style={{
                  marginTop: "6px",
                  padding: "6px 8px",
                  borderRadius: "7px",
                  fontSize: "11px",
                  color: "var(--error)",
                  background:
                    "color-mix(in srgb, var(--error) 10%, transparent)",
                }}
              >
                {actionError}
              </div>
            )}
          </div>
        </div>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete saved view"
          message={`"${deleteTarget.name}" will be removed from your saved views. The resources it points at are not affected.`}
          confirmLabel="Delete"
          danger
          loading={busy}
          onConfirm={() => {
            remove(deleteTarget);
          }}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  );
}
