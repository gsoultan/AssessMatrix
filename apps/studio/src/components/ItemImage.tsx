import { useState } from 'react'
import { Modal } from '@mantine/core'
import { IconZoomIn } from '@tabler/icons-react'
import type { Media } from '../media'

/**
 * Figure display for candidates and previews. Tap to enlarge — on a phone a
 * circuit diagram or a chart is unreadable at column width, and a participant
 * who cannot read the figure cannot answer the question.
 */
export function ItemImage({ media, max = 320, className = '' }: { media: Media; max?: number; className?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className={`group relative block overflow-hidden rounded-lg border border-hair ${className}`}
        aria-label={media.alt ? `Enlarge: ${media.alt}` : 'Enlarge image'}
      >
        <img
          src={media.url}
          alt={media.alt}
          className="block h-auto w-full object-contain"
          style={{ maxHeight: max, background: 'var(--color-sunken)' }}
        />
        <span
          className="absolute bottom-1.5 right-1.5 grid h-6 w-6 place-items-center rounded-md opacity-0 transition-opacity group-hover:opacity-100"
          style={{ background: 'rgb(0 0 0 / 0.55)' }}
        >
          <IconZoomIn size={13} stroke={2} color="#fff" />
        </span>
      </button>
      <Modal opened={open} onClose={() => setOpen(false)} size="auto" centered title={media.alt || 'Figure'}>
        <img src={media.url} alt={media.alt} className="block max-h-[78vh] w-auto max-w-full object-contain" />
      </Modal>
    </>
  )
}
