import { ALERTS_URL, AlertsApiError } from './api';
import { normalizeSeverity, severityLabelFor } from './severity';
import type { Severity } from './severity';
import type { AlertState } from './types';
import { apiUrl } from '../config/apiBase';
import { apiFetch } from '../config/transport';

/**
 * Client for the alert detail API, `GET /api/v1/alerts/{id}`. Same-origin and
 * cookie-based like the alerts list client, with the response validated and
 * mapped here so components never touch raw API payloads. The one-call detail
 * endpoint is preferred over `GET /api/v1/alerts/{id}/events`: it returns the
 * alert snapshot plus the lifecycle history in a single round trip.
 */

/**
 * Per-alert operator actions, decided server-side from the caller's roles
 * and label scopes (`actions` in the API payload). The UI only offers a
 * mutating control when the matching flag is true; anything absent means
 * "not allowed".
 */
export interface AlertActions {
  canAcknowledge: boolean;
  /** Operator rights on this alert plus an Alertmanager to write the silence to. */
  canSilence: boolean;
  /** Operator rights to record who owns the alert. */
  canAssign: boolean;
  /** Operator rights to file the alert as handled, or reopen it. */
  canClose: boolean;
  /** Operator rights to append a note. */
  canNote: boolean;
}

/** Fully validated alert detail, mapped for the drawer panels. */
export interface AlertDetail {
  id: string;
  fingerprint: string;
  source: string;
  status: AlertState;
  severity: Severity;
  /** Display text for the severity tag; preserves unknown source values. */
  severityLabel: string;
  /** Alert name from labels.alertname, with an explicit fallback. */
  name: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  startsAt: string;
  endsAt: string | null;
  generatorURL: string;
  externalURL: string;
  firstSeen: string;
  lastSeen: string;
  repeatCount: number;
  occurrence: number;
  /** Whether an operator has acknowledged the alert. */
  acknowledged: boolean;
  /** Actor who acknowledged; empty when unset or not acknowledged. */
  acknowledgedBy: string;
  /** Acknowledgement timestamp; null when not acknowledged. */
  acknowledgedAt: string | null;
  /** A silence or inhibition is holding this alert back at the source. */
  suppressed: boolean;
  /** Ids of the silences currently matching; empty means inhibited instead. */
  silencedBy: string[];
  /** Who owns the alert, as free text. Empty means unassigned. */
  assignee: string;
  /** The operator who decided the assignment, which is not who owns it. */
  assignedBy: string;
  assignedAt: string | null;
  /**
   * An operator filed this alert as handled. Separate from `status`, which is
   * what the source reports: closing writes nothing to Alertmanager, so a
   * closed alert can still be firing.
   */
  closed: boolean;
  closedBy: string;
  closedAt: string | null;
  /** Server-provided actions the caller may run against this alert. */
  actions: AlertActions;
  /** Source payload as delivered by Alertmanager; rendered as plain JSON. */
  rawData: unknown;
}

/** One immutable lifecycle history entry. */
export interface AlertHistoryEvent {
  id: number;
  occurrence: number;
  /** Raw event type from the API (created/updated/resolved/reopened/imported). */
  type: string;
  /** Human-readable label; preserves unknown types instead of dropping them. */
  typeLabel: string;
  sourceStatus: string;
  actor: string;
  message: string;
  occurredAt: string;
}

/**
 * A silence promview created itself, kept after Alertmanager expired it.
 *
 * Only promview's own silences appear here. One made straight on the
 * Alertmanager is just as real and just as suppressing, and the drawer says so
 * rather than inventing an author for it.
 */
export interface AlertSilenceRecord {
  source: string;
  silenceId: string;
  matchers: Record<string, string>;
  createdBy: string;
  comment: string;
  startsAt: string;
  endsAt: string;
}

/** The validated detail endpoint payload. */
export interface AlertDetailResult {
  alert: AlertDetail;
  history: AlertHistoryEvent[];
  /** Provenance for the silences named in `alert.silencedBy`, where known. */
  silences: AlertSilenceRecord[];
  /** Operator notes, oldest first — the order a handover is read in. */
  notes: AlertNote[];
}

/**
 * One operator's written judgement about the alert. Append-only: the API has
 * no endpoint that edits or removes one, so the drawer offers neither.
 */
export interface AlertNote {
  id: number;
  /**
   * The occurrence the note was written against. An alert that resolved and
   * fired again is a different incident, and a note about the previous one must
   * not read as though it describes this one.
   */
  occurrence: number;
  author: string;
  body: string;
  createdAt: string;
}

