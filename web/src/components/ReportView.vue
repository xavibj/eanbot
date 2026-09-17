<script setup>
import { computed } from 'vue'
import { reportDownloadUrl } from '../api.js'
import { formatBytes, formatDateTime, formatNumber, orDash } from '../format.js'

const props = defineProps({
  report: { type: Object, required: true },
  crawlId: { type: String, required: true },
})

// Sample buckets keep the order of the Markdown report, with Spanish labels.
const SAMPLE_LABELS = [
  ['3xx', 'Redirecciones (3xx)'],
  ['4xx', 'No encontradas (4xx)'],
  ['5xx', 'Error de servidor (5xx)'],
  ['error', 'Errores de red'],
  ['blocked', 'Bloqueadas por robots'],
  ['noindex', 'Noindex'],
]

function list(value) {
  return Array.isArray(value) ? value : []
}

const byStatus = computed(() => list(props.report.by_status))
const byDepth = computed(() => list(props.report.by_depth))
const byLanguage = computed(() => list(props.report.by_language))
const bySection = computed(() => list(props.report.by_section))
const redirects = computed(() => list(props.report.redirects))
const errors = computed(() => list(props.report.errors))
const topBroken = computed(() => list(props.report.top_broken))
const duplicateTitles = computed(() => list(props.report.duplicate_titles))
const slowest = computed(() => list(props.report.slowest))
const largest = computed(() => list(props.report.largest))

const contentTypes = computed(() => {
  const raw = props.report.content_types
  if (!raw || typeof raw !== 'object') return []
  return Object.entries(raw)
    .map(([key, n]) => ({ key, n: Number(n) || 0 }))
    .sort((a, b) => b.n - a.n || a.key.localeCompare(b.key))
})

const samples = computed(() => {
  const raw = props.report.samples
  if (!raw || typeof raw !== 'object') return []
  return SAMPLE_LABELS
    .map(([key, label]) => ({ key, label, rows: list(raw[key]) }))
    .filter((group) => group.rows.length > 0)
})

const mdHref = computed(() => reportDownloadUrl(props.crawlId, 'md'))
const jsonHref = computed(() => reportDownloadUrl(props.crawlId, 'json'))
</script>

