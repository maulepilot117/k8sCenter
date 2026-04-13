import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { GatewayClassSummary } from "@/lib/gateway-types.ts";
import ConditionsTable from "@/components/gateway/ConditionsTable.tsx";

interface Props {
  name: string;
}

export default function GatewayClassDetailIsland({ name }: Props) {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const data = useSignal<GatewayClassSummary | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    async function fetch() {
      loading.value = true;
      error.value = null;
      try {
        const res = await apiGet<GatewayClassSummary>(
          `/v1/gateway/gatewayclasses/${name}`,
        );
        data.value = res.data ?? null;
      } catch {
        error.value = "Failed to load GatewayClass details";
      } finally {
        loading.value = false;
      }
    }
    fetch();
  }, [name]);

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (error.value) {
    return <p class="text-sm text-danger p-6">{error.value}</p>;
  }

  if (!data.value) return null;

  const gc = data.value;

  return (
    <div class="p-6 space-y-6">
      <a
        href="/networking/gateway-api?kind=gatewayclasses"
        class="text-sm text-brand hover:underline"
      >
        &larr; Back to Gateway Classes
      </a>

      <h2 class="text-2xl font-bold text-text-primary">{gc.name}</h2>

      <div class="rounded-lg border border-border-primary bg-bg-elevated p-5">
        <h3 class="text-sm font-semibold text-text-primary mb-4">Details</h3>
        <dl class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <dt class="text-text-muted">Controller</dt>
            <dd class="text-text-primary">{gc.controllerName}</dd>
          </div>
          <div>
            <dt class="text-text-muted">Description</dt>
            <dd class="text-text-primary">{gc.description || "-"}</dd>
          </div>
          <div>
            <dt class="text-text-muted">Age</dt>
            <dd class="text-text-primary">{gc.age}</dd>
          </div>
        </dl>
      </div>

      <ConditionsTable conditions={gc.conditions} />
    </div>
  );
}
