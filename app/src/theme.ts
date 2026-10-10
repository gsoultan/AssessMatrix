import { createTheme, type MantineColorsTuple } from '@mantine/core'

/**
 * Brand primary IS the categorical slot-1 blue, stepped from the sequential
 * blue ramp. One blue in the product means a brand colour can never be mistaken
 * for a data series colour.
 */
const brand: MantineColorsTuple = [
  '#eef5fd',
  '#cde2fb',
  '#b7d3f6',
  '#9ec5f4',
  '#9ec5f4',
  '#6da7ec',
  '#2a78d6', // primary shade
  '#256abf',
  '#1c5cab',
  '#1c5cab',
]

export const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: 6,
  colors: { brand },
  fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif',
  headings: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
  defaultRadius: 'md',
  cursorType: 'pointer',
  components: {
    Paper: { defaultProps: { bg: 'var(--color-surface)' } },
  },
})
