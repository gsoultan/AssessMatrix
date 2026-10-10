
import { useEffect } from 'react'
import type React from 'react'
import { Drawer, Burger, useMantineColorScheme } from '@mantine/core'
import { useDisclosure, useMediaQuery } from '@mantine/hooks'
import {
  IconPlugConnected,
  IconCode,
  IconEye,
  IconWebhook,
  IconWand,
  IconChecklist,
  IconChartBar,
  IconLibrary,
  IconUsers,
  IconSend,
  IconEdit,
  IconCertificate,
  IconSparkles,
  IconSun,
  IconMoon,
  IconLogout,
  IconX,
} from '@tabler/icons-react'

import { CandidateApp } from './screens/CandidateApp'
import { ExamBuilder } from './screens/ExamBuilder'
import { IntegrationHub } from './screens/IntegrationHub'
import { EmbedStudio } from './screens/EmbedStudio'
import { ProctorMonitor } from './screens/ProctorMonitor'
import { EventInspector } from './screens/EventInspector'
import { AuthorStudio } from './screens/AuthorStudio'
import { EvidenceProfile } from './screens/EvidenceProfile'
import { AiTask } from './screens/AiTask'
import { Responses } from './screens/Responses'
import { Library } from './screens/Library'
import { People } from './screens/People'
import { Assignments } from './screens/Assignments'
import { SignIn } from './screens/SignIn'
import { useApp } from './state'
import { CORE, SHOW_MOCKS } from './backend/mode'

/** Design mock-ups with canned data. Shown in a local build; hidden against the core unless asked for. */
const MOCKS = new Set(['author', 'aitask', 'profile', 'hub', 'embed', 'events', 'monitor'])

const ALL_SCREENS = [
  { key: 'library', group: 'Build', name: 'Library', sub: 'All exams, quizzes, surveys', Icon: IconLibrary, El: Library },
  { key: 'builder', group: 'Build', name: 'Editor', sub: 'Sections · questions · publish', Icon: IconEdit, El: ExamBuilder },
  { key: 'author', group: 'Build', name: 'Item studio', sub: 'Generative items', Icon: IconWand, El: AuthorStudio },
  { key: 'people', group: 'Deliver', name: 'People', sub: 'Roster of candidates', Icon: IconUsers, El: People },
  { key: 'assignments', group: 'Deliver', name: 'Assignments', sub: 'Invite and track', Icon: IconSend, El: Assignments },
  { key: 'player', group: 'Candidate', name: 'Take exam', sub: 'The candidate experience', Icon: IconChecklist, El: CandidateApp },
  { key: 'responses', group: 'Candidate', name: 'Responses', sub: 'Aggregate results', Icon: IconChartBar, El: Responses },
  { key: 'aitask', group: 'Candidate', name: 'AI-in-the-loop task', sub: 'Score the collaboration', Icon: IconSparkles, El: AiTask },
  { key: 'profile', group: 'Candidate', name: 'Evidence profile', sub: 'Skill graph · credentials', Icon: IconCertificate, El: EvidenceProfile },
  { key: 'hub', group: 'Integrate', name: 'Integration hub', sub: 'The four doors', Icon: IconPlugConnected, El: IntegrationHub },
  { key: 'embed', group: 'Integrate', name: 'Embed studio', sub: 'Preview + preflight', Icon: IconCode, El: EmbedStudio },
  { key: 'events', group: 'Integrate', name: 'Event inspector', sub: 'Webhooks · replay', Icon: IconWebhook, El: EventInspector },
  { key: 'monitor', group: 'Operate', name: 'Proctor monitor', sub: 'Live attempts', Icon: IconEye, El: ProctorMonitor },
] as const

type Key = (typeof ALL_SCREENS)[number]['key']
const SCREENS = ALL_SCREENS.filter((s) => SHOW_MOCKS || !MOCKS.has(s.key))

/**
 * Reachable, but not listed. The editor is always entered by opening something
 * from the Library — a standalone nav entry just edits whichever instrument
 * happens to be first, which is a guess the user never asked us to make.
 */
const HIDDEN_FROM_NAV = new Set<Key>(['builder'])
const NAV = SCREENS.filter((s) => !HIDDEN_FROM_NAV.has(s.key))

export function App() {
  const staff = useApp((s) => s.staff)
  const candidateView = useApp((s) => s.candidateView)
  const boot = useApp((s) => s.boot)
  useEffect(() => {
    // A candidate who came by link never needs a staff session checked.
    if (!candidateView) void boot()
  }, [boot, candidateView])

  // Against the core, a candidate gets their assessment and nothing else.
  if (CORE && candidateView)
    return (
      <main className="flex min-h-full flex-col">
        <CandidateApp standalone />
      </main>
    )
  if (staff === undefined)
    return (
      <p role="status" className="p-8 text-center text-[13px] text-ink-muted">
        Loading…
      </p>
    )
  if (staff === null) return <SignIn />
  return <Shell />
}

