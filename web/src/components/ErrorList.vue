<script setup>
defineProps({
  errors: { type: Array, default: () => [] },
})
</script>

<template>
  <div v-if="errors && errors.length" class="alert alert-error" role="alert">
    <ul>
      <li v-for="(message, i) in errors" :key="i">{{ message }}</li>
    </ul>
  </div>
</template>
