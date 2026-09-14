// Hash based routing: no router library, the view is a pure function of
// location.hash so that reloading keeps the current view.
import { computed, ref } from 'vue'

function strip(hash) {
  const raw = String(hash ?? '')
  return raw.startsWith('#') ? raw.slice(1) : raw
}

const current = ref(strip(window.location.hash))

window.addEventListener('hashchange', () => {
  current.value = strip(window.location.hash)
})

function parse(path) {
  const clean = String(path ?? '').split('?')[0]
  const parts = clean.split('/').filter((p) => p !== '')

  if (parts.length === 0) return { name: 'list' }
  if (parts.length === 1 && parts[0] === 'new') return { name: 'new' }

  if (parts[0] === 'crawls' && /^[0-9]+$/.test(parts[1] ?? '')) {
    if (parts.length === 2) return { name: 'crawl', crawlId: parts[1] }
    if (parts.length === 4 && parts[2] === 'pages' && /^[0-9]+$/.test(parts[3])) {
      return { name: 'page', crawlId: parts[1], pageId: parts[3] }
    }
  }
  return { name: 'notfound' }
}

export const route = computed(() => parse(current.value))

// navigate changes the hash (and therefore the view). Writing the same hash
// twice fires no hashchange event, so the ref is refreshed by hand.
export function navigate(to) {
  const target = String(to ?? '#/')
  const hash = target.startsWith('#') ? target : `#${target}`
  if (window.location.hash === hash) {
    current.value = strip(hash)
    return
  }
  window.location.hash = hash
}

export function crawlHref(crawlId) {
  return `#/crawls/${crawlId}`
}

export function pageHref(crawlId, pageId) {
  return `#/crawls/${crawlId}/pages/${pageId}`
}
