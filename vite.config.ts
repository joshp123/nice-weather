import { execSync } from 'node:child_process';
import { defineConfig } from 'vite';

// The footer shows the commit the live site was built from, so anyone can compare it with the code.
const commit = execSync('git rev-parse --short HEAD').toString().trim();
const committedAt = execSync('git log -1 --format=%cs').toString().trim();

export default defineConfig({
  define: { __BUILD_COMMIT__: JSON.stringify(commit), __BUILD_DATE__: JSON.stringify(committedAt) },
  build: { target: 'es2022', rollupOptions: { input: { main: 'index.html', method: 'method.html' } } },
});
