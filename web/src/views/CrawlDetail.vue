<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { cancelCrawl, errorsOf, getCrawl, listBroken, listPages } from '../api.js'
import { crawlDuration, formatDateTime, formatMillis, formatNumber, orDash } from '../format.js'
import { navigate, pageHref } from '../router.js'
import CrawlBadge from '../components/CrawlBadge.vue'
import ErrorList from '../components/ErrorList.vue'
import StatusCode from '../components/StatusCode.vue'
import SummaryCards from '../components/SummaryCards.vue'

const props = defineProps({
  crawlId: { type: String, required: true },
})

const PAGE_SIZE = 100

const STATUS_OPTIONS = [
  { value: '', label: 'Todos los códigos' },
  { value: '2xx', label: '2xx — correctas' },
  { value: '3xx', label: '3xx — redirecciones' },
  { value: '4xx', label: '4xx — no encontradas' },
  { value: '5xx', label: '5xx — error de servidor' },
  { value: 'error', label: 'Errores de red' },
  { value: 'blocked', label: 'Bloqueadas por robots' },
]

const crawl = ref(null)
const summary = ref(null)
const running = ref(false)
const headErrors = ref([])
const headLoading = ref(true)
const cancelling = ref(false)

const tab = ref('pages')

const statusFilter = ref('')
const search = ref('')
const offset = ref(0)
const limit = ref(PAGE_SIZE)
const pages = ref([])
const total = ref(0)
const pagesLoading = ref(false)
const pagesErrors = ref([])

const broken = ref([])
const brokenLoaded = ref(false)
const brokenLoading = ref(false)
const brokenErrors = ref([])

let stopped = false
let timer = null
let searchTimer = null
let pagesSeq = 0
let brokenSeq = 0

const isRunning = computed(() => running.value || crawl.value?.status === 'running')

const config = computed(() => {
  const raw = crawl.value?.config
  if (!raw) return null
  if (typeof raw === 'object') return raw
  try {
    return JSON.parse(String(raw))
  } catch {
    return null
  }
})

// Only header names are shown: values may carry access tokens.
const headerNames = computed(() => {
  const raw = config.value?.headers
  if (!raw || typeof raw !== 'object') return ''
  return Object.keys(raw).join(', ')
})

const rangeLabel = computed(() => {
  if (total.value === 0) return '0 páginas'
  const from = offset.value + 1
  const to = Math.min(offset.value + pages.value.length, total.value)
  return `${formatNumber(from)}–${formatNumber(to)} de ${formatNumber(total.value)}`
})

const canPrev = computed(() => offset.value > 0 && !pagesLoading.value)
const canNext = computed(() => offset.value + limit.value < total.value && !pagesLoading.value)

function clearTimers() {
  if (timer !== null) {
    clearTimeout(timer)
    timer = null
  }
  if (searchTimer !== null) {
    clearTimeout(searchTimer)
    searchTimer = null
  }
}

// Refresh every 2 s, only while the crawl is running.
function schedule() {
  if (timer !== null) {
    clearTimeout(timer)
    timer = null
  }
  if (stopped || !isRunning.value) return
  timer = setTimeout(async () => {
    timer = null
    await loadHead(true)
    if (stopped) return
    if (tab.value === 'pages') await loadPages(true)
    else await loadBroken(true)
  }, 2000)
}

async function loadHead(silent = false) {
  if (!silent) {
    headLoading.value = true
    headErrors.value = []
  }
  try {
    const data = await getCrawl(props.crawlId)
    if (stopped) return
    crawl.value = data.crawl || null
    summary.value = data.summary || null
    running.value = !!data.running
    headErrors.value = []
  } catch (err) {
    if (stopped) return
    headErrors.value = errorsOf(err)
    if (!silent) {
      crawl.value = null
      summary.value = null
      running.value = false
    }
  } finally {
    if (!stopped) {
      headLoading.value = false
      schedule()
    }
  }
}

