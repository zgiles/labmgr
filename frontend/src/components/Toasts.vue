<script setup lang="ts">
import { dismissToast, store } from '../store'
import Icon from './Icon.vue'
</script>

<template>
  <div class="toasts" role="status" aria-live="polite">
    <TransitionGroup name="toast">
      <div v-for="t in store.toasts" :key="t.id" class="toast" :class="`tone-${t.tone}`">
        <Icon v-if="t.tone === 'error'" name="alert" class="lead" />
        <span>{{ t.message }}</span>
        <button class="icon-btn" aria-label="Dismiss" @click="dismissToast(t.id)"><Icon name="close" :size="14" /></button>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toasts {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 70;
  display: grid;
  gap: 8px;
  width: min(380px, calc(100vw - 32px));
}
.toast {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 6px 10px 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-lg);
  font-size: 13.5px;
}
.toast span {
  flex: 1;
  padding-top: 5px;
  overflow-wrap: anywhere;
}
.toast .icon-btn {
  width: 28px;
  height: 28px;
}
.lead {
  margin-top: 7px;
  flex: none;
  color: var(--danger);
}
.tone-error {
  border-color: color-mix(in srgb, var(--danger) 30%, transparent);
}
.toast-enter-active,
.toast-leave-active {
  transition:
    opacity 180ms var(--ease),
    transform 180ms var(--ease);
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
</style>
