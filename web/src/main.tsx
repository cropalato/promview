import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { setApiBaseUrl } from './config/apiBase';
import './styles/global.css';

/**
 * A shell hosting this console injects the server it was configured for before
 * any application script runs, because a local webview has no origin to be
 * relative to. The browser build sets nothing and keeps its relative paths.
 *
 * A bad value is reported rather than swallowed: booting against a silently
 * wrong server is worse than not booting, since every screen would then be a
 * connection error with no clue where it was looking.
 */
const injectedBase = (globalThis as { __PROMVIEW_API_BASE__?: unknown }).__PROMVIEW_API_BASE__;
if (typeof injectedBase === 'string' && injectedBase !== '') {
  setApiBaseUrl(injectedBase);
}

const container = document.getElementById('root');
if (!container) {
  throw new Error('Promview boot failed: #root element is missing');
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
