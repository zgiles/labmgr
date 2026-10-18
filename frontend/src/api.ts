import type {
  ActivityEvent,
  BMCDetails,
  BMCLogEntry,
  BootScript,
  Operation,
  OpRequest,
  OSTemplate,
  Server,
} from './types'

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method,
    headers: {
      'X-Labmgr-Client': 'web',
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  const data = text ? JSON.parse(text) : undefined
  if (!response.ok) {
    throw new ApiError(data?.error ?? `${response.status} ${response.statusText}`, response.status)
  }
  return data as T
}

const v2 = (path: string) => `/v2${path}`
const id = encodeURIComponent

export const api = {
  servers: () => request<Server[]>('GET', v2('/servers')),
  refresh: (serverId: string) => request<Server>('POST', v2(`/servers/${id(serverId)}/refresh`)),
  refreshAll: () => request<Server[]>('POST', v2('/refresh')),

  submit: (serverId: string, req: OpRequest) =>
    request<Operation>('POST', v2(`/servers/${id(serverId)}/operations`), req),
  cancel: (opId: string) => request<Operation>('POST', v2(`/operations/${id(opId)}/cancel`)),
  runNow: (opId: string) => request<Operation>('POST', v2(`/operations/${id(opId)}/run-now`)),
  retry: (opId: string) => request<Operation>('POST', v2(`/operations/${id(opId)}/retry`)),

  events: (serverId?: string, limit = 100) =>
    request<ActivityEvent[]>(
      'GET',
      serverId ? v2(`/servers/${id(serverId)}/events?limit=${limit}`) : v2(`/events?limit=${limit}`),
    ),

  bootScript: (serverId: string) => request<BootScript>('GET', v2(`/servers/${id(serverId)}/boot-script`)),
  bmc: (serverId: string) => request<BMCDetails>('GET', v2(`/servers/${id(serverId)}/bmc`)),
  kvm: (serverId: string) =>
    request<{ url: string; kind: 'direct' | 'login'; note?: string }>('POST', v2(`/servers/${id(serverId)}/kvm`)),
  bmcLogs: (serverId: string, limit = 50) =>
    request<BMCLogEntry[]>('GET', v2(`/servers/${id(serverId)}/bmc/logs?limit=${limit}`)),

  templates: () => request<{ templates: OSTemplate[] }>('GET', v2('/os/templates')).then((r) => r.templates),
  createTemplate: (t: Partial<OSTemplate>) => request<OSTemplate>('POST', v2('/os/templates'), t),
  updateTemplate: (templateId: string, t: Partial<OSTemplate>) =>
    request<OSTemplate>('PUT', v2(`/os/templates/${id(templateId)}`), t),
  deleteTemplate: (templateId: string) => request<unknown>('DELETE', v2(`/os/templates/${id(templateId)}`)),

  assignOS: (serverId: string, templateId: string, oneShot = false) =>
    request<unknown>('PUT', v2(`/servers/${id(serverId)}/os`), { template_id: templateId, one_shot: oneShot }),
  clearOS: (serverId: string) => request<unknown>('DELETE', v2(`/servers/${id(serverId)}/os`)),
}
