import { isBusy, isOff, isOn } from './format'
import { api } from './api'
import { errorText, runOperation, toast } from './store'
import type { OpRequest, Server } from './types'
import { confirmAction, openDialog, openServer } from './ui'

export interface ActionItem {
  label: string
  run: () => void
  danger?: boolean
  disabled?: boolean
  hint?: string
  href?: string
}

export interface ActionGroup {
  label?: string
  items: ActionItem[]
}

interface Confirmation {
  title: string
  body: string
  confirmLabel: string
}

async function run(server: Server, req: OpRequest, confirm?: Confirmation, done?: string) {
  if (confirm && !(await confirmAction({ ...confirm, danger: true }))) return
  const op = await runOperation(server.id, req)
  if (op && done) toast(done)
}

function powerOffBody(server: Server): string {
  const outlets = server.outlets.length > 0
  if (server.has_bmc && outlets)
    return 'Forces the system off through the BMC, then cuts its outlets. Anything running stops without a clean shutdown.'
  if (server.has_bmc) return 'Forces the system off through the BMC. Anything running stops without a clean shutdown.'
  return 'Cuts power at the PDU immediately.'
}

export function powerOn(server: Server) {
  return run(server, { kind: 'power_on' })
}

export function powerOff(server: Server) {
  return run(
    server,
    { kind: 'power_off' },
    { title: `Power off ${server.name}?`, body: powerOffBody(server), confirmLabel: `Power off ${server.name}` },
  )
}

/**
 * Opens the BMC's graphical console in a new tab. The tab is opened during the click
 * (so pop-up blockers allow it) and pointed at the launch URL once the BMC answers;
 * on Dell that URL carries a one-time login, so it opens straight into the console.
 */
export async function openKVM(server: Server) {
  const tab = window.open('', '_blank')
  try {
    const launch = await api.kvm(server.id)
    if (tab) {
      tab.opener = null
      tab.location.href = launch.url
    } else {
      window.open(launch.url, '_blank', 'noopener')
    }
    if (launch.note) toast(launch.note)
  } catch (e) {
    tab?.close()
    toast(`Couldn't open the console: ${errorText(e)}`, 'error')
  }
}

export function serverActions(server: Server): ActionGroup[] {
  const busy = isBusy(server)
  const on = isOn(server)
  const off = isOff(server)
  const bmcUp = server.has_bmc && server.state.bmc === 'ok'
  const groups: ActionGroup[] = []

  const power: ActionItem[] = [
    { label: 'Power on', run: () => powerOn(server), disabled: busy || server.state.state === 'on' },
  ]
  if (server.has_bmc) {
    power.push({
      label: 'Shut down',
      hint: 'Ask the OS to shut down cleanly',
      disabled: busy || !bmcUp || !on,
      run: () =>
        run(
          server,
          { kind: 'graceful_shutdown' },
          {
            title: `Shut down ${server.name}?`,
            body: 'Asks the operating system to shut down cleanly, then waits up to 5 minutes for the system to power off.',
            confirmLabel: `Shut down ${server.name}`,
          },
        ),
    })
  }
  power.push({
    label: 'Restart',
    hint: server.has_bmc ? 'Hard reset through the BMC' : 'Power cycle the outlets',
    danger: true,
    disabled: busy || !on,
    run: () =>
      run(
        server,
        { kind: 'restart' },
        {
          title: `Restart ${server.name}?`,
          body: server.has_bmc
            ? 'Hard resets the system through the BMC, like pressing the reset button. Unsaved work on the server is lost.'
            : 'Turns its outlets off and back on.',
          confirmLabel: `Restart ${server.name}`,
        },
      ),
  })
  if (server.has_bmc) {
    power.push({
      label: 'Restart gracefully',
      hint: 'Ask the OS to reboot',
      disabled: busy || !bmcUp || !on,
      run: () =>
        run(
          server,
          { kind: 'graceful_restart' },
          {
            title: `Restart ${server.name} gracefully?`,
            body: 'Asks the operating system to reboot itself cleanly.',
            confirmLabel: `Restart ${server.name}`,
          },
        ),
    })
  }
  power.push({ label: 'Power off', danger: true, disabled: busy || off, run: () => powerOff(server) })
  if (server.outlets.length > 0) {
    power.push({
      label: 'Power cycle',
      hint: 'Fully off, outlets included, then back on',
      danger: true,
      disabled: busy,
      run: () =>
        run(
          server,
          { kind: 'power_cycle' },
          {
            title: `Power cycle ${server.name}?`,
            body: 'Powers fully off (BMC, then outlets), pauses, and powers back on. Use this to clear a hung BMC.',
            confirmLabel: `Power cycle ${server.name}`,
          },
        ),
    })
  }
  groups.push({ label: 'Power', items: power })

  if (server.has_bmc) {
    groups.push({
      label: 'Next boot',
      items: [
        {
          label: 'Network (PXE) once',
          disabled: busy || !bmcUp,
          run: () => run(server, { kind: 'boot_override', target: 'pxe' }, undefined, `${server.name} will network boot next time`),
        },
        {
          label: 'BIOS setup once',
          disabled: busy || !bmcUp,
          run: () => run(server, { kind: 'boot_override', target: 'bios' }, undefined, `${server.name} will enter BIOS setup next time`),
        },
        { label: 'Reinstall…', disabled: busy, run: () => openDialog({ type: 'reinstall', serverId: server.id }) },
      ],
    })
  }

  if (server.has_bmc) {
    groups.push({
      label: 'Console',
      items: [
        { label: 'Graphical console (KVM)', disabled: !bmcUp, run: () => openKVM(server) },
        { label: 'Serial console', run: () => openServer(server.id, 'console') },
      ],
    })
  }

  groups.push({
    items: [{ label: 'Schedule…', run: () => openDialog({ type: 'schedule', serverId: server.id }) }],
  })

  if (server.outlets.length > 0) {
    groups.push({
      label: 'Outlets',
      items: server.outlets.flatMap((ref) => {
        const state = server.state.outlets?.find((o) => o.ref === ref)?.state
        return [
          {
            label: `Turn on ${ref}`,
            disabled: busy || state === 'on',
            run: () => run(server, { kind: 'outlet', target: ref, action: 'on' }),
          },
          {
            label: `Turn off ${ref}`,
            danger: true,
            disabled: busy || state === 'off',
            run: () =>
              run(
                server,
                { kind: 'outlet', target: ref, action: 'off' },
                { title: `Turn off outlet ${ref}?`, body: `Cuts power to ${server.name} through this outlet immediately.`, confirmLabel: 'Turn off outlet' },
              ),
          },
        ]
      }),
    })
  }

  if (server.has_bmc) {
    groups.push({
      label: 'Diagnostics',
      items: [
        {
          label: 'Send NMI',
          hint: 'Non-maskable interrupt',
          danger: true,
          disabled: busy || !bmcUp || !on,
          run: () =>
            run(
              server,
              { kind: 'bmc', action: 'nmi' },
              {
                title: `Send an NMI to ${server.name}?`,
                body: 'Sends a non-maskable interrupt. Linux usually panics and writes a crash dump if kdump is set up.',
                confirmLabel: 'Send NMI',
              },
            ),
        },
      ],
    })
  }

  return groups
}
