/** Fixtures for the mockup. Nothing here talks to a backend. */

export type Severity = 'ok' | 'warning' | 'serious' | 'critical'

export const exam = {
  title: 'Sertifikasi Backend Engineer — Level 3',
  issuer: 'LSP Teknologi Informasi',
  section: 'Bagian 2 dari 3 · Konkurensi',
  tier: 'Tier 3 · Certified',
  itemIndex: 8,
  itemTotal: 20,
  secondsLeft: 767,
  queued: 3,
  online: false,
}

export const item = {
  stem: 'Sebuah layanan Go menerima 12.000 permintaan per detik. Setiap permintaan menulis satu baris audit ke channel ber-buffer berkapasitas 1.024 yang dikonsumsi oleh satu goroutine tunggal.',
  question: 'Apa yang terjadi ketika konsumen melambat menjadi 9.000 baris per detik selama 30 detik?',
  options: [
    { id: 'a', label: 'Penulis memblokir, latensi permintaan naik, lalu pulih', tag: null },
    { id: 'b', label: 'Baris audit lama ditimpa secara diam-diam', tag: null },
    { id: 'c', label: 'Channel tumbuh dinamis; memori naik hingga OOM', tag: null },
    { id: 'd', label: 'Runtime menjatuhkan goroutine penulis dengan panic', tag: null },
  ],
  selected: 'a',
  variant: 'v-7f3a · unik per peserta',
}

export const doors = [
  {
    n: 1,
    key: 'link',
    name: 'Link out',
    desc: 'Host mints an attempt, sends the candidate a URL.',
    effort: '~10 min',
    offline: 'Full PWA',
    state: 'live' as const,
  },
  {
    n: 2,
    key: 'embed',
    name: 'Embed',
    desc: '<assessmatrix-runner> web component in a sandboxed iframe.',
    effort: '~1 hour',
    offline: 'Degraded',
    state: 'live' as const,
  },
  {
    n: 3,
    key: 'headless',
    name: 'Headless',
    desc: 'Connect RPC v3 + SDK. You render your own UI.',
    effort: '1–2 weeks',
    offline: 'Yours',
    state: 'beta' as const,
  },
  {
    n: 4,
    key: 'sovereign',
    name: 'Sovereign',
    desc: 'Self-host, or our cloud on your CNAME. Data stays in-region.',
    effort: 'Deployment',
    offline: 'Full PWA',
    state: 'enterprise' as const,
  },
]

export const connectedApps = [
  { name: 'Greenhouse', kind: 'ATS', door: 'Embed', events: 1284, health: 'ok' as Severity, last: '12s ago' },
  { name: 'Moodle — Univ. Brawijaya', kind: 'LMS · LTI 1.3', door: 'Standards', events: 9102, health: 'ok' as Severity, last: '4s ago' },
  { name: 'Workable', kind: 'ATS', door: 'Embed', events: 318, health: 'warning' as Severity, last: '6m ago' },
  { name: 'SIAP-LSP', kind: 'Registration', door: 'Headless', events: 2041, health: 'ok' as Severity, last: '31s ago' },
  { name: 'Talenta HRIS', kind: 'HRIS · SCIM', door: 'Standards', events: 76, health: 'critical' as Severity, last: '2h ago' },
]

export const preflight = [
  {
    state: 'pass' as const,
    title: 'frame-ancestors allows app.your-ats.com',
    detail: 'Tenant origin allowlist is enforced at the CSP layer.',
  },
  {
    state: 'pass' as const,
    title: 'Attempt token minted server-side',
    detail: 'Scoped to one attempt, expires in 15 min. Your API key never reaches the browser.',
  },
  {
    state: 'fail' as const,
    title: 'Camera is not delegated to the iframe',
    detail: 'Tier 2+ proctoring will fail silently. Add allow="camera; microphone" to your <iframe>.',
    fix: 'Apply fix',
  },
  {
    state: 'warn' as const,
    title: 'Third-party storage is partitioned',
    detail: 'The offline queue falls back to memory. For full offline use Door 1 or Door 4.',
    fix: 'Compare doors',
  },
]

export const liveAttempts = [
  { id: 'AT-4821', name: 'Budi Santoso', exam: 'Backend L3', risk: 72, sev: 'serious' as Severity, flags: ['2nd tab opened', 'gaze off-screen ×4'], min: 34, net: 'weak' },
  { id: 'AT-4822', name: 'Sari Dewi', exam: 'Backend L3', risk: 8, sev: 'ok' as Severity, flags: [], min: 31, net: 'good' },
  { id: 'AT-4823', name: 'Andi Pratama', exam: 'Backend L3', risk: 91, sev: 'critical' as Severity, flags: ['Second person detected', 'paste from outside'], min: 29, net: 'good' },
  { id: 'AT-4824', name: 'Maya Kusuma', exam: 'Backend L3', risk: 21, sev: 'ok' as Severity, flags: ['app switch ×1'], min: 28, net: 'offline' },
  { id: 'AT-4825', name: 'Rizky Hidayat', exam: 'Data Analyst L2', risk: 44, sev: 'warning' as Severity, flags: ['linear typing, no debugging'], min: 12, net: 'good' },
  { id: 'AT-4826', name: 'Putri Anggraini', exam: 'Data Analyst L2', risk: 5, sev: 'ok' as Severity, flags: [], min: 9, net: 'good' },
]

