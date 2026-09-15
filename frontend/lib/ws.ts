/**
 * Re-export of the ported module. One instance, deliberately.
 *
 * Same defect as `lib/cluster.ts` — see that file for the full account. This
 * one carried a WebSocket connection, its subscription registry and the
 * `wsStatus` signal at module scope, and the built bundle contained two
 * copies: one inlined into `useWsRefetch` (9 src importers), one imported
 * directly by seven islands. Two sockets, two registries, and an island
 * subscribed through one of them could not see an event delivered to the
 * other.
 */
export * from "@/src/lib/ws.ts";