<template>
  <div class="report">
    <div class="report__actions">
      <a class="btn" :href="mdHref" download>Descargar Markdown</a>
      <a class="btn" :href="jsonHref" download>Descargar JSON</a>
      <span class="small muted">
        Informe generado el {{ formatDateTime(report.generated_at) }}
      </span>
    </div>

    <details v-if="byStatus.length" class="report-section" open>
      <summary>Códigos</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th>Código</th><th class="num">Páginas</th></tr>
          </thead>
          <tbody>
            <tr v-for="row in byStatus" :key="row.status">
              <td class="mono">{{ row.status }}</td>
              <td class="num">{{ formatNumber(row.n) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="byDepth.length" class="report-section">
      <summary>Por profundidad</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Profundidad</th><th class="num">Total</th><th class="num">2xx</th>
              <th class="num">3xx</th><th class="num">4xx</th><th class="num">5xx</th>
              <th class="num">Errores</th><th class="num">Bloq.</th><th class="num">Noindex</th>
              <th class="num">ms medios</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in byDepth" :key="row.key">
              <td>{{ row.key }}</td>
              <td class="num">{{ formatNumber(row.total) }}</td>
              <td class="num">{{ formatNumber(row.status_2xx) }}</td>
              <td class="num">{{ formatNumber(row.status_3xx) }}</td>
              <td class="num">{{ formatNumber(row.status_4xx) }}</td>
              <td class="num">{{ formatNumber(row.status_5xx) }}</td>
              <td class="num">{{ formatNumber(row.errors) }}</td>
              <td class="num">{{ formatNumber(row.blocked) }}</td>
              <td class="num">{{ formatNumber(row.noindex) }}</td>
              <td class="num">{{ formatNumber(row.avg_ms) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="byLanguage.length" class="report-section">
      <summary>Por idioma</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Idioma</th><th class="num">Total</th><th class="num">2xx</th>
              <th class="num">3xx</th><th class="num">4xx</th><th class="num">5xx</th>
              <th class="num">Errores</th><th class="num">Bloq.</th><th class="num">Noindex</th>
              <th class="num">ms medios</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in byLanguage" :key="row.key">
              <td>{{ row.key }}</td>
              <td class="num">{{ formatNumber(row.total) }}</td>
              <td class="num">{{ formatNumber(row.status_2xx) }}</td>
              <td class="num">{{ formatNumber(row.status_3xx) }}</td>
              <td class="num">{{ formatNumber(row.status_4xx) }}</td>
              <td class="num">{{ formatNumber(row.status_5xx) }}</td>
              <td class="num">{{ formatNumber(row.errors) }}</td>
              <td class="num">{{ formatNumber(row.blocked) }}</td>
              <td class="num">{{ formatNumber(row.noindex) }}</td>
              <td class="num">{{ formatNumber(row.avg_ms) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="bySection.length" class="report-section">
      <summary>Por sección</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Sección</th><th class="num">Total</th><th class="num">2xx</th>
              <th class="num">3xx</th><th class="num">4xx</th><th class="num">5xx</th>
              <th class="num">Errores</th><th class="num">Bloq.</th><th class="num">Noindex</th>
              <th class="num">ms medios</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in bySection" :key="row.key">
              <td>{{ row.key }}</td>
              <td class="num">{{ formatNumber(row.total) }}</td>
              <td class="num">{{ formatNumber(row.status_2xx) }}</td>
              <td class="num">{{ formatNumber(row.status_3xx) }}</td>
              <td class="num">{{ formatNumber(row.status_4xx) }}</td>
              <td class="num">{{ formatNumber(row.status_5xx) }}</td>
              <td class="num">{{ formatNumber(row.errors) }}</td>
              <td class="num">{{ formatNumber(row.blocked) }}</td>
              <td class="num">{{ formatNumber(row.noindex) }}</td>
              <td class="num">{{ formatNumber(row.avg_ms) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="contentTypes.length" class="report-section">
      <summary>Tipos de contenido</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th>Tipo</th><th class="num">Páginas</th></tr>
          </thead>
          <tbody>
            <tr v-for="row in contentTypes" :key="row.key">
              <td class="mono">{{ row.key }}</td>
              <td class="num">{{ formatNumber(row.n) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="redirects.length" class="report-section" open>
      <summary>Redirecciones</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th>Patrón</th><th class="num">N</th><th>Ejemplo origen</th><th>Ejemplo destino</th></tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in redirects" :key="i">
              <td>{{ row.pattern }}</td>
              <td class="num">{{ formatNumber(row.n) }}</td>
              <td class="mono">{{ orDash(row.sample_from) }}</td>
              <td class="mono">{{ orDash(row.sample_to) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="errors.length" class="report-section" open>
      <summary>Errores</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th>Tipo</th><th class="num">N</th><th>URL de muestra</th></tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in errors" :key="i">
              <td>{{ row.kind }}</td>
              <td class="num">{{ formatNumber(row.n) }}</td>
              <td class="mono">{{ orDash(row.sample_url) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="topBroken.length" class="report-section">
      <summary>Páginas rotas con más referrers</summary>
      <p class="small muted" style="margin: 8px 0 0">
        Sobre {{ formatNumber(report.top_broken_scanned) }} páginas rotas analizadas.
      </p>
      <div class="table-wrap" style="margin-top: 8px">
        <table>
          <thead>
            <tr><th>URL</th><th class="num">Código</th><th>Error</th><th class="num">Referrers</th></tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in topBroken" :key="i">
              <td class="mono">{{ row.url }}</td>
              <td class="num">{{ row.status }}</td>
              <td>{{ orDash(row.error) }}</td>
              <td class="num">{{ formatNumber(row.referrers_count) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="duplicateTitles.length" class="report-section">
      <summary>Títulos duplicados</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th>Título</th><th class="num">N</th><th>URL de muestra</th></tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in duplicateTitles" :key="i">
              <td>{{ row.title }}</td>
              <td class="num">{{ formatNumber(row.n) }}</td>
              <td class="mono">{{ orDash(row.sample_url) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="slowest.length" class="report-section">
      <summary>Páginas más lentas</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>URL</th><th class="num">Código</th><th class="num">ms</th>
              <th class="num">Tamaño</th><th class="num">Prof.</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in slowest" :key="i">
              <td class="mono">{{ row.url }}</td>
              <td class="num">{{ row.status }}</td>
              <td class="num">{{ formatNumber(row.duration_ms) }}</td>
              <td class="num">{{ formatBytes(row.size) }}</td>
              <td class="num">{{ row.depth }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="largest.length" class="report-section">
      <summary>Páginas más grandes</summary>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>URL</th><th class="num">Código</th><th class="num">Tamaño</th>
              <th class="num">ms</th><th class="num">Prof.</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in largest" :key="i">
              <td class="mono">{{ row.url }}</td>
              <td class="num">{{ row.status }}</td>
              <td class="num">{{ formatBytes(row.size) }}</td>
              <td class="num">{{ formatNumber(row.duration_ms) }}</td>
              <td class="num">{{ row.depth }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <details v-if="samples.length" class="report-section">
      <summary>Muestras</summary>
      <div v-for="group in samples" :key="group.key" class="report-subsection">
        <h3>{{ group.label }}</h3>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>URL</th><th class="num">Código</th><th class="num">ms</th>
                <th class="num">Tamaño</th><th class="num">Prof.</th><th>Extra</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(row, i) in group.rows" :key="i">
                <td class="mono">{{ row.url }}</td>
                <td class="num">{{ row.status }}</td>
                <td class="num">{{ formatNumber(row.duration_ms) }}</td>
                <td class="num">{{ formatBytes(row.size) }}</td>
                <td class="num">{{ row.depth }}</td>
                <td>{{ orDash(row.extra) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </details>
  </div>
</template>
