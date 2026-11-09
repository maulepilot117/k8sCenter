/** @jsxImportSource react */
import { useEffect, useRef, useState } from "react";
import { CertificateForm } from "@/components/wizard/CertificateForm.tsx";
import { apiGet, apiPost } from "@/lib/api.ts";
import type { Issuer } from "@/lib/certmanager-types.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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
  return input
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

function buildManifest(f: CertificateWizardForm): string {
  const ref = decodeIssuerRef(f.issuerRefValue);
  const dnsNames = splitDnsNames(f.dnsNamesInput);
  const dnsBlock =
    dnsNames.length > 0
      ? `\n  dnsNames:\n${dnsNames.map((n) => `    - ${n}`).join("\n")}`
      : "";
  const cnBlock = f.commonName.trim()
    ? `\n  commonName: ${f.commonName.trim()}`
    : "";
  const caBlock = f.isCA ? "\n  isCA: true" : "";
  const sizeBlock =
    f.privateKey.algorithm !== "Ed25519"
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

export default function CertificateWizard({
  onClose,
}: {
  onClose?: () => void;
}) {
  const close = onClose ?? (() => globalThis.history.back());
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<CertificateWizardForm>(initialForm);
  const [errors, setErrors] = useState<Record<string, string>>({});

  const [issuers, setIssuers] = useState<Issuer[]>([]);
  const [issuersLoading, setIssuersLoading] = useState(true);
  const namespaces = useSignalValue(useNamespaces());

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  useEffect(() => {
    Promise.all([
      apiGet<Issuer[]>("/v1/certificates/issuers"),
      apiGet<Issuer[]>("/v1/certificates/clusterissuers"),
    ])
      .then(([ns, cl]) => {
        const nsList = Array.isArray(ns.data) ? ns.data : [];
        const clList = Array.isArray(cl.data) ? cl.data : [];
        setIssuers([...nsList, ...clList]);
      })
      .catch(() => {})
      .finally(() => {
        setIssuersLoading(false);
      });
  }, []);

  const updateField = (field: string, value: unknown) => {
    setForm((prev) => {
      const f = { ...prev, [field]: value };
      // Auto-derive secretName from name until the user has diverged it
      if (
        field === "name" &&
        typeof value === "string" &&
        value !== "" &&
        (f.secretName === "" || f.secretName === `${prev.name}-tls`)
      ) {
        f.secretName = `${value}-tls`;
      }
      return f;
    });
  };

  const updatePrivateKey = (field: string, value: unknown) => {
    setForm((prev) => {
      const pk = { ...prev.privateKey, [field]: value };
      if (field === "algorithm") {
        if (value === "RSA") pk.size = 2048;
        else if (value === "ECDSA") pk.size = 256;
        else if (value === "Ed25519") pk.size = 0;
      }
      return { ...prev, privateKey: pk };
    });
  };

  const validateStep = (): boolean => {
    const f = form;
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
    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);
    const f = form;
    const ref = decodeIssuerRef(f.issuerRefValue);
    if (!ref) {
      if (gen !== previewGen.current) return;
      setPreviewError("Invalid issuer selection");
      setPreviewLoading(false);
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
      if (gen !== previewGen.current) return;
      setPreviewYaml(resp.data.yaml);
    } catch (err) {
      if (gen !== previewGen.current) return;
      setPreviewError(
        err instanceof Error ? err.message : "Failed to generate preview",
      );
    } finally {
      if (gen === previewGen.current) setPreviewLoading(false);
    }
  };

  const handleNext = async () => {
    if (step === 0) {
      if (!validateStep()) return;
      setStep(1);
      await fetchPreview();
    } else {
      close();
    }
  };

  return (
    <WizardShell
      title="Create Certificate"
      subtitle={`Step ${step + 1} of 2 · namespace ${form.namespace}`}
      icon={
        <svg
          width="21"
          height="21"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <circle cx="10" cy="9" r="5" />
          <path d="M7 14l-2 4M13 14l2 4M8 18h4" />
        </svg>
      }
      steps={STEPS}
      current={step}
      onStep={(i) => {
        if (i < step) setStep(i);
      }}
      onCancel={close}
      onBack={() => setStep((prev) => Math.max(0, prev - 1))}
      onNext={handleNext}
      nextLabel={step === 0 ? "Continue" : "Done"}
      yaml={step === 0 ? buildManifest(form) : undefined}
    >
      {step === 0 && (
        <CertificateForm
          form={form}
          errors={errors}
          issuers={issuers}
          issuersLoading={issuersLoading}
          namespaces={namespaces}
          onUpdate={updateField}
          onUpdatePrivateKey={updatePrivateKey}
        />
      )}

      {step === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/security/certificates"
        />
      )}
    </WizardShell>
  );
}
