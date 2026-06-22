import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { CertificateForm } from "@/components/wizard/CertificateForm.tsx";
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

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Name, issuer & SANs" },
  { label: "Review", sub: "Preview & apply" },
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

// decodeIssuerRef splits "Kind:Name" — uses indexOf so names containing colons
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
  return input.split(",").map((s) => s.trim()).filter((s) => s.length > 0);
}

function buildManifest(f: CertificateWizardForm): string {
  const ref = decodeIssuerRef(f.issuerRefValue);
  const dnsNames = splitDnsNames(f.dnsNamesInput);
  const dnsBlock = dnsNames.length > 0
    ? `\n  dnsNames:\n${dnsNames.map((n) => `    - ${n}`).join("\n")}`
    : "";
  const cnBlock = f.commonName.trim()
    ? `\n  commonName: ${f.commonName.trim()}`
    : "";
  const caBlock = f.isCA ? "\n  isCA: true" : "";
  const sizeBlock = f.privateKey.algorithm !== "Ed25519"
    ? `\n      size: ${f.privateKey.size}`
    : "";
  return `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: ${f.name || "<name>"}
  namespace: ${f.namespace || "<namespace>"}
spec:
  secretName: ${f.secretName || "<secret-name>"}
  issuerRef:
    kind: ${ref?.kind || "Issuer"}
    name: ${ref?.name || "<issuer>"}
    group: cert-manager.io
  duration: ${f.duration || "2160h"}
  renewBefore: ${f.renewBefore || "360h"}${dnsBlock}${cnBlock}${caBlock}
  privateKey:
    algorithm: ${f.privateKey.algorithm}
    rotationPolicy: ${f.privateKey.rotationPolicy}${sizeBlock}`;
}

export default function CertificateWizard(
  { onClose }: { onClose?: () => void },
) {
  const close = onClose ?? (() => globalThis.history.back());
  const step = useSignal(0);
  const form = useSignal<CertificateWizardForm>(initialForm());
  const errors = useSignal<Record<string, string>>({});

  const issuers = useSignal<Issuer[]>([]);
  const issuersLoading = useSignal(true);
  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useEffect(() => {
    Promise.all([
      apiGet<Issuer[]>("/v1/certificates/issuers"),
      apiGet<Issuer[]>("/v1/certificates/clusterissuers"),
    ])
      .then(([ns, cl]) => {
        const nsList = Array.isArray(ns.data) ? ns.data : [];
        const clList = Array.isArray(cl.data) ? cl.data : [];
        issuers.value = [...nsList, ...clList];
      })
      .catch(() => {})
      .finally(() => {
        issuersLoading.value = false;
      });
  }, []);

  const updateField = (field: string, value: unknown) => {
    const f = { ...form.value, [field]: value };
    // Auto-derive secretName from name until the user has diverged it
    if (
      field === "name" && typeof value === "string" && value !== "" &&
      (f.secretName === "" || f.secretName === form.value.name + "-tls")
    ) {
      f.secretName = `${value}-tls`;
    }
    form.value = f;
  };

  const updatePrivateKey = (field: string, value: unknown) => {
    const pk = { ...form.value.privateKey, [field]: value };
    if (field === "algorithm") {
      if (value === "RSA") pk.size = 2048;
      else if (value === "ECDSA") pk.size = 256;
      else if (value === "Ed25519") pk.size = 0;
    }
    form.value = { ...form.value, privateKey: pk };
  };

  const validateStep = (): boolean => {
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
      issuerRef: { kind: ref.kind, name: ref.name, group: "cert-manager.io" },
    };
    if (dnsNames.length > 0) payload.dnsNames = dnsNames;
    if (f.commonName.trim() !== "") payload.commonName = f.commonName.trim();
    if (f.duration.trim() !== "") payload.duration = f.duration.trim();
    if (f.renewBefore.trim() !== "") payload.renewBefore = f.renewBefore.trim();
    const pk: Record<string, unknown> = {
      algorithm: f.privateKey.algorithm,
      rotationPolicy: f.privateKey.rotationPolicy,
    };
    if (f.privateKey.algorithm !== "Ed25519") pk.size = f.privateKey.size;
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

  const handleNext = async () => {
    if (step.value === 0) {
      if (!validateStep()) return;
      step.value = 1;
      await fetchPreview();
    } else {
      close();
    }
  };

  return (
    <WizardShell
      title="Create Certificate"
      subtitle={`Step ${
        step.value + 1
      } of 2 · namespace ${form.value.namespace}`}
      icon={
        <svg
          width="21"
          height="21"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <circle cx="10" cy="9" r="5" />
          <path d="M7 14l-2 4M13 14l2 4M8 18h4" />
        </svg>
      }
      steps={STEPS}
      current={step.value}
      onStep={(i) => {
        if (i < step.value) step.value = i;
      }}
      onCancel={close}
      onBack={() => (step.value = Math.max(0, step.value - 1))}
      onNext={handleNext}
      nextLabel={step.value === 0 ? "Continue" : "Done"}
      yaml={step.value === 0 ? buildManifest(form.value) : undefined}
    >
      {step.value === 0 && (
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

      {step.value === 1 && (
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
    </WizardShell>
  );
}
