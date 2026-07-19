<template>
  <div class="relative flex min-h-screen items-center justify-center overflow-hidden p-4">
    <!-- Deep space background -->
    <div class="galaxy-base absolute inset-0"></div>

    <!-- Decorative Elements -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <!-- Milky-way band -->
      <div class="milky-way absolute"></div>

      <!-- Nebula glows -->
      <div
        class="absolute -right-32 -top-32 h-[28rem] w-[28rem] rounded-full bg-violet-600/25 blur-3xl"
      ></div>
      <div
        class="absolute -bottom-40 -left-32 h-[26rem] w-[26rem] rounded-full bg-cyan-500/20 blur-3xl"
      ></div>
      <div
        class="absolute left-1/2 top-1/2 h-[30rem] w-[30rem] -translate-x-1/2 -translate-y-1/2 rounded-full bg-indigo-600/15 blur-3xl"
      ></div>
      <div
        class="absolute bottom-1/4 right-1/4 h-64 w-64 rounded-full bg-fuchsia-500/15 blur-3xl"
      ></div>

      <!-- Star field layers -->
      <div class="stars stars-sm absolute inset-0"></div>
      <div class="stars stars-md absolute inset-0"></div>
      <div class="stars stars-lg absolute inset-0"></div>

      <!-- Shooting stars -->
      <div class="shooting-star" style="top: 12%; left: 62%; animation-delay: 2s"></div>
      <div class="shooting-star" style="top: 34%; left: 18%; animation-delay: 7s"></div>
      <div class="shooting-star" style="top: 68%; left: 74%; animation-delay: 12s"></div>
    </div>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo/Brand -->
      <div class="mb-8 text-center">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div class="logo-float mb-5 inline-flex h-20 w-20 items-center justify-center">
            <div class="logo-glow inline-flex h-full w-full items-center justify-center overflow-hidden rounded-2xl">
              <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
            </div>
          </div>
          <h1 class="galaxy-title mb-2 text-4xl font-bold tracking-wide">
            {{ siteName }}
          </h1>
          <p class="text-sm text-indigo-200/70">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container -->
      <div class="galaxy-card rounded-2xl p-8">
        <slot />
      </div>

      <!-- Footer Links -->
      <div class="auth-footer mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- Copyright -->
      <div class="mt-8 text-center text-xs text-indigo-200/40">
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

const siteName = computed(() => appStore.siteName || '环星')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
/* Deep space gradient base (always dark, independent of theme) */
.galaxy-base {
  background:
    radial-gradient(ellipse 80% 60% at 70% 10%, rgba(88, 28, 135, 0.35), transparent 60%),
    radial-gradient(ellipse 70% 55% at 15% 85%, rgba(8, 145, 178, 0.22), transparent 60%),
    radial-gradient(ellipse 60% 50% at 50% 50%, rgba(67, 56, 202, 0.18), transparent 65%),
    linear-gradient(160deg, #030014 0%, #0a0a2a 45%, #120b33 75%, #050212 100%);
}

/* Diagonal milky-way band */
.milky-way {
  top: -20%;
  left: -25%;
  width: 150%;
  height: 45%;
  transform: rotate(-18deg);
  background: linear-gradient(
    90deg,
    transparent 0%,
    rgba(165, 180, 252, 0.05) 20%,
    rgba(199, 210, 254, 0.12) 45%,
    rgba(224, 231, 255, 0.16) 50%,
    rgba(199, 210, 254, 0.12) 55%,
    rgba(165, 180, 252, 0.05) 80%,
    transparent 100%
  );
  filter: blur(28px);
}

/* Star field: tiled radial-gradient dots at three scales */
.stars {
  background-repeat: repeat;
}

.stars-sm {
  background-image:
    radial-gradient(1px 1px at 18px 32px, rgba(255, 255, 255, 0.9), transparent),
    radial-gradient(1px 1px at 76px 118px, rgba(255, 255, 255, 0.6), transparent),
    radial-gradient(1px 1px at 132px 54px, rgba(186, 230, 253, 0.8), transparent),
    radial-gradient(1px 1px at 168px 152px, rgba(255, 255, 255, 0.5), transparent),
    radial-gradient(1px 1px at 44px 176px, rgba(221, 214, 254, 0.7), transparent),
    radial-gradient(1px 1px at 108px 12px, rgba(255, 255, 255, 0.65), transparent);
  background-size: 190px 190px;
  animation: twinkle 4.5s ease-in-out infinite;
}

.stars-md {
  background-image:
    radial-gradient(1.5px 1.5px at 52px 84px, rgba(255, 255, 255, 0.95), transparent),
    radial-gradient(1.5px 1.5px at 194px 30px, rgba(165, 243, 252, 0.85), transparent),
    radial-gradient(1.5px 1.5px at 140px 196px, rgba(255, 255, 255, 0.7), transparent),
    radial-gradient(1.5px 1.5px at 250px 140px, rgba(196, 181, 253, 0.8), transparent);
  background-size: 290px 290px;
  animation: twinkle 6s ease-in-out infinite 1.2s;
}

.stars-lg {
  background-image:
    radial-gradient(2.5px 2.5px at 90px 60px, rgba(255, 255, 255, 1), transparent),
    radial-gradient(2px 2px at 300px 230px, rgba(186, 230, 253, 0.95), transparent),
    radial-gradient(2px 2px at 210px 330px, rgba(240, 171, 252, 0.75), transparent);
  background-size: 390px 390px;
  animation: twinkle 7.5s ease-in-out infinite 0.6s;
}

@keyframes twinkle {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.45;
  }
}

