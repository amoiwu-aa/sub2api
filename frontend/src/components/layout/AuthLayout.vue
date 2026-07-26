<template>
  <div
    class="auth-shell relative flex min-h-screen items-center justify-center overflow-hidden px-4 py-10"
    :class="isDark ? 'auth-shell--dark' : 'auth-shell--light'"
  >
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

    <div class="auth-stage relative z-10 w-full max-w-[420px]">
      <div class="glass-card">
        <div class="glass-card__glow" aria-hidden="true"></div>
        <div class="glass-card__sheen" aria-hidden="true"></div>

        <div class="relative z-10">
          <!-- Brand -->
          <div v-if="settingsLoaded" class="auth-reveal mb-6 text-center" style="--reveal-delay: 60ms">
            <div class="brand-logo mx-auto mb-3">
              <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
            </div>
            <p class="brand-name">{{ siteName }}</p>
            <p class="brand-subtitle mt-1">{{ siteSubtitle }}</p>
          </div>

          <div class="auth-reveal" style="--reveal-delay: 140ms">
            <slot />
          </div>
        </div>
      </div>

      <div class="auth-footer mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <div class="auth-copy mt-8 text-center text-[11px] tracking-wide">
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

function applyTheme(dark: boolean) {
  isDark.value = dark
  document.documentElement.classList.toggle('dark', dark)
  localStorage.setItem('theme', dark ? 'dark' : 'light')
}

function toggleTheme() {
  applyTheme(!isDark.value)
}

function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches
  const shouldUseDark = savedTheme === 'dark' || (!savedTheme && prefersDark)
  applyTheme(shouldUseDark)
}

