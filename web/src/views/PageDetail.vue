<script setup>
import { computed, onMounted, ref } from 'vue'
import { errorsOf, getPage } from '../api.js'
import { formatBool, formatBytes, formatDateTime, formatNumber, orDash, viaNofollowLabel } from '../format.js'
import { crawlHref, pageHref } from '../router.js'
import ErrorList from '../components/ErrorList.vue'
import StatusCode from '../components/StatusCode.vue'

const props = defineProps({
  crawlId: { type: String, required: true },
  pageId: { type: String, required: true },
})

const page = ref(null)
const outlinks = ref([])
const inlinks = ref([])
const outlinksTotal = ref(0)
const inlinksTotal = ref(0)
const loading = ref(true)
const errors = ref([])

let stopped = false

function totalOr(value, fallback) {
  const n = Number(value)
  return Number.isFinite(n) && n >= fallback ? n : fallback
}

const outlinksTruncated = computed(() => outlinksTotal.value > outlinks.value.length)
const inlinksTruncated = computed(() => inlinksTotal.value > inlinks.value.length)

async function load() {
  loading.value = true
  errors.value = []
  page.value = null
  outlinks.value = []
  inlinks.value = []
  outlinksTotal.value = 0
  inlinksTotal.value = 0
  try {
    const data = await getPage(props.crawlId, props.pageId)
    if (stopped) return
    page.value = data.page || null
    outlinks.value = Array.isArray(data.outlinks) ? data.outlinks : []
    inlinks.value = Array.isArray(data.inlinks) ? data.inlinks : []
    // The API caps each list at 500; the totals say how many there really are.
    outlinksTotal.value = totalOr(data.outlinks_total, outlinks.value.length)
    inlinksTotal.value = totalOr(data.inlinks_total, inlinks.value.length)
  } catch (err) {
    if (stopped) return
    errors.value = errorsOf(err)
  } finally {
    if (!stopped) loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <section>
    <div class="view-header">
      <div>
        <h1>Detalle de página</h1>
        <p class="meta-line" v-if="page">
          <span class="mono">{{ page.url }}</span>
        </p>
      </div>
      <div class="btn-row">
        <a class="btn" :href="crawlHref(crawlId)">Volver al rastreo</a>
        <a
          v-if="page"
          class="btn"
          :href="page.url"
          target="_blank"
          rel="noopener noreferrer"
        >Abrir URL</a>
      </div>
    </div>

    <ErrorList :errors="errors" />

    <p v-if="loading" class="loading">Cargando página…</p>

    <template v-else-if="page">
      <div class="card">
        <dl class="deflist">
          <dt>Código</dt>
          <dd><StatusCode :status="page.status" :blocked="page.blocked" /></dd>

          <dt>URL</dt>
          <dd class="mono">{{ page.url }}</dd>

          <dt>Profundidad</dt>
          <dd>{{ page.depth }}</dd>

          <dt>Tipo de contenido</dt>
          <dd>{{ orDash(page.content_type) }}</dd>

          <dt>Tamaño</dt>
          <dd>{{ formatBytes(page.size) }}</dd>

          <dt>Duración</dt>
          <dd>{{ formatNumber(page.duration_ms) }} ms</dd>

          <dt>Título</dt>
          <dd>{{ orDash(page.title) }}</dd>

          <dt>Descripción</dt>
          <dd>{{ orDash(page.description) }}</dd>

          <dt>H1</dt>
          <dd>{{ orDash(page.h1) }}</dd>

          <dt>Canonical</dt>
          <dd class="mono">{{ orDash(page.canonical) }}</dd>

          <dt>Meta robots</dt>
          <dd>{{ orDash(page.meta_robots) }}</dd>

          <dt>X-Robots-Tag</dt>
          <dd>{{ orDash(page.x_robots_tag) }}</dd>

          <dt>Noindex</dt>
          <dd>{{ formatBool(page.noindex) }}</dd>

          <dt>Nofollow</dt>
          <dd>{{ formatBool(page.nofollow) }}</dd>

          <dt>Redirige a</dt>
          <dd class="mono">{{ orDash(page.redirect_to) }}</dd>

          <dt>Vía nofollow</dt>
          <dd>{{ viaNofollowLabel(page.via_nofollow) }}</dd>

          <dt>Bloqueada</dt>
          <dd>{{ formatBool(page.blocked) }}</dd>

          <dt>Error</dt>
          <dd>{{ orDash(page.error) }}</dd>

          <dt>Rastreada</dt>
          <dd>{{ formatDateTime(page.fetched_at) }}</dd>
        </dl>
      </div>

      <div class="card">
        <h2>
          Enlaces salientes ({{ formatNumber(outlinks.length) }} de {{ formatNumber(outlinksTotal) }})
        </h2>
        <p v-if="outlinksTruncated" class="small muted" style="margin: 4px 0 0">
          Mostrando los primeros {{ formatNumber(outlinks.length) }}.
        </p>
        <div v-if="outlinks.length === 0" class="empty">Esta página no enlaza a ninguna otra.</div>
        <div v-else class="table-wrap" style="margin-top: 10px">
          <table>
            <thead>
              <tr>
                <th>URL destino</th>
                <th>Texto</th>
                <th>Nofollow</th>
                <th>En ámbito</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(link, i) in outlinks" :key="i">
                <td class="mono">{{ link.to_url }}</td>
                <td>{{ orDash(link.text) }}</td>
                <td>{{ formatBool(link.nofollow) }}</td>
                <td>{{ formatBool(link.in_scope) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="card">
        <h2>
          Enlaces entrantes ({{ formatNumber(inlinks.length) }} de {{ formatNumber(inlinksTotal) }})
        </h2>
        <p class="small muted" style="margin: 4px 0 0">Estas páginas enlazan a esta URL.</p>
        <p v-if="inlinksTruncated" class="small muted" style="margin: 4px 0 0">
          Mostrando los primeros {{ formatNumber(inlinks.length) }}.
        </p>
        <div v-if="inlinks.length === 0" class="empty">Ninguna página rastreada enlaza aquí.</div>
        <div v-else class="table-wrap" style="margin-top: 10px">
          <table>
            <thead>
              <tr>
                <th>URL origen</th>
                <th>Texto</th>
                <th>Nofollow</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(link, i) in inlinks" :key="i">
                <td class="mono">
                  <a :href="pageHref(crawlId, link.from_page_id)">{{ link.from_url }}</a>
                </td>
                <td>{{ orDash(link.text) }}</td>
                <td>{{ formatBool(link.nofollow) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>

    <div v-else-if="!errors.length" class="card empty">No se ha encontrado la página.</div>
  </section>
</template>
