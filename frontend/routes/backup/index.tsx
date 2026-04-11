import { define } from "@/utils.ts";

export const handler = define.handlers({
  GET(_ctx) {
    return new Response(null, {
      status: 302,
      headers: { Location: "/backup/backups" },
    });
  },
});
