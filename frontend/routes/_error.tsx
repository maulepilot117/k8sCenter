import { HttpError } from "fresh";
import { define } from "@/utils.ts";
import { Button } from "@/components/ui/Button.tsx";

export default define.page(function ErrorPage({ error }) {
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

  return (
    <div class="flex min-h-full items-center justify-center p-6">
      <div class="text-center">
        <p class="text-6xl font-bold text-slate-300 dark:text-slate-600">
          {status}
        </p>
        <h1 class="mt-4 text-xl font-semibold text-slate-900 dark:text-white">
          {message}
        </h1>
        <p class="mt-2 text-sm text-slate-500 dark:text-slate-400">
          {status === 404
            ? "The page you're looking for doesn't exist."
            : "An unexpected error occurred. Please try again."}
        </p>
        <div class="mt-6">
          <a href="/">
            <Button variant="primary">Back to Dashboard</Button>
          </a>
        </div>
      </div>
    </div>
  );
});
