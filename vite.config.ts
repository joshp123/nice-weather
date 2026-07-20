import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { resolve } from 'node:path';

const buildCanary = process.env.NICE_WEATHER_CANARY_BUILD === '1';

export default defineConfig({
  plugins: [react()],
  build: {
    emptyOutDir: !buildCanary,
    rollupOptions: {
      input: buildCanary ? { canary: resolve(__dirname, 'canary/index.html') } : { app: resolve(__dirname, 'index.html') },
    },
  },
  test: {
    environment: 'node',
  },
});
