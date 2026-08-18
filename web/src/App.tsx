import { useCallback, useMemo, useState } from 'react';
import type { AlertSort } from './alerts/api';
import {
  FilterParseError,
  formatFilter,
  parseFilter,
  serializeMatcher,
  upsertMatcher,
} from './alerts/filter';
import type { LabelMatcher } from './alerts/filter';
import type { AlertStreamEvent } from './alerts/stream';
import type { AlertSummary } from './alerts/types';
import { OIDC_LOGIN_URL } from './auth/session';
import type { NavigateTo } from './auth/session';
import { AlertDetailDrawer } from './components/AlertDetailDrawer';
import { AlertTable } from './components/AlertTable';
import { resolveColumns } from './alerts/columns';
import { FilterBar } from './components/FilterBar';
import { SeverityStrip } from './components/SeverityStrip';
import { StatusFooter } from './components/StatusFooter';
import { TopBar } from './components/TopBar';
import type { ConnectionState } from './components/TopBar';
import { PulseMark } from './components/icons';
import { useAlertDetail } from './hooks/useAlertDetail';
import { useAlertNotifications } from './hooks/useAlertNotifications';
import { useAlertRoute } from './hooks/useAlertRoute';
import { useAlerts } from './hooks/useAlerts';
import { usePreferences } from './hooks/usePreferences';
import { useAlertStream } from './hooks/useAlertStream';
import { useRuntimeConfig } from './hooks/useRuntimeConfig';
import { useSession } from './hooks/useSession';

const DEFAULT_PRODUCT_NAME = 'Promview';

const NO_ALERTS: readonly AlertSummary[] = [];

export interface AppProps {
  /**
   * Full-page navigation seam for the OIDC login/logout round-trips. Defaults
   * to `window.location.assign`; tests and the desktop client inject their own.
   */
  navigate?: NavigateTo;
}

/**
 * Console shell for the Alerts route. Boots by fetching the runtime
 * configuration; in OIDC mode it then verifies the session with
 * `GET /api/v1/me` before any alert fetch or stream starts — a 401 gates the
 * console behind a sign-in link, a 403 behind an access-denied panel. A 401
 * from any alert request after boot (session expired) drops back to that
 * same sign-in gate and stops alert/SSE activity. Open mode keeps its
 * anonymous viewer without the extra request. Once unlocked, it pages in
 * firing alerts from
 * `GET /api/v1/alerts`, keeping the alerts loading and error states inside
 * the console area so the configured shell stays put. After the first ready
 * snapshot it opens the live event stream; stream events coalesce into quiet
 * first-page refreshes, and the top bar mirrors the live connection.
 *
 * Row selection is deep-linkable via `/alerts/{id}`: the selected alert loads
 * in a detail drawer, and stream events targeting it quietly refresh both the
 * list and the open detail.
 */
