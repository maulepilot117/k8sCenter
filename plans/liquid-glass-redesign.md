# Liquid Glass Redesign — Design System + Phase Plan

**Scope (user-confirmed 2026-06-10):** shared liquid glass design language across the
Fresh web frontend AND the Flutter mobile app; the multi-theme system (Nexus, Dracula,
Tokyo Night, Catppuccin, Nord, One Dark, Gruvbox) is REPLACED by a single `liquid-glass`
theme; full app sweep; **balanced** glass intensity — glass on chrome (nav, topbar,
modals, sheets, toasts, overlay cards), solid high-contrast surfaces for data tables,
YAML/log editors, and terminals (GPU + WCAG rationale).

## Design system

### Tokens (canonical: `shared/themes/liquid-glass.json`)

21 legacy tokens retained (every component already consumes them) + 5 new glass tokens:

| Token | Value | Use |
|---|---|---|
| `bgBase` | `#05080F` | page backdrop (deep space blue-black; gives glass depth) |
| `bgSurface` | `#0D1422` | SOLID data surfaces — tables, editors, logs |
| `bgElevated` | `#141C2E` | solid elevated / no-backdrop-filter fallback |
| `bgHover` | `#1C2640` | hover states |
| `borderPrimary` / `borderSubtle` | `#263350` / `#1A2338` | solid borders |
| `textPrimary` | `#EDF2FB` | ≥12:1 on all bg tiers |
| `textSecondary` | `#97A4C0` | 7.4:1 on bgSurface (AA+) |
| `textMuted` | `#66738F` | 3.9:1 — caption grade only (WCAG AA Large) |
| `accent` | `#3DAEFF` | ice blue, primary actions |
| `accentSecondary` | `#8E7BFF` | violet, gradients/secondary |
| `success`/`warning`/`error`/`info` | `#34D399`/`#FBBF24`/`#FB7185`/`#7DD3FC` | + matching `*Dim` rgba(…,0.12) |
| `glassSurface` | `rgba(15,21,36,0.55)` | glass panel fill (chrome) |
| `glassElevated` | `rgba(22,30,49,0.66)` | glass modal/sheet fill |
| `glassBorder` | `rgba(151,180,228,0.16)` | 1px glass edge |
| `glassHighlight` | `rgba(255,255,255,0.07)` | inset top bevel (box-shadow inset 0 1px 0) |
| `glassScrim` | `rgba(3,6,12,0.50)` | modal scrim (40–60% per HIG) |

CSS vars: `--glass-surface`, `--glass-elevated`, `--glass-border`, `--glass-highlight`,
`--glass-scrim` (emitted by `tools/theme-gen/main.ts`; Dart fields of the same names in
`themes.g.dart`).

### Glass recipes

**Web** (Phase 2 adds utilities to `frontend/assets/styles.css`):

```css
.glass         { background: var(--glass-surface);
                 backdrop-filter: blur(20px) saturate(140%);
                 -webkit-backdrop-filter: blur(20px) saturate(140%);
                 border: 1px solid var(--glass-border);
                 box-shadow: inset 0 1px 0 var(--glass-highlight); }
.glass-elevated{ same with var(--glass-elevated), blur(28px); }
@supports not (backdrop-filter: blur(1px)) { .glass { background: var(--bg-elevated); } }
@media (prefers-reduced-transparency: reduce) { .glass { backdrop-filter: none; background: var(--bg-elevated); } }
```

Ambient backdrop (so blur has something to refract): two fixed, very dim radial
gradients on `body` — accent at ~7% top-left, accentSecondary at ~5% bottom-right.
Static, no animation (data-dense ops tool; respect `prefers-reduced-motion` anyway).

**Flutter** (Phase 7): `GlassContainer` widget — `ClipRRect` + `BackdropFilter
(ImageFilter.blur(sigma 20))` + glass token fill + 1px glassBorder. Used ONLY on:
bottom sheets (confirm sheet, pickers), dialogs, app bar (scrolled-under state),
drawer. Never on scrolling list items (GPU).

### Structural style
- Radii: web `--radius` 10px → 16px for glass panels, 10px solid cards; Flutter sheets 20px top.
- Typography: keep Geist/Geist Mono (already spatial/clean; avoids font swap churn).
- Motion: 150–300ms ease-out micro; modals scale 0.97→1 + fade; respect reduced-motion (already wired).
- Icons: existing SVG set unchanged.
- Anti-patterns (per design DB): no vibrant block colors, no playful palette drift,
  glass never under body text < 4.5:1, no blur on virtualized rows.

## Phase plan (≤5 hand-edited files each, approval gate between phases)

