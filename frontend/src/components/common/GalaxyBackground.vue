<template>
  <div
    class="galaxy pointer-events-none absolute inset-0 overflow-hidden"
    :class="{ 'galaxy--auth': variant === 'auth' }"
    aria-hidden="true"
  >
    <div class="galaxy__sky"></div>
    <div class="galaxy__nebula galaxy__nebula--a" :class="{ 'is-static': reduceMotion }"></div>
    <div class="galaxy__nebula galaxy__nebula--b" :class="{ 'is-static': reduceMotion }"></div>
    <div class="galaxy__nebula galaxy__nebula--c" :class="{ 'is-static': reduceMotion }"></div>
    <div class="galaxy__far-galaxy galaxy__far-galaxy--1" :class="{ 'is-static': reduceMotion }"></div>
    <div class="galaxy__far-galaxy galaxy__far-galaxy--2" :class="{ 'is-static': reduceMotion }"></div>
    <div class="galaxy__ring" :class="{ 'is-static': reduceMotion }"></div>
    <div class="galaxy__dust" :class="{ 'is-static': reduceMotion }"></div>
    <canvas ref="canvasRef" class="galaxy__canvas"></canvas>
    <div class="galaxy__vignette"></div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

const props = withDefaults(
  defineProps<{
    variant?: 'home' | 'auth'
  }>(),
  { variant: 'home' }
)

type Star = {
  x: number
  y: number
  z: number
  r: number
  twinkle: number
  twinkleSpeed: number
  baseAlpha: number
}

type ShootingStar = {
  x: number
  y: number
  len: number
  speed: number
  life: number
  maxLife: number
  angle: number
}

type QuantumTrail = {
  points: { x: number; y: number }[]
  t: number
  speed: number
  width: number
  alpha: number
}

const canvasRef = ref<HTMLCanvasElement | null>(null)
const reduceMotion = ref(false)

let rafId = 0
let resizeObserver: ResizeObserver | null = null
let stars: Star[] = []
let trails: QuantumTrail[] = []
let shooting: ShootingStar | null = null
let width = 0
let height = 0
let dpr = 1
let running = false
let lastTs = 0
let spawnCooldown = 900
let mediaQuery: MediaQueryList | null = null
let onMotionChange: ((event: MediaQueryListEvent) => void) | null = null

function prefersReducedMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function starCountForSize(w: number, h: number): number {
  const area = w * h
  const boost = props.variant === 'auth' ? 1.35 : 1
  if (area < 500_000) return Math.floor(90 * boost)
  if (area < 1_200_000) return Math.floor(140 * boost)
  return Math.floor(190 * boost)
}

function seedStars(count: number) {
  stars = Array.from({ length: count }, () => {
    const z = Math.random()
    return {
      x: Math.random() * width,
      y: Math.random() * height,
      z,
      r: 0.35 + z * 1.7 + Math.random() * 0.55,
      twinkle: Math.random() * Math.PI * 2,
      twinkleSpeed: 0.015 + Math.random() * 0.04,
      baseAlpha: 0.35 + z * 0.55
    }
  })
}

function seedTrails() {
  if (props.variant !== 'auth') {
    trails = []
    return
  }
  const count = width < 700 ? 8 : 14
  trails = Array.from({ length: count }, () => {
    const x0 = Math.random() * width
    const y0 = Math.random() * height
    const x1 = x0 + (Math.random() - 0.5) * width * 0.55
    const y1 = y0 + (Math.random() - 0.5) * height * 0.4
    const x2 = x1 + (Math.random() - 0.5) * width * 0.35
    const y2 = y1 + (Math.random() - 0.5) * height * 0.3
    return {
      points: [
        { x: x0, y: y0 },
        { x: x1, y: y1 },
        { x: x2, y: y2 }
      ],
      t: Math.random(),
      speed: 0.00035 + Math.random() * 0.00055,
      width: 0.6 + Math.random() * 0.9,
      alpha: 0.08 + Math.random() * 0.12
    }
  })
}

