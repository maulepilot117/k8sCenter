// FROZEN from the Fresh tree on 2026-09-14, before U12 deletes it.
//
// U7 proved the anti-flash script and the error copy byte-identical by
// reading frontend/routes/_app.tsx and _error.tsx directly. That is the right
// check while both trees exist and a broken test the moment the Fresh tree is
// removed -- and a test that disappears with its source stops being a
// regression guard exactly when it starts being one, because from then on
// nothing else pins these strings at all.
//
// So the values are frozen here. The tests assert against this file always,
// and additionally assert this file still matches the Fresh source for as long
// as that source exists. After deletion the drift guard self-disables and the
// parity assertion survives.
//
// Do not edit these to make a test pass. They are a record of what the
// pre-migration UI rendered; changing one is a deliberate product change that
// belongs in its own commit with its own reasoning.

export const FRESH_ANTI_FLASH_SCRIPT =
  '(function(){try{var t=localStorage.getItem("kc.theme");if(t==="light")document.documentElement.classList.add("theme-light")}catch(e){}})();(function(){try{var a=localStorage.getItem("k8scenter-animations");if(a==="false")document.documentElement.classList.add("no-animations")}catch(e){}})()';

export const FRESH_ERROR_HEADINGS: Record<string, string> = {
  "403": "Access denied",
  "404": "Page not found",
  "500": "Something went wrong",
};

export const FRESH_ERROR_DESCRIPTIONS: Record<string, string> = {
  "403": "You don't have permission to access this page.",
  "404": "The page you're looking for doesn't exist.",
  "500": "An unexpected error occurred. Please try again.",
};
