<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    variant?: 'home' | 'auth'
  }>(),
  {
    variant: 'home'
  }
)

const rootRef = ref<HTMLElement | null>(null)
const canvasRef = ref<HTMLCanvasElement | null>(null)

let rafId = 0
let resizeObserver: ResizeObserver | null = null
let width = 0
let height = 0
let dpr = 1
let stars: Star[] = []
let spiralStars: SpiralStar[] = []
let dust: Dust[] = []
let flares: Flare[] = []
let trails: Trail[] = []
let constellations: Constellation[] = []
let t0 = 0

interface Star {
  x: number
  y: number
  r: number
  base: number
  tw: number
  ph: number
  tone: number
}

interface SpiralStar {
  a: number
  dist: number
  r: number
  base: number
  tw: number
  ph: number
  arm: number
  warm: number
  jitterR: number
  jitterA: number
}

interface Dust {
  x: number
  y: number
  r: number
  a: number
  drift: number
  ph: number
}

interface Flare {
  x: number
  y: number
  r: number
  a: number
  tw: number
  ph: number
}

interface Trail {
  x: number
  y: number
  len: number
  ang: number
  speed: number
  a: number
  w: number
}

interface ConstellationNode {
  x: number
  y: number
  r: number
  tw: number
  ph: number
}

interface ConstellationEdge {
  a: number
  b: number
}

interface Constellation {
  cx: number
  cy: number
  driftX: number
  driftY: number
  ph: number
  nodes: ConstellationNode[]
  edges: ConstellationEdge[]
}

// 神经网络星座：节点随机撒、边按距离连，整体缓慢漂移——银河是"自然"，
// 星座是"AI"，两者叠在同一片深空里。
const CONSTELLATION_LAYOUTS: Array<{
  cx: number
  cy: number
  spreadX: number
  spreadY: number
  nodes: number
  linkDist: number
}> = [
  { cx: 0.15, cy: 0.24, spreadX: 0.17, spreadY: 0.2, nodes: 9, linkDist: 0.085 },
  { cx: 0.85, cy: 0.16, spreadX: 0.16, spreadY: 0.19, nodes: 8, linkDist: 0.08 },
  { cx: 0.9, cy: 0.78, spreadX: 0.15, spreadY: 0.18, nodes: 8, linkDist: 0.075 }
]

const isAuth = computed(() => props.variant === 'auth')

const shootingStars = computed(() =>
  isAuth.value
    ? [
        { top: '14%', delay: '1s', duration: '7.2s' },
        { top: '28%', delay: '4.2s', duration: '8.4s' },
        { top: '46%', delay: '7.8s', duration: '6.8s' },
        { top: '62%', delay: '11s', duration: '9s' },
        { top: '78%', delay: '14.5s', duration: '7.6s' }
      ]
    : [
        { top: '18%', delay: '0.8s', duration: '6.5s' },
        { top: '42%', delay: '3.6s', duration: '7.8s' },
        { top: '68%', delay: '6.4s', duration: '6.2s' }
      ]
)

function rand(min: number, max: number) {
  return min + Math.random() * (max - min)
}

