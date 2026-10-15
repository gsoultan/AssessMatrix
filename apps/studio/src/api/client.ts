/**
 * Connect clients for the Go core. One transport, same origin: Vite proxies
 * /assessmatrix.v1.* to the core in development, and production serves both
 * from one origin, so the session cookies stay first-party and SameSite=Strict.
 */
import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { AssignmentService } from '@assessmatrix/proto/assignment_pb'
import { AuthService } from '@assessmatrix/proto/auth_pb'
import { CandidateService } from '@assessmatrix/proto/candidate_pb'
import { ExamService } from '@assessmatrix/proto/exam_pb'
import { PersonService } from '@assessmatrix/proto/people_pb'
import { ProctorService } from '@assessmatrix/proto/proctor_pb'
import { ResultService } from '@assessmatrix/proto/result_pb'
import { WebhookService } from '@assessmatrix/proto/webhook_pb'
import { TenantService } from '@assessmatrix/proto/tenant_pb'

const transport = createConnectTransport({
  baseUrl: '/',
  // Cookies, not tokens in JavaScript: the session is HttpOnly.
  fetch: (input, init) => fetch(input, { ...init, credentials: 'same-origin' }),
})

export const api = {
  auth: createClient(AuthService, transport),
  exams: createClient(ExamService, transport),
  people: createClient(PersonService, transport),
  assignments: createClient(AssignmentService, transport),
  candidate: createClient(CandidateService, transport),
  results: createClient(ResultService, transport),
  proctor: createClient(ProctorService, transport),
  webhooks: createClient(WebhookService, transport),
  tenants: createClient(TenantService, transport),
}
