<script setup>
import { computed } from 'vue'
import { route } from './router.js'
import CrawlDetail from './views/CrawlDetail.vue'
import CrawlList from './views/CrawlList.vue'
import NewCrawl from './views/NewCrawl.vue'
import NotFound from './views/NotFound.vue'
import PageDetail from './views/PageDetail.vue'

// Remounting on every route change keeps each view's timers and state local.
const viewKey = computed(() => {
  const r = route.value
  return [r.name, r.crawlId || '', r.pageId || ''].join(':')
})
</script>

<template>
  <header class="app-header">
    <div class="app-header__inner">
      <a class="brand" href="#/">ean<span>bot</span></a>
      <nav class="app-nav">
        <a href="#/" :class="route.name === 'list' ? 'is-active' : ''">Rastreos</a>
        <a href="#/new" :class="route.name === 'new' ? 'is-active' : ''">Nuevo rastreo</a>
      </nav>
    </div>
  </header>

  <main class="container">
    <CrawlList v-if="route.name === 'list'" :key="viewKey" />
    <NewCrawl v-else-if="route.name === 'new'" :key="viewKey" />
    <CrawlDetail
      v-else-if="route.name === 'crawl'"
      :key="viewKey"
      :crawl-id="route.crawlId"
    />
    <PageDetail
      v-else-if="route.name === 'page'"
      :key="viewKey"
      :crawl-id="route.crawlId"
      :page-id="route.pageId"
    />
    <NotFound v-else :key="viewKey" />
  </main>
</template>
