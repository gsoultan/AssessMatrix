import { useRef, useState } from 'react'
import { Textarea, TextInput } from '@mantine/core'
import {
  IconCheck,
  IconPhotoPlus,
  IconTrash,
  IconUserEdit,
} from '@tabler/icons-react'
import { type Media, ingestImage, MediaError, prettyBytes } from '../media'
import { type Attempt, type Item, isScale } from '../store'

/** Candidate attachment. No alt-text prompt: this is the participant's own work
    going to a human reviewer, not published content they are authoring. */
function AnswerImage({ value, onChange }: { value?: Media; onChange: (m: Media | undefined) => void }) {
  const input = useRef<HTMLInputElement>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const take = async (f: File | undefined) => {
    if (!f) return
    setErr('')
    setBusy(true)
    try {
      onChange(await ingestImage(f, 'Lampiran jawaban peserta'))
    } catch (e) {
      setErr(e instanceof MediaError ? e.message : 'Gagal melampirkan gambar.')
    } finally {
      setBusy(false)
      if (input.current) input.current.value = ''
    }
  }

  return (
    <div className="mt-2.5">
      {value ? (
        <div className="flex items-center gap-2.5 rounded-lg border border-hair p-2">
          <img src={value.url} alt="" className="h-12 w-12 shrink-0 rounded-md object-cover" />
          <span className="tnum min-w-0 flex-1 text-[11.5px] text-ink-muted">
            {value.w}×{value.h} · {prettyBytes(value.bytes)}
          </span>
          <button onClick={() => onChange(undefined)} aria-label="Hapus lampiran" className="shrink-0">
            <IconTrash size={14} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
          </button>
        </div>
      ) : (
        <button
          onClick={() => input.current?.click()}
          disabled={busy}
          className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-rule py-2.5 text-[12px] font-medium text-ink-muted"
        >
          <IconPhotoPlus size={14} stroke={1.8} />
          {busy ? 'Memproses…' : 'Lampirkan gambar'}
        </button>
      )}
      <input
        ref={input}
        type="file"
        accept="image/*"
        className="sr-only"
        aria-label="Lampirkan gambar"
        onChange={(e) => void take(e.currentTarget.files?.[0])}
      />
      {err && (
        <p className="mt-1 text-[11.5px]" style={{ color: 'var(--color-critical)' }}>
          {err}
        </p>
      )}
    </div>
  )
}

export const mmss = (s: number) => `${Math.floor(Math.max(0, s) / 60)}:${String(Math.max(0, s) % 60).padStart(2, '0')}`

