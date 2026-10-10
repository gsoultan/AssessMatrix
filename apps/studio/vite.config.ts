import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// With VITE_BACKEND=core, the dev server forwards API calls to a core running
// locally (AM_PUBLIC_URL=http://localhost:5180), so the browser sees one origin
// and the session cookies stay first-party.
const core = process.env.CORE_URL ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5180,
    proxy: {
      '/assessmatrix.v1.': core,
      '/dev/mailbox': core,
    },
  },
})