function seedField() {
  const area = Math.max(1, width * height)
  const starCount = Math.min(isAuth.value ? 520 : 360, Math.floor(area / (isAuth.value ? 2200 : 3200)))
  const dustCount = Math.min(isAuth.value ? 90 : 48, Math.floor(area / (isAuth.value ? 14000 : 22000)))
  const flareCount = isAuth.value ? 18 : 8
  const trailCount = isAuth.value ? 10 : 0
  const spiralCount = Math.min(isAuth.value ? 1400 : 700, Math.floor(area / (isAuth.value ? 900 : 1600)))

  stars = Array.from({ length: starCount }, () => ({
    x: Math.random() * width,
    y: Math.random() * height,
    r: rand(0.35, isAuth.value ? 1.9 : 1.45),
    base: rand(0.2, isAuth.value ? 0.95 : 0.78),
    tw: rand(0.45, 1.8),
    ph: rand(0, Math.PI * 2),
    tone: Math.random()
  }))

  dust = Array.from({ length: dustCount }, () => ({
    x: Math.random() * width,
    y: Math.random() * height,
    r: rand(isAuth.value ? 28 : 18, isAuth.value ? 100 : 70),
    a: rand(0.02, isAuth.value ? 0.1 : 0.055),
    drift: rand(0.01, 0.05),
    ph: rand(0, Math.PI * 2)
  }))

  flares = Array.from({ length: flareCount }, () => ({
    x: Math.random() * width,
    y: Math.random() * height,
    r: rand(1.2, 3.4),
    a: rand(0.35, 0.85),
    tw: rand(0.35, 1.1),
    ph: rand(0, Math.PI * 2)
  }))

  trails = Array.from({ length: trailCount }, () => ({
    x: Math.random() * width,
    y: Math.random() * height,
    len: rand(40, 120),
    ang: rand(-0.55, -0.2),
    speed: rand(0.35, 1.1),
    a: rand(0.08, 0.28),
    w: rand(0.6, 1.5)
  }))

  spiralStars = Array.from({ length: spiralCount }, (_, i) => {
    const arm = i % 4
    // denser near core, uneven along arms
    const dist = Math.pow(Math.random(), 0.55) * Math.min(width, height) * (isAuth.value ? 0.62 : 0.45)
    const armWidth = 0.22 + dist * 0.00055
    return {
      a: (arm * Math.PI) / 2 + dist * 0.016 + rand(-armWidth, armWidth),
      dist: dist * rand(0.88, 1.12),
      r: rand(0.25, isAuth.value ? 1.85 : 1.2),
      base: rand(0.12, isAuth.value ? 0.9 : 0.65) * (0.55 + Math.random() * 0.45),
      tw: rand(0.35, 1.5),
      ph: rand(0, Math.PI * 2),
      arm,
      warm: Math.random(),
      jitterR: rand(-18, 18),
      jitterA: rand(-0.08, 0.08)
    }
  })

  // 星座只在 auth 变体出现：登录页是品牌门面，值得这层"AI"隐喻；
  // home 变体内容多，保持安静。
  if (!isAuth.value) {
    constellations = []
    return
  }
  constellations = CONSTELLATION_LAYOUTS.map((layout) => {
    const nodes: ConstellationNode[] = Array.from({ length: layout.nodes }, () => ({
      x: layout.cx + rand(-layout.spreadX, layout.spreadX),
      y: layout.cy + rand(-layout.spreadY, layout.spreadY),
      r: rand(1.1, 2.2),
      tw: rand(0.4, 1.1),
      ph: rand(0, Math.PI * 2)
    }))
    const edges: ConstellationEdge[] = []
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const dx = nodes[i].x - nodes[j].x
        const dy = nodes[i].y - nodes[j].y
        if (Math.hypot(dx, dy) < layout.linkDist) {
          edges.push({ a: i, b: j })
        }
      }
    }
    return {
      cx: layout.cx,
      cy: layout.cy,
      driftX: rand(0.004, 0.009),
      driftY: rand(0.003, 0.007),
      ph: rand(0, Math.PI * 2),
      nodes,
      edges
    }
  })
}

function resize() {
  const el = rootRef.value
  const canvas = canvasRef.value
  if (!el || !canvas) return

  const rect = el.getBoundingClientRect()
  width = Math.max(1, Math.floor(rect.width))
  height = Math.max(1, Math.floor(rect.height))
  dpr = Math.min(window.devicePixelRatio || 1, 2)

  canvas.width = Math.floor(width * dpr)
  canvas.height = Math.floor(height * dpr)
  canvas.style.width = `${width}px`
  canvas.style.height = `${height}px`

  const ctx = canvas.getContext('2d')
  if (ctx) ctx.setTransform(dpr, 0, 0, dpr, 0, 0)

  seedField()
}

