/** Shared validation constants used across frontend wizard steps and backend validators. */

/** Maximum number of replicas for a deployment. */
export const MAX_REPLICAS = 1000;

/** Maximum valid port number. */
export const MAX_PORT = 65535;

/** Minimum NodePort value (k8s default range). */
export const MIN_NODE_PORT = 30000;

/** Maximum NodePort value (k8s default range). */
export const MAX_NODE_PORT = 32767;

/** Maximum probe path length. */
export const MAX_PROBE_PATH_LENGTH = 1024;

/** Maximum username length for login form. */
export const MAX_USERNAME_LENGTH = 255;

/** Maximum password length for login form. */
export const MAX_PASSWORD_LENGTH = 255;

/**
 * DNS label regex for k8s resource names.
 * Lowercase alphanumeric with hyphens, 1-63 characters.
 */
export const DNS_LABEL_REGEX = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

/** Namespace name regex — alias of DNS_LABEL_REGEX. */
export const NS_NAME_REGEX = DNS_LABEL_REGEX;

/** Env var name regex (POSIX). */
export const ENV_VAR_NAME_REGEX = /^[A-Za-z_][A-Za-z0-9_]*$/;

/**
 * IANA service name regex for k8s port names.
 * Lowercase alphanumeric and hyphens, max 15 chars, at least one letter,
 * cannot start or end with hyphen, no consecutive hyphens.
 */
export const PORT_NAME_REGEX = /^[a-z]([a-z0-9-]{0,13}[a-z0-9])?$|^[a-z0-9]$/;

/** StorageClass item shape returned by the API. */
export interface StorageClassItem {
  metadata: { name: string };
  provisioner?: string;
}

/** PVC access mode options for wizard radio groups. */
export const ACCESS_MODES = [
  {
    value: "ReadWriteOnce",
    label: "ReadWriteOnce",
    desc: "Single node read-write",
  },
  {
    value: "ReadWriteMany",
    label: "ReadWriteMany",
    desc: "Multi-node read-write",
  },
  {
    value: "ReadOnlyMany",
    label: "ReadOnlyMany",
    desc: "Multi-node read-only",
  },
  {
    value: "ReadWriteOncePod",
    label: "ReadWriteOncePod",
    desc: "Single pod read-write",
  },
];

/** Restart policy options for Job and CronJob wizards. */
export const RESTART_POLICY_OPTIONS = [
  { value: "Never", label: "Never" },
  { value: "OnFailure", label: "OnFailure" },
];

/** Standard Tailwind input class for wizard form fields. */
export const WIZARD_INPUT_CLASS =
  "mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand";

