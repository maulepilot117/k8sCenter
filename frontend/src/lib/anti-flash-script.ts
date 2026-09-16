/**
 * Verbatim copy of the pre-hydration theme/animation script from the Fresh
 * build's `frontend/routes/_app.tsx`. It reads two localStorage keys
 * (`kc.theme`, `k8scenter-animations`) and toggles two `<html>` classes
 * before first paint, which is the entire anti-flash mechanism (see
 * `assets/styles.css`'s `!important` background rules, which this script's
 * classes interact with). KTD13 requires this be byte-identical to what the
 * Fresh build shipped — see `anti-flash-script_test.ts`, which compares it
 * against the frozen original in `__fixtures__/fresh-parity.ts` rather than
 * trusting a hand-copy.
 *
 * It MUST stay inline and unprocessed — `frontend/src/layouts/BaseLayout.astro`
 * renders it with the `is:inline` directive so Astro does not bundle,
 * defer, or relocate it (any of those reintroduces the flash). Do not move
 * it out of `<head>` or place any stylesheet before it.
 */
// The literal is intentionally left unbroken so it stays trivially diffable
// against the frozen Fresh original, so the formatter is told to leave it
// alone.
// biome-ignore format: keep this literal on one line; see comment above
export const ANTI_FLASH_SCRIPT =
  `(function(){try{var t=localStorage.getItem("kc.theme");if(t==="light")document.documentElement.classList.add("theme-light")}catch(e){}})();(function(){try{var a=localStorage.getItem("k8scenter-animations");if(a==="false")document.documentElement.classList.add("no-animations")}catch(e){}})()`;