function drawMilkyBand(ctx: CanvasRenderingContext2D, time: number) {
  const cx = width * 0.52
  const cy = height * (isAuth.value ? 0.44 : 0.48)
  const bandW = Math.max(width, height) * (isAuth.value ? 1.35 : 1.1)
  const bandH = Math.min(width, height) * (isAuth.value ? 0.42 : 0.28)

  ctx.save()
  ctx.translate(cx, cy)
  ctx.rotate(-0.48 + Math.sin(time * 0.015) * 0.012)

  const g = ctx.createLinearGradient(-bandW / 2, 0, bandW / 2, 0)
  g.addColorStop(0, 'rgba(8, 18, 42, 0)')
  g.addColorStop(0.18, 'rgba(56, 120, 190, 0.16)')
  g.addColorStop(0.36, 'rgba(170, 210, 255, 0.34)')
  g.addColorStop(0.5, 'rgba(255, 248, 235, 0.42)')
  g.addColorStop(0.64, 'rgba(140, 190, 255, 0.32)')
  g.addColorStop(0.82, 'rgba(70, 110, 190, 0.14)')
  g.addColorStop(1, 'rgba(8, 18, 42, 0)')

  ctx.globalCompositeOperation = 'screen'
  ctx.fillStyle = g
  ctx.beginPath()
  ctx.ellipse(0, 0, bandW / 2, bandH / 2, 0, 0, Math.PI * 2)
  ctx.fill()

  // denser luminous core of the band
  const core = ctx.createRadialGradient(0, 0, 0, 0, 0, bandH * 0.55)
  core.addColorStop(0, 'rgba(255, 250, 240, 0.28)')
  core.addColorStop(0.35, 'rgba(180, 210, 255, 0.16)')
  core.addColorStop(1, 'rgba(40, 80, 160, 0)')
  ctx.fillStyle = core
  ctx.beginPath()
  ctx.ellipse(0, 0, bandW * 0.28, bandH * 0.55, 0, 0, Math.PI * 2)
  ctx.fill()

  ctx.restore()
}

function drawSpiral(ctx: CanvasRenderingContext2D, time: number) {
  const cx = width * 0.52
  const cy = height * (isAuth.value ? 0.44 : 0.48)
  const rot = time * 0.01
  const scale = Math.min(width, height)

  ctx.globalCompositeOperation = 'screen'

  // bright galactic nucleus + dusty halo
  const coreR = scale * (isAuth.value ? 0.28 : 0.18)
  const core = ctx.createRadialGradient(cx, cy, 0, cx, cy, coreR)
  core.addColorStop(0, 'rgba(255, 252, 245, 0.72)')
  core.addColorStop(0.08, 'rgba(255, 236, 200, 0.42)')
  core.addColorStop(0.22, 'rgba(170, 205, 255, 0.22)')
  core.addColorStop(0.45, 'rgba(80, 130, 220, 0.1)')
  core.addColorStop(1, 'rgba(20, 40, 90, 0)')
  ctx.fillStyle = core
  ctx.beginPath()
  ctx.arc(cx, cy, coreR, 0, Math.PI * 2)
  ctx.fill()

  // soft continuous arm dust (not just dots)
  for (let arm = 0; arm < 4; arm++) {
    const baseAng = (arm * Math.PI) / 2 + rot
    const warmArm = arm % 2 === 1
    for (let k = 0; k < 14; k++) {
      const dist = (k + 0.6) * scale * 0.042
      const ang = baseAng + dist * 0.016
      const x = cx + Math.cos(ang) * dist
      const y = cy + Math.sin(ang) * dist * 0.58
      const radius = 28 + dist * 0.42
      const wash = ctx.createRadialGradient(x, y, 0, x, y, radius)
      const a0 = (isAuth.value ? 0.16 : 0.09) * (1 - k / 16)
      wash.addColorStop(
        0,
        warmArm
          ? `rgba(255, 220, 175, ${a0})`
          : `rgba(170, 210, 255, ${a0})`
      )
      wash.addColorStop(0.45, warmArm ? `rgba(255, 190, 140, ${a0 * 0.35})` : `rgba(110, 160, 240, ${a0 * 0.35})`)
      wash.addColorStop(1, 'rgba(40,70,140,0)')
      ctx.fillStyle = wash
      ctx.beginPath()
      ctx.arc(x, y, radius, 0, Math.PI * 2)
      ctx.fill()
    }
  }

  for (const s of spiralStars) {
    const ang = s.a + rot + s.jitterA
    const dist = s.dist + s.jitterR
    const x = cx + Math.cos(ang) * dist
    const y = cy + Math.sin(ang) * dist * 0.58
    if (x < -30 || y < -30 || x > width + 30 || y > height + 30) continue

    const pulse = 0.5 + 0.5 * Math.sin(time * s.tw + s.ph)
    const alpha = Math.min(1, s.base * pulse * (isAuth.value ? 1.05 : 0.8))
    const tone =
      s.warm > 0.72
        ? `rgba(255, ${210 + Math.floor(s.warm * 35)}, ${170 + Math.floor(s.warm * 40)}, ${alpha})`
        : s.warm > 0.4
          ? `rgba(${190 + Math.floor(s.warm * 40)}, ${220 + Math.floor(s.warm * 20)}, 255, ${alpha})`
          : `rgba(210, 230, 255, ${alpha})`

    ctx.beginPath()
    ctx.fillStyle = tone
    ctx.arc(x, y, s.r, 0, Math.PI * 2)
    ctx.fill()

    // soft bloom for brighter stars only — keep sparse
    if (s.r > 1.25 && alpha > 0.45 && isAuth.value) {
      ctx.beginPath()
      ctx.fillStyle = `rgba(255, 245, 230, ${alpha * 0.18})`
      ctx.arc(x, y, s.r * 2.6, 0, Math.PI * 2)
      ctx.fill()
    }
  }
}

