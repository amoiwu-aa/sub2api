<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- Compact Home Page -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <header class="border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-dark-800">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <img
            :src="siteLogo || '/ringstar-logo.jpg?v=3'"
            alt="Logo"
            class="h-9 w-9 shrink-0 rounded-lg object-contain"
          />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <button
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-10 shrink-0 items-center justify-center rounded-lg bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="flex min-w-0 flex-1 items-center justify-center px-4 py-16 sm:px-6">
      <div class="min-w-0 max-w-2xl text-center">
        <img
          :src="siteLogo || '/ringstar-logo.jpg?v=3'"
          alt="Logo"
          class="mx-auto mb-6 h-20 w-20 rounded-2xl object-contain"
        />
        <h1 class="[overflow-wrap:anywhere] text-3xl font-bold md:text-4xl">{{ siteName }}</h1>
        <p class="mt-4 whitespace-pre-wrap [overflow-wrap:anywhere] text-base text-gray-600 dark:text-dark-300">{{ siteSubtitle }}</p>
        <router-link
          :to="isAuthenticated ? dashboardPath : '/login'"
          class="mt-8 inline-flex min-h-10 items-center justify-center rounded-lg bg-primary-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-primary-700"
        >
          {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
        </router-link>
      </div>
    </main>

    <footer class="min-w-0 border-t border-gray-200 px-4 py-5 text-center text-sm text-gray-500 [overflow-wrap:anywhere] sm:px-6 dark:border-dark-800 dark:text-dark-400">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    class="home-cosmos relative flex min-h-screen flex-col overflow-hidden"
    :class="isDark ? 'home-cosmos--dark bg-[#071525]' : 'home-cosmos--light bg-white'"
  >
    <GalaxyBackground v-if="isDark" />

    <!-- Header -->
    <header class="relative z-20 px-6 py-4">
      <nav class="mx-auto flex max-w-6xl items-center justify-between">
        <!-- Logo -->
        <div class="flex items-center">
          <div
            class="logo-mark h-36 w-36 sm:h-44 sm:w-44"
            :class="isDark ? 'logo-mark--dark' : 'logo-mark--light'"
          >
            <img :src="siteLogo || '/ringstar-logo.jpg?v=3'" alt="Logo" class="h-full w-full object-contain" />
          </div>
        </div>

        <!-- Nav Actions -->
        <div class="flex items-center gap-3">
          <LocaleSwitcher />

          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="rounded-full p-2 transition-colors"
            :class="isDark ? 'text-slate-300 hover:bg-white/10 hover:text-white' : 'home-ctrl-light'"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" class="icon-twinkle" />
          </a>

          <button
            @click="toggleTheme"
            class="rounded-full p-2 transition-colors"
            :class="isDark ? 'text-slate-300 hover:bg-white/10 hover:text-white' : 'home-ctrl-light'"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" class="icon-twinkle" />
            <Icon v-else name="moon" size="md" class="icon-twinkle" />
          </button>

          <router-link
            v-if="isAuthenticated"
            :to="dashboardPath"
            class="inline-flex items-center gap-1.5 rounded-full py-1 pl-1 pr-2.5 transition-colors"
            :class="isDark ? 'bg-white/10 ring-1 ring-white/15 hover:bg-white/15' : 'home-login-chip'"
          >
            <span
              class="flex h-5 w-5 items-center justify-center rounded-full bg-gradient-to-br from-cyan-400 to-teal-500 text-[10px] font-semibold text-white"
            >
              {{ userInitial }}
            </span>
            <span class="text-xs font-medium text-white">{{ t('home.dashboard') }}</span>
            <svg
              class="h-3 w-3 text-slate-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M4.5 19.5l15-15m0 0H8.25m11.25 0v11.25"
              />
            </svg>
          </router-link>
          <router-link
            v-else
            to="/login"
            class="inline-flex items-center rounded-full px-3.5 py-1.5 text-xs font-semibold transition-colors"
            :class="isDark ? 'bg-white/10 ring-1 ring-white/15 hover:bg-white/15 text-white' : 'home-login-chip'"
          >
            {{ t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <!-- Main Content -->
    <main class="relative z-10 flex-1 px-6 py-16">
      <div class="mx-auto max-w-6xl">
        <!-- Hero Section -->
        <div class="mb-16 flex flex-col items-center text-center">
          <h1
            class="hero-title mb-4 max-w-3xl text-4xl font-bold md:text-5xl lg:text-6xl"
            :class="isDark ? 'text-white' : 'text-[#2C4A5E]'"
          >
            {{ siteName }}
          </h1>
          <p
            class="mb-8 max-w-2xl text-lg md:text-xl"
            :class="isDark ? 'text-slate-300' : 'text-[#6B8796]'"
          >
            {{ siteSubtitle }}
          </p>
          <div>
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="btn px-8 py-3 text-base"
              :class="isDark ? 'btn-primary shadow-lg shadow-cyan-500/30' : 'home-cta-light'"
            >
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
              <Icon name="arrowRight" size="md" class="ml-2 icon-nudge" :stroke-width="2" />
            </router-link>
          </div>
        </div>

        <!-- Connect: API endpoints with one-click copy -->
        <section class="connect-panel mb-12" :aria-label="t('home.connect.title')">
          <div class="mb-6 text-center">
            <h2
              class="text-xl font-bold md:text-2xl"
              :class="isDark ? 'text-white' : 'text-[#2C4A5E]'"
            >
              {{ t('home.connect.title') }}
            </h2>
            <p
              class="mt-1.5 text-sm"
              :class="isDark ? 'text-slate-300' : 'text-[#6B8796]'"
            >
              {{ t('home.connect.subtitle') }}
            </p>
          </div>

          <!-- Primary base URL -->
          <div class="connect-primary">
            <span class="connect-primary-label">{{ t('home.connect.primaryLabel') }}</span>
            <code
              class="connect-primary-url"
              role="button"
              tabindex="0"
              :title="t('home.connect.copyAria')"
              @click="copyEndpoint('primary', apiBaseUrl)"
              @keydown.enter.prevent="copyEndpoint('primary', apiBaseUrl)"
              @keydown.space.prevent="copyEndpoint('primary', apiBaseUrl)"
            >{{ apiBaseUrl }}</code>
            <button
              type="button"
              class="connect-copy-btn"
              :class="{ 'is-copied': copiedKey === 'primary' }"
              :aria-label="t('home.connect.copyAria')"
              @click="copyEndpoint('primary', apiBaseUrl)"
            >
              <Icon :name="copiedKey === 'primary' ? 'checkCircle' : 'copy'" size="sm" />
              {{ copiedKey === 'primary' ? t('home.connect.copied') : t('home.connect.copy') }}
            </button>
          </div>

          <!-- Backup / accelerated lines configured by admin -->
          <div v-if="customEndpoints.length > 0" class="mt-3 flex flex-wrap justify-center gap-2">
            <button
              v-for="(ep, index) in customEndpoints"
              :key="ep.endpoint"
              type="button"
              class="connect-alt-chip"
              :class="{ 'is-copied': copiedKey === `custom-${index}` }"
              :title="ep.description || t('home.connect.copyAria')"
              @click="copyEndpoint(`custom-${index}`, ep.endpoint)"
            >
              <span class="connect-alt-name">{{ ep.name }}</span>
              <code class="connect-alt-url">{{ ep.endpoint }}</code>
              <Icon :name="copiedKey === `custom-${index}` ? 'checkCircle' : 'copy'" size="sm" class="shrink-0" />
            </button>
          </div>

          <!-- Per-client setup -->
          <p class="connect-clients-title">{{ t('home.connect.clientsTitle') }}</p>
          <div class="grid gap-3 md:grid-cols-2">
            <div v-for="item in clientEndpoints" :key="item.key" class="connect-client-row">
              <div class="min-w-0 flex-1">
                <div
                  class="text-sm font-semibold"
                  :class="isDark ? 'text-white' : 'text-[#2C4A5E]'"
                >
                  {{ t(`home.connect.clients.${item.key}.name`) }}
                </div>
                <div
                  class="mt-0.5 text-xs"
                  :class="isDark ? 'text-slate-400' : 'text-[#6B8796]'"
                >
                  {{ t(`home.connect.clients.${item.key}.hint`) }}
                </div>
                <code
                  class="connect-client-url"
                  role="button"
                  tabindex="0"
                  :title="t('home.connect.copyAria')"
                  @click="copyEndpoint(item.key, item.url)"
                  @keydown.enter.prevent="copyEndpoint(item.key, item.url)"
                  @keydown.space.prevent="copyEndpoint(item.key, item.url)"
                >{{ item.url }}</code>
              </div>
              <button
                type="button"
                class="connect-copy-icon"
                :class="{ 'is-copied': copiedKey === item.key }"
                :aria-label="t('home.connect.copyAria')"
                @click="copyEndpoint(item.key, item.url)"
              >
                <Icon :name="copiedKey === item.key ? 'checkCircle' : 'copy'" size="sm" />
              </button>
            </div>
          </div>
        </section>

        <!-- Feature Tags -->
        <div class="mb-12 flex flex-wrap items-center justify-center gap-4 md:gap-6">
          <div class="cosmos-chip">
            <span class="icon-badge icon-float delay-0">
              <Icon name="swap" size="sm" class="text-cyan-300" />
            </span>
            <span class="text-sm font-medium text-slate-100">{{
              t('home.tags.subscriptionToApi')
            }}</span>
          </div>
          <div class="cosmos-chip">
            <span class="icon-badge icon-float delay-1">
              <Icon name="shield" size="sm" class="text-cyan-300" />
            </span>
            <span class="text-sm font-medium text-slate-100">{{
              t('home.tags.stickySession')
            }}</span>
          </div>
          <div class="cosmos-chip">
            <span class="icon-badge icon-float delay-2">
              <Icon name="chart" size="sm" class="text-cyan-300" />
            </span>
            <span class="text-sm font-medium text-slate-100">{{
              t('home.tags.realtimeBilling')
            }}</span>
          </div>
        </div>

        <!-- Features Grid -->
        <div class="mb-12 grid gap-6 md:grid-cols-3">
          <div class="cosmos-card group">
            <div class="feature-icon feature-icon--blue icon-orbit delay-0">
              <Icon name="server" size="lg" class="text-white icon-bob" />
            </div>
            <h3 class="mb-2 text-lg font-semibold text-white">
              {{ t('home.features.unifiedGateway') }}
            </h3>
            <p class="text-sm leading-relaxed text-slate-300">
              {{ t('home.features.unifiedGatewayDesc') }}
            </p>
          </div>

          <div class="cosmos-card group">
            <div class="feature-icon feature-icon--teal icon-orbit delay-1">
              <svg
                class="h-6 w-6 text-white icon-bob"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                stroke-width="1.5"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M18 18.72a9.094 9.094 0 003.741-.479 3 3 0 00-4.682-2.72m.94 3.198l.001.031c0 .225-.012.447-.037.666A11.944 11.944 0 0112 21c-2.17 0-4.207-.576-5.963-1.584A6.062 6.062 0 016 18.719m12 0a5.971 5.971 0 00-.941-3.197m0 0A5.995 5.995 0 0012 12.75a5.995 5.995 0 00-5.058 2.772m0 0a3 3 0 00-4.681 2.72 8.986 8.986 0 003.74.477m.94-3.197a5.971 5.971 0 00-.94 3.197M15 6.75a3 3 0 11-6 0 3 3 0 016 0zm6 3a2.25 2.25 0 11-4.5 0 2.25 2.25 0 014.5 0zm-13.5 0a2.25 2.25 0 11-4.5 0 2.25 2.25 0 014.5 0z"
                />
              </svg>
            </div>
            <h3 class="mb-2 text-lg font-semibold text-white">
              {{ t('home.features.multiAccount') }}
            </h3>
            <p class="text-sm leading-relaxed text-slate-300">
              {{ t('home.features.multiAccountDesc') }}
            </p>
          </div>

          <div class="cosmos-card group">
            <div class="feature-icon feature-icon--sky icon-orbit delay-2">
              <svg
                class="h-6 w-6 text-white icon-bob"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                stroke-width="1.5"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z"
                />
              </svg>
            </div>
            <h3 class="mb-2 text-lg font-semibold text-white">
              {{ t('home.features.balanceQuota') }}
            </h3>
            <p class="text-sm leading-relaxed text-slate-300">
              {{ t('home.features.balanceQuotaDesc') }}
            </p>
          </div>
        </div>

        <!-- Supported Providers -->
        <div class="mb-8 text-center">
          <h2
            class="mb-3 text-2xl font-bold"
            :class="isDark ? 'text-white' : 'text-[#2C4A5E]'"
          >
            {{ t('home.providers.title') }}
          </h2>
          <p
            class="text-sm"
            :class="isDark ? 'text-slate-300' : 'text-[#6B8796]'"
          >
            {{ t('home.providers.description') }}
          </p>
        </div>

        <div class="mb-16 flex flex-wrap items-center justify-center gap-4">
          <div class="provider-chip">
            <div class="provider-mark provider-mark--orange icon-spin-soft delay-0">
              <span class="text-xs font-bold text-white">C</span>
            </div>
            <span class="text-sm font-medium text-slate-100">{{ t('home.providers.claude') }}</span>
            <span class="provider-tag">{{ t('home.providers.supported') }}</span>
          </div>
          <div class="provider-chip">
            <div class="provider-mark provider-mark--green icon-spin-soft delay-1">
              <span class="text-xs font-bold text-white">G</span>
            </div>
            <span class="text-sm font-medium text-slate-100">GPT</span>
            <span class="provider-tag">{{ t('home.providers.supported') }}</span>
          </div>
          <div class="provider-chip">
            <div class="provider-mark provider-mark--blue icon-spin-soft delay-2">
              <span class="text-xs font-bold text-white">G</span>
            </div>
            <span class="text-sm font-medium text-slate-100">{{ t('home.providers.gemini') }}</span>
            <span class="provider-tag">{{ t('home.providers.supported') }}</span>
          </div>
          <div class="provider-chip">
            <div class="provider-mark provider-mark--rose icon-spin-soft delay-3">
              <span class="text-xs font-bold text-white">A</span>
            </div>
            <span class="text-sm font-medium text-slate-100">{{ t('home.providers.antigravity') }}</span>
            <span class="provider-tag">{{ t('home.providers.supported') }}</span>
          </div>
          <div class="provider-chip">
            <div class="provider-mark provider-mark--black icon-spin-soft delay-4">
              <span class="text-xs font-bold text-white">X</span>
            </div>
            <span class="text-sm font-medium text-slate-100">{{ t('home.providers.grok') }}</span>
            <span class="provider-tag">{{ t('home.providers.supported') }}</span>
          </div>
          <div class="provider-chip">
            <div class="provider-mark provider-mark--violet icon-spin-soft delay-0">
              <span class="text-xs font-bold text-white">C</span>
            </div>
            <span class="text-sm font-medium text-slate-100">{{ t('home.providers.cursor') }}</span>
            <span class="provider-tag">{{ t('home.providers.supported') }}</span>
          </div>
          <div class="provider-chip">
            <div class="provider-mark provider-mark--purple icon-spin-soft delay-1">
              <span class="text-xs font-bold text-white">K</span>
            </div>
            <span class="text-sm font-medium text-slate-100">{{ t('home.providers.kiro') }}</span>
            <span class="provider-tag">{{ t('home.providers.supported') }}</span>
          </div>
          <div class="provider-chip provider-chip--muted">
            <div class="provider-mark provider-mark--gray icon-spin-soft delay-4">
              <span class="text-xs font-bold text-white">+</span>
            </div>
            <span class="text-sm font-medium text-slate-200">{{ t('home.providers.more') }}</span>
            <span class="provider-tag provider-tag--muted">{{ t('home.providers.soon') }}</span>
          </div>
        </div>
      </div>
    </main>

    <footer
      class="relative z-10 border-t px-6 py-8"
      :class="isDark ? 'border-white/10' : 'border-[#D5EAF3]'"
    >
      <div
        class="mx-auto flex max-w-6xl flex-col items-center justify-center gap-4 text-center sm:flex-row sm:text-left"
      >
        <p
          class="text-sm"
          :class="isDark ? 'text-slate-400' : 'text-[#6B8796]'"
        >
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex items-center gap-4">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm transition-colors"
            :class="isDark ? 'text-slate-400 hover:text-white' : 'text-[#6B8796] hover:text-[#2C4A5E]'"
          >
            {{ t('home.docs') }}
          </a>
          <a
            :href="githubUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm transition-colors"
            :class="isDark ? 'text-slate-400 hover:text-white' : 'text-[#6B8796] hover:text-[#2C4A5E]'"
          >
            GitHub
          </a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import GalaxyBackground from '@/components/common/GalaxyBackground.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { sanitizeUrl } from '@/utils/url'
import { resolvePanelHomePath } from '@/utils/adminAccess'

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'RingStar')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI API Gateway Platform')
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)

const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

const isDark = ref(document.documentElement.classList.contains('dark'))
const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => resolvePanelHomePath(isAdmin.value, authStore.isAffiliateAdmin))
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

