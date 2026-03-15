import { define } from "@/utils.ts";
import LoginForm from "@/islands/LoginForm.tsx";
import AuthProviderButtons from "@/islands/AuthProviderButtons.tsx";
import { Logo } from "@/components/ui/Logo.tsx";

export default define.page(function LoginPage() {
  return (
    <div class="flex min-h-full items-center justify-center bg-slate-50 px-4 dark:bg-slate-900">
      <div class="w-full max-w-sm">
        {/* Logo */}
        <div class="mb-8 text-center">
          <Logo size={48} class="mx-auto text-brand" />
          <h1 class="mt-4 text-2xl font-bold text-slate-900 dark:text-white">
            k8sCenter
          </h1>
          <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Sign in to manage your cluster
          </p>
        </div>

        {/* Login card */}
        <div class="rounded-lg border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-800">
          <LoginForm />
          <AuthProviderButtons />
        </div>
      </div>
    </div>
  );
});