export const evidenceTimeline = [
  { t: '09:12:04', kind: 'ok', label: 'Identity verified — biometric match 0.97' },
  { t: '09:12:31', kind: 'ok', label: 'Attempt started · Tier 3 · lockdown engaged' },
  { t: '09:31:18', kind: 'warn', label: 'Focus lost 4.2s — app switch' },
  { t: '09:38:52', kind: 'warn', label: 'Gaze off-screen ×4 within 90s' },
  { t: '09:41:07', kind: 'crit', label: 'Second tab requested — refused by coordinator' },
  { t: '09:41:09', kind: 'info', label: 'Candidate shown reason: "one tab per attempt"' },
]

export const deliveries = [
  { id: 'evt_9f2c41', event: 'attempt.scored', target: 'Greenhouse', code: 200, ms: 148, at: '10:42:11', tries: 1 },
  { id: 'evt_9f2c40', event: 'integrity.flag.raised', target: 'SIAP-LSP', code: 200, ms: 96, at: '10:41:58', tries: 1 },
  { id: 'evt_9f2c3f', event: 'credential.issued', target: 'Talenta HRIS', code: 503, ms: 30_012, at: '10:41:40', tries: 4 },
  { id: 'evt_9f2c3e', event: 'attempt.submitted', target: 'Greenhouse', code: 200, ms: 133, at: '10:41:22', tries: 1 },
  { id: 'evt_9f2c3d', event: 'evidence.recorded', target: 'Moodle — UB', code: 200, ms: 211, at: '10:40:09', tries: 1 },
  { id: 'evt_9f2c3c', event: 'attempt.started', target: 'Workable', code: 429, ms: 62, at: '10:39:55', tries: 2 },
]

export const skills = [
  { name: 'Concurrency & parallelism', level: 82, ci: 6, n: 41, sev: 'ok' as Severity },
  { name: 'API design', level: 74, ci: 8, n: 28, sev: 'ok' as Severity },
  { name: 'Data modelling', level: 61, ci: 7, n: 33, sev: 'ok' as Severity },
  { name: 'Security fundamentals', level: 38, ci: 11, n: 12, sev: 'warning' as Severity },
  { name: 'Observability', level: 24, ci: 14, n: 6, sev: 'critical' as Severity },
]

export const evidenceSources = [
  { ctx: 'Certify', label: 'LSP TIK — Backend L3', when: 'Sep 2026', weight: 'High stakes · proctored' },
  { ctx: 'Campus', label: 'Univ. Brawijaya — Sistem Terdistribusi', when: 'Jun 2026', weight: 'Course exam' },
  { ctx: 'Hire', label: 'PT Nusantara Digital — screen', when: 'Mar 2026', weight: 'AI-in-the-loop task' },
]

export const itemTemplate = {
  name: 'Buffered channel saturation',
  skill: 'Concurrency & parallelism',
  variables: [
    { name: 'rps', domain: '8000 … 20000, step 1000', sample: '12000' },
    { name: 'cap', domain: '256 | 512 | 1024 | 2048', sample: '1024' },
    { name: 'drain', domain: '0.5×rps … 0.9×rps', sample: '9000' },
    { name: 'window', domain: '10 … 60 s', sample: '30' },
  ],
  bank: 'No static bank — 7,392 valid instantiations',
  calibration: { p: 0.62, rpb: 0.41, n: 1_284, method: 'CTT (IRT at n≥500/variant)' },
}

export const tiles = [
  { label: 'Attempts today', value: '1,284', delta: +12.4, spark: [38, 41, 39, 47, 52, 49, 58, 61, 57, 66, 71, 78] },
  { label: 'Integrity flags', value: '37', delta: -8.1, goodWhenDown: true, spark: [12, 14, 11, 15, 13, 10, 12, 9, 11, 8, 9, 7] },
  { label: 'Median sync lag', value: '1.4s', delta: -3.2, goodWhenDown: true, spark: [22, 21, 24, 20, 19, 21, 18, 17, 19, 16, 15, 14] },
  { label: 'Webhook success', value: '99.2%', delta: +0.3, spark: [96, 97, 97, 98, 97, 98, 99, 98, 99, 99, 99, 99] },
]
