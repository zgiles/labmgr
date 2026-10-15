// Mirrors the Go API (backend/api.go, state.go, engine.go)

export type PowerState = 'on' | 'off' | 'unknown'
export type Link = 'ok' | 'unreachable' | 'none' | 'pending'

export type DisplayState =
  | 'unknown'
  | 'on'
  | 'off'
  | 'powered'
  | 'partial'
  | 'unreachable'
  | 'powering_on'
  | 'powering_off'
  | 'shutting_down'
  | 'restarting'
  | 'power_cycling'
  | 'busy'

export interface OutletStatus {
  ref: string
  state: PowerState
}

export interface OperationSummary {
  id: string
  kind: string
  label: string
  step: number
  steps: number
  step_label: string
}

export interface BootFetch {
  at: string
  client_ip: string
  source: string
}

export interface ServerState {
  server_id: string
  state: DisplayState
  power: PowerState
  bmc: Link
  bmc_error?: string
  pdu: Link
  pdu_error?: string
  outlets?: OutletStatus[]
  operation?: OperationSummary
  last_boot?: BootFetch
  bmc_checked_at?: string
  pdu_checked_at?: string
}

export interface OSView {
  template_id: string
  template_name: string
  boot_method: string
  one_shot: boolean
  assigned_at: string
  assigned_by?: string
  then_template?: string
}

export interface Server {
  id: string
  name: string
  description: string
  metadata: Record<string, string>
  bmc_host?: string
  has_bmc: boolean
  outlets: string[]
  mac?: string
  state: ServerState
  os?: OSView
  console: { web_url?: string; serial: boolean }
}

export type OpStatus = 'scheduled' | 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted'
export type StepStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'skipped'

export interface Step {
  kind: string
  label: string
  action?: string
  target?: string
  seconds?: number
  status: StepStatus
  attempts: number
  error?: string
  started_at?: string
  finished_at?: string
}

export interface Operation {
  id: string
  server_id?: string
  kind: string
  label: string
  action?: string
  target?: string
  status: OpStatus
  steps: Step[]
  current: number
  error?: string
  note?: string
  requested_by?: string
  created_at: string
  scheduled_for?: string
  started_at?: string
  finished_at?: string
}

export interface ActivityEvent {
  id: string
  at: string
  server_id?: string
  kind: string
  message: string
  actor?: string
  level?: 'info' | 'warning' | 'error'
}

export interface OSTemplate {
  id: string
  name: string
  description: string
  boot_method: 'local' | 'chain' | 'template'
  next_path?: string
  boot_template?: string
  is_built_in: boolean
  created_at: string
}

export interface BootScript {
  script: string
  source: string
  source_name: string
  one_shot: boolean
}

export interface BMCDetails {
  info: {
    manufacturer?: string
    model?: string
    serial_number?: string
    bios_version?: string
    health?: string
    bmc_model?: string
    bmc_firmware?: string
    graphical_kvm: boolean
  }
  boot: { target: string; enabled: string; mode?: string }
  boot_error?: string
}

export interface BMCLogEntry {
  created: string
  severity: string
  message: string
}

export interface OpRequest {
  kind: string
  action?: string
  target?: string
  persistent?: boolean
  scheduled_for?: string
}
