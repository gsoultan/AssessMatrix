import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Collapse } from '@mantine/core'
import {
  IconArrowRight,
  IconArrowLeft,
  IconCheck,
  IconX,
  IconFlag,
  IconLock,
  IconWifi,
  IconWifiOff,
  IconCamera,
  IconDatabase,
  IconAlertTriangle,
  IconMail,
  IconMailCheck,
  IconLinkOff,
  IconShieldLock,
  IconShieldCheck,
  IconMoodSmile,
  IconListCheck,
  IconEye,
  IconChevronDown,
  IconCertificate,
  IconRotate,
  IconPlayerPlay,
} from '@tabler/icons-react'
import { ItemView, mmss } from '../components/ItemView'
import { ItemImage } from '../components/ItemImage'
import { Meter } from '../components/Viz'
import type { Media } from '../media'
import { SESSION_HASH, useApp, useResponses } from '../state'
import { type Refusal, OTP_TRIES, listOutbox } from '../invite'
import { type Seat, gate as door } from '../backend/gate'
import { CORE } from '../backend/mode'
import { claim } from '../backend/onetab'
import { enterFullscreen, viewer, watch } from '../backend/signals'
import type { Camera, VisionState } from '../backend/vision'
import { PURPOSES, requiredConsents } from '../integrity'
import {
  type Assignment,
  type Exam,
  type Item,
  allItems,
  isScored,
  isLive,
  resolveExam,
  isSurvey,
  score,
  totalMinutes,
  totalPoints,
  getAttempt,
} from '../store'

type Stage = 'pick' | 'gate' | 'invite' | 'consent' | 'ready' | 'attempt' | 'review' | 'done'

/**
 * Where someone holding a link stands before they are let in. The token lives
 * here, in memory, for exactly as long as the code step takes.
 *
 * `stub` is the assignment id, set only in a local build so the prototype's
 * stand-in inbox can show the right code. Against the core a client learns
 * nothing but the masked address until the code is verified.
 */
type Gate =
  | { at: 'checking' }
  | { at: 'send'; token: string; email: string; stub?: string }
  | { at: 'code'; token: string; email: string; stub?: string; retryAt: number }
  | { at: 'refused'; why: Refusal | 'session' | 'offline' | 'elsewhere'; token?: string; email?: string; resent?: boolean }

/** Why a link did not open, said to the person holding it, with what to do next. */
const REFUSED: Record<Refusal | 'session' | 'offline' | 'elsewhere', { title: string; body: string }> = {
  elsewhere: {
    title: 'Sedang dibuka di tab lain',
    body: 'Asesmen ini sudah terbuka di tab atau jendela lain pada peramban ini. Lanjutkan di sana, lalu tutup tab ini. Jawaban Anda tidak hilang.',
  },
  offline: {
    title: 'Tidak dapat terhubung',
    body: 'Periksa koneksi internet Anda, lalu buka kembali tautan dari email. Jawaban yang sudah tersimpan tidak hilang.',
  },
  unknown: {
    title: 'Tautan ini tidak dikenali',
    body: 'Periksa kembali email undangan Anda dan buka tautan langsung dari sana, tanpa menyalin sebagian.',
  },
  revoked: {
    title: 'Undangan ini telah ditarik',
    body: 'Penyelenggara membatalkan undangan ini. Hubungi mereka bila menurut Anda ini keliru.',
  },
  expired: {
    title: 'Tautan ini sudah kedaluwarsa',
    body: 'Batas waktunya sudah lewat. Hanya penyelenggara yang dapat mengirim undangan baru.',
  },
  locked: {
    title: 'Tautan ini dikunci',
    body: 'Terlalu banyak kode diminta atau salah dimasukkan. Demi keamanan Anda, penyelenggara perlu mengirim undangan baru.',
  },
  used: {
    title: 'Tautan ini sudah dipakai',
    body: 'Setiap tautan hanya berlaku sekali. Bila Anda berganti perangkat atau peramban tertutup, minta tautan baru — jawaban yang tersimpan di perangkat ini tidak hilang.',
  },
  superseded: {
    title: 'Tautan ini sudah diganti',
    body: 'Ada tautan yang lebih baru untuk undangan ini. Gunakan email terakhir, atau minta dikirim lagi.',
  },
  submitted: {
    title: 'Anda sudah mengumpulkan',
    body: 'Pengerjaan untuk undangan ini telah selesai dan tidak dapat dibuka kembali.',
  },
  closed: {
    title: 'Asesmen ini belum dibuka',
    body: 'Penyelenggara belum membukanya, atau sudah menutupnya. Coba lagi nanti atau hubungi mereka.',
  },
  session: {
    title: 'Sesi Anda berakhir',
    body: 'Undangan ini ditarik atau dibuka di tempat lain. Jawaban Anda tetap tersimpan di perangkat ini. Hubungi penyelenggara untuk melanjutkan.',
  },
}

/* ── readiness ───────────────────────────────────────────────────────────── */

type Check = { Icon: typeof IconWifi; label: string; v: string; ok: boolean }

/**
 * What this device can actually do, measured rather than assumed: free
 * storage for the offline copy, the network as the browser reports it, and
 * for tier 2 whether the camera opens. Only the camera can stop a start, and
 * even then the candidate is told what will happen rather than turned away.
 */
async function readiness(tier: number): Promise<Check[]> {
  const out: Check[] = []
  if (tier >= 2) {
    const cam = CORE ? await import('../backend/vision').then((v) => v.cameraReady()).catch(() => 'none' as const) : 'ok'
    out.push({
      Icon: IconCamera,
      label: 'Kamera depan',
      v:
        cam === 'ok'
          ? 'Dapat dibuka. Diperiksa di perangkat ini saja.'
          : cam === 'denied'
            ? 'Izin kamera ditolak. Ujian tetap bisa dimulai, tetapi ketiadaan kamera dicatat dan ditinjau.'
            : 'Tidak ada kamera. Ujian tetap bisa dimulai, tetapi ketiadaan kamera dicatat dan ditinjau.',
      ok: cam === 'ok',
    })
  }
  try {
    const est = await navigator.storage?.estimate?.()
    const free = est?.quota != null ? Math.floor((est.quota - (est.usage ?? 0)) / 1e6) : null
    out.push({
      Icon: IconDatabase,
      label: 'Penyimpanan luring',
      v: free == null ? 'Tidak dapat diperiksa' : free >= 50 ? `${free.toLocaleString('id-ID')} MB tersedia` : `Hanya ${free} MB tersisa`,
      ok: free == null || free >= 50,
    })
  } catch {
    out.push({ Icon: IconDatabase, label: 'Penyimpanan luring', v: 'Tidak dapat diperiksa', ok: true })
  }
  const link = (navigator as Navigator & { connection?: { downlink?: number } }).connection
  const mbps = link?.downlink
  out.push(
    !navigator.onLine
      ? { Icon: IconWifiOff, label: 'Jaringan', v: 'Luring', ok: false }
      : mbps != null && mbps < 1.5
        ? { Icon: IconWifi, label: 'Jaringan', v: `Lambat — ${mbps.toLocaleString('id-ID')} Mbps`, ok: false }
        : { Icon: IconWifi, label: 'Jaringan', v: mbps != null ? `${mbps.toLocaleString('id-ID')} Mbps` : 'Tersambung', ok: true },
  )
  return out
}

/* ── small pieces ─────────────────────────────────────────────────────────── */

