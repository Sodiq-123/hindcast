import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The Go backend serves the API on :8080; in dev, Vite proxies /api to it.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { '/api': { target: 'http://localhost:8080', changeOrigin: true, timeout: 600000, proxyTimeout: 600000 } },
  },
})
