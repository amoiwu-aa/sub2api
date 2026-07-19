<template>
  <!--
    The `dark` class scopes the auth screen to the design system's dark tokens in both app
    themes: the deep-space backdrop is part of the brand, and slotted form content (inputs,
    buttons, footer links) keeps its well-tested dark-mode contrast.
  -->
  <div class="dark relative flex min-h-screen items-center justify-center overflow-hidden bg-[#030812] p-4">
    <!-- Deep space backdrop -->
    <div class="auth-nebula absolute inset-0" aria-hidden="true"></div>

    <!-- Decorative Elements -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
      <!-- Starfield -->
      <div class="auth-stars-far absolute inset-x-0 top-0"></div>
      <div class="auth-stars absolute inset-x-0 top-0"></div>

      <!-- Tech grid -->
      <div
        class="auth-grid absolute inset-0 bg-[linear-gradient(rgba(45,212,191,0.05)_1px,transparent_1px),linear-gradient(90deg,rgba(45,212,191,0.05)_1px,transparent_1px)] bg-[size:56px_56px]"
      ></div>

      <!-- Nebula orbs -->
      <div class="auth-orb-a absolute -right-32 -top-32 h-96 w-96 rounded-full bg-primary-500/15 blur-3xl"></div>
      <div class="auth-orb-b absolute -bottom-40 -left-32 h-96 w-96 rounded-full bg-cyan-500/10 blur-3xl"></div>

      <!-- Orbital ring with satellite -->
      <div class="auth-ring absolute left-1/2 top-1/2"></div>
    </div>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo/Brand -->
      <div class="mb-8 text-center">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div class="relative mb-4 inline-flex">
            <div class="auth-halo absolute -inset-3 rounded-full bg-primary-400/25 blur-xl" aria-hidden="true"></div>
            <div
              class="relative inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl bg-[#04101c] shadow-[0_0_36px_rgba(45,212,191,0.35)] ring-1 ring-primary-400/40"
            >
              <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
            </div>
          </div>
          <h1 class="text-gradient mb-2 text-3xl font-bold tracking-wide">
            {{ siteName }}
          </h1>
          <p class="text-sm tracking-widest text-slate-400">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container -->
      <div class="auth-card card-glass relative rounded-2xl p-8 ring-1 ring-white/5">
        <div
          class="absolute inset-x-10 top-0 h-px bg-gradient-to-r from-transparent via-primary-400/60 to-transparent"
          aria-hidden="true"
        ></div>
        <slot />
      </div>

      <!-- Footer Links -->
      <div class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- Copyright -->
      <div class="mt-8 text-center text-xs text-slate-500">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || '环星中转')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI 接口聚合中转平台')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.text-gradient {
  @apply bg-gradient-to-r from-primary-300 via-primary-400 to-cyan-400 bg-clip-text text-transparent;
}

/* Deep-space base: faint teal nebulae over a near-black backdrop */
.auth-nebula {
  background:
    radial-gradient(ellipse 60% 45% at 70% -10%, rgba(20, 184, 166, 0.16), transparent 70%),
    radial-gradient(ellipse 55% 45% at 15% 110%, rgba(6, 182, 212, 0.12), transparent 70%),
    radial-gradient(ellipse 40% 35% at 50% 50%, rgba(20, 184, 166, 0.05), transparent 70%);
}

/* Starfield: repeating radial-gradient dots; near layer drifts, far layer twinkles */
.auth-stars,
.auth-stars-far {
  height: calc(100% + 280px);
  background-repeat: repeat;
  will-change: transform;
}

