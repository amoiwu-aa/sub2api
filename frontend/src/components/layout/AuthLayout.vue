<template>
  <div
    class="auth-shell relative flex min-h-screen items-center justify-center overflow-hidden px-4 py-10"
    :class="isDark ? 'auth-shell--dark' : 'auth-shell--light'"
  >
    <GalaxyBackground v-if="isDark" variant="auth" />

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
      <div v-if="settingsLoaded" class="auth-reveal mb-5 text-center" style="--reveal-delay: 60ms">
        <div class="brand-logo mx-auto">
          <img :src="siteLogo || '/ringstar-logo.jpg?v=4'" alt="Logo" class="h-full w-full object-contain" />
        </div>
      </div>

      <div class="glass-card">
        <div class="glass-card__glow" aria-hidden="true"></div>
        <div class="glass-card__sheen" aria-hidden="true"></div>

        <div class="relative z-10">
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
  const dark = savedTheme === 'dark'
  isDark.value = dark
  document.documentElement.classList.toggle('dark', dark)
}

onMounted(() => {
  initTheme()
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
/* 主题色板以 CSS 变量下发给表单。
 *
 * 不用 `:global(html:not(.dark)) .login-input` 那套：本项目的 scoped CSS 编译
 * 会把 `:global(X) Y`（:global 作为前导、后面还跟后代）这种规则整条丢弃，
 * 与 :not() 无关——生产构建产物里一条都不剩（护栏见
 * src/__tests__/scoped-global-selector.spec.ts），
 * 结果是登录表单的浅色样式从来没生效过——无论切到哪个主题，输入框都渲染
 * 深色底，压在浅色卡片上就是几块灰疙瘩。
 * 自定义属性的继承不受作用域约束，父组件定义、子组件消费，天然可靠。 */
.auth-shell--dark {
  background: #040914;
}

.auth-shell--light {
  background: #ffffff;
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
  --auth-title: #2c4a5e;
  --auth-label: #2c4a5e;
  --auth-muted: #6b8796;
  --auth-field-bg: #ffffff;
  --auth-field-border: rgba(126, 200, 227, 0.45);
  --auth-field-border-hover: rgba(91, 184, 214, 0.7);
  --auth-field-text: #2c4a5e;
  --auth-field-placeholder: #9bb0be;
  --auth-field-bg-focus: #ffffff;
  --auth-icon: #7ec8e3;
  --auth-link: #4ba3c7;
  --auth-link-hover: #d4a017;
  --auth-divider: rgba(126, 200, 227, 0.28);
  --auth-submit-bg: #5bb8d6;
  --auth-submit-color: #143447;
  --auth-submit-border: transparent;
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
  border-radius: 9999px;
  border-color: rgba(126, 200, 227, 0.35);
  background: #ffffff;
  box-shadow: 0 6px 16px rgba(126, 200, 227, 0.16);
}

.auth-control :deep(button) {
  color: #cbd5e1;
}

.auth-shell--light .auth-control :deep(button) {
  border-radius: 9999px;
  color: #3d6a80;
}

.auth-control :deep(button:hover) {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
}

.auth-shell--light .auth-control :deep(button:hover) {
  background: #e8f6fc;
  color: #2c4a5e;
}

.auth-control :deep(.absolute) {
  border-color: rgba(165, 243, 252, 0.16);
  background: rgba(15, 23, 42, 0.95);
  backdrop-filter: blur(16px);
}

.auth-shell--light .auth-control :deep(.absolute) {
  border-radius: 1rem;
  border-color: rgba(126, 200, 227, 0.32);
  background: #ffffff;
  box-shadow: 0 12px 28px rgba(126, 200, 227, 0.18);
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
  border-radius: 9999px;
  border-color: rgba(126, 200, 227, 0.35);
  background: #ffffff;
  color: #3d6a80;
  box-shadow: 0 6px 16px rgba(126, 200, 227, 0.16);
}

.auth-control-btn:hover {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
}

.auth-shell--light .auth-control-btn:hover {
  background: #e8f6fc;
  color: #2c4a5e;
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
  border-radius: 1.75rem;
  border-color: rgba(126, 200, 227, 0.32);
  background: #ffffff;
  box-shadow:
    0 18px 44px rgba(126, 200, 227, 0.22),
    0 4px 12px rgba(242, 192, 122, 0.08);
  backdrop-filter: none;
  -webkit-backdrop-filter: none;
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
    rgba(126, 200, 227, 0.55) 0%,
    rgba(242, 192, 122, 0.28) 45%,
    rgba(248, 212, 216, 0.22) 100%
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
  background: none;
}

.brand-logo {
  display: inline-flex;
  height: 9rem;
  width: 9rem;
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
  border: none;
  background: transparent;
  box-shadow: none;
  border-radius: 0;
  overflow: visible;
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
  color: #5a8ba3;
}

.brand-subtitle {
  font-size: 0.7rem !important;
  letter-spacing: 0.14em;
  color: #5b7a8c;
}

.auth-shell--light .brand-subtitle {
  color: #8aa4b3;
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
  color: #6b8796;
  --auth-link: #4ba3c7;
  --auth-link-hover: #d4a017;
}

.auth-copy {
  color: rgba(100, 116, 139, 0.8);
}

.auth-shell--light .auth-copy {
  color: #8aa4b3;
}

</style>
