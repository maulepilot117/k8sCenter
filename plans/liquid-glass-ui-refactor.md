# Liquid Glass UI Refactor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Per repo `CLAUDE.md`: launch parallel sub-agents for any task touching >5 independent files (Rule 5), re-read files before editing after context decay (Rule 6/9), and run repo-wide verification before declaring done (Rule 4).

**Goal:** Refactor the entire k8sCenter web frontend (Deno 2.x / Fresh 2.x / Preact / Tailwind v4) to the "Liquid Glass" design system from `design_handoff_ui_refactor/`, pixel-faithfully, across all 192 route files, 28 wizards, and the special screens — with zero behavioral regressions.

**Architecture:** A 3-pane shell (64px icon rail → 250px grouped collapsible secondary nav → content) replaces the current 2-pane layout and horizontal tab strips. Every screen is recomposed from a fixed set of drop-in primitives (`GlassCard`, `WidgetShell`, `ResourceTable`, `DetailShell`, `WizardShell`, the form kit, the charts). Glass is chrome/widgets only; data surfaces (tables, YAML, logs, terminals) stay solid. All color/spacing/radius is a `var(--token)`; light + dark modes are the same tokens re-valued.

**Tech Stack:** Deno 2.x, Fresh 2.x (Preact + islands), `@preact/signals`, Tailwind v4 (CSS-first, utility-only + `.glass*` utilities), inline styles + CSS vars, `tools/theme-gen` (single-source theme → CSS + Dart), Playwright e2e.

## Global Constraints

These apply to **every** task. Copied verbatim from the handoff (`DESIGN_SYSTEM.md`, `CLAUDE_CODE_REFACTOR.md`) and the four locked decisions.

