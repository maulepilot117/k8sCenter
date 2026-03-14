import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { BACKEND_URL } from "@/lib/constants.ts";

interface ProviderInfo {
  id: string;
  type: string;
  displayName: string;
  loginURL?: string;
}

/**
 * Renders SSO buttons for configured OIDC providers and a provider selector
 * for credential-based providers (local + LDAP).
 *
 * Fetches the provider list from /api/v1/auth/providers on mount.
 * Only renders if there are OIDC or multiple credential providers configured.
 */
export default function AuthProviderButtons() {
  const providers = useSignal<ProviderInfo[]>([]);
  const loading = useSignal(true);

  useEffect(() => {
    if (!IS_BROWSER) return;

    api<ProviderInfo[]>("/v1/auth/providers", { method: "GET" })
      .then((res) => {
        providers.value = res.data;
      })
      .catch(() => {
        // Silently fail — local login form is always available
      })
      .finally(() => {
        loading.value = false;
      });
  }, []);

  if (loading.value || !IS_BROWSER) return null;

  const oidcProviders = providers.value.filter((p) => p.type === "oidc");

  // Don't render anything if there are no OIDC providers
  if (oidcProviders.length === 0) return null;

  return (
    <div class="mt-5">
      {/* Divider */}
      <div class="relative my-4">
        <div class="absolute inset-0 flex items-center">
          <div class="w-full border-t border-slate-200 dark:border-slate-700" />
        </div>
        <div class="relative flex justify-center text-xs">
          <span class="bg-white px-2 text-slate-500 dark:bg-slate-800 dark:text-slate-400">
            or continue with
          </span>
        </div>
      </div>

      {/* OIDC provider buttons */}
      <div class="space-y-2">
        {oidcProviders.map((provider) => (
          <a
            key={provider.id}
            href={provider.loginURL}
            class="block"
          >
            <Button
              type="button"
              variant="secondary"
              size="lg"
              class="w-full"
            >
              Sign in with {provider.displayName}
            </Button>
          </a>
        ))}
      </div>
    </div>
  );
}
