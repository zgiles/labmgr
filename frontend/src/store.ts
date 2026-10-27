import { computed, reactive } from 'vue'
import { api, ApiError } from './api'
import type { ActivityEvent, Operation, OpRequest, OSTemplate, PowerSample, Server, ServerState } from './types'

export type Connection = 'connecting' | 'live' | 'reconnecting'

interface Toast {
  id: number
  message: string
  tone: 'info' | 'error'
}

export const store = reactive({
  servers: [] as Server[],
  operations: {} as Record<string, Operation>,
  events: [] as ActivityEvent[],
  templates: [] as OSTemplate[],
  connection: 'connecting' as Connection,
  loaded: false,
  toasts: [] as Toast[],
  /** last hour of power readings per server, for table sparklines */
  power: {} as Record<string, PowerSample[]>,
})

const finished = new Set(['succeeded', 'failed', 'cancelled', 'interrupted'])
export const isActive = (op: Operation) => !finished.has(op.status)

export const operationsNewestFirst = computed(() =>
  Object.values(store.operations).sort((a, b) => b.created_at.localeCompare(a.created_at)),
)
export const activeOperations = computed(() => operationsNewestFirst.value.filter(isActive))

export function operationsFor(serverId: string): Operation[] {
  return operationsNewestFirst.value.filter((op) => op.server_id === serverId)
}

/** The operation currently running on a server, if any */
export function runningOperation(serverId: string): Operation | undefined {
  return operationsNewestFirst.value.find(
    (op) => op.server_id === serverId && (op.status === 'running' || op.status === 'pending'),
  )
}

export function serverById(id?: string): Server | undefined {
  return store.servers.find((s) => s.id === id)
}

let toastSeq = 0
export function toast(message: string, tone: Toast['tone'] = 'info') {
  const t = { id: ++toastSeq, message, tone }
  store.toasts.push(t)
  setTimeout(() => dismissToast(t.id), tone === 'error' ? 9000 : 4500)
}
export function dismissToast(id: number) {
  const i = store.toasts.findIndex((t) => t.id === id)
  if (i >= 0) store.toasts.splice(i, 1)
}

export function errorText(e: unknown): string {
  if (e instanceof ApiError || e instanceof Error) return e.message
  return String(e)
}

/** Submits an operation; failures become a toast and resolve to undefined */
export async function runOperation(serverId: string, req: OpRequest): Promise<Operation | undefined> {
  try {
    const op = await api.submit(serverId, req)
    store.operations[op.id] = op
    if (op.status === 'scheduled') toast(`${op.label} scheduled`)
    return op
  } catch (e) {
    toast(errorText(e), 'error')
    return undefined
  }
}

export async function reloadServers() {
  try {
    store.servers = await api.servers()
  } catch (e) {
    toast(`Couldn't load servers: ${errorText(e)}`, 'error')
  }
}

export async function reloadTemplates() {
  try {
    store.templates = await api.templates()
  } catch (e) {
    toast(`Couldn't load boot images: ${errorText(e)}`, 'error')
  }
}

function applyState(state: ServerState) {
  const server = serverById(state.server_id)
  if (server) server.state = state
  // Extend the sparkline with each new reading, keeping an hour
  if (state.watts != null && state.watts_at) {
    const t = new Date(state.watts_at).getTime()
    const series = store.power[state.server_id] ?? (store.power[state.server_id] = [])
    if (!series.length || series[series.length - 1][0] < t) {
      series.push([t, state.watts])
      const cutoff = Date.now() - 3600_000
      while (series.length && series[0][0] < cutoff) series.shift()
    }
  }
}

async function loadPower() {
  try {
    store.power = await api.powerAll(1)
  } catch {
    // sparklines are a nicety; the table still works without them
  }
}

let reloadTimer: ReturnType<typeof setTimeout> | undefined
function reloadSoon() {
  clearTimeout(reloadTimer)
  reloadTimer = setTimeout(() => {
    reloadServers()
    reloadTemplates()
  }, 150)
}

/** Opens the live update stream; EventSource reconnects on its own */
export function connect() {
  const source = new EventSource('/api/v2/stream')

  source.addEventListener('snapshot', (e) => {
    const data = JSON.parse((e as MessageEvent).data) as { servers: Server[]; operations: Operation[] }
    store.servers = data.servers
    store.operations = Object.fromEntries(data.operations.map((op) => [op.id, op]))
    store.connection = 'live'
    store.loaded = true
  })
  source.addEventListener('state', (e) => applyState(JSON.parse((e as MessageEvent).data)))
  source.addEventListener('operation', (e) => {
    const op = JSON.parse((e as MessageEvent).data) as Operation
    store.operations[op.id] = op
  })
  source.addEventListener('event', (e) => {
    store.events.unshift(JSON.parse((e as MessageEvent).data))
    if (store.events.length > 200) store.events.length = 200
  })
  source.addEventListener('os', reloadSoon)
  source.addEventListener('config', reloadSoon)
  source.onerror = () => {
    if (store.connection !== 'connecting') store.connection = 'reconnecting'
  }

  reloadTemplates()
  loadPower()
  setInterval(loadPower, 5 * 60_000)
  api.events(undefined, 100).then((events) => (store.events = events)).catch(() => {})
}