function resizeCanvas() {
  const canvas = canvasRef.value
  if (!canvas) return
  const parent = canvas.parentElement
  if (!parent) return

  const rect = parent.getBoundingClientRect()
  width = Math.max(1, Math.floor(rect.width))
  height = Math.max(1, Math.floor(rect.height))
  dpr = Math.min(window.devicePixelRatio || 1, 1.75)

  canvas.width = Math.floor(width * dpr)
  canvas.height = Math.floor(height * dpr)
  canvas.style.width = `${width}px`
  canvas.style.height = `${height}px`

  const ctx = canvas.getContext('2d')
  if (ctx) ctx.setTransform(dpr, 0, 0, dpr, 0, 0)

  const target = starCountForSize(width, height)
  if (Math.abs(stars.length - target) > 8) {
    seedStars(target)
  }
  seedTrails()
}

function spawnShootingStar() {
  const fromTop = Math.random() > 0.35
  shooting = {
    x: Math.random() * width * 0.85,
    y: fromTop ? Math.random() * height * 0.35 : Math.random() * height * 0.2,
    len: 70 + Math.random() * 110,
    speed: 8 + Math.random() * 7,
    life: 0,
    maxLife: 42 + Math.random() * 28,
    angle: Math.PI / 5 + Math.random() * 0.35
  }
}

function drawStars(ctx: CanvasRenderingContext2D, alphaScale = 1) {
  for (const star of stars) {
    const alpha = Math.max(
      0.08,
      Math.min(1, (star.baseAlpha + 0.35 * Math.sin(star.twinkle)) * alphaScale)
    )
    ctx.beginPath()
    ctx.fillStyle = `rgba(236, 252, 255, ${alpha})`
    ctx.arc(star.x, star.y, star.r, 0, Math.PI * 2)
    ctx.fill()

    if (star.z > 0.78) {
      ctx.beginPath()
      ctx.fillStyle = `rgba(165, 243, 252, ${alpha * 0.35})`
      ctx.arc(star.x, star.y, star.r * 2.4, 0, Math.PI * 2)
      ctx.fill()
    }
  }
}

function drawTrails(ctx: CanvasRenderingContext2D) {
  for (const trail of trails) {
    const [p0, p1, p2] = trail.points
    const grad = ctx.createLinearGradient(p0.x, p0.y, p2.x, p2.y)
    grad.addColorStop(0, `rgba(34, 211, 238, 0)`)
    grad.addColorStop(0.45, `rgba(125, 211, 252, ${trail.alpha})`)
    grad.addColorStop(1, `rgba(45, 212, 191, 0)`)
    ctx.strokeStyle = grad
    ctx.lineWidth = trail.width
    ctx.beginPath()
    ctx.moveTo(p0.x, p0.y)
    ctx.quadraticCurveTo(p1.x, p1.y, p2.x, p2.y)
    ctx.stroke()

    const t = trail.t
    const ox = (1 - t) * (1 - t) * p0.x + 2 * (1 - t) * t * p1.x + t * t * p2.x
    const oy = (1 - t) * (1 - t) * p0.y + 2 * (1 - t) * t * p1.y + t * t * p2.y
    ctx.beginPath()
    ctx.fillStyle = `rgba(224, 255, 255, ${Math.min(0.9, trail.alpha * 4)})`
    ctx.arc(ox, oy, 1.2, 0, Math.PI * 2)
    ctx.fill()
  }
}

function drawShootingStar(ctx: CanvasRenderingContext2D) {
  if (!shooting) return
  const t = shooting.life / shooting.maxLife
  const fade = t < 0.2 ? t / 0.2 : t > 0.7 ? (1 - t) / 0.3 : 1
  const dx = Math.cos(shooting.angle) * shooting.len
  const dy = Math.sin(shooting.angle) * shooting.len
  const grad = ctx.createLinearGradient(
    shooting.x,
    shooting.y,
    shooting.x - dx,
    shooting.y - dy
  )
  grad.addColorStop(0, `rgba(255, 255, 255, ${0.9 * fade})`)
  grad.addColorStop(0.35, `rgba(165, 243, 252, ${0.45 * fade})`)
  grad.addColorStop(1, 'rgba(56, 189, 248, 0)')
  ctx.strokeStyle = grad
  ctx.lineWidth = 2
  ctx.lineCap = 'round'
  ctx.beginPath()
  ctx.moveTo(shooting.x, shooting.y)
  ctx.lineTo(shooting.x - dx, shooting.y - dy)
  ctx.stroke()
}

