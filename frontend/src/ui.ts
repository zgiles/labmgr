import { reactive } from 'vue'

export type View = 'servers' | 'boot' | 'activity'
export type PanelTab = 'overview' | 'boot' | 'activity' | 'console'

interface ConfirmRequest {
  title: string
  body: string
  confirmLabel: string
  danger?: boolean
  resolve: (ok: boolean) => void
}

export type DialogRequest =
  | { type: 'schedule'; serverId: string }
  | { type: 'reinstall'; serverId: string }

export const ui = reactive({
  view: 'servers' as View,
  serverId: null as string | null,
  tab: 'overview' as PanelTab,
  dialog: null as DialogRequest | null,
  confirm: null as ConfirmRequest | null,
})

// Routes: #/servers, #/servers/<id>/<tab>, #/boot, #/activity
const tabs: PanelTab[] = ['overview', 'boot', 'activity', 'console']

function parseHash() {
  const [view, serverId, tab] = location.hash.replace(/^#\/?/, '').split('/').map(decodeURIComponent)
  ui.view = view === 'boot' || view === 'activity' ? view : 'servers'
  ui.serverId = ui.view === 'servers' && serverId ? serverId : null
  ui.tab = tabs.includes(tab as PanelTab) ? (tab as PanelTab) : 'overview'
}

function writeHash() {
  let hash = `#/${ui.view}`
  if (ui.view === 'servers' && ui.serverId) {
    hash += `/${encodeURIComponent(ui.serverId)}`
    if (ui.tab !== 'overview') hash += `/${ui.tab}`
  }
  if (location.hash !== hash) history.replaceState(null, '', hash)
}

export function startRouter() {
  parseHash()
  window.addEventListener('hashchange', parseHash)
}

export function showView(view: View) {
  ui.view = view
  ui.serverId = null
  writeHash()
}

export function openServer(id: string, tab: PanelTab = 'overview') {
  ui.view = 'servers'
  ui.serverId = id
  ui.tab = tab
  writeHash()
}

export function setTab(tab: PanelTab) {
  ui.tab = tab
  writeHash()
}

export function closeServer() {
  ui.serverId = null
  writeHash()
}

export function confirmAction(opts: Omit<ConfirmRequest, 'resolve'>): Promise<boolean> {
  ui.confirm?.resolve(false)
  return new Promise((resolve) => {
    ui.confirm = { ...opts, resolve }
  })
}

export function openDialog(dialog: DialogRequest) {
  ui.dialog = dialog
}
