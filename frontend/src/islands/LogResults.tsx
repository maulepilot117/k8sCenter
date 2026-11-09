/** @jsxImportSource react */
import type { LogLine } from "@/lib/types/logs.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface LogResultsProps {
  lines: LogLine[];
  loading: boolean;
}

const RE_ERROR = /\berror\b/i;
const RE_WARN = /\bwarn(ing)?\b/i;
const RE_DEBUG = /\bdebug\b/i;

function parseSeverity(line: string): string {
  const lower = line.toLowerCase();
  if (
    lower.includes('"level":"error"') ||
    lower.includes("level=error") ||
    RE_ERROR.test(line.slice(0, 100))
  )
    return "error";
  if (
    lower.includes('"level":"warn"') ||
    lower.includes("level=warn") ||
    RE_WARN.test(line.slice(0, 100))
  )
    return "warn";
  if (
    lower.includes('"level":"debug"') ||
    lower.includes("level=debug") ||
    RE_DEBUG.test(line.slice(0, 100))
  )
    return "debug";
  return "info";
}

function severityColor(severity: string): string {
  switch (severity) {
    case "error":
      return "text-danger font-semibold";
    case "warn":
      return "text-warning";
    case "debug":
      return "text-text-muted";
    default:
      return "text-accent";
  }
}

function formatTimestamp(nanoStr: string): string {
  try {
    const ms = parseInt(nanoStr, 10) / 1_000_000;
    return new Date(ms).toISOString().replace("T", " ").slice(0, 23);
  } catch {
    return nanoStr;
  }
}

export default function LogResults(props: LogResultsProps) {
  const isClient = useIsClient();
  if (!isClient) return null;

  const { lines, loading } = props;

  if (loading) {
    return (
      <div className="flex items-center justify-center rounded-lg border border-border-primary bg-surface p-12">
        <div className="text-sm text-text-muted">Searching logs...</div>
      </div>
    );
  }

  if (lines.length === 0) {
    return (
      <div className="flex items-center justify-center rounded-lg border border-border-primary bg-surface p-12">
        <div className="text-sm text-text-muted">
          No log entries found. Try adjusting your filters or time range.
        </div>
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-lg border border-border-primary">
      <div className="overflow-x-auto">
        <div className="font-mono text-xs leading-relaxed">
          {lines.map((entry, i) => {
            const severity = parseSeverity(entry.line);
            const isError = severity === "error";
            return (
              <div
                // biome-ignore lint/suspicious/noArrayIndexKey: log lines have no id, and the list is replaced whole on each search
                key={i}
                className={`flex gap-3 border-b border-border-subtle px-4 py-0.5 ${
                  isError ? "bg-danger/5" : ""
                }`}
              >
                <span className="min-w-[160px] shrink-0 text-text-muted">
                  {formatTimestamp(entry.timestamp)}
                </span>
                <span
                  className={`min-w-[60px] shrink-0 uppercase ${severityColor(
                    severity,
                  )}`}
                >
                  {severity}
                </span>
                {entry.labels?.pod && (
                  <a
                    href={`/workloads/pods/${
                      entry.labels.namespace ?? "default"
                    }/${entry.labels.pod}`}
                    title={entry.labels.pod}
                    className="min-w-[140px] shrink-0 text-info hover:underline"
                  >
                    {entry.labels.pod.slice(0, 20)}
                  </a>
                )}
                <span className="text-text-secondary break-all">
                  {entry.line}
                </span>
              </div>
            );
          })}
        </div>
      </div>
      <div className="border-t border-border-primary bg-surface px-4 py-2 text-xs text-text-muted">
        {lines.length.toLocaleString()} entries
      </div>
    </div>
  );
}
