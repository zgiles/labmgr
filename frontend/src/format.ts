import type { DisplayState, OpStatus, Server } from './types'

export type Tone = 'success' | 'neutral' | 'warning' | 'danger' | 'accent' | 'muted'

interface StateInfo {
  label: string
  tone: Tone
  live?: boolean // pulsing dot
}

export const stateInfo: Record<DisplayState, StateInfo> = {
  on: { label: 'On', tone: 'success' },
  off: { label: 'Off', tone: 'neutral' },
  powered: { label: 'Powered', tone: 'warning' },
  partial: { label: 'Partly on', tone: 'warning' },
  unreachable: { label: 'Unreachable', tone: 'danger' },
  unknown: { label: 'Checking', tone: 'muted' },
  powering_on: { label: 'Powering on', tone: 'accent', live: true },
  powering_off: { label: 'Powering off', tone: 'accent', live: true },
  shutting_down: { label: 'Shutting down', tone: 'accent', live: true },
  restarting: { label: 'Restarting', tone: 'accent', live: true },
  power_cycling: { label: 'Power cycling', tone: 'accent', live: true },
  busy: { label: 'Working', tone: 'accent', live: true },
}

/** One line saying why a server is in its state */
export function stateExplanation(server: Server, short = false): string {
  const st = server.state
  const outletsOn = st.outlets?.filter((o) => o.state === 'on').length ?? 0
  const outletCount = st.outlets?.length ?? 0
  switch (st.state) {
    case 'on':
      return server.has_bmc ? 'The BMC reports the system running' : 'All outlets are on'
    case 'off':
      if (server.has_bmc && st.bmc === 'unreachable') return 'Outlets are off, so the BMC is dark too'
      if (outletCount && outletsOn) return 'System off; outlets still supply standby power'
      return server.has_bmc ? 'The BMC reports the system off' : 'All outlets are off'
    case 'powered':
      return 'Outlets are on, but the BMC isn’t responding'
    case 'partial':
      return `${outletsOn} of ${outletCount} outlets on`
    case 'unreachable':
      if (server.has_bmc && outletCount) return 'Neither the BMC nor the PDU is responding'
      if (server.has_bmc) return short ? 'BMC not responding' : 'The BMC isn’t responding. Is the server plugged in?'
      return 'The PDU isn’t responding'
    case 'unknown':
      return 'Waiting for the first status check'
    default:
      return st.operation?.step_label ?? 'An operation is running'
  }
}

export const isOn = (s: Server) => s.state.state === 'on' || s.state.state === 'powered' || s.state.state === 'partial'
export const isOff = (s: Server) => s.state.state === 'off'
export const isBusy = (s: Server) => !!s.state.operation

export const opStatusInfo: Record<OpStatus, { label: string; tone: Tone }> = {
  scheduled: { label: 'Scheduled', tone: 'muted' },
  pending: { label: 'Starting', tone: 'accent' },
  running: { label: 'Running', tone: 'accent' },
  succeeded: { label: 'Done', tone: 'success' },
  failed: { label: 'Failed', tone: 'danger' },
  cancelled: { label: 'Cancelled', tone: 'neutral' },
  interrupted: { label: 'Interrupted', tone: 'warning' },
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

/** "3 min ago", "in 2 hours" */
export function relativeTime(iso?: string, now = Date.now()): string {
  if (!iso) return ''
  const seconds = Math.round((new Date(iso).getTime() - now) / 1000)
  const abs = Math.abs(seconds)
  if (abs < 45) return seconds <= 0 ? 'just now' : 'in a moment'
  if (abs < 3600) return rtf.format(Math.round(seconds / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(seconds / 3600), 'hour')
  return rtf.format(Math.round(seconds / 86400), 'day')
}

export function dateTime(iso?: string): string {
  if (!iso) return ''
  return new Date(iso).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  })
}

/** "1m 20s" */
export function duration(fromIso?: string, toIso?: string): string {
  if (!fromIso) return ''
  const ms = (toIso ? new Date(toIso).getTime() : Date.now()) - new Date(fromIso).getTime()
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  return s % 60 ? `${m}m ${s % 60}s` : `${m}m`
}

/** Strips the "@client-ip" labmgr appends to requesters */
export function actorName(actor?: string): string {
  return actor?.split('@')[0] ?? ''
}
