---
title: Moving the frontend from Preact to React 19 through a compat bridge, and what broke at the switch
date: 2026-11-10
last_updated: 2026-11-12
category: docs/solutions
module: frontend
problem_type: migration
component: frontend
severity: high
related_components: [frontend_islands, test_harness, build_guards]
tags: [react, preact, preact-compat, hydration, astro, signals, testing-library, happy-dom, use-is-client, migration]
applies_when:
  - "Adding or changing an island, especially one whose first render reads a store, storage, the URL or the window"
  - "Writing an island test, or a test that hydrates server markup"
  - "Moving another codebase from Preact to React in stages"
  - "A page logs a React hydration error (#418, #419, #421, #422, #423, #425)"
---

# Moving the frontend from Preact to React 19 through a compat bridge

The frontend moved from Preact to React 19 over eight PRs (plan
`docs/plans/2026-11-07-1219-refactor-react-migration-recharts-plan.md`,
PR1 to PR8). Source files moved to React APIs one group at a time while
everything still ran on Preact through `preact/compat`; PR8 then switched the
runtime in one commit. This records what the bridge needed to work, what
broke when the real React arrived, and the conventions that came out of it.

## What the bridge needed

**One alias in every tool.** `tsconfig.json` `paths` (read by `astro check`
and `bun test`) and `vite.resolve.alias` (read by `astro dev` and the build)
mapped `react`, `react-dom/*` and the JSX runtimes to `preact/compat`, with
the same map in both. The Vite entries were anchored regexes so `react` never
rewrote `react-dom/*` or `react-*` packages. With one runtime everywhere, a
converted file could render an unconverted one and the reverse, so no PR had
to convert a whole dependency chain at once. Running `@astrojs/preact` and
`@astrojs/react` side by side was rejected: a shared component can only
belong to one runtime.

**A guard with no baseline.** `check-react-bridge` failed on a file importing
both `react` and Preact, or using a Preact-only JSX attribute (`class=`,
`for=`, `onFocusOut`, hyphenated SVG attributes) in a file that imports
`react`; Preact's types accept all of those, so only the guard stopped them.
It printed the count of files still on Preact APIs as progress. With no
baseline file, parallel conversion PRs never conflicted on it. At the switch
it became `check-no-preact`. During the bridge each converted file opened with
`/** @jsxImportSource react */`; once `tsconfig.json` set React's runtime for
every file those pragmas only repeated it, so the cleanup PR deleted all of
them, and the guard now fails on any `@jsxImportSource` pragma anywhere in a
file (Bun still honours one placed after the first statement).

**Stores stayed signals, read through one hook.** Store modules used
`@preact/signals-core` through the switch (since replaced by an in-repo
store, see "Replacing signals-core" below), read in render only through
`useSignalValue` (`useSyncExternalStore` with a stable `subscribe`). Two
details mattered:

- The hook's server snapshot returns the signal's current value, not a fixed
  default. With a default, the hydration render's effects would fetch for
  all namespaces or the local cluster while a remote one is selected.
- A bare `.value` read in render is not safe even on Preact: Astro's Preact
  renderer loads `@preact/signals` only when an island has signal props, so
  render tracking was not guaranteed once an island stopped importing it.

**Compat rewrites events globally, and tests saw it.** Preact 11's compat
rewrites `onChange` on text inputs to `oninput`, `onFocus`/`onBlur` to the
bubbling `focusin`/`focusout`, and `onDoubleClick` to `ondblclick`. With
compat loaded, Testing Library's `fireEvent.change` on a text field fires an
`input` event, so a test that means a commit has to dispatch a real `change`
event. The one text field that relied on commit semantics (the vulnerability
page jump) uses a native `change` listener.

