import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { createReadStream, existsSync } from 'node:fs';
import { relative, resolve } from 'node:path';

export default defineConfig({
  plugins: [
    react(),
    {
      name: 'artifact-tile-server',
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          const url = req.url?.split('?')[0] ?? '';
          if (!url.startsWith('/artifacts/')) {
            next();
            return;
          }
          const artifactRoot = resolve(server.config.root, 'artifacts');
          const path = resolve(server.config.root, decodeURIComponent(url.slice(1)));
          const relativePath = relative(artifactRoot, path);
          if (relativePath.startsWith('..')) {
            res.statusCode = 404;
            res.end('missing artifact');
            return;
          }
          if (existsSync(path)) {
            res.statusCode = 200;
            if (url.endsWith('.png')) {
              res.setHeader('Content-Type', 'image/png');
            } else if (url.endsWith('.json')) {
              res.setHeader('Content-Type', 'application/json');
            }
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
    environment: 'node',
  },
});
