/** Backup assurance — freshness policy form model and pure helpers
 * (Release F U36). The form holds minutes as typed; the local checks mirror
 * the handler's and the server stays authoritative.
 */

import {
  type AssurancePolicy,
  type AssuranceScopeKind,
  DEFAULT_GRACE_SECONDS,
  MIN_MAX_AGE_SECONDS,
  type TreatPartialAs,
} from "@/lib/backup-assurance-types.ts";

export interface PolicyForm {
  scopeKind: AssuranceScopeKind;
  scopeNamespace: string;
  scopeName: string;
  /** Minutes, as typed. */
  maxAgeMinutes: string;
  graceMinutes: string;
  treatPartialAs: TreatPartialAs;
  alertOnPaused: boolean;
  enabled: boolean;
}

export function emptyForm(): PolicyForm {
  return {
    scopeKind: "schedule",
    scopeNamespace: "",
    scopeName: "",
    maxAgeMinutes: String(24 * 60),
    graceMinutes: String(DEFAULT_GRACE_SECONDS / 60),
    treatPartialAs: "failure",
    alertOnPaused: true,
    enabled: true,
  };
}

export function formFromPolicy(p: AssurancePolicy): PolicyForm {
  return {
    scopeKind: p.scopeKind,
    scopeNamespace: p.scopeNamespace,
    scopeName: p.scopeName,
    maxAgeMinutes: String(p.maxAgeSeconds / 60),
    graceMinutes: String(p.graceSeconds / 60),
    treatPartialAs: p.treatPartialAs,
    alertOnPaused: p.alertOnPaused,
    enabled: p.enabled,
  };
}

/** Minutes as typed → whole seconds, or null when not a number. */
export function minutesToSeconds(raw: string): number | null {
  const n = Number(raw.trim());
  if (raw.trim() === "" || !Number.isFinite(n)) return null;
  return Math.round(n * 60);
}

/** Client-side checks that mirror the handler's; the server stays authoritative. */
export function localFieldErrors(
  f: PolicyForm,
  creating: boolean,
): Record<string, string> {
  const errs: Record<string, string> = {};
  if (creating) {
    if (f.scopeKind !== "cluster" && f.scopeNamespace.trim() === "") {
      errs.scopeNamespace = "is required";
    }
    if (f.scopeKind === "schedule" && f.scopeName.trim() === "") {
      errs.scopeName = "is required for schedule scope";
    }
  }
  const maxAge = minutesToSeconds(f.maxAgeMinutes);
  if (maxAge === null) errs.maxAgeSeconds = "must be a number of minutes";
  else if (maxAge < MIN_MAX_AGE_SECONDS) {
    errs.maxAgeSeconds = `must be at least ${MIN_MAX_AGE_SECONDS / 60} minutes`;
  }
  const grace = minutesToSeconds(f.graceMinutes);
  if (grace === null) errs.graceSeconds = "must be a number of minutes";
  else if (grace < 0) errs.graceSeconds = "must not be negative";
  return errs;
}

export const FIELD_LABELS: Record<string, string> = {
  scopeKind: "Scope",
  scopeNamespace: "Namespace",
  scopeName: "Schedule",
  maxAgeSeconds: "Maximum age",
  graceSeconds: "Grace",
  treatPartialAs: "Treat PartiallyFailed as",
  alertOnPaused: "Alert on paused",
  enabled: "Enabled",
  revision: "Revision",
};