.auth-stars {
  background-image:
    radial-gradient(1.5px 1.5px at 27px 39px, rgba(94, 234, 212, 0.9), transparent 55%),
    radial-gradient(1px 1px at 152px 94px, rgba(255, 255, 255, 0.8), transparent 55%),
    radial-gradient(1px 1px at 211px 205px, rgba(255, 255, 255, 0.55), transparent 55%),
    radial-gradient(1.5px 1.5px at 88px 174px, rgba(165, 243, 252, 0.7), transparent 55%),
    radial-gradient(1px 1px at 254px 132px, rgba(255, 255, 255, 0.45), transparent 55%);
  background-size: 280px 280px;
  animation: auth-drift 180s linear infinite;
}

.auth-stars-far {
  background-image:
    radial-gradient(1px 1px at 45px 67px, rgba(255, 255, 255, 0.35), transparent 55%),
    radial-gradient(1px 1px at 120px 146px, rgba(148, 226, 213, 0.3), transparent 55%),
    radial-gradient(1px 1px at 190px 28px, rgba(255, 255, 255, 0.25), transparent 55%),
    radial-gradient(1px 1px at 66px 228px, rgba(255, 255, 255, 0.2), transparent 55%);
  background-size: 230px 280px;
  animation: auth-twinkle 7s ease-in-out infinite alternate;
}

@keyframes auth-drift {
  from {
    transform: translate3d(0, 0, 0);
  }
  to {
    transform: translate3d(0, -280px, 0);
  }
}

@keyframes auth-twinkle {
  from {
    opacity: 0.45;
  }
  to {
    opacity: 1;
  }
}

/* Grid fades out toward the edges to keep the scene airy */
.auth-grid {
  -webkit-mask-image: radial-gradient(ellipse 85% 70% at 50% 45%, black 25%, transparent 78%);
  mask-image: radial-gradient(ellipse 85% 70% at 50% 45%, black 25%, transparent 78%);
}

/* Slow-floating nebula orbs */
.auth-orb-a {
  animation: auth-float-a 26s ease-in-out infinite alternate;
}

.auth-orb-b {
  animation: auth-float-b 32s ease-in-out infinite alternate;
}

@keyframes auth-float-a {
  from {
    transform: translate3d(0, 0, 0);
  }
  to {
    transform: translate3d(-40px, 48px, 0);
  }
}

@keyframes auth-float-b {
  from {
    transform: translate3d(0, 0, 0);
  }
  to {
    transform: translate3d(48px, -40px, 0);
  }
}

/* Orbital ring with a glowing satellite dot, rotating very slowly behind the card */
.auth-ring {
  width: min(56rem, 160vw);
  aspect-ratio: 1;
  border-radius: 9999px;
  border: 1px solid rgba(45, 212, 191, 0.1);
  animation: auth-orbit 90s linear infinite;
}

.auth-ring::before {
  content: '';
  position: absolute;
  left: 50%;
  top: -3px;
  width: 6px;
  height: 6px;
  margin-left: -3px;
  border-radius: 9999px;
  background: rgba(94, 234, 212, 0.9);
  box-shadow: 0 0 12px 2px rgba(45, 212, 191, 0.6);
}

@keyframes auth-orbit {
  from {
    transform: translate(-50%, -50%) rotate(0deg);
  }
  to {
    transform: translate(-50%, -50%) rotate(360deg);
  }
}

/* Soft pulsing halo behind the logo */
.auth-halo {
  animation: auth-pulse 5s ease-in-out infinite alternate;
}

@keyframes auth-pulse {
  from {
    opacity: 0.5;
    transform: scale(0.96);
  }
  to {
    opacity: 1;
    transform: scale(1.06);
  }
}

/* Control-panel card: deep shadow plus a faint teal ambient glow */
.auth-card {
  box-shadow:
    0 8px 32px rgba(0, 0, 0, 0.45),
    0 0 80px -24px rgba(20, 184, 166, 0.35);
}

@media (prefers-reduced-motion: reduce) {
  .auth-stars,
  .auth-stars-far,
  .auth-orb-a,
  .auth-orb-b,
  .auth-ring,
  .auth-halo {
    animation: none;
  }
}
</style>
