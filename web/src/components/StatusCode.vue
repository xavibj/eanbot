<script setup>
import { computed } from 'vue'
import { statusCodeClass, statusCodeLabel } from '../format.js'

const props = defineProps({
  status: { type: [Number, String], default: 0 },
  blocked: { type: Boolean, default: false },
})

const label = computed(() => statusCodeLabel(props.status, props.blocked))
const cls = computed(() => statusCodeClass(props.status, props.blocked))
</script>

<template>
  <span :class="cls">{{ label }}</span>
</template>
