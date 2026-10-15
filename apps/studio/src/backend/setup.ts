/**
 * First-run setup against the core: the first organisation and its
 * administrator, once. The server prints the setup code to its log.
 */
import { api } from '../api/client'
import { staffIn, type Staff } from './staff'

export type SetupStatus = { needed: boolean; schemaVersion: string; schemaLatest: string; region: string }

export type SetupForm = { code: string; organization: string; name: string; email: string; region: string }

export const setup = {
  async status(): Promise<SetupStatus> {
    const r = await api.setup.getSetupStatus({})
    return { needed: r.needed, schemaVersion: r.schemaVersion, schemaLatest: r.schemaLatest, region: r.region }
  },
  /** Creates the organisation and signs its administrator in. */
  async complete(f: SetupForm): Promise<Staff> {
    const r = await api.setup.completeSetup({
      setupCode: f.code,
      organizationName: f.organization,
      adminName: f.name,
      adminEmail: f.email,
      region: f.region,
    })
    if (!r.staff) throw new Error('the server did not return the administrator')
    return staffIn(r.staff)
  },
}
