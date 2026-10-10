/**
 * Which backend this build talks to, fixed at build time.
 *
 * - local (default): everything lives in this browser's localStorage. The
 *   prototype and its end-to-end test run this way, with no server.
 * - core: the Go core over Connect. Staff sign in; candidates arrive by link.
 */
export const CORE = import.meta.env.VITE_BACKEND === 'core'

/**
 * The design mock-ups (proctor monitor, integration hub and the rest) show
 * canned data. In a local build that is the point; in a core build they would
 * pass for working features, so they stay hidden unless asked for.
 */
export const SHOW_MOCKS = !CORE || import.meta.env.VITE_MOCKS === '1'