/** Human labels for the lifecycle event types the API emits today. */
const HISTORY_TYPE_LABELS: Record<string, string> = {
  created: 'Created',
  updated: 'Updated',
  resolved: 'Resolved',
  reopened: 'Reopened',
  imported: 'Imported',
};

/**
 * Display label for a history event type. Known types get the curated label;
 * unknown types keep their source text so schema drift never hides events.
 */
export function historyTypeLabel(type: string): string {
  const known = HISTORY_TYPE_LABELS[type.trim().toLowerCase()];
  if (known !== undefined) {
    return known;
  }
  const trimmed = type.trim();
  return trimmed === '' ? 'Event' : trimmed;
}

export function buildAlertDetailUrl(id: string): string {
  return `${ALERTS_URL}/${encodeURIComponent(id)}`;
}

export function buildAcknowledgeUrl(id: string): string {
  return `${buildAlertDetailUrl(id)}/acknowledge`;
}

export function buildAssigneeUrl(id: string): string {
  return `${buildAlertDetailUrl(id)}/assignee`;
}

export function buildCloseUrl(id: string): string {
  return `${buildAlertDetailUrl(id)}/close`;
}

export function buildNotesUrl(id: string): string {
  return `${buildAlertDetailUrl(id)}/notes`;
}

type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

/**
 * Fetches and validates one alert detail. Follows the same error contract as
 * the list client: network failures, HTTP errors, and malformed payloads all
 * surface as `AlertsApiError`.
 */