**Duplicate packages break identity.** Adding `@preact/signals-core` left a
nested older copy under `@preact/signals`; two copies mean two signal
classes, and 19 tests failed. An `overrides` entry pins one copy, and a
stale nested `node_modules` copy that `bun install` reported as "no changes"
had to be deleted by hand. The same rule now covers `react`, `react-dom`,
`@preact/signals-core` and `@codemirror/*` in `vite.resolve.dedupe`; the
signal entry left with the package.

**Test isolation.** Bun shares modules across test files in one process, so
a `mock.module` that is not restored leaks into later files (four island
tests mocked `ToastProvider.tsx` that way). happy-dom is registered per file
through `setupDom()`, not a global preload, because it replaces `fetch`,
`Request`, `Response` and `WebSocket` for the server tests; queries go
through `within(document.body)`, because `screen` binds before per-file
registration.

## What broke at the switch

**React nulls `currentTarget` after the handler returns.**
`NetworkPolicyWizard` read `e.currentTarget.value` inside a `setState`
updater. Preact runs the updater synchronously; React runs it later, when
`currentTarget` is already `null`. Read the value into a local before calling
the setter.

**`createElement` children passed as an array need keys.**
`lib/resource-columns.ts` passed children as one array argument, which React
treats as a list and warns about. Pass children as separate arguments.

**react-dom must load with a document present and Bun's own timers.**
react-dom decides once, when it first loads, whether the `input` event
exists; loaded without a document it falls back to an old-IE polyfill and
typing in a test does nothing while clicks still work. It also keeps the
timer functions it finds at load, and happy-dom's stop firing once the window
is removed, after which no update outside `act()` renders. Bun evaluates
CommonJS packages ahead of the importing module, so only a preload is early
enough: `src/test-utils/react-dom-env.ts` (`bunfig.toml` `[test] preload`)
registers happy-dom, restores Bun's timers, loads react-dom and Testing
Library, and unregisters happy-dom again.

**Test semantics changed.** About 30 test files moved: `fireEvent.input` for
typing, `click` for checkboxes, awaited `act`, the real `focusout` /
`mouseout` event names, and `renderToString` comment markers in SSR
assertions. `setupDom()` now fails any test that leaves an `act()` scope
open and drains React's scheduler before unregistering happy-dom, because a
leaked scope makes later files hang or pass vacuously.

**`useId` is per island.** `@astrojs/react` gives every island root its own
`identifierPrefix`, so two islands rendering the same chart get different
gradient ids. A test that hydrates two roots must pass its own prefix
(`src/test-utils/hydrate.tsx` takes one).

## Hydration: the browser's first render must equal the server's

