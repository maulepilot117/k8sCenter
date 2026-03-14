import { define } from "@/utils.ts";

/**
 * BFF endpoint for OIDC token exchange.
 *
 * After the OIDC callback, the Go backend sets an httpOnly cookie
 * (`oidc_access_token`) containing the k8sCenter JWT access token.
 * This endpoint reads that cookie, returns the token in the response body,
 * and clears the cookie (single-use).
 *
 * This prevents the access token from being exposed in URL fragments
 * or query parameters during the OIDC redirect flow.
 */
export const handler = define.handlers({
  POST(ctx) {
    const cookieHeader = ctx.req.headers.get("cookie") ?? "";
    const cookies = parseCookies(cookieHeader);
    const accessToken = cookies["oidc_access_token"];

    if (!accessToken) {
      return new Response(
        JSON.stringify({
          error: { code: 401, message: "No OIDC token available" },
        }),
        { status: 401, headers: { "Content-Type": "application/json" } },
      );
    }

    // Clear the cookie
    const clearCookie =
      "oidc_access_token=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0";

    return new Response(
      JSON.stringify({
        data: { accessToken },
      }),
      {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Set-Cookie": clearCookie,
        },
      },
    );
  },
});

/** Simple cookie parser. */
function parseCookies(header: string): Record<string, string> {
  const cookies: Record<string, string> = {};
  for (const pair of header.split(";")) {
    const [key, ...rest] = pair.trim().split("=");
    if (key) {
      cookies[key] = rest.join("=");
    }
  }
  return cookies;
}
