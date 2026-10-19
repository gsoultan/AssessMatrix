/**
 * Tenants, who has access to them, and instruments shared between them.
 * Only against the core: a local build is one browser, one tenant.
 *
 * A session is in one tenant at a time. Managing access and sharing need the
 * admin role, and the core checks it again in every other tenant a request
 * touches, so what this file is shown is already what the caller may change.
 */
import { api } from '../api/client'
import { Role } from '@assessmatrix/proto/auth_pb'
import { Status } from '@assessmatrix/proto/exam_pb'
import type { ExamCopy as ExamCopyMsg, Tenant as TenantMsg } from '@assessmatrix/proto/tenant_pb'
import { type Staff, type StaffRole, roleIn, staffIn } from './staff'

/** role 'none': a tenant a super administrator manages without working in it. */
export type Tenant = { id: string; name: string; region: string; role: StaffRole | 'none'; current: boolean }

/** No access, or a role. */
export type Grant = StaffRole | 'none'

export type StaffAccess = { email: string; name: string; grants: Record<string, StaffRole>; superAdmin: boolean }

export type ExamCopy = {
  tenantId: string
  tenantName: string
  /** Empty when not shared there yet. */
  examId: string
  /** The original's version the copy holds. */
  sourceVersion: number
  /** Live there; 0 if the copy was taken offline. */
  publishedVersion: number
  archived: boolean
}

export type ShareResult = { tenantId: string; copy?: ExamCopy; error: string }

const tenantIn = (t: TenantMsg): Tenant => ({
  id: t.id,
  name: t.name,
  region: t.region,
  role: t.role === Role.UNSPECIFIED ? 'none' : roleIn(t.role),
  current: t.current,
})

const copyIn = (c: ExamCopyMsg): ExamCopy => ({
  tenantId: c.tenantId,
  tenantName: c.tenantName,
  examId: c.examId,
  sourceVersion: c.sourceVersion,
  publishedVersion: c.status === Status.PUBLISHED ? c.publishedVersion : 0,
  archived: c.status === Status.ARCHIVED,
})

const roleOut = (g: Grant) => (g === 'admin' ? Role.ADMIN : g === 'member' ? Role.MEMBER : Role.UNSPECIFIED)

export const tenancy = {
  async mine(): Promise<Tenant[]> {
    return (await api.tenants.listMyTenants({})).tenants.map(tenantIn)
  },
  async switchTo(tenantId: string): Promise<Staff> {
    const r = await api.tenants.switchTenant({ tenantId })
    if (!r.staff) throw new Error('The switch did not complete. Reload and try again.')
    return staffIn(r.staff)
  },
  /** Super administrators only. An empty adminEmail makes the caller its administrator. */
  async create(name: string, region = '', adminEmail = '', adminName = ''): Promise<Tenant> {
    const r = await api.tenants.createTenant({ name, region, adminEmail, adminName })
    if (!r.tenant) throw new Error('The tenant was not created. Try again.')
    return tenantIn(r.tenant)
  },
  async access(): Promise<{ tenants: Tenant[]; staff: StaffAccess[] }> {
    const r = await api.tenants.listAccess({})
    return {
      tenants: r.tenants.map(tenantIn),
      staff: r.staff.map((s) => ({
        email: s.email,
        name: s.name,
        grants: Object.fromEntries(s.grants.map((g) => [g.tenantId, roleIn(g.role)])),
        superAdmin: s.superAdmin,
      })),
    }
  },
  async setAccess(email: string, tenantId: string, grant: Grant, name = ''): Promise<void> {
    await api.tenants.setAccess({ email, name, tenantId, role: roleOut(grant) })
  },
  async copies(examId: string): Promise<ExamCopy[]> {
    return (await api.tenants.listExamCopies({ examId })).copies.map(copyIn)
  },
  /** version 0 shares the one live now. */
  async share(examId: string, version: number, tenantIds: string[]): Promise<ShareResult[]> {
    const r = await api.tenants.shareExam({ examId, version, tenantIds })
    return r.results.map((x) => ({ tenantId: x.tenantId, copy: x.copy ? copyIn(x.copy) : undefined, error: x.error }))
  },
}
