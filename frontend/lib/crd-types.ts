/** CRD metadata returned by GET /v1/extensions/crds */
export interface CRDInfo {
  group: string;
  version: string;
  resource: string;
  kind: string;
  scope: "Namespaced" | "Cluster";
  served: boolean;
  storageVersion: boolean;
  additionalPrinterColumns?: PrinterColumn[];
}

export interface PrinterColumn {
  name: string;
  type: string;
  jsonPath: string;
  description?: string;
  priority?: number;
}

/** Grouped CRD response from the backend */
export type CRDGroupedResponse = Record<string, CRDInfo[]>;

/** CRD instance counts response */
export type CRDCountsResponse = Record<string, number>;

/** OpenAPI V3 schema property (simplified for form rendering) */
export interface SchemaProperty {
  type?: string;
  description?: string;
  enum?: string[];
  default?: unknown;
  format?: string;
  required?: string[];
  properties?: Record<string, SchemaProperty>;
  items?: SchemaProperty;
  additionalProperties?: boolean | SchemaProperty;
  oneOf?: SchemaProperty[];
  anyOf?: SchemaProperty[];
  "x-kubernetes-preserve-unknown-fields"?: boolean;
  "x-kubernetes-int-or-string"?: boolean;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
}
