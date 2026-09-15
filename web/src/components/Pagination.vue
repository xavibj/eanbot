<script setup>
import { computed } from 'vue'
import { formatNumber } from '../format.js'

const props = defineProps({
  offset: { type: Number, default: 0 },
  limit: { type: Number, default: 100 },
  count: { type: Number, default: 0 },
  total: { type: Number, default: 0 },
  disabled: { type: Boolean, default: false },
})

const emit = defineEmits(['prev', 'next'])

const rangeLabel = computed(() => {
  if (props.total <= 0) return '0'
  const from = props.offset + 1
  const to = Math.min(props.offset + props.count, props.total)
  return `${formatNumber(from)}–${formatNumber(to)} de ${formatNumber(props.total)}`
})

const canPrev = computed(() => props.offset > 0 && !props.disabled)
const canNext = computed(() => props.offset + props.limit < props.total && !props.disabled)
</script>

<template>
  <div class="pagination">
    <span>{{ rangeLabel }}</span>
    <span class="btn-row">
      <button type="button" class="btn btn-sm" :disabled="!canPrev" @click="emit('prev')">
        Anterior
      </button>
      <button type="button" class="btn btn-sm" :disabled="!canNext" @click="emit('next')">
        Siguiente
      </button>
    </span>
  </div>
</template>
