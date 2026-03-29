import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";

/**
 * Intercepts same-origin navigation clicks and performs soft page transitions.
 * Fetches the new page in the background, swaps the <main> content, and
 * optionally uses the View Transitions API for smooth animation.
 * Falls back to normal navigation for external links, downloads, etc.
 */
export default function SmoothNav() {
  useEffect(() => {
    if (!IS_BROWSER) return;
    let abortController: AbortController | null = null;

    function shouldIntercept(anchor: HTMLAnchorElement): boolean {
      // Only intercept same-origin, same-target links
      if (anchor.origin !== location.origin) return false;
      if (anchor.target && anchor.target !== "_self") return false;
      if (anchor.download) return false;
      if (anchor.pathname === location.pathname) return false;
      // Skip auth routes (they have different layouts)
      if (
        anchor.pathname === "/login" || anchor.pathname === "/setup" ||
        anchor.pathname.startsWith("/auth/")
      ) return false;
      return true;
    }

    async function navigate(href: string) {
      // Abort any in-flight navigation
      abortController?.abort();
      abortController = new AbortController();

      try {
        const resp = await fetch(href, {
          signal: abortController.signal,
          headers: { Accept: "text/html" },
        });
        if (!resp.ok) {
          // Fall back to normal navigation on error
          location.href = href;
          return;
        }
        const html = await resp.text();

        // Parse the fetched HTML and extract <main> content
        const parser = new DOMParser();
        const doc = parser.parseFromString(html, "text/html");
        const newMain = doc.querySelector("main");
        const currentMain = document.querySelector("main");

        if (!newMain || !currentMain) {
          location.href = href;
          return;
        }

        // Update the URL
        history.pushState(null, "", href);

        // Swap content with View Transition if available
        const swap = () => {
          currentMain.innerHTML = newMain.innerHTML;
          // Scroll to top
          currentMain.scrollTop = 0;
          // Re-run any island hydration scripts from the new content
          const scripts = currentMain.querySelectorAll("script");
          scripts.forEach((oldScript) => {
            const newScript = document.createElement("script");
            Array.from(oldScript.attributes).forEach((attr) =>
              newScript.setAttribute(attr.name, attr.value)
            );
            newScript.textContent = oldScript.textContent;
            oldScript.replaceWith(newScript);
          });
          // Update page title
          const newTitle = doc.querySelector("title");
          if (newTitle) document.title = newTitle.textContent ?? "";
          // Dispatch a popstate-like event so islands can react to URL change
          globalThis.dispatchEvent(new Event("smoothnav"));
        };

        if ("startViewTransition" in document) {
          // deno-lint-ignore no-explicit-any
          (document as any).startViewTransition(swap);
        } else {
          swap();
        }
      } catch (err) {
        if ((err as Error).name !== "AbortError") {
          // Fall back to normal navigation
          location.href = href;
        }
      }
    }

    function handleClick(e: MouseEvent) {
      // Skip if modifier keys (new tab, etc.)
      if (e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
      if (e.button !== 0) return;

      // Find the closest <a> ancestor
      const anchor = (e.target as Element).closest?.("a");
      if (!anchor || !shouldIntercept(anchor)) return;

      e.preventDefault();
      navigate(anchor.href);
    }

    function handlePopState() {
      navigate(location.href);
    }

    document.addEventListener("click", handleClick);
    globalThis.addEventListener("popstate", handlePopState);

    return () => {
      document.removeEventListener("click", handleClick);
      globalThis.removeEventListener("popstate", handlePopState);
      abortController?.abort();
    };
  }, []);

  return null;
}
