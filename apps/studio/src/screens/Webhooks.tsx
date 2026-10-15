/**
 * Webhooks against the core: the organisation's own systems hear about
 * publishes, invitations, starts, hand-ins, scores and credentials as they
 * happen. Each request is signed; every delivery is logged and can be sent again.
 */
import { useCallback, useEffect, useState } from 'react'
import { Badge, Checkbox, Select, TextInput } from '@mantine/core'
import { DeliveryState, type WebhookDelivery, type WebhookEndpoint } from '@assessmatrix/proto/webhook_pb'
import { Panel } from '../components/Panel'
import { api } from '../api/client'
import { explain } from '../backend/repo'

const when = (ms: bigint) => (ms ? new Date(Number(ms)).toLocaleString() : '—')

const stateBadge: Record<DeliveryState, { label: string; color: string }> = {
  [DeliveryState.UNSPECIFIED]: { label: '—', color: 'gray' },
  [DeliveryState.PENDING]: { label: 'Pending', color: 'yellow' },
  [DeliveryState.DELIVERED]: { label: 'Delivered', color: 'green' },
  [DeliveryState.FAILED]: { label: 'Failed', color: 'red' },
}

function Problem({ text }: { text: string }) {
  if (!text) return null
  return (
    <p role="alert" className="mt-2 text-[12.5px]" style={{ color: 'var(--color-critical)' }}>
      {text}
    </p>
  )
}

const primary = 'h-9 rounded-lg px-4 text-[13px] font-medium text-white disabled:opacity-50'
const quiet = 'hair rounded-md bg-surface px-2.5 py-1 text-[12px] font-medium text-ink-2 disabled:opacity-50'

export function Webhooks() {
  const [endpoints, setEndpoints] = useState<WebhookEndpoint[] | null>(null)
  const [types, setTypes] = useState<string[]>([])
  const [error, setError] = useState('')
  const [secret, setSecret] = useState<{ url: string; value: string } | null>(null)
  const [adding, setAdding] = useState(false)
  const [tick, setTick] = useState(0)
  const load = useCallback(() => {
    api.webhooks.listWebhooks({}).then(
      (r) => {
        setEndpoints(r.endpoints)
        setTypes(r.eventTypes)
      },
      (err) => setError(explain(err)),
    )
    setTick((t) => t + 1)
  }, [])
  useEffect(load, [load])

  return (
    <div className="p-4 sm:p-6">
      <header className="mb-5">
        <h1 className="text-[15px] font-semibold tracking-tight">Webhooks</h1>
        <p className="mt-0.5 max-w-[72ch] text-[12.5px] leading-snug text-ink-muted">
          Tell your own systems when an exam is published, someone is invited, starts, hands in, is scored or earns a credential.
          Payloads carry ids, never names or emails. Each request is signed with the webhook's secret.
        </p>
      </header>

      {secret && (
        <section role="status" className="mb-4 rounded-xl p-4" style={{ background: 'var(--color-wash-warn)' }}>
          <p className="text-[13px] font-semibold text-ink">Copy this secret now. It is not shown again.</p>
          <p className="mt-0.5 text-[12px] text-ink-2">For {secret.url}. Verify the AssessMatrix-Signature header with it.</p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <code className="tnum break-all rounded bg-surface px-2 py-1 text-[12.5px]">{secret.value}</code>
            <button className={quiet} onClick={() => void navigator.clipboard?.writeText(secret.value)}>
              Copy
            </button>
            <button className={quiet} onClick={() => setSecret(null)}>
              Done
            </button>
          </div>
        </section>
      )}

      <Panel
        title="Endpoints"
        hint="Up to 10. A failing delivery is retried for about four hours, then kept in the log below to send again."
        actions={
          !adding && (
            <button className={primary} style={{ background: 'var(--color-s1)' }} onClick={() => setAdding(true)}>
              Add webhook
            </button>
          )
        }
        className="mb-4"
      >
        {adding && (
          <EndpointForm
            types={types}
            onCancel={() => setAdding(false)}
            onSaved={(url, value) => {
              setAdding(false)
              if (value) setSecret({ url, value })
              load()
            }}
          />
        )}
        <Problem text={error} />
        {!endpoints ? (
          <p className="text-[12.5px] text-ink-muted">Loading…</p>
        ) : endpoints.length === 0 ? (
          !adding && <p className="text-[12.5px] text-ink-muted">No webhooks yet. Add one to start sending events.</p>
        ) : (
          <ul aria-label="Webhooks" className="flex flex-col gap-3">
            {endpoints.map((e) => (
              <Endpoint key={e.id} endpoint={e} types={types} onChanged={load} onSecret={(value) => setSecret({ url: e.url, value })} />
            ))}
          </ul>
        )}
      </Panel>

      <Deliveries endpoints={endpoints ?? []} tick={tick} onRefresh={load} />
    </div>
  )
}

