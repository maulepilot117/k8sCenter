/**
 * Typed client for the change-receipt endpoints (`/v1/changes`). Stateless:
 * every function is one request over `api.ts`, returns the unwrapped payload,
 * and throws `ApiError` on failure. Read the failure through `ApiError.status`,
 * `ApiError.reason` and `errorExtra()`, never `body.error`.
 *
 * **Which cluster a request is addressed to.**
 *
 *   - Receipt reads (`listReceipts`, `getReceipt`, `getVerification`) are
 *     receipt-scoped: the server authorizes and verifies against the cluster
 *     the RECEIPT names, never the request's X-Cluster-ID. They are therefore
 *     pinned to the local cluster, not to whatever the operator has selected.
 *     Left on the ambient selection, an operator viewing a remote cluster
 *     would send that cluster's id, and a non-admin (or any receipt read while
 *     the selection is a remote the caller may not reach) would be refused by
 *     the cluster-access gate before the receipt was even looked up, so a
 *     receipt written against cluster A could not be opened while looking at
 *     cluster B.
 *   - `resolveOwnership` IS cluster-scoped: it asks who manages objects on one
 *     specific cluster. The caller names that cluster explicitly, and it is
 *     sent both as X-Cluster-ID and as the body's `clusterId` (the server
 *     refuses a body that disagrees with the header). The response echoes the
 *     cluster it resolved against; callers holding a pin should compare it.
 *
 * Client-only, like `api.ts`.
 */

import { apiGet, apiPost } from "./api.ts";
import type {
  OwnershipObjectRef,
  OwnershipRequest,
  OwnershipResponse,
  ReceiptDetail,
  ReceiptView,
  VerificationView,
} from "./change-types.ts";
import { LOCAL_CLUSTER_ID } from "./cluster.ts";

/** Receipt reads are not bound to the selected cluster; see the module doc. */
const RECEIPT_READ_CLUSTER = LOCAL_CLUSTER_ID;

/** One page of the caller's own receipts, newest first. */
export interface ReceiptPage {
  items: ReceiptView[];
  /** Total receipts the caller owns, across all pages. */
  total: number;
  /** Echoed by the server; absent only if an older server omits them. */
  page?: number;
  pageSize?: number;
}

/** Paging for `listReceipts`; omitted fields take the server's defaults. */
export interface ListReceiptsParams {
  page?: number;
  pageSize?: number;
}

/**
 * Builds the receipt-list query string: `""` when nothing is set, else `?` plus
 * `page` then `pageSize`.
 */
export function buildReceiptListQuery(params: ListReceiptsParams = {}): string {
  const query = new URLSearchParams();
  if (params.page !== undefined) query.set("page", String(params.page));
  if (params.pageSize !== undefined) {
    query.set("pageSize", String(params.pageSize));
  }
  return query.size > 0 ? `?${query}` : "";
}

/** `GET /v1/changes`: the caller's own receipts (grants are reachable by id only). */
export async function listReceipts(
  params: ListReceiptsParams = {},
  signal?: AbortSignal,
): Promise<ReceiptPage> {
  const res = await apiGet<ReceiptView[]>(
    `/v1/changes${buildReceiptListQuery(params)}`,
    { clusterId: RECEIPT_READ_CLUSTER, signal },
  );
  return {
    items: res.data ?? [],
    total: res.metadata?.total ?? res.data?.length ?? 0,
    page: res.metadata?.page,
    pageSize: res.metadata?.pageSize,
  };
}

/**
 * `GET /v1/changes/{id}`: one receipt, redacted for this caller. A receipt the
 * caller may not read is a 404, not a 403.
 */
export async function getReceipt(
  id: string,
  signal?: AbortSignal,
): Promise<ReceiptDetail> {
  const res = await apiGet<ReceiptDetail>(
    `/v1/changes/${encodeURIComponent(id)}`,
    { clusterId: RECEIPT_READ_CLUSTER, signal },
  );
  return res.data;
}

/**
 * `GET /v1/changes/{id}/verification`: one verification pass. For the owner
 * this persists the verdict; poll again after `retryAfterSeconds` while the
 * state is "verifying".
 */
export async function getVerification(
  id: string,
  signal?: AbortSignal,
): Promise<VerificationView> {
  const res = await apiGet<VerificationView>(
    `/v1/changes/${encodeURIComponent(id)}/verification`,
    { clusterId: RECEIPT_READ_CLUSTER, signal },
  );
  return res.data;
}

/**
 * `POST /v1/changes/ownership`: which GitOps controller manages each object on
 * `clusterId`, under the caller's own identity. Results are in request order.
 */
export async function resolveOwnership(
  clusterId: string,
  objects: OwnershipObjectRef[],
  signal?: AbortSignal,
): Promise<OwnershipResponse> {
  const body: OwnershipRequest = { clusterId, objects };
  const res = await apiPost<OwnershipResponse>("/v1/changes/ownership", body, {
    clusterId,
    signal,
  });
  return res.data;
}
