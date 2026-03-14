import { define } from "@/utils.ts";
import OIDCCallbackHandler from "@/islands/OIDCCallbackHandler.tsx";

export default define.page(function OIDCCallbackPage() {
  return (
    <div class="flex min-h-full items-center justify-center bg-slate-50 px-4 dark:bg-slate-900">
      <div class="w-full max-w-sm text-center">
        <OIDCCallbackHandler />
      </div>
    </div>
  );
});