function drawFrame(time: number) {
  const canvas = canvasRef.value
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  ctx.clearRect(0, 0, width, height)
  ctx.globalCompositeOperation = 'source-over'

  drawMilkyBand(ctx, time)
  drawSpiral(ctx, time)

  ctx.globalCompositeOperation = 'screen'
  for (const d of dust) {
    const ox = Math.cos(time * d.drift + d.ph) * 10
    const oy = Math.sin(time * d.drift * 0.8 + d.ph) * 7
    const g = ctx.createRadialGradient(d.x + ox, d.y + oy, 0, d.x + ox, d.y + oy, d.r)
    g.addColorStop(0, `rgba(150, 200, 255, ${d.a})`)
    g.addColorStop(0.55, `rgba(80, 130, 220, ${d.a * 0.4})`)
    g.addColorStop(1, 'rgba(20, 40, 90, 0)')
    ctx.fillStyle = g
    ctx.beginPath()
    ctx.arc(d.x + ox, d.y + oy, d.r, 0, Math.PI * 2)
    ctx.fill()
  }

  for (const s of stars) {
    const pulse = 0.55 + 0.45 * Math.sin(time * s.tw + s.ph)
    const alpha = Math.min(1, s.base * pulse)
    const tone =
      s.tone > 0.82
        ? `rgba(255, 236, 210, ${alpha})`
        : s.tone > 0.55
          ? `rgba(190, 220, 255, ${alpha})`
          : `rgba(235, 245, 255, ${alpha})`
    ctx.beginPath()
    ctx.fillStyle = tone
    ctx.arc(s.x, s.y, s.r, 0, Math.PI * 2)
    ctx.fill()
  }

  for (const f of flares) {
    const pulse = 0.45 + 0.55 * Math.sin(time * f.tw + f.ph)
    const a = f.a * pulse
    const g = ctx.createRadialGradient(f.x, f.y, 0, f.x, f.y, f.r * 8)
    g.addColorStop(0, `rgba(255, 250, 240, ${a})`)
    g.addColorStop(0.2, `rgba(190, 220, 255, ${a * 0.4})`)
    g.addColorStop(1, 'rgba(80, 140, 255, 0)')
    ctx.fillStyle = g
    ctx.beginPath()
    ctx.arc(f.x, f.y, f.r * 8, 0, Math.PI * 2)
    ctx.fill()

    ctx.strokeStyle = `rgba(230, 245, 255, ${a * 0.55})`
    ctx.lineWidth = 0.7
    ctx.beginPath()
    ctx.moveTo(f.x - f.r * 5, f.y)
    ctx.lineTo(f.x + f.r * 5, f.y)
    ctx.moveTo(f.x, f.y - f.r * 5)
    ctx.lineTo(f.x, f.y + f.r * 5)
    ctx.stroke()
  }

  for (const tr of trails) {
    const x = (tr.x + time * tr.speed * 28) % (width + 160) - 80
    const y = (tr.y + time * tr.speed * 10) % (height + 120) - 60
    const x2 = x + Math.cos(tr.ang) * tr.len
    const y2 = y + Math.sin(tr.ang) * tr.len
    const g = ctx.createLinearGradient(x, y, x2, y2)
    g.addColorStop(0, `rgba(180, 230, 255, ${tr.a})`)
    g.addColorStop(1, 'rgba(180, 230, 255, 0)')
    ctx.strokeStyle = g
    ctx.lineWidth = tr.w
    ctx.beginPath()
    ctx.moveTo(x, y)
    ctx.lineTo(x2, y2)
    ctx.stroke()
  }

  // 神经网络星座：细线连节点，整体漂移，节点呼吸式明灭。
  // 透明度刻意压低——它是氛围，不是主角，不能和登录卡片抢视线。
  for (const c of constellations) {
    const ox = Math.cos(time * c.driftX * 6 + c.ph) * 14
    const oy = Math.sin(time * c.driftY * 6 + c.ph) * 10
    const px = c.nodes.map((n) => n.x * width + ox)
    const py = c.nodes.map((n) => n.y * height + oy)

    for (const e of c.edges) {
      const pulse = 0.5 + 0.5 * Math.sin(time * 0.35 + c.ph + (e.a + e.b) * 0.7)
      const a = 0.09 + 0.18 * pulse
      const g = ctx.createLinearGradient(px[e.a], py[e.a], px[e.b], py[e.b])
      g.addColorStop(0, `rgba(125, 211, 252, ${a})`)
      g.addColorStop(1, `rgba(45, 212, 191, ${a * 0.8})`)
      ctx.strokeStyle = g
      ctx.lineWidth = 0.6
      ctx.beginPath()
      ctx.moveTo(px[e.a], py[e.a])
      ctx.lineTo(px[e.b], py[e.b])
      ctx.stroke()
    }

    for (let i = 0; i < c.nodes.length; i++) {
      const n = c.nodes[i]
      const pulse = 0.55 + 0.45 * Math.sin(time * n.tw + n.ph)
      const a = 0.38 + 0.55 * pulse
      const g = ctx.createRadialGradient(px[i], py[i], 0, px[i], py[i], n.r * 4)
      g.addColorStop(0, `rgba(190, 242, 255, ${a})`)
      g.addColorStop(0.35, `rgba(103, 232, 249, ${a * 0.35})`)
      g.addColorStop(1, 'rgba(34, 211, 238, 0)')
      ctx.fillStyle = g
      ctx.beginPath()
      ctx.arc(px[i], py[i], n.r * 4, 0, Math.PI * 2)
      ctx.fill()

      ctx.fillStyle = `rgba(240, 253, 255, ${Math.min(1, a + 0.25)})`
      ctx.beginPath()
      ctx.arc(px[i], py[i], n.r * 0.55, 0, Math.PI * 2)
      ctx.fill()
    }
  }

  ctx.globalCompositeOperation = 'source-over'
}