const currentYear = computed(() => new Date().getFullYear())

// ── 接入地址区块 ──
// 主地址优先用管理员配置的 api_base_url（对外域名可能与当前访问域名不同），
// 未配置时退回当前站点 origin。
const apiBaseUrl = computed(() => {
  const configured = (appStore.cachedPublicSettings?.api_base_url || '').trim()
  return (configured || window.location.origin).replace(/\/+$/, '')
})

const customEndpoints = computed(() => appStore.cachedPublicSettings?.custom_endpoints || [])

// 各客户端要填的地址由网关路由结构决定：
// Claude Code / Gemini CLI 自己会拼 /v1/messages、/v1beta/...，填根地址即可；
// Codex 与 OpenAI 兼容客户端填到 /v1；Cursor IDE 与 Antigravity 有专用前缀。
const clientEndpoints = computed(() => [
  { key: 'claudeCode', url: apiBaseUrl.value },
  { key: 'codex', url: `${apiBaseUrl.value}/v1` },
  { key: 'geminiCli', url: apiBaseUrl.value },
  { key: 'openaiCompat', url: `${apiBaseUrl.value}/v1` },
  { key: 'cursorIde', url: `${apiBaseUrl.value}/cursor-ide/v1` },
  { key: 'antigravity', url: `${apiBaseUrl.value}/antigravity` }
])

