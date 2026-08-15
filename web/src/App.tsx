import { useCallback, useMemo, useState } from 'react';
import { matchesFilter } from './alerts/filter';
import type { AlertStreamEvent } from './alerts/stream';
import type { AlertSummary } from './alerts/types';
import { AlertDetailDrawer } from './components/AlertDetailDrawer';
import { AlertTable } from './components/AlertTable';
import { FilterBar } from './components/FilterBar';
import { SeverityStrip } from './components/SeverityStrip';
import { StatusFooter } from './components/StatusFooter';
import { TopBar } from './components/TopBar';
import type { ConnectionState } from './components/TopBar';
import { PulseMark } from './components/icons';
import { useAlertDetail } from './hooks/useAlertDetail';
import { useAlertRoute } from './hooks/useAlertRoute';
import { useAlerts } from './hooks/useAlerts';
import { useAlertStream } from './hooks/useAlertStream';
import { useRuntimeConfig } from './hooks/useRuntimeConfig';

const DEFAULT_PRODUCT_NAME = 'Promview';

const NO_ALERTS: readonly AlertSummary[] = [];

/**
 * Console shell for the Alerts route. Boots by fetching the runtime
 * configuration; once that succeeds it pages in firing alerts from
 * `GET /api/v1/alerts`, keeping the alerts loading and error states inside
 * the console area so the configured shell stays put. After the first ready
 * snapshot it opens the live event stream; stream events coalesce into quiet
 * first-page refreshes, and the top bar mirrors the live connection.
 *
 * Row selection is deep-linkable via `/alerts/{id}`: the selected alert loads
 * in a detail drawer, and stream events targeting it quietly refresh both the
 * list and the open detail.
 */
export default function App() {
  const { state: configState, retry: retryConfig } = useRuntimeConfig();
  const {
    state: alertsState,
    retry: retryAlerts,
    loadMore,
    scheduleLiveRefresh,
  } = useAlerts(configState.status === 'ready');
  const { selectedAlertId, openAlert, closeAlert } = useAlertRoute();
  const {
    state: detailState,
    retry: retryDetail,
    refreshIfSelected: refreshDetailIfSelected,
  } = useAlertDetail(selectedAlertId);
  // A stream event refreshes the list; when it targets the open alert, the
  // detail drawer quietly refreshes alongside it.
  const handleAlertEvent = useCallback(
    (event: AlertStreamEvent) => {
      scheduleLiveRefresh();
      refreshDetailIfSelected(event.alertId);
    },
    [scheduleLiveRefresh, refreshDetailIfSelected],
  );
  const streamStatus = useAlertStream({
    cursor: alertsState.status === 'ready' ? alertsState.data.streamCursor : null,
    onAlertEvent: handleAlertEvent,
  });
  const [filterDraft, setFilterDraft] = useState('');
  const [appliedFilter, setAppliedFilter] = useState('');

  const loadedAlerts = alertsState.status === 'ready' ? alertsState.data.alerts : NO_ALERTS;
  const visibleAlerts = useMemo(
    () => loadedAlerts.filter((alert) => matchesFilter(alert, appliedFilter)),
    [loadedAlerts, appliedFilter],
  );

  const config = configState.status === 'ready' ? configState.config : undefined;
  const filterActive = appliedFilter.trim() !== '';

  // The indicator follows the live path end to end: shell sync, hard
  // request failures, then the stream itself.
  let connection: ConnectionState;
  if (configState.status === 'loading' || alertsState.status === 'loading') {
    connection = 'loading';
  } else if (configState.status === 'error' || alertsState.status === 'error') {
    connection = 'error';
  } else if (streamStatus === 'connected') {
    connection = 'ready';
  } else if (streamStatus === 'reconnecting') {
    connection = 'reconnecting';
  } else {
    connection = 'loading';
  }

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        Skip to alerts
      </a>
      <TopBar
        productName={config?.productName ?? DEFAULT_PRODUCT_NAME}
        connection={connection}
        authMode={config?.authMode}
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
        ) : (
          <>
            <div className="console-head">
              <h1 className="console-title">Alerts</h1>
              <p className="console-meta">live view · all sources</p>
            </div>
            {config !== undefined && config.authMode !== 'open' ? (
              <div className="notice" role="note">
                This deployment uses {config.authMode === 'ldap' ? 'LDAP' : 'OIDC'} sign-in.
                Interactive authentication is not available in this build yet, so the console is
                shown read-only.
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
                  shown={visibleAlerts.length}
                  total={alertsState.data.total}
                  onChange={setFilterDraft}
                  onApply={setAppliedFilter}
                />
                <SeverityStrip
                  counts={alertsState.data.severityCounts}
                  total={alertsState.data.total}
                />
                <AlertTable
                  alerts={visibleAlerts}
                  filterActive={filterActive}
                  filterQuery={appliedFilter}
                  selectedId={selectedAlertId}
                  onSelect={(alert) => openAlert(alert.id)}
                  onClearFilter={() => {
                    setFilterDraft('');
                    setAppliedFilter('');
                  }}
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
      {selectedAlertId !== null ? (
        <AlertDetailDrawer
          key={selectedAlertId}
          alertId={selectedAlertId}
          state={detailState}
          onClose={closeAlert}
          onRetry={retryDetail}
        />
      ) : null}
      <StatusFooter
        authMode={config?.authMode}
        stream={alertsState.status === 'ready' ? streamStatus : undefined}
      />
    </div>
  );
}