function loop(ts: number) {
  if (!t0) t0 = ts
  drawFrame((ts - t0) / 1000)
  rafId = window.requestAnimationFrame(loop)
}

function start() {
  resize()
  cancelAnimationFrame(rafId)
  t0 = 0
  rafId = window.requestAnimationFrame(loop)
}

function stop() {
  cancelAnimationFrame(rafId)
  rafId = 0
}

onMounted(() => {
  start()
  if (rootRef.value) {
    resizeObserver = new ResizeObserver(() => resize())
    resizeObserver.observe(rootRef.value)
  }
  window.addEventListener('resize', resize)
})

onUnmounted(() => {
  stop()
  resizeObserver?.disconnect()
  window.removeEventListener('resize', resize)
})

watch(
  () => props.variant,
  () => {
    seedField()
  }
)
</script>

<template>
  <div
    ref="rootRef"
    class="galaxy"
    :class="isAuth ? 'galaxy--auth' : 'galaxy--home'"
    aria-hidden="true"
  >
    <div class="galaxy__void" />
    <div class="galaxy__milky" />
    <div class="galaxy__spiral" />
    <div class="galaxy__nebula galaxy__nebula--a" />
    <div class="galaxy__nebula galaxy__nebula--b" />
    <div class="galaxy__nebula galaxy__nebula--c" />
    <div class="galaxy__nebula galaxy__nebula--d" />
    <div class="galaxy__glow galaxy__glow--core" />
    <div class="galaxy__glow galaxy__glow--teal" />
    <div class="galaxy__glow galaxy__glow--violet" />
    <div class="galaxy__glow galaxy__glow--warm" />
    <div class="galaxy__dust" />
    <div v-if="isAuth" class="galaxy__grid" />
    <div class="galaxy__haze" />
    <canvas ref="canvasRef" class="galaxy__canvas" />
    <div
      v-for="(star, index) in shootingStars"
      :key="index"
      class="galaxy__shoot"
      :style="{
        top: star.top,
        animationDelay: star.delay,
        animationDuration: star.duration
      }"
    />
    <div class="galaxy__vignette" />
  </div>
</template>

<style scoped>
.galaxy {
  position: absolute;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
  z-index: 0;
  isolation: isolate;
  background: #020617;
}

