import { createRoot } from 'react-dom/client';
import App from './App';
import { installBrowserTelemetry } from './telemetry';

installBrowserTelemetry();
createRoot(document.getElementById('root')!).render(<App />);
