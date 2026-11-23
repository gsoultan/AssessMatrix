/**
 * Updates: which version this server runs, whether a newer release exists,
 * and updating to it. Super administrators only; the core refuses anyone else.
 *
 * The server never replaces itself. Pressing Update records which release;
 * the updater on the host backs up, installs, checks the new version is
 * ready and goes back if it is not (docs/13 §2). Where no updater is
 * connected (Kubernetes, or one not installed yet) this shows the steps.
 */
import { useCallback, useEffect, useState } from 'react'
import { Badge } from '@mantine/core'
import { UpdateState, type GetUpdateStatusResponse, type Release, type UpdateRun } from '@assessmatrix/proto/system_pb'
import { Panel, Label } from '../components/Panel'
import { api } from '../api/client'
import { explain } from '../backend/repo'

const when = (s: bigint) => (s ? new Date(Number(s) * 1000).toLocaleString() : '—')

const stateBadge: Record<UpdateState, { label: string; color: string }> = {
  [UpdateState.UNSPECIFIED]: { label: '—', color: 'gray' },
  [UpdateState.REQUESTED]: { label: 'Waiting', color: 'yellow' },
  [UpdateState.RUNNING]: { label: 'Updating', color: 'blue' },
  [UpdateState.SUCCEEDED]: { label: 'Updated', color: 'green' },
  [UpdateState.FAILED]: { label: 'Failed', color: 'red' },
  [UpdateState.ROLLED_BACK]: { label: 'Rolled back', color: 'orange' },
  [UpdateState.CANCELLED]: { label: 'Cancelled', color: 'gray' },
}

const active = (r?: UpdateRun) => !!r && (r.state === UpdateState.REQUESTED || r.state === UpdateState.RUNNING)

const primary = 'h-9 rounded-lg px-4 text-[13px] font-medium text-white disabled:opacity-50'
const quiet = 'hair rounded-md bg-surface px-2.5 py-1 text-[12px] font-medium text-ink-2 disabled:opacity-50'

function Problem({ text }: { text: string }) {
  if (!text) return null
  return (
    <p role="alert" className="mt-2 text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
      {text}
    </p>
  )
}