- **One branch, one PR** for the entire refactor: `feat/liquid-glass-ui-refactor`. Domains are internal phases/commits, not separate PRs. The tree must compile green (`deno task check`) at the end of every phase so progress is continuously verifiable.
- **Tokens only.** Zero new hex literals in any component. Every color/spacing/radius is a `var(--token)`. If a needed value doesn't exist, add a token — never inline it.
- **Compose, don't restyle.** Build screens from the primitives. If two screens need the same thing, it's a primitive — add it once. No bespoke card/table/modal CSS.
- **Glass = chrome + widgets. Solid = data** (tables, YAML, logs, terminals). Never put `backdrop-filter` on a large data table.
- **Light + dark both work.** Reintroduce the light/dark toggle (reverses the 2026-06-10 single-theme decision, per locked decision #1). Light mode = same tokens re-valued under `.theme-light` on `<html>`.
- **Adopt the handoff IA, redirect old paths** (locked decision #2). New nav hrefs from `nav-sections.ts` are canonical; old URLs (e.g. `/monitoring`, `/policy`, `/rbac/*`) get redirects so bookmarks/e2e selectors don't 404.
- **Primitives: add alongside, migrate per-screen, delete when unused** (locked decision #3). The 3 colliding components (`StatusBadge`, `Select`, `Field`) are introduced without breaking their ~34 existing importers; old components are deleted only once they hit zero importers.
- **No regressions.** Preserve RBAC gating, WebSocket live updates, namespace/cluster scoping, impersonation, command palette, toasts, audit behavior. This is a visual + IA refactor, not a rewrite.
- **Motion/a11y:** subtle only (120–200ms ease); respect `prefers-reduced-motion` + `prefers-reduced-transparency`. **No entrance opacity animations that can strand content at `opacity:0`.** Content visible at rest. Hit targets ≥36px (≥44px mobile). WCAG AA contrast.
- **Layout guardrails:** Don't transition the nav grid track (instant snap on collapse). Top bar must `min-width:0; overflow:hidden`. Content column is `minmax(0,1fr); overflow-x:hidden` — no horizontal overflow with nav open OR collapsed.
- **Mobile parity:** keep `mobile/` theme in sync by regenerating the Dart theme from the JSON sources (`make check-themes` must pass).
- **Type/spacing:** Geist / Geist Mono; page title 24/700/-0.02em; card title 14/650; section header 11/600/uppercase; body 13px; `tabular-nums` on numeric columns. Spacing multiples of 4. Radius: cards 16–18, modals/wizards 20, inputs/buttons 9, pills 6.
- **Verification gate (every phase):** `cd frontend && deno task check` (fmt + lint + check, repo-wide) AND `make test` must pass; `make test-e2e` green before final merge. Plus the per-screen Definition of Done checklist below.
  - **Baseline note (discovered at execution start):** CI (`ci.yml`) runs only `deno lint` + `deno fmt --check`, NOT `deno check`. `main` had 34 pre-existing `deno check` type errors across 19 files and 65 CRLF-blob files that failed `deno fmt --check` on a Windows checkout. **Foundation Tasks 0.0a/0.0b fix both** (LF normalization + all 34 type errors + add `deno check` to CI + correct the CLAUDE.md "identical to CI" line). After 0.0b the baseline is zero, so the gate above is genuinely `deno task check` fully green AND matches the updated CI.

**Per-screen Definition of Done** (the "test" for visual tasks — there are no unit tests for layout):
- [ ] No hard-coded colors; all `var(--token)`
- [ ] Composed from primitives (no bespoke card/table/modal CSS)
- [ ] Glass for chrome/widgets, solid for data surfaces
- [ ] Works in light + dark
- [ ] No horizontal overflow with the nav panel open OR collapsed
- [ ] Existing behavior intact (live updates, RBAC, scoping)
- [ ] Visually matches `design-reference/k8sCenter Shell.dc.html` for the analogous screen
- [ ] `deno task check` passes

---

## File Structure

**Foundation (Phase 0) — created/replaced once:**
- `shared/themes/liquid-glass.json` — refreshed dark tokens (folded from `themes.refreshed.css`)
- `shared/themes/liquid-glass-light.json` — NEW light tokens
- `tools/theme-gen/main.ts` — extended to emit `.theme-light` + new token vars
- `frontend/assets/styles.css` — `:root` layout vars updated (`--rail-width:64px`, `--panel-width:250px`, glass rims)
- `frontend/lib/nav-sections.ts` — merged into `lib/constants.ts` (grouped `groups[]` model)
- `frontend/lib/nav.ts`, `frontend/lib/theme.ts` — NEW signals
- `frontend/components/ui/` — `GlassCard`, `WidgetShell`, `ResourceTable`, `YamlView` (new names, no collision)
- `frontend/components/ui/glass/` — `StatusBadge`, `Select`, `Field` (colliding 3, namespaced here until migration completes)
- `frontend/components/ui/form/` — `TextField`, `Stepper`, `Segmented`, `Toggle`
- `frontend/components/charts/` — `Sparkline`, `Gauge`, `Donut`, `BarRow`
- `frontend/components/k8s/DetailShell.tsx` — NEW
- `frontend/islands/` — `SecondaryNav`, `NavToggle`, `ThemeToggle`, `WizardShell`; `IconRail` (replace), `TopBarV2` (3 edits)
- `frontend/routes/_layout.tsx` — replace with 3-pane shell

**Sweep (Phases 1–11) — per domain, modify existing routes/islands in place.**

---

## Phasing Overview

| Phase | Scope | Files (approx) |
|---|---|---|
| 0 | Foundation: tokens + theme-gen light mode + shell + nav model + primitives + chrome wiring + IA redirects | ~30 |
| 1 | Workloads | 20 routes + wizards (Deployment/StatefulSet/DaemonSet/CronJob/Job/HPA/PDB) |
| 2 | Network | 32 routes + wizards (Ingress/NetworkPolicy/Service) |
| 3 | Storage | 9 + cluster/pvs,storageclasses + wizards (PVC/StorageClass/Snapshot/ScheduledSnapshot/RestoreSnapshot) |
| 4 | Config | 15 + wizards (ConfigMap/Secret/NamespaceLimits) |
| 5 | Security (rbac + admin + security) | 12 + 8 + 14 + wizards (RoleBinding/Policy/Certificate/Issuer) |
| 6 | Observability (observability + monitoring + alerting) + **Topology** + **Log Explorer** | 9 + 2 special engines |
| 7 | GitOps | 6 |
| 8 | External Secrets | 17 + wizards (ExternalSecret/SecretStore) |
| 9 | Backup | 7 + wizards (VeleroBackup/VeleroRestore/VeleroSchedule) |
| 10 | Tools + Scaling + Extensions | 3 + 7 + 5 |
| 11 | Settings + Command Palette + cleanup (delete superseded components) | 6 + palette |

Each phase ends with: `deno task check` green, DoD checklist on every touched screen, commit. Per CLAUDE.md Rule 5, fan out sub-agents (5–8 files each) within a phase; Haiku/`Explore` for inventory, Sonnet for well-scoped single-file conversions, Opus (parent) for cross-cutting synthesis and review.

---

## PHASE 0 — Foundation

Goal: every existing page renders inside the new 3-pane shell; secondary nav shows grouped children with no horizontal scroll; collapse works; light/dark toggle cleanly; primitives available. **No page content changes yet.**

### Task 0.0a: Normalize line endings to LF — ✅ DONE (commits 18b728f..a9c842d)
Added `* text=auto eol=lf` to `.gitattributes`; normalized 65 CRLF-blob frontend files to LF. `deno fmt --check` + `deno lint` now green locally.

### Task 0.0b: Fix 34 pre-existing type errors + add `deno check` to CI

**Files:**
- Modify: the 19 files in `.superpowers/sdd/baseline-type-errors.txt` (e.g. `routes/api/[...path].ts`, `islands/NotificationChannels.tsx`/`NotificationRules.tsx`/`NotificationBell.tsx`, `islands/AlertsPage.tsx`, `islands/ClusterManager.tsx`, `islands/AuditLogViewer.tsx`, `islands/ClusterTopology.tsx`, `islands/StorageDashboard.tsx`, `islands/WorkloadsDashboard.tsx`, `islands/ResourceDetail.tsx`, `islands/NetworkPolicyWizard.tsx`, `islands/PDBWizard.tsx`, `islands/ScheduledSnapshotWizard.tsx`, `lib/wizard-constants.ts`, `components/ui/ConfirmDialog.tsx`, `components/ui/Field.tsx`, `components/k8s/detail/NetworkPolicyOverview.tsx`, `components/k8s/detail/RoleBindingOverview.tsx`)
- Modify: `.github/workflows/ci.yml` (add a `deno check` step after lint/fmt)
- Modify: root `CLAUDE.md` (the "identical to CI" line is now true once CI runs `deno check`)

**Interfaces:**
- Produces: `deno check` exits 0 repo-wide; CI gains a type-check step. Error categories: `Context<State>` handler signature (Fresh 2.x) ×5; `{data:T[]}` vs `T[]` API-unwrap mismatches; `StatusBadgeProps` prop mismatches; missing exports (`LabelEntry`, `formConfig`); assorted property-access. **Fix the type, do not suppress** — where a `.data` unwrap is missing, add it (may be a latent runtime bug); never add `// @ts-ignore` or `as any` to paper over.

- [ ] **Step 1:** Run `cd frontend && deno check 2>&1` — read every error with its file:line.
- [ ] **Step 2:** Fix each, smallest correct change per the real type (unwrap `.data`, correct handler signature to Fresh 2.x `Context`, align StatusBadge props, export missing members). Per CLAUDE.md Rule 9, re-read each file before editing.
- [ ] **Step 3:** `deno check` → 0 errors; `deno fmt --check` + `deno lint` still green; `make test` green.
- [ ] **Step 4:** Add `deno check` step to `ci.yml`; update CLAUDE.md line; commit. `git commit -m "fix(types): clear pre-existing deno check errors; gate deno check in CI"`

### Task 0.1: Fold refreshed tokens into the dark theme JSON

**Files:**
- Modify: `shared/themes/liquid-glass.json`
- Reference: `design_handoff_ui_refactor/frontend/assets/themes.refreshed.css` (dark `:root` block), `design_handoff_ui_refactor/shared/themes/liquid-glass.json`

**Interfaces:**
- Produces: a `liquid-glass.json` whose `colors` map contains every key in `tools/theme-gen/main.ts` `CSS_VAR_MAP` re-valued to the refreshed dark palette (`--bg-base:#080b16`, `--bg-surface:#0d1322`, `--accent:#43b0ff`, `--accent-secondary:#9b87ff`, `--success:#34d399`, `--warning:#fbbf24`, `--error:#fb7185`, `--info:#7dd3fc`, glass surfaces/borders/scrim per the refreshed file).

- [ ] **Step 1: Read current dark JSON and the refreshed source.** Diff the two value sets; list every changed token.
- [ ] **Step 2: Update `liquid-glass.json` `colors` values** to the refreshed dark hex set. Keep `id: "liquid-glass"`, `name`, `default: true`. Do not add keys the generator doesn't map yet (handled in 0.3).
- [ ] **Step 3: Regenerate + verify no schema break.** Run `deno run -A tools/theme-gen/main.ts` — expect it to write `themes.generated.css` + `themes.g.dart` with the new dark values, no error.
- [ ] **Step 4: Commit.** `git commit -m "refactor(theme): refresh liquid-glass dark tokens"`

### Task 0.2: Add the light theme JSON

**Files:**
- Create: `shared/themes/liquid-glass-light.json`
- Reference: `design_handoff_ui_refactor/shared/themes/liquid-glass-light.json` + `.theme-light` block in `themes.refreshed.css`

**Interfaces:**
- Produces: `liquid-glass-light.json` with `id: "liquid-glass-light"`, `name: "Liquid Glass Light"`, `default: false`, and a `colors` map covering every `CSS_VAR_MAP` key at the light values (`--bg-base:#eaeef6`, `--bg-surface:#ffffff`, `--accent:#2a7fe0`, `--success:#10a877`, `--warning:#d99411`, `--error:#e0556a`, `--info:#3aa0d8`, glass surfaces per the light block).

- [ ] **Step 1: Create the file** from the handoff light JSON, ensuring all `CSS_VAR_MAP` keys present (the generator throws if any is missing).
- [ ] **Step 2: Do NOT run theme-gen yet** — the generator will reject an unlisted theme file until 0.3 adds it to `ORDER`. Proceed to 0.3.

### Task 0.3: Extend theme-gen to emit `.theme-light` and the new vars

**Files:**
- Modify: `tools/theme-gen/main.ts`

**Interfaces:**
- Consumes: `liquid-glass.json` (0.1), `liquid-glass-light.json` (0.2).
- Produces: generator that (a) lists `liquid-glass-light` in `ORDER`; (b) emits the light theme under selector `.theme-light` (NOT `[data-theme=...]`) so `lib/theme.ts`'s `classList.toggle("theme-light")` works; (c) extends `CSS_VAR_MAP` with the refreshed extras that belong in the theme layer (`accent2 → --accent-2`, `glassRimLight → --glass-rim-light`, `glassRimSoft → --glass-rim-soft`, `glassRimDark → --glass-rim-dark`). Layout vars (`--rail-width`, `--panel-width`) stay in `styles.css`, not the theme JSON.

- [ ] **Step 1: Add `"liquid-glass-light"` to `ORDER`** (after `"liquid-glass"`).
- [ ] **Step 2: Change the selector rule** in `emitCss`: `const selector = t.default ? ":root" : t.id === "liquid-glass-light" ? ".theme-light" : `[data-theme="${t.id}"]`;`
- [ ] **Step 3: Extend `CSS_VAR_MAP`** with `accent2: "--accent-2"`, `glassRimLight: "--glass-rim-light"`, `glassRimSoft: "--glass-rim-soft"`, `glassRimDark: "--glass-rim-dark"`. Add the matching keys to BOTH JSON files (the generator validates presence).
- [ ] **Step 4: Update the Dart `defaultThemeId` expectation** — still `liquid-glass` (light is `default:false`). Confirm `emitDart` now includes both themes in `kubeThemes` map (mobile gets light tokens for free).
- [ ] **Step 5: Regenerate.** `deno run -A tools/theme-gen/main.ts`. Inspect `themes.generated.css`: expect a `:root {…}` dark block and a `.theme-light {…}` light block with all vars.
- [ ] **Step 6: Verify check mode + mobile parity.** `deno run -A tools/theme-gen/main.ts --check` → "themes in sync"; `make check-themes` passes.
- [ ] **Step 7: Commit.** `git commit -m "feat(theme): add liquid-glass light theme via theme-gen .theme-light selector"`

### Task 0.4: Update layout vars in styles.css

**Files:**
- Modify: `frontend/assets/styles.css` (`:root` block, ~line 21/43-44)

**Interfaces:**
- Produces: `--rail-width: 64px;` (was 60px), `--panel-width: 250px;` (new), `--topbar-height: 56px;` (was 52px — confirm against prototype; keep 52 if e2e/header rely on it, but DESIGN_SYSTEM §5 says 56). Keep `--content-padding`, `--grid-gap`. Add `--gap` alias if charts reference it.

- [ ] **Step 1: Read the `:root` block + every `--topbar-height` usage** (`grep -rn "topbar-height" frontend/`). Decide 52→56 only if no hard pixel offset breaks; document the choice.
- [ ] **Step 2: Update the three layout vars.** Add `--panel-width: 250px;`.
- [ ] **Step 3: Verify glass utilities + reduced-transparency fallbacks** still present (§2 lists `.glass`, `.glass-elevated`, `.glass-bar`, `.glass-scrim`). No change needed if so.
- [ ] **Step 4: Commit.** `git commit -m "refactor(layout): set rail/panel/topbar layout tokens"`

### Task 0.5: Install the grouped nav model into constants.ts

**Files:**
- Modify: `frontend/lib/constants.ts` (replace flat `tabs:[]` on `DOMAIN_SECTIONS` with grouped `groups:[]`; keep `RESOURCE_DETAIL_PATHS` and other exports)
- Reference: `design_handoff_ui_refactor/frontend/lib/nav-sections.ts`

**Interfaces:**
- Produces: exported `DOMAIN_SECTIONS: DomainSection[]` and `SETTINGS_SECTION` using the grouped shape (`{id,label,icon,href,alert?,groups:[{header,items:[{label,href,kind?,count?,health?}]}]}`), plus `getActiveDomain(path)` and `domainById(id)` helpers. Type exports: `Health`, `NavItem`, `NavGroup`, `DomainSection`.
- Consumed by: `IconRail` (0.7), `SecondaryNav` (0.8).

- [ ] **Step 1: Re-read `constants.ts`** fully (it's large — `RESOURCE_DETAIL_PATHS` at line 51, `DOMAIN_SECTIONS` at 353). Identify every other export so nothing is dropped.
- [ ] **Step 2: Replace the `DomainSection`/`tabs` types and `DOMAIN_SECTIONS`/`SETTINGS_SECTION` values** with the grouped model + helpers from `nav-sections.ts`. Preserve `RESOURCE_DETAIL_PATHS` and all unrelated exports verbatim.
- [ ] **Step 3: grep for old `.tabs` consumers.** `grep -rn "\.tabs" frontend/` — fix any reader (besides IconRail, replaced next) to use `.groups`. Per CLAUDE.md Rule 10, also grep for `DOMAIN_SECTIONS` string usages and `SETTINGS_SECTION`.
- [ ] **Step 4: `deno check` the file.** Expect type errors only where `.tabs` is still read; fix them.
- [ ] **Step 5: Commit.** `git commit -m "refactor(nav): grouped domain-section model in constants"`

### Task 0.6: Add nav + theme signals

**Files:**
- Create: `frontend/lib/nav.ts`, `frontend/lib/theme.ts` (drop-in from handoff)

**Interfaces:**
- Produces: `navCollapsed` signal + `toggleNav()` + `panelWidth()` (nav.ts); `theme` signal + `applyTheme()` + `toggleTheme()` (theme.ts).
- Consumed by: `_layout.tsx`, `SecondaryNav`, `NavToggle`, `ThemeToggle`.

- [ ] **Step 1: Copy both files** from the handoff `frontend/lib/`.
- [ ] **Step 2: Reconcile with existing `lib/themes.ts`** (PATCHES.md §2 optional note): pick ONE theme source of truth. Recommended — keep `lib/themes.ts` as the initializer but have it ALSO toggle `theme-light` and read/write the same `kc.theme` key, OR let `lib/theme.ts` own it and drop `initTheme()`'s theme logic. Do not run both independently. Document which.
- [ ] **Step 3: `deno check` both.** Commit. `git commit -m "feat(nav,theme): collapsible-nav + light/dark signals"`

### Task 0.7: Replace IconRail (grouped active-domain detection)

**Files:**
- Replace: `frontend/islands/IconRail.tsx`
- Reference: `design_handoff_ui_refactor/frontend/islands/IconRail.tsx`, `PATCHES.md §1`

**Interfaces:**
- Consumes: `DOMAIN_SECTIONS`, `SETTINGS_SECTION`, `getActiveDomain` from `@/lib/constants.ts` (grouped).
- Produces: rail that lights the right icon for deep routes (matches any `groups[].items[].href`), preserves existing `ICONS` map, logo, settings-at-bottom, tooltip behavior.

- [ ] **Step 1: Read both files.** Confirm the existing `ICONS` map keys cover every `icon` used in the grouped sections (grid/box/globe/harddrive/sliders/shield/activity/git-branch/archive/key/wrench/settings).
- [ ] **Step 2: Swap the import to `getActiveDomain` from constants** and the grouped active-domain check (`items[].href` matching). Keep the portal `RailTooltip` (don't downgrade to native `title`).
- [ ] **Step 3: `deno check`.** Render-smoke: rail shows all domains, active highlights on a deep route.
- [ ] **Step 4: Commit.** `git commit -m "refactor(nav): IconRail grouped active-domain detection"`

### Task 0.8: Add SecondaryNav, NavToggle, ThemeToggle islands

**Files:**
- Create: `frontend/islands/SecondaryNav.tsx`, `NavToggle.tsx`, `ThemeToggle.tsx` (drop-in)

**Interfaces:**
- Consumes: grouped `DOMAIN_SECTIONS`/`SETTINGS_SECTION`/`getActiveDomain`, `navCollapsed`/`toggleNav`/`panelWidth`, `theme`/`toggleTheme`/`applyTheme`.
- Produces: the 250px grouped, filterable, collapsible secondary panel; top-bar nav toggle; top-bar theme toggle.

- [ ] **Step 1: Copy the three islands.** Fix `@/` import paths to point at `constants.ts` (not `nav-sections.ts`).
- [ ] **Step 2: Verify SecondaryNav reads `groups[]`**, renders domain title + collapse chevron + filter box + `groups[{header, items}]`, item = health dot + label + optional count, active = `color-mix(--accent 16%)`. Collapse sets `--panel-width:0px` (instant snap, no width transition).
- [ ] **Step 3: `deno check` all three.** Commit. `git commit -m "feat(nav): SecondaryNav + NavToggle + ThemeToggle islands"`

### Task 0.9: Wire the 3-pane shell + TopBarV2 edits

**Files:**
- Replace: `frontend/routes/_layout.tsx` (3-col grid)
- Modify: `frontend/islands/TopBarV2.tsx` (PATCHES.md §2: import `NavToggle`+`ThemeToggle`; `<NavToggle/>` first in LEFT cluster; `<ThemeToggle/>` before `NotificationBell` in RIGHT cluster; reconcile theme init per 0.6)

**Interfaces:**
- Consumes: `IconRail`, `SecondaryNav`, `TopBarV2`, plus existing `AlertBanner`/`ToastProvider`/`KeyboardShortcuts`/`CommandPalette`/`QuickActionsFab`.
- Produces: `grid-template-columns: var(--rail-width,64px) var(--panel-width,250px) minmax(0,1fr)`; content column `minmax(0,1fr); overflow-x:hidden`; login/setup/auth bypass preserved.

- [ ] **Step 1: Replace `_layout.tsx`** with the 3-pane version (keep the login/setup/auth early return). SecondaryNav column is `gridColumn 2, overflow:hidden`.
- [ ] **Step 2: Apply the 3 TopBarV2 edits.** Add `<NavToggle/>` + `<ThemeToggle/>`; ensure `applyTheme()` runs on mount (via the reconciled theme module from 0.6 — not a duplicate init).
- [ ] **Step 3: Verify top bar `min-width:0; overflow:hidden`** and children shrink (it already reads CSS vars, so palette/light mode inherit).
- [ ] **Step 4: Run app, smoke every domain:** each existing page renders in the shell; nav groups show with no horizontal scroll; collapse snaps; light/dark toggles cleanly; no content shifted to `opacity:0`.
- [ ] **Step 5: `deno task check` (repo-wide) + commit.** `git commit -m "feat(layout): 3-pane liquid-glass shell + topbar nav/theme toggles"`

### Task 0.10: Install primitives + form kit + charts (no collisions)

**Files:**
- Create: `frontend/components/ui/GlassCard.tsx`, `WidgetShell.tsx`, `ResourceTable.tsx`, `YamlView.tsx`
- Create: `frontend/components/charts/Sparkline.tsx`, `Gauge.tsx`, `Donut.tsx`, `BarRow.tsx`
- Create: `frontend/components/k8s/DetailShell.tsx`
- Create: `frontend/components/ui/form/TextField.tsx`, `Stepper.tsx`, `Segmented.tsx`, `Toggle.tsx`
- Create (namespaced to avoid collision): `frontend/components/ui/glass/StatusBadge.tsx`, `Select.tsx`, `Field.tsx`
- Create: `frontend/islands/WizardShell.tsx`

**Interfaces:**
- Produces the canonical primitive APIs consumed by all sweep phases: `GlassCard({padding,style,children})`; `WidgetShell({title,action,children,padding,style})`; `ResourceTable({columns:Column[],rows:Row[],chevron?})` with `Column{key,label,width?,align?}` / `Row{id,cells,onClick?}`; `DetailShell({icon,title,subtitle?,status?,actions?,tabs:DetailTab[],active,onTab,rail?,children})`; `WizardShell({title,icon?,subtitle?,steps:WizardStep[],current,onStep,onCancel,onBack,onNext,nextLabel,yaml?,children})`; charts `Sparkline`/`Gauge`/`Donut`/`BarRow`; form kit `Field`/`TextField`/`Select`/`Stepper`/`Segmented`/`Toggle`; glass `StatusBadge({label,tone})` + `StatusDot({tone,size})`.

- [ ] **Step 1: Copy all drop-in primitives** from the handoff to the paths above. The 3 colliders go under `components/ui/glass/` so the existing `components/ui/StatusBadge.tsx` etc. keep working for their 34 importers.
- [ ] **Step 2: Fix imports** — `GlassCard`/`WidgetShell`/`DetailShell`/`WizardShell` reference each other and `YamlView`/`StatusBadge`; ensure those resolve to the new paths (DetailShell → `@/components/ui/glass/StatusBadge.tsx`).
- [ ] **Step 3: `deno task check` (repo-wide).** Fix any fmt/lint/type issue. Primitives must compile even though nothing uses them yet.
- [ ] **Step 4: Commit.** `git commit -m "feat(ui): install liquid-glass primitives, form kit, charts"`

### Task 0.11: IA redirects for moved routes

**Files:**
- Create: redirect routes for old→new paths (Fresh route handlers returning 307), e.g. `routes/monitoring/index.tsx` may stay but `/policy/*`→`/security/*`, `/rbac/*` stays under Security domain, `/observability/topology` is new vs current topology route.
- Reference: `nav-sections.ts` hrefs vs the route inventory.

**Interfaces:**
- Produces: every `groups[].items[].href` either resolves to an existing route or to a redirect to the real one, so the nav never 404s and old bookmarks/e2e selectors survive.

- [ ] **Step 1: Build the href→route map.** For each `items[].href` in constants, `grep` the route tree for a matching file. List the mismatches (new paths with no route, and old routes with no nav entry).
- [ ] **Step 2: Decide per mismatch:** (a) the target route already exists at a different path → add a redirect handler at the new href; (b) the route is genuinely new (e.g. `/observability/investigate`) → it gets built in its domain phase, so add a temporary placeholder route now that renders a "coming in this refactor" `WidgetShell` (NOT a 404).
- [ ] **Step 3: Add redirect handlers** (Fresh `handler` returning `Response` 307 to the canonical path).
- [ ] **Step 4: Smoke every nav item** — click each, confirm no 404. `deno task check` + commit. `git commit -m "feat(nav): IA redirects for moved routes"`

**Phase 0 done when:** every existing page renders inside the new 3-pane shell; secondary nav groups show with no horizontal scroll; collapse snaps; light/dark toggles cleanly; primitives compile; nav never 404s. **No page content restyled yet.** `deno task check` + `make test` + `make check-themes` green. **CHECKPOINT — pause for approval before Phase 1.**

---

## PHASES 1–11 — Screen Sweep (repeatable task template)

Each domain phase applies the same four conversion patterns. Within a phase, fan out sub-agents (5–8 route files each, per CLAUDE.md Rule 5). Each route is one right-sized task ending in the DoD checklist + `deno task check`.

### Conversion pattern A — List views → `ResourceTable`
- Replace the bespoke/`DataTable` list with `ResourceTable`. Columns vary by kind but ALWAYS: status dot + name (mono where it's an identifier), `StatusBadge` for phase/health, right-aligned `tabular-nums` numerics, row → detail (`onClick` to the detail route), trailing chevron.
- Keep existing data-fetching, RBAC gating, namespace/cluster scoping, WebSocket live updates, filters/search. Only the rendering layer changes.

### Conversion pattern B — Detail views → `DetailShell`
- Wrap in `DetailShell`: glass header card (icon tile + title + `StatusBadge` + action buttons) + tab strip. Move existing Overview / YAML / Events / Metrics content into tabs. Render YAML via `YamlView` (solid mono, line numbers, `--accent-secondary` keys / `--accent` values). Add the 300px live-metrics `rail` (Sparklines for CPU/mem/requests/restarts) where the resource has metrics.
- Preserve existing actions (Scale/Restart/Rollback/Suspend/etc.) and their confirm dialogs, audit, impersonation.

### Conversion pattern C — Dashboards/overviews → `WidgetShell` + charts
- Each widget = `WidgetShell` (glass card + 14/650 title + optional right action slot). Dashboards use responsive **flex-wrap rows** (`display:flex; flex-wrap:wrap; gap:var(--gap)`; each widget `flex:<grow> 1 <basis>px; min-width:<floor>px`) — NOT a fixed 12-col grid. Metric tiles wrap symmetric (2×2), never 3+1; tile text overflow-safe. Use `Gauge`/`Donut`/`Sparkline`/`BarRow`; area charts via inline SVG with top-stop gradient.

### Conversion pattern D — Wizards → `WizardShell`
- One island per wizard, built on `WizardShell` like the reference `DeploymentWizard.tsx`: own form state, render step fields from the form kit, pass the assembled manifest as `yaml` (re-renders live per keystroke). Trigger from the page's primary "New …" button and register a "Create …" entry in the command palette. Preserve the existing wizard's preview→server-side-apply pipeline, validation, partial-apply error routing, and any secret-data-strip safeguards.

### Per-domain task list (each is `route → pattern → DoD → check → commit`)

- [ ] **Phase 1 — Workloads** (20 routes): deployments/statefulsets/daemonsets/replicasets/pods/jobs/cronjobs lists (A) + detail (B) + new wizards on WizardShell: Deployment (reference exists), StatefulSet, DaemonSet, CronJob, Job, HPA, PDB. Workloads overview tiles (C).
- [ ] **Phase 2 — Network** (32 routes): services/ingresses/endpoints/endpointslices/networkpolicies/cilium-policies lists (A) + details (B); mesh overview/routing/mtls/gateway-api/flows (C + special mesh widgets, keep engines); wizards: Ingress, NetworkPolicy, Service (reference exists).
- [ ] **Phase 3 — Storage** (9 + cluster/pvs + cluster/storageclasses): pvcs/pvs/storageclasses/snapshots lists (A) + details (B); storage overview (C); wizards: PVC, StorageClass, Snapshot, ScheduledSnapshot, RestoreSnapshot.
- [ ] **Phase 4 — Config** (15): configmaps/secrets/serviceaccounts/resourcequotas/limitranges lists (A) + details (B); namespace-limits (C); wizards: ConfigMap, Secret (respect secret-data masking/strip), NamespaceLimits. Quota/LimitRange detail uses `BarRow`.
- [ ] **Phase 5 — Security** (rbac 12 + admin 8 + security 14): roles/clusterroles/rolebindings/clusterrolebindings/webhooks lists (A) + details (B); policies/violations/compliance/vulnerabilities/certificates (A/C, compliance gauge + by-engine/by-severity cards); wizards: RoleBinding, Policy, Certificate, Issuer. Set rail `alert` from live violation/expiry counts.
- [ ] **Phase 6 — Observability** (observability 3 + monitoring 3 + alerting 3) + **Topology** + **Log Explorer**: metrics overview/dashboards/prometheus (C); alerts/rules (A); **Topology** — keep engine, restyle chrome to glass, nodes/edges to status tokens, hover→blast-radius dim; **Log Explorer** — keep engine, glass chrome, LogQL bar + volume histogram + level-colored lines; investigate (new).
- [ ] **Phase 7 — GitOps** (6): applications/applicationsets lists (A) + details (B, composite-id driven, diff view), notifications (A).
- [ ] **Phase 8 — External Secrets** (17): dashboard (C), external-secrets/cluster-external-secrets/stores/cluster-stores/push-secrets lists (A) + details (B, drift tri-state, sync history), chain overlay; wizards: ExternalSecret, SecretStore (+ template registry).
- [ ] **Phase 9 — Backup** (7): backups/restores/schedules lists (A) + new flows; wizards: VeleroBackup, VeleroRestore (mapping rows), VeleroSchedule.
- [ ] **Phase 10 — Tools + Scaling + Extensions** (3 + 7 + 5): yaml-apply (solid editor surface), storageclass-wizard; scaling/HPA surfaces; extensions generic CRD list/detail/new (A/B, generic).
- [ ] **Phase 11 — Settings + Command Palette + cleanup** (6 + palette): general/clusters/users/auth/audit (A/C + form kit); **Command Palette** restyle to `glass-elevated` over `--glass-scrim` + "Create …" wizard entries; **delete superseded components** once `grep` confirms zero importers (`components/ui/StatusBadge.tsx`, `Select.tsx`, `Field.tsx` old versions, `Card.tsx`, `DataTable.tsx`, `GaugeRing.tsx`, `SparklineChart.tsx`), and move the surviving 3 from `components/ui/glass/` to canonical `components/ui/` names, fixing imports.

**Each phase:** `deno task check` (repo-wide) + `make test` green; DoD on every screen; commit per logical group. **CHECKPOINT after each phase — pause for approval.**

---

## Final integration (after Phase 11)

- [ ] **Run repo-wide verification:** `cd frontend && deno task check` (fmt+lint+check), `make test`, `make check-themes`, `make test-e2e` (update Playwright selectors for the new nav/IA). Fix ALL failures.
- [ ] **Manual smoke:** every domain in light AND dark, nav open AND collapsed, no horizontal overflow, no `opacity:0` strand, command palette, live WebSocket updates, RBAC gating, cluster/namespace scoping all intact.
- [ ] **Confirm no orphaned old components** remain (`grep` each deleted name → zero hits).
- [ ] **`/ce:review`** per CLAUDE.md before merge; homelab smoke test (backend/frontend in scope).
- [ ] **Open the single PR** `feat/liquid-glass-ui-refactor` → `main`; watch CI to green; address review.

---

## Self-Review notes (plan vs spec)

- **Spec coverage:** Foundation install (CLAUDE_CODE_REFACTOR Phase 0) → Phase 0 tasks 0.1–0.11. Screen sweep order (Workloads→…→Settings) → Phases 1–11. List/Detail/Dashboard/Wizard patterns (§6,§7,§8) → conversion patterns A–D. Special screens (Topology, Log Explorer, Command Palette) → Phases 6 & 11. Tokens/type/spacing/motion/a11y → Global Constraints. Mobile sync → 0.3 + check-themes gate. The 4 locked decisions → Global Constraints.
- **Adaptation noted:** strict per-file unit TDD does not fit a visual refactor; the verification gate is `deno task check` + `make test` + e2e + the per-screen DoD checklist + visual diff vs the prototype. This replaces the "write failing test" steps for layout-only tasks; logic-bearing wizard changes keep their existing test coverage.
- **Collision handling:** only `StatusBadge`/`Select`/`Field` collide; introduced under `components/ui/glass/` and promoted to canonical names in Phase 11 once old importers are migrated (locked decision #3).
- **Wizard count:** handoff said ~17; repo has 28 wizard islands — all are covered across their domain phases.
```
