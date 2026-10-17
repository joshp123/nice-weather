// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import { defineConfig } from 'vite';
export default defineConfig({
  root: 'src/engine', base: '/engine-debug/',
  build: { target: 'es2022', outDir: '../../dist/engine-debug', emptyOutDir: false, rollupOptions: { input: 'src/engine/debug.html' } },
});