export function Updates() {
  const [status, setStatus] = useState<GetUpdateStatusResponse | null>(null)
  const [error, setError] = useState('')
  const [checking, setChecking] = useState(false)
  const load = useCallback(async (refresh = false) => {
    try {
      setStatus(await api.system.getUpdateStatus({ refresh }))
      setError('')
    } catch (err) {
      setError(explain(err))
    }
  }, [])
  useEffect(() => {
    void load()
  }, [load])

  // While an update waits or runs, follow it.
  const current = status?.runs[0]
  const following = active(current)
  useEffect(() => {
    if (!following) return
    const t = setInterval(() => void load(), 5000)
    return () => clearInterval(t)
  }, [following, load])

  const check = async () => {
    setChecking(true)
    await load(true)
    setChecking(false)
  }

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5">
        <h1 className="text-[15px] font-semibold tracking-tight">Updates</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          New versions of AssessMatrix for this server. Updating backs the database up first, waits until no one is sitting an exam,
          and goes back to this version by itself if the new one does not start.
        </p>
      </header>
      <Problem text={error} />
      {!status ? (
        !error && <p className="text-[12.5px] text-ink-muted">Loading…</p>
      ) : (
        <>
          <Panel
            title="This server"
            actions={
              status.checksEnabled && (
                <button className={quiet} onClick={() => void check()} disabled={checking}>
                  {checking ? 'Checking…' : 'Check now'}
                </button>
              )
            }
            className="mb-4"
          >
            <dl className="grid grid-cols-1 gap-3 text-[13px] sm:grid-cols-3">
              <div>
                <Label>Version</Label>
                <dd className="tnum mt-0.5 font-medium text-ink">{status.currentVersion}</dd>
              </div>
              <div>
                <Label>Database</Label>
                <dd className="tnum mt-0.5 break-all text-ink-2">{status.schemaVersion.replace(/\.sql$/, '') || '—'}</dd>
              </div>
              <div>
                <Label>Last checked</Label>
                <dd className="tnum mt-0.5 text-ink-2">{status.checksEnabled ? when(status.checkedAt) : 'Checks are off'}</dd>
              </div>
            </dl>
            {status.checkError && (
              <p className="mt-3 text-[12.5px] text-ink-muted">
                The last check could not reach the release feed ({status.checkError}). It tries again on its own.
              </p>
            )}
          </Panel>

          <Available status={status} onChanged={() => void load()} />

          {status.runs.length > 0 && (
            <Panel title="History" hint="The last ten updates asked for on this server." className="mt-4">
              <ul aria-label="Updates" className="flex flex-col divide-y divide-hair">
                {status.runs.map((r) => (
                  <li key={r.id} className="flex flex-col gap-1 py-2.5 first:pt-0 last:pb-0 sm:flex-row sm:items-start sm:gap-3">
                    <Badge color={stateBadge[r.state].color} variant="light" className="shrink-0">
                      {stateBadge[r.state].label}
                    </Badge>
                    <div className="min-w-0 flex-1 text-[12.5px]">
                      <p className="tnum font-medium text-ink">
                        {r.fromVersion} → {r.targetVersion}
                      </p>
                      {r.message && <p className="mt-0.5 text-ink-2">{r.message}</p>}
                      <p className="mt-0.5 text-ink-muted">
                        Asked by {r.requestedBy} on {when(r.requestedAt)}
                        {r.finishedAt ? ` · finished ${when(r.finishedAt)}` : ''}
                      </p>
                    </div>
                  </li>
                ))}
              </ul>
            </Panel>
          )}
        </>
      )}
    </div>
  )
}

function ReleaseNotes({ release }: { release: Release }) {
  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <p className="tnum text-[14px] font-semibold text-ink">{release.version}</p>
        {release.kind && (
          <Badge variant="light" color={release.kind === 'major' ? 'orange' : 'gray'}>
            {release.kind === 'patch' ? 'Fixes' : release.kind === 'minor' ? 'New features' : 'Major release'}
          </Badge>
        )}
        {release.security && (
          <Badge variant="light" color="red">
            Security fix
          </Badge>
        )}
        {release.publishedAt > 0n && <span className="text-[12px] text-ink-muted">Released {when(release.publishedAt)}</span>}
      </div>
      {release.notes && (
        <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded-lg bg-ink/[0.03] p-3 font-sans text-[12.5px] leading-relaxed text-ink-2">
          {release.notes}
        </pre>
      )}
      {release.notesUrl && (
        <a href={release.notesUrl} target="_blank" rel="noreferrer" className="mt-2 inline-block text-[12.5px] font-medium" style={{ color: 'var(--color-s1)' }}>
          Full release notes
        </a>
      )}
    </div>
  )
}

