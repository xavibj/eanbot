// Display helpers. Everything here is defensive: the API may hand us nulls,
// missing fields or unparseable dates and the UI must still render.

const CRAWL_STATUS_LABELS = {
  running: 'en curso',
  done: 'terminado',
  failed: 'fallido',
  cancelled: 'cancelado',
}

export function crawlStatusLabel(status) {
  const key = String(status ?? '')
  return CRAWL_STATUS_LABELS[key] || key || 'desconocido'
}

export function crawlStatusClass(status) {
  const key = String(status ?? '')
  return `badge badge-${CRAWL_STATUS_LABELS[key] ? key : 'unknown'}`
}

function toDate(value) {
  const raw = String(value ?? '')
  if (raw === '') return null
  const d = new Date(raw)
  return Number.isNaN(d.getTime()) ? null : d
}

export function formatDateTime(value) {
  const d = toDate(value)
  if (!d) return '—'
  try {
    return d.toLocaleString('es-ES', {
      day: '2-digit',
      month: '2-digit',
      year: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return d.toISOString()
  }
}

export function formatMillis(ms) {
  const n = Number(ms)
  if (!Number.isFinite(n) || n < 0) return '—'
  if (n < 1000) return `${Math.round(n)} ms`
  if (n < 60000) return `${(n / 1000).toFixed(1)} s`
  const minutes = Math.floor(n / 60000)
  const seconds = Math.round((n % 60000) / 1000)
  return `${minutes} min ${seconds} s`
}

// crawlDuration: finished_at − started_at, or "en curso" while unfinished.
export function crawlDuration(crawl) {
  const started = toDate(crawl?.started_at)
  const finished = toDate(crawl?.finished_at)
  if (!started) return '—'
  if (!finished) return 'en curso'
  return formatMillis(finished.getTime() - started.getTime())
}

export function formatBytes(size) {
  const n = Number(size)
  if (!Number.isFinite(n) || n < 0) return '—'
  if (n < 1024) return `${Math.round(n)} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} kB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

export function formatNumber(value) {
  const n = Number(value)
  if (!Number.isFinite(n)) return '0'
  try {
    return n.toLocaleString('es-ES')
  } catch {
    return String(n)
  }
}

export function formatBool(value) {
  return value ? 'sí' : 'no'
}

// viaNofollow pages were only reachable through rel=nofollow links.
export function viaNofollowLabel(value) {
  return value ? 'Sí, descubierta solo por enlaces nofollow' : 'No'
}

export function orDash(value) {
  const raw = String(value ?? '')
  return raw === '' ? '—' : raw
}

// statusCodeLabel/statusCodeClass: BLOQ when robots blocked the page, ERR
// when the fetch failed (status 0), the HTTP code otherwise.
export function statusCodeLabel(status, blocked) {
  if (blocked) return 'BLOQ'
  const n = Number(status) || 0
  return n === 0 ? 'ERR' : String(n)
}

export function statusCodeClass(status, blocked) {
  if (blocked) return 'code code-blocked'
  const n = Number(status) || 0
  if (n === 0) return 'code code-err'
  if (n >= 200 && n < 300) return 'code code-2xx'
  if (n >= 300 && n < 400) return 'code code-3xx'
  return 'code code-bad'
}
