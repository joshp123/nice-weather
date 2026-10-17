import { execSync } from 'node:child_process';
import { defineConfig } from 'vite';
import { scoringExplainerData } from './research/site/scoring-explainer-data.mjs';

// The footer shows the commit the live site was built from, so anyone can compare it with the code.
const commit = execSync('git rev-parse --short HEAD').toString().trim();
const committedAt = execSync('git log -1 --format=%cs').toString().trim();

const explanation = scoringExplainerData();

export default defineConfig({
  plugins: [{
    name: 'published-climate-explanation',
    transformIndexHtml(html) {
      return html.replace('__YEARS__', explanation.spans.join('; '))
        .replace('__YEAR_COUNT__', `${explanation.days.toLocaleString('en-GB')} days across ${explanation.spanCount} one-year spans`);
    },
  }],
  define: { __BUILD_COMMIT__: JSON.stringify(commit), __BUILD_DATE__: JSON.stringify(committedAt), __CURVE_REFERENCE_POINTS__: JSON.stringify(explanation.references) },
  build: { target: 'es2022', rollupOptions: { input: { main: 'index.html', method: 'method.html' } } },
});
