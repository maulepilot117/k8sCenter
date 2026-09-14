/**
 * U9 harness-only helper — NOT one of the 152 islands, not counted in any
 * unit's port total. Deleted along with frontend/src/dev-harness/ and
 * frontend/src/pages/u9-harness.astro once U8 mounts real islands on real
 * pages and no longer needs a place to observe hydration in isolation.
 *
 * Proves Astro's client-directive prop channel handles the two structured
 * types this codebase actually uses for Kubernetes data (Date for
 * timestamps, Map for label/annotation-shaped data) without a manual
 * (de)serialization step, and that a string prop containing HTML
 * metacharacters renders as inert text rather than executing.
 */
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

interface PropProbeProps {
  createdAt: Date;
  labels: Map<string, string>;
  dangerousName: string;
}

export default function PropProbe({
  createdAt,
  labels,
  dangerousName,
}: PropProbeProps) {
  const isDate = createdAt instanceof Date;
  const isMap = labels instanceof Map;
  return (
    <div style={{ border: "1px solid gray", padding: "8px", margin: "8px" }}>
      <p>PropProbe hydrated: {String(IS_BROWSER)}</p>
      <p data-testid="date-check">
        createdAt instanceof Date: {String(isDate)} — value:{" "}
        {isDate ? createdAt.toISOString() : String(createdAt)}
      </p>
      <p data-testid="map-check">
        labels instanceof Map: {String(isMap)} — size:{" "}
        {isMap ? labels.size : "n/a"} — entry:{" "}
        {isMap ? JSON.stringify([...labels.entries()]) : "n/a"}
      </p>
      {/* Rendered as text, never dangerouslySetInnerHTML — proves Preact/
          Astro escape this rather than letting it break out of the
          serialized payload or execute as script. */}
      <p data-testid="xss-check">dangerousName: {dangerousName}</p>
    </div>
  );
}