export async function fetchAlertDetail(
  id: string,
  fetchImpl: FetchLike = apiFetch,
): Promise<AlertDetailResult> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(buildAlertDetailUrl(id)));
  } catch (cause) {
    throw new AlertsApiError('Unable to reach the Promview API', { cause });
  }

  if (!response.ok) {
    throw new AlertsApiError(`Alert detail request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch (cause) {
    throw new AlertsApiError('Alert detail response was not valid JSON', { cause });
  }

  return parseAlertDetailResponse(body);
}

/** True when the detail request failed because the alert does not exist. */
export function isAlertNotFound(error: unknown): boolean {
  return error instanceof AlertsApiError && error.status === 404;
}

/**
 * Toggles the acknowledgement of one alert through
 * `POST /api/v1/alerts/{id}/acknowledge` with a JSON `{acknowledged}` body.
 * The response is the full detail envelope (updated alert, including the
 * refreshed acknowledgement state and actions, plus the lifecycle history),
 * so callers can replace their cached detail wholesale. Same-origin and
 * cookie-based like the other clients; the same error contract applies — a
 * 403 means the server withheld the operator permission the UI gated on,
 * and surfaces like any other failure.
 */
export async function setAlertAcknowledgement(
  id: string,
  acknowledged: boolean,
  fetchImpl: FetchLike = apiFetch,
): Promise<AlertDetailResult> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(buildAcknowledgeUrl(id)), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ acknowledged }),
    });
  } catch (cause) {
    throw new AlertsApiError('Unable to reach the Promview API', { cause });
  }

  if (!response.ok) {
    throw new AlertsApiError(`Acknowledge request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch (cause) {
    throw new AlertsApiError('Acknowledge response was not valid JSON', { cause });
  }

  return parseAlertDetailResponse(body);
}

/**
 * Returns the URL only when it is safe to open in a new tab (http/https).
 * Anything else — empty values, relative paths, javascript: URLs — yields
 * null so the UI renders plain text instead of a link.
 */
export function safeExternalUrl(value: string): string | null {
  const trimmed = value.trim();
  if (trimmed === '') {
    return null;
  }
  try {
    const url = new URL(trimmed);
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.toString() : null;
  } catch {
    return null;
  }
}

export function parseAlertDetailResponse(body: unknown): AlertDetailResult {
  const record = asRecord(body, 'Alert detail response');
  const historyValue = record.history;
  return {
    alert: parseAlertDetail(record.alert),
    history: historyValue === null || historyValue === undefined ? [] : parseHistory(historyValue),
    silences: parseSilences(record.silences),
    notes: parseNotes(record.notes),
  };
}

/** An absent or malformed notes array means no notes, never a broken drawer. */
function parseNotes(value: unknown): AlertNote[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const notes: AlertNote[] = [];
  for (const entry of value) {
    if (typeof entry !== 'object' || entry === null) {
      continue;
    }
    const raw = entry as Record<string, unknown>;
    const body = typeof raw.body === 'string' ? raw.body : '';
    if (body === '') {
      continue;
    }
    notes.push({
      id: typeof raw.id === 'number' ? raw.id : 0,
      occurrence: typeof raw.occurrence === 'number' ? raw.occurrence : 0,
      author: typeof raw.author === 'string' ? raw.author : '',
      body,
      createdAt: typeof raw.createdAt === 'string' ? raw.createdAt : '',
    });
  }
  return notes;
}

/**
 * Silence provenance never fails the whole detail. A malformed or absent entry
 * costs an explanation; refusing to render the alert costs the operator the
 * alert itself, which is the worse trade.
 */
function parseSilences(value: unknown): AlertSilenceRecord[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((entry): AlertSilenceRecord[] => {
    if (typeof entry !== 'object' || entry === null || Array.isArray(entry)) {
      return [];
    }
    const raw = entry as Record<string, unknown>;
    const silenceId = optionalString(raw.silenceId);
    if (silenceId === '') {
      return [];
    }
    const matchers: Record<string, string> = {};
    if (typeof raw.matchers === 'object' && raw.matchers !== null && !Array.isArray(raw.matchers)) {
      for (const [name, item] of Object.entries(raw.matchers as Record<string, unknown>)) {
        if (typeof item === 'string') {
          matchers[name] = item;
        }
      }
    }
    return [
      {
        source: optionalString(raw.source),
        silenceId,
        matchers,
        createdBy: optionalString(raw.createdBy),
        comment: optionalString(raw.comment),
        startsAt: optionalString(raw.startsAt),
        endsAt: optionalString(raw.endsAt),
      },
    ];
  });
}

function parseAlertDetail(value: unknown): AlertDetail {
  const raw = asRecord(value, 'alert');
  const id = requiredString(raw.id, 'alert.id');
  const status = raw.status;
  if (status !== 'firing' && status !== 'resolved' && status !== 'expired') {
    throw new AlertsApiError(`Alert ${id} has an unsupported status: ${String(status)}`);
  }
  const severityRaw = requiredString(raw.severity, 'alert.severity');
  const severity = normalizeSeverity(severityRaw);
  const labels = stringRecord(raw.labels, 'alert.labels');
  const annotations = stringRecord(raw.annotations, 'alert.annotations');
  return {
    id,
    fingerprint: optionalString(raw.fingerprint),
    source: requiredString(raw.source, 'alert.source'),
    status,
    severity,
    severityLabel: severityLabelFor(severityRaw, severity),
    name: alertName(labels),
    labels,
    annotations,
    startsAt: requiredString(raw.startsAt, 'alert.startsAt'),
    endsAt:
      raw.endsAt === null || raw.endsAt === undefined
        ? null
        : requiredString(raw.endsAt, 'alert.endsAt'),
    generatorURL: optionalString(raw.generatorURL),
    externalURL: optionalString(raw.externalURL),
    firstSeen: requiredString(raw.firstSeen, 'alert.firstSeen'),
    lastSeen: requiredString(raw.lastSeen, 'alert.lastSeen'),
    repeatCount: requiredNumber(raw.repeatCount, 'alert.repeatCount'),
    occurrence: requiredNumber(raw.occurrence, 'alert.occurrence'),
    suppressed: raw.suppressed === true,
    silencedBy: Array.isArray(raw.silencedBy)
      ? raw.silencedBy.filter((entry): entry is string => typeof entry === 'string')
      : [],
    assignee: optionalString(raw.assignee),
    assignedBy: optionalString(raw.assignedBy),
    assignedAt:
      raw.assignedAt === null || raw.assignedAt === undefined
        ? null
        : optionalString(raw.assignedAt),
    closed: raw.closed === true,
    closedBy: optionalString(raw.closedBy),
    closedAt:
      raw.closedAt === null || raw.closedAt === undefined ? null : optionalString(raw.closedAt),
    acknowledged: raw.acknowledged === true,
    acknowledgedBy: optionalString(raw.acknowledgedBy),
    acknowledgedAt:
      raw.acknowledgedAt === null || raw.acknowledgedAt === undefined
        ? null
        : requiredString(raw.acknowledgedAt, 'alert.acknowledgedAt'),
    actions: parseActions(raw.actions),
    rawData: raw.rawData === null || raw.rawData === undefined ? {} : raw.rawData,
  };
}

/** An absent or malformed actions envelope means "no actions allowed". */
function parseActions(value: unknown): AlertActions {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {
      canAcknowledge: false,
      canSilence: false,
      canAssign: false,
      canClose: false,
      canNote: false,
    };
  }
  const record = value as Record<string, unknown>;
  return {
    canAcknowledge: record.canAcknowledge === true,
    canSilence: record.canSilence === true,
    canAssign: record.canAssign === true,
    canClose: record.canClose === true,
    canNote: record.canNote === true,
  };
}

