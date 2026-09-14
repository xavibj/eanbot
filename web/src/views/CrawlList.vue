<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { cancelCrawl, deleteCrawl, errorsOf, listCrawls } from '../api.js'
import { crawlDuration, formatDateTime, formatNumber } from '../format.js'
import { crawlHref, navigate } from '../router.js'
import CrawlBadge from '../components/CrawlBadge.vue'
import ErrorList from '../components/ErrorList.vue'

const crawls = ref([])
const errors = ref([])
const loading = ref(true)
const busyId = ref(0)

let timer = null
let stopped = false

const hasRunning = computed(() => crawls.value.some((c) => c && c.status === 'running'))

function clearTimer() {
  if (timer !== null) {
    clearTimeout(timer)
    timer = null
  }
}

// Poll every 2 s, but only while at least one crawl is running.
function schedule() {
  clearTimer()
  if (stopped || !hasRunning.value) return
  timer = setTimeout(() => {
    timer = null
    load(true)
  }, 2000)
}

async function load(silent = false) {
  if (!silent) {
    loading.value = true
    errors.value = []
    crawls.value = []
  }
  try {
    const data = await listCrawls()
    if (stopped) return
    crawls.value = Array.isArray(data.crawls) ? data.crawls : []
    errors.value = []
  } catch (err) {
    if (stopped) return
    errors.value = errorsOf(err)
    if (!silent) crawls.value = []
  } finally {
    if (!stopped) {
      loading.value = false
      schedule()
    }
  }
}

async function onCancel(crawl) {
  if (busyId.value) return
  busyId.value = Number(crawl.id) || 0
  errors.value = []
  try {
    await cancelCrawl(crawl.id)
  } catch (err) {
    errors.value = errorsOf(err)
  } finally {
    busyId.value = 0
  }
  await load(true)
}

async function onDelete(crawl) {
  if (busyId.value) return
  const seed = String(crawl.seed ?? '')
  if (!window.confirm(`¿Borrar el rastreo de ${seed} y todas sus páginas?`)) return
  busyId.value = Number(crawl.id) || 0
  errors.value = []
  try {
    await deleteCrawl(crawl.id)
  } catch (err) {
    errors.value = errorsOf(err)
  } finally {
    busyId.value = 0
  }
  await load(true)
}

function open(crawl) {
  navigate(crawlHref(crawl.id))
}

onMounted(() => load())
onBeforeUnmount(() => {
  stopped = true
  clearTimer()
})
</script>

<template>
  <section>
    <div class="view-header">
      <div>
        <h1>Rastreos</h1>
        <p class="meta-line" v-if="hasRunning">Actualizando cada 2 s mientras haya rastreos en curso.</p>
      </div>
      <a class="btn btn-primary" href="#/new">Nuevo rastreo</a>
    </div>

    <ErrorList :errors="errors" />

    <p v-if="loading" class="loading">Cargando rastreos…</p>

    <div v-else-if="crawls.length === 0" class="card empty">
      Todavía no hay rastreos. <a href="#/new">Crea el primero</a>.
    </div>

    <div v-else class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Semilla</th>
            <th>Estado</th>
            <th class="num">Páginas</th>
            <th>Inicio</th>
            <th>Duración</th>
            <th>Acciones</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="crawl in crawls" :key="crawl.id">
            <td>
              <a :href="crawlHref(crawl.id)" class="truncate" :title="crawl.seed">{{ crawl.seed }}</a>
            </td>
            <td>
              <CrawlBadge :status="crawl.status" />
              <div v-if="crawl.error" class="small muted">{{ crawl.error }}</div>
            </td>
            <td class="num">{{ formatNumber(crawl.pages_count) }}</td>
            <td class="nowrap">{{ formatDateTime(crawl.started_at) }}</td>
            <td class="nowrap">{{ crawlDuration(crawl) }}</td>
            <td>
              <div class="btn-row">
                <button type="button" class="btn btn-sm" @click="open(crawl)">Abrir</button>
                <button
                  v-if="crawl.status === 'running'"
                  type="button"
                  class="btn btn-sm"
                  :disabled="busyId !== 0"
                  @click="onCancel(crawl)"
                >
                  Cancelar
                </button>
                <button
                  type="button"
                  class="btn btn-sm btn-danger"
                  :disabled="busyId !== 0"
                  @click="onDelete(crawl)"
                >
                  Borrar
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
