import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwind from '@tailwindcss/vite';

const backend = 'http://127.0.0.1:8080';

export default defineConfig({
  root: 'web',
  plugins: [react(), tailwind()],
  build: {
    outDir: '../internal/webassets/dist', emptyOutDir: true, sourcemap: false,
    target: 'es2022', assetsInlineLimit: 0,
    rolldownOptions: { output: { manualChunks(id) {
      if (id.includes('/node_modules/react/') || id.includes('/node_modules/react-dom/') || id.includes('/node_modules/scheduler/')) return 'react';
      if (id.includes('/node_modules/@tanstack/')) return 'query';
      if (id.includes('/node_modules/@radix-ui/') || id.includes('/node_modules/@floating-ui/')) return 'primitives';
    } } },
  },
  // The backend accepts exactly one browser origin; the dev proxy presents the backend's own.
  server: { host: '127.0.0.1', port: 18162, strictPort: true, proxy: Object.fromEntries(['/api', '/docs', '/healthz', '/readyz'].map((path) => [path, {
    target: backend, configure: (proxy: { on(event: 'proxyReq', fn: (request: { getHeader(n: string): unknown; setHeader(n: string, v: string): void }) => void): void }) => {
      proxy.on('proxyReq', (request) => { if (request.getHeader('origin')) request.setHeader('origin', backend); });
    },
  }])) },
});
