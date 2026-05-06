/**
 * Phase K: YAML templates for the 11 SecretStore providers that don't have a
 * guided wizard. Templates are surfaced through
 * `/external-secrets/stores/new-from-template?template=<provider>` and applied
 * via the existing `/api/v1/yaml/apply` server-side path — no Phase K-specific
 * Go validators exist; ESO + the cluster API are the schema authority.
 *
 * Each template carries:
 *   - A `# Source:` comment linking to the ESO docs page the schema came from,
 *     so quarterly drift checks can re-derive the template from upstream.
 *   - `# REPLACE:` markers on every field the operator must fill before applying.
 *   - `kind: SecretStore` (namespaced) — Phase K v1 ships namespaced templates
 *     only. Operators wanting a ClusterSecretStore can change `kind:` and drop
 *     `metadata.namespace`; the apply path accepts both.
 *
 * Adding a provider:
 *   1. Add the key to the `SecretStoreProvider` union and `TEMPLATE_ONLY_PROVIDERS`
 *      set in `eso-types.ts`.
 *   2. Add an entry below.
 *   3. Add a picker tile to `SecretStoreProviderPickerStep.tsx`.
 *   4. The registry-coverage test will fail loudly if step 2 is forgotten.
 */

import type { SecretStoreProvider } from "./eso-types.ts";

export interface ESOTemplate {
  /** Display name shown in the route header and the template grid. */
  displayName: string;
  /** One-paragraph operator-facing summary shown above the editor. */
  notes: string;
  /** Upstream docs URL so the operator can verify field shape post-edit. */
  docsURL: string;
  /** Pre-filled SecretStore YAML with `# REPLACE:` markers on required fields. */
  yaml: string;
}

const akeylessTemplate: ESOTemplate = {
  displayName: "Akeyless",
  notes: "Akeyless Vault SaaS / self-hosted gateway. Authenticate via JWT, " +
    "Kubernetes, or static access ID + access key. Free-text item path; " +
    "Akeyless paths are slash-separated (e.g. `/apps/prod/db-password`).",
  docsURL: "https://external-secrets.io/latest/provider/akeyless/",
  yaml: `# Source: https://external-secrets.io/latest/provider/akeyless/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: akeyless-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    akeyless:
      akeylessGWApiURL: https://api.akeyless.io # REPLACE: gateway URL if self-hosted
      authSecretRef:
        # Pick exactly one auth method below and delete the others.
        kubernetesAuth:
          accessID: # REPLACE: Akeyless access ID with kubernetes auth method bound
          k8sConfName: # REPLACE: Akeyless k8s gateway config name
          serviceAccountRef:
            name: default # REPLACE: ServiceAccount in this namespace whose JWT to send
        # secretRef:
        #   accessID:
        #     name: akeyless-creds
        #     key: access-id
        #   accessType:
        #     name: akeyless-creds
        #     key: access-type # one of: access_key, password, jwt, saml, ldap
        #   accessTypeParam:
        #     name: akeyless-creds
        #     key: access-type-param # access key value, password, JWT, etc.
`,
};

const bitwardenTemplate: ESOTemplate = {
  displayName: "Bitwarden Secrets Manager",
  notes:
    "Bitwarden Secrets Manager — separate product from the password manager. " +
    "Authenticate with a machine-account access token. Each secret lives in " +
    "a project; the ExternalSecret references secrets by UUID, not by name.",
  docsURL:
    "https://external-secrets.io/latest/provider/bitwarden-secrets-manager/",
  yaml:
    `# Source: https://external-secrets.io/latest/provider/bitwarden-secrets-manager/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: bitwarden-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    bitwardensecretsmanager:
      apiURL: https://api.bitwarden.com # REPLACE: self-hosted API URL if applicable
      identityURL: https://identity.bitwarden.com # REPLACE: self-hosted identity URL if applicable
      organizationID: # REPLACE: Bitwarden organization UUID
      projectID: # REPLACE: Bitwarden project UUID — scopes the access token's reach
      auth:
        secretRef:
          credentials:
            name: bitwarden-access-token # REPLACE: Secret in this namespace
            key: token # REPLACE: key in the Secret containing the machine account access token
`,
};

