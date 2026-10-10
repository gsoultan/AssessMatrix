import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// With VITE_BACKEND=core, the dev server forwards API calls to a core running
// locally (AM_PUBLIC_URL=http://localhost:5180), so the browser sees one origin
// and the session cookies stay first-party.
const core = process.env.CORE_URL ?? 'http://localhost:8080'

export const base = {
  plugins: [react(), tailwindcss()],
  build: {
    // The runner is built separately (vite.runner.config.ts), so it shares no
    // chunk with the studio and a candidate never downloads studio code.
    rollupOptions: { input: { studio: 'index.html' } },
  },
  server: {
    port: 5180,
    proxy: {
      '/assessmatrix.v1.': core,
      '/dev/mailbox': core,
    },
  },
}

export default defineConfig(base)
