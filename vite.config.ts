import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { existsSync } from 'node:fs';
import { join } from 'node:path';

export default defineConfig({
  plugins: [
    react(),
    {
      name: 'artifact-tile-404',
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          const url = req.url?.split('?')[0] ?? '';
          if (!url.startsWith('/artifacts/tiles/') || !url.endsWith('.png')) {
            next();
            return;
          }
          const path = join(server.config.publicDir, decodeURIComponent(url));
          if (existsSync(path)) {
            next();
            return;
          }
          res.statusCode = 404;
          res.end('missing tile');
        });
      },
    },
  ],
  test: {
    environment: 'jsdom',
  },
});
