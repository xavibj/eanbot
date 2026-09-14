<script setup>
import { computed } from 'vue'
import { crawlStatusClass, crawlStatusLabel } from '../format.js'

const props = defineProps({
  status: { type: String, default: '' },
})

const label = computed(() => crawlStatusLabel(props.status))
const cls = computed(() => crawlStatusClass(props.status))
</script>

<template>
  <span :class="cls">{{ label }}</span>
</template>
