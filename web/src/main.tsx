import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { connectHost } from './config/hostBridge';
import './styles/global.css';

// A host shell points the console at its configured server and hands it a
// transport; a browser has neither and keeps its own. Runs before the first
// render so the very first request already goes the right way.
connectHost();

const container = document.getElementById('root');
if (!container) {
  throw new Error('Promview boot failed: #root element is missing');
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
