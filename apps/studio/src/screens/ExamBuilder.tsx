import { useEffect, useMemo, useState } from 'react'
import { TextInput, Textarea, NumberInput, Select, Switch, Menu, Collapse, Tooltip, Modal } from '@mantine/core'
import {
  IconPlus,
  IconTrash,
  IconCopy,
  IconCheck,
  IconChevronDown,
  IconArrowUp,
  IconArrowDown,
  IconSettings,
  IconEye,
  IconPencil,
  IconAlertTriangle,
  IconDeviceFloppy,
  IconCircleDot,
  IconListCheck,
  IconToggleLeft,
  IconAbc,
  IconAlignLeft,
  IconDots,
  IconSelector,
  IconArrowLeft,
  IconPhoto,
  IconAdjustmentsHorizontal,
  IconStar,
  IconClipboardText,
  IconBolt,
  IconFileCheck,
  IconRocket,
  IconArchive,
  IconPencilMinus,
} from '@tabler/icons-react'
import { useApp } from '../state'
import { ImageField } from '../components/ImageField'
import { ItemImage } from '../components/ItemImage'
import { prettyBytes } from '../media'
import {
  type Exam,
  type Item,
  type ItemType,
  type Kind,
  KINDS,
  hasUnpublishedVersion,
  isScale,
  isScored,
  ITEM_TYPES,
  TIERS,
  blankItem,
  blankSection,
  totalMinutes,
  totalPoints,
  allItems,
  examMediaBytes,
  missingAlt,
  StorageFullError,
  uid,
} from '../store'

const TYPE_META: Record<ItemType, { Icon: typeof IconCircleDot; label: string; hint: string }> = {
  single: { Icon: IconCircleDot, label: 'Single choice', hint: 'One correct answer.' },
  multiple: { Icon: IconListCheck, label: 'Multiple choice', hint: 'Several correct answers.' },
  boolean: { Icon: IconToggleLeft, label: 'True / false', hint: 'Two fixed options.' },
  short: { Icon: IconAbc, label: 'Short answer', hint: 'Matched against accepted answers.' },
  essay: { Icon: IconAlignLeft, label: 'Essay', hint: 'Scored by a human, not the machine.' },
  likert: { Icon: IconAdjustmentsHorizontal, label: 'Likert scale', hint: 'Agree/disagree, 3–7 steps.' },
  rating: { Icon: IconStar, label: 'Rating', hint: 'A number on a scale, e.g. 0–10.' },
}

const KIND_ICON: Record<Kind, typeof IconCircleDot> = {
  exam: IconFileCheck,
  quiz: IconBolt,
  survey: IconClipboardText,
}

const isComplete = (i: Item, scored: boolean) => {
  if (!i.stem.trim()) return false
  if (i.choices.length > 0 && i.choices.some((c) => !c.text.trim())) return false
  // A questionnaire has no key, so "no correct answer" is the correct state.
  if (!scored || i.type === 'essay' || isScale(i.type)) return true
  return i.correct.length > 0
}

/* ──────────────────────────────────────────────────────────── question card */