.galaxy__void {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(ellipse 95% 75% at 50% 42%, rgba(18, 36, 78, 0.55), transparent 62%),
    radial-gradient(ellipse 70% 55% at 20% 80%, rgba(8, 20, 48, 0.7), transparent 55%),
    linear-gradient(180deg, #01030c 0%, #06102a 42%, #030816 100%);
}

.galaxy__milky {
  position: absolute;
  left: -20%;
  top: 18%;
  width: 140%;
  height: 58%;
  background: linear-gradient(
    118deg,
    transparent 8%,
    rgba(70, 120, 210, 0.14) 28%,
    rgba(190, 220, 255, 0.28) 44%,
    rgba(255, 246, 230, 0.34) 50%,
    rgba(150, 195, 255, 0.24) 58%,
    rgba(60, 100, 190, 0.12) 72%,
    transparent 90%
  );
  filter: blur(28px) saturate(1.25);
  transform: rotate(-28deg) scaleY(0.72);
  opacity: 0.9;
  animation: milky-drift 48s ease-in-out infinite alternate;
  mix-blend-mode: screen;
}

.galaxy__spiral {
  position: absolute;
  left: 50%;
  top: 44%;
  width: min(140vw, 1280px);
  height: min(140vw, 1280px);
  transform: translate(-50%, -50%);
  background:
    conic-gradient(
      from 20deg,
      transparent 0deg,
      rgba(140, 190, 255, 0.12) 16deg,
      rgba(255, 236, 210, 0.3) 36deg,
      transparent 68deg,
      transparent 90deg,
      rgba(120, 180, 255, 0.14) 108deg,
      rgba(210, 230, 255, 0.26) 128deg,
      transparent 158deg,
      transparent 180deg,
      rgba(255, 220, 180, 0.22) 202deg,
      transparent 232deg,
      transparent 270deg,
      rgba(150, 200, 255, 0.16) 292deg,
      rgba(255, 240, 220, 0.24) 318deg,
      transparent 348deg,
      transparent 360deg
    ),
    radial-gradient(
      circle at center,
      rgba(255, 248, 235, 0.55) 0%,
      rgba(255, 230, 190, 0.28) 10%,
      rgba(170, 210, 255, 0.18) 22%,
      rgba(60, 110, 200, 0.08) 40%,
      transparent 62%
    );
  filter: blur(26px) contrast(1.1) saturate(1.25);
  opacity: 0.92;
  mix-blend-mode: screen;
  animation: spiral-spin 150s linear infinite;
}

.galaxy__nebula {
  position: absolute;
  border-radius: 999px;
  filter: blur(50px) saturate(1.35);
  mix-blend-mode: screen;
  animation: nebula-breathe 16s ease-in-out infinite;
}

.galaxy__nebula--a {
  width: 58vw;
  height: 42vw;
  left: -12%;
  top: -8%;
  background: radial-gradient(circle, rgba(56, 189, 248, 0.42), rgba(14, 116, 220, 0.12) 48%, transparent 72%);
  animation-duration: 18s;
}

.galaxy__nebula--b {
  width: 52vw;
  height: 40vw;
  right: -14%;
  top: 10%;
  background: radial-gradient(circle, rgba(129, 140, 248, 0.34), rgba(67, 56, 202, 0.1) 50%, transparent 74%);
  animation-duration: 22s;
  animation-delay: -4s;
}

.galaxy__nebula--c {
  width: 64vw;
  height: 38vw;
  left: 12%;
  bottom: -16%;
  background: radial-gradient(circle, rgba(45, 212, 191, 0.28), rgba(13, 148, 136, 0.1) 46%, transparent 72%);
  animation-duration: 20s;
  animation-delay: -8s;
}

.galaxy__nebula--d {
  width: 40vw;
  height: 30vw;
  left: 38%;
  top: 28%;
  background: radial-gradient(circle, rgba(255, 220, 180, 0.2), rgba(251, 146, 60, 0.08) 42%, transparent 70%);
  filter: blur(60px) saturate(1.2);
  animation-duration: 24s;
  animation-delay: -2s;
}

.galaxy__glow {
  position: absolute;
  border-radius: 999px;
  filter: blur(40px);
  mix-blend-mode: screen;
  animation: glow-drift 22s ease-in-out infinite alternate;
}

.galaxy__glow--core {
  width: 36vw;
  height: 36vw;
  left: 42%;
  top: 30%;
  background: radial-gradient(circle, rgba(255, 248, 235, 0.28), rgba(147, 197, 253, 0.1) 45%, transparent 70%);
  animation-duration: 26s;
}

.galaxy__glow--teal {
  width: 42vw;
  height: 30vw;
  left: 8%;
  top: 18%;
  background: radial-gradient(circle, rgba(34, 211, 238, 0.24), transparent 70%);
}

.galaxy__glow--violet {
  width: 38vw;
  height: 32vw;
  right: 4%;
  top: 26%;
  background: radial-gradient(circle, rgba(165, 180, 252, 0.22), transparent 70%);
  animation-delay: -6s;
}

.galaxy__glow--warm {
  width: 34vw;
  height: 26vw;
  left: 34%;
  bottom: 8%;
  background: radial-gradient(circle, rgba(251, 191, 36, 0.12), transparent 70%);
  animation-delay: -10s;
}

.galaxy__dust {
  position: absolute;
  inset: 0;
  opacity: 0.5;
  background-image:
    radial-gradient(1.5px 1.5px at 8% 18%, rgba(255, 255, 255, 0.9), transparent),
    radial-gradient(1px 1px at 18% 62%, rgba(186, 230, 253, 0.75), transparent),
    radial-gradient(1.5px 1.5px at 28% 28%, rgba(255, 255, 255, 0.7), transparent),
    radial-gradient(1px 1px at 40% 78%, rgba(224, 231, 255, 0.7), transparent),
    radial-gradient(2px 2px at 52% 22%, rgba(255, 255, 255, 0.85), transparent),
    radial-gradient(1px 1px at 66% 48%, rgba(165, 243, 252, 0.7), transparent),
    radial-gradient(1.5px 1.5px at 74% 16%, rgba(255, 255, 255, 0.8), transparent),
    radial-gradient(1px 1px at 84% 68%, rgba(199, 210, 254, 0.75), transparent),
    radial-gradient(1px 1px at 92% 34%, rgba(255, 255, 255, 0.65), transparent);
  background-size: 100% 100%;
  animation: dust-twinkle 7s ease-in-out infinite;
}

.galaxy__haze {
  position: absolute;
  inset: -10%;
  background:
    radial-gradient(ellipse 80% 50% at 50% 100%, rgba(15, 23, 42, 0.45), transparent 60%),
    radial-gradient(ellipse 60% 40% at 10% 30%, rgba(8, 47, 73, 0.25), transparent 50%);
  animation: haze-shift 36s ease-in-out infinite alternate;
}

/* 透视网格：只在 auth 变体渲染（模板里 v-if）。从地平线向上淡出，
 * 给"科技感"一个地平线参照，也让画面下半部不至于只有渐变。 */
.galaxy__grid {
  position: absolute;
  left: -25%;
  right: -25%;
  bottom: -6%;
  height: 46%;
  background-image:
    linear-gradient(rgba(34, 211, 238, 0.17) 1px, transparent 1px),
    linear-gradient(90deg, rgba(34, 211, 238, 0.17) 1px, transparent 1px);
  background-size: 44px 44px;
  transform: perspective(520px) rotateX(62deg);
  transform-origin: center bottom;
  animation: grid-flow 14s linear infinite;
  -webkit-mask-image: linear-gradient(to top, rgba(0, 0, 0, 0.9) 0%, transparent 78%);
  mask-image: linear-gradient(to top, rgba(0, 0, 0, 0.9) 0%, transparent 78%);
}

.galaxy__canvas {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  display: block;
  mix-blend-mode: screen;
  opacity: 1;
}

.galaxy__shoot {
  position: absolute;
  left: -12%;
  width: 140px;
  height: 1.5px;
  border-radius: 999px;
  background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.95), rgba(125, 211, 252, 0.35), transparent);
  filter: drop-shadow(0 0 6px rgba(125, 211, 252, 0.7));
  opacity: 0;
  animation-name: shoot-across;
  animation-timing-function: cubic-bezier(0.2, 0.7, 0.2, 1);
  animation-iteration-count: infinite;
}

