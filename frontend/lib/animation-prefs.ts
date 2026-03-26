// CLIENT-ONLY MODULE — Do NOT import in server-rendered components.
// This module uses browser APIs (document, localStorage) and Preact signals.

import { signal } from "@preact/signals";

const STORAGE_KEY = "k8scenter-animations";

export const animationsEnabled = signal<boolean>(true);

export function setAnimations(enabled: boolean): void {
  animationsEnabled.value = enabled;

  if (enabled) {
    document.documentElement.classList.remove("no-animations");
  } else {
    document.documentElement.classList.add("no-animations");
  }

  try {
    localStorage.setItem(STORAGE_KEY, String(enabled));
  } catch {
    // localStorage may be unavailable
  }
}

export function initAnimationPrefs(): void {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "false") {
      animationsEnabled.value = false;
      document.documentElement.classList.add("no-animations");
    }
  } catch {
    // localStorage may be unavailable
  }
}