Preact hydrated over server markup that differed from its first render
without complaint. React treats a text mismatch as an error: it discards the
island's server HTML, renders it again on the client and logs a hydration
error (#418 in production builds). An attribute-only mismatch is silent in a
production build: React keeps the server's attribute and logs nothing.

Rules that came out of the hardening wave (PR8 U13):

- **Never ask whether the code runs in a browser while rendering.**
  `IS_BROWSER`, `hasDom()` and `typeof window` in a component or hook body,
  in a helper the render calls, or in a lazy `useState` / `useReducer`
  initialiser all count: React calls initialisers during the hydration
  render. Gate client-only markup on `useIsClient()` (server snapshot
  `false`), or move the check into an effect or handler.
- **An effect that mounts a library into a client-only ref** lists `isClient`
  in its dependencies, or uses a ref callback. Otherwise it runs once while
  the ref is still `null` and never mounts (CodeMirror, Monaco).
- **A create form starts on `DEFAULT_NAMESPACE`** on both sides and adopts
  the picker's namespace after hydration through `useAdoptSelectedNamespace`
  (`src/lib/namespace.ts`). Starting on the stored selection made 18
  wizards throw #418 whenever the browser had restored a namespace. An island
  whose markup is entirely behind `useIsClient()` may read the selection at
  state creation through `selectedNamespaceOrDefault()`.
- **URL parameters come in as props** from the Astro page
  (`Astro.url.searchParams`), so server and client agree.
  `ESOChainPage`, `VeleroRestoreWizard`, `ViolationBrowser` and topology do
  this.
- **Notices that depend on a restored store** (the remote-cluster notice on
  the alerting pages) render once hydrated.
- **A fetch effect whose input changes right after hydration** (the adopted
  namespace) ignores a stale response with a `current` flag in its cleanup.

`frontend/server/check-no-render-is-browser.ts` enforces the first rule. It
reads one file at a time, so it does not see a browser read inside an
imported function or a module-scope store rendered without
`useSignalValue`. The e2e page-error fixture (no allow-list since PR8)
catches the text mismatches those cause. Nothing catches an attribute-only
mismatch in a production build, so review render paths that read restored
state with that in mind.

The route sweep used to prove this loads every page twice on the built
server, once with fresh storage and once with a namespace and a remote
cluster restored, and collects page errors and React hydration messages. The
restored pass is the one that found the wizard bug; a fresh-storage sweep
alone reported nothing.

## Replacing signals-core

`@preact/signals-core` outlived the switch as the last Preact package. A
follow-up replaced it with `frontend/src/lib/signals.ts`, an in-repo store
with the same surface the stores used: `signal`, `computed`, `effect` and
`batch`, and `.value`, `.peek()` and `.subscribe()` on a signal. Islands
changed only their import path. What mattered:

- **The callers needed tracking, not just a value box.** `cluster.ts`,
  `namespace.ts` and `resource-counts.ts` persist or refetch from
  auto-tracked `effect`s, four modules derive values with `computed`, and
  `switchCluster` relies on `batch` so no subscriber sees a new cluster id
  beside the old generation. A plain value-and-listener store would have
  meant rewriting each of those by hand.
- **`subscribe` keeps signals-core's contract**: it calls back at once with
  the current value, then with each new value. Five callers depend on that
  first call (`PinnedResources` and `SavedViews` load on it, `ResourceDetail`
  and `yaml-apply` read the value argument), so `useSignalValue` and
  `useSourceStates` still skip it.
- **The switch had to be one change.** A `computed` or `effect` from one
  library cannot track a signal from the other, so a store moved alone
  would compile and then silently stop updating the islands that read it
  through another store.
- **The lockfile was edited by hand.** `bun install` re-resolves every
  locked version against the 7-day `minimumReleaseAge` gate and refused a
  recently published override, so the three `@preact/signals-core` entries
  were deleted from `bun.lock` instead; `bun install --frozen-lockfile`, as
  CI runs it, accepts the result.

`check-no-preact` now fails on any `@preact/*` import, signals-core
included, so a second signal library cannot come back by accident.

## Cost

First-load JavaScript, gzipped, measured with Playwright on built servers
(gzip level 9 over every same-origin script the page loads; no backend):

| Page | Before PR1 (Preact) | PR7 (compat bridge) | PR8 (React 19) | PR8 vs before PR1 |
|---|---|---|---|---|
| Dashboard | 101.6 KB | 104.0 KB | 162.9 KB | +61.3 KB |
| Pod detail | 217.5 KB | 220.1 KB | 279.5 KB | +62.0 KB |
| Deployment wizard | 54.1 KB | 56.1 KB | 115.3 KB | +61.2 KB |

The bridge itself cost about 2.5 KB per page. React and react-dom add about
61 KB to every page that hydrates an island, close to the plan's 65 KB
estimate and well inside its 200 KB stop condition for the dashboard. The
before-PR1 column was re-measured on the same machine and matched the numbers
recorded in PR1 exactly.

## Related

- Plan: `docs/plans/2026-11-07-1219-refactor-react-migration-recharts-plan.md`
- `docs/solutions/idempotent-create-client-request-id.md` (the incident create
  guard that stays a synchronously written ref)
- `docs/solutions/frontend-bun-image.md` (the production image that ships the
  compiled server)