.galaxy__vignette {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(ellipse 75% 65% at 50% 45%, transparent 30%, rgba(2, 6, 23, 0.28) 72%, rgba(2, 6, 23, 0.72) 100%),
    linear-gradient(180deg, rgba(2, 6, 23, 0.25), transparent 22%, transparent 78%, rgba(2, 6, 23, 0.4));
}

.galaxy--auth .galaxy__milky {
  opacity: 1;
  filter: blur(22px) saturate(1.35);
}

.galaxy--auth .galaxy__spiral {
  opacity: 1;
  filter: blur(22px) contrast(1.15) saturate(1.35);
}

.galaxy--auth .galaxy__nebula {
  filter: blur(42px) saturate(1.45);
}

.galaxy--auth .galaxy__dust {
  opacity: 0.62;
}

.galaxy--auth .galaxy__vignette {
  background:
    radial-gradient(ellipse 80% 70% at 50% 45%, transparent 38%, rgba(2, 6, 23, 0.18) 75%, rgba(2, 6, 23, 0.55) 100%),
    linear-gradient(180deg, rgba(2, 6, 23, 0.15), transparent 20%, transparent 80%, rgba(2, 6, 23, 0.28));
}

.galaxy--home .galaxy__spiral {
  opacity: 0.7;
}

