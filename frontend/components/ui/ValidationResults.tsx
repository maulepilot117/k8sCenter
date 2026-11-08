/** @jsxImportSource react */
import type { ValidateResponse } from "@/lib/yaml-apply.ts";

/**
 * The verdict of a `/yaml/validate` dry run: a one-line summary, then each
 * failing document with its errors. Shared by both YAML editors so a passing
 * Validate always shows an outcome, not just a failing one.
 */
export function ValidationResults({
  response,
}: {
  response: ValidateResponse;
}) {
  const total = response.documents.length;
  const failing = response.documents.filter((d) => !d.valid);
  const ok = response.valid;
  return (
    <div
      role="status"
      className={`rounded-lg border p-4 ${
        ok
          ? "border-success/30 bg-success/10"
          : "border-warning/30 bg-warning/10"
      }`}
    >
      <p
        className={`m-0 text-sm font-semibold ${ok ? "text-success" : "text-warning"}`}
      >
        {total} resource{total !== 1 ? "s" : ""} validated
        {failing.length > 0 ? `: ${failing.length} with errors` : ": all valid"}
      </p>
      {failing.map((d) => (
        <div key={`${d.index}-${d.kind}-${d.name}`} className="mt-2 text-sm">
          <span className="font-mono text-text-primary">
            {d.kind}/{d.name}
          </span>
          {d.errors?.length ? (
            d.errors.map((e) => (
              <div
                key={`${e.field ?? ""}:${e.message}`}
                className="text-danger"
              >
                {e.field ? `${e.field}: ` : ""}
                {e.message}
              </div>
            ))
          ) : (
            <div className="text-danger">invalid</div>
          )}
        </div>
      ))}
    </div>
  );
}
