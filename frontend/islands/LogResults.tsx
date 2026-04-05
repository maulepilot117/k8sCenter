import { type Signal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

interface LogLine {
  timestamp: string;
  line: string;
  labels: Record<string, string>;
}

interface LogResultsProps {
  lines: Signal<LogLine[]>;
  loading: Signal<boolean>;
}

function parseSeverity(line: string): string {
  const lower = line.toLowerCase();
  if (lower.includes('"level":"error"') || lower.includes("level=error") || /\berror\b/i.test(line.slice(0, 100))) return "error";
  if (lower.includes('"level":"warn"') || lower.includes("level=warn") || /\bwarn(ing)?\b/i.test(line.slice(0, 100))) return "warn";
  if (lower.includes('"level":"debug"') || lower.includes("level=debug") || /\bdebug\b/i.test(line.slice(0, 100))) return "debug";
  return "info";
}

function severityColor(severity: string): string {
  switch (severity) {
    case "error": return "text-status-error font-semibold";
    case "warn": return "text-status-warning";
    case "debug": return "text-text-muted";
    default: return "text-accent-primary";
  }
}

function formatTimestamp(nanoStr: string): string {
  try {
    const ms = parseInt(nanoStr) / 1_000_000;
    return new Date(ms).toISOString().replace("T", " ").slice(0, 23);
  } catch {
    return nanoStr;
  }
}

export default function LogResults(props: LogResultsProps) {
  if (!IS_BROWSER) return null;

  const { lines, loading } = props;

  if (loading.value) {
    return (
      <div class="flex items-center justify-center rounded-lg border border-border-primary bg-bg-surface p-12">
        <div class="text-sm text-text-muted">Searching logs...</div>
      </div>
    );
  }

  if (lines.value.length === 0) {
    return (
      <div class="flex items-center justify-center rounded-lg border border-border-primary bg-bg-surface p-12">
        <div class="text-sm text-text-muted">No log entries found. Try adjusting your filters or time range.</div>
      </div>
    );
  }

  return (
    <div class="overflow-hidden rounded-lg border border-border-primary">
      <div class="overflow-x-auto">
        <div class="font-mono text-xs leading-relaxed">
          {lines.value.map((entry, i) => {
            const severity = parseSeverity(entry.line);
            const isError = severity === "error";
            return (
              <div
                key={i}
                class={`flex border-b border-border-subtle px-4 py-0.5 ${isError ? "bg-status-error/5" : ""}`}
              >
                <span class="min-w-[160px] shrink-0 text-text-muted">
                  {formatTimestamp(entry.timestamp)}
                </span>
                <span class={`min-w-[60px] shrink-0 uppercase ${severityColor(severity)}`}>
                  {severity}
                </span>
                {entry.labels?.pod && (
                  <a
                    href={`/workloads/pods/${entry.labels.namespace ?? "default"}/${entry.labels.pod}`}
                    class="min-w-[140px] shrink-0 text-status-info hover:underline"
                  >
                    {entry.labels.pod.slice(0, 20)}
                  </a>
                )}
                <span class="text-text-secondary break-all">{entry.line}</span>
              </div>
            );
          })}
        </div>
      </div>
      <div class="border-t border-border-primary bg-bg-surface px-4 py-2 text-xs text-text-muted">
        {lines.value.length.toLocaleString()} entries
      </div>
    </div>
  );
}
