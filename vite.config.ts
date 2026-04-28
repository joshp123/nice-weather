import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { createReadStream, existsSync } from 'node:fs';
import { join } from 'node:path';

export default defineConfig({
  plugins: [
    react(),
    {
      name: 'artifact-tile-server',
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          const url = req.url?.split('?')[0] ?? '';
          if (!url.startsWith('/artifacts/tiles/') || !url.endsWith('.png')) {
            next();
            return;
          }
          const path = join(server.config.publicDir, decodeURIComponent(url));
          if (existsSync(path)) {
            res.statusCode = 200;
            res.setHeader('Content-Type', 'image/png');
            createReadStream(path).pipe(res);
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