function EndpointForm({
  types,
  endpoint,
  onCancel,
  onSaved,
}: {
  types: string[]
  endpoint?: WebhookEndpoint
  onCancel: () => void
  onSaved: (url: string, secret?: string) => void
}) {
  const [url, setUrl] = useState(endpoint?.url ?? 'https://')
  const [description, setDescription] = useState(endpoint?.description ?? '')
  const [events, setEvents] = useState<string[]>(endpoint ? [...endpoint.events] : types)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const save = async () => {
    setBusy(true)
    setError('')
    try {
      if (endpoint) {
        await api.webhooks.updateWebhook({ id: endpoint.id, url, description, events, active: endpoint.active })
        onSaved(url)
      } else {
        const r = await api.webhooks.createWebhook({ url, description, events })
        onSaved(url, r.secret)
      }
    } catch (err) {
      setError(explain(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      className="mb-4 rounded-xl border border-hair p-4"
      onSubmit={(ev) => {
        ev.preventDefault()
        void save()
      }}
    >
      <div className="flex flex-wrap gap-3">
        <TextInput label="Receiver URL" required type="url" value={url} onChange={(e) => setUrl(e.currentTarget.value)} className="min-w-[280px] flex-[2]" />
        <TextInput label="Description" maxLength={200} value={description} onChange={(e) => setDescription(e.currentTarget.value)} className="min-w-[200px] flex-1" />
      </div>
      <Checkbox.Group label="Events" value={events} onChange={setEvents} className="mt-3">
        <div className="mt-1.5 grid gap-1.5 sm:grid-cols-2 xl:grid-cols-4">
          {types.map((t) => (
            <Checkbox key={t} value={t} label={<span className="tnum text-[12.5px]">{t}</span>} />
          ))}
        </div>
      </Checkbox.Group>
      <Problem text={error} />
      <div className="mt-3 flex gap-2">
        <button type="submit" disabled={busy || events.length === 0} className={primary} style={{ background: 'var(--color-s1)' }}>
          {endpoint ? 'Save' : 'Add webhook'}
        </button>
        <button type="button" className={quiet} onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  )
}

function Endpoint({
  endpoint: e,
  types,
  onChanged,
  onSecret,
}: {
  endpoint: WebhookEndpoint
  types: string[]
  onChanged: () => void
  onSecret: (secret: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const run = async (fn: () => Promise<unknown>, done?: string) => {
    setBusy(true)
    setError('')
    setNote('')
    try {
      await fn()
      if (done) setNote(done)
      onChanged()
    } catch (err) {
      setError(explain(err))
    } finally {
      setBusy(false)
    }
  }
  if (editing)
    return (
      <li>
        <EndpointForm types={types} endpoint={e} onCancel={() => setEditing(false)} onSaved={() => (setEditing(false), onChanged())} />
      </li>
    )
  return (
    <li className="rounded-xl border border-hair p-4" aria-label={e.url}>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="tnum break-all text-[13px] font-medium text-ink">{e.url}</p>
          <p className="text-[12px] text-ink-muted">
            {e.description || 'No description'} · secret {e.secretHint}… · {e.delivered} delivered
            {e.failing > 0 && <span style={{ color: 'var(--color-critical)' }}> · {e.failing} failed</span>} · last {when(e.lastDeliveryAt)}
          </p>
        </div>
        <Badge size="sm" variant="light" color={e.active ? 'green' : 'gray'}>
          {e.active ? 'Active' : 'Paused'}
        </Badge>
      </div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {e.events.map((t) => (
          <Badge key={t} size="xs" variant="default" radius="sm" tt="none" className="tnum">
            {t}
          </Badge>
        ))}
      </div>
      <div className="mt-3 flex flex-wrap gap-2">
        <button className={quiet} disabled={busy || !e.active} onClick={() => run(() => api.webhooks.pingWebhook({ id: e.id }), 'Test event queued; see Deliveries.')}>
          Send test event
        </button>
        <button className={quiet} disabled={busy} onClick={() => setEditing(true)}>
          Edit
        </button>
        <button
          className={quiet}
          disabled={busy}
          onClick={() => run(() => api.webhooks.updateWebhook({ id: e.id, url: e.url, description: e.description, events: e.events, active: !e.active }))}
        >
          {e.active ? 'Pause' : 'Resume'}
        </button>
        <button className={quiet} disabled={busy} onClick={() => run(async () => onSecret((await api.webhooks.rotateWebhookSecret({ id: e.id })).secret))}>
          New secret
        </button>
        {confirmDelete ? (
          <button
            className={quiet}
            style={{ color: 'var(--color-critical)' }}
            disabled={busy}
            onClick={() => run(() => api.webhooks.deleteWebhook({ id: e.id }))}
          >
            Delete it and its log
          </button>
        ) : (
          <button className={quiet} disabled={busy} onClick={() => setConfirmDelete(true)}>
            Delete
          </button>
        )}
      </div>
      {note && (
        <p role="status" className="mt-2 text-[12.5px] text-ink-2">
          {note}
        </p>
      )}
      <Problem text={error} />
    </li>
  )
}

function Deliveries({ endpoints, tick, onRefresh }: { endpoints: WebhookEndpoint[]; tick: number; onRefresh: () => void }) {
  const [endpointId, setEndpointId] = useState('')
  const [failedOnly, setFailedOnly] = useState(false)
  const [rows, setRows] = useState<WebhookDelivery[] | null>(null)
  const [error, setError] = useState('')
  const load = useCallback(() => {
    api.webhooks.listDeliveries({ endpointId, failedOnly, limit: 100 }).then(
      (r) => setRows(r.deliveries),
      (err) => setError(explain(err)),
    )
  }, [endpointId, failedOnly])
  useEffect(load, [load, tick])
  const urlOf = (id: string) => endpoints.find((e) => e.id === id)?.url ?? 'deleted'
  const redeliver = async (id: string) => {
    setError('')
    try {
      await api.webhooks.redeliver({ deliveryId: id })
      onRefresh()
    } catch (err) {
      setError(explain(err))
    }
  }
  return (
    <Panel
      title="Deliveries"
      hint="Kept 30 days. A redelivery sends the same event id and body, so a receiver that de-duplicates on AssessMatrix-Event-Id is safe."
      actions={
        <button className={quiet} onClick={onRefresh}>
          Refresh
        </button>
      }
      pad={false}
    >
      <div className="flex flex-wrap items-end gap-3 px-4 pt-3 sm:px-5">
        <Select
          label="Webhook"
          className="w-full max-w-[360px]"
          data={[{ value: '', label: 'All webhooks' }, ...endpoints.map((e) => ({ value: e.id, label: e.url }))]}
          value={endpointId}
          onChange={(v) => setEndpointId(v ?? '')}
          allowDeselect={false}
        />
        <Checkbox label="Failed only" checked={failedOnly} onChange={(e) => setFailedOnly(e.currentTarget.checked)} className="pb-2" />
      </div>
      <div className="px-4 sm:px-5">
        <Problem text={error} />
      </div>
      {!rows ? (
        <p className="p-4 text-[12.5px] text-ink-muted sm:px-5">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="p-4 text-[12.5px] text-ink-muted sm:px-5">No deliveries yet.</p>
      ) : (
        <div className="scroll-thin mt-3 overflow-x-auto">
          <table className="w-full min-w-[720px] text-left">
            <thead>
              <tr className="border-b border-hair text-[11px] uppercase tracking-wider text-ink-muted">
                <th className="px-4 py-2 font-medium sm:px-5">Event</th>
                <th className="px-3 py-2 font-medium">Webhook</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2 text-right font-medium">Tries</th>
                <th className="px-4 py-2 sm:px-5">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((d) => {
                const s = stateBadge[d.state]
                return (
                  <tr key={d.id} className="border-b border-hair/60 align-top last:border-0">
                    <td className="px-4 py-3 sm:px-5">
                      <p className="tnum text-[12.5px] font-medium text-ink">{d.eventType}</p>
                      <p className="tnum text-[11px] text-ink-muted">{when(d.createdAt)}</p>
                      <details className="mt-1">
                        <summary className="cursor-pointer text-[11.5px] text-ink-muted">Payload</summary>
                        <pre className="mt-1 max-w-[52ch] overflow-x-auto whitespace-pre-wrap break-all rounded bg-[var(--color-wash)] p-2 text-[11px]">
                          {d.payload}
                        </pre>
                      </details>
                    </td>
                    <td className="tnum max-w-[260px] break-all px-3 py-3 text-[12px] text-ink-2">{urlOf(d.endpointId)}</td>
                    <td className="px-3 py-3">
                      <Badge size="sm" variant="light" color={s.color}>
                        {s.label}
                        {d.lastStatus ? ` · ${d.lastStatus}` : ''}
                      </Badge>
                      {d.lastError && <p className="mt-1 max-w-[32ch] text-[11.5px] text-ink-muted">{d.lastError}</p>}
                    </td>
                    <td className="tnum px-3 py-3 text-right text-[12.5px] text-ink-2">{d.tries}</td>
                    <td className="px-4 py-3 text-right sm:px-5">
                      {d.state !== DeliveryState.PENDING && (
                        <button className={quiet} onClick={() => void redeliver(d.id)}>
                          Send again
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  )
}