export default function App({ navigate }: AppProps = {}) {
  const { state: configState, retry: retryConfig } = useRuntimeConfig();
  const authMode = configState.status === 'ready' ? configState.config.authMode : undefined;
  const {
    state: sessionState,
    retry: retrySession,
    signOut,
    signOutState,
    expire: expireSession,
  } = useSession(authMode, { navigate });
  // Alert fetches and the live stream stay paused until the deployment's auth
  // requirements are satisfied: config loaded, and — for OIDC — a verified
  // session. Gating the detail drawer on the same flag keeps deep links from
  // firing unauthenticated requests. A 401 from any alert request after boot
  // means the session expired: expire() drops the console back to the OIDC
  // sign-in gate, which pauses fetches and closes the stream again.
  const consoleUnlocked =
    configState.status === 'ready' &&
    (configState.config.authMode !== 'oidc' || sessionState.status === 'ready');
  // Server-side filter/sort state. The filter input holds a draft; only a
  // draft that parses into label matchers is applied, and applying restarts
  // the alert query from the first page with repeated `match` params.
  const [filterDraft, setFilterDraft] = useState('');
  const [appliedMatchers, setAppliedMatchers] = useState<readonly LabelMatcher[]>([]);
  const [filterError, setFilterError] = useState<string | null>(null);
  const [sort, setSort] = useState<AlertSort | null>(null);

  const applyFilter = useCallback((raw: string) => {
    try {
      setAppliedMatchers(parseFilter(raw));
      setFilterError(null);
    } catch (error) {
      // The invalid draft stays in the input; the previous filter remains
      // applied and no request goes out with broken syntax.
      setFilterError(
        error instanceof FilterParseError ? error.message : 'Invalid filter expression',
      );
    }
  }, []);

  const changeFilter = useCallback((value: string) => {
    setFilterDraft(value);
    // Editing the draft retires the last apply attempt's error.
    setFilterError(null);
  }, []);

  const clearFilter = useCallback(() => {
    setFilterDraft('');
    setAppliedMatchers([]);
    setFilterError(null);
  }, []);

  // Include/exclude buttons in the detail drawer upsert one matcher and
  // apply immediately; the canonical expression is reflected into the input.
  const applyLabelMatcher = (matcher: LabelMatcher) => {
    const next = upsertMatcher(appliedMatchers, matcher);
    setAppliedMatchers(next);
    setFilterDraft(formatFilter(next));
    setFilterError(null);
  };

  const alertsQuery = useMemo(
    () => ({
      match: appliedMatchers.map(serializeMatcher),
      sort: sort?.field,
      order: sort?.order,
    }),
    [appliedMatchers, sort],
  );

  const {
    state: alertsState,
    retry: retryAlerts,
    loadMore,
    scheduleLiveRefresh,
  } = useAlerts(consoleUnlocked, alertsQuery, { onUnauthorized: expireSession });

  // Layout preferences follow the operator; they are only fetched once the
  // console is unlocked, since an unauthenticated request would just 401.
  const { preferences } = usePreferences(consoleUnlocked, authMode === 'oidc');
  const columns = resolveColumns(preferences.columns.map((column) => column.id));
  const { selectedAlertId, openAlert, closeAlert } = useAlertRoute();
  const effectiveSelectedAlertId = consoleUnlocked ? selectedAlertId : null;
  const {
    state: detailState,
    retry: retryDetail,
    refreshIfSelected: refreshDetailIfSelected,
    acknowledge: acknowledgeDetail,
  } = useAlertDetail(effectiveSelectedAlertId, { onUnauthorized: expireSession });
  // Browser notifications for new critical alerts while the tab is hidden;
  // a click focuses the window and deep-links to the alert.
  const {
    optInState: notificationOptInState,
    toggleOptIn: toggleNotificationOptIn,
    handleEvent: handleNotificationEvent,
  } = useAlertNotifications({ navigateToAlert: openAlert });
  // Every stream event — including redacted removals — refreshes the list;
  // when it targets the open alert, the detail drawer quietly refreshes
  // alongside it. The same event also feeds the notification check (opted
  // in + hidden tab + new critical alert; removals never qualify).
  const handleAlertEvent = useCallback(
    (event: AlertStreamEvent) => {
      scheduleLiveRefresh();
      refreshDetailIfSelected(event.alertId);
      handleNotificationEvent(event);
    },
    [scheduleLiveRefresh, refreshDetailIfSelected, handleNotificationEvent],
  );
  const streamStatus = useAlertStream({
    cursor:
      consoleUnlocked && alertsState.status === 'ready' ? alertsState.data.streamCursor : null,
    onAlertEvent: handleAlertEvent,
  });

  const loadedAlerts = alertsState.status === 'ready' ? alertsState.data.alerts : NO_ALERTS;

  const config = configState.status === 'ready' ? configState.config : undefined;
  const filterActive = appliedMatchers.length > 0;
  const appliedFilterText = formatFilter(appliedMatchers);

  // The indicator follows the live path end to end: shell sync, session
  // verification, hard request failures, then the stream itself.
  let connection: ConnectionState;
  if (
    configState.status === 'loading' ||
    sessionState.status === 'loading' ||
    alertsState.status === 'loading'
  ) {
    connection = 'loading';
  } else if (
    configState.status === 'error' ||
    sessionState.status === 'error' ||
    alertsState.status === 'error'
  ) {
    connection = 'error';
  } else if (streamStatus === 'connected') {
    connection = 'ready';
  } else if (streamStatus === 'reconnecting') {
    connection = 'reconnecting';
  } else {
    connection = 'loading';
  }

  return (
    <div className="app-shell" data-density={preferences.density}>
      <a className="skip-link" href="#main">
        Skip to alerts
      </a>
      <TopBar
        productName={config?.productName ?? DEFAULT_PRODUCT_NAME}
        connection={connection}
        authMode={config?.authMode}
        session={sessionState.status === 'ready' ? sessionState.session : undefined}
        onSignOut={signOut}
        signOutPending={signOutState === 'pending'}
        notificationOptIn={{
          state: notificationOptInState,
          onToggle: toggleNotificationOptIn,
        }}
      />
      <main id="main" className="console">
        {configState.status === 'loading' ? (
          <section className="boot boot-loading" aria-label="Loading">
            <PulseMark className="boot-mark" />
            <h1 className="boot-title">Connecting to the Promview API</h1>
            <p className="boot-copy" role="status">
              Fetching runtime configuration from /api/v1/config…
            </p>
          </section>
        ) : configState.status === 'error' ? (
          <section className="boot boot-error" role="alert" aria-label="Connection error">
            <PulseMark className="boot-mark" />
            <h1 className="boot-title boot-error-title">Cannot reach the Promview API</h1>
            <p className="boot-copy">{configState.error.message}</p>
            <button type="button" className="button" onClick={retryConfig}>
              Retry connection
            </button>
          </section>
        ) : configState.config.authMode === 'oidc' && sessionState.status === 'unauthenticated' ? (
          <section className="boot" aria-label="Sign in required">
            <PulseMark className="boot-mark" />
            <h1 className="boot-title">Sign in required</h1>
            <p className="boot-copy">
              This deployment uses OIDC sign-in. Alerts and the live stream stay paused until you
              sign in with your identity provider.
            </p>
            <a className="button" href={OIDC_LOGIN_URL}>
              Sign in with your identity provider
            </a>
          </section>
        ) : configState.config.authMode === 'oidc' && sessionState.status === 'forbidden' ? (
          <section className="boot boot-error" role="alert" aria-label="Access denied">
            <PulseMark className="boot-mark" />
            <h1 className="boot-title boot-error-title">This account has no read access</h1>
            <p className="boot-copy">
              You are signed in, but your account has no viewer, operator, or administrator role for
              this console. Ask an administrator to grant access, or sign out and switch accounts.
            </p>
            <button
              type="button"
              className="button"
              onClick={signOut}
              disabled={signOutState === 'pending'}
            >
              {signOutState === 'pending' ? 'Signing out…' : 'Sign out'}
            </button>
          </section>
        ) : configState.config.authMode === 'oidc' && sessionState.status === 'error' ? (
          <section className="boot boot-error" role="alert" aria-label="Session error">
            <PulseMark className="boot-mark" />
            <h1 className="boot-title boot-error-title">Cannot verify your session</h1>
            <p className="boot-copy">{sessionState.error.message}</p>
            <button type="button" className="button" onClick={retrySession}>
              Retry session check
            </button>
          </section>
        ) : configState.config.authMode === 'oidc' && sessionState.status !== 'ready' ? (
          <section className="boot boot-loading" aria-label="Loading">
            <PulseMark className="boot-mark" />
            <h1 className="boot-title">Verifying your session</h1>
            <p className="boot-copy" role="status">
              Checking your session with /api/v1/me before loading alerts…
            </p>
          </section>
        ) : (
          <>
            <div className="console-head">
              <h1 className="console-title">Alerts</h1>
              <p className="console-meta">live view · all sources</p>
            </div>
            {signOutState === 'error' ? (
              <div className="notice" role="alert">
                Sign-out failed. Check the connection to the Promview API and try again.
              </div>
            ) : null}
            {alertsState.status === 'loading' ? (
              <section className="alerts-panel alerts-panel-loading" aria-label="Loading alerts">
                <PulseMark className="alerts-panel-mark" />
                <p className="alerts-panel-copy" role="status">
                  Loading firing alerts from /api/v1/alerts…
                </p>
              </section>
            ) : alertsState.status === 'error' ? (
              <section
                className="alerts-panel alerts-panel-error"
                role="alert"
                aria-label="Alerts error"
              >
                <h2 className="alerts-panel-title">Cannot load alerts</h2>
                <p className="alerts-panel-copy">{alertsState.error.message}</p>
                <button type="button" className="button" onClick={retryAlerts}>
                  Retry alerts request
                </button>
              </section>
            ) : (
              <>
                <FilterBar
                  value={filterDraft}
                  shown={loadedAlerts.length}
                  total={alertsState.data.total}
                  error={filterError}
                  onChange={changeFilter}
                  onApply={applyFilter}
                />
                <SeverityStrip
                  counts={alertsState.data.severityCounts}
                  total={alertsState.data.total}
                />
                <AlertTable
                  alerts={loadedAlerts}
                  columns={columns}
                  filterActive={filterActive}
                  filterQuery={appliedFilterText}
                  selectedId={effectiveSelectedAlertId}
                  onSelect={(alert) => openAlert(alert.id)}
                  onClearFilter={clearFilter}
                  sort={sort}
                  onSortChange={setSort}
                  pagination={{
                    loaded: alertsState.data.alerts.length,
                    total: alertsState.data.total,
                    hasMore: alertsState.data.nextCursor !== '',
                    loadingMore: alertsState.loadingMore,
                    error: alertsState.moreError,
                    onLoadMore: loadMore,
                  }}
                />
              </>
            )}
          </>
        )}
      </main>
      {effectiveSelectedAlertId !== null ? (
        <AlertDetailDrawer
          key={effectiveSelectedAlertId}
          alertId={effectiveSelectedAlertId}
          state={detailState}
          onClose={closeAlert}
          onRetry={retryDetail}
          onAcknowledge={acknowledgeDetail}
          onFilterLabel={applyLabelMatcher}
        />
      ) : null}
      <StatusFooter
        authMode={config?.authMode}
        stream={alertsState.status === 'ready' ? streamStatus : undefined}
      />
    </div>
  );
}
