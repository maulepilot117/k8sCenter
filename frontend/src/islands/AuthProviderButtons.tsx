/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { api } from "@/lib/api.ts";
import type { ProviderInfo } from "@/lib/k8s-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

/**
 * Renders SSO buttons for configured OIDC providers and a provider selector
 * for credential-based providers (local + LDAP).
 *
 * Fetches the provider list from /api/v1/auth/providers on mount.
 * Only renders if there are OIDC or multiple credential providers configured.
 */
export default function AuthProviderButtons() {
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const isClient = useIsClient();

  useEffect(() => {
    if (!IS_BROWSER) return;

    api<ProviderInfo[]>("/v1/auth/providers", { method: "GET" })
      .then((res) => {
        setProviders(res.data);
      })
      .catch(() => {
        // Silently fail — local login form is always available
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  if (loading || !isClient) return null;

  const oidcProviders = providers.filter((p) => p.type === "oidc");

  // Don't render anything if there are no OIDC providers
  if (oidcProviders.length === 0) return null;

  return (
    <div className="mt-5">
      {/* Divider */}
      <div className="relative my-4">
        <div className="absolute inset-0 flex items-center">
          <div className="w-full border-t border-border-primary" />
        </div>
        <div className="relative flex justify-center text-xs">
          <span className="bg-surface px-2 text-text-muted text-text-muted">
            or continue with
          </span>
        </div>
      </div>

      {/* OIDC provider buttons */}
      <div className="space-y-2">
        {oidcProviders.map((provider) => (
          <a key={provider.id} href={provider.loginURL} className="block">
            <Button
              type="button"
              variant="secondary"
              size="lg"
              className="w-full"
            >
              Sign in with {provider.displayName}
            </Button>
          </a>
        ))}
      </div>
    </div>
  );
}
