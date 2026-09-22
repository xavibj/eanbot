<script setup>
import { computed, reactive, ref } from 'vue'
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
  headers: '',
  origin: '',
  insecure_tls: false,
})

const serverErrors = ref([])
const headerErrors = ref([])
const sending = ref(false)

// Client-side header errors block the submit until the textarea is edited.
const errors = computed(() => [...headerErrors.value, ...serverErrors.value])
const blocked = computed(() => headerErrors.value.length > 0)

function setText(field, event) {
  form[field] = String(event?.target?.value ?? '')
}

function setDigits(field, event) {
  form[field] = String(event?.target?.value ?? '').replace(/[^0-9]/g, '')
}

function setHeaders(event) {
  form.headers = String(event?.target?.value ?? '')
  // Validation happens on submit, not on every keystroke; editing clears it.
  if (headerErrors.value.length) headerErrors.value = []
}

// parseHeaders turns "Nombre: valor" lines into an object. Empty lines are
// ignored; a line without ":" is rejected with its 1-based line number.
function parseHeaders(text) {
  const headers = {}
  const errors = []
  const lines = String(text ?? '').split('\n')
  for (let i = 0; i < lines.length; i += 1) {
    const line = String(lines[i] ?? '').trim()
    if (line === '') continue
    const at = line.indexOf(':')
    if (at < 0) {
      errors.push(`cabecera sin “:” en la línea ${i + 1}`)
      continue
    }
    headers[line.slice(0, at).trim()] = line.slice(at + 1).trim()
  }
  return { headers, errors }
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

function buildBody(headers) {
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
  const origin = String(form.origin ?? '').trim()
  if (origin !== '') body.origin = origin
  body.insecure_tls = !!form.insecure_tls
  if (headers && Object.keys(headers).length > 0) body.headers = headers
  return body
}

async function onSubmit() {
  if (sending.value) return
  const parsed = parseHeaders(form.headers)
  serverErrors.value = []
  if (parsed.errors.length) {
    headerErrors.value = parsed.errors
    return
  }
  headerErrors.value = []
  sending.value = true
  try {
    const data = await createCrawl(buildBody(parsed.headers))
    // The API may answer with the bare crawl object or wrap it in {crawl}.
    const crawl = data && data.crawl ? data.crawl : data
    const id = crawl && crawl.id
    if (id === undefined || id === null) {
      serverErrors.value = ['la petición ha fallado']
      return
    }
    navigate(crawlHref(id))
  } catch (err) {
    serverErrors.value = errorsOf(err)
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

      <div class="field field--wide" style="margin-top: 16px">
        <label for="headers">Cabeceras adicionales</label>
        <textarea
          id="headers"
          rows="3"
          spellcheck="false"
          autocomplete="off"
          placeholder="x-crawler-token: XXXXXXXX"
          :value="form.headers"
          :disabled="sending"
          @input="setHeaders"
        ></textarea>
        <span class="hint">Una por línea, con formato <code>Nombre: valor</code>.</span>
      </div>

      <div class="field field--wide" style="margin-top: 14px">
        <label for="origin">IP del origen (saltar Cloudflare)</label>
        <input
          id="origin"
          class="input-mono"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="203.0.113.10"
          :value="form.origin"
          :disabled="sending"
          @input="setText('origin', $event)"
        />
        <span class="hint">ip o ip:puerto; la cabecera Host y el SNI siguen siendo los del sitio.</span>
      </div>

      <div class="checks" style="margin-top: 14px">
        <label class="check">
          <input
            type="checkbox"
            :checked="form.insecure_tls"
            :disabled="sending"
            @change="setBool('insecure_tls', $event)"
          />
          No verificar el certificado TLS
        </label>
        <span class="hint">Solo para certificados Origin CA de Cloudflare o propios.</span>
      </div>

      <div v-if="errors.length" style="margin-top: 16px">
        <ErrorList :errors="errors" />
      </div>

      <div class="form-actions">
        <button type="submit" class="btn btn-primary" :disabled="sending || blocked">
          {{ sending ? 'Creando…' : 'Empezar rastreo' }}
        </button>
        <a class="btn" href="#/" :class="sending ? 'is-disabled' : ''">Cancelar</a>
      </div>
    </form>
  </section>
</template>