/* ── the answer surface, shared by mobile and desktop ── */
export function ItemView({
  item,
  attempt,
  onChoice,
  onText,
  onFile,
  dense,
}: {
  item: Item
  attempt: Attempt
  onChoice: (id: string) => void
  onText: (v: string) => void
  onFile: (m: Media | undefined) => void
  dense?: boolean
}) {
  const picked = attempt.answers[item.id] ?? []
  // When every option carries a figure the question is really "pick the right
  // picture" — a text list is the wrong shape for that.
  const imageGrid = item.choices.length > 0 && item.choices.every((c) => c.image)

  if (isScale(item.type) && item.scale) {
    const start = item.type === 'rating' && item.scale.points > 7 ? 0 : 1
    return (
      <div>
        <div className="flex flex-wrap gap-1.5">
          {Array.from({ length: item.scale.points }, (_, i) => {
            const v = String(start + i)
            const on = picked.includes(v)
            return (
              <button
                key={v}
                onClick={() => onChoice(v)}
                aria-pressed={on}
                className={`tnum grid place-items-center rounded-lg text-[13px] font-medium ${
                  dense ? 'h-10 min-w-10 px-2' : 'h-11 min-w-11 px-2.5'
                }`}
                style={
                  on
                    ? { background: 'var(--color-s1)', color: 'var(--color-on-brand)' }
                    : { boxShadow: 'inset 0 0 0 1px var(--color-hair)', color: 'var(--color-ink-2)' }
                }
              >
                {v}
              </button>
            )
          })}
        </div>
        <div className="mt-2 flex justify-between gap-4 text-[11.5px] text-ink-muted">
          <span>{item.scale.minLabel}</span>
          <span className="text-right">{item.scale.maxLabel}</span>
        </div>
      </div>
    )
  }

  if (item.type === 'short')
    return (
      <>
        <TextInput
          size={dense ? 'sm' : 'md'}
          placeholder="Jawaban Anda"
          value={attempt.text[item.id] ?? ''}
          onChange={(e) => onText(e.currentTarget.value)}
        />
        {item.allowAnswerImage && <AnswerImage value={attempt.files[item.id]} onChange={onFile} />}
      </>
    )

  if (item.type === 'essay')
    return (
      <>
        <Textarea
          size={dense ? 'sm' : 'md'}
          autosize
          minRows={dense ? 5 : 7}
          placeholder="Tulis jawaban Anda…"
          value={attempt.text[item.id] ?? ''}
          onChange={(e) => onText(e.currentTarget.value)}
        />
        {item.allowAnswerImage && <AnswerImage value={attempt.files[item.id]} onChange={onFile} />}
        <p className="mt-1.5 flex items-center gap-1.5 text-[11px] text-ink-muted">
          <IconUserEdit size={12} stroke={1.9} /> Dinilai oleh penilai manusia.
        </p>
      </>
    )

  if (imageGrid)
    return (
      <div className="grid grid-cols-2 gap-2.5">
        {item.choices.map((c, n) => {
          const on = picked.includes(c.id)
          return (
            <button
              key={c.id}
              onClick={() => onChoice(c.id)}
              aria-pressed={on}
              className="overflow-hidden rounded-xl border bg-surface text-left"
              style={
                on
                  ? { borderColor: 'transparent', boxShadow: '0 0 0 2px var(--color-s1)' }
                  : { borderColor: 'var(--color-hair)' }
              }
            >
              <img
                src={c.image!.url}
                alt={c.image!.alt}
                className="block aspect-[4/3] w-full object-contain"
                style={{ background: 'var(--color-sunken)' }}
              />
              <span className="flex items-start gap-2 px-2.5 py-2">
                <span
                  className={`mt-px grid h-5 w-5 shrink-0 place-items-center text-[11px] font-semibold ${
                    item.type === 'multiple' ? 'rounded-md' : 'rounded-full'
                  } ${on ? 'text-on-brand' : 'border border-rule text-ink-muted'}`}
                  style={on ? { background: 'var(--color-s1)' } : undefined}
                >
                  {on && item.type === 'multiple' ? <IconCheck size={11} stroke={3} /> : 'ABCDEFGH'[n]}
                </span>
                <span className="text-[12.5px] leading-snug text-ink">{c.text}</span>
              </span>
            </button>
          )
        })}
        {item.type === 'multiple' && (
          <p className="col-span-2 text-[11.5px] text-ink-muted">Pilih semua yang benar.</p>
        )}
      </div>
    )

  return (
    <div className="flex flex-col gap-2">
      {item.choices.map((c, n) => {
        const on = picked.includes(c.id)
        return (
          <button
            key={c.id}
            onClick={() => onChoice(c.id)}
            aria-pressed={on}
            className={`flex w-full items-start gap-3 rounded-xl border px-3.5 text-left transition-colors ${
              dense ? 'py-3' : 'py-3.5'
            } ${on ? 'border-transparent' : 'border-hair bg-surface hover:bg-ink/[0.02]'}`}
            style={on ? { background: 'var(--color-wash)', boxShadow: '0 0 0 1.5px var(--color-s1)' } : undefined}
          >
            <span
              className={`mt-px grid h-5 w-5 shrink-0 place-items-center text-[11px] font-semibold ${
                item.type === 'multiple' ? 'rounded-md' : 'rounded-full'
              } ${on ? 'border-transparent text-on-brand' : 'border border-rule text-ink-muted'}`}
              style={on ? { background: 'var(--color-s1)' } : undefined}
            >
              {on && item.type === 'multiple' ? <IconCheck size={12} stroke={3} /> : 'ABCDEFGH'[n]}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-[14px] leading-snug text-ink">
                {c.text || (!c.image && <em className="text-ink-muted">—</em>)}
              </span>
              {c.image && (
                <img
                  src={c.image.url}
                  alt={c.image.alt}
                  className="mt-2 block max-h-[140px] w-auto rounded-md border border-hair object-contain"
                  style={{ background: 'var(--color-sunken)' }}
                />
              )}
            </span>
          </button>
        )
      })}
      {item.type === 'multiple' && (
        <p className="mt-0.5 text-[11.5px] text-ink-muted">Pilih semua yang benar.</p>
      )}
    </div>
  )
}
