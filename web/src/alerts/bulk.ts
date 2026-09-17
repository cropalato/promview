/**
 * Bulk operator actions over a selection of alerts.
 *
 * Each endpoint takes the same body as its single-alert counterpart plus the
 * ids to apply it to, and answers per alert rather than as one verdict: an
 * alert outside the operator's scope must not cost them the other thirty-nine.
 */
import { AlertsApiError } from './api';
import { apiFetch } from '../config/transport';
import { apiUrl } from '../config/apiBase';

const BULK_URL = '/api/v1/alerts/bulk';

/** What happened to one alert in a bulk request. */
export type BulkStatus = 'applied' | 'unchanged' | 'notFound';

export interface BulkOutcome {
  /**
   * The alert id, as a string to match everything else in this client. The API
   * answers with a number here although it takes strings in the request; that
   * asymmetry is real, and normalising it once is cheaper than every caller
   * remembering it.
   */
  id: string;
  status: BulkStatus;
}

export interface BulkResult {
  applied: number;
  unchanged: number;
  notFound: number;
  results: BulkOutcome[];
  /**
   * True when the server answered 207, meaning something in the selection was
   * not visible to this operator. Kept apart from the counts so a caller does
   * not have to infer the distinction the status line already drew.
   */
  partial: boolean;
}

/** The most alerts one request may carry; the server rejects more. */
export const MAX_BULK_ALERTS = 500;

function parseStatus(value: unknown): BulkStatus {
  return value === 'applied' || value === 'unchanged' ? value : 'notFound';
}

function parseBulkResult(body: unknown, partial: boolean): BulkResult {
  const record = typeof body === 'object' && body !== null ? (body as Record<string, unknown>) : {};
  const rawResults = Array.isArray(record.results) ? record.results : [];
  const results: BulkOutcome[] = [];
  for (const entry of rawResults) {
    if (typeof entry !== 'object' || entry === null) {
      continue;
    }
    const raw = entry as Record<string, unknown>;
    // Alert ids start at 1, so a zero or a negative is malformed rather than a
    // real outcome. String(0) is not empty, which an emptiness check misses.
    const id =
      typeof raw.id === 'number' && Number.isInteger(raw.id) && raw.id > 0
        ? String(raw.id)
        : typeof raw.id === 'string' && raw.id !== ''
          ? raw.id
          : '';
    if (id === '') {
      continue;
    }
    results.push({ id, status: parseStatus(raw.status) });
  }
  const count = (value: unknown): number => (typeof value === 'number' ? value : 0);
  return {
    applied: count(record.applied),
    unchanged: count(record.unchanged),
    notFound: count(record.notFound),
    results,
    partial,
  };
}

async function sendBulk(
  path: string,
  method: string,
  body: Record<string, unknown>,
  ids: readonly string[],
  what: string,
  fetchImpl: FetchLike,
): Promise<BulkResult> {
  if (ids.length === 0) {
    throw new AlertsApiError('No alerts selected');
  }
  if (ids.length > MAX_BULK_ALERTS) {
    throw new AlertsApiError(`At most ${MAX_BULK_ALERTS} alerts can be changed at once`);
  }
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(`${BULK_URL}/${path}`), {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids, ...body }),
    });
  } catch (cause) {
    throw new AlertsApiError('Unable to reach the Promview API', { cause });
  }
  // 207 is a successful answer that happens to report misses, not a failure.
  if (!response.ok && response.status !== 207) {
    throw new AlertsApiError(`${what} request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }
  let payload: unknown;
  try {
    payload = await response.json();
  } catch (cause) {
    throw new AlertsApiError(`${what} response was not valid JSON`, { cause });
  }
  return parseBulkResult(payload, response.status === 207);
}

type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

export function bulkAcknowledge(
  ids: readonly string[],
  acknowledged: boolean,
  fetchImpl: FetchLike = apiFetch,
): Promise<BulkResult> {
  return sendBulk('acknowledge', 'POST', { acknowledged }, ids, 'Acknowledge', fetchImpl);
}

export function bulkAssign(
  ids: readonly string[],
  assignee: string,
  fetchImpl: FetchLike = apiFetch,
): Promise<BulkResult> {
  return sendBulk('assignee', 'PUT', { assignee }, ids, 'Assign', fetchImpl);
}

export function bulkClose(
  ids: readonly string[],
  closed: boolean,
  fetchImpl: FetchLike = apiFetch,
): Promise<BulkResult> {
  return sendBulk('close', 'POST', { closed }, ids, 'Close', fetchImpl);
}

export function bulkNote(
  ids: readonly string[],
  body: string,
  fetchImpl: FetchLike = apiFetch,
): Promise<BulkResult> {
  return sendBulk('notes', 'POST', { body }, ids, 'Note', fetchImpl);
}