async function loadPages(silent = false) {
  const seq = ++pagesSeq
  if (!silent) {
    // Any filter/search/page change drops the stale rows before asking.
    pagesLoading.value = true
    pagesErrors.value = []
    pages.value = []
    total.value = 0
  }
  try {
    const data = await listPages(props.crawlId, {
      status: statusFilter.value,
      q: search.value,
      limit: limit.value,
      offset: offset.value,
    })
    if (stopped || seq !== pagesSeq) return
    pages.value = Array.isArray(data.pages) ? data.pages : []
    total.value = Number(data.total) || 0
    const gotLimit = Number(data.limit)
    if (Number.isFinite(gotLimit) && gotLimit > 0) limit.value = gotLimit
    const gotOffset = Number(data.offset)
    if (Number.isFinite(gotOffset) && gotOffset >= 0) offset.value = gotOffset
    pagesErrors.value = []
  } catch (err) {
    if (stopped || seq !== pagesSeq) return
    pagesErrors.value = errorsOf(err)
    if (!silent) {
      pages.value = []
      total.value = 0
    }
  } finally {
    if (!stopped && seq === pagesSeq) pagesLoading.value = false
  }
}

async function loadBroken(silent = false) {
  const seq = ++brokenSeq
  if (!silent) {
    brokenLoading.value = true
    brokenErrors.value = []
    broken.value = []
  }
  try {
    const data = await listBroken(props.crawlId)
    if (stopped || seq !== brokenSeq) return
    broken.value = Array.isArray(data.broken) ? data.broken : []
    brokenErrors.value = []
    brokenLoaded.value = true
  } catch (err) {
    if (stopped || seq !== brokenSeq) return
    brokenErrors.value = errorsOf(err)
    if (!silent) broken.value = []
  } finally {
    if (!stopped && seq === brokenSeq) brokenLoading.value = false
  }
}

function onStatusChange(event) {
  statusFilter.value = String(event?.target?.value ?? '')
  offset.value = 0
  loadPages()
}

function onSearchInput(event) {
  const value = String(event?.target?.value ?? '')
  search.value = value
  if (searchTimer !== null) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = null
    offset.value = 0
    loadPages()
  }, 300)
}

function prevPage() {
  if (!canPrev.value) return
  offset.value = Math.max(0, offset.value - limit.value)
  loadPages()
}

function nextPage() {
  if (!canNext.value) return
  offset.value = offset.value + limit.value
  loadPages()
}

function selectTab(name) {
  if (tab.value === name) return
  tab.value = name
  if (name === 'broken' && !brokenLoaded.value) loadBroken()
  schedule()
}

function openPage(page) {
  if (!page || page.id === undefined) return
  navigate(pageHref(props.crawlId, page.id))
}

async function onCancel() {
  if (cancelling.value) return
  cancelling.value = true
  headErrors.value = []
  try {
    await cancelCrawl(props.crawlId)
  } catch (err) {
    headErrors.value = errorsOf(err)
  } finally {
    cancelling.value = false
  }
  await loadHead(true)
}

watch(isRunning, () => schedule())

onMounted(() => {
  loadHead()
  loadPages()
})

onBeforeUnmount(() => {
  stopped = true
  clearTimers()
})
</script>

