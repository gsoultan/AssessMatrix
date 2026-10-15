/**
 * The public verify page: no framework, no auth, a few KB. Served at /verify/
 * by the core, from the studio's build output unless --outDir says otherwise.
 */
import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  base: '/verify/',
  // In development (make up), the credential lookups go to a local core.
  server: {
    proxy: {
      '/credentials/': process.env.CORE_URL ?? 'http://localhost:8080',
      '/issuers/': process.env.CORE_URL ?? 'http://localhost:8080',
    },
  },
  build: {
    outDir: fileURLToPath(new URL('../studio/dist/verify', import.meta.url)),
    emptyOutDir: true,
  },
})