function Primary({
  children,
  onClick,
  tone = 'brand',
  disabled,
}: {
  children: React.ReactNode
  onClick: () => void
  tone?: 'brand' | 'good'
  disabled?: boolean
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="flex h-12 w-full items-center justify-center gap-2 rounded-xl text-[15px] font-semibold text-on-brand disabled:opacity-40"
      style={{ background: tone === 'good' ? 'var(--color-good-fill)' : 'var(--color-s1)' }}
    >
      {children}
    </button>
  )
}

function Screen({ children, wide = false }: { children: React.ReactNode; wide?: boolean }) {
  return (
    <div className="scroll-thin min-h-0 flex-1 overflow-y-auto">
      <div className={`mx-auto w-full px-5 py-7 sm:px-6 ${wide ? 'max-w-[760px]' : 'max-w-[560px]'}`}>{children}</div>
    </div>
  )
}

const Title = ({ children }: { children: React.ReactNode }) => (
  <h1 className="text-[22px] font-semibold leading-tight tracking-tight text-ink">{children}</h1>
)

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3 px-4 py-3">
      <dt className="text-[13.5px] text-ink-muted">{k}</dt>
      <dd className="tnum text-[14px] font-medium text-ink">{v}</dd>
    </div>
  )
}

/** What the device check found. Compact on the start page, one card per check on its own step. */
function DeviceChecks({ checks, compact = false }: { checks: Check[] | null; compact?: boolean }) {
  const rows = checks ?? [{ Icon: IconDatabase, label: 'Memeriksa perangkat…', v: '', ok: true }]
  if (compact)
    return (
      <div className="hair mt-4 divide-y divide-hair rounded-xl bg-surface">
        {rows.map((r) => (
          <div key={r.label} className="flex items-center gap-2.5 px-4 py-2.5">
            <r.Icon size={16} stroke={1.7} className="shrink-0 text-ink-muted" />
            <p className="min-w-0 flex-1 truncate text-[13px] text-ink">
              {r.label}
              {r.v && <span className="text-ink-muted"> · {r.v}</span>}
            </p>
            {r.ok ? (
              <IconCheck size={15} stroke={2.4} aria-label="baik" style={{ color: 'var(--color-good)' }} />
            ) : (
              <IconAlertTriangle size={15} stroke={2} aria-label="perlu perhatian" style={{ color: 'var(--color-serious)' }} />
            )}
          </div>
        ))}
      </div>
    )
  return (
    <div className="mt-5 flex flex-col gap-2">
      {rows.map((r) => (
        <div key={r.label} className="hair flex items-center gap-3 rounded-xl bg-surface px-4 py-3.5">
          <r.Icon size={18} stroke={1.7} className="shrink-0 text-ink-muted" />
          <div className="min-w-0 flex-1">
            <p className="text-[14px] font-medium text-ink">{r.label}</p>
            <p className="text-[12.5px] text-ink-muted">{r.v}</p>
          </div>
          {r.ok ? (
            <IconCheck size={17} stroke={2.4} style={{ color: 'var(--color-good)' }} />
          ) : (
            <IconAlertTriangle size={17} stroke={2} style={{ color: 'var(--color-serious)' }} />
          )}
        </div>
      ))}
    </div>
  )
}

/**
 * The studio's way into the candidate app: pick an instrument and see it the
 * way a candidate does, start page first. Staff-facing, so it is in English
 * and sits inside the studio; the preview itself is the real candidate app.
 */
