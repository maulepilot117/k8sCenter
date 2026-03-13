import { define } from "@/utils.ts";
import { BACKEND_URL } from "@/lib/constants.ts";

/**
 * WebSocket proxy route — upgrades browser WS connections and relays
 * bidirectionally to the Go backend WebSocket endpoint.
 *
 * Browser connects to: ws://host/ws/v1/ws/resources
 * This proxy connects to: ws://backend:8080/api/v1/ws/resources
 */
export const handler = define.handlers({
  GET(ctx) {
    const path = ctx.params.path;

    // Validate path — only allow v1/ prefixed, no traversal
    if (!path.startsWith("v1/") || /\.\.|\/\/|%2e/i.test(path)) {
      return new Response(
        JSON.stringify({ error: { code: 400, message: "Invalid WS path" } }),
        { status: 400, headers: { "Content-Type": "application/json" } },
      );
    }

    // Allowlist known WS endpoints (P2-087)
    const allowedPatterns = [
      /^v1\/ws\/resources$/,
      /^v1\/ws\/logs\/[^/]+\/[^/]+\/[^/]+$/,
      /^v1\/ws\/exec\/[^/]+\/[^/]+\/[^/]+$/,
      /^v1\/ws\/alerts$/,
    ];
    if (!allowedPatterns.some((p) => p.test(path))) {
      return new Response(
        JSON.stringify({
          error: { code: 404, message: "Unknown WS endpoint" },
        }),
        { status: 404, headers: { "Content-Type": "application/json" } },
      );
    }

    // Must be a WebSocket upgrade request
    if (ctx.req.headers.get("upgrade")?.toLowerCase() !== "websocket") {
      return new Response("Expected WebSocket upgrade", { status: 426 });
    }

    // Build backend WS URL
    const backendUrl = new URL(BACKEND_URL);
    const wsProto = backendUrl.protocol === "https:" ? "wss:" : "ws:";
    const backendWsUrl = `${wsProto}//${backendUrl.host}/api/${path}`;

    // Upgrade the client connection
    const { socket: clientSocket, response } = Deno.upgradeWebSocket(ctx.req);

    // Connect to backend once the client socket is open
    clientSocket.onopen = () => {
      const backendSocket = new WebSocket(backendWsUrl);
      const pendingMessages: string[] = [];

      // Queue client messages until backend is connected
      clientSocket.onmessage = (event) => {
        if (backendSocket.readyState === WebSocket.OPEN) {
          backendSocket.send(event.data);
        } else {
          pendingMessages.push(event.data);
        }
      };

      backendSocket.onopen = () => {
        // Flush queued messages (includes the auth token)
        for (const msg of pendingMessages) {
          backendSocket.send(msg);
        }
        pendingMessages.length = 0;

        // Relay backend messages to client
        backendSocket.onmessage = (event) => {
          if (clientSocket.readyState === WebSocket.OPEN) {
            clientSocket.send(event.data);
          }
        };
      };

      // If backend closes, close client
      backendSocket.onclose = (event) => {
        if (clientSocket.readyState === WebSocket.OPEN) {
          clientSocket.close(event.code, event.reason);
        }
      };

      backendSocket.onerror = () => {
        if (clientSocket.readyState === WebSocket.OPEN) {
          clientSocket.close(1011, "Backend connection error");
        }
      };

      // If client closes, close backend (propagate close code)
      clientSocket.onclose = (event) => {
        if (backendSocket.readyState === WebSocket.OPEN) {
          backendSocket.close(event.code, event.reason);
        }
      };

      clientSocket.onerror = () => {
        if (backendSocket.readyState === WebSocket.OPEN) {
          backendSocket.close();
        }
      };
    };

    return response;
  },
});
