/** @jsxImportSource react */
import { useEffect, useRef, useState } from "react";
import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import { apiGet } from "@/lib/api.ts";
import type { K8sResource, Secret } from "@/lib/k8s-types.ts";
import { decodeSecretValue, type RevealedValue } from "@/lib/secret-reveal.ts";

/** The reveal endpoint's payload: the key and its base64-encoded bytes. */
interface RevealResponse {
  key: string;
  value: string;
}

function revealPath(namespace: string, name: string, key: string): string {
  return `/v1/resources/secrets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/reveal/${encodeURIComponent(key)}`;
}

/**
 * Secret detail overview. Values stay masked (the GET serves `****`); each key
 * has its own Reveal, which calls the per-key reveal endpoint so every
 * disclosure is audited server-side, as on mobile. Revealed values live only
 * in this component's state and are dropped on Hide or when the page goes.
 */
export function SecretOverview({ resource }: { resource: K8sResource }) {
  const s = resource as Secret;
  const entries = Object.entries(s.data ?? {}).sort(([a], [b]) =>
    a.localeCompare(b),
  );
  const [revealed, setRevealed] = useState<Record<string, RevealedValue>>({});
  const [revealing, setRevealing] = useState<ReadonlySet<string>>(new Set());
  const [errors, setErrors] = useState<Record<string, string>>({});
  // The in-flight guard has to hold between two clicks in the same tick, so it
  // cannot wait for a render; `revealing` is its rendered copy.
  const inFlight = useRef(new Set<string>());

  const namespace = s.metadata.namespace ?? "";
  const name = s.metadata.name;

  // The page updates the Secret live; a value revealed before an edit would
  // otherwise keep showing the old data.
  // A reveal that was in flight across an edit is dropped too: it read the
  // old data.
  const version = s.metadata.resourceVersion;
  const currentVersion = useRef(version);
  currentVersion.current = version;
  const resetFor = useRef(version);
  useEffect(() => {
    if (resetFor.current === version) return;
    resetFor.current = version;
    setRevealed({});
    setErrors({});
  }, [version]);

  const reveal = async (key: string) => {
    if (inFlight.current.has(key)) return;
    inFlight.current.add(key);
    setRevealing((prev) => new Set(prev).add(key));
    setErrors(({ [key]: _, ...rest }) => rest);
    const requestedFor = currentVersion.current;
    try {
      const res = await apiGet<RevealResponse>(
        revealPath(namespace, name, key),
      );
      if (currentVersion.current !== requestedFor) return;
      const decoded = decodeSecretValue(res.data.value);
      setRevealed((prev) => ({ ...prev, [key]: decoded }));
    } catch (err) {
      if (currentVersion.current !== requestedFor) return;
      const message = err instanceof Error ? err.message : "Reveal failed";
      setErrors((prev) => ({ ...prev, [key]: message }));
    } finally {
      inFlight.current.delete(key);
      setRevealing((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
    }
  };

  const hide = (key: string) => {
    setRevealed(({ [key]: _, ...rest }) => rest);
  };

  return (
    <div className="space-y-4">
      {/* Type */}
      <div>
        <SectionHeader>Summary</SectionHeader>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Type" value={s.type ?? "Opaque"} mono />
          <Field label="Keys" value={String(entries.length)} />
        </div>
      </div>

      {/* Data keys: masked until revealed one at a time */}
      {entries.length > 0 && (
        <div>
          <SectionHeader>Data</SectionHeader>
          <div className="overflow-x-auto rounded-md border border-border-primary">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary">
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Key
                  </th>
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Value
                  </th>
                  <th className="px-3 py-1.5" />
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {entries.map(([key]) => {
                  const value = revealed[key];
                  const busy = revealing.has(key);
                  const error = errors[key];
                  return (
                    <tr key={key}>
                      <td className="px-3 py-1.5 align-top font-mono text-xs text-accent whitespace-nowrap">
                        {key}
                      </td>
                      <td className="px-3 py-1.5 font-mono text-xs">
                        {value ? (
                          <div>
                            <pre className="whitespace-pre-wrap break-all text-text-primary">
                              {value.text}
                            </pre>
                            {value.binary && (
                              <span className="text-text-muted">
                                Binary data, shown as base64
                              </span>
                            )}
                          </div>
                        ) : (
                          <span className="text-text-muted">****</span>
                        )}
                        {error && (
                          <p className="mt-1 font-sans text-danger">{error}</p>
                        )}
                      </td>
                      <td className="px-3 py-1.5 text-right align-top">
                        <button
                          type="button"
                          onClick={() => (value ? hide(key) : reveal(key))}
                          disabled={busy}
                          aria-label={`${value ? "Hide" : "Reveal"} ${key}`}
                          className="rounded border border-border-primary px-2 py-0.5 text-xs text-text-secondary hover:bg-hover hover:text-text-primary disabled:opacity-50"
                        >
                          {busy ? "Revealing…" : value ? "Hide" : "Reveal"}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <p className="mt-1 text-xs text-text-muted">
            Each reveal is recorded in the audit log.
          </p>
        </div>
      )}
    </div>
  );
}
