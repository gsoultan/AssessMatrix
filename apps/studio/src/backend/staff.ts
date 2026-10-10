/**
 * Staff sign-in against the core: email, then a six-digit code sent to it.
 * There are no passwords to store or reset (docs/09 §5).
 */
import { ConnectError, Code } from '@connectrpc/connect'
import { api } from '../api/client'

export type Staff = { id: string; email: string; name: string; tenantName: string }

const staffIn = (s: { id: string; email: string; name: string; tenantName: string }): Staff => ({
  id: s.id,
  email: s.email,
  name: s.name,
  tenantName: s.tenantName,
})

export const staffAuth = {
  /** The signed-in staff member, or null. Anything but "signed out" is thrown. */
  async whoAmI(): Promise<Staff | null> {
    try {
      const r = await api.auth.whoAmI({})
      return r.staff ? staffIn(r.staff) : null
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) return null
      throw err
    }
  },
  /** Answers the same whether or not the address is known, so it reveals nothing. */
  async requestCode(email: string) {
    await api.auth.requestStaffCode({ email })
  },
  async verifyCode(email: string, code: string): Promise<Staff | null> {
    const r = await api.auth.verifyStaffCode({ email, code: code.replace(/\D/g, '') })
    return r.ok && r.staff ? staffIn(r.staff) : null
  },
  async signOut() {
    try {
      await api.auth.signOut({})
    } catch {
      /* the cookie is cleared or expires either way */
    }
  },
}