.galaxy--home .galaxy__milky {
  opacity: 0.75;
}


@keyframes milky-drift {
  0% {
    transform: rotate(-28deg) scaleY(0.72) translate3d(0, 0, 0);
  }
  100% {
    transform: rotate(-26deg) scaleY(0.76) translate3d(2%, -1.5%, 0);
  }
}

@keyframes spiral-spin {
  from {
    transform: translate(-50%, -50%) rotate(0deg);
  }
  to {
    transform: translate(-50%, -50%) rotate(360deg);
  }
}

@keyframes nebula-breathe {
  0%,
  100% {
    transform: scale(1) translate3d(0, 0, 0);
    filter: blur(50px) saturate(1.3);
  }
  50% {
    transform: scale(1.08) translate3d(1.5%, -1%, 0);
    filter: blur(56px) saturate(1.45);
  }
}

@keyframes glow-drift {
  0% {
    transform: translate3d(0, 0, 0) scale(1);
  }
  100% {
    transform: translate3d(3%, -2%, 0) scale(1.08);
  }
}

@keyframes dust-twinkle {
  0%,
  100% {
    opacity: 0.42;
  }
  50% {
    opacity: 0.7;
  }
}

@keyframes haze-shift {
  0% {
    transform: translate3d(0, 0, 0);
  }
  100% {
    transform: translate3d(-1.5%, 1%, 0);
  }
}

@keyframes grid-flow {
  from {
    background-position: 0 0, 0 0;
  }
  to {
    background-position: 0 44px, 0 0;
  }
}

@keyframes shoot-across {
  0% {
    opacity: 0;
    transform: translate3d(0, 0, 0) rotate(18deg);
  }
  6% {
    opacity: 1;
  }
  24% {
    opacity: 0;
    transform: translate3d(118vw, 34vh, 0) rotate(18deg);
  }
  100% {
    opacity: 0;
    transform: translate3d(118vw, 34vh, 0) rotate(18deg);
  }
}

@media (max-width: 768px) {
  .galaxy__milky {
    height: 48%;
    filter: blur(22px);
  }

  .galaxy__spiral {
    width: 160vw;
    height: 160vw;
  }

  .galaxy__nebula--a,
  .galaxy__nebula--b,
  .galaxy__nebula--c {
    filter: blur(40px);
  }
}

@media (prefers-reduced-motion: reduce) {
  .galaxy__milky,
  .galaxy__spiral,
  .galaxy__nebula,
  .galaxy__glow,
  .galaxy__dust,
  .galaxy__haze,
  .galaxy__shoot,
  .galaxy__grid {
    animation: none !important;
  }
}
</style>

<!-- 浅色主题下仍保持深空底色——星系是品牌资产，不跟随 UI 主题反色。
     必须放在**无作用域**块里：scoped 块中 `:global(X) Y` 形态的规则会被
     Vue 的 scoped-CSS 编译整条丢弃（见 src/__tests__/scoped-global-selector.spec.ts）。 -->
<style>
html:not(.dark) .galaxy__void {
  background:
    radial-gradient(ellipse 95% 75% at 50% 42%, rgba(18, 36, 78, 0.5), transparent 62%),
    linear-gradient(180deg, #01030c 0%, #06102a 42%, #030816 100%);
}

html:not(.dark) .galaxy__vignette {
  background:
    radial-gradient(ellipse 80% 70% at 50% 45%, transparent 36%, rgba(2, 6, 23, 0.2) 78%, rgba(2, 6, 23, 0.58) 100%),
    linear-gradient(180deg, rgba(2, 6, 23, 0.12), transparent 22%, transparent 80%, rgba(2, 6, 23, 0.3));
}
</style>