- [x] **Phase 1 — token foundation (DONE 2026-06-10)**
  `shared/themes/liquid-glass.json` (new, default), 7 old theme JSONs deleted,
  `tools/theme-gen/main.ts` (ORDER + 5 glass tokens), `frontend/lib/themes.ts`,
  `frontend/routes/_app.tsx` (inline script + #05080f), `frontend/assets/styles.css`
  (fallback colors), regenerated `themes.generated.css` + `themes.g.dart`.
  Mobile fallout fixed in same phase: 74 test files `buildKubeTheme('nexus')`→
  `'liquid-glass'` (mechanical), theme_controller/kube_theme_builder tests updated,
  ThemePickerSheet deleted (lib + test + Settings tile + app_router palette button —
  single theme = nothing to pick).
  Verified: `make check-themes` in sync; `deno lint` clean; `deno check` = 40 errors,
  byte-identical to clean-main baseline (pre-existing, stricter local Deno); local
  `deno fmt` noise is CRLF checkout artifact (autocrlf=true; CI sees LF); `flutter
  analyze` clean; full `flutter test` 1227 passed / 20 skipped; WCAG contrast test
  passes for liquid-glass.
- [x] **Phase 2 — web glass foundation (DONE)**: styles.css `.glass` /
  `.glass-elevated` / `.glass-scrim` utilities + ambient radial backdrop
  (`body::before`, color-mix from accent tokens) + `--radius-glass`/blur vars +
  `@supports` and `prefers-reduced-transparency` fallbacks + Tailwind `@theme`
  glass color bridge; `ThemeSelector.tsx` deleted + TopBarV2 mount removed.
- [x] **Phase 3 — web chrome (DONE)**: `TopBarV2.tsx` + `IconRail.tsx` (glass bar/rail
  + glass tooltip), `CommandPalette.tsx` (glass-scrim + glass-elevated panel),
  `ToastProvider.tsx` (glass toasts, type identity via left accent edge).
- [x] **Phase 4 — web primitives (DONE)**: `Card.tsx` (opt-in `glass` prop, solid
  default), `ConfirmDialog.tsx` (scrim + glass panel), `Button.tsx` (danger fg →
  bg-base for contrast, cursor-pointer), `Tabs.tsx` (inactive → textSecondary),
  `form-styles.ts` (8px radius; inputs stay solid).
- [x] **Phase 5 — web high-traffic (DONE)**: `login.tsx` glass hero card,
  `GaugeRing.tsx` + `DeploymentOverview.tsx` + `DashboardV2.tsx` hardcoded
  gradient colors → accent tokens, `PodTerminal.tsx` xterm bg → #0d1422 (token
  value; canvas can't read CSS vars).
- [x] **Phase 6 — web sweep (DONE, 3 sub-agents)**: 12 overlay/dialog surfaces
  glassified (UserManager, NotificationChannels/Rules/Shared, KeyboardShortcuts,
  ESOBulkRefreshDialog, ScaleDialog, CertificateDetail, CRDResourceList,
  ClusterTopology tooltip, QuickActionsFab menu, NotificationBell dropdown,
  PodTerminal reconnect overlay); all 23 `text-white`-on-light-token occurrences
  across 16 files → `var(--bg-base)`. Playwright screenshot pass deferred (local
  vite build broken by esm.sh monaco serving Node-builtin imports — pre-existing
  on main, see Open Issues).
- [x] **Phase 7 — Flutter glass foundation (DONE)**: `KubeColors` + 5 glass tokens
  (copyWith/lerp/builder wiring), new `widgets/glass_container.dart` (BackdropFilter
  primitive, high-contrast fallback), `confirm_sheet.dart` glassified (transparent
  sheet + glassScrim barrier + GlassContainer).
- [x] **Phase 8 — Flutter surfaces (DONE, sub-agent)**: cluster_picker_sheet,
  action_sheet, scale_sheet, eso/bulk_refresh_sheet, scanning namespace picker,
  secret_screens copy-confirm dialog — all on the GlassContainer pattern.
  Full `flutter test`: 1227 passed / 20 skipped; analyze clean; themes in sync.
- [ ] **Phase 9 — verification + ship (IN PROGRESS)**: PR via feature branch
  `feat/liquid-glass-redesign`, CI watch, `/ce:review` before merge, homelab smoke.
  AppBar glass intentionally deferred (needs extendBodyBehindAppBar across 42
  screens — separate effort if wanted).

## Open Issues (pre-existing, NOT caused by redesign)
- Local Windows `deno task dev` / `deno task build` broken: esm.sh now serves
  monaco-editor 0.52.2 denonext build importing Node builtins → fresh
  check-imports rejects; dev server separately fails `createDefine` SSR import.
  Both reproduce on clean main. CI (Linux) was green at last merge. Track as its
  own issue — may hit CI when esm.sh cache rolls over.
- Local `deno check` shows 40 pre-existing type errors on main (local Deno newer/
  stricter than CI pin); redesign added zero (verified by baseline diff).

## Invariants
- `shared/themes/*.json` stays the single source of truth; never hand-edit generated files.
- Data tables / YAML / logs / terminal stay SOLID (`bgSurface`) — glass is chrome-only.
- Every glass surface needs the `@supports` + reduced-transparency fallbacks.
- Old persisted theme ids (`nexus` etc.) must keep falling back gracefully on both platforms.
