import { normalizeSeverity, severityLabelFor } from './severity';
import type { Severity } from './severity';
import type { AlertState, AlertSummary } from './types';
import { apiUrl } from '../config/apiBase';
import { apiFetch } from '../config/transport';

/**
 * Client for the alert query API, `GET /api/v1/alerts`. Same-origin and
 * cookie-based like the runtime config client, so the same code runs in the
 * embedded browser build and the future Tauri desktop client. Responses are
 * validated and mapped into table rows here so components never touch raw
 * API payloads.
 */
export const ALERTS_URL = '/api/v1/alerts';

/** Page size for cursor pagination; matches the server's default limit. */
export const ALERTS_PAGE_SIZE = 100;

export class AlertsApiError extends Error {
  readonly status?: number;

  constructor(message: string, options: { status?: number; cause?: unknown } = {}) {
    super(message, options.cause !== undefined ? { cause: options.cause } : undefined);
    this.name = 'AlertsApiError';
    this.status = options.status;
  }
}

/**
 * True when the API rejected the request as unauthenticated (HTTP 401) — in
 * protected deployments the server-side session expired after boot. The shell
 * treats this as a sign-out event, not a request error.
 */
export function isAlertsUnauthorized(error: unknown): boolean {
  return error instanceof AlertsApiError && error.status === 401;
}

/** Sortable alert fields, in the console's vocabulary (column ids). */
export const ALERT_SORT_FIELDS = [
  'severity',
  'state',
  'name',
  'summary',
  'team',
  'instance',
  'source',
  'age',
] as const;

export type AlertSortField = (typeof ALERT_SORT_FIELDS)[number];

export type AlertSortOrder = 'asc' | 'desc';

/** Active server-side sort: one field plus direction. */
export interface AlertSort {
  field: AlertSortField;
  order: AlertSortOrder;
}

/** Endpoint vocabulary for each console sort field. */
const SORT_PARAM_FIELDS: Record<AlertSortField, string> = {
  severity: 'severity',
  state: 'status',
  name: 'alertname',
  summary: 'summary',
  team: 'team',
  instance: 'instance',
  source: 'source',
  age: 'startsAt',
};

/** Query parameters accepted by the alerts endpoint. */
export interface AlertsQuery {
  limit?: number;
  cursor?: string;
  status?: AlertState;
  source?: string;
  severity?: string;
  team?: string;
  /**
   * Restricts to alerts a silence or inhibition is holding back, or excludes
   * them. Leaving it unset keeps them in the list, which is the console's
   * default: an alert vanishing because somebody else silenced it is the
   * failure silencing is meant to avoid, not cause.
   */
  suppressed?: boolean;
  /**
   * Serialized label matchers (`severity=critical`, `team!=infra`), sent as
   * repeated `match` parameters. All matchers must hold for an alert to
   * appear.
   */
  match?: readonly string[];
  /**
   * Server-side sort field, in the console vocabulary; the wire mapping is
   * applied here. Without one the server's default order applies.
   */
  sort?: AlertSortField;
  /** Sort direction applied together with `sort`. */
  order?: AlertSortOrder;
  /**
   * Label keys to collapse the result by. With any key set, the endpoint
   * returns groups instead of alerts; a group is expanded by asking for its
   * members with the ordinary query plus a matcher on the group's key.
   */
  groupBy?: readonly string[];
}

/** One validated page from the alerts API, mapped into UI rows. */
export interface AlertsPage {
  alerts: AlertSummary[];
  /** Opaque cursor for the next page; empty when no more alerts match. */
  nextCursor: string;
  /** Server-side severity histogram over every matching alert. */
  severityCounts: Record<string, number>;
  /** Server-side count of every matching alert, not just the loaded rows. */
  total: number;
  /**
   * Monotonic cursor for the live event stream (`GET /api/v1/stream`). Every
   * snapshot carries the position the stream should resume from.
   */
  streamCursor: number;
}

/** One collapsed row: the alerts sharing a key combination, summarised. */
export interface AlertGroupSummary {
  /** Label values that define the group, keyed by the grouping label. */
  key: Record<string, string>;
  /** Members matching the current filters and the reader's own access. */
  total: number;
  acknowledged: number;
  /** Members a silence or inhibition is holding back. */
  silenced: number;
  severityCounts: Record<string, number>;
  worstSeverity: Severity;
  /** Preserves the server's severity text when it falls outside the buckets. */
  worstSeverityLabel?: string;
  latestLastSeen: string;
  earliestStartsAt: string;
  /** Newest member; a one-member group opens this alert directly. */
  sampleAlertId: string;
}

