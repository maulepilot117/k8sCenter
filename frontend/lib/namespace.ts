/**
 * Re-export of the ported module. One instance, deliberately.
 *
 * See `lib/cluster.ts` for the full account of why. This one held the
 * `selectedNamespace` signal and its localStorage persistence; `lib/auth.ts`
 * (9 src importers) reached this copy while the islands reached
 * `@/src/lib/namespace.ts`.
 *
 * The built bundle happened to contain only one copy of this particular
 * module, which is exactly why the rule is enforced on the source graph and
 * not on the output: whether Rollup deduplicates two byte-similar modules is
 * not a property to depend on for correctness.
 */
export * from "@/src/lib/namespace.ts";