const copiedKey = ref<string | null>(null)
let copiedResetTimer: number | undefined

async function copyEndpoint(key: string, url: string) {
  const success = await copyToClipboard(url)
  if (!success) return
  copiedKey.value = key
  if (copiedResetTimer !== undefined) {
    window.clearTimeout(copiedResetTimer)
  }
  copiedResetTimer = window.setTimeout(() => {
    if (copiedKey.value === key) {
      copiedKey.value = null
    }
  }, 1800)
}

onUnmounted(() => {
  if (copiedResetTimer !== undefined) {
    window.clearTimeout(copiedResetTimer)
  }
})

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  isDark.value = savedTheme === 'dark'
  document.documentElement.classList.toggle('dark', isDark.value)
}

onMounted(() => {
  initTheme()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>

<style scoped>
.hero-title {
  text-shadow: 0 0 28px rgba(34, 211, 238, 0.25);
}

.home-cosmos--light .hero-title {
  text-shadow: none;
  letter-spacing: -0.028em;
  font-weight: 600;
}

.home-cta-light {
  border-radius: 9999px;
  background: linear-gradient(135deg, #7ec8e3 0%, #5bb8d6 52%, #f2c07a 145%);
  color: #143447;
  box-shadow:
    0 10px 28px rgba(91, 184, 214, 0.38),
    inset 0 1px 0 rgba(255, 255, 255, 0.55);
}

.home-cta-light:hover {
  background: linear-gradient(135deg, #f5c16c 0%, #e8a85a 68%, #7ec8e3 160%);
  color: #3a2a10;
  box-shadow: 0 12px 32px rgba(232, 168, 90, 0.32);
}

.home-ctrl-light {
  color: #6b8796;
}

.home-ctrl-light:hover {
  background: #e8f6fc;
  color: #2c4a5e;
}

.home-login-chip {
  background: linear-gradient(135deg, #7ec8e3, #5bb8d6);
  color: #143447;
  box-shadow: 0 6px 16px rgba(91, 184, 214, 0.3);
}

.home-login-chip:hover {
  background: linear-gradient(135deg, #f5c16c, #e8a85a);
  color: #3a2a10;
}

.home-login-chip :deep(span),
.home-login-chip :deep(svg) {
  color: #143447;
}

.logo-mark {
  display: flex;
  align-items: center;
  justify-content: center;
}

.logo-mark--light {
  background: transparent;
  box-shadow: none;
}

.logo-mark--dark {
  filter: drop-shadow(0 0 12px rgba(34, 211, 238, 0.28));
}

.cosmos-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.65rem;
  border-radius: 9999px;
  border: 1px solid rgba(165, 243, 252, 0.18);
  background: rgba(15, 23, 42, 0.45);
  padding: 0.65rem 1.25rem;
  backdrop-filter: blur(10px);
  box-shadow: 0 8px 24px rgba(2, 8, 18, 0.25);
}

.icon-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.cosmos-card {
  border-radius: 1rem;
  border: 1px solid rgba(165, 243, 252, 0.14);
  background: rgba(15, 23, 42, 0.48);
  padding: 1.5rem;
  backdrop-filter: blur(12px);
  transition: transform 0.3s ease, box-shadow 0.3s ease, border-color 0.3s ease;
}

/* ── Connect panel ── */
.connect-panel {
  border-radius: 1.25rem;
  border: 1px solid rgba(165, 243, 252, 0.16);
  background: rgba(15, 23, 42, 0.5);
  padding: 1.75rem;
  backdrop-filter: blur(12px);
  box-shadow: 0 18px 48px rgba(2, 8, 18, 0.35);
}

.connect-primary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
  border-radius: 0.9rem;
  border: 1px solid rgba(34, 211, 238, 0.35);
  background: linear-gradient(120deg, rgba(8, 51, 68, 0.55), rgba(15, 23, 42, 0.65));
  padding: 0.9rem 1.1rem;
  box-shadow:
    0 0 24px rgba(34, 211, 238, 0.12),
    inset 0 1px 0 rgba(255, 255, 255, 0.06);
}

.connect-primary-label {
  flex-shrink: 0;
  border-radius: 0.375rem;
  background: rgba(34, 211, 238, 0.16);
  padding: 0.25rem 0.5rem;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: #a5f3fc;
}

.connect-primary-url {
  flex: 1;
  min-width: 200px;
  cursor: pointer;
  font-family: ui-monospace, 'Fira Code', monospace;
  font-size: 1rem;
  font-weight: 500;
  color: #fff;
  word-break: break-all;
  text-decoration-line: none;
  text-underline-offset: 4px;
}

.connect-primary-url:hover,
.connect-primary-url:focus-visible {
  text-decoration-line: underline;
  text-decoration-style: dashed;
  text-decoration-color: rgba(165, 243, 252, 0.6);
  outline: none;
}

@media (min-width: 768px) {
  .connect-primary-url {
    font-size: 1.125rem;
  }
}

.connect-copy-btn {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 0.375rem;
  border-radius: 0.55rem;
  border: 1px solid rgba(34, 211, 238, 0.4);
  background: rgba(34, 211, 238, 0.14);
  padding: 0.45rem 0.85rem;
  font-size: 0.8rem;
  font-weight: 600;
  color: #a5f3fc;
  transition: background-color 0.2s ease, border-color 0.2s ease, color 0.2s ease;
}

.connect-copy-btn:hover {
  background: rgba(34, 211, 238, 0.24);
  border-color: rgba(34, 211, 238, 0.6);
  color: #cffafe;
}

.connect-copy-btn.is-copied {
  border-color: rgba(52, 211, 153, 0.5);
  background: rgba(52, 211, 153, 0.16);
  color: #6ee7b7;
}

.connect-alt-chip {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  gap: 0.5rem;
  border-radius: 0.6rem;
  border: 1px solid rgba(165, 243, 252, 0.18);
  background: rgba(15, 23, 42, 0.45);
  padding: 0.4rem 0.75rem;
  color: #94a3b8;
  transition: border-color 0.2s ease, color 0.2s ease;
}

.connect-alt-chip:hover {
  border-color: rgba(34, 211, 238, 0.45);
  color: #cffafe;
}

.connect-alt-chip.is-copied {
  border-color: rgba(52, 211, 153, 0.5);
  color: #6ee7b7;
}

.connect-alt-name {
  flex-shrink: 0;
  font-size: 0.75rem;
  font-weight: 600;
  color: #e2e8f0;
}

.connect-alt-url {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, 'Fira Code', monospace;
  font-size: 0.75rem;
}

.connect-clients-title {
  margin: 1.5rem 0 0.75rem;
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: #7dd3fc;
}

.connect-client-row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  border-radius: 0.75rem;
  border: 1px solid rgba(165, 243, 252, 0.12);
  background: rgba(15, 23, 42, 0.4);
  padding: 0.8rem 1rem;
  transition: border-color 0.2s ease, background-color 0.2s ease;
}

.connect-client-row:hover {
  border-color: rgba(34, 211, 238, 0.4);
  background: rgba(15, 23, 42, 0.55);
}

.connect-client-url {
  display: block;
  margin-top: 0.35rem;
  cursor: pointer;
  font-family: ui-monospace, 'Fira Code', monospace;
  font-size: 0.75rem;
  color: #67e8f9;
  word-break: break-all;
}

.connect-client-url:hover,
.connect-client-url:focus-visible {
  text-decoration-line: underline;
  text-decoration-style: dashed;
  text-underline-offset: 3px;
  outline: none;
}

.connect-copy-icon {
  flex-shrink: 0;
  border-radius: 0.5rem;
  border: 1px solid transparent;
  padding: 0.45rem;
  color: #64748b;
  transition: color 0.2s ease, border-color 0.2s ease, background-color 0.2s ease;
}

.connect-copy-icon:hover {
  border-color: rgba(34, 211, 238, 0.4);
  background: rgba(34, 211, 238, 0.12);
  color: #a5f3fc;
}

.connect-copy-icon.is-copied {
  color: #6ee7b7;
}

.cosmos-card:hover {
  transform: translateY(-4px);
  border-color: rgba(34, 211, 238, 0.35);
  box-shadow: 0 18px 40px rgba(8, 47, 73, 0.35);
}

.feature-icon {
  margin-bottom: 1rem;
  display: flex;
  height: 3rem;
  width: 3rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.75rem;
  box-shadow: 0 10px 24px rgba(0, 0, 0, 0.25);
}

.feature-icon--blue {
  background: linear-gradient(135deg, #38bdf8, #0284c7);
}
.feature-icon--teal {
  background: linear-gradient(135deg, #2dd4bf, #0f766e);
}
.feature-icon--sky {
  background: linear-gradient(135deg, #67e8f9, #0891b2);
}

.provider-chip {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  border-radius: 0.75rem;
  border: 1px solid rgba(34, 211, 238, 0.22);
  background: rgba(15, 23, 42, 0.5);
  padding: 0.75rem 1.25rem;
  backdrop-filter: blur(10px);
  box-shadow: 0 0 0 1px rgba(34, 211, 238, 0.08);
}

.provider-chip--muted {
  border-color: rgba(148, 163, 184, 0.2);
  opacity: 0.7;
}

.provider-mark {
  display: flex;
  height: 2rem;
  width: 2rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.5rem;
}

.provider-mark--orange {
  background: linear-gradient(135deg, #fb923c, #f97316);
}
.provider-mark--green {
  background: linear-gradient(135deg, #22c55e, #16a34a);
}
.provider-mark--blue {
  background: linear-gradient(135deg, #3b82f6, #2563eb);
}
.provider-mark--rose {
  background: linear-gradient(135deg, #fb7185, #db2777);
}
.provider-mark--black {
  background: linear-gradient(135deg, #475569, #0f172a);
}
.provider-mark--violet {
  background: linear-gradient(135deg, #818cf8, #4f46e5);
}
.provider-mark--purple {
  background: linear-gradient(135deg, #c084fc, #7c3aed);
}
.provider-mark--gray {
  background: linear-gradient(135deg, #64748b, #475569);
}

.provider-tag {
  border-radius: 0.25rem;
  background: rgba(34, 211, 238, 0.16);
  padding: 0.125rem 0.375rem;
  font-size: 10px;
  font-weight: 500;
  color: #a5f3fc;
}

.provider-tag--muted {
  background: rgba(148, 163, 184, 0.18);
  color: #cbd5e1;
}

/* Icon motion (definitions live in unscoped style block below) */
.icon-float,
.icon-bob,
.icon-orbit,
.icon-spin-soft,
.icon-twinkle,
.icon-nudge {
  will-change: transform;
}

.delay-0,
.delay-1,
.delay-2,
.delay-3,
.delay-4 {
  /* delay values applied in unscoped block */
}

/* Light / kawaii landing — fox-mascot palette, white page, no logo plate */
.home-cosmos--light {
  background-color: #ffffff;
  background-image:
    radial-gradient(circle at 8% 22%, rgba(242, 192, 122, 0.55) 0 1.15px, transparent 1.6px),
    radial-gradient(circle at 93% 14%, rgba(126, 200, 227, 0.55) 0 1.15px, transparent 1.6px),
    radial-gradient(circle at 86% 40%, rgba(248, 180, 190, 0.42) 0 1px, transparent 1.4px),
    radial-gradient(circle at 16% 74%, rgba(126, 200, 227, 0.28) 0 1.1px, transparent 1.55px),
    radial-gradient(circle at 72% 82%, rgba(242, 192, 122, 0.28) 0 1px, transparent 1.4px);
}

.home-cosmos--light .logo-mark {
  background: transparent;
  box-shadow: none;
}

.home-cosmos--light header :deep(button) {
  border-radius: 9999px;
}

.home-cosmos--light header :deep(button:hover) {
  background-color: #e8f6fc;
}

.home-cosmos--light header :deep(.absolute) {
  border-radius: 1rem;
  border-color: rgba(126, 200, 227, 0.32);
  box-shadow: 0 12px 28px rgba(126, 200, 227, 0.18);
}

.home-cosmos--light .cosmos-chip {
  border: 1.5px solid rgba(126, 200, 227, 0.38);
  background: #ffffff;
  box-shadow:
    0 8px 20px rgba(126, 200, 227, 0.18),
    inset 0 1px 0 rgba(255, 255, 255, 0.9);
  backdrop-filter: none;
  color: #2c4a5e;
}

.home-cosmos--light .cosmos-chip .text-cyan-300 {
  color: #5bb8d6;
}

.home-cosmos--light .cosmos-chip span {
  color: #2c4a5e;
}

.home-cosmos--light .provider-chip > span.text-sm,
.home-cosmos--light .provider-chip > span.text-slate-100,
.home-cosmos--light .provider-chip > span.text-slate-200 {
  color: #2c4a5e;
}

.home-cosmos--light .cosmos-card {
  position: relative;
  border-radius: 1.5rem;
  border: 1.5px solid rgba(126, 200, 227, 0.28);
  background: #ffffff;
  backdrop-filter: none;
  box-shadow:
    0 14px 36px rgba(126, 200, 227, 0.18),
    0 2px 8px rgba(242, 192, 122, 0.06);
}

.home-cosmos--light .cosmos-card::after {
  content: '✦';
  position: absolute;
  top: 0.85rem;
  right: 1rem;
  font-size: 0.72rem;
  color: #f2c07a;
  opacity: 0.72;
  pointer-events: none;
}

.home-cosmos--light .cosmos-card h3 {
  color: #2c4a5e;
}

.home-cosmos--light .cosmos-card p {
  color: #6b8796;
}

.home-cosmos--light .cosmos-card:hover {
  transform: translateY(-3px);
  border-color: rgba(91, 184, 214, 0.45);
  box-shadow: 0 18px 40px rgba(126, 200, 227, 0.26);
}

.home-cosmos--light .connect-panel {
  border-radius: 1.5rem;
  border: 1.5px solid rgba(126, 200, 227, 0.28);
  background: #ffffff;
  backdrop-filter: none;
  box-shadow:
    0 16px 40px rgba(126, 200, 227, 0.16),
    0 2px 8px rgba(242, 192, 122, 0.05);
}

.home-cosmos--light .connect-primary {
  border-radius: 1.25rem;
  border: 1.5px solid rgba(126, 200, 227, 0.35);
  background: #f3fafe;
  box-shadow: 0 6px 16px rgba(126, 200, 227, 0.1);
}

.home-cosmos--light .connect-primary-label {
  border-radius: 9999px;
  background: rgba(126, 200, 227, 0.22);
  color: #3d6a80;
}

.home-cosmos--light .connect-primary-url {
  color: #2c4a5e;
}

.home-cosmos--light .connect-primary-url:hover,
.home-cosmos--light .connect-primary-url:focus-visible {
  text-decoration-color: rgba(91, 184, 214, 0.55);
}

.home-cosmos--light .connect-copy-btn {
  border-radius: 9999px;
  border: 1px solid transparent;
  background: linear-gradient(135deg, #7ec8e3, #5bb8d6);
  color: #143447;
}

.home-cosmos--light .connect-copy-btn:hover {
  background: linear-gradient(135deg, #f5c16c, #e8a85a);
  border-color: transparent;
  color: #3a2a10;
}

.home-cosmos--light .connect-copy-btn.is-copied {
  border-color: transparent;
  background: linear-gradient(135deg, #8fd4b8, #6bbf9a);
  color: #143447;
}

.home-cosmos--light .connect-alt-chip {
  border-radius: 9999px;
  border: 1.5px solid rgba(126, 200, 227, 0.28);
  background: #ffffff;
  color: #6b8796;
  box-shadow: 0 4px 12px rgba(126, 200, 227, 0.12);
}

.home-cosmos--light .connect-alt-chip:hover {
  border-color: rgba(91, 184, 214, 0.5);
  color: #2c4a5e;
}

.home-cosmos--light .connect-alt-chip.is-copied {
  border-color: rgba(107, 191, 154, 0.55);
  color: #2c4a5e;
}

.home-cosmos--light .connect-alt-name {
  color: #2c4a5e;
}

.home-cosmos--light .connect-clients-title {
  color: #6b8796;
}

.home-cosmos--light .connect-client-row {
  border-radius: 1.15rem;
  border: 1.5px solid rgba(126, 200, 227, 0.22);
  background: #ffffff;
  box-shadow: 0 6px 16px rgba(126, 200, 227, 0.1);
}

.home-cosmos--light .connect-client-row:hover {
  border-color: rgba(91, 184, 214, 0.42);
  background: #f7fbfe;
}

.home-cosmos--light .connect-client-url {
  color: #4ba3c7;
}

.home-cosmos--light .connect-copy-icon {
  border-radius: 9999px;
  color: #8aa4b3;
}

.home-cosmos--light .connect-copy-icon:hover {
  border-color: rgba(126, 200, 227, 0.4);
  background: #e8f6fc;
  color: #2c4a5e;
}

.home-cosmos--light .connect-copy-icon.is-copied {
  color: #3d8f6e;
}

.home-cosmos--light .feature-icon {
  border-radius: 1rem;
  box-shadow: 0 8px 18px rgba(126, 200, 227, 0.28);
}

.home-cosmos--light .feature-icon--blue {
  background: linear-gradient(135deg, #8fd4ec, #5bb8d6);
}

.home-cosmos--light .feature-icon--teal {
  background: linear-gradient(135deg, #f5c16c, #e8a85a);
}

.home-cosmos--light .feature-icon--sky {
  background: linear-gradient(135deg, #f3c4cb, #7ec8e3);
}

.home-cosmos--light .provider-chip {
  border-radius: 1.15rem;
  border: 1.5px solid rgba(126, 200, 227, 0.3);
  background: #ffffff;
  backdrop-filter: none;
  box-shadow: 0 8px 18px rgba(126, 200, 227, 0.14);
  color: #2c4a5e;
}

.home-cosmos--light .provider-chip--muted {
  border-color: rgba(126, 200, 227, 0.2);
  opacity: 0.78;
}

.home-cosmos--light .provider-mark {
  border-radius: 0.7rem;
}

.home-cosmos--light .provider-tag {
  border-radius: 9999px;
  background: rgba(126, 200, 227, 0.2);
  color: #3d6a80;
}

.home-cosmos--light .provider-tag--muted {
  background: rgba(242, 192, 122, 0.22);
  color: #9a7040;
}

@media (prefers-reduced-motion: reduce) {
  /* Keep light icon motion for brand presence; only calm large atmosphere layers. */
  .logo-mark--dark {
    filter: none;
  }
}
</style>

<style>
/* Unscoped so keyframes are not renamed by Vue scoped CSS and stay reliable. */
.home-cosmos .icon-float {
  animation: home-icon-float 2.2s ease-in-out infinite;
}
.home-cosmos .icon-bob {
  animation: home-icon-bob 2s ease-in-out infinite;
}
.home-cosmos .icon-orbit {
  animation: home-icon-orbit 2.6s ease-in-out infinite;
}
.home-cosmos .icon-spin-soft {
  animation: home-icon-spin 2.4s ease-in-out infinite;
}
.home-cosmos .icon-twinkle {
  animation: home-icon-twinkle 1.8s ease-in-out infinite;
}
.home-cosmos .icon-nudge {
  animation: home-icon-nudge 1.2s ease-in-out infinite;
}
.home-cosmos .delay-0 {
  animation-delay: 0s;
}
.home-cosmos .delay-1 {
  animation-delay: 0.35s;
}
.home-cosmos .delay-2 {
  animation-delay: 0.7s;
}
.home-cosmos .delay-3 {
  animation-delay: 1.05s;
}
.home-cosmos .delay-4 {
  animation-delay: 1.4s;
}

@keyframes home-icon-float {
  0%,
  100% {
    transform: translateY(0) scale(1);
  }
  50% {
    transform: translateY(-8px) scale(1.12);
  }
}
@keyframes home-icon-bob {
  0%,
  100% {
    transform: translateY(0) rotate(0deg) scale(1);
  }
  50% {
    transform: translateY(-5px) rotate(12deg) scale(1.1);
  }
}
@keyframes home-icon-orbit {
  0%,
  100% {
    transform: translateY(0) scale(1) rotate(0deg);
    box-shadow: 0 10px 24px rgba(0, 0, 0, 0.25);
  }
  50% {
    transform: translateY(-10px) scale(1.1) rotate(-4deg);
    box-shadow: 0 18px 36px rgba(34, 211, 238, 0.4);
  }
}
@keyframes home-icon-spin {
  0%,
  100% {
    transform: translateY(0) rotate(0deg) scale(1);
  }
  50% {
    transform: translateY(-7px) rotate(16deg) scale(1.12);
  }
}
@keyframes home-icon-twinkle {
  0%,
  100% {
    opacity: 0.7;
    transform: scale(1);
  }
  50% {
    opacity: 1;
    transform: scale(1.18);
  }
}
@keyframes home-icon-nudge {
  0%,
  100% {
    transform: translateX(0);
  }
  50% {
    transform: translateX(7px);
  }
}

/* Light landing: keep motion calm; a tiny twinkle keeps the kawaii sparkle. */
.home-cosmos--light .icon-float,
.home-cosmos--light .icon-bob,
.home-cosmos--light .icon-orbit,
.home-cosmos--light .icon-spin-soft,
.home-cosmos--light .icon-nudge {
  animation: none;
}
.home-cosmos--light .icon-twinkle {
  animation: home-icon-twinkle 2.4s ease-in-out infinite;
}
</style>
