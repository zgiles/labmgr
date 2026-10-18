<script setup lang="ts">
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { openKVM } from '../actions'
import type { Server } from '../types'
import Icon from './Icon.vue'

const props = defineProps<{ server: Server }>()

type Status = 'connecting' | 'connected' | 'closed'
const status = ref<Status>('connecting')
const message = ref('')
const host = ref<HTMLDivElement>()

let term: Terminal | undefined
let fit: FitAddon | undefined
let ws: WebSocket | undefined
let observer: ResizeObserver | undefined
let disposed = false

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

function sendResize() {
  if (ws?.readyState === WebSocket.OPEN && term) {
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
  }
}

function connect() {
  if (!term) return
  ws?.close()
  status.value = 'connecting'
  message.value = ''
  term.reset()

  const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
  const url = `${scheme}://${location.host}/api/v2/servers/${encodeURIComponent(props.server.id)}/console?cols=${term.cols}&rows=${term.rows}`
  const socket = new WebSocket(url)
  socket.binaryType = 'arraybuffer'
  ws = socket

  socket.onmessage = (e) => {
    if (typeof e.data === 'string') {
      const msg = JSON.parse(e.data) as { type: string; state?: string; message?: string }
      if (msg.type === 'status' && msg.state === 'connected') {
        status.value = 'connected'
        sendResize()
        term?.focus()
      } else if (msg.type === 'error' || (msg.type === 'status' && msg.state === 'closed')) {
        status.value = 'closed'
        message.value = msg.message ?? ''
      }
      return
    }
    term?.write(new Uint8Array(e.data as ArrayBuffer))
  }
  socket.onclose = () => {
    if (ws === socket && status.value !== 'closed') {
      status.value = 'closed'
      if (!message.value) message.value = 'Connection to Lab Manager closed'
    }
  }
}

onMounted(() => {
  term = new Terminal({
    cursorBlink: true,
    fontFamily: cssVar('--font-mono'),
    fontSize: 13,
    lineHeight: 1.15,
    scrollback: 5000,
    theme: {
      background: cssVar('--term-bg'),
      foreground: cssVar('--term-text'),
      cursor: cssVar('--accent'),
      selectionBackground: 'rgba(91, 140, 255, 0.35)',
      // BMCs paint "white on black" (SGR 37;40); make that black our background, not xterm's grey
      black: cssVar('--term-bg'),
      brightBlack: '#6b6b68',
    },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(host.value!)
  fit.fit()

  const encoder = new TextEncoder()
  term.onData((data) => {
    if (ws?.readyState === WebSocket.OPEN && status.value === 'connected') ws.send(encoder.encode(data))
  })
  term.onResize(sendResize)

  observer = new ResizeObserver(() => {
    if (!disposed) fit?.fit()
  })
  observer.observe(host.value!)

  // Wait a frame so the drawer has its final width before sizing the PTY
  requestAnimationFrame(() => {
    fit?.fit()
    connect()
  })
})

onBeforeUnmount(() => {
  disposed = true
  observer?.disconnect()
  ws?.close()
  term?.dispose()
})
</script>

<template>
  <div class="console">
    <div class="bar">
      <span class="status" :class="`s-${status}`">
        <span class="dot" aria-hidden="true" />
        <template v-if="status === 'connecting'">Connecting to the BMC…</template>
        <template v-else-if="status === 'connected'">Serial console</template>
        <template v-else>Disconnected<span v-if="message" class="why">: {{ message }}</span></template>
      </span>
      <div class="tools">
        <button v-if="status === 'closed'" class="btn btn-sm btn-primary" @click="connect">Reconnect</button>
        <a class="btn btn-sm btn-ghost" :href="`/api/v2/servers/${encodeURIComponent(server.id)}/console/log`" download>Download log</a>
        <button class="btn btn-sm" :disabled="server.state.bmc !== 'ok'" @click="openKVM(server)">
          Graphical console <Icon name="external" :size="12" />
        </button>
      </div>
    </div>
    <div ref="host" class="term" />
    <p class="hint">
      Shared with anyone else watching {{ server.name }}. Output is kept so the log covers what scrolled past.
      The host must send its console to the serial port (e.g. <span class="mono">console=ttyS1,115200</span>) for anything to appear.
    </p>
  </div>
</template>

<style scoped>
.console {
  display: flex;
  flex-direction: column;
  gap: 10px;
  height: 100%;
  min-height: 420px;
}
.bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.status {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
  min-width: 0;
}
.why {
  font-weight: 400;
  color: var(--text-2);
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-3);
  flex: none;
}
.s-connected .dot {
  background: var(--success);
}
.s-connecting .dot {
  background: var(--accent);
  animation: blink 1.2s ease-in-out infinite;
}
.s-closed .dot {
  background: var(--danger);
}
@keyframes blink {
  50% {
    opacity: 0.3;
  }
}
.tools {
  display: flex;
  gap: 4px;
}
.tools a.btn {
  text-decoration: none;
  color: var(--text);
}
.term {
  flex: 1;
  min-height: 360px;
  padding: 10px 4px 10px 12px;
  border-radius: var(--r-md);
  background: var(--term-bg);
  overflow: hidden;
}
.hint {
  font-size: 12.5px;
  color: var(--text-2);
}
</style>
