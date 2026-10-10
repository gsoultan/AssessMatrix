/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** `core` talks to the Go core over Connect; anything else keeps data in this browser. */
  readonly VITE_BACKEND?: string
  /** `1` shows the design mock-ups alongside the working screens when the core is the backend. */
  readonly VITE_MOCKS?: string
}
