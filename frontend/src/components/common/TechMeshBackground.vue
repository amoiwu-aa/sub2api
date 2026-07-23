<template>
  <div class="tech-bg pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
    <div class="tech-bg__base"></div>
    <div class="tech-bg__noise"></div>
    <div class="tech-bg__aurora" :class="{ 'is-static': reduceMotion }"></div>
    <div class="tech-bg__lattice" :class="{ 'is-static': reduceMotion }"></div>
    <div class="tech-bg__floor" :class="{ 'is-static': reduceMotion }"></div>
    <div class="tech-bg__horizon"></div>
    <div class="tech-bg__beams" :class="{ 'is-static': reduceMotion }">
      <i></i><i></i><i></i>
    </div>
    <div v-if="!reduceMotion" class="tech-bg__scan"></div>
    <div class="tech-bg__rings" :class="{ 'is-static': reduceMotion }">
      <span></span><span></span><span></span>
    </div>
    <canvas ref="canvasRef" class="tech-bg__canvas"></canvas>
    <div class="tech-bg__vignette"></div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

type Particle = {
  x: number
  y: number
  vx: number
  vy: number
  r: number
  pulse: number
  pulseSpeed: number
}

const canvasRef = ref<HTMLCanvasElement | null>(null)
const reduceMotion = ref(false)

let rafId = 0
let resizeObserver: ResizeObserver | null = null
let particles: Particle[] = []
let width = 0
let height = 0
let dpr = 1
let running = false
let lastTs = 0
let scanY = 0
let mediaQuery: MediaQueryList | null = null
let onMotionChange: ((event: MediaQueryListEvent) => void) | null = null

const LINK_DIST = 180
const PRIMARY = { r: 13, g: 148, b: 136 }
const GLOW = { r: 45, g: 212, b: 191 }

function prefersReducedMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function particleCountForSize(w: number, h: number): number {
  const area = w * h
  if (area < 500_000) return 42
  if (area < 1_200_000) return 68
  return 88
}

function seedParticles(count: number) {
  particles = Array.from({ length: count }, () => ({
    x: Math.random() * width,
    y: Math.random() * height,
    vx: (Math.random() - 0.5) * 0.5,
    vy: (Math.random() - 0.5) * 0.5,
    r: 1.6 + Math.random() * 2.4,
    pulse: Math.random() * Math.PI * 2,
    pulseSpeed: 0.02 + Math.random() * 0.03
  }))
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

  const target = particleCountForSize(width, height)
  if (Math.abs(particles.length - target) > 4) {
    seedParticles(target)
  }
}

function drawNetwork(ctx: CanvasRenderingContext2D, alpha = 1) {
  for (let i = 0; i < particles.length; i++) {
    for (let j = i + 1; j < particles.length; j++) {
      const a = particles[i]
      const b = particles[j]
      const dx = a.x - b.x
      const dy = a.y - b.y
      const dist = Math.hypot(dx, dy)
      if (dist < LINK_DIST) {
        const lineAlpha = (1 - dist / LINK_DIST) * 0.55 * alpha
        ctx.strokeStyle = `rgba(${GLOW.r}, ${GLOW.g}, ${GLOW.b}, ${lineAlpha})`
        ctx.lineWidth = 1.1
        ctx.beginPath()
        ctx.moveTo(a.x, a.y)
        ctx.lineTo(b.x, b.y)
        ctx.stroke()
      }
    }
  }

  for (const p of particles) {
    const glow = (0.55 + 0.4 * Math.sin(p.pulse)) * alpha

    ctx.beginPath()
    ctx.fillStyle = `rgba(${GLOW.r}, ${GLOW.g}, ${GLOW.b}, ${glow * 0.18})`
    ctx.arc(p.x, p.y, p.r * 4.2, 0, Math.PI * 2)
    ctx.fill()

    ctx.beginPath()
    ctx.fillStyle = `rgba(${PRIMARY.r}, ${PRIMARY.g}, ${PRIMARY.b}, ${glow})`
    ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2)
    ctx.fill()

    ctx.beginPath()
    ctx.fillStyle = `rgba(240, 253, 250, ${glow * 0.75})`
    ctx.arc(p.x, p.y, Math.max(0.9, p.r * 0.42), 0, Math.PI * 2)
    ctx.fill()
  }
}