onMounted(() => {
  initTheme()
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
/* 主题色板以 CSS 变量下发给表单。
 *
 * 不用 `:global(html:not(.dark)) .login-input` 那套：Vue 的 scoped CSS 在
 * :global() 里嵌 :not() 时会把整条规则丢掉（实测编译产物里一条都不剩），
 * 结果是登录表单的浅色样式从来没生效过——无论切到哪个主题，输入框都渲染
 * 深色底，压在浅色卡片上就是几块灰疙瘩。
 * 自定义属性的继承不受作用域约束，父组件定义、子组件消费，天然可靠。 */
.auth-shell--dark,
.auth-shell--light {
  background: #040914;
}

.auth-shell--dark {
  --auth-title: #f2f7fa;
  --auth-label: #c3d3de;
  --auth-muted: #93a9b8;
  --auth-field-bg: rgba(2, 7, 16, 0.55);
  --auth-field-border: rgba(165, 243, 252, 0.22);
  --auth-field-border-hover: rgba(125, 211, 252, 0.4);
  --auth-field-text: #f2f7fa;
  --auth-field-placeholder: #8399ad;
  --auth-field-bg-focus: rgba(6, 32, 52, 0.72);
  --auth-icon: #8fa8ba;
  --auth-link: #5fdcd0;
  --auth-link-hover: #9cf0e6;
  --auth-divider: rgba(255, 255, 255, 0.1);
}

.auth-shell--light {
  --auth-title: #0d1b2a;
  --auth-label: #33475a;
  --auth-muted: #64748b;
  --auth-field-bg: #ffffff;
  --auth-field-border: rgba(15, 23, 42, 0.14);
  --auth-field-border-hover: rgba(13, 148, 136, 0.45);
  --auth-field-text: #0d1b2a;
  /* #94a3b8 压在白底上只有 2.56:1，达不到 WCAG AA 的 4.5:1；#64748b 约 4.8:1。 */
  --auth-field-placeholder: #64748b;
  --auth-field-bg-focus: #ffffff;
  --auth-icon: #7c8b9c;
  --auth-link: #0d8a80;
  --auth-link-hover: #0b6c66;
  --auth-divider: rgba(15, 23, 42, 0.1);
}

/* 进场：整块面板先浮起，内部元素再依次跟进。
 * 一次编排得当的载入，比零散的悬停微交互更有存在感；也顺手掩盖了
 * backdrop-filter 首帧的突兀。 */
.auth-stage {
  animation: auth-stage-in 620ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.auth-reveal {
  animation: auth-reveal-in 560ms cubic-bezier(0.22, 1, 0.36, 1) both;
  animation-delay: var(--reveal-delay, 0ms);
}

@keyframes auth-stage-in {
  from {
    opacity: 0;
    transform: translateY(14px) scale(0.985);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

@keyframes auth-reveal-in {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .auth-stage,
  .auth-reveal {
    animation: none;
  }
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

.auth-shell--light .auth-control {
  border-color: rgba(14, 116, 144, 0.18);
  background: rgba(255, 255, 255, 0.72);
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.08);
}

.auth-control :deep(button) {
  color: #cbd5e1;
}

.auth-shell--light .auth-control :deep(button) {
  color: #334155;
}

.auth-control :deep(button:hover) {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
}

.auth-shell--light .auth-control :deep(button:hover) {
  background: rgba(14, 116, 144, 0.08);
  color: #0f172a;
}

.auth-control :deep(.absolute) {
  border-color: rgba(165, 243, 252, 0.16);
  background: rgba(15, 23, 42, 0.95);
  backdrop-filter: blur(16px);
}

.auth-shell--light .auth-control :deep(.absolute) {
  border-color: rgba(148, 163, 184, 0.35);
  background: rgba(255, 255, 255, 0.96);
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

.auth-shell--light .auth-control-btn {
  border-color: rgba(14, 116, 144, 0.18);
  background: rgba(255, 255, 255, 0.72);
  color: #334155;
}

.auth-control-btn:hover {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
}

.auth-shell--light .auth-control-btn:hover {
  background: rgba(14, 116, 144, 0.08);
  color: #0f172a;
}

/* 卡片必须自带足够深的底色。
 *
 * 之前是近乎全透明（白 5%）+ saturate(140%)，星系那条明亮旋臂正好从卡片后面
 * 扫过，被 backdrop-filter 放大成一片白雾，标签和 placeholder 直接糊掉。
 * 玻璃感来自「模糊 + 边缘高光」，不是来自「透明」——让星点留成隐约纹理即可，
 * 文字对比度不能交给背景去决定。 */
.glass-card {
  position: relative;
  overflow: hidden;
  border-radius: 1.5rem;
  border: 1px solid rgba(148, 197, 214, 0.16);
  background:
    linear-gradient(
      160deg,
      rgba(12, 22, 40, 0.82) 0%,
      rgba(8, 16, 31, 0.86) 55%,
      rgba(6, 13, 26, 0.9) 100%
    );
  padding: 2.25rem 1.85rem 1.85rem;
  box-shadow:
    0 40px 90px -20px rgba(0, 4, 12, 0.7),
    0 12px 32px -12px rgba(0, 4, 12, 0.5),
    0 1px 0 rgba(190, 232, 245, 0.14) inset;
  backdrop-filter: blur(24px) saturate(105%);
  -webkit-backdrop-filter: blur(24px) saturate(105%);
}

/* 浅色主题下 shell 背景仍是深空色，所以卡片要做成一张「明确的浅色卡」，
 * 而不是半透明——半透明在深底上只会变成灰蒙蒙的一团。 */
.auth-shell--light .glass-card {
  border-color: rgba(255, 255, 255, 0.5);
  background:
    linear-gradient(160deg, rgba(253, 254, 255, 0.95) 0%, rgba(241, 247, 252, 0.93) 100%);
  box-shadow:
    0 40px 90px -20px rgba(0, 4, 12, 0.55),
    0 12px 32px -12px rgba(2, 12, 28, 0.35),
    0 1px 0 rgba(255, 255, 255, 0.9) inset;
  backdrop-filter: blur(24px) saturate(105%);
  -webkit-backdrop-filter: blur(24px) saturate(105%);
}

/* 描边：一条静止的细发丝线，左上最亮、向右下衰减——像镜筒的斜切边被
 * 星系核的光照到。原来这里挂着 neon-breathe 无限呼吸，配合按钮上的两个
 * 循环动画，整页有三处东西在不停动，读起来很躁。光该是"被照亮"的，不是"自己闪"。 */
.glass-card__glow {
  pointer-events: none;
  position: absolute;
  inset: -1px;
  border-radius: inherit;
  padding: 1px;
  background: linear-gradient(
    145deg,
    rgba(125, 232, 250, 0.55) 0%,
    rgba(94, 202, 224, 0.18) 28%,
    rgba(148, 197, 214, 0.06) 55%,
    rgba(45, 212, 191, 0.16) 100%
  );
  -webkit-mask:
    linear-gradient(#fff 0 0) content-box,
    linear-gradient(#fff 0 0);
  -webkit-mask-composite: xor;
  mask:
    linear-gradient(#fff 0 0) content-box,
    linear-gradient(#fff 0 0);
  mask-composite: exclude;
}

.auth-shell--light .glass-card__glow {
  background: linear-gradient(
    145deg,
    rgba(255, 255, 255, 0.95) 0%,
    rgba(186, 230, 253, 0.5) 35%,
    rgba(148, 163, 184, 0.12) 100%
  );
}

/* 顶缘高光收成一条细线。原来是一大团 radial 白雾盖住卡片上半部分，
 * 在深色主题下正好压在标题和标签上，看起来像镜头脏了。 */
.glass-card__sheen {
  pointer-events: none;
  position: absolute;
  inset: 0;
  background:
    linear-gradient(180deg, rgba(198, 240, 252, 0.14) 0%, transparent 3px),
    radial-gradient(140% 46% at 50% 0%, rgba(125, 232, 250, 0.07), transparent 60%),
    radial-gradient(100% 50% at 88% 104%, rgba(45, 212, 191, 0.06), transparent 55%);
}

.auth-shell--light .glass-card__sheen {
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.9) 0%, transparent 3px),
    radial-gradient(140% 46% at 50% 0%, rgba(224, 242, 254, 0.5), transparent 62%);
}

.brand-logo {
  display: inline-flex;
  height: 3.25rem;
  width: 3.25rem;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 1rem;
  border: 1px solid rgba(165, 243, 252, 0.16);
  background: rgba(4, 10, 22, 0.55);
  box-shadow:
    0 10px 26px -8px rgba(0, 4, 12, 0.7),
    0 0 0 1px rgba(255, 255, 255, 0.04) inset;
}

.auth-shell--light .brand-logo {
  border-color: rgba(15, 23, 42, 0.08);
  background: rgba(255, 255, 255, 0.85);
  box-shadow: 0 10px 24px -10px rgba(15, 23, 42, 0.25);
}

/* 品牌名降级成 eyebrow：真正的标题是下面的「登录」。
 * 原来两者字重接近、上下堆叠，互相抢焦点。 */
.brand-name {
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: #8fb4c6;
}

.auth-shell--light .brand-name {
  color: #64748b;
}

.brand-subtitle {
  font-size: 0.7rem !important;
  letter-spacing: 0.14em;
  color: #5b7a8c;
}

.auth-shell--light .brand-subtitle {
  color: #94a3b8;
}

/* 页脚在卡片之外，压的是深空背景——不管卡片是浅是深，这里永远需要浅色字。
 * 之前浅色主题给了 #475569，直接糊进星空里。
 * 链接色也必须就地覆写：--auth-link 在浅色主题下是深青，同理会消失。 */
.auth-footer {
  color: #a9bdc9;
  --auth-link: #5fdcd0;
  --auth-link-hover: #9cf0e6;
}

.auth-shell--light .auth-footer {
  color: #b9cad4;
}

.auth-copy {
  color: rgba(100, 116, 139, 0.8);
}

.auth-shell--light .auth-copy {
  color: #64748b;
}

</style>
