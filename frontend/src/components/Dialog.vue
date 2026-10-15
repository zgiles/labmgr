<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import Icon from './Icon.vue'

defineProps<{ title: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()

const box = ref<HTMLDivElement>()
let previousFocus: HTMLElement | null = null

function focusables(): HTMLElement[] {
  return Array.from(
    box.value?.querySelectorAll<HTMLElement>('button:not(:disabled), input, select, textarea, a[href]') ?? [],
  )
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    emit('close')
  } else if (e.key === 'Tab') {
    // Keep focus inside the dialog
    const list = focusables()
    if (!list.length) return
    const first = list[0]
    const last = list[list.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
}

onMounted(async () => {
  previousFocus = document.activeElement as HTMLElement
  await nextTick()
  const list = focusables()
  // Skip the close button so typing goes straight into the first field
  ;(list.find((el) => !el.classList.contains('close')) ?? list[0])?.focus()
})
onBeforeUnmount(() => previousFocus?.focus())
</script>

<template>
  <Teleport to="body">
    <div class="overlay" @pointerdown.self="emit('close')">
      <div ref="box" class="dialog" :class="{ wide }" role="dialog" aria-modal="true" :aria-label="title" @keydown="onKey">
        <header>
          <h2>{{ title }}</h2>
          <button class="icon-btn close" aria-label="Close" @click="emit('close')"><Icon name="close" /></button>
        </header>
        <div class="body"><slot /></div>
        <footer v-if="$slots.footer"><slot name="footer" /></footer>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: grid;
  place-items: center;
  padding: 16px;
  background: var(--overlay);
  animation: fade 160ms var(--ease);
}
.dialog {
  width: min(460px, 100%);
  max-height: calc(100vh - 32px);
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-xl);
  box-shadow: var(--shadow-lg);
  animation: rise 200ms var(--ease);
}
.dialog.wide {
  width: min(680px, 100%);
}
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 18px 20px 8px 24px;
}
h2 {
  font-size: 17px;
  font-weight: 600;
  letter-spacing: -0.01em;
}
.body {
  padding: 4px 24px 20px;
  overflow-y: auto;
  color: var(--text-2);
  line-height: 1.55;
}
footer {
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  padding: 14px 20px;
  border-top: 1px solid var(--border);
}
@keyframes fade {
  from {
    opacity: 0;
  }
}
@keyframes rise {
  from {
    opacity: 0;
    transform: translateY(6px) scale(0.99);
  }
}
</style>