function PreviewPicker({ exams, onOpen }: { exams: Exam[]; onOpen: (e: Exam) => void }) {
  const shown = exams.filter((e) => e.status !== 'archived')
  const steps = [
    ['Invite', 'From Assignments, each person gets their own link by email.'],
    ['Confirm', 'Opening the link sends a 6-digit code to that address.'],
    ['Start', 'The code opens the start page. One button begins the test.'],
  ] as const
  return (
    <div className="mx-auto max-w-[760px] p-4 sm:p-6">
      <h1 className="text-[15px] font-semibold tracking-tight">Candidate preview</h1>
      <p className="mt-1 max-w-[560px] text-[13px] leading-snug text-ink-muted">
        Open any exam the way a candidate sees it, from the start page to the result. Nothing is emailed and no
        candidate is involved.
      </p>

      <ol className="mt-5 grid gap-2 sm:grid-cols-3">
        {steps.map(([t, d], i) => (
          <li key={t} className="hair rounded-xl bg-surface p-3.5">
            <p className="text-[12.5px] font-semibold text-ink">
              <span className="tnum text-ink-muted">{i + 1}.</span> {t}
            </p>
            <p className="mt-1 text-[12px] leading-snug text-ink-2">{d}</p>
          </li>
        ))}
      </ol>

      <div className="hair mt-5 divide-y divide-hair rounded-xl bg-surface">
        {shown.length === 0 && <p className="px-4 py-8 text-center text-[13px] text-ink-muted">Nothing to preview yet. Create an exam in the Library.</p>}
        {shown.map((e) => (
          <div key={e.id} className="flex items-center gap-3 px-4 py-3">
            <div className="min-w-0 flex-1">
              <p className="truncate text-[13.5px] font-medium text-ink">{e.title}</p>
              <p className="tnum truncate text-[12px] text-ink-muted">
                {isLive(e) ? 'Published · candidates can open it by invitation' : 'Draft · candidates cannot open it yet'} ·{' '}
                {allItems(e).length} questions
              </p>
            </div>
            <button
              onClick={() => onOpen(e)}
              aria-label={`Preview ${e.title}`}
              className="inline-flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-[12.5px] font-semibold text-on-brand"
              style={{ background: 'var(--color-s1)' }}
            >
              <IconEye size={14} stroke={2} /> Preview
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

/* ── the candidate application ────────────────────────────────────────────── */

export function CandidateApp({ standalone = false }: { standalone?: boolean }) {
  /* Shared, persisted state comes from the app store; only view state is local. */
  const exams = useApp((s) => s.exams)
  const attempt = useApp((s) => s.attempt)
  const beginAttempt = useApp((s) => s.beginAttempt)
  const resumeAttempt = useApp((s) => s.resumeAttempt)
  const discardAttempt = useApp((s) => s.discardAttempt)
  const setChoice = useApp((s) => s.setChoice)
  const setTextAnswer = useApp((s) => s.setText)
  const setFile = useApp((s) => s.setFile)
  const toggleFlagIn = useApp((s) => s.toggleFlag)
  const tickDown = useApp((s) => s.tick)
  const finalizeAttempt = useApp((s) => s.finalizeAttempt)

  const takePreview = useApp((s) => s.takePreview)
  const previewWanted = useApp((s) => s.previewExamId)
  const takeInviteToken = useApp((s) => s.takeInviteToken)
  const inviteToken = useApp((s) => s.inviteToken)
  const syncDelivery = useApp((s) => s.syncDelivery)
  const takeSeat = useApp((s) => s.admit)
  const seat = useApp((s) => s.seat)
  const sessionLost = useApp((s) => s.sessionLost)
  const offline = useApp((s) => s.offline)
  const setScreen = useApp((s) => s.setScreen)
  const [assignment, setAssignment] = useState<Assignment | null>(null)
  const [gate, setGate] = useState<Gate>({ at: 'checking' })
  const [code, setCode] = useState('')
  const [codeNote, setCodeNote] = useState('')
  const [now, setNow] = useState(() => Date.now())
  /** Whether this tab holds an invitation's session. Checked with the server before start and submit. */
  const session = useRef(false)
  /** One submit at a time; the clock can ask again on every tick. */
  const finalizing = useRef(false)
  const [submitNote, setSubmitNote] = useState('')
  /** The staff roster is not the candidate's to read; in a local build it is all one browser. */
  const syncLocal = useCallback(() => {
    if (!CORE) void syncDelivery()
  }, [syncDelivery])
  const [exam, setExam] = useState<Exam | null>(null)
  // A candidate cannot read other people's responses; the count is a local-build nicety.
  const responses = useResponses(CORE ? '' : (exam?.id ?? ''))
  const [stage, setStage] = useState<Stage>('pick')
  const [idx, setIdx] = useState(0)
  const [blocked, setBlocked] = useState<string[]>([])
  const [confirming, setConfirming] = useState(false)
  const [dismissedWarn, setDismissedWarn] = useState(0)
  const [showLog, setShowLog] = useState(false)
  const timer = useRef<number | null>(null)
  /** What the readiness screen found on this device. */
  const [checks, setChecks] = useState<Check[] | null>(null)
  const [declined, setDeclined] = useState(false)
  const [camera, setCamera] = useState<VisionState | null>(null)
  const signal = useApp((s) => s.signal)

  /** Past the gate: this tab now acts for one assignment and nothing else. */
  const admit = useCallback(
    async (s: Seat) => {
      const as = s.assignment
      // A sitting that has been handed in is over; its session opens nothing.
      if (as.status === 'submitted') {
        void door.end()
        return setGate({ at: 'refused', why: 'submitted' })
      }
      // One sitting, one tab. The other tab keeps it; this one says so.
      if (CORE && !(await claim(as.id))) {
        setStage('gate')
        return setGate({ at: 'refused', why: 'elsewhere' })
      }
      session.current = true
      takeSeat(s)
      syncLocal()
      setAssignment(as)
      setExam(s.exam)
      // Only this assignment's saved work — never whoever sat here before.
      resumeAttempt(s.exam, as.id)
      setIdx(0)
      setStage('invite')
      // The address bar says "in a session", not how it was reached.
      history.replaceState(null, '', location.pathname + location.search + SESSION_HASH)
    },
    [resumeAttempt, syncLocal, takeSeat],
  )

  // An invitation link is the real way in. It identifies the person and the
  // instrument, but holding it proves nothing yet — that is what the code is for.
  useEffect(() => {
    const token = takeInviteToken()
    if (!token) return
    setStage('gate')
    setGate({ at: 'checking' })
    setCode('')
    setCodeNote('')
    void door.openLink(token).then(
      (r) => {
        syncLocal()
        setGate(r.ok ? { at: 'send', token, email: r.email, stub: r.stub } : { at: 'refused', why: r.why, token, email: r.email })
      },
      () => setGate({ at: 'refused', why: 'offline', token }),
    )
  }, [inviteToken, takeInviteToken, syncLocal])

  // Reloading mid-exam, or reopening a killed browser, comes back through the
  // session the link started rather than through the link again.
  useEffect(() => {
    if (location.hash !== SESSION_HASH || session.current) return
    let stale = false
    setStage('gate')
    void door.resume().then(
      (r) => {
        if (stale) return
        if (r) admit(r)
        else setGate({ at: 'refused', why: 'session' })
      },
      () => !stale && setGate({ at: 'refused', why: 'offline' }),
    )
    return () => {
      stale = true
    }
  }, [admit])

  // The resend countdown is the only thing on the gate that moves.
  useEffect(() => {
    if (stage !== 'gate' || gate.at !== 'code') return
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(t)
  }, [stage, gate.at])

  const sendCode = async (token: string, email: string, stub?: string) => {
    const r = await door.requestCode(token).catch(() => null)
    if (!r) return setGate({ at: 'refused', why: 'offline', token, email })
    syncLocal()
    setNow(Date.now())
    if (r.ok) {
      setCode('')
      setCodeNote('')
      setGate({ at: 'code', token, email, stub, retryAt: r.retryAt })
    } else if (r.why === 'cooldown')
      setGate({ at: 'code', token, email, stub, retryAt: r.retryAt ?? Date.now() })
    else setGate({ at: 'refused', why: r.why, token, email })
  }

  const submitCode = async (token: string) => {
    const r = await door.verifyCode(token, code).catch(() => null)
    if (!r) return setCodeNote('Tidak dapat terhubung. Periksa koneksi Anda lalu coba lagi.')
    if (r.ok) {
      const s = await door.resume().catch(() => null)
      return s ? admit(s) : setGate({ at: 'refused', why: 'session' })
    }
    syncLocal()
    setCode('')
    if (r.why === 'wrong')
      setCodeNote(`Kode tidak cocok. Sisa ${r.triesLeft} dari ${OTP_TRIES} percobaan untuk kode ini.`)
    else if (r.why === 'stale') setCodeNote('Kode ini sudah tidak berlaku. Minta kode baru.')
    else setGate({ at: 'refused', why: r.why, token })
  }

  const askNewLink = async (token: string, why: Refusal | 'session' | 'offline' | 'elsewhere', email?: string) => {
    const r = await door.requestNewLink(token).catch(() => null)
    if (!r) return setGate({ at: 'refused', why: 'offline', token, email })
    syncLocal()
    if (r.ok || r.why === 'cooldown') setGate({ at: 'refused', why, token, email, resent: true })
    else setGate({ at: 'refused', why: r.why === 'limit' ? 'locked' : r.why, email })
  }

  // Arriving from the Library's "preview as candidate" skips the picker, which
  // a real participant would never see anyway.
  useEffect(() => {
    const id = takePreview()
    if (!id) return
    const target = exams.find((e) => e.id === id)
    if (target) {
      setAssignment(null)
      setExam(target)
      resumeAttempt(target)
      setIdx(0)
      setStage('invite')
    }
  }, [takePreview, exams, resumeAttempt])

  // What the participant sits is the version their attempt was pinned to, not
  // whatever the editor has since changed.
  const sitting = useMemo(
    () => (exam ? resolveExam(exam, attempt?.examVersion ?? assignment?.examVersion) : null),
    [exam, attempt?.examVersion, assignment?.examVersion],
  )
  const items = useMemo(() => (sitting ? allItems(sitting) : []), [sitting])
  const item = items[idx]
  const running = !!attempt && !attempt.submittedAt
  const survey = !!exam && isSurvey(exam)
  const tier = sitting?.tier ?? 0
  const consents = requiredConsents(tier)

  // Integrity signals while the attempt is on screen, as far as the tier
  // collects them (integrity.ts). A preview on this device collects nothing.
  const onScreen = useRef<string | undefined>(undefined)
  onScreen.current = stage === 'attempt' ? item?.id : undefined
  const live = running && !!attempt?.id && (stage === 'attempt' || stage === 'review')
  useEffect(() => {
    if (!live || tier < 1) return
    return watch(tier, signal, () => onScreen.current)
  }, [live, tier, signal])
  const view = useMemo(() => viewer(tier, signal), [tier, signal])
  useEffect(() => {
    view(live ? onScreen.current : undefined)
  }, [live, view, idx, stage])
  useEffect(() => () => view(undefined), [view])

  // Tier 2: the face check, on this device, for as long as the attempt runs.
  const cam = useRef<Camera | null>(null)
  useEffect(() => {
    if (!live || tier < 2) return
    let gone = false
    void import('../backend/vision').then((v) => {
      if (!gone) cam.current = v.watchCamera(signal, setCamera)
    })
    return () => {
      gone = true
      cam.current?.stop()
      cam.current = null
    }
  }, [live, tier, signal])

  // The device is checked for real: on the start page itself when there is no
  // camera to ask about, and on its own step after consent when there is — the
  // camera is never opened before the candidate has agreed to it.
  const checking = stage === 'ready' || (stage === 'invite' && tier < 2)
  useEffect(() => {
    if (!checking) return
    let stale = false
    setChecks(null)
    void readiness(tier).then((c) => !stale && setChecks(c))
    if (tier >= 2 && CORE) void import('../backend/vision').then((v) => v.preload()).catch(() => {})
    return () => {
      stale = true
    }
  }, [checking, tier])

  /** A revoked or replaced invitation ends here, with the work still on the device. */
  const endedView = useCallback(() => {
    session.current = false
    void door.end()
    setConfirming(false)
    setGate({ at: 'refused', why: 'session' })
    setStage('gate')
  }, [])

  const sessionEnded = useCallback(async () => {
    if (!assignment || !seat) return false
    if (session.current && (await door.stands(seat))) return false
    endedView()
    return true
  }, [assignment, seat, endedView])

  // The server said so mid-attempt, while syncing an answer.
  useEffect(() => {
    if (sessionLost && session.current) endedView()
  }, [sessionLost, endedView])

  const finalize = useCallback(
    async (force: boolean) => {
      if (!exam || finalizing.current) return
      finalizing.current = true
      try {
        if (await sessionEnded()) return
        // What the camera and the clock on this question measured so far goes in before the attempt closes.
        cam.current?.flush()
        view(undefined)
        const r = await finalizeAttempt(exam, force)
        if (r.ok) {
          setBlocked([])
          setConfirming(false)
          setSubmitNote('')
          setStage('done')
        } else if ('missing' in r) {
          setBlocked(r.missing)
          setConfirming(false)
          const first = items.findIndex((i) => i.id === r.missing[0])
          if (first >= 0) setIdx(first)
          setStage('attempt')
        } else if (r.why === 'session') endedView()
        else {
          setConfirming(false)
          setSubmitNote('Belum terkirim: koneksi terputus. Jawaban Anda aman di perangkat ini. Coba kumpulkan lagi saat tersambung.')
        }
      } finally {
        finalizing.current = false
      }
    },
    [exam, items, finalizeAttempt, sessionEnded, endedView, view],
  )

  useEffect(() => {
    if (!running || !exam?.timed) return
    timer.current = window.setInterval(tickDown, 1000)
    return () => {
      if (timer.current) window.clearInterval(timer.current)
    }
  }, [running, exam?.timed, tickDown])

  useEffect(() => {
    if (!attempt || attempt.submittedAt || !exam?.timed || stage === 'gate') return
    if (attempt.secondsLeft <= 0) void finalize(true)
  }, [attempt, exam?.timed, finalize, stage])

  const open = (e: Exam) => {
    session.current = false
    setExam(e)
    resumeAttempt(e)
    setIdx(0)
    setStage('invite')
  }

  const begin = async () => {
    // Full screen needs this click, before anything is awaited.
    if (tier >= 2 && CORE) void enterFullscreen()
    if (!exam || (await sessionEnded())) return
    await beginAttempt(exam, assignment ?? undefined, consents)
    if (!useApp.getState().attempt) return
    setIdx(0)
    setStage('attempt')
  }

  /** From the start page: consent if the tier asks for any, the camera check if it needs one, else straight in. */
  const proceed = () => {
    setDeclined(false)
    if (consents.length) setStage('consent')
    else if (tier >= 2) setStage('ready')
    else void begin()
  }

  const answered = (it: Item) =>
    (attempt?.answers[it.id]?.length ?? 0) > 0 || !!attempt?.text[it.id]?.trim()
  const answeredCount = items.filter(answered).length

  const choose = (choiceId: string) => {
    if (!item) return
    setChoice(item, choiceId, idx + 1)
    setBlocked((b) => b.filter((x) => x !== item.id))
  }

  const typeAnswer = (v: string) => {
    if (!item) return
    setTextAnswer(item, v, idx + 1)
    if (v.trim()) setBlocked((b) => b.filter((x) => x !== item.id))
  }

  const attachFile = (m: Media | undefined) => item && setFile(item, m)

  const toggleFlag = () => item && toggleFlagIn(item, idx + 1)

  // The prototype has no mailbox to check, so the bar above the candidate's
  // screen shows the code that was "sent". It goes when real mail arrives.
  const stubCode =
    stage === 'gate' && gate.at === 'code' && gate.stub
      ? listOutbox().find((m) => m.kind === 'code' && m.assignmentId === gate.stub)?.code
      : undefined

  /** Leave the preview. A candidate's session started here (a local build's emailed link) is ended on the way out. */
  const exitPreview = () => {
    if (assignment || location.hash === SESSION_HASH) return setScreen('library')
    session.current = false
    setExam(null)
    setConfirming(false)
    setStage('pick')
  }

  /* ── a preview BAR above the app, not a chip floating over it. A control that
        obscures the thing it is previewing is a bad control, and on mobile the
        floating version covered the sync status. ── */
  const previewBar = (
    <div
      data-chrome
      className="flex shrink-0 items-center gap-2 px-4 py-1.5"
      style={{ background: 'var(--color-ink)', color: 'var(--color-plane)' }}
    >
      <IconEye size={13} stroke={1.9} className="shrink-0 opacity-70" />
      <span className="min-w-0 flex-1 truncate text-[11.5px] opacity-80">
        Candidate preview{exam ? ` · ${exam.title}` : ''} — candidates see only what is below this bar
      </span>
      {stubCode && (
        <span className="tnum shrink-0 text-[11.5px]" title="No mail is sent in the prototype. This is what the candidate's inbox would show.">
          <span className="opacity-70">Stub inbox · code </span>
          <span data-testid="stub-code" className="font-semibold">{stubCode}</span>
        </span>
      )}
      {exam && !assignment && stage !== 'invite' && (
        <button
          onClick={() => {
            discardAttempt(exam.id)
            open(exam)
          }}
          className="shrink-0 rounded px-2 py-0.5 text-[11.5px] font-medium"
          style={{ background: 'rgb(255 255 255 / 0.16)' }}
        >
          Restart
        </button>
      )}
      <button
        onClick={exitPreview}
        className="shrink-0 rounded px-2 py-0.5 text-[11.5px] font-medium"
        style={{ background: 'rgb(255 255 255 / 0.16)' }}
      >
        Exit preview
      </button>
    </div>
  )

  // In the studio, with nothing chosen yet: the administrator picks what to preview.
  const arriving = !!previewWanted || !!inviteToken || location.hash === SESSION_HASH
  if (!standalone && !arriving && (stage === 'pick' || (!exam && stage !== 'gate'))) return <PreviewPicker exams={exams} onOpen={(e) => {
    setAssignment(null)
    open(e)
  }} />

  const warnAt = attempt && exam?.timed ? (attempt.secondsLeft <= 60 ? 60 : attempt.secondsLeft <= 300 ? 300 : 0) : 0
  const showWarn = warnAt > 0 && dismissedWarn !== warnAt && stage === 'attempt'

  /* ── stages ─────────────────────────────────────────────────────────────── */

  let body: React.ReactNode = null

  if (stage === 'gate')
    body = (
      <Screen>
        {gate.at === 'checking' && (
          <p role="status" className="pt-10 text-center text-[13.5px] text-ink-muted">
            Memeriksa undangan Anda…
          </p>
        )}

        {gate.at === 'send' && (
          <>
            <span className="grid h-12 w-12 place-items-center rounded-xl" style={{ background: 'var(--color-wash)' }}>
              <IconMailCheck size={24} stroke={1.7} style={{ color: 'var(--color-s1)' }} />
            </span>
            <div className="mt-4">
              <Title>Pastikan ini memang Anda</Title>
            </div>
            <p className="mt-2 text-[13.5px] leading-relaxed text-ink-2">
              Undangan ini dikirim ke <strong className="tnum font-medium text-ink">{gate.email}</strong>. Kami
              akan mengirim kode 6 digit ke alamat itu. Setelah kode cocok, tautan ini tidak dapat dipakai lagi
              oleh siapa pun.
            </p>
            <div className="mt-6">
              <Primary onClick={() => sendCode(gate.token, gate.email, gate.stub)}>
                Kirim kode <IconArrowRight size={17} stroke={2.2} />
              </Primary>
            </div>
          </>
        )}

        {gate.at === 'code' && (
          <div>
            <Title>Masukkan kode</Title>
            <p className="mt-2 text-[13.5px] leading-relaxed text-ink-2">
              Kode 6 digit dikirim ke <strong className="tnum font-medium text-ink">{gate.email}</strong> dan
              berlaku 10 menit.
            </p>
            <label htmlFor="invite-code" className="mt-5 block text-[13px] font-medium text-ink">
              Kode verifikasi
            </label>
            <input
              id="invite-code"
              autoFocus
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9]{6}"
              maxLength={6}
              value={code}
              onChange={(e) => {
                setCode(e.currentTarget.value.replace(/\D/g, '').slice(0, 6))
                setCodeNote('')
              }}
              onKeyDown={(e) => e.key === 'Enter' && code.length === 6 && void submitCode(gate.token)}
              aria-describedby="invite-code-note"
              aria-invalid={!!codeNote}
              className="tnum hair mt-1.5 h-14 w-full rounded-xl bg-surface px-4 text-[24px] font-semibold tracking-[0.4em] text-ink"
            />
            <p id="invite-code-note" role="alert" className="mt-2 min-h-[18px] text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
              {codeNote}
            </p>
            <div className="mt-3">
              <Primary onClick={() => void submitCode(gate.token)} disabled={code.length !== 6}>
                Verifikasi <IconArrowRight size={17} stroke={2.2} />
              </Primary>
            </div>
            <button
              type="button"
              disabled={now < gate.retryAt}
              onClick={() => sendCode(gate.token, gate.email, gate.stub)}
              className="tnum mt-3 flex h-11 w-full items-center justify-center text-[13.5px] font-medium text-ink-2 disabled:opacity-60"
            >
              {now < gate.retryAt
                ? `Kirim ulang kode dalam ${Math.ceil((gate.retryAt - now) / 1000)} detik`
                : 'Kirim ulang kode'}
            </button>
          </div>
        )}

        {gate.at === 'refused' && (
          <div role="alert">
            <span className="grid h-12 w-12 place-items-center rounded-xl" style={{ background: 'var(--color-wash-warn)' }}>
              <IconLinkOff size={24} stroke={1.7} style={{ color: 'var(--color-serious)' }} />
            </span>
            <div className="mt-4">
              <Title>{REFUSED[gate.why].title}</Title>
            </div>
            <p className="mt-2 text-[13.5px] leading-relaxed text-ink-2">{REFUSED[gate.why].body}</p>
            {gate.resent ? (
              <p className="mt-5 rounded-xl p-4 text-[13.5px] leading-relaxed text-ink" style={{ background: 'var(--color-wash-good)' }}>
                Tautan baru dikirim ke <strong className="tnum font-medium">{gate.email}</strong>. Buka email
                terbaru; tautan lama tidak berlaku.
              </p>
            ) : (
              gate.token &&
              (gate.why === 'used' || gate.why === 'superseded') && (
                <div className="mt-6">
                  <Primary onClick={() => askNewLink(gate.token!, gate.why, gate.email)}>
                    <IconMail size={17} stroke={2} /> Kirim tautan baru ke email saya
                  </Primary>
                </div>
              )
            )}
          </div>
        )}
      </Screen>
    )
  else if (stage === 'pick' || !exam)
    // Someone with no link. There is nothing to choose: an invitation opens one assessment.
    body = (
      <Screen>
        <span className="grid h-12 w-12 place-items-center rounded-xl" style={{ background: 'var(--color-wash)' }}>
          <IconMail size={24} stroke={1.7} style={{ color: 'var(--color-s1)' }} />
        </span>
        <div className="mt-4">
          <Title>Buka tautan dari email undangan Anda</Title>
        </div>
        <p className="mt-2 text-[13.5px] leading-relaxed text-ink-2">
          Setiap peserta menerima tautan pribadi lewat email. Buka tautan itu di perangkat yang akan Anda pakai, dan
          asesmen Anda langsung terbuka di sini.
        </p>
      </Screen>
    )
  else if (stage === 'invite') {
    // Against the core the seat carries the attempt; locally it is in this browser.
    const saved = CORE && assignment ? attempt : getAttempt(exam.id, assignment?.id)
    const resumable = saved && !saved.submittedAt
    body = (
      <Screen>
        <p className="text-[12px] font-medium uppercase tracking-wider text-ink-muted">{exam.issuer}</p>
        <Title>{exam.title}</Title>
        {assignment && (
          <p className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px] text-ink-2">
            <IconMail size={13} stroke={1.9} className="text-ink-muted" />
            Untuk <strong className="font-medium text-ink">{seat?.who}</strong>
            {assignment.dueAt && (
              <span className="tnum text-ink-muted">· batas {new Date(assignment.dueAt).toLocaleDateString()}</span>
            )}
            <span className="inline-flex items-center gap-1 text-[11.5px] text-ink-muted">
              <IconShieldCheck size={12} stroke={1.9} /> email terverifikasi
            </span>
          </p>
        )}
        <p className="mt-2.5 text-[13.5px] leading-relaxed text-ink-2">
          {survey
            ? 'Jawaban Anda membantu kami memperbaiki proses. Tidak ada jawaban benar atau salah.'
            : 'Bacalah rincian di bawah sebelum memulai. Anda dapat menandai soal dan kembali lagi sebelum mengumpulkan.'}
        </p>

        {resumable && (
          <div className="mt-5 rounded-xl p-4" style={{ background: 'var(--color-wash)' }}>
            <p className="text-[13.5px] font-semibold text-ink">Anda punya pengerjaan yang tertunda</p>
            <p className="tnum mt-1 text-[12.5px] text-ink-2">
              {saved!.answers && Object.keys(saved!.answers).length} jawaban tersimpan
              {exam.timed ? ` · sisa ${mmss(saved!.secondsLeft)}` : ''}
            </p>
            <button
              onClick={async () => {
                if (await sessionEnded()) return
                resumeAttempt(exam, assignment?.id)
                setStage('attempt')
              }}
              className="mt-3 flex h-11 w-full items-center justify-center gap-2 rounded-xl text-[14px] font-semibold text-on-brand"
              style={{ background: 'var(--color-s1)' }}
            >
              <IconRotate size={16} stroke={2} /> Lanjutkan
            </button>
          </div>
        )}

        <dl className="hair mt-5 divide-y divide-hair rounded-xl bg-surface">
          <Row k="Pertanyaan" v={String(items.length)} />
          {exam.timed && <Row k="Durasi" v={`${totalMinutes(exam)} menit`} />}
          {isScored(exam) && <Row k="Total poin" v={String(totalPoints(exam))} />}
          {isScored(exam) && <Row k="Ambang lulus" v={`${exam.passPct}%`} />}
          {exam.anonymous && <Row k="Anonim" v="Ya" />}
        </dl>

        <ul className="mt-4 flex flex-col gap-1.5 text-[12.5px] leading-snug text-ink-2">
          <li>• Jawaban tersimpan otomatis setiap kali Anda mengetik atau memilih, juga saat koneksi terputus.</li>
          {exam.timed && <li>• Waktu berjalan sejak Anda menekan Mulai sampai Anda mengumpulkan.</li>}
          <li>• Gunakan satu tab saja.</li>
        </ul>

        {tier < 2 && <DeviceChecks checks={checks} compact />}

        <div className="mt-4 rounded-xl border border-hair p-4">
          <p className="flex items-center gap-1.5 text-[13px] font-semibold text-ink">
            <IconShieldLock size={15} stroke={1.9} className="text-ink-muted" />
            Yang kami kumpulkan
          </p>
          <ul className="mt-2 flex flex-col gap-1.5 text-[12.5px] leading-snug text-ink-2">
            <li>• Jawaban Anda dan waktu pengerjaan.</li>
            {consents.map((p) => (
              <li key={p}>• {PURPOSES[p].title}.</li>
            ))}
            {exam.anonymous && <li>• Tidak ada nama yang disimpan bersama jawaban.</li>}
          </ul>
        </div>

        {/* An invited sitting is the one sitting. Starting over would hand back the
            clock, so with work in progress the only way forward is to continue it. */}
        {!(assignment && resumable) && (
          <div className="mt-6">
            <Primary onClick={proceed}>
              {resumable ? 'Mulai ulang dari awal' : survey ? 'Mulai mengisi' : 'Mulai'}
              <IconArrowRight size={17} stroke={2.2} />
            </Primary>
          </div>
        )}
      </Screen>
    )
  } else if (stage === 'consent')
    body = (
      <Screen>
        <Title>Persetujuan pemrosesan data</Title>
        <p className="mt-2 text-[13.5px] leading-relaxed text-ink-2">
          Sesuai UU PDP No. 27/2022, berikut setiap hal yang dicatat selama ujian ini, untuk apa, dan berapa banyak.
          Penyelenggara mewajibkan semuanya untuk ujian ini.
        </p>
        <div className="mt-5 flex flex-col gap-2.5">
          {consents.map((p) => (
            <div key={p} className="hair rounded-xl bg-surface p-4">
              <p className="text-[14px] font-medium text-ink">{PURPOSES[p].title}</p>
              <p className="mt-1.5 text-[12.5px] leading-snug text-ink-2">{PURPOSES[p].what}</p>
              <p className="mt-1 text-[12px] leading-snug text-ink-muted">{PURPOSES[p].keep}</p>
            </div>
          ))}
        </div>
        <p className="mt-3 text-[12px] leading-snug text-ink-muted">
          Setiap catatan menjadi bagian dari berkas pengerjaan Anda. Jika ada yang ditandai, alasannya ditulis apa
          adanya dan Anda berhak meminta serta menyanggahnya.
        </p>
        {declined && (
          <p role="status" className="mt-4 rounded-xl p-4 text-[13px] leading-snug text-ink" style={{ background: 'var(--color-wash)' }}>
            Tanpa persetujuan ini, ujian ini tidak dapat dikerjakan di sini. Tidak ada yang dicatat. Hubungi penyelenggara
            jika Anda memerlukan cara lain untuk mengikutinya.
          </p>
        )}
        <div className="mt-6">
          <Primary onClick={() => (tier >= 2 ? setStage('ready') : void begin())}>
            Setuju dan lanjut <IconArrowRight size={17} stroke={2.2} />
          </Primary>
        </div>
        <button onClick={() => setDeclined(true)} className="mt-3 w-full text-center text-[13px] text-ink-2 underline">
          Tidak setuju
        </button>
      </Screen>
    )
  else if (stage === 'ready')
    body = (
      <Screen>
        <Title>Siap memulai?</Title>
        <p className="mt-2 text-[13.5px] leading-relaxed text-ink-2">
          Kami periksa sekarang supaya tidak ada kejutan saat pengerjaan berjalan.
        </p>
        <DeviceChecks checks={checks} />

        <div className="mt-4 rounded-xl p-4" style={{ background: 'var(--color-wash)' }}>
          <p className="text-[13px] font-semibold text-ink">Jaringan lambat atau terputus bukan masalah</p>
          <p className="mt-1 text-[12.5px] leading-snug text-ink-2">
            Seluruh bagian diunduh lebih dulu. Pengerjaan berjalan luring dan tersinkron otomatis
            saat koneksi kembali.
          </p>
        </div>

        <div className="mt-6">
          <Primary onClick={begin}>
            <IconPlayerPlay size={17} stroke={2.2} /> Mulai sekarang
          </Primary>
        </div>
      </Screen>
    )
  else if (stage === 'attempt' && attempt && item)
    body = (
      <Screen wide>
        <p className="tnum text-[12px] font-medium uppercase tracking-wider text-ink-muted">
          Pertanyaan {idx + 1} dari {items.length}
          {item.skill ? ` · ${item.skill}` : ''}
        </p>
        {tier >= 2 && camera && (
          <p role="status" className="mt-1 text-[12px] text-ink-muted">
            {camera === 'on'
              ? 'Kamera aktif · diperiksa di perangkat ini, tidak direkam'
              : camera === 'starting'
                ? 'Menyalakan kamera…'
                : 'Kamera tidak aktif · ini dicatat untuk ditinjau'}
          </p>
        )}
        <p className="mt-2 text-[18px] font-semibold leading-snug text-ink">
          {item.stem}
          {item.required && <span style={{ color: 'var(--color-critical)' }}> *</span>}
        </p>
        {item.image && <ItemImage media={item.image} max={340} className="mt-4 w-fit" />}
        {blocked.includes(item.id) && (
          <p className="mt-3 flex items-center gap-1.5 text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
            <IconAlertTriangle size={14} stroke={2.2} /> Pertanyaan ini wajib dijawab.
          </p>
        )}
        <div className="mt-5">
          <div data-item-type={item.type}>
            <ItemView item={item} attempt={attempt} onChoice={choose} onText={typeAnswer} onFile={attachFile} />
          </div>
        </div>
        <button
          onClick={toggleFlag}
          className="mt-5 inline-flex items-center gap-1.5 text-[13px]"
          style={{ color: attempt.flagged.includes(item.id) ? 'var(--color-warn)' : undefined }}
        >
          <IconFlag size={15} stroke={1.8} className={attempt.flagged.includes(item.id) ? '' : 'text-ink-muted'} />
          <span className={attempt.flagged.includes(item.id) ? 'font-medium' : 'text-ink-2'}>
            {attempt.flagged.includes(item.id) ? 'Ditandai untuk ditinjau' : 'Tandai untuk ditinjau'}
          </span>
        </button>
      </Screen>
    )
  else if (stage === 'review' && attempt)
    body = (
      <Screen>
        <Title>Tinjau sebelum mengumpulkan</Title>
        <div className="mt-4 grid grid-cols-3 gap-2">
          {[
            ['Terjawab', String(answeredCount), 'var(--color-good)'],
            ['Ditandai', String(attempt.flagged.length), 'var(--color-warn)'],
            ['Belum', String(items.length - answeredCount), 'var(--color-critical)'],
          ].map(([k, v, c]) => (
            <div key={k} className="hair rounded-xl bg-surface p-3 text-center">
              <p className="tnum text-[22px] font-semibold leading-none" style={{ color: c }}>
                {v}
              </p>
              <p className="mt-1.5 text-[12px] text-ink-muted">{k}</p>
            </div>
          ))}
        </div>

        <div className="mt-5 grid grid-cols-6 gap-2 sm:grid-cols-8">
          {items.map((it, n) => {
            const ans = answered(it)
            const flag = attempt.flagged.includes(it.id)
            return (
              <button
                key={it.id}
                onClick={() => {
                  setIdx(n)
                  setStage('attempt')
                }}
                className="tnum relative grid h-11 place-items-center rounded-lg text-[13px] font-medium"
                style={{
                  background: ans ? 'var(--color-track)' : 'transparent',
                  color: ans ? 'var(--color-brand-text)' : 'var(--color-ink-muted)',
                  boxShadow: ans ? 'none' : 'inset 0 0 0 1px var(--color-hair)',
                }}
                aria-label={`Pertanyaan ${n + 1}${ans ? ', terjawab' : ', belum dijawab'}`}
              >
                {n + 1}
                {flag && (
                  <span className="absolute right-1 top-1 h-1.5 w-1.5 rounded-full" style={{ background: 'var(--color-warn)' }} />
                )}
              </button>
            )
          })}
        </div>

        <div className="mt-6">
          <Primary tone="good" onClick={() => setConfirming(true)}>
            <IconCheck size={17} stroke={2.4} /> {survey ? 'Kirim jawaban' : 'Kumpulkan'}
          </Primary>
          <button
            onClick={() => setStage('attempt')}
            className="mt-2.5 flex h-11 w-full items-center justify-center gap-1.5 text-[13.5px] font-medium text-ink-2"
          >
            <IconArrowLeft size={15} stroke={2} /> Kembali mengerjakan
          </button>
        </div>
      </Screen>
    )
  else if (stage === 'done' && attempt) {
    const s = exam ? score(exam, attempt) : null
    body = (
      <Screen>
        {survey ? (
          <div className="pt-4 text-center">
            <span className="mx-auto grid h-16 w-16 place-items-center rounded-2xl" style={{ background: 'var(--color-wash-good)' }}>
              <IconMoodSmile size={30} stroke={1.7} style={{ color: 'var(--color-good)' }} />
            </span>
            <h1 className="mt-4 text-[22px] font-semibold tracking-tight text-ink">Terima kasih</h1>
            <p className="mt-2 text-[13.5px] leading-relaxed text-ink-2">
              Jawaban Anda sudah tercatat{exam!.anonymous ? ' secara anonim' : ''}.
            </p>
            {!CORE && (
              <p className="tnum mt-3 text-[12.5px] text-ink-muted">
                {responses.length} tanggapan terkumpul
              </p>
            )}
          </div>
        ) : (
          <>
            <div className="text-center">
              <span
                className="mx-auto grid h-16 w-16 place-items-center rounded-2xl"
                style={{ background: attempt.score?.passed ? 'var(--color-wash-good)' : 'var(--color-wash-critical)' }}
              >
                {attempt.score?.passed ? (
                  <IconShieldCheck size={30} stroke={1.7} style={{ color: 'var(--color-good)' }} />
                ) : (
                  <IconX size={30} stroke={2} style={{ color: 'var(--color-critical)' }} />
                )}
              </span>
              <p className="mt-4 text-[44px] font-semibold leading-none tracking-tight text-ink">
                {attempt.score?.pct ?? 0}%
              </p>
              <p className="tnum mt-2 text-[13px] text-ink-2">
                {attempt.score?.raw} / {attempt.score?.max} poin · ambang {exam!.passPct}%
              </p>
              <p
                className="mt-3 inline-flex items-center gap-1.5 rounded-full px-3.5 py-1.5 text-[13px] font-semibold"
                style={{
                  background: attempt.score?.passed ? 'var(--color-wash-good)' : 'var(--color-wash-critical)',
                  color: attempt.score?.passed ? 'var(--color-good-text)' : 'var(--color-critical)',
                }}
              >
                {attempt.score?.passed ? 'Lulus' : 'Belum lulus'}
              </p>
            </div>
            <div className="mt-5">
              <Meter value={attempt.score?.pct ?? 0} sev={attempt.score?.passed ? 'ok' : 'critical'} />
            </div>
            {s && s.pendingHuman > 0 && (
              <p className="mt-4 rounded-xl p-3.5 text-[12.5px] leading-snug text-ink-2" style={{ background: 'var(--color-wash-warn)' }}>
                <strong className="tnum font-medium text-ink">{s.pendingHuman} poin</strong> menunggu penilaian
                manusia dan belum termasuk di atas. Skor akhir dapat naik.
              </p>
            )}
            {(CORE ? !!attempt.credentialUrl : attempt.score?.passed) && (
              <div className="mt-4 rounded-xl p-4 text-white" style={{ background: 'linear-gradient(135deg, var(--color-credential-from) 0%, var(--color-credential-to) 100%)' }}>
                <p className="text-[10.5px] font-medium uppercase tracking-wider opacity-80">Sertifikat digital</p>
                <p className="mt-1.5 text-[15px] font-semibold leading-tight">{exam!.title}</p>
                <p className="mt-0.5 text-[12px] opacity-85">{exam!.issuer} · Open Badges 3.0</p>
                {attempt.credentialUrl && (
                  <a
                    href={attempt.credentialUrl}
                    target="_blank"
                    rel="noopener"
                    className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-lg bg-white/15 py-2 text-[12.5px] font-semibold"
                  >
                    <IconCertificate size={14} stroke={2} /> Lihat dan bagikan sertifikat
                  </a>
                )}
                {CORE && <p className="mt-2 text-[11.5px] opacity-85">Tautannya juga dikirim ke email Anda.</p>}
              </div>
            )}
          </>
        )}

        {/* transparency: show the participant exactly what was recorded */}
        <div className="hair mt-6 rounded-xl bg-surface">
          <button
            onClick={() => setShowLog((v) => !v)}
            className="flex w-full items-center justify-between gap-2 px-4 py-3 text-left"
          >
            <span className="text-[13px] font-medium text-ink">Apa yang kami catat</span>
            <IconChevronDown size={15} stroke={2} className={`text-ink-muted transition-transform ${showLog ? 'rotate-180' : ''}`} />
          </button>
          <Collapse expanded={showLog} keepMounted={false}>
            <ol className="border-t border-hair px-4 py-3">
              {attempt.events.slice(-10).map((e) => (
                <li key={e.seq} className="tnum flex items-baseline justify-between gap-3 py-1 text-[12px]">
                  <span className="text-ink-2">{e.kind}</span>
                  <span className="text-ink-muted">{new Date(e.t).toLocaleTimeString()}</span>
                </li>
              ))}
              <li className="tnum mt-1.5 border-t border-hair pt-2 text-[11.5px] text-ink-muted">
                {attempt.events.length} peristiwa · tidak ada video yang diunggah
              </li>
            </ol>
          </Collapse>
        </div>

        {/* Trying again is for open practice. An invitation is sat once. */}
        {!assignment && (
          <button
            onClick={() => {
              discardAttempt(exam!.id)
              setIdx(0)
              setStage('pick')
            }}
            className="mt-4 flex h-11 w-full items-center justify-center gap-1.5 text-[13.5px] font-medium text-ink-2"
          >
            <IconRotate size={15} stroke={2} /> {survey ? 'Isi lagi sebagai responden baru' : 'Coba lagi'}
          </button>
        )}
      </Screen>
    )
  }

  /* ── chrome ─────────────────────────────────────────────────────────────── */

  const inAttempt = stage === 'attempt' || stage === 'review'

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-plane">
      {!standalone && previewBar}

      {exam && exam.status !== 'published' && stage !== 'pick' && (
        <div
          className="flex shrink-0 items-center gap-2 px-5 py-1.5 sm:px-6"
          style={{ background: 'var(--color-wash-warn)' }}
        >
          <IconAlertTriangle size={13} stroke={2.2} style={{ color: 'var(--color-serious)' }} />
          <p className="text-[11.5px]" style={{ color: 'var(--color-serious)' }}>
            {exam.status === 'draft' ? 'Draft' : 'Archived'} — no candidate can reach this yet.
          </p>
        </div>
      )}
      {inAttempt && exam && attempt && (
        <header className="shrink-0 border-b border-hair bg-surface">
          <div className="mx-auto flex w-full max-w-[760px] items-center gap-3 px-5 py-3 sm:px-6">
            <div className="min-w-0 flex-1">
              <p className="truncate text-[13px] font-semibold text-ink">{exam.title}</p>
              <p className="tnum truncate text-[11.5px] text-ink-muted">
                {answeredCount}/{items.length} terjawab
                {attempt.queued > 0 ? ` · ${attempt.queued} menunggu sinkron` : ' · tersimpan'}
              </p>
            </div>
            {exam.timed && (
              <span
                className="tnum flex shrink-0 items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[15px] font-semibold"
                style={
                  warnAt === 60
                    ? { background: 'var(--color-wash-critical)', color: 'var(--color-critical)' }
                    : warnAt === 300
                      ? { background: 'var(--color-wash-warn)', color: 'var(--color-serious)' }
                      : { background: 'var(--color-sunken)', color: 'var(--color-ink)' }
                }
              >
                <IconLock size={14} stroke={2} />
                {mmss(attempt.secondsLeft)}
              </span>
            )}
          </div>
          <div className="h-1 w-full" style={{ background: 'var(--color-track)' }}>
            <div
              className="h-full transition-all"
              style={{ width: `${((idx + 1) / items.length) * 100}%`, background: 'var(--color-s1)' }}
            />
          </div>
        </header>
      )}

      {showWarn && (
        <div
          className="flex shrink-0 items-center gap-2 px-5 py-2 sm:px-6"
          style={{ background: warnAt === 60 ? 'var(--color-wash-critical)' : 'var(--color-wash-warn)' }}
        >
          <IconAlertTriangle
            size={14}
            stroke={2.2}
            style={{ color: warnAt === 60 ? 'var(--color-critical)' : 'var(--color-serious)' }}
          />
          <p className="flex-1 text-[12.5px]" style={{ color: warnAt === 60 ? 'var(--color-critical)' : 'var(--color-serious)' }}>
            {warnAt === 60 ? 'Sisa waktu kurang dari 1 menit.' : 'Sisa waktu 5 menit.'}
          </p>
          <button onClick={() => setDismissedWarn(warnAt)} aria-label="Tutup peringatan">
            <IconX size={14} stroke={2.2} className="text-ink-muted" />
          </button>
        </div>
      )}

      {body}

      {stage === 'attempt' && attempt && (
        <footer className="shrink-0 border-t border-hair bg-surface">
          <div className="mx-auto flex w-full max-w-[760px] items-center gap-2.5 px-5 pb-5 pt-3 sm:px-6">
            <button
              onClick={() => setIdx(Math.max(0, idx - 1))}
              disabled={idx === 0}
              aria-label="Sebelumnya"
              className="hair grid h-12 w-12 shrink-0 place-items-center rounded-xl bg-surface disabled:opacity-35"
            >
              <IconArrowLeft size={18} stroke={2} className="text-ink-2" />
            </button>
            <button
              onClick={() => setStage('review')}
              className="hair flex h-12 shrink-0 items-center gap-1.5 rounded-xl bg-surface px-3.5 text-[13px] font-medium text-ink-2"
            >
              <IconListCheck size={16} stroke={1.9} /> Tinjau
            </button>
            {idx === items.length - 1 ? (
              <button
                onClick={() => setStage('review')}
                className="flex h-12 flex-1 items-center justify-center gap-2 rounded-xl text-[15px] font-semibold text-on-brand"
                style={{ background: 'var(--color-good-fill)' }}
              >
                Selesai <IconCheck size={17} stroke={2.4} />
              </button>
            ) : (
              <button
                onClick={() => setIdx(idx + 1)}
                className="flex h-12 flex-1 items-center justify-center gap-2 rounded-xl text-[15px] font-semibold text-on-brand"
                style={{ background: 'var(--color-s1)' }}
              >
                Berikutnya <IconArrowRight size={17} stroke={2.2} />
              </button>
            )}
          </div>
        </footer>
      )}

      {/* submit confirmation — never a surprise */}
      {confirming && attempt && exam && (
        <div className="fixed inset-0 z-[70] flex items-end justify-center bg-black/40 sm:items-center">
          <div className="w-full max-w-[420px] rounded-t-2xl bg-surface p-5 sm:rounded-2xl">
            <p className="text-[16px] font-semibold text-ink">
              {survey ? 'Kirim jawaban?' : 'Kumpulkan sekarang?'}
            </p>
            <p className="mt-1.5 text-[13px] leading-snug text-ink-2">
              {items.length - answeredCount > 0
                ? `${items.length - answeredCount} pertanyaan belum dijawab. Anda tidak dapat mengubah jawaban setelah mengumpulkan.`
                : 'Semua pertanyaan sudah dijawab. Anda tidak dapat mengubah jawaban setelah mengumpulkan.'}
            </p>
            <div className="mt-5 flex flex-col gap-2">
              <button
                onClick={() => finalize(false)}
                className="flex h-12 items-center justify-center gap-2 rounded-xl text-[15px] font-semibold text-on-brand"
                style={{ background: 'var(--color-good-fill)' }}
              >
                <IconCheck size={17} stroke={2.4} /> Ya, kumpulkan
              </button>
              <button
                onClick={() => setConfirming(false)}
                className="flex h-11 items-center justify-center text-[13.5px] font-medium text-ink-2"
              >
                Batal
              </button>
            </div>
          </div>
        </div>
      )}

      {submitNote && inAttempt && (
        <p role="alert" className="shrink-0 border-t border-hair px-5 py-2 text-[12px] text-ink-2 sm:px-6" style={{ background: 'var(--color-wash-warn)' }}>
          {submitNote}
        </p>
      )}

      {/* offline strip — honest, and never alarming. Against the core it shows
          only while answers are actually waiting for a connection. */}
      {inAttempt && attempt && attempt.queued > 0 && (!attempt.id || offline) && (
        <div className="flex shrink-0 items-center gap-2 border-t border-hair px-5 py-2 sm:px-6" style={{ background: 'var(--color-wash-warn)' }}>
          <IconWifiOff size={13} stroke={1.8} className="text-ink-2" />
          <span className="tnum text-[11.5px] text-ink-2">
            Tersimpan di perangkat · {attempt.queued} menunggu sinkron
          </span>
        </div>
      )}
    </div>
  )
}
