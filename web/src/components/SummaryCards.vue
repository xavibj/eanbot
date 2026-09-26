<script setup>
import { computed } from 'vue'
import { formatNumber } from '../format.js'

const props = defineProps({
  summary: { type: Object, default: null },
  // only crawls configured with follow_nofollow get the extra card
  showViaNofollow: { type: Boolean, default: false },
})

const cards = computed(() => {
  const s = props.summary || {}
  const n = (v) => Number(v) || 0
  const cards = [
    { key: 'total', label: 'Total', value: n(s.total), tone: '' },
    { key: 's2', label: '2xx', value: n(s.status_2xx), tone: 'ok' },
    { key: 's3', label: '3xx', value: n(s.status_3xx), tone: 'info' },
    { key: 's4', label: '4xx', value: n(s.status_4xx), tone: 'bad' },
    { key: 's5', label: '5xx', value: n(s.status_5xx), tone: 'bad' },
    { key: 'err', label: 'Errores', value: n(s.errors), tone: 'bad' },
    { key: 'blk', label: 'Bloqueadas', value: n(s.blocked), tone: 'grey' },
    { key: 'nix', label: 'Noindex', value: n(s.noindex), tone: 'grey' },
  ]
  if (props.showViaNofollow) {
    cards.push({ key: 'vnf', label: 'Solo vía nofollow', value: n(s.via_nofollow), tone: 'grey' })
  }
  return cards
})
</script>

<template>
  <div class="summary-grid">
    <div
      v-for="card in cards"
      :key="card.key"
      class="summary-card"
      :class="card.tone ? `summary-card--${card.tone}` : ''"
    >
      <div class="summary-card__label">{{ card.label }}</div>
      <div class="summary-card__value">{{ formatNumber(card.value) }}</div>
    </div>
  </div>
</template>
