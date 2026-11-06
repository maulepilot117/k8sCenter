/**
 * Client-only. Starts the page's token refresh as soon as the chrome loads.
 *
 * After a page load the access token (kept in memory only) is gone, so every
 * island's first request used to go out without it, take a 401, and wait on
 * the refresh that the first 401 started: about 17 failed requests per page
 * load. With the refresh started here, before the islands hydrate, those
 * requests wait on it instead (`send` in lib/api.ts joins a refresh in
 * flight) and each is sent once, with the token.
 *
 * Only the chrome page runs this: login, setup and the OIDC callback need no
 * session, and the refresh shares their 5/min rate limit.
 */
import { getAccessToken, refreshSession } from "@/lib/api.ts";

export function bootSession(): Promise<boolean> | null {
  if (getAccessToken()) return null;
  return refreshSession();
}
