# Step 15: Polish — UX Refinements and Final Cleanup

## Overview

Final polish pass for the Phase 1 MVP. Focus on dark mode toggle, loading states, toast notifications, keyboard shortcuts, and branding cleanup. Many items from the original plan are already implemented (dark: classes, EmptyState component, CSP headers, container images, input validation, rate limiting).

## Items to Implement

### 1. Dark Mode Toggle
- Add a `ThemeToggle` island to the TopBar
- Persist choice in localStorage (`theme: "light" | "dark" | "system"`)
- Default to OS preference via `prefers-color-scheme`
- Apply `class="dark"` on `<html>` element via JS on load
- Update `_app.tsx` to inject a script that reads localStorage before render (prevents flash)

### 2. Loading Skeletons
- Add a `Skeleton` component (`components/ui/Skeleton.tsx`) — animated placeholder bars
- Use in `ResourceTable`, `ResourceDetail`, `Dashboard`, `AuditLogViewer` during initial fetch
- Replace bare spinners with skeleton layouts that match the final content shape

### 3. Toast Notifications
- Add a `ToastProvider` island and `useToast` hook
- Show success/error toasts for: create, delete, apply, YAML operations, login/logout
- Auto-dismiss after 5 seconds, dismissable on click
- Stack multiple toasts vertically

### 4. Keyboard Shortcuts
- `?` — show keyboard shortcut help modal
- `/` — focus search bar (if present)
- `k` / `j` — navigate up/down in resource tables
- `Escape` — close modals/overlays
- Add a `KeyboardShortcuts` island to `_layout.tsx`

### 5. Branding Cleanup
- Fix `_app.tsx` title: "KubeCenter" → "k8sCenter"
- Verify all user-facing strings say "k8sCenter" not "KubeCenter"

## Items Already Done (No Action Needed)

- Dark mode CSS classes (506 occurrences across 73 files)
- `EmptyState` component exists at `components/layout/EmptyState.tsx`
- CSP headers in `_middleware.ts`
- Rate limiting on auth endpoints
- Input validation (k8s name regex in `handle_setup.go`, lengths in `handle_auth.go`)
- Container images: distroless for Go, Deno slim for frontend (Dockerfiles done)
- Error page (`_error.tsx`) exists

## Deferred to Post-MVP

- E2E test suite with Playwright (large effort, separate initiative)
- Responsive tablet design (CSS-only, can iterate post-launch)

## Files to Create

```
frontend/islands/ThemeToggle.tsx      # Dark mode toggle button
frontend/islands/ToastProvider.tsx    # Toast notification system
frontend/islands/KeyboardShortcuts.tsx # Keyboard shortcut handler + help modal
frontend/components/ui/Skeleton.tsx   # Loading skeleton component
```

## Files to Modify

```
frontend/routes/_app.tsx              # Fix title, add theme script
frontend/routes/_layout.tsx           # Add KeyboardShortcuts island
frontend/islands/TopBar.tsx           # Add ThemeToggle
frontend/islands/ResourceTable.tsx    # Use Skeleton during loading
frontend/islands/Dashboard.tsx        # Use Skeleton during loading
```

## Acceptance Criteria

- [ ] Dark mode toggle in TopBar persists across sessions
- [ ] No flash of wrong theme on page load
- [ ] Loading skeletons on resource table and dashboard
- [ ] Toast notifications for create/delete/apply operations
- [ ] `?` shows keyboard shortcut help
- [ ] `/` focuses search bar
- [ ] Title shows "k8sCenter" everywhere
- [ ] `make lint` and `make test` pass
- [ ] `deno fmt --check` passes

## References

- Existing TopBar: `frontend/islands/TopBar.tsx`
- Existing layout: `frontend/routes/_layout.tsx`
- Existing app shell: `frontend/routes/_app.tsx`
- Tailwind dark mode: https://tailwindcss.com/docs/dark-mode