function Shell() {
  const staff = useApp((st) => st.staff)
  const signOut = useApp((st) => st.signOut)
  const notice = useApp((st) => st.notice)
  const dismissNotice = useApp((st) => st.dismissNotice)
  // Active screen lives in the store so a screen can navigate — "open this in
  // the editor" is a selection plus a navigation, and both belong together.
  const active = useApp((st) => st.screen) as Key
  const setActive = useApp((st) => st.setScreen)
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const dark = colorScheme === 'dark'
  const [opened, { toggle, close }] = useDisclosure(false)
  const isDesktop = useMediaQuery('(min-width: 1024px)')
  const current = SCREENS.find((s) => s.key === active) ?? SCREENS[0]!
  const Screen = current.El as React.ComponentType<{ isDesktop: boolean }>

  // The editor has no nav entry, so keep its parent section lit — otherwise
  // nothing is highlighted and you lose your place.
  const navActive: Key = active === 'builder' ? 'library' : active

  const groups = [...new Set(NAV.map((s) => s.group))]

  const themeToggle = (
    <button
      onClick={() => setColorScheme(dark ? 'light' : 'dark')}
      aria-label={dark ? 'Switch to light mode' : 'Switch to dark mode'}
      className="hair flex items-center gap-1.5 rounded-lg bg-surface px-2.5 py-1.5 text-[11.5px] font-medium text-ink-2"
    >
      {dark ? <IconSun size={14} stroke={1.9} /> : <IconMoon size={14} stroke={1.9} />}
      {dark ? 'Light' : 'Dark'}
    </button>
  )

  const signOutButton = (
    <button
      onClick={() => void signOut()}
      title={staff?.email}
      className="hair flex items-center gap-1.5 rounded-lg bg-surface px-2.5 py-1.5 text-[11.5px] font-medium text-ink-2"
    >
      <IconLogout size={14} stroke={1.9} /> Sign out
    </button>
  )

  const nav = (
    <nav className="flex flex-col gap-5 p-3">
      {groups.map((g) => (
        <div key={g}>
          <p className="px-3 pb-1.5 text-[11px] font-medium uppercase tracking-wider text-ink-muted">{g}</p>
          <div className="flex flex-col gap-0.5">
            {NAV.filter((s) => s.group === g).map((s) => {
              const on = s.key === navActive
              return (
                <button
                  key={s.key}
                  onClick={() => {
                    setActive(s.key)
                    close()
                  }}
                  className={`flex items-start gap-2.5 rounded-lg px-3 py-2 text-left transition-colors ${
                    on ? 'text-ink' : 'text-ink-2 hover:bg-ink/[0.04]'
                  }`}
                  style={on ? { background: 'var(--color-wash)' } : undefined}
                >
                  <s.Icon size={17} stroke={1.7} className="mt-px shrink-0" color={on ? 'var(--color-s1)' : 'var(--color-ink-muted)'} />
                  <span className="min-w-0">
                    <span className={`block truncate text-[13px] ${on ? 'font-semibold' : 'font-medium'}`}>{s.name}</span>
                    <span className="block truncate text-[11.5px] text-ink-muted">{s.sub}</span>
                  </span>
                </button>
              )
            })}
          </div>
        </div>
      ))}
    </nav>
  )

  return (
    <div className="flex min-h-full">
      {/* Desktop rail */}
      <aside className="hidden w-[248px] shrink-0 border-r border-hair bg-surface lg:block">
        <div className="flex items-center gap-2.5 border-b border-hair px-5 py-4">
          <div className="grid h-7 w-7 place-items-center rounded-md" style={{ background: 'var(--color-s1)' }}>
            <span className="text-[13px] font-bold text-white">A</span>
          </div>
          <div>
            <p className="text-[13px] font-semibold leading-none tracking-tight">AssessMatrix</p>
            <p className="mt-1 text-[11px] leading-none text-ink-muted">
              {CORE ? staff?.tenantName : 'prototype · local data'}
            </p>
          </div>
        </div>
        {nav}
        <div className="flex flex-wrap items-center gap-2 px-3 pb-4">
          {themeToggle}
          {CORE && signOutButton}
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* Mobile bar */}
        <header className="sticky top-0 z-30 flex items-center gap-3 border-b border-hair bg-surface px-4 py-3 lg:hidden">
          <Burger opened={opened} onClick={toggle} size="sm" aria-label="Screens" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-[13px] font-semibold leading-none">{current.name}</p>
            <p className="mt-1 truncate text-[11px] leading-none text-ink-muted">{current.sub}</p>
          </div>
          {themeToggle}
        </header>

        <Drawer opened={opened} onClose={close} size="260px" title="AssessMatrix" padding={0} hiddenFrom="lg">
          {nav}
          {CORE && <div className="px-3 pb-4">{signOutButton}</div>}
        </Drawer>

        {notice && (
          <div role="alert" className="flex items-start gap-2 border-b border-hair px-4 py-2.5 sm:px-6" style={{ background: 'var(--color-wash-warn)' }}>
            <p className="min-w-0 flex-1 text-[12.5px] leading-snug text-ink">{notice}</p>
            <button onClick={dismissNotice} aria-label="Dismiss" className="shrink-0 text-ink-muted">
              <IconX size={15} stroke={2} />
            </button>
          </div>
        )}

        <main className="min-w-0 flex-1">
          <Screen isDesktop={!!isDesktop} />
        </main>
      </div>
    </div>
  )
}