function parseHistory(value: unknown): AlertHistoryEvent[] {
  if (!Array.isArray(value)) {
    throw new AlertsApiError('Alert detail response was malformed: history must be a list');
  }
  return value.map((item, index) => parseHistoryEvent(item, index));
}

function parseHistoryEvent(value: unknown, index: number): AlertHistoryEvent {
  const raw = asRecord(value, `history[${index}]`);
  const type = requiredString(raw.type, `history[${index}].type`);
  return {
    id: requiredNumber(raw.id, `history[${index}].id`),
    occurrence: requiredNumber(raw.occurrence, `history[${index}].occurrence`),
    type,
    typeLabel: historyTypeLabel(type),
    sourceStatus: optionalString(raw.sourceStatus),
    actor: optionalString(raw.actor),
    message: optionalString(raw.message),
    occurredAt: requiredString(raw.occurredAt, `history[${index}].occurredAt`),
  };
}

function alertName(labels: Record<string, string>): string {
  const name = labels.alertname?.trim();
  return name !== undefined && name !== '' ? name : '(unnamed alert)';
}

function asRecord(value: unknown, context: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new AlertsApiError(`${context} was malformed`);
  }
  return value as Record<string, unknown>;
}

function requiredString(value: unknown, field: string): string {
  if (typeof value !== 'string' || value === '') {
    throw new AlertsApiError(`Alert detail response was malformed: ${field} must be a string`);
  }
  return value;
}

/** Optional text: unset or empty values collapse to the empty string. */
function optionalString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function requiredNumber(value: unknown, field: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new AlertsApiError(`Alert detail response was malformed: ${field} must be a number`);
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
      throw new AlertsApiError(
        `Alert detail response was malformed: ${field}.${key} must be a string`,
      );
    }
    result[key] = entry;
  }
  return result;
}

/**
 * Shared shape of the three mutating detail clients. Each sends a JSON body to
 * one endpoint and gets the full detail envelope back, so a caller replaces its
 * cached detail wholesale rather than patching fields it did not fetch.
 *
 * A 403 is surfaced like any other failure: it means the server withheld a
 * permission the UI gated on, which happens legitimately when a binding changes
 * between the page load and the click, and hiding it would leave the operator
 * wondering why nothing happened.
 */
async function mutateAlertDetail(
  url: string,
  method: string,
  body: unknown,
  what: string,
  fetchImpl: FetchLike,
): Promise<AlertDetailResult> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(url), {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  } catch (cause) {
    throw new AlertsApiError('Unable to reach the Promview API', { cause });
  }
  if (!response.ok) {
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
  return parseAlertDetailResponse(payload);
}

/**
 * Records who owns an alert, or clears it with an empty string. A PUT because
 * the request states the assignment in full: sending it twice leaves the same
 * owner, and there is no separate unassign verb.
 *
 * The assignee is free text, not a Promview user. An alert is routinely handed
 * to somebody who has never signed in — a vendor, a rota address, the name in a
 * runbook — so the field must not be constrained to known accounts.
 */
export async function setAlertAssignee(
  id: string,
  assignee: string,
  fetchImpl: FetchLike = apiFetch,
): Promise<AlertDetailResult> {
  return mutateAlertDetail(buildAssigneeUrl(id), 'PUT', { assignee }, 'Assign', fetchImpl);
}

/**
 * Files an alert as handled, or reopens it. Promview-local: nothing is written
 * to Alertmanager and the alert keeps reporting whatever the source says, so a
 * closed alert can still be firing.
 */
export async function setAlertClosed(
  id: string,
  closed: boolean,
  fetchImpl: FetchLike = apiFetch,
): Promise<AlertDetailResult> {
  return mutateAlertDetail(buildCloseUrl(id), 'POST', { closed }, 'Close', fetchImpl);
}

/**
 * Appends one operator note. POST rather than PUT because each call adds a note
 * rather than restating the set; notes are append-only and there is no endpoint
 * that edits or removes one.
 */
export async function addAlertNote(
  id: string,
  body: string,
  fetchImpl: FetchLike = apiFetch,
): Promise<AlertDetailResult> {
  return mutateAlertDetail(buildNotesUrl(id), 'POST', { body }, 'Note', fetchImpl);
}
