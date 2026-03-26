import { HttpError } from "fresh";
import { define } from "@/utils.ts";

export default define.page(function ErrorPage({ error, url }) {
  let status = 500;
  let message = "Something went wrong";

  if (error instanceof HttpError) {
    status = error.status;
    if (status === 404) {
      message = "Page not found";
    } else if (status === 403) {
      message = "Access denied";
    }
  }

  const is404 = status === 404;
  const is403 = status === 403;
  const isServerError = status >= 500;

  return (
    <div class="flex min-h-full items-center justify-center p-6">
      <div class="text-center">
        <p class="text-6xl font-bold text-text-muted">
          {status}
        </p>
        <h1 class="mt-4 text-xl font-semibold text-text-primary">
          {message}
        </h1>
        <p class="mt-2 text-sm text-text-secondary">
          {is404
            ? "The page you're looking for doesn't exist."
            : is403
            ? "You don't have permission to access this page."
            : "An unexpected error occurred. Please try again."}
        </p>
        <div class="mt-6 flex items-center justify-center gap-3">
          {isServerError && (
            <a
              href={url.pathname}
              class="inline-flex items-center rounded-md bg-brand px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-brand/90"
            >
              Retry
            </a>
          )}
          <a
            href="javascript:history.back()"
            class="inline-flex items-center rounded-md border border-border-primary bg-surface px-4 py-2 text-sm font-medium text-text-secondary shadow-sm hover:bg-hover"
          >
            Go Back
          </a>
          <a
            href="/"
            class="inline-flex items-center rounded-md border border-border-primary bg-surface px-4 py-2 text-sm font-medium text-text-secondary shadow-sm hover:bg-hover"
          >
            Dashboard
          </a>
        </div>
      </div>
    </div>
  );
});
