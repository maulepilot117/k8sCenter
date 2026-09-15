/**
 * Operator-facing copy for the three error surfaces, ported verbatim from
 * the `HttpError` branch of `frontend/routes/_error.tsx`. Kept as data
 * (rather than repeated inline in three `.astro` files) so
 * `src/pages/404.astro`, `403.astro` and `500.astro` — and any future
 * caller — can't drift from each other on wording.
 */
export interface ErrorCopy {
  status: 404 | 403 | 500;
  heading: string;
  description: string;
  /** Only the 5xx surface offers a "Retry" link back to the failing URL. */
  showRetry: boolean;
}

export const ERROR_COPY: Record<"404" | "403" | "500", ErrorCopy> = {
  "404": {
    status: 404,
    heading: "Page not found",
    description: "The page you're looking for doesn't exist.",
    showRetry: false,
  },
  "403": {
    status: 403,
    heading: "Access denied",
    description: "You don't have permission to access this page.",
    showRetry: false,
  },
  "500": {
    status: 500,
    heading: "Something went wrong",
    description: "An unexpected error occurred. Please try again.",
    showRetry: true,
  },
};
