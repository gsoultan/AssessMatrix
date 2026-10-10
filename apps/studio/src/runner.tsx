/**
 * The candidate runner: the assessment and nothing else.
 *
 * Its own entry, so a candidate's browser never downloads the studio, and its
 * own path (/take/), which a deployment can put on its own origin
 * (AM_RUNNER_URL) so that candidate and staff sessions never share cookies.
 */
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider } from '@mantine/core'

import './styles.css'

import { theme } from './theme'
import { CandidateApp } from './screens/CandidateApp'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="light">
      <main className="flex min-h-full flex-col">
        <CandidateApp standalone />
      </main>
    </MantineProvider>
  </StrictMode>,
)

// The shell stays on the device, so a reload with no network still opens the
// exam and its queued answers. Only in a built runner: the dev server has no sw.js.
if (import.meta.env.PROD && 'serviceWorker' in navigator)
  navigator.serviceWorker.register('/take/sw.js', { scope: '/take/' }).catch((e) => console.warn('service worker:', e))
