/** @jsxImportSource react */
import { useEffect, useState } from "react";
import ConditionsTable from "@/components/gateway/ConditionsTable.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiGet } from "@/lib/api.ts";
import { age } from "@/lib/format.ts";
import type { GatewayClassSummary } from "@/lib/gateway-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface Props {
  name: string;
}

export default function GatewayClassDetailIsland({ name }: Props) {
  const isClient = useIsClient();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<GatewayClassSummary | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    async function fetch() {
      setLoading(true);
      setError(null);
      try {
        const res = await apiGet<GatewayClassSummary>(
          `/v1/gateway/gatewayclasses/${name}`,
        );
        setData(res.data ?? null);
      } catch {
        setError("Failed to load GatewayClass details");
      } finally {
        setLoading(false);
      }
    }
    fetch();
  }, [name]);

  if (!isClient) return null;

  if (loading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  if (error) {
    return <p className="text-sm text-danger p-6">{error}</p>;
  }

  if (!data) return null;

  const gc = data;

  return (
    <div className="p-6 space-y-6">
      <a
        href="/networking/gateway-api?kind=gatewayclasses"
        className="text-sm text-brand hover:underline"
      >
        &larr; Back to Gateway Classes
      </a>

      <h2 className="text-2xl font-bold text-text-primary">{gc.name}</h2>

      <div className="rounded-lg border border-border-primary bg-elevated p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-4">
          Details
        </h3>
        <dl className="grid grid-cols-2 gap-4 text-sm">
          <div>
            <dt className="text-text-muted">Controller</dt>
            <dd className="text-text-primary">{gc.controllerName}</dd>
          </div>
          <div>
            <dt className="text-text-muted">Description</dt>
            <dd className="text-text-primary">{gc.description || "-"}</dd>
          </div>
          <div>
            <dt className="text-text-muted">Age</dt>
            <dd className="text-text-primary" title={gc.age}>
              {gc.age ? age(gc.age) : "-"}
            </dd>
          </div>
        </dl>
      </div>

      <ConditionsTable conditions={gc.conditions} />
    </div>
  );
}
