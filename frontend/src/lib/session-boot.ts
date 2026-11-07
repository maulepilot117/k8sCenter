import { getAccessToken, refreshSession } from "@/lib/api.ts";

export function bootSession(): void {
  if (!getAccessToken()) void refreshSession();
}