function drawStaticScene(ctx: CanvasRenderingContext2D) {
  ctx.clearRect(0, 0, width, height)
  if (!stars.length) seedStars(starCountForSize(width, height))
  drawStars(ctx, 0.85)
  if (props.variant === 'auth') drawTrails(ctx)
}

function drawFrame(ts: number) {
  if (!running) return
  const canvas = canvasRef.value
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  const dt = Math.min(32, ts - (lastTs || ts))
  lastTs = ts
  ctx.clearRect(0, 0, width, height)

  for (const star of stars) {
    star.twinkle += star.twinkleSpeed * (dt / 16)
    const drift = (0.012 + star.z * 0.04) * (dt / 16)
    star.x += drift
    star.y += drift * 0.18
    if (star.x > width + 4) star.x = -4
    if (star.y > height + 4) star.y = -4
  }

  for (const trail of trails) {
    trail.t += trail.speed * dt
    if (trail.t > 1) {
      trail.t = 0
      const x0 = Math.random() * width
      const y0 = Math.random() * height
      trail.points = [
        { x: x0, y: y0 },
        {
          x: x0 + (Math.random() - 0.5) * width * 0.5,
          y: y0 + (Math.random() - 0.5) * height * 0.35
        },
        {
          x: x0 + (Math.random() - 0.5) * width * 0.7,
          y: y0 + (Math.random() - 0.5) * height * 0.45
        }
      ]
    }
  }

  if (props.variant === 'auth') drawTrails(ctx)
  drawStars(ctx, 1)

  spawnCooldown -= dt
  if (!shooting && spawnCooldown <= 0) {
    spawnShootingStar()
    spawnCooldown = 900 + Math.random() * 1600
  }

  if (shooting) {
    shooting.life += dt / 16
    shooting.x += Math.cos(shooting.angle) * shooting.speed * (dt / 16)
    shooting.y += Math.sin(shooting.angle) * shooting.speed * (dt / 16)
    drawShootingStar(ctx)
    if (shooting.life >= shooting.maxLife) shooting = null
  }

  rafId = requestAnimationFrame(drawFrame)
}

function start() {
  if (running || reduceMotion.value) return
  running = true
  lastTs = 0
  rafId = requestAnimationFrame(drawFrame)
}

function stop() {
  running = false
  if (rafId) {
    cancelAnimationFrame(rafId)
    rafId = 0
  }
}

function onVisibility() {
  if (document.hidden) stop()
  else if (!reduceMotion.value) start()
}

function paintStaticIfNeeded() {
  const ctx = canvasRef.value?.getContext('2d')
  if (ctx && reduceMotion.value) drawStaticScene(ctx)
}

onMounted(() => {
  reduceMotion.value = prefersReducedMotion()
  resizeCanvas()
  paintStaticIfNeeded()

  const canvas = canvasRef.value
  resizeObserver = new ResizeObserver(() => {
    resizeCanvas()
    paintStaticIfNeeded()
  })
  if (canvas?.parentElement) resizeObserver.observe(canvas.parentElement)

  mediaQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
  onMotionChange = (event) => {
    reduceMotion.value = event.matches
    if (reduceMotion.value) {
      stop()
      paintStaticIfNeeded()
    } else {
      start()
    }
  }
  mediaQuery.addEventListener('change', onMotionChange)
  document.addEventListener('visibilitychange', onVisibility)
  if (!reduceMotion.value) start()
})

onUnmounted(() => {
  stop()
  document.removeEventListener('visibilitychange', onVisibility)
  if (mediaQuery && onMotionChange) mediaQuery.removeEventListener('change', onMotionChange)
  resizeObserver?.disconnect()
  resizeObserver = null
})
</script>

<style scoped>
.galaxy {
  z-index: 0;
}