function Available({ status, onChanged }: { status: GetUpdateStatusResponse; onChanged: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmNow, setConfirmNow] = useState(false)
  const run = status.runs[0]
  const next = status.next

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await fn()
      setConfirmNow(false)
      onChanged()
    } catch (err) {
      setError(explain(err))
    } finally {
      setBusy(false)
    }
  }

  if (!status.checksEnabled)
    return (
      <Panel title="New versions">
        <p className="text-[12.5px] text-ink-muted">
          This server does not look for new versions: it is a development build, or update checks are turned off (AM_UPDATE_CHECK=off).
        </p>
      </Panel>
    )

  if (active(run))
    return (
      <Panel title={run!.state === UpdateState.RUNNING ? `Updating to ${run!.targetVersion}` : `Update to ${run!.targetVersion} is waiting`}>
        <p role="status" className="text-[13px] text-ink">
          {run!.state === UpdateState.RUNNING
            ? run!.message || 'Started.'
            : run!.waitForIdle && status.sittingsInProgress > 0n
              ? `It starts once no one is sitting an exam (${status.sittingsInProgress} in progress now).`
              : status.updaterConnected
                ? 'It starts within a minute.'
                : 'It starts when the updater on the server next runs. None has checked in for a while.'}
        </p>
        <p className="mt-1 text-[12px] text-ink-muted">
          The studio may stop answering for a few seconds while the server restarts. This page follows along.
        </p>
        {run!.state === UpdateState.REQUESTED && (
          <button className={`${quiet} mt-3`} disabled={busy} onClick={() => void act(() => api.system.cancelUpdate({}))}>
            Cancel the update
          </button>
        )}
        <Problem text={error} />
      </Panel>
    )

  if (!next)
    return (
      <Panel title="New versions">
        <p className="text-[13px] text-ink">
          {status.checkedAt ? 'This server is up to date.' : 'Not checked yet. Press Check now.'}
        </p>
      </Panel>
    )

  const viaStep = status.latest && status.latest.version !== next.version
  return (
    <Panel
      title="A new version is available"
      hint={viaStep ? `${status.latest!.version} is the newest. This server gets there through ${next.version} first, then it is offered here.` : undefined}
    >
      <ReleaseNotes release={next} />
      <div className="mt-4 border-t border-hair pt-4">
        {status.updaterConnected ? (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <button
                className={primary}
                style={{ background: 'var(--color-s1)' }}
                disabled={busy}
                onClick={() => void act(() => api.system.requestUpdate({ version: next.version, now: false }))}
              >
                Update to {next.version}
              </button>
              {!confirmNow ? (
                <button className={quiet} disabled={busy} onClick={() => setConfirmNow(true)}>
                  Update now, without waiting
                </button>
              ) : (
                <span className="flex flex-wrap items-center gap-2">
                  <button
                    className={quiet}
                    style={{ color: 'var(--color-critical)' }}
                    disabled={busy}
                    onClick={() => void act(() => api.system.requestUpdate({ version: next.version, now: true }))}
                  >
                    Yes, update now
                  </button>
                  <button className={quiet} disabled={busy} onClick={() => setConfirmNow(false)}>
                    Keep waiting
                  </button>
                </span>
              )}
            </div>
            <p className="mt-2 text-[12px] leading-snug text-ink-muted">
              {status.sittingsInProgress > 0n
                ? `${status.sittingsInProgress} exam sitting${status.sittingsInProgress === 1n ? ' is' : 's are'} in progress. The update waits until they end.`
                : 'No one is sitting an exam, so it starts within a minute.'}{' '}
              {confirmNow &&
                'Updating now restarts the server during sittings: candidates keep every answer, but their clock runs through the few seconds it takes.'}
            </p>
          </>
        ) : (
          <ManualSteps version={next.version} />
        )}
        <Problem text={error} />
      </div>
    </Panel>
  )
}

function ManualSteps({ version }: { version: string }) {
  const tag = version.replace(/^v/, '')
  return (
    <div className="text-[12.5px] leading-relaxed text-ink-2">
      <p className="font-medium text-ink">No updater is connected to this server, so there is no Update button here.</p>
      <p className="mt-1">
        On one machine with Docker Compose, install the updater once (operations guide, Updates) and the button appears. To update by hand
        instead, back up first, then in the install directory:
      </p>
      <pre className="mt-1 overflow-auto rounded-lg bg-ink/[0.03] p-3 text-[12px]">
        {`make backup
# in .env: AM_IMAGE=ghcr.io/gsoultan/assessmatrix:${tag}
docker compose pull core && docker compose run --rm migrate && docker compose up -d core`}
      </pre>
      <p className="mt-2">On Kubernetes, roll out {version} the usual way: the migrate Job first, then the Deployments.</p>
    </div>
  )
}
