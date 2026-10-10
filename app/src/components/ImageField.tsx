import { useRef, useState } from 'react'
import { TextInput, Tooltip } from '@mantine/core'
import { IconPhotoPlus, IconTrash, IconReplace, IconAlertTriangle, IconLoader2 } from '@tabler/icons-react'
import { type Media, ingestImage, MediaError, prettyBytes } from '../media'

/**
 * Author-side image attachment. Deliberately asks for alt text every time:
 * an assessment item whose figure has no text alternative is inaccessible to a
 * screen-reader candidate, and in high-stakes testing that is an accommodation
 * failure with legal weight — not a lint warning.
 */
export function ImageField({
  value,
  onChange,
  compact = false,
  label = 'Add image',
}: {
  value?: Media
  onChange: (m: Media | undefined) => void
  compact?: boolean
  label?: string
}) {
  const input = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [over, setOver] = useState(false)

  const take = async (file: File | undefined) => {
    if (!file) return
    setErr('')
    setBusy(true)
    try {
      onChange(await ingestImage(file, value?.alt ?? ''))
    } catch (e) {
      setErr(e instanceof MediaError ? e.message : 'Could not attach that image.')
    } finally {
      setBusy(false)
      if (input.current) input.current.value = ''
    }
  }

  const picker = (
    <input
      ref={input}
      type="file"
      accept="image/*"
      className="sr-only"
      aria-label={label}
      onChange={(e) => void take(e.currentTarget.files?.[0])}
    />
  )

  if (!value)
    return (
      <div>
        <button
          onClick={() => input.current?.click()}
          onDragOver={(e) => {
            e.preventDefault()
            setOver(true)
          }}
          onDragLeave={() => setOver(false)}
          onDrop={(e) => {
            e.preventDefault()
            setOver(false)
            void take(e.dataTransfer.files?.[0])
          }}
          disabled={busy}
          className={`flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed text-[12px] font-medium ${
            compact ? 'py-2' : 'py-4'
          }`}
          style={{
            borderColor: over ? 'var(--color-s1)' : 'var(--color-rule)',
            background: over ? 'var(--color-wash)' : 'transparent',
            color: 'var(--color-ink-muted)',
          }}
        >
          {busy ? (
            <IconLoader2 size={14} stroke={2} className="animate-spin" />
          ) : (
            <IconPhotoPlus size={14} stroke={1.8} />
          )}
          {busy ? 'Processing…' : label}
        </button>
        {picker}
        {err && (
          <p className="mt-1 text-[11.5px] leading-snug" style={{ color: 'var(--color-critical)' }}>
            {err}
          </p>
        )}
      </div>
    )

  const noAlt = !value.alt.trim()

  return (
    <div className="rounded-lg border border-hair p-2">
      <div className="flex items-start gap-2.5">
        <img
          src={value.url}
          alt={value.alt}
          className="shrink-0 rounded-md object-cover"
          style={{ width: compact ? 56 : 84, height: compact ? 56 : 84, background: 'var(--color-sunken)' }}
        />
        <div className="min-w-0 flex-1">
          <TextInput
            size="xs"
            aria-label="Image description (alt text)"
            placeholder="Describe the image for screen readers"
            value={value.alt}
            onChange={(e) => onChange({ ...value, alt: e.currentTarget.value })}
            error={noAlt ? ' ' : undefined}
          />
          <p className="tnum mt-1 text-[11px] text-ink-muted">
            {value.w}×{value.h} · {prettyBytes(value.bytes)}
          </p>
          {noAlt && (
            <p className="mt-1 flex items-start gap-1 text-[11px] leading-snug" style={{ color: 'var(--color-serious)' }}>
              <IconAlertTriangle size={11} stroke={2.2} className="mt-px shrink-0" />
              Needed for screen readers. Describe the figure without giving the answer away.
            </p>
          )}
        </div>
        <div className="flex shrink-0 flex-col gap-1">
          <Tooltip label="Replace" withArrow>
            <button
              onClick={() => input.current?.click()}
              aria-label="Replace image"
              className="hair grid h-6 w-6 place-items-center rounded-md bg-surface"
            >
              <IconReplace size={12} stroke={1.9} className="text-ink-2" />
            </button>
          </Tooltip>
          <Tooltip label="Remove" withArrow>
            <button
              onClick={() => onChange(undefined)}
              aria-label="Remove image"
              className="hair grid h-6 w-6 place-items-center rounded-md bg-surface"
            >
              <IconTrash size={12} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
            </button>
          </Tooltip>
        </div>
      </div>
      {picker}
      {err && (
        <p className="mt-1 text-[11.5px]" style={{ color: 'var(--color-critical)' }}>
          {err}
        </p>
      )}
    </div>
  )
}
