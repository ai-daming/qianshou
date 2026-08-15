import { defineConfig } from 'vite';

export default defineConfig({
  server: {
    host: '127.0.0.1',
    port: 41728,
    proxy: {
      '/api': 'http://127.0.0.1:41727',
      '/health': 'http://127.0.0.1:41727'
    }
  },
  build: {
    target: 'es2023',
    outDir: 'dist',
    emptyOutDir: true
  }
});