function QuestionCard({
  item,
  n,
  scored,
  open,
  first,
  last,
  onOpen,
  onPatch,
  onType,
  onMove,
  onDuplicate,
  onDelete,
}: {
  item: Item
  n: number
  scored: boolean
  open: boolean
  first: boolean
  last: boolean
  onOpen: () => void
  onPatch: (p: Partial<Item>) => void
  onType: (t: ItemType) => void
  onMove: (dir: -1 | 1) => void
  onDuplicate: () => void
  onDelete: () => void
}) {
  const meta = TYPE_META[item.type]
  const good = isComplete(item, scored)
  const multi = item.type === 'multiple'
  // Either the choices are image-based or they are not — showing an upload slot
  // under every option by default is noise on the 90% of items that are text.
  const [choiceImages, setChoiceImages] = useState(item.choices.some((c) => c.image))

  return (
    <article
      className="hair overflow-hidden rounded-xl bg-surface transition-shadow"
      style={open ? { boxShadow: '0 0 0 1.5px var(--color-s1)' } : undefined}
    >
      {/* collapsed row — always visible, doubles as the summary */}
      <button
        onClick={onOpen}
        aria-expanded={open}
        className="flex w-full items-center gap-3 px-3.5 py-3 text-left sm:px-4"
      >
        <span
          className="tnum grid h-7 w-7 shrink-0 place-items-center rounded-lg text-[12px] font-semibold"
          style={{ background: 'var(--color-wash)', color: 'var(--color-brand-text)' }}
        >
          {n}
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5">
            <meta.Icon size={13} stroke={1.8} className="shrink-0 text-ink-muted" />
            <span className="text-[11px] font-medium uppercase tracking-wider text-ink-muted">{meta.label}</span>
            {!good && (
              <span
                className="inline-flex items-center gap-1 rounded px-1.5 text-[10px] font-medium"
                style={{ background: 'var(--color-wash-warn)', color: 'var(--color-serious)' }}
              >
                <IconAlertTriangle size={9} stroke={2.4} /> Incomplete
              </span>
            )}
          </span>
          <span className={`mt-0.5 block truncate text-[13.5px] ${item.stem.trim() ? 'text-ink' : 'text-ink-muted'}`}>
            {item.stem.trim() || 'Untitled question — click to write it'}
          </span>
        </span>
        <span className="tnum hidden shrink-0 text-[11.5px] text-ink-muted sm:block">
          {item.points} pt{item.points === 1 ? '' : 's'}
        </span>
        <IconChevronDown
          size={16}
          stroke={2}
          className={`shrink-0 text-ink-muted transition-transform ${open ? 'rotate-180' : ''}`}
        />
      </button>

      <Collapse expanded={open} keepMounted={false}>
        <div className="border-t border-hair px-3.5 pb-4 pt-4 sm:px-4">
          <Textarea
            label="Question"
            placeholder="What are you asking?"
            autosize
            minRows={2}
            value={item.stem}
            onChange={(e) => onPatch({ stem: e.currentTarget.value })}
          />

          <div className="mt-2">
            <ImageField
              compact
              label="Add a figure to the question"
              value={item.image}
              onChange={(m) => onPatch({ image: m })}
            />
          </div>

          {item.choices.length > 0 && (
            <div className="mt-4">
              <p className="mb-2 text-[12px] font-medium text-ink">
                Answer options
                <span className="ml-1.5 font-normal text-ink-muted">
                  {scored ? ` — mark ${multi ? 'every correct answer' : 'the correct answer'}` : ' — no correct answer in a questionnaire'}
                </span>
              </p>
              <div className="flex flex-col gap-1.5">
                {item.choices.map((c, ci) => {
                  const ok = item.correct.includes(c.id)
                  return (
                    <div
                      key={c.id}
                      className="flex flex-wrap items-start gap-x-2 gap-y-1.5 rounded-lg border px-2 py-1.5"
                      style={
                        ok && scored
                          ? { borderColor: 'transparent', background: 'var(--color-wash-good)', boxShadow: '0 0 0 1px var(--color-good)' }
                          : { borderColor: 'var(--color-hair)' }
                      }
                    >
                      <span className="tnum mt-1.5 w-4 shrink-0 text-center text-[11px] font-semibold text-ink-muted">
                        {'ABCDEFGH'[ci]}
                      </span>
                      {/* Auto-growing, not a single line: options are frequently full
                          sentences, and a field that scrolls them out of view is
                          unusable for authoring. */}
                      <Textarea
                        variant="unstyled"
                        size="sm"
                        autosize
                        minRows={1}
                        className="min-w-0 flex-1 basis-[calc(100%-1.5rem)] sm:basis-auto"
                        placeholder={`Option ${'ABCDEFGH'[ci]}`}
                        value={c.text}
                        disabled={item.type === 'boolean'}
                        onChange={(e) =>
                          onPatch({
                            choices: item.choices.map((x) => (x.id === c.id ? { ...x, text: e.currentTarget.value } : x)),
                          })
                        }
                      />
                      <span className="ml-auto flex shrink-0 items-center gap-2 sm:ml-0">
                      {scored && (
                      <button
                        onClick={() =>
                          onPatch({
                            correct: multi
                              ? ok
                                ? item.correct.filter((x) => x !== c.id)
                                : [...item.correct, c.id]
                              : [c.id],
                          })
                        }
                        aria-pressed={ok}
                        className="inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-[11.5px] font-medium"
                        style={
                          ok
                            ? { background: 'var(--color-good)', color: '#fff' }
                            : { boxShadow: 'inset 0 0 0 1px var(--color-rule)', color: 'var(--color-ink-muted)' }
                        }
                      >
                        <IconCheck size={11} stroke={3} />
                        Correct
                      </button>
                      )}
                      {item.type !== 'boolean' && item.choices.length > 2 && (
                        <button
                          onClick={() =>
                            onPatch({
                              choices: item.choices.filter((x) => x.id !== c.id),
                              correct: item.correct.filter((x) => x !== c.id),
                            })
                          }
                          aria-label={`Remove option ${'ABCDEFGH'[ci]}`}
                          className="shrink-0"
                        >
                          <IconTrash size={13} stroke={1.8} className="text-ink-muted" />
                        </button>
                      )}
                      </span>
                      {choiceImages && (
                        <div className="basis-full pl-6">
                          <ImageField
                            compact
                            label={`Image for option ${'ABCDEFGH'[ci]}`}
                            value={c.image}
                            onChange={(m) =>
                              onPatch({
                                choices: item.choices.map((x) => (x.id === c.id ? { ...x, image: m } : x)),
                              })
                            }
                          />
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1.5">
                {item.type !== 'boolean' && (
                  <button
                    onClick={() => onPatch({ choices: [...item.choices, { id: uid('c'), text: '' }] })}
                    className="inline-flex items-center gap-1 text-[12.5px] font-medium"
                    style={{ color: 'var(--color-s1)' }}
                  >
                    <IconPlus size={13} stroke={2.4} /> Add option
                  </button>
                )}
                <button
                  onClick={() => {
                    if (choiceImages) onPatch({ choices: item.choices.map((c) => ({ ...c, image: undefined })) })
                    setChoiceImages(!choiceImages)
                  }}
                  className="inline-flex items-center gap-1 text-[12.5px] font-medium text-ink-muted"
                >
                  <IconPhoto size={13} stroke={1.9} />
                  {choiceImages ? 'Remove option images' : 'Use images for options'}
                </button>
              </div>
            </div>
          )}

          {isScale(item.type) && item.scale && (
            <div className="mt-4 rounded-lg border border-hair p-3">
              <p className="mb-2.5 text-[12px] font-medium text-ink">Scale</p>
              <div className="grid gap-3 sm:grid-cols-[110px_minmax(0,1fr)_minmax(0,1fr)]">
                <Select
                  size="xs"
                  label="Steps"
                  value={String(item.scale.points)}
                  allowDeselect={false}
                  onChange={(v) => onPatch({ scale: { ...item.scale!, points: Number(v) } })}
                  data={['3', '4', '5', '7', '10', '11'].map((n) => ({ value: n, label: n }))}
                />
                <TextInput
                  size="xs"
                  label="Low end"
                  value={item.scale.minLabel}
                  onChange={(e) => onPatch({ scale: { ...item.scale!, minLabel: e.currentTarget.value } })}
                />
                <TextInput
                  size="xs"
                  label="High end"
                  value={item.scale.maxLabel}
                  onChange={(e) => onPatch({ scale: { ...item.scale!, maxLabel: e.currentTarget.value } })}
                />
              </div>
              <div className="mt-3 flex flex-wrap items-center gap-1.5">
                {Array.from({ length: item.scale.points }, (_, i) => (
                  <span
                    key={i}
                    className="tnum grid h-7 min-w-7 place-items-center rounded-md border border-hair px-1.5 text-[12px] text-ink-2"
                  >
                    {item.type === 'rating' && item.scale!.points > 7 ? i : i + 1}
                  </span>
                ))}
              </div>
            </div>
          )}

          {item.type === 'short' && (
            <Textarea
              className="mt-4"
              label="Accepted answers"
              description="One per line. Matching ignores case and surrounding spaces."
              autosize
              minRows={2}
              placeholder={'lag\nbacklog'}
              value={item.correct.join('\n')}
              onChange={(e) => onPatch({ correct: e.currentTarget.value.split('\n').filter((x) => x.trim()) })}
            />
          )}

          {(item.type === 'short' || item.type === 'essay') && (
            <label className="mt-3 flex items-start gap-2.5 rounded-lg border border-hair p-3">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={!!item.allowAnswerImage}
                onChange={(e) => onPatch({ allowAnswerImage: e.currentTarget.checked })}
              />
              <span>
                <span className="block text-[12.5px] font-medium text-ink">
                  Let the participant attach an image to their answer
                </span>
                <span className="mt-0.5 block text-[11.5px] leading-snug text-ink-muted">
                  For handwritten working, a sketched diagram, or a screenshot. The attachment is
                  part of the submission and goes to the human reviewer with it.
                </span>
              </span>
            </label>
          )}

          {item.type === 'essay' && (
            <div className="mt-4 flex items-start gap-2 rounded-lg p-3" style={{ background: 'var(--color-wash-warn)' }}>
              <IconAlertTriangle size={15} stroke={1.9} className="mt-px shrink-0" style={{ color: 'var(--color-serious)' }} />
              <p className="text-[12px] leading-snug text-ink-2">
                Essays go to a human review queue. Their points are excluded from the auto-scored
                total and reported separately, rather than quietly counted as zero.
              </p>
            </div>
          )}

          <label className="mt-3 flex items-center gap-2.5">
            <input
              type="checkbox"
              checked={!!item.required}
              onChange={(e) => onPatch({ required: e.currentTarget.checked })}
            />
            <span className="text-[12.5px] text-ink-2">Required — the participant cannot submit without answering</span>
          </label>

          {/* per-question controls, at the bottom where they belong */}
          <div className="mt-4 flex flex-wrap items-end gap-3 border-t border-hair pt-3.5">
            <Select
              size="xs"
              label="Type"
              value={item.type}
              onChange={(v) => v && onType(v as ItemType)}
              allowDeselect={false}
              data={ITEM_TYPES.map((t) => ({ value: t.v, label: TYPE_META[t.v].label }))}
              w={158}
            />
            <NumberInput
              size="xs"
              label="Points"
              w={84}
              disabled={!scored}
              min={0}
              value={item.points}
              onChange={(v) => onPatch({ points: Number(v) || 0 })}
            />
            <TextInput
              size="xs"
              label="Skill node"
              placeholder="optional"
              className="min-w-[130px] flex-1"
              value={item.skill}
              onChange={(e) => onPatch({ skill: e.currentTarget.value })}
            />
            <div className="flex items-center gap-1">
              <Tooltip label="Move up" withArrow>
                <button
                  onClick={() => onMove(-1)}
                  disabled={first}
                  aria-label="Move question up"
                  className="hair grid h-7 w-7 place-items-center rounded-md bg-surface disabled:opacity-35"
                >
                  <IconArrowUp size={14} stroke={2} className="text-ink-2" />
                </button>
              </Tooltip>
              <Tooltip label="Move down" withArrow>
                <button
                  onClick={() => onMove(1)}
                  disabled={last}
                  aria-label="Move question down"
                  className="hair grid h-7 w-7 place-items-center rounded-md bg-surface disabled:opacity-35"
                >
                  <IconArrowDown size={14} stroke={2} className="text-ink-2" />
                </button>
              </Tooltip>
              <Tooltip label="Duplicate" withArrow>
                <button
                  onClick={onDuplicate}
                  aria-label="Duplicate question"
                  className="hair grid h-7 w-7 place-items-center rounded-md bg-surface"
                >
                  <IconCopy size={14} stroke={1.9} className="text-ink-2" />
                </button>
              </Tooltip>
              <Tooltip label="Delete" withArrow>
                <button
                  onClick={onDelete}
                  aria-label="Delete question"
                  className="hair grid h-7 w-7 place-items-center rounded-md bg-surface"
                >
                  <IconTrash size={14} stroke={1.9} style={{ color: 'var(--color-critical)' }} />
                </button>
              </Tooltip>
            </div>
          </div>
        </div>
      </Collapse>
    </article>
  )
}

/* ──────────────────────────────────────────────────────────────── the screen */

export function ExamBuilder() {
  const exams = useApp((s) => s.exams)
  const upsertExam = useApp((s) => s.upsertExam)
  const saveVersioned = useApp((s) => s.saveVersioned)
  const removeExam = useApp((s) => s.removeExam)
  const createExam = useApp((s) => s.createExam)
  const setStatus = useApp((s) => s.setStatus)
  const currentExamId = useApp((s) => s.currentExamId)
  const [exam, setExam] = useState<Exam>(
    () => useApp.getState().exams.find((e) => e.id === currentExamId) ?? useApp.getState().exams[0]!,
  )

  // Opening something from the Library is what selects it here.
  useEffect(() => {
    if (!currentExamId || currentExamId === exam.id) return
    const next = exams.find((e) => e.id === currentExamId)
    if (next) {
      setExam(next)
      setOpenId('')
      setSaved(true)
      setSettingsOpen(false)
    }
  }, [currentExamId, exams, exam.id])
  const [openId, setOpenId] = useState<string>('')
  const [saved, setSaved] = useState(true)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [mode, setMode] = useState<'edit' | 'preview'>('edit')
  const [showKey, setShowKey] = useState(true)

  const patch = (p: Partial<Exam>) => {
    setExam((e) => ({ ...e, ...p }))
    setSaved(false)
  }
  const mapSection = (id: string, fn: (s: Exam['sections'][number]) => Exam['sections'][number]) =>
    patch({ sections: exam.sections.map((s) => (s.id === id ? fn(s) : s)) })

  const items = useMemo(() => allItems(exam), [exam])
  const numberOf = useMemo(() => {
    const m = new Map<string, number>()
    let n = 0
    exam.sections.forEach((s) => s.items.forEach((i) => m.set(i.id, ++n)))
    return m
  }, [exam])
  const scored = isScored(exam)
  const incomplete = items.filter((i) => !isComplete(i, scored)).length
  const mediaBytes = useMemo(() => examMediaBytes(exam), [exam])
  const altGaps = useMemo(() => missingAlt(exam), [exam])

  const addQuestion = (sectionId: string, type: ItemType) => {
    const it = blankItem(type)
    mapSection(sectionId, (s) => ({ ...s, items: [...s.items, it] }))
    setOpenId(it.id)
  }

  const duplicate = (sectionId: string, item: Item) => {
    const remap = new Map(item.choices.map((c) => [c.id, uid('c')]))
    const copy: Item = {
      ...item,
      id: uid('i'),
      choices: item.choices.map((c) => ({ ...c, id: remap.get(c.id)! })),
      correct: item.choices.length ? item.correct.map((c) => remap.get(c) ?? c) : [...item.correct],
    }
    mapSection(sectionId, (s) => {
      const at = s.items.findIndex((x) => x.id === item.id)
      const next = [...s.items]
      next.splice(at + 1, 0, copy)
      return { ...s, items: next }
    })
    setOpenId(copy.id)
  }

  const move = (sectionId: string, from: number, dir: -1 | 1) =>
    mapSection(sectionId, (s) => {
      const to = from + dir
      if (to < 0 || to >= s.items.length) return s
      const next = [...s.items]
      const [m] = next.splice(from, 1)
      next.splice(to, 0, m!)
      return { ...s, items: next }
    })

  const setScreen = useApp((s) => s.setScreen)
  const [saveErr, setSaveErr] = useState('')
  const [askVersion, setAskVersion] = useState(false)

  const writeExam = (mode: 'same' | 'new') => {
    try {
      const next = saveVersioned(exam, mode)
      setExam(next)
      setSaved(true)
      setSaveErr('')
      setAskVersion(false)
    } catch (e) {
      setSaveErr(e instanceof StorageFullError ? e.message : 'Could not save this exam.')
      setAskVersion(false)
    }
  }

  /** Editing the exact version candidates are sitting is the only case that asks. */
  const editingLiveVersion = exam.status === 'published' && exam.publishedVersion === exam.version

  const publish = () => {
    if (editingLiveVersion) {
      setAskVersion(true)
      return
    }
    try {
      upsertExam(exam)
      setSaved(true)
      setSaveErr('')
    } catch (e) {
      setSaveErr(e instanceof StorageFullError ? e.message : 'Could not save this exam.')
    }
  }

  const pick = (e: Exam) => {
    setExam(e)
    setOpenId('')
    setSaved(true)
    setSettingsOpen(false)
  }

  /* ---------------------------------------------------------------- header */
  const header = (
    <div className="sticky top-0 z-20 -mx-4 mb-4 border-b border-hair bg-plane/90 px-4 py-3 backdrop-blur sm:-mx-6 sm:px-6">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <button
          onClick={() => setScreen('library')}
          className="hair order-1 inline-flex shrink-0 items-center gap-1.5 rounded-lg bg-surface px-2.5 py-1.5 text-[12px] font-medium text-ink-2"
        >
          <IconArrowLeft size={14} stroke={2} className="text-ink-muted" />
          Library
        </button>
        <Menu position="bottom-start" width={280}>
          <Menu.Target>
            <button className="hair order-1 inline-flex shrink-0 items-center gap-1.5 rounded-lg bg-surface px-2.5 py-1.5 text-[12px] font-medium text-ink-2">
              <IconSelector size={14} stroke={1.9} className="text-ink-muted" />
              Switch
            </button>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Label>Stored in this browser</Menu.Label>
            {exams.map((e) => (
              <Menu.Item
                key={e.id}
                onClick={() => pick(e)}
                leftSection={e.id === exam.id ? <IconCheck size={13} stroke={2.4} /> : <span className="w-[13px]" />}
                rightSection={
                  exams.length > 1 && (
                    <span
                      role="button"
                      tabIndex={0}
                      aria-label={`Delete ${e.title}`}
                      onClick={(ev) => {
                        ev.stopPropagation()
                        const next = removeExam(e.id)
                        if (e.id === exam.id && next[0]) pick(next[0])
                      }}
                      onKeyDown={() => {}}
                    >
                      <IconTrash size={13} stroke={1.8} />
                    </span>
                  )
                }
              >
                <span className="block truncate text-[12.5px]">{e.title}</span>
                <span className="tnum block text-[11px] text-ink-muted">
                  {allItems(e).length} questions · {totalPoints(e)} pts
                </span>
              </Menu.Item>
            ))}
            <Menu.Divider />
            <Menu.Label>Create</Menu.Label>
            {KINDS.map((k) => {
              const Icon = KIND_ICON[k.v]
              return (
                <Menu.Item
                  key={k.v}
                  leftSection={<Icon size={15} stroke={1.8} />}
                  onClick={() => {
                    const e = createExam(k.v)
                    pick(e)
                    setOpenId(allItems(e)[0]?.id ?? '')
                  }}
                >
                  <span className="block text-[12.5px]">New {k.label.toLowerCase()}</span>
                  <span className="block text-[11px] leading-snug text-ink-muted">{k.hint}</span>
                </Menu.Item>
              )
            })}
          </Menu.Dropdown>
        </Menu>

        <TextInput
          variant="unstyled"
          size="md"
          aria-label="Exam title"
          placeholder="Untitled exam"
          className="order-3 min-w-0 basis-full font-semibold sm:order-2 sm:basis-0 sm:flex-1"
          value={exam.title}
          onChange={(e) => patch({ title: e.currentTarget.value })}
        />

        <div className="order-2 ml-auto flex items-center gap-2 sm:order-3">
          <div className="hair flex rounded-lg bg-surface p-0.5">
            {([
              ['edit', 'Edit', IconPencil],
              ['preview', 'Preview', IconEye],
            ] as const).map(([v, label, Icon]) => (
              <button
                key={v}
                onClick={() => setMode(v)}
                className="inline-flex items-center gap-1.5 rounded-[6px] px-2.5 py-1 text-[12px] font-medium"
                style={
                  mode === v
                    ? { background: 'var(--color-s1)', color: 'var(--color-on-brand)' }
                    : { color: 'var(--color-ink-muted)' }
                }
              >
                <Icon size={13} stroke={2} />
                {label}
              </button>
            ))}
          </div>
          {/* Saving and publishing are different decisions, so they are different
              buttons. A draft that is merely saved must not go live by accident. */}
          <button
            onClick={publish}
            disabled={saved}
            className="hair inline-flex items-center gap-1.5 rounded-lg bg-surface px-3 py-1.5 text-[12.5px] font-medium text-ink-2 disabled:opacity-45"
          >
            {saved ? <IconCheck size={14} stroke={2.6} /> : <IconDeviceFloppy size={14} stroke={2} />}
            {saved ? 'Saved' : exam.status === 'draft' ? 'Save draft' : 'Save changes'}
          </button>
          {hasUnpublishedVersion(exam) && exam.status === 'published' ? (
            <Tooltip label={`Make v${exam.version} the version candidates get`} withArrow>
              <div>
                <button
                  onClick={() => {
                    writeExam('same')
                    setStatus(exam.id, 'published')
                    setExam((e) => ({ ...e, publishedVersion: e.version }))
                  }}
                  className="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-[12.5px] font-semibold text-on-brand"
                  style={{ background: 'var(--color-s1)' }}
                >
                  <IconRocket size={14} stroke={2} /> Publish v{exam.version}
                </button>
              </div>
            </Tooltip>
          ) : exam.status === 'draft' ? (
            <Tooltip label={items.length === 0 ? 'Add a question first' : 'Make it available to candidates'} withArrow>
              <div>
                <button
                  onClick={() => {
                    publish()
                    setStatus(exam.id, 'published')
                    setExam((e) => ({ ...e, status: 'published' }))
                  }}
                  disabled={items.length === 0}
                  className="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-[12.5px] font-semibold text-on-brand disabled:opacity-45"
                  style={{ background: 'var(--color-s1)' }}
                >
                  <IconRocket size={14} stroke={2} /> Publish v{exam.version}
                </button>
              </div>
            </Tooltip>
          ) : (
            <Menu position="bottom-end" width={190}>
              <Menu.Target>
                <button className="hair grid h-[30px] w-[30px] place-items-center rounded-lg bg-surface" aria-label="Lifecycle actions">
                  <IconDots size={14} stroke={2} className="text-ink-2" />
                </button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item
                  leftSection={<IconPencilMinus size={14} stroke={1.9} />}
                  onClick={() => {
                    setStatus(exam.id, 'draft')
                    setExam((e) => ({ ...e, status: 'draft' }))
                  }}
                >
                  Revert to draft
                </Menu.Item>
                <Menu.Item
                  leftSection={<IconArchive size={14} stroke={1.9} />}
                  onClick={() => {
                    setStatus(exam.id, 'archived')
                    setScreen('library')
                  }}
                >
                  Archive
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          )}
        </div>
      </div>

      {exam.status === 'published' && !saved && (
        <p
          className="mt-2 flex items-start gap-1.5 rounded-lg p-2 text-[12px] leading-snug"
          style={{ background: 'var(--color-wash-warn)', color: 'var(--color-ink-2)' }}
        >
          <IconAlertTriangle size={13} stroke={2.2} className="mt-px shrink-0" style={{ color: 'var(--color-serious)' }} />
          <span>
            This is <strong className="font-medium text-ink">live as v{exam.publishedVersion}</strong>.
            Saving will ask whether to update that version in place or branch a new one.
          </span>
        </p>
      )}
      {saveErr && (
        <p
          className="mt-2 flex items-start gap-1.5 rounded-lg p-2 text-[12px] leading-snug"
          style={{ background: 'var(--color-wash-critical)', color: 'var(--color-critical)' }}
        >
          <IconAlertTriangle size={13} stroke={2.2} className="mt-px shrink-0" />
          {saveErr}
        </p>
      )}
      <div className="tnum mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-ink-muted">
        <span
          className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium"
          style={{ background: 'var(--color-wash)', color: 'var(--color-brand-text)' }}
        >
          {KINDS.find((k) => k.v === exam.kind)?.label}
        </span>
        <span
          className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-semibold uppercase tracking-wide"
          style={
            exam.status === 'published'
              ? { background: 'var(--color-wash-good)', color: 'var(--color-good-text)' }
              : { background: 'var(--color-sunken)', color: 'var(--color-ink-2)' }
          }
        >
          {exam.status} v{exam.version}
        </span>
        {hasUnpublishedVersion(exam) && (
          <span className="tnum" style={{ color: 'var(--color-serious)' }}>
            v{exam.publishedVersion} live · editing v{exam.version}
          </span>
        )}
        <span>{exam.sections.length} sections</span>
        <span>·</span>
        <span>{items.length} questions</span>
        <span>·</span>
        {scored && (
          <>
            <span>{totalPoints(exam)} points</span>
            <span>·</span>
          </>
        )}
        {exam.timed && (
          <>
            <span>{totalMinutes(exam)} min</span>
            <span>·</span>
          </>
        )}
        {exam.kind === 'exam' && <span>Tier {exam.tier}</span>}
        {mediaBytes > 0 && (
          <>
            <span>·</span>
            <span className="inline-flex items-center gap-1">
              <IconPhoto size={11} stroke={2} /> {prettyBytes(mediaBytes)}
            </span>
          </>
        )}
        {incomplete > 0 && (
          <>
            <span>·</span>
            <span className="inline-flex items-center gap-1" style={{ color: 'var(--color-serious)' }}>
              <IconAlertTriangle size={11} stroke={2.2} /> {incomplete} incomplete
            </span>
          </>
        )}
        {altGaps > 0 && (
          <>
            <span>·</span>
            <span className="inline-flex items-center gap-1" style={{ color: 'var(--color-serious)' }}>
              <IconAlertTriangle size={11} stroke={2.2} /> {altGaps} missing alt text
            </span>
          </>
        )}
        <button
          onClick={() => setSettingsOpen((o) => !o)}
          className="ml-auto inline-flex items-center gap-1 font-medium"
          style={{ color: 'var(--color-s1)' }}
        >
          <IconSettings size={13} stroke={1.9} />
          {settingsOpen ? 'Hide settings' : 'Settings'}
        </button>
      </div>
    </div>
  )

  const versionDialog = (
    <Modal opened={askVersion} onClose={() => setAskVersion(false)} centered title="Save changes" size="md">
      <p className="text-[13px] leading-relaxed text-ink-2">
        <strong className="font-medium text-ink">v{exam.version}</strong> is the version candidates
        are sitting right now.
      </p>
      <div className="mt-4 flex flex-col gap-2.5">
        <button
          onClick={() => writeExam('new')}
          className="rounded-xl border p-3.5 text-left"
          style={{ borderColor: 'transparent', boxShadow: '0 0 0 1.5px var(--color-s1)', background: 'var(--color-wash)' }}
        >
          <p className="text-[13.5px] font-semibold text-ink">Save as v{exam.version + 1}</p>
          <p className="mt-1 text-[12.5px] leading-snug text-ink-2">
            Branches a new working version. v{exam.version} stays live and untouched, so anyone
            mid-attempt and every existing invitation keeps the paper they started on. Publish
            v{exam.version + 1} when you are ready.
          </p>
        </button>
        <button
          onClick={() => writeExam('same')}
          className="rounded-xl border border-hair p-3.5 text-left"
        >
          <p className="text-[13.5px] font-semibold text-ink">Update v{exam.version} in place</p>
          <p className="mt-1 flex items-start gap-1.5 text-[12.5px] leading-snug text-ink-2">
            <IconAlertTriangle size={13} stroke={2.1} className="mt-px shrink-0" style={{ color: 'var(--color-serious)' }} />
            <span>
              Rewrites what candidates are sitting, including anyone mid-attempt. Fine for a typo,
              wrong for anything that changes the answer.
            </span>
          </p>
        </button>
      </div>
      <button onClick={() => setAskVersion(false)} className="mt-3 w-full py-2 text-[13px] font-medium text-ink-2">
        Cancel
      </button>
    </Modal>
  )

  /* -------------------------------------------------------------- settings */
  const settings = (
    <Collapse expanded={settingsOpen} keepMounted={false}>
      <div className="hair mb-4 grid gap-4 rounded-xl bg-surface p-4 sm:grid-cols-2 xl:grid-cols-4">
        <Select
          size="xs"
          label="Purpose"
          description={KINDS.find((k) => k.v === exam.kind)?.hint}
          value={exam.kind}
          allowDeselect={false}
          onChange={(v) => {
            const kind = v as Kind
            patch({
              kind,
              timed: kind === 'exam',
              anonymous: kind === 'survey',
              tier: kind === 'exam' ? exam.tier : 0,
            })
          }}
          data={KINDS.map((k) => ({ value: k.v, label: k.label }))}
        />
        <TextInput
          size="xs"
          label="Issuer"
          value={exam.issuer}
          onChange={(e) => patch({ issuer: e.currentTarget.value })}
        />
        {exam.kind === 'exam' && (
        <Select
          size="xs"
          label="Integrity tier"
          description={TIERS[exam.tier].hint}
          value={String(exam.tier)}
          onChange={(v) => patch({ tier: Number(v) as Exam['tier'] })}
          allowDeselect={false}
          data={TIERS.map((t) => ({ value: String(t.v), label: t.label }))}
        />
        )}
        {scored && (
        <NumberInput
          size="xs"
          label="Pass threshold"
          description="Percent of auto-scored points."
          suffix="%"
          min={0}
          max={100}
          value={exam.passPct}
          onChange={(v) => patch({ passPct: Number(v) || 0 })}
        />
        )}
        <div className="flex flex-col gap-2 pt-5">
          <Switch
            size="sm"
            label="Shuffle questions within a section"
            checked={exam.shuffle}
            onChange={(e) => patch({ shuffle: e.currentTarget.checked })}
          />
          <Switch
            size="sm"
            label="Timed"
            checked={exam.timed}
            onChange={(e) => patch({ timed: e.currentTarget.checked })}
          />
          <Switch
            size="sm"
            label="Anonymous responses"
            checked={exam.anonymous}
            onChange={(e) => patch({ anonymous: e.currentTarget.checked })}
          />
        </div>
      </div>
    </Collapse>
  )

  /* --------------------------------------------------------------- preview */
  if (mode === 'preview')
    return (
      <div className="p-4 sm:p-6">
        {versionDialog}
        {header}
        {settings}
        <div className="mx-auto max-w-[720px]">
          <div className="mb-4 flex items-center justify-between gap-3">
            <p className="text-[12.5px] text-ink-muted">Exactly what the participant sees.</p>
            {scored && (
              <Switch size="xs" label="Show answer key" checked={showKey} onChange={(e) => setShowKey(e.currentTarget.checked)} />
            )}
          </div>
          {items.length === 0 && (
            <p className="hair rounded-xl bg-surface py-10 text-center text-[13px] text-ink-muted">
              No questions yet.
            </p>
          )}
          {exam.sections.map((s, si) => (
            <section key={s.id} className="mb-6">
              <h2 className="mb-2.5 text-[13px] font-semibold text-ink">
                {si + 1}. {s.name} <span className="tnum font-normal text-ink-muted">· {s.minutes} min</span>
              </h2>
              <div className="flex flex-col gap-3">
                {s.items.map((it, ii) => (
                  <div key={it.id} className="hair rounded-xl bg-surface p-4">
                    <p className="tnum text-[11px] text-ink-muted">
                      Soal {numberOf.get(it.id) ?? ii + 1} · {it.points} poin{it.skill ? ` · ${it.skill}` : ''}
                    </p>
                    <p className="mt-1.5 text-[14.5px] font-semibold leading-snug text-ink">
                      {it.stem.trim() || <span className="font-normal text-ink-muted">Untitled question</span>}
                    </p>
                    {it.image && <ItemImage media={it.image} className="mt-3" />}
                    {it.choices.length > 0 && (
                      <div className="mt-3 flex flex-col gap-1.5">
                        {it.choices.map((c, ci) => {
                          const key = showKey && it.correct.includes(c.id)
                          return (
                            <div
                              key={c.id}
                              className="flex items-start gap-2.5 rounded-lg border px-3 py-2"
                              style={
                                key
                                  ? { borderColor: 'transparent', background: 'var(--color-wash-good)' }
                                  : { borderColor: 'var(--color-hair)' }
                              }
                            >
                              <span className="mt-px grid h-5 w-5 shrink-0 place-items-center rounded-full border border-rule text-[10px] font-semibold text-ink-muted">
                                {'ABCDEFGH'[ci]}
                              </span>
                              <span className="min-w-0 flex-1">
                                <span className="block text-[13px] leading-snug text-ink-2">
                                  {c.text.trim() || (!c.image && <em className="text-ink-muted">Empty option</em>)}
                                </span>
                                {c.image && <ItemImage media={c.image} max={150} className="mt-1.5 w-fit" />}
                              </span>
                              {key && (
                                <span className="inline-flex shrink-0 items-center gap-1 text-[11px] font-medium" style={{ color: 'var(--color-good-text)' }}>
                                  <IconCheck size={11} stroke={3} /> Correct
                                </span>
                              )}
                            </div>
                          )
                        })}
                      </div>
                    )}
                    {isScale(it.type) && it.scale && (
                      <div className="mt-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          {Array.from({ length: it.scale.points }, (_, i) => (
                            <span
                              key={i}
                              className="tnum grid h-8 min-w-8 place-items-center rounded-md border border-hair px-2 text-[12.5px] text-ink-2"
                            >
                              {it.type === 'rating' && it.scale!.points > 7 ? i : i + 1}
                            </span>
                          ))}
                        </div>
                        <div className="mt-1.5 flex justify-between text-[11px] text-ink-muted">
                          <span>{it.scale.minLabel}</span>
                          <span>{it.scale.maxLabel}</span>
                        </div>
                      </div>
                    )}
                    {(it.type === 'short' || it.type === 'essay') && (
                      <div className="mt-3 rounded-lg border border-dashed border-rule px-3 py-4 text-[12px] text-ink-muted">
                        {it.type === 'short' ? 'Short answer field' : 'Long-form answer area'}
                        {showKey && it.type === 'short' && it.correct.length > 0 && (
                          <span className="tnum ml-2" style={{ color: 'var(--color-good-text)' }}>
                            accepts: {it.correct.join(', ')}
                          </span>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </section>
          ))}
        </div>
      </div>
    )

  /* ------------------------------------------------------------------ edit */
  return (
    <div className="p-4 sm:p-6">
      {versionDialog}
      {header}
      {settings}

      <div className="mx-auto max-w-[720px]">
        {exam.sections.map((s, si) => (
          <section key={s.id} className="mb-7">
            <div className="mb-2.5 flex items-center gap-2">
              <span className="tnum shrink-0 text-[11px] font-medium uppercase tracking-wider text-ink-muted">
                Section {si + 1}
              </span>
              <TextInput
                variant="unstyled"
                size="sm"
                aria-label={`Section ${si + 1} name`}
                placeholder="Section name"
                className="min-w-0 flex-1 font-semibold"
                value={s.name}
                onChange={(e) => mapSection(s.id, (x) => ({ ...x, name: e.currentTarget.value }))}
              />
              <NumberInput
                size="xs"
                w={76}
                min={1}
                suffix=" min"
                aria-label={`Section ${si + 1} duration`}
                value={s.minutes}
                onChange={(v) => mapSection(s.id, (x) => ({ ...x, minutes: Number(v) || 1 }))}
              />
              {exam.sections.length > 1 && (
                <Menu position="bottom-end">
                  <Menu.Target>
                    <button className="hair grid h-7 w-7 shrink-0 place-items-center rounded-md bg-surface" aria-label={`Section ${si + 1} actions`}>
                      <IconDots size={14} stroke={2} className="text-ink-2" />
                    </button>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item
                      color="red"
                      leftSection={<IconTrash size={13} stroke={1.9} />}
                      onClick={() => patch({ sections: exam.sections.filter((x) => x.id !== s.id) })}
                    >
                      Delete section and {s.items.length} question{s.items.length === 1 ? '' : 's'}
                    </Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              )}
            </div>

            <div className="flex flex-col gap-2">
              {s.items.map((it, ii) => (
                <QuestionCard
                  key={it.id}
                  item={it}
                  n={numberOf.get(it.id) ?? ii + 1}
                  scored={scored}
                  open={openId === it.id}
                  first={ii === 0}
                  last={ii === s.items.length - 1}
                  onOpen={() => setOpenId(openId === it.id ? '' : it.id)}
                  onPatch={(p) => mapSection(s.id, (x) => ({ ...x, items: x.items.map((y) => (y.id === it.id ? { ...y, ...p } : y)) }))}
                  onType={(t) =>
                    mapSection(s.id, (x) => ({
                      ...x,
                      items: x.items.map((y) => (y.id === it.id ? { ...blankItem(t), id: y.id, stem: y.stem, points: y.points, skill: y.skill } : y)),
                    }))
                  }
                  onMove={(d) => move(s.id, ii, d)}
                  onDuplicate={() => duplicate(s.id, it)}
                  onDelete={() => {
                    mapSection(s.id, (x) => ({ ...x, items: x.items.filter((y) => y.id !== it.id) }))
                    if (openId === it.id) setOpenId('')
                  }}
                />
              ))}

              {s.items.length === 0 && (
                <p className="hair rounded-xl bg-surface py-8 text-center text-[13px] text-ink-muted">
                  No questions in this section yet.
                </p>
              )}
            </div>

            {/* the primary action, right where the work is */}
            <div className="mt-2.5 flex items-center gap-2">
              <button
                onClick={() => addQuestion(s.id, 'single')}
                className="inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-[12.5px] font-semibold text-on-brand"
                style={{ background: 'var(--color-s1)' }}
              >
                <IconPlus size={14} stroke={2.4} /> Add question
              </button>
              <Menu position="bottom-start" width={230}>
                <Menu.Target>
                  <button className="hair inline-flex items-center gap-1 rounded-lg bg-surface px-2.5 py-2 text-[12.5px] font-medium text-ink-2">
                    Choose type <IconChevronDown size={13} stroke={2} />
                  </button>
                </Menu.Target>
                <Menu.Dropdown>
                  {ITEM_TYPES.map((t) => {
                    const m = TYPE_META[t.v]
                    return (
                      <Menu.Item key={t.v} leftSection={<m.Icon size={15} stroke={1.8} />} onClick={() => addQuestion(s.id, t.v)}>
                        <span className="block text-[12.5px]">{m.label}</span>
                        <span className="block text-[11px] text-ink-muted">{m.hint}</span>
                      </Menu.Item>
                    )
                  })}
                </Menu.Dropdown>
              </Menu>
            </div>
          </section>
        ))}

        <button
          onClick={() => patch({ sections: [...exam.sections, blankSection(exam.sections.length + 1)] })}
          className="hair flex w-full items-center justify-center gap-1.5 rounded-xl bg-surface py-3 text-[12.5px] font-medium text-ink-2"
        >
          <IconPlus size={14} stroke={2.2} /> Add section
        </button>
      </div>
    </div>
  )
}