function drawStaticScene(ctx: CanvasRenderingContext2D) {
  ctx.clearRect(0, 0, width, height)
  if (!particles.length) {
    seedParticles(Math.min(36, particleCountForSize(width, height)))
  }
  drawNetwork(ctx, 0.8)
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

  // sweeping diagonal energy bar
  const beamX = ((ts * 0.04) % (width + 280)) - 140
  const beam = ctx.createLinearGradient(beamX, 0, beamX + 220, height)
  beam.addColorStop(0, 'rgba(56,189,248,0)')
  beam.addColorStop(0.5, 'rgba(45,212,191,0.12)')
  beam.addColorStop(1, 'rgba(20,184,166,0)')
  ctx.fillStyle = beam
  ctx.fillRect(0, 0, width, height)

  scanY = (scanY + dt * 0.07) % (height + 200)
  const band = ctx.createLinearGradient(0, scanY - 100, 0, scanY + 100)
  band.addColorStop(0, 'rgba(20,184,166,0)')
  band.addColorStop(0.5, 'rgba(45,212,191,0.18)')
  band.addColorStop(1, 'rgba(20,184,166,0)')
  ctx.fillStyle = band
  ctx.fillRect(0, scanY - 100, width, 200)

  for (const p of particles) {
    p.x += p.vx * (dt / 16)
    p.y += p.vy * (dt / 16)
    p.pulse += p.pulseSpeed * (dt / 16)
    if (p.x < -28) p.x = width + 28
    if (p.x > width + 28) p.x = -28
    if (p.y < -28) p.y = height + 28
    if (p.y > height + 28) p.y = -28
  }

  drawNetwork(ctx, 1)
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
.tech-bg {
  z-index: 0;
}

.tech-bg__base {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(100% 70% at 10% 0%, rgba(45, 212, 191, 0.34), transparent 55%),
    radial-gradient(90% 60% at 90% 8%, rgba(14, 165, 233, 0.22), transparent 52%),
    radial-gradient(80% 50% at 50% 100%, rgba(13, 148, 136, 0.26), transparent 55%),
    linear-gradient(165deg, #f8fafc 0%, #ccfbf1 38%, #e0f2fe 100%);
}

:global(.dark) .tech-bg__base {
  background:
    radial-gradient(100% 70% at 8% 0%, rgba(20, 184, 166, 0.38), transparent 52%),
    radial-gradient(90% 60% at 94% 4%, rgba(14, 165, 233, 0.24), transparent 50%),
    radial-gradient(80% 55% at 50% 110%, rgba(13, 148, 136, 0.3), transparent 55%),
    linear-gradient(168deg, #020617 0%, #06202a 46%, #042f2e 100%);
}

.tech-bg__noise {
  position: absolute;
  inset: 0;
  opacity: 0.035;
  background-image: url("data:image/svg+xml,%3Csvg viewBox='0 0 200 200' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.85' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E");
  mix-blend-mode: multiply;
}

:global(.dark) .tech-bg__noise {
  opacity: 0.08;
  mix-blend-mode: soft-light;
}

.tech-bg__aurora {
  position: absolute;
  inset: -30%;
  background:
    radial-gradient(ellipse 48% 34% at 16% 24%, rgba(45, 212, 191, 0.42), transparent 70%),
    radial-gradient(ellipse 42% 36% at 84% 70%, rgba(56, 189, 248, 0.3), transparent 70%),
    radial-gradient(ellipse 34% 28% at 55% 12%, rgba(94, 234, 212, 0.26), transparent 70%);
  filter: blur(12px);
  animation: aurora-drift 14s ease-in-out infinite alternate;
}

:global(.dark) .tech-bg__aurora {
  background:
    radial-gradient(ellipse 48% 34% at 14% 22%, rgba(20, 184, 166, 0.48), transparent 70%),
    radial-gradient(ellipse 42% 36% at 86% 72%, rgba(14, 165, 233, 0.32), transparent 70%),
    radial-gradient(ellipse 34% 28% at 54% 10%, rgba(45, 212, 191, 0.28), transparent 70%);
}

.tech-bg__lattice {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(13, 148, 136, 0.11) 1px, transparent 1px),
    linear-gradient(90deg, rgba(13, 148, 136, 0.11) 1px, transparent 1px);
  background-size: 64px 64px;
  mask-image: radial-gradient(ellipse 80% 70% at 50% 40%, #000 20%, transparent 75%);
  animation: lattice-shift 28s linear infinite;
}

:global(.dark) .tech-bg__lattice {
  background-image:
    linear-gradient(rgba(45, 212, 191, 0.16) 1px, transparent 1px),
    linear-gradient(90deg, rgba(45, 212, 191, 0.16) 1px, transparent 1px);
}

.tech-bg__floor {
  position: absolute;
  left: -25%;
  right: -25%;
  bottom: -8%;
  height: 78%;
  background-image:
    linear-gradient(rgba(13, 148, 136, 0.28) 1px, transparent 1px),
    linear-gradient(90deg, rgba(13, 148, 136, 0.28) 1px, transparent 1px);
  background-size: 44px 44px;
  transform-origin: 50% 100%;
  transform: perspective(780px) rotateX(64deg);
  mask-image: linear-gradient(to top, rgba(0, 0, 0, 0.85) 0%, rgba(0, 0, 0, 0.4) 45%, transparent 85%);
  animation: grid-scroll 10s linear infinite;
}

:global(.dark) .tech-bg__floor {
  background-image:
    linear-gradient(rgba(45, 212, 191, 0.34) 1px, transparent 1px),
    linear-gradient(90deg, rgba(45, 212, 191, 0.34) 1px, transparent 1px);
}

.tech-bg__horizon {
  position: absolute;
  left: -8%;
  right: -8%;
  bottom: 22%;
  height: 3px;
  background: linear-gradient(
    90deg,
    transparent,
    rgba(13, 148, 136, 0.2) 15%,
    rgba(45, 212, 191, 0.7) 50%,
    rgba(56, 189, 248, 0.25) 85%,
    transparent
  );
  box-shadow: 0 0 28px rgba(45, 212, 191, 0.35);
}

.tech-bg__beams {
  position: absolute;
  inset: 0;
  overflow: hidden;
}

.tech-bg__beams i {
  position: absolute;
  top: -25%;
  width: 2px;
  height: 150%;
  background: linear-gradient(
    180deg,
    transparent,
    rgba(45, 212, 191, 0),
    rgba(45, 212, 191, 0.7),
    rgba(56, 189, 248, 0),
    transparent
  );
  filter: blur(0.6px);
  animation: beam-fall 7.5s linear infinite;
  opacity: 0.7;
}

.tech-bg__beams i:nth-child(1) {
  left: 14%;
  animation-delay: 0s;
}
.tech-bg__beams i:nth-child(2) {
  left: 48%;
  animation-delay: 2.4s;
  opacity: 0.5;
}
.tech-bg__beams i:nth-child(3) {
  left: 78%;
  animation-delay: 4.8s;
}

.tech-bg__scan {
  position: absolute;
  inset: 0;
  background: linear-gradient(
    180deg,
    transparent 0%,
    rgba(45, 212, 191, 0.1) 48%,
    rgba(56, 189, 248, 0.06) 54%,
    transparent 100%
  );
  background-size: 100% 240%;
  animation: scan-sweep 5.8s ease-in-out infinite;
}

.tech-bg__rings {
  position: absolute;
  left: 50%;
  top: 42%;
  width: min(620px, 70vw);
  aspect-ratio: 1;
  transform: translate(-50%, -50%);
  opacity: 0.55;
}

.tech-bg__rings span {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  border: 1px solid rgba(13, 148, 136, 0.35);
  box-shadow: inset 0 0 30px rgba(45, 212, 191, 0.08);
  animation: ring-pulse 7s ease-in-out infinite;
}

.tech-bg__rings span:nth-child(2) {
  inset: 14%;
  border-color: rgba(56, 189, 248, 0.28);
  animation-delay: 1s;
}

.tech-bg__rings span:nth-child(3) {
  inset: 30%;
  border-color: rgba(45, 212, 191, 0.4);
  animation-delay: 2s;
}

:global(.dark) .tech-bg__rings {
  opacity: 0.7;
}

.tech-bg__canvas {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
}

.tech-bg__vignette {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(120% 90% at 50% 36%, transparent 48%, rgba(248, 250, 252, 0.28) 100%),
    linear-gradient(to bottom, rgba(248, 250, 252, 0.08), transparent 14%, transparent 84%, rgba(248, 250, 252, 0.2));
}

:global(.dark) .tech-bg__vignette {
  background:
    radial-gradient(120% 90% at 50% 36%, transparent 45%, rgba(2, 6, 23, 0.5) 100%),
    linear-gradient(to bottom, rgba(2, 6, 23, 0.22), transparent 16%, transparent 82%, rgba(2, 6, 23, 0.38));
}

.is-static {
  animation: none !important;
}

@keyframes aurora-drift {
  0% {
    transform: translate3d(-3%, -2%, 0) scale(1);
    opacity: 0.8;
  }
  50% {
    transform: translate3d(4%, 3%, 0) scale(1.1);
    opacity: 1;
  }
  100% {
    transform: translate3d(-2%, 4%, 0) scale(1.05);
    opacity: 0.88;
  }
}

@keyframes lattice-shift {
  from {
    background-position: 0 0, 0 0;
  }
  to {
    background-position: 64px 64px, -64px 64px;
  }
}

@keyframes grid-scroll {
  from {
    background-position: 0 0, 0 0;
  }
  to {
    background-position: 0 44px, 44px 0;
  }
}

@keyframes scan-sweep {
  0% {
    background-position: 0 -50%;
  }
  100% {
    background-position: 0 150%;
  }
}

@keyframes beam-fall {
  0% {
    transform: translateY(-35%) rotate(14deg);
    opacity: 0;
  }
  14% {
    opacity: 1;
  }
  86% {
    opacity: 1;
  }
  100% {
    transform: translateY(40%) rotate(14deg);
    opacity: 0;
  }
}

@keyframes ring-pulse {
  0%,
  100% {
    transform: scale(0.94);
    opacity: 0.35;
  }
  50% {
    transform: scale(1.05);
    opacity: 0.9;
  }
}

@media (prefers-reduced-motion: reduce) {
  .tech-bg__aurora,
  .tech-bg__lattice,
  .tech-bg__floor,
  .tech-bg__beams i,
  .tech-bg__scan,
  .tech-bg__rings span {
    animation: none !important;
  }
}
</style>
