import { defineConfig } from 'vite';
import { scoringExplainerData } from './research/site/scoring-explainer-data.mjs';
import { maplibreParseStatePatch } from './research/fine/maplibre-parse-state.mjs';
import { AWFUL_MAXIMUM } from './hosting/sites/score-bands.mjs';

const explanation = scoringExplainerData();

export default defineConfig({
  plugins: [maplibreParseStatePatch(), {
    name: 'published-climate-explanation',
    transformIndexHtml(html) {
      return html.replaceAll('__AWFUL_MAXIMUM__', String(AWFUL_MAXIMUM))
        .replace('__YEARS__', explanation.spans.join('; '))
        .replace('__YEAR_COUNT__', `${explanation.days.toLocaleString('en-GB')} days across ${explanation.spanCount} one-year spans`);
    },
  }],
  define: { __CURVE_REFERENCE_POINTS__: JSON.stringify(explanation.references) },
  build: { target: 'es2022', rollupOptions: { input: { main: 'index.html', method: 'method.html' } } },
});