<template>
  <section>
    <div class="view-header">
      <div>
        <h1>
          <span v-if="crawl">{{ crawl.seed }}</span>
          <span v-else>Rastreo {{ crawlId }}</span>
        </h1>
        <p class="meta-line" v-if="crawl">
          <span><CrawlBadge :status="crawl.status" /></span>
          <span>Inicio: {{ formatDateTime(crawl.started_at) }}</span>
          <span>Duración: {{ crawlDuration(crawl) }}</span>
          <span>Páginas: {{ formatNumber(crawl.pages_count) }}</span>
        </p>
        <p class="meta-line" v-if="config">
          <span>Máx. páginas: {{ config.max_pages }}</span>
          <span>Profundidad: {{ config.max_depth }}</span>
          <span>Concurrencia: {{ config.concurrency }}</span>
          <span>Retardo: {{ config.delay_ms }} ms</span>
          <span v-if="config.include_subdomains">subdominios incluidos</span>
          <span v-if="config.ignore_robots">robots.txt ignorado</span>
          <span v-if="config.use_sitemaps">sitemaps</span>
          <span v-if="headerNames">cabeceras: {{ headerNames }}</span>
        </p>
      </div>
      <div class="btn-row">
        <button
          v-if="isRunning"
          type="button"
          class="btn"
          :disabled="cancelling"
          @click="onCancel"
        >
          {{ cancelling ? 'Cancelando…' : 'Cancelar rastreo' }}
        </button>
        <a class="btn" href="#/">Todos los rastreos</a>
      </div>
    </div>

    <ErrorList :errors="headErrors" />

    <p v-if="headLoading" class="loading">Cargando rastreo…</p>

    <template v-else-if="crawl">
      <div v-if="crawl.error" class="alert alert-error" style="margin-bottom: 16px">
        Error del rastreo: {{ crawl.error }}
      </div>

      <SummaryCards :summary="summary" />
      <p class="meta-line" style="margin-top: 8px" v-if="summary">
        <span>Profundidad máx.: {{ formatNumber(summary.max_depth) }}</span>
        <span>Tiempo medio: {{ formatMillis(summary.avg_duration_ms) }}</span>
      </p>

      <div class="tabs" style="margin-top: 20px">
        <button
          type="button"
          class="tab"
          :class="tab === 'pages' ? 'is-active' : ''"
          @click="selectTab('pages')"
        >
          Páginas
        </button>
        <button
          type="button"
          class="tab"
          :class="tab === 'broken' ? 'is-active' : ''"
          @click="selectTab('broken')"
        >
          Enlaces rotos
        </button>
      </div>

      <div v-show="tab === 'pages'">
        <div class="filters">
          <div class="field field--status">
            <label for="status">Estado</label>
            <select
              id="status"
              :value="statusFilter"
              :disabled="pagesLoading"
              @change="onStatusChange"
            >
              <option v-for="opt in STATUS_OPTIONS" :key="opt.value" :value="opt.value">
                {{ opt.label }}
              </option>
            </select>
          </div>
          <div class="field">
            <label for="q">Buscar</label>
            <input
              id="q"
              type="text"
              autocomplete="off"
              placeholder="URL o título"
              :value="search"
              :disabled="pagesLoading"
              @input="onSearchInput"
            />
          </div>
        </div>

        <ErrorList :errors="pagesErrors" />

        <p v-if="pagesLoading" class="loading">Cargando páginas…</p>

        <div v-else-if="pages.length === 0" class="card empty">
          No hay páginas que coincidan con el filtro.
        </div>

        <div v-else class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Código</th>
                <th>URL</th>
                <th>Título</th>
                <th>Tipo</th>
                <th class="num">Prof.</th>
                <th class="num">ms</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="page in pages"
                :key="page.id"
                class="clickable"
                @click="openPage(page)"
              >
                <td><StatusCode :status="page.status" :blocked="page.blocked" /></td>
                <td>
                  <span class="truncate mono" :title="page.url">{{ page.url }}</span>
                </td>
                <td>
                  <span class="truncate" :title="page.title">{{ orDash(page.title) }}</span>
                </td>
                <td class="nowrap">{{ orDash(page.content_type) }}</td>
                <td class="num">{{ page.depth }}</td>
                <td class="num">{{ formatNumber(page.duration_ms) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="pagination" v-if="!pagesLoading && total > 0">
          <span>{{ rangeLabel }}</span>
          <span class="btn-row">
            <button type="button" class="btn btn-sm" :disabled="!canPrev" @click="prevPage">
              Anterior
            </button>
            <button type="button" class="btn btn-sm" :disabled="!canNext" @click="nextPage">
              Siguiente
            </button>
          </span>
        </div>
      </div>

      <div v-show="tab === 'broken'">
        <ErrorList :errors="brokenErrors" />

        <p v-if="brokenLoading" class="loading">Cargando enlaces rotos…</p>

        <div v-else-if="broken.length === 0" class="card empty">
          No hay enlaces rotos en este rastreo.
        </div>

        <div v-else>
          <div v-for="item in broken" :key="item.page.id" class="card broken-item">
            <div class="broken-item__head">
              <StatusCode :status="item.page.status" :blocked="item.page.blocked" />
              <a class="broken-item__url" :href="pageHref(crawlId, item.page.id)">
                {{ item.page.url }}
              </a>
            </div>
            <p v-if="item.page.error" class="small muted" style="margin: 6px 0 0">
              {{ item.page.error }}
            </p>
            <p class="small muted" style="margin: 8px 0 0">
              Enlazada desde {{ formatNumber((item.referrers || []).length) }} página(s):
            </p>
            <ul class="referrers">
              <li v-for="(referrer, i) in item.referrers || []" :key="i">
                <a :href="pageHref(crawlId, referrer.from_page_id)">{{ referrer.from_url }}</a>
                <span v-if="referrer.text" class="anchor"> — «{{ referrer.text }}»</span>
                <span v-if="referrer.nofollow" class="pill" style="margin-left: 6px">nofollow</span>
              </li>
            </ul>
          </div>
        </div>
      </div>
    </template>

    <div v-else-if="!headErrors.length" class="card empty">No se ha encontrado el rastreo.</div>
  </section>
</template>
