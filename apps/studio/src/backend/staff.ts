/**
 * Staff sign-in against the core: email, then a six-digit code sent to it.
 * There are no passwords to store or reset (docs/09 §5).
 */
import { ConnectError, Code } from '@connectrpc/connect'
import { api } from '../api/client'
import { Role, type Staff as StaffMsg } from '@assessmatrix/proto/auth_pb'

/** admin: adds users to the tenant as members, shares instruments. member: the everyday work. */
export type StaffRole = 'admin' | 'member'

export type Staff = {
  id: string
  email: string
  name: string
  tenantId: string
  tenantName: string
  role: StaffRole
  /** Runs the installation: creates tenants and decides who administers each. */
  superAdmin: boolean
}

/** May open the administrators' screens. A super administrator always may. */
export const administers = (s: Staff | null | undefined) => !!s && (s.role === 'admin' || s.superAdmin)

export const roleIn = (r: Role): StaffRole => (r === Role.ADMIN ? 'admin' : 'member')

export const staffIn = (s: StaffMsg): Staff => ({
  id: s.id,
  email: s.email,
  name: s.name,
  tenantId: s.tenantId,
  tenantName: s.tenantName,
  role: roleIn(s.role),
  superAdmin: s.superAdmin,
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
