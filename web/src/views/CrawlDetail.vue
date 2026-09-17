<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { cancelCrawl, errorsOf, getCrawl, getReport, listBroken, listPages } from '../api.js'
import { crawlDuration, formatDateTime, formatMillis, formatNumber, orDash } from '../format.js'
import { navigate, pageHref } from '../router.js'
import CrawlBadge from '../components/CrawlBadge.vue'
import ErrorList from '../components/ErrorList.vue'
import Pagination from '../components/Pagination.vue'
import ReportView from '../components/ReportView.vue'
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
const brokenTotal = ref(0)
const brokenOffset = ref(0)
const brokenLimit = ref(PAGE_SIZE)
const brokenLoaded = ref(false)
const brokenLoading = ref(false)
const brokenErrors = ref([])

const report = ref(null)
const reportLoading = ref(false)
const reportErrors = ref([])

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

const originLabel = computed(() => String(config.value?.origin ?? '').trim())

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
    else if (tab.value === 'broken') await loadBroken(true)
    // the report tab is generated on demand only, never by the 2 s refresh
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
    // Like the pages tab: drop the stale rows before asking again.
    brokenLoading.value = true
    brokenErrors.value = []
    broken.value = []
    brokenTotal.value = 0
  }
  try {
    const data = await listBroken(props.crawlId, {
      limit: brokenLimit.value,
      offset: brokenOffset.value,
    })
    if (stopped || seq !== brokenSeq) return
    broken.value = Array.isArray(data.broken) ? data.broken : []
    brokenTotal.value = Number(data.total) || 0
    const gotLimit = Number(data.limit)
    if (Number.isFinite(gotLimit) && gotLimit > 0) brokenLimit.value = gotLimit
    const gotOffset = Number(data.offset)
    if (Number.isFinite(gotOffset) && gotOffset >= 0) brokenOffset.value = gotOffset
    brokenErrors.value = []
    brokenLoaded.value = true
  } catch (err) {
    if (stopped || seq !== brokenSeq) return
    brokenErrors.value = errorsOf(err)
    if (!silent) {
      broken.value = []
      brokenTotal.value = 0
    }
  } finally {
    if (!stopped && seq === brokenSeq) brokenLoading.value = false
  }
}

function prevBroken() {
  brokenOffset.value = Math.max(0, brokenOffset.value - brokenLimit.value)
  loadBroken()
}

function nextBroken() {
  brokenOffset.value = brokenOffset.value + brokenLimit.value
  loadBroken()
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
  offset.value = Math.max(0, offset.value - limit.value)
  loadPages()
}

function nextPage() {
  offset.value = offset.value + limit.value
  loadPages()
}

// The report is generated only when the user asks for it: switching tabs or
// coming back to an already generated report never re-runs it.
async function generateReport() {
  if (reportLoading.value) return
  reportLoading.value = true
  reportErrors.value = []
  try {
    const data = await getReport(props.crawlId)
    if (stopped) return
    report.value = data.report || null
    if (!report.value) reportErrors.value = ['la petición ha fallado']
  } catch (err) {
    if (stopped) return
    reportErrors.value = errorsOf(err)
  } finally {
    if (!stopped) reportLoading.value = false
  }
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
          <span v-if="originLabel">origen: {{ originLabel }}</span>
          <span v-if="config.insecure_tls">tls sin verificar</span>
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
        <button
          type="button"
          class="tab"
          :class="tab === 'report' ? 'is-active' : ''"
          @click="selectTab('report')"
        >
          Informe
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

        <Pagination
          v-if="!pagesLoading && total > 0"
          :offset="offset"
          :limit="limit"
          :count="pages.length"
          :total="total"
          :disabled="pagesLoading"
          @prev="prevPage"
          @next="nextPage"
        />
      </div>

      <div v-show="tab === 'broken'">
        <ErrorList :errors="brokenErrors" />

        <p v-if="brokenLoading" class="loading">Cargando enlaces rotos…</p>

        <div v-else-if="broken.length === 0" class="card empty">
          No hay enlaces rotos en este rastreo.
        </div>

        <div v-else class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Código</th>
                <th>URL</th>
                <th class="num">Referrers</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="item in broken"
                :key="item.page.id"
                class="clickable"
                @click="openPage(item.page)"
              >
                <td><StatusCode :status="item.page.status" :blocked="item.page.blocked" /></td>
                <td>
                  <span class="truncate mono" :title="item.page.url">{{ item.page.url }}</span>
                  <div v-if="item.page.error" class="small muted">{{ item.page.error }}</div>
                </td>
                <td class="num">{{ formatNumber(item.referrers_count) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <Pagination
          v-if="!brokenLoading && brokenTotal > 0"
          :offset="brokenOffset"
          :limit="brokenLimit"
          :count="broken.length"
          :total="brokenTotal"
          :disabled="brokenLoading"
          @prev="prevBroken"
          @next="nextBroken"
        />
      </div>

      <div v-show="tab === 'report'">
        <ErrorList :errors="reportErrors" />

        <div v-if="!report && !reportLoading" class="card">
          <p class="muted" style="margin: 0 0 12px">
            El informe agrega códigos, profundidades, idiomas, secciones,
            redirecciones, errores y muestras de este rastreo. Se genera bajo
            demanda.
          </p>
          <div class="btn-row">
            <button
              type="button"
              class="btn btn-primary"
              :disabled="isRunning || reportLoading"
              @click="generateReport"
            >
              Generar informe
            </button>
            <button
              v-if="isRunning"
              type="button"
              class="btn"
              :disabled="reportLoading"
              @click="generateReport"
            >
              Generar igualmente
            </button>
          </div>
          <p v-if="isRunning" class="small muted" style="margin: 10px 0 0">
            Disponible al terminar: el rastreo sigue en marcha y el informe sería parcial.
          </p>
        </div>

        <p v-if="reportLoading" class="loading">
          Generando informe, puede tardar unos segundos…
        </p>

        <template v-if="report && !reportLoading">
          <ReportView :report="report" :crawl-id="crawlId" />
          <div class="btn-row" style="margin-top: 16px">
            <button type="button" class="btn btn-sm" @click="generateReport">
              Regenerar
            </button>
          </div>
        </template>
      </div>
    </template>

    <div v-else-if="!headErrors.length" class="card empty">No se ha encontrado el rastreo.</div>
  </section>
</template>
