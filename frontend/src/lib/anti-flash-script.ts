/**
 * Verbatim copy of the pre-hydration theme/animation script from the Fresh
 * build's `frontend/routes/_app.tsx`. It reads two localStorage keys
 * (`kc.theme`, `k8scenter-animations`) and toggles two `<html>` classes
 * before first paint, which is the entire anti-flash mechanism (see
 * `assets/styles.css`'s `!important` background rules, which this script's
 * classes interact with). KTD13 requires this be byte-identical across the
 * Fresh and Astro builds — see `anti-flash-script_test.ts`, which extracts
 * the original string straight out of `_app.tsx` and compares it to this
 * one rather than trusting a hand-copy.
 *
 * It MUST stay inline and unprocessed — `frontend/src/layouts/BaseLayout.astro`
 * renders it with the `is:inline` directive so Astro does not bundle,
 * defer, or relocate it (any of those reintroduces the flash). Do not move
 * it out of `<head>` or place any stylesheet before it.
 */
// deno fmt and biome format disagree on how to wrap this one-line literal
// (deno wants it split across two lines, biome wants it on one) -- rather
// than let the two toolchains fight over it every time either formatter
// runs, both are told to leave it alone. The line is intentionally
// unbroken so it stays trivially diffable against the original in
// routes/_app.tsx.
// deno-fmt-ignore
// biome-ignore format: deno fmt and biome disagree on wrapping this line; see comment above
export const ANTI_FLASH_SCRIPT =
  `(function(){try{var t=localStorage.getItem("kc.theme");if(t==="light")document.documentElement.classList.add("theme-light")}catch(e){}})();(function(){try{var a=localStorage.getItem("k8scenter-animations");if(a==="false")document.documentElement.classList.add("no-animations")}catch(e){}})()`;