/* Shooting stars */
.shooting-star {
  position: absolute;
  width: 130px;
  height: 1.5px;
  border-radius: 9999px;
  background: linear-gradient(90deg, rgba(255, 255, 255, 0.9), rgba(165, 243, 252, 0.35), transparent);
  transform: rotate(-32deg);
  opacity: 0;
  animation: shoot 9s linear infinite;
}

@keyframes shoot {
  0% {
    opacity: 0;
    transform: rotate(-32deg) translateX(0);
  }
  2% {
    opacity: 1;
  }
  8% {
    opacity: 0;
    transform: rotate(-32deg) translateX(-340px);
  }
  100% {
    opacity: 0;
    transform: rotate(-32deg) translateX(-340px);
  }
}

/* Logo glow + float */
.logo-glow {
  box-shadow:
    0 0 24px rgba(139, 92, 246, 0.55),
    0 0 64px rgba(34, 211, 238, 0.3);
}

.logo-float {
  animation: logo-float 6s ease-in-out infinite;
}

@keyframes logo-float {
  0%,
  100% {
    transform: translateY(0);
  }
  50% {
    transform: translateY(-8px);
  }
}

/* Galaxy gradient title */
.galaxy-title {
  background: linear-gradient(100deg, #67e8f9 0%, #a5b4fc 40%, #c084fc 70%, #f0abfc 100%);
  background-clip: text;
  -webkit-background-clip: text;
  color: transparent;
  -webkit-text-fill-color: transparent;
  text-shadow: 0 0 32px rgba(129, 140, 248, 0.35);
}

/* Glass card over the galaxy: light frosted in light mode, dark glass in dark mode */
.galaxy-card {
  @apply bg-white/85 backdrop-blur-xl dark:bg-[#0c102c]/70;
  border: 1px solid rgba(199, 210, 254, 0.22);
  box-shadow:
    0 8px 40px rgba(2, 6, 23, 0.55),
    0 0 80px rgba(99, 102, 241, 0.18),
    inset 0 1px 0 rgba(255, 255, 255, 0.12);
}

/* Primary action buttons inside auth cards follow the galaxy palette */
.galaxy-card :deep(.btn-primary) {
  background: linear-gradient(100deg, #0ea5e9 0%, #6366f1 45%, #a855f7 100%);
  border: none;
  box-shadow:
    0 4px 18px rgba(99, 102, 241, 0.45),
    0 0 32px rgba(168, 85, 247, 0.25);
  transition: filter 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.galaxy-card :deep(.btn-primary:hover:not(:disabled)) {
  filter: brightness(1.12);
  box-shadow:
    0 6px 24px rgba(99, 102, 241, 0.55),
    0 0 40px rgba(168, 85, 247, 0.35);
}

/* Footer slot text sits directly on the dark galaxy, keep it readable in any theme */
.auth-footer :deep(p),
.auth-footer :deep(span) {
  color: rgba(199, 210, 254, 0.75);
}

.auth-footer :deep(a) {
  color: #a5f3fc;
}

.auth-footer :deep(a:hover) {
  color: #e0f2fe;
}
</style>