const conjurTemplate: ESOTemplate = {
  displayName: "CyberArk Conjur",
  notes:
    "CyberArk Conjur Open Source / Enterprise. Authenticate with an apiKey or " +
    "JWT (workload identity). Free-text variable path — Conjur paths are " +
    "policy-scoped (e.g. `prod/db/password`).",
  docsURL: "https://external-secrets.io/latest/provider/conjur/",
  yaml: `# Source: https://external-secrets.io/latest/provider/conjur/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: conjur-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    conjur:
      url: https://conjur.example.com # REPLACE: Conjur appliance URL
      caBundle: | # REPLACE: PEM-encoded CA bundle, or remove and use caProvider
        -----BEGIN CERTIFICATE-----
        ...
        -----END CERTIFICATE-----
      auth:
        # Pick exactly one of apikey or jwt below and delete the other.
        apikey:
          account: myaccount # REPLACE: Conjur organization account name
          userRef:
            name: conjur-creds
            key: username
          apiKeyRef:
            name: conjur-creds
            key: apikey
        # jwt:
        #   account: myaccount
        #   serviceAccountRef:
        #     name: default # workload SA whose JWT will be sent to Conjur
        #   hostId: host/conjur-jwt-host-id
`,
};

const infisicalTemplate: ESOTemplate = {
  displayName: "Infisical",
  notes: "Infisical Cloud or self-hosted. Authenticate via Universal Auth " +
    "(machine identity client ID + secret). The store binds to a single " +
    "Infisical project + environment + secrets path.",
  docsURL: "https://external-secrets.io/latest/provider/infisical/",
  yaml: `# Source: https://external-secrets.io/latest/provider/infisical/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: infisical-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    infisical:
      hostAPI: https://app.infisical.com/api # REPLACE: self-hosted API URL if applicable
      auth:
        universalAuthCredentials:
          clientId:
            name: infisical-creds # REPLACE: Secret in this namespace
            key: client-id
          clientSecret:
            name: infisical-creds
            key: client-secret
      secretsScope:
        projectSlug: # REPLACE: Infisical project slug
        environmentSlug: prod # REPLACE: environment slug (dev/staging/prod/...)
        secretsPath: "/" # REPLACE: secrets path within the project, e.g. /backend
`,
};

const pulumiTemplate: ESOTemplate = {
  displayName: "Pulumi ESC",
  notes: "Pulumi Environments, Secrets & Configuration. Authenticate with a " +
    "Pulumi access token. Each ExternalSecret references a path inside a " +
    "named environment — see Pulumi ESC's `pulumi env` CLI for path syntax.",
  docsURL: "https://external-secrets.io/latest/provider/pulumi/",
  yaml: `# Source: https://external-secrets.io/latest/provider/pulumi/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: pulumi-esc-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    pulumi:
      apiUrl: https://api.pulumi.com/api/esc # REPLACE: self-hosted API URL if applicable
      organization: # REPLACE: Pulumi organization name
      project: default # REPLACE: ESC project name
      environment: # REPLACE: ESC environment name
      accessToken:
        secretRef:
          name: pulumi-access-token # REPLACE: Secret in this namespace
          key: token
`,
};

const passboltTemplate: ESOTemplate = {
  displayName: "Passbolt",
  notes:
    "Passbolt CE / Pro. Authenticate with a GPG private key + passphrase " +
    "stored in a Kubernetes Secret. Resources are referenced by Passbolt " +
    "resource ID (UUID).",
  docsURL: "https://external-secrets.io/latest/provider/passbolt/",
  yaml: `# Source: https://external-secrets.io/latest/provider/passbolt/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: passbolt-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    passbolt:
      host: https://passbolt.example.com # REPLACE: Passbolt instance URL
      auth:
        passwordSecretRef:
          name: passbolt-creds # REPLACE: Secret in this namespace
          key: password # GPG private-key passphrase
        privateKeySecretRef:
          name: passbolt-creds
          key: private-key # ASCII-armored GPG private key
`,
};

const keeperTemplate: ESOTemplate = {
  displayName: "Keeper Secrets Manager",
  notes: "Keeper Secrets Manager. Authenticate with a base64-encoded KSM " +
    "configuration blob (generated via `ksm config export --format json | base64`). " +
    "Records are referenced by record UID.",
  docsURL: "https://external-secrets.io/latest/provider/keepersecurity/",
  yaml: `# Source: https://external-secrets.io/latest/provider/keepersecurity/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: keeper-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    keepersecurity:
      authRef:
        name: keeper-config # REPLACE: Secret in this namespace
        key: config # base64-encoded KSM config JSON
      folderID: # REPLACE: Keeper shared-folder UID this store may read
`,
};

