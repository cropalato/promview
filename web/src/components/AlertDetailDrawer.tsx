import { useEffect, useId, useRef, useState } from 'react';
import type { KeyboardEvent } from 'react';
import type { LabelMatcher } from '../alerts/filter';
import type { AlertDetailState } from '../hooks/useAlertDetail';
import { AlertDetailOverview } from './AlertDetailOverview';
import { AlertRawView } from './AlertRawView';
import { AlertTimeline } from './AlertTimeline';
import { CloseIcon, PulseMark } from './icons';

const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'timeline', label: 'Timeline' },
  { id: 'raw', label: 'Raw' },
] as const;

type DetailTab = (typeof TABS)[number]['id'];

interface AlertDetailDrawerProps {
  alertId: string;
  state: AlertDetailState;
  onClose: () => void;
  onRetry: () => void;
  /** Runs an acknowledge toggle; forwarded to the overview's gated action. */
  onAcknowledge?: (acknowledged: boolean) => Promise<void>;
  /** Applies a label matcher to the console filter; enables the label buttons. */
  onFilterLabel?: (matcher: LabelMatcher) => void;
  /** Opens the silence dialog for this alert; forwarded to the gated action. */
  onSilence?: () => void;
}

/**
 * Read-only alert detail surface: a non-modal right-side drawer on desktop
 * and a full-screen sheet on mobile (purely a CSS distinction — the semantics
 * stay identical). Focus moves into the dialog on open and returns to the
 * previously focused element (usually the selected row) on close; Escape and
 * the close button both dismiss it. The component is remounted per alert id
 * (the App keys it by id), so tab/scroll state never leaks between alerts.
 */
export function AlertDetailDrawer({
  alertId,
  state,
  onClose,
  onRetry,
  onAcknowledge,
  onFilterLabel,
  onSilence,
}: AlertDetailDrawerProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const restoreFocusRef = useRef<HTMLElement | null>(null);
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const [activeTab, setActiveTab] = useState<DetailTab>('overview');
  const baseId = useId();
  const titleId = `${baseId}-title`;
  const tabId = (tab: DetailTab) => `${baseId}-tab-${tab}`;
  const panelId = (tab: DetailTab) => `${baseId}-panel-${tab}`;

  // Move focus into the dialog on open; restore it on close.
  useEffect(() => {
    const previous = document.activeElement;
    restoreFocusRef.current = previous instanceof HTMLElement ? previous : null;
    panelRef.current?.focus();
    return () => {
      const target = restoreFocusRef.current;
      if (target !== null && document.contains(target)) {
        target.focus();
      }
    };
  }, []);

  // Escape dismisses the drawer. Handlers that already consumed the key
  // (e.g. the filter input clearing itself) win via defaultPrevented.
  useEffect(() => {
    const handleKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented) {
        event.preventDefault();
        onClose();
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  // ARIA tab pattern: arrow keys move selection and focus together.
  const handleTabsKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const current = TABS.findIndex((tab) => tab.id === activeTab);
    let next = current;
    if (event.key === 'ArrowRight') {
      next = (current + 1) % TABS.length;
    } else if (event.key === 'ArrowLeft') {
      next = (current - 1 + TABS.length) % TABS.length;
    } else if (event.key === 'Home') {
      next = 0;
    } else if (event.key === 'End') {
      next = TABS.length - 1;
    } else {
      return;
    }
    event.preventDefault();
    const tab = TABS[next];
    if (tab === undefined) {
      return;
    }
    setActiveTab(tab.id);
    tabRefs.current[next]?.focus();
  };

  const ready = state.status === 'ready' ? state.detail : null;
  const title = ready !== null ? ready.alert.name : 'Alert detail';
  const subtitle = ready !== null ? ready.alert.source : alertId;

  return (
    <div
      ref={panelRef}
      className="detail-drawer"
      role="dialog"
      aria-modal="false"
      aria-labelledby={titleId}
      tabIndex={-1}
    >
      <div className="detail-head">
        <div className="detail-head-text">
          <h2 className="detail-title" id={titleId}>
            {title}
          </h2>
          <p className="detail-subtitle">{subtitle}</p>
        </div>
        <button
          type="button"
          className="detail-close"
          onClick={onClose}
          aria-label="Close alert detail"
        >
          <CloseIcon className="detail-close-icon" />
        </button>
      </div>
      {ready !== null ? (
        <>
          <div
            className="detail-tabs"
            role="tablist"
            aria-label="Alert detail sections"
            onKeyDown={handleTabsKeyDown}
          >
            {TABS.map((tab, index) => (
              <button
                key={tab.id}
                ref={(element) => {
                  tabRefs.current[index] = element;
                }}
                type="button"
                role="tab"
                id={tabId(tab.id)}
                aria-selected={activeTab === tab.id}
                aria-controls={panelId(tab.id)}
                tabIndex={activeTab === tab.id ? 0 : -1}
                className="detail-tab"
                onClick={() => setActiveTab(tab.id)}
              >
                {tab.label}
              </button>
            ))}
          </div>
          <div className="detail-body">
            <div
              role="tabpanel"
              id={panelId(activeTab)}
              aria-labelledby={tabId(activeTab)}
              tabIndex={0}
              className="detail-panel"
            >
              {activeTab === 'overview' ? (
                <AlertDetailOverview
                  detail={ready.alert}
                  silences={ready.silences}
                  onAcknowledge={onAcknowledge}
                  onFilterLabel={onFilterLabel}
                  onSilence={onSilence}
                />
              ) : null}
              {activeTab === 'timeline' ? <AlertTimeline history={ready.history} /> : null}
              {activeTab === 'raw' ? <AlertRawView rawData={ready.alert.rawData} /> : null}
            </div>
          </div>
        </>
      ) : (
        <div className="detail-body">
          {state.status === 'loading' || state.status === 'idle' ? (
            <div className="detail-state" role="status">
              <PulseMark className="detail-state-mark detail-state-pulse" />
              <p className="detail-state-copy">Loading alert detail…</p>
            </div>
          ) : state.status === 'not-found' ? (
            <div className="detail-state" role="alert">
              <h3 className="detail-state-title">Alert not found</h3>
              <p className="detail-state-copy">
                Alert <span className="detail-mono">{alertId}</span> is not available. It may have
                been resolved and pruned, or the link is stale.
              </p>
              <button type="button" className="button" onClick={onRetry}>
                Retry detail request
              </button>
            </div>
          ) : state.status === 'error' ? (
            <div className="detail-state" role="alert">
              <h3 className="detail-state-title detail-state-error">Cannot load alert detail</h3>
              <p className="detail-state-copy">{state.error.message}</p>
              <button type="button" className="button" onClick={onRetry}>
                Retry detail request
              </button>
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
