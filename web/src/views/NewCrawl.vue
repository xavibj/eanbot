<script setup>
import { reactive, ref } from 'vue'
import { createCrawl, errorsOf } from '../api.js'
import { crawlHref, navigate } from '../router.js'
import ErrorList from '../components/ErrorList.vue'

// Numeric fields are kept as strings (type="text" + inputmode="numeric") and
// coerced on submit: v-model on type="number" silently casts and can break.
const form = reactive({
  seed: 'https://xavibolivar.xavi.net',
  max_pages: '',
  max_depth: '',
  concurrency: '',
  delay_ms: '500',
  include_subdomains: false,
  ignore_robots: false,
  use_sitemaps: true,
})

const errors = ref([])
const sending = ref(false)

function setText(field, event) {
  form[field] = String(event?.target?.value ?? '')
}

function setDigits(field, event) {
  form[field] = String(event?.target?.value ?? '').replace(/[^0-9]/g, '')
}

function setBool(field, event) {
  form[field] = !!event?.target?.checked
}

function numberOrNull(value) {
  const raw = String(value ?? '').trim()
  if (raw === '') return null
  const n = Number(raw)
  return Number.isFinite(n) ? n : null
}

function buildBody() {
  // Only fields the user actually filled in are sent; the server applies the
  // defaults. delay_ms is always sent as a number because 0 is meaningful.
  const body = { seed: String(form.seed ?? '').trim() }
  for (const field of ['max_pages', 'max_depth', 'concurrency']) {
    const n = numberOrNull(form[field])
    if (n !== null) body[field] = n
  }
  const delay = numberOrNull(form.delay_ms)
  body.delay_ms = delay === null ? 0 : delay
  body.include_subdomains = !!form.include_subdomains
  body.ignore_robots = !!form.ignore_robots
  body.use_sitemaps = !!form.use_sitemaps
  return body
}

async function onSubmit() {
  if (sending.value) return
  sending.value = true
  errors.value = []
  try {
    const data = await createCrawl(buildBody())
    // The API may answer with the bare crawl object or wrap it in {crawl}.
    const crawl = data && data.crawl ? data.crawl : data
    const id = crawl && crawl.id
    if (id === undefined || id === null) {
      errors.value = ['la petición ha fallado']
      return
    }
    navigate(crawlHref(id))
  } catch (err) {
    errors.value = errorsOf(err)
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <section>
    <div class="view-header">
      <h1>Nuevo rastreo</h1>
      <a class="btn" href="#/">Volver</a>
    </div>

    <form class="card" novalidate @submit.prevent="onSubmit">
      <div class="form-grid">
        <div class="field field--wide">
          <label for="seed">Semilla</label>
          <input
            id="seed"
            type="text"
            autocomplete="off"
            spellcheck="false"
            placeholder="https://ejemplo.com"
            :value="form.seed"
            :disabled="sending"
            @input="setText('seed', $event)"
          />
          <span class="hint">URL http o https absoluta. Obligatoria.</span>
        </div>

        <div class="field">
          <label for="max_pages">Máx. páginas</label>
          <input
            id="max_pages"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            placeholder="500"
            :value="form.max_pages"
            :disabled="sending"
            @input="setDigits('max_pages', $event)"
          />
          <span class="hint">Vacío = 500.</span>
        </div>

        <div class="field">
          <label for="max_depth">Profundidad máx.</label>
          <input
            id="max_depth"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            placeholder="10"
            :value="form.max_depth"
            :disabled="sending"
            @input="setDigits('max_depth', $event)"
          />
          <span class="hint">Vacío = 10.</span>
        </div>

        <div class="field">
          <label for="concurrency">Concurrencia</label>
          <input
            id="concurrency"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            placeholder="4"
            :value="form.concurrency"
            :disabled="sending"
            @input="setDigits('concurrency', $event)"
          />
          <span class="hint">Peticiones simultáneas. Vacío = 4.</span>
        </div>

        <div class="field">
          <label for="delay_ms">Retardo (ms)</label>
          <input
            id="delay_ms"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            placeholder="500"
            :value="form.delay_ms"
            :disabled="sending"
            @input="setDigits('delay_ms', $event)"
          />
          <span class="hint">Cortesía entre peticiones. 0 = sin retardo.</span>
        </div>
      </div>

      <div class="checks">
        <label class="check">
          <input
            type="checkbox"
            :checked="form.include_subdomains"
            :disabled="sending"
            @change="setBool('include_subdomains', $event)"
          />
          Incluir subdominios
        </label>
        <label class="check">
          <input
            type="checkbox"
            :checked="form.ignore_robots"
            :disabled="sending"
            @change="setBool('ignore_robots', $event)"
          />
          Ignorar robots.txt (solo para sitios propios)
        </label>
        <label class="check">
          <input
            type="checkbox"
            :checked="form.use_sitemaps"
            :disabled="sending"
            @change="setBool('use_sitemaps', $event)"
          />
          Usar sitemaps
        </label>
      </div>

      <div v-if="errors.length" style="margin-top: 16px">
        <ErrorList :errors="errors" />
      </div>

      <div class="form-actions">
        <button type="submit" class="btn btn-primary" :disabled="sending">
          {{ sending ? 'Creando…' : 'Empezar rastreo' }}
        </button>
        <a class="btn" href="#/" :class="sending ? 'is-disabled' : ''">Cancelar</a>
      </div>
    </form>
  </section>
</template>