/** One validated page of groups. */
export interface AlertGroupsPage {
  groups: AlertGroupSummary[];
  nextCursor: string;
  severityCounts: Record<string, number>;
  /** Matching alerts, counted across every group, not just this page. */
  total: number;
  /** Matching groups, which is what the page count refers to. */
  totalGroups: number;
  streamCursor: number;
}

type FetchLike = (url: string) => Promise<Response>;

export function buildAlertsUrl(query: AlertsQuery = {}): string {
  const params = new URLSearchParams();
  if (query.limit !== undefined) {
    params.set('limit', String(query.limit));
  }
  if (query.cursor !== undefined && query.cursor !== '') {
    params.set('cursor', query.cursor);
  }
  if (query.status !== undefined) {
    params.set('status', query.status);
  }
  if (query.source !== undefined && query.source !== '') {
    params.set('source', query.source);
  }
  if (query.severity !== undefined && query.severity !== '') {
    params.set('severity', query.severity);
  }
  if (query.team !== undefined && query.team !== '') {
    params.set('team', query.team);
  }
  if (query.suppressed !== undefined) {
    params.set('suppressed', query.suppressed ? 'true' : 'false');
  }
  for (const matcher of query.match ?? []) {
    if (matcher !== '') {
      params.append('match', matcher);
    }
  }
  if (query.sort !== undefined) {
    params.set('sort', SORT_PARAM_FIELDS[query.sort]);
    if (query.order !== undefined) {
      // Age ascending reads youngest-first, which is startsAt descending.
      const flipped = query.order === 'asc' ? 'desc' : 'asc';
      params.set('order', query.sort === 'age' ? flipped : query.order);
    }
  }
  if (query.groupBy !== undefined && query.groupBy.length > 0) {
    params.set('groupBy', query.groupBy.join(','));
  }
  const suffix = params.toString();
  return suffix === '' ? ALERTS_URL : `${ALERTS_URL}?${suffix}`;
}

/**
 * Fetches and validates one page of alerts. Same-origin credentials (the
 * fetch default) keep browser session cookies flowing without any
 * client-side transport branching.
 */
export async function fetchAlerts(
  query: AlertsQuery = {},
  fetchImpl: FetchLike = apiFetch,
): Promise<AlertsPage> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(buildAlertsUrl(query)));
  } catch (cause) {
    throw new AlertsApiError('Unable to reach the Promview API', { cause });
  }

  if (!response.ok) {
    throw new AlertsApiError(`Alerts request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch (cause) {
    throw new AlertsApiError('Alerts response was not valid JSON', { cause });
  }

  return parseAlertsResponse(body);
}

/**
 * Fetches and validates one page of groups. Grouping is a shape of the same
 * endpoint, so filters, credentials and error handling are identical.
 */
export async function fetchAlertGroups(
  query: AlertsQuery,
  fetchImpl: FetchLike = apiFetch,
): Promise<AlertGroupsPage> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(buildAlertsUrl(query)));
  } catch (cause) {
    throw new AlertsApiError('Unable to reach the Promview API', { cause });
  }
  if (!response.ok) {
    throw new AlertsApiError(`Alerts request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }
  let body: unknown;
  try {
    body = await response.json();
  } catch (cause) {
    throw new AlertsApiError('Alerts response was not valid JSON', { cause });
  }
  return parseAlertGroupsResponse(body);
}

export function parseAlertGroupsResponse(body: unknown): AlertGroupsPage {
  const record = asRecord(body, 'Alerts response');
  if (!Array.isArray(record.groups)) {
    throw new AlertsApiError('Alerts response was malformed: groups must be a list');
  }
  return {
    groups: record.groups.map((group, index) => parseAlertGroup(group, index)),
    nextCursor: typeof record.nextCursor === 'string' ? record.nextCursor : '',
    severityCounts: numberRecord(record.severityCounts, 'severityCounts'),
    total: requiredNumber(record.total, 'total'),
    totalGroups: requiredNumber(record.totalGroups, 'totalGroups'),
    streamCursor: requiredNumber(record.streamCursor, 'streamCursor'),
  };
}

function parseAlertGroup(value: unknown, index: number): AlertGroupSummary {
  const raw = asRecord(value, `groups[${index}]`);
  const severityRaw = requiredString(raw.worstSeverity, `groups[${index}].worstSeverity`);
  const severity = normalizeSeverity(severityRaw);
  return {
    key: stringRecord(raw.key, `groups[${index}].key`),
    total: requiredNumber(raw.total, `groups[${index}].total`),
    acknowledged: requiredNumber(raw.acknowledged, `groups[${index}].acknowledged`),
    // Servers before silence visibility omit the field; absent means "none
    // known", never a parse failure.
    silenced: typeof raw.silenced === 'number' && Number.isFinite(raw.silenced) ? raw.silenced : 0,
    severityCounts: numberRecord(raw.severityCounts, `groups[${index}].severityCounts`),
    worstSeverity: severity,
    worstSeverityLabel: severityLabelFor(severityRaw, severity),
    latestLastSeen: requiredString(raw.latestLastSeen, `groups[${index}].latestLastSeen`),
    earliestStartsAt: requiredString(raw.earliestStartsAt, `groups[${index}].earliestStartsAt`),
    sampleAlertId: requiredString(raw.sampleAlertId, `groups[${index}].sampleAlertId`),
  };
}

