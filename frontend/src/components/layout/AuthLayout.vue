<template>
  <div class="auth-shell relative flex min-h-screen items-center justify-center overflow-hidden px-4 py-10">
    <GalaxyBackground variant="auth" />

    <!-- Upper-right controls -->
    <div class="auth-topbar absolute right-4 top-4 z-30 flex items-center gap-1.5 sm:right-6 sm:top-6">
      <div class="auth-control">
        <LocaleSwitcher />
      </div>
      <button
        type="button"
        class="auth-control-btn"
        :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
        @click="toggleTheme"
      >
        <Icon v-if="isDark" name="sun" size="md" />
        <Icon v-else name="moon" size="md" />
      </button>
    </div>

    <div class="relative z-10 w-full max-w-[420px]">
      <div class="glass-card">
        <div class="glass-card__glow" aria-hidden="true"></div>
        <div class="glass-card__sheen" aria-hidden="true"></div>

        <div class="relative z-10">
          <!-- Brand -->
          <div v-if="settingsLoaded" class="mb-7 text-center">
            <div class="brand-logo mx-auto mb-4">
              <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
            </div>
            <p class="brand-name">{{ siteName }}</p>
            <p class="mt-1 text-[13px] tracking-wide text-slate-400">{{ siteSubtitle }}</p>
          </div>

          <slot />
        </div>
      </div>

      <div class="mt-6 text-center text-sm text-slate-400">
        <slot name="footer" />
      </div>

      <div class="mt-8 text-center text-[11px] tracking-wide text-slate-500/80">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import GalaxyBackground from '@/components/common/GalaxyBackground.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'RingStar')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true })
)
const siteSubtitle = computed(
  () => appStore.cachedPublicSettings?.site_subtitle || 'AI API Gateway'
)
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)
const currentYear = computed(() => new Date().getFullYear())

const isDark = ref(document.documentElement.classList.contains('dark'))

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.auth-shell {
  background: #040914;
}

.auth-control {
  display: inline-flex;
  align-items: center;
  border-radius: 0.75rem;
  border: 1px solid rgba(165, 243, 252, 0.14);
  background: rgba(15, 23, 42, 0.45);
  backdrop-filter: blur(12px);
  box-shadow: 0 8px 24px rgba(2, 8, 18, 0.25);
}

.auth-control :deep(button) {
  color: #cbd5e1;
}

.auth-control :deep(button:hover) {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
}

.auth-control :deep(.absolute) {
  border-color: rgba(165, 243, 252, 0.16);
  background: rgba(15, 23, 42, 0.95);
  backdrop-filter: blur(16px);
}

.auth-control-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.75rem;
  border: 1px solid rgba(165, 243, 252, 0.14);
  background: rgba(15, 23, 42, 0.45);
  padding: 0.45rem;
  color: #cbd5e1;
  backdrop-filter: blur(12px);
  transition: color 0.2s ease, background 0.2s ease;
}

.auth-control-btn:hover {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
}

.glass-card {
  position: relative;
  overflow: hidden;
  border-radius: 1.5rem;
  border: 1px solid rgba(165, 243, 252, 0.22);
  background:
    linear-gradient(155deg, rgba(255, 255, 255, 0.14) 0%, rgba(255, 255, 255, 0.04) 40%, rgba(8, 47, 73, 0.34) 100%);
  padding: 2rem 1.75rem 1.75rem;
  box-shadow:
    0 30px 80px rgba(2, 8, 18, 0.58),
    0 0 48px rgba(34, 211, 238, 0.12),
    0 0 0 1px rgba(255, 255, 255, 0.05) inset,
    0 1px 0 rgba(255, 255, 255, 0.18) inset;
  backdrop-filter: blur(32px) saturate(155%);
  -webkit-backdrop-filter: blur(32px) saturate(155%);
}

.glass-card__glow {
  pointer-events: none;
  position: absolute;
  inset: -1px;
  border-radius: inherit;
  padding: 1.5px;
  background: linear-gradient(
    135deg,
    rgba(34, 211, 238, 0.95),
    rgba(45, 212, 191, 0.45),
    rgba(56, 189, 248, 0.85),
    rgba(20, 184, 166, 0.55)
  );
  opacity: 0.85;
  animation: neon-breathe 4.8s ease-in-out infinite;
  -webkit-mask:
    linear-gradient(#fff 0 0) content-box,
    linear-gradient(#fff 0 0);
  -webkit-mask-composite: xor;
  mask:
    linear-gradient(#fff 0 0) content-box,
    linear-gradient(#fff 0 0);
  mask-composite: exclude;
}

.glass-card__sheen {
  pointer-events: none;
  position: absolute;
  inset: 0;
  background:
    radial-gradient(120% 80% at 20% 0%, rgba(255, 255, 255, 0.16), transparent 45%),
    radial-gradient(90% 60% at 90% 100%, rgba(34, 211, 238, 0.08), transparent 50%);
  opacity: 0.9;
}

.brand-logo {
  display: inline-flex;
  height: 4rem;
  width: 4rem;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 1.15rem;
  border: 1px solid rgba(165, 243, 252, 0.22);
  background: rgba(15, 23, 42, 0.55);
  box-shadow:
    0 12px 30px rgba(2, 8, 18, 0.45),
    0 0 24px rgba(34, 211, 238, 0.2);
}

.brand-name {
  font-size: 1.05rem;
  font-weight: 650;
  letter-spacing: 0.04em;
  color: #f8fafc;
  text-shadow: 0 0 22px rgba(34, 211, 238, 0.28);
}

@keyframes neon-breathe {
  0%,
  100% {
    opacity: 0.45;
    filter: blur(0.2px) brightness(0.95);
  }
  50% {
    opacity: 0.95;
    filter: blur(0.45px) brightness(1.2);
  }
}

@media (prefers-reduced-motion: reduce) {
  .glass-card__glow {
    animation: none;
    opacity: 0.7;
  }
}
</style>
