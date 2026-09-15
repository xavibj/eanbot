// Minimal fetch wrapper for the eanbot JSON API (see specs/004-api-web.md).
// Every call resolves with the decoded JSON body (null for 204) or rejects
// with an ApiError carrying a list of Spanish messages ready to be shown.

export class ApiError extends Error {
  constructor(errors, status) {
    const list = Array.isArray(errors) && errors.length ? errors.map(String) : ['la petición ha fallado']
    super(list.join(' · '))
    this.name = 'ApiError'
    this.errors = list
    this.status = Number(status) || 0
  }
}

const NETWORK_ERRORS = ['la petición ha fallado']

async function request(path, options) {
  let res
  try {
    res = await fetch(path, options)
  } catch {
    // Network failures collapse into a single generic message.
    throw new ApiError(NETWORK_ERRORS, 0)
  }

  if (res.status === 204) return null

  let body = null
  try {
    body = await res.json()
  } catch {
    body = null
  }

  if (!res.ok) {
    const errors = body && Array.isArray(body.errors) ? body.errors : NETWORK_ERRORS
    throw new ApiError(errors, res.status)
  }
  if (body === null || typeof body !== 'object') {
    throw new ApiError(NETWORK_ERRORS, res.status)
  }
  return body
}

function get(path) {
  return request(path, { headers: { Accept: 'application/json' } })
}

function send(method, path, body) {
  const options = { method, headers: { Accept: 'application/json' } }
  if (body !== undefined) {
    options.headers['Content-Type'] = 'application/json'
    options.body = JSON.stringify(body)
  }
  return request(path, options)
}

function query(params) {
  const sp = new URLSearchParams()
  for (const [key, value] of Object.entries(params || {})) {
    const raw = String(value ?? '')
    if (raw !== '') sp.set(key, raw)
  }
  const qs = sp.toString()
  return qs ? `?${qs}` : ''
}

export function listCrawls() {
  return get('/api/crawls')
}

export function createCrawl(config) {
  return send('POST', '/api/crawls', config)
}

export function getCrawl(id) {
  return get(`/api/crawls/${encodeURIComponent(id)}`)
}

export function cancelCrawl(id) {
  return send('POST', `/api/crawls/${encodeURIComponent(id)}/cancel`)
}

export function deleteCrawl(id) {
  return send('DELETE', `/api/crawls/${encodeURIComponent(id)}`)
}

export function listPages(id, { status, q, limit, offset } = {}) {
  return get(`/api/crawls/${encodeURIComponent(id)}/pages${query({ status, q, limit, offset })}`)
}

export function getPage(id, pageId) {
  return get(`/api/crawls/${encodeURIComponent(id)}/pages/${encodeURIComponent(pageId)}`)
}

export function listBroken(id, { limit, offset } = {}) {
  return get(`/api/crawls/${encodeURIComponent(id)}/broken${query({ limit, offset })}`)
}

// errorsOf turns anything thrown by the calls above into a list of strings.
export function errorsOf(err) {
  if (err && Array.isArray(err.errors) && err.errors.length) return err.errors.map(String)
  return NETWORK_ERRORS.slice()
}