.galaxy__sky {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(120% 80% at 18% 12%, rgba(14, 165, 233, 0.18), transparent 55%),
    radial-gradient(90% 70% at 82% 18%, rgba(45, 212, 191, 0.14), transparent 50%),
    radial-gradient(80% 60% at 50% 100%, rgba(8, 47, 73, 0.2), transparent 55%),
    linear-gradient(165deg, #0b1220 0%, #102a43 42%, #0f3d4c 100%);
}

:global(html:not(.dark)) .galaxy__sky {
  background:
    radial-gradient(120% 80% at 15% 8%, rgba(56, 189, 248, 0.35), transparent 55%),
    radial-gradient(90% 70% at 88% 20%, rgba(45, 212, 191, 0.28), transparent 52%),
    radial-gradient(80% 55% at 50% 110%, rgba(125, 211, 252, 0.22), transparent 55%),
    linear-gradient(168deg, #e0f2fe 0%, #ecfeff 45%, #f0fdfa 100%);
}

:global(html:not(.dark)) .galaxy--auth .galaxy__sky {
  background:
    radial-gradient(110% 75% at 12% 10%, rgba(34, 211, 238, 0.28), transparent 52%),
    radial-gradient(95% 70% at 88% 16%, rgba(56, 189, 248, 0.22), transparent 50%),
    radial-gradient(80% 55% at 48% 108%, rgba(14, 116, 144, 0.12), transparent 55%),
    linear-gradient(168deg, #dbeafe 0%, #e0f2fe 46%, #f0fdfa 100%);
}

:global(html:not(.dark)) .galaxy__vignette {
  background:
    radial-gradient(115% 85% at 50% 35%, transparent 42%, rgba(224, 242, 254, 0.45) 100%),
    linear-gradient(to bottom, rgba(224, 242, 254, 0.2), transparent 18%, transparent 78%, rgba(240, 253, 250, 0.35));
}

:global(html:not(.dark)) .galaxy__ring {
  border-color: rgba(8, 145, 178, 0.28);
  opacity: 0.55;
}

:global(html:not(.dark)) .galaxy__far-galaxy {
  opacity: 0.28;
}

.galaxy--auth .galaxy__sky {
  background:
    radial-gradient(110% 75% at 12% 10%, rgba(34, 211, 238, 0.32), transparent 52%),
    radial-gradient(95% 70% at 88% 16%, rgba(56, 189, 248, 0.26), transparent 50%),
    radial-gradient(80% 55% at 48% 108%, rgba(8, 47, 73, 0.4), transparent 55%),
    linear-gradient(168deg, #040914 0%, #0a1f33 46%, #07343c 100%);
}

.galaxy--auth .galaxy__nebula--a {
  opacity: 1;
  background: radial-gradient(ellipse 48% 38% at 22% 30%, rgba(34, 211, 238, 0.5), transparent 70%);
}

.galaxy--auth .galaxy__nebula--b {
  opacity: 0.95;
  background: radial-gradient(ellipse 50% 40% at 78% 62%, rgba(56, 189, 248, 0.4), transparent 72%);
}

.galaxy__nebula {
  position: absolute;
  inset: -20%;
  filter: blur(28px);
  opacity: 0.85;
  will-change: transform, opacity;
}

.galaxy__nebula--a {
  background: radial-gradient(ellipse 42% 34% at 22% 30%, rgba(34, 211, 238, 0.38), transparent 70%);
  animation: nebula-a 18s ease-in-out infinite alternate;
}

.galaxy__nebula--b {
  background: radial-gradient(ellipse 46% 36% at 78% 62%, rgba(56, 189, 248, 0.28), transparent 72%);
  animation: nebula-b 22s ease-in-out infinite alternate;
}

.galaxy__nebula--c {
  background: radial-gradient(ellipse 36% 28% at 55% 18%, rgba(153, 246, 228, 0.18), transparent 70%);
  animation: nebula-c 16s ease-in-out infinite alternate;
}

.galaxy__far-galaxy {
  position: absolute;
  border-radius: 50%;
  opacity: 0.35;
  filter: blur(1px);
  animation: far-spin 90s linear infinite;
}

.galaxy__far-galaxy--1 {
  left: 8%;
  top: 16%;
  width: 180px;
  height: 90px;
  background:
    radial-gradient(ellipse at center, rgba(125, 211, 252, 0.35) 0%, transparent 68%),
    conic-gradient(from 40deg, transparent, rgba(45, 212, 191, 0.25), transparent 40%);
  transform: rotate(-24deg);
}

.galaxy__far-galaxy--2 {
  right: 10%;
  bottom: 18%;
  width: 220px;
  height: 110px;
  background:
    radial-gradient(ellipse at center, rgba(56, 189, 248, 0.28) 0%, transparent 70%),
    conic-gradient(from 120deg, transparent, rgba(34, 211, 238, 0.2), transparent 45%);
  transform: rotate(16deg);
  animation-duration: 120s;
  animation-direction: reverse;
}

.galaxy__ring {
  position: absolute;
  left: 58%;
  top: 28%;
  width: min(520px, 58vw);
  aspect-ratio: 1.7 / 1;
  transform: translate(-30%, -20%) rotate(-18deg);
  border-radius: 50%;
  border: 1.5px solid rgba(165, 243, 252, 0.22);
  box-shadow:
    0 0 40px rgba(34, 211, 238, 0.12),
    inset 0 0 30px rgba(45, 212, 191, 0.08);
  animation: ring-spin 48s linear infinite;
  opacity: 0.7;
}

.galaxy__ring::before {
  content: '';
  position: absolute;
  inset: 12% 8%;
  border-radius: 50%;
  border: 1px solid rgba(125, 211, 252, 0.16);
}

.galaxy--auth .galaxy__ring {
  left: 50%;
  top: 42%;
  opacity: 0.45;
  width: min(640px, 70vw);
}

.galaxy__dust {
  position: absolute;
  inset: 0;
  background-image: radial-gradient(rgba(255, 255, 255, 0.35) 0.6px, transparent 0.7px);
  background-size: 3px 3px;
  opacity: 0.05;
  animation: dust-drift 40s linear infinite;
  mask-image: radial-gradient(ellipse 70% 60% at 50% 40%, #000 20%, transparent 75%);
}

.galaxy__canvas {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
}

.galaxy__vignette {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(115% 85% at 50% 35%, transparent 40%, rgba(2, 8, 18, 0.55) 100%),
    linear-gradient(to bottom, rgba(2, 8, 18, 0.25), transparent 18%, transparent 78%, rgba(2, 8, 18, 0.45));
}

.galaxy--auth .galaxy__vignette {
  background:
    radial-gradient(90% 70% at 50% 45%, transparent 30%, rgba(2, 8, 18, 0.45) 100%),
    linear-gradient(to bottom, rgba(2, 8, 18, 0.35), transparent 22%, transparent 75%, rgba(2, 8, 18, 0.5));
}

.is-static {
  animation: none !important;
}

@keyframes nebula-a {
  0% {
    transform: translate3d(-2%, -1%, 0) scale(1);
    opacity: 0.7;
  }
  100% {
    transform: translate3d(4%, 3%, 0) scale(1.12);
    opacity: 1;
  }
}

@keyframes nebula-b {
  0% {
    transform: translate3d(3%, 2%, 0) scale(1.05);
    opacity: 0.65;
  }
  100% {
    transform: translate3d(-4%, -2%, 0) scale(1.15);
    opacity: 0.95;
  }
}

@keyframes nebula-c {
  0% {
    transform: translate3d(0, 2%, 0) scale(1);
    opacity: 0.5;
  }
  100% {
    transform: translate3d(-2%, -3%, 0) scale(1.1);
    opacity: 0.85;
  }
}

@keyframes ring-spin {
  from {
    transform: translate(-30%, -20%) rotate(-18deg);
  }
  to {
    transform: translate(-30%, -20%) rotate(342deg);
  }
}

@keyframes far-spin {
  from {
    filter: blur(1px) hue-rotate(0deg);
  }
  to {
    filter: blur(1px) hue-rotate(25deg);
  }
}

@keyframes dust-drift {
  from {
    background-position: 0 0;
  }
  to {
    background-position: 120px 80px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .galaxy__nebula,
  .galaxy__ring,
  .galaxy__dust,
  .galaxy__far-galaxy {
    animation: none !important;
  }
}
</style>
