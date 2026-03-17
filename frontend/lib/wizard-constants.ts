/** Shared validation constants used across frontend wizard steps and backend validators. */

/** Maximum number of replicas for a deployment. */
export const MAX_REPLICAS = 1000;

/** Maximum valid port number. */
export const MAX_PORT = 65535;

/** Minimum NodePort value (k8s default range). */
export const MIN_NODE_PORT = 30000;

/** Maximum NodePort value (k8s default range). */
export const MAX_NODE_PORT = 32767;

/** Maximum number of ports per service. */
export const MAX_PORTS = 20;

/** Maximum number of environment variables per container. */
export const MAX_ENV_VARS = 50;

/** Maximum file upload size in bytes (2 MB, matches backend MaxBodySize). */
export const MAX_BODY_BYTES = 2 * 1024 * 1024;

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