const onboardbaseTemplate: ESOTemplate = {
  displayName: "Onboardbase",
  notes:
    "Onboardbase. Authenticate with an API key + passcode. Each store binds " +
    "to a single Onboardbase project + environment.",
  docsURL: "https://external-secrets.io/latest/provider/onboardbase/",
  yaml: `# Source: https://external-secrets.io/latest/provider/onboardbase/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: onboardbase-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    onboardbase:
      apiHost: https://public.onboardbase.com/api/v1 # REPLACE: self-hosted API URL if applicable
      project: # REPLACE: Onboardbase project name
      environment: production # REPLACE: environment name
      auth:
        apiKeyRef:
          name: onboardbase-creds # REPLACE: Secret in this namespace
          key: api-key
        passcodeRef:
          name: onboardbase-creds
          key: passcode
`,
};

const oracleVaultTemplate: ESOTemplate = {
  displayName: "Oracle Cloud Vault",
  notes:
    "Oracle Cloud Infrastructure (OCI) Vault. Authenticate via OCI principal " +
    "(workload identity / instance principal — preferred) or with API-key " +
    "credentials in a Secret. References are by OCID.",
  docsURL: "https://external-secrets.io/latest/provider/oracle-vault/",
  yaml: `# Source: https://external-secrets.io/latest/provider/oracle-vault/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: oracle-vault-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    oracle:
      region: # REPLACE: OCI region, e.g. us-phoenix-1
      vault: # REPLACE: target Vault OCID (ocid1.vault.oc1...)
      # Pick exactly one of principalType (preferred) or auth (static creds).
      principalType: Workload # one of: Workload, Instance, UserPrincipal
      # auth:
      #   tenancy: ocid1.tenancy.oc1...
      #   user: ocid1.user.oc1...
      #   secretRef:
      #     fingerprint:
      #       name: oracle-creds
      #       key: fingerprint
      #     privatekey:
      #       name: oracle-creds
      #       key: private-key
`,
};

const alibabaTemplate: ESOTemplate = {
  displayName: "Alibaba Cloud KMS",
  notes: "Alibaba Cloud Key Management Service Secret. Authenticate with " +
    "AccessKey ID + AccessKey secret stored in a Kubernetes Secret. References " +
    "are by Alibaba secret name within the configured region.",
  docsURL: "https://external-secrets.io/latest/provider/alibaba/",
  yaml: `# Source: https://external-secrets.io/latest/provider/alibaba/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: alibaba-kms-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    alibaba:
      regionID: cn-hangzhou # REPLACE: Alibaba KMS region
      auth:
        secretRef:
          accessKeyIDSecretRef:
            name: alibaba-creds # REPLACE: Secret in this namespace
            key: access-key-id
          accessKeySecretSecretRef:
            name: alibaba-creds
            key: access-key-secret
`,
};

const webhookTemplate: ESOTemplate = {
  displayName: "Generic webhook",
  notes:
    "Generic webhook provider — fetches secrets from any HTTP endpoint that " +
    "speaks JSON. Use this for in-house secret backends or any provider " +
    "without a dedicated ESO integration. The `result.jsonPath` selects which " +
    "field of the response body to use as the secret value.",
  docsURL: "https://external-secrets.io/latest/provider/webhook/",
  yaml: `# Source: https://external-secrets.io/latest/provider/webhook/
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: webhook-store
  namespace: default # REPLACE: target namespace
spec:
  provider:
    webhook:
      url: "https://secrets.example.com/v1/secret/{{ .remoteRef.key }}" # REPLACE: full URL with {{ .remoteRef.key }} interpolation
      method: GET
      timeout: 5s
      headers:
        Content-Type: application/json
        Authorization: "Bearer {{ .auth.token }}"
      result:
        jsonPath: "$.data.value" # REPLACE: jsonpath selecting the secret value in the response body
      secrets:
        - name: token
          secretRef:
            name: webhook-creds # REPLACE: Secret in this namespace
            key: token
`,
};

/**
 * Single source of truth for Phase K templates. Keys must match the
 * `TEMPLATE_ONLY_PROVIDERS` set in `eso-types.ts` exactly — the
 * registry-coverage test in `eso-yaml-templates.test.ts` enforces this.
 */
export const ESO_YAML_TEMPLATES: Partial<
  Record<SecretStoreProvider, ESOTemplate>
> = {
  akeyless: akeylessTemplate,
  bitwardensecretsmanager: bitwardenTemplate,
  conjur: conjurTemplate,
  infisical: infisicalTemplate,
  pulumi: pulumiTemplate,
  passbolt: passboltTemplate,
  keeper: keeperTemplate,
  onboardbase: onboardbaseTemplate,
  oraclevault: oracleVaultTemplate,
  alibaba: alibabaTemplate,
  webhook: webhookTemplate,
};