export function parseAlertsResponse(body: unknown): AlertsPage {
  const record = asRecord(body, 'Alerts response');
  if (!Array.isArray(record.alerts)) {
    throw new AlertsApiError('Alerts response was malformed: alerts must be a list');
  }
  return {
    alerts: record.alerts.map((item, index) => parseAlert(item, index)),
    nextCursor: typeof record.nextCursor === 'string' ? record.nextCursor : '',
    severityCounts: numberRecord(record.severityCounts, 'severityCounts'),
    total: requiredNumber(record.total, 'total'),
    streamCursor: requiredNumber(record.streamCursor, 'streamCursor'),
  };
}

function parseAlert(value: unknown, index: number): AlertSummary {
  const raw = asRecord(value, `alerts[${index}]`);
  const id = requiredString(raw.id, `alerts[${index}].id`);
  const status = raw.status;
  if (status !== 'firing' && status !== 'resolved' && status !== 'expired') {
    throw new AlertsApiError(`Alert ${id} has an unsupported status: ${String(status)}`);
  }
  const severityRaw = requiredString(raw.severity, `alerts[${index}].severity`);
  const severity = normalizeSeverity(severityRaw);
  const labels = stringRecord(raw.labels, `alerts[${index}].labels`);
  const annotations = stringRecord(raw.annotations, `alerts[${index}].annotations`);
  return {
    id,
    severity,
    severityLabel: severityLabelFor(severityRaw, severity),
    state: status,
    name: alertName(labels),
    summary: alertSummary(annotations),
    team: presentLabel(labels.team),
    instance: presentLabel(labels.instance),
    source: requiredString(raw.source, `alerts[${index}].source`),
    startsAt: requiredString(raw.startsAt, `alerts[${index}].startsAt`),
    notes: 0,
    labels,
    suppressed: raw.suppressed === true,
    silencedBy: Array.isArray(raw.silencedBy)
      ? raw.silencedBy.filter((entry): entry is string => typeof entry === 'string')
      : [],
    lastSeen: typeof raw.lastSeen === 'string' ? raw.lastSeen : '',
  };
}

function alertName(labels: Record<string, string>): string {
  const name = labels.alertname?.trim();
  return name !== undefined && name !== '' ? name : '(unnamed alert)';
}

function alertSummary(annotations: Record<string, string>): string {
  const summary = annotations.summary?.trim();
  if (summary !== undefined && summary !== '') {
    return summary;
  }
  const description = annotations.description?.trim();
  if (description !== undefined && description !== '') {
    return description;
  }
  return 'No summary or description annotation provided.';
}

function asRecord(value: unknown, context: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new AlertsApiError(`${context} was malformed`);
  }
  return value as Record<string, unknown>;
}

function requiredString(value: unknown, field: string): string {
  if (typeof value !== 'string' || value === '') {
    throw new AlertsApiError(`Alerts response was malformed: ${field} must be a string`);
  }
  return value;
}

function requiredNumber(value: unknown, field: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new AlertsApiError(`Alerts response was malformed: ${field} must be a number`);
  }
  return value;
}

/** The Go server encodes unset maps as null; treat both as an empty record. */
function stringRecord(value: unknown, field: string): Record<string, string> {
  if (value === null || value === undefined) {
    return {};
  }
  const record = asRecord(value, field);
  const result: Record<string, string> = {};
  for (const [key, entry] of Object.entries(record)) {
    if (typeof entry !== 'string') {
      throw new AlertsApiError(`Alerts response was malformed: ${field}.${key} must be a string`);
    }
    result[key] = entry;
  }
  return result;
}

function numberRecord(value: unknown, field: string): Record<string, number> {
  if (value === null || value === undefined) {
    return {};
  }
  const record = asRecord(value, field);
  const result: Record<string, number> = {};
  for (const [key, entry] of Object.entries(record)) {
    if (typeof entry !== 'number' || !Number.isFinite(entry)) {
      throw new AlertsApiError(`Alerts response was malformed: ${field}.${key} must be a number`);
    }
    result[key] = entry;
  }
  return result;
}

function presentLabel(value: string | undefined): string | undefined {
  return value !== undefined && value.trim() !== '' ? value : undefined;
}
