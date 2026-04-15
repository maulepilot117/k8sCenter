import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { CertificateForm } from "@/components/wizard/CertificateForm.tsx";
import { Button } from "@/components/ui/Button.tsx";
import type { Issuer } from "@/lib/certmanager-types.ts";

export interface CertificateWizardForm {
  name: string;
  namespace: string;
  secretName: string;
  issuerRefValue: string; // encoded "Issuer:name" or "ClusterIssuer:name"
  dnsNamesInput: string; // comma-separated user input
  commonName: string;
  duration: string;
  renewBefore: string;
  privateKey: {
    algorithm: "RSA" | "ECDSA" | "Ed25519";
    size: number;
    rotationPolicy: "Always" | "Never";
  };
  isCA: boolean;
}

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
];

function initialForm(): CertificateWizardForm {
  return {
    name: "",
    namespace: initialNamespace(),
    secretName: "",
    issuerRefValue: "",
    dnsNamesInput: "",
    commonName: "",
    duration: "2160h",
    renewBefore: "360h",
    privateKey: { algorithm: "RSA", size: 2048, rotationPolicy: "Always" },
    isCA: false,
  };
}

// decodeIssuerRef splits "Kind:Name" into its parts, using indexOf so names
// containing further colons (won't happen for DNS labels but guards future changes)
// don't truncate. Returns null for malformed or empty input.
function decodeIssuerRef(
  encoded: string,
): { kind: string; name: string } | null {
  const idx = encoded.indexOf(":");
  if (idx <= 0 || idx === encoded.length - 1) return null;
  const kind = encoded.slice(0, idx);
  const name = encoded.slice(idx + 1);
  if (kind !== "Issuer" && kind !== "ClusterIssuer") return null;
  if (!name) return null;
  return { kind, name };
}

function splitDnsNames(input: string): string[] {
  return input
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

export default function CertificateWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<CertificateWizardForm>(initialForm());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const issuers = useSignal<Issuer[]>([]);
  const issuersLoading = useSignal(true);
  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  useEffect(() => {
    if (!IS_BROWSER) return;
    Promise.all([
      apiGet<Issuer[]>("/v1/certificates/issuers"),
      apiGet<Issuer[]>("/v1/certificates/clusterissuers"),
    ])
      .then(([ns, cl]) => {
        const nsList = Array.isArray(ns.data) ? ns.data : [];
        const clList = Array.isArray(cl.data) ? cl.data : [];
        issuers.value = [...nsList, ...clList];
      })
      .catch(() => {
        // Dropdown shows empty state; user can still type.
      })
      .finally(() => {
        issuersLoading.value = false;
      });
  }, []);

  const updateField = (field: string, value: unknown) => {
    dirty.value = true;
    const f = { ...form.value, [field]: value };
    // Auto-default secretName from name when user hasn't customised it.
    if (
      field === "name" && typeof value === "string" &&
      (f.secretName === "" || f.secretName === form.value.name + "-tls")
    ) {
      f.secretName = value ? `${value}-tls` : "";
    }
    form.value = f;
  };

  const updatePrivateKey = (field: string, value: unknown) => {
    dirty.value = true;
    const pk = { ...form.value.privateKey, [field]: value };
    // Sensible default size when algorithm changes.
    if (field === "algorithm") {
      if (value === "RSA") pk.size = 2048;
      else if (value === "ECDSA") pk.size = 256;
      else if (value === "Ed25519") pk.size = 0;
    }
    form.value = { ...form.value, privateKey: pk };
  };

  const validateStep = (step: number): boolean => {
    if (step !== 0) {
      errors.value = {};
      return true;
    }
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be a valid DNS label (lowercase, alphanumeric, hyphens)";
    }
    if (!f.namespace || !DNS_LABEL_REGEX.test(f.namespace)) {
      errs.namespace = "Must be a valid DNS label";
    }
    if (!f.secretName || !DNS_LABEL_REGEX.test(f.secretName)) {
      errs.secretName = "Must be a valid DNS label";
    }
    if (!f.issuerRefValue) {
      errs.issuerRef = "Select an issuer";
    } else if (!decodeIssuerRef(f.issuerRefValue)) {
      errs.issuerRef = "Invalid issuer selection";
    }
    const dnsNames = splitDnsNames(f.dnsNamesInput);
    if (dnsNames.length === 0 && f.commonName.trim() === "") {
      errs.dnsNames = "At least one DNS name or a common name is required";
    }

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;
    const ref = decodeIssuerRef(f.issuerRefValue);
    if (!ref) {
      previewError.value = "Invalid issuer selection";
      previewLoading.value = false;
      return;
    }
    const dnsNames = splitDnsNames(f.dnsNamesInput);

    const payload: Record<string, unknown> = {
      name: f.name,
      namespace: f.namespace,
      secretName: f.secretName,
      issuerRef: {
        kind: ref.kind,
        name: ref.name,
        group: "cert-manager.io",
      },
    };
    if (dnsNames.length > 0) payload.dnsNames = dnsNames;
    if (f.commonName.trim() !== "") payload.commonName = f.commonName.trim();
    if (f.duration.trim() !== "") payload.duration = f.duration.trim();
    if (f.renewBefore.trim() !== "") {
      payload.renewBefore = f.renewBefore.trim();
    }
    const pk: Record<string, unknown> = {
      algorithm: f.privateKey.algorithm,
      rotationPolicy: f.privateKey.rotationPolicy,
    };
    if (f.privateKey.algorithm !== "Ed25519") {
      pk.size = f.privateKey.size;
    }
    payload.privateKey = pk;
    if (f.isCA) payload.isCA = true;

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/certificate/preview",
        payload,
      );
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      previewLoading.value = false;
    }
  };

  const goNext = async () => {
    if (!validateStep(currentStep.value)) return;
    if (currentStep.value === 0) {
      currentStep.value = 1;
      await fetchPreview();
    }
  };

  const goBack = () => {
    if (currentStep.value > 0) currentStep.value = currentStep.value - 1;
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  return (
    <div class="p-6 max-w-4xl mx-auto">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">
          Create Certificate
        </h1>
        <a
          href="/security/certificates"
          class="text-sm text-text-muted hover:text-text-primary"
        >
          Cancel
        </a>
      </div>

      <WizardStepper
        steps={STEPS}
        currentStep={currentStep.value}
        onStepClick={(step) => {
          if (step < currentStep.value) currentStep.value = step;
        }}
      />

      <div class="mt-6">
        {currentStep.value === 0 && (
          <CertificateForm
            form={form.value}
            errors={errors.value}
            issuers={issuers.value}
            issuersLoading={issuersLoading.value}
            namespaces={namespaces.value}
            onUpdate={updateField}
            onUpdatePrivateKey={updatePrivateKey}
          />
        )}

        {currentStep.value === 1 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/security/certificates"
          />
        )}
      </div>

      {currentStep.value < 1 && (
        <div class="flex justify-between mt-8">
          <Button variant="ghost" onClick={goBack} disabled>
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            Preview YAML
          </Button>
        </div>
      )}

      {currentStep.value === 1 && !previewLoading.value &&
        previewError.value === null && (
        <div class="flex justify-start mt-4">
          <Button variant="ghost" onClick={goBack}>Back</Button>
        </div>
      )}
    </div>
  );
}
