<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-6">
      <!-- Hero -->
      <div class="card p-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h1 class="text-xl font-semibold text-gray-900 dark:text-white">
              {{ t('envCheck.title') }}
            </h1>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('envCheck.description') }}
            </p>
          </div>
          <div class="flex items-center gap-3">
            <span v-if="lastScannedAt" class="text-xs text-gray-400 dark:text-gray-500">
              {{ t('envCheck.lastScanned', { time: formatDateTime(lastScannedAt.toISOString()) }) }}
            </span>
            <button @click="scan" :disabled="running" class="btn btn-primary">
              <Icon name="refresh" size="md" :class="['mr-2', running ? 'animate-spin' : '']" />
              {{ running ? t('envCheck.scanning') : t('envCheck.rescan') }}
            </button>
          </div>
        </div>
      </div>

      <!-- Claude 出口 IP -->
      <section class="card p-6">
        <SectionHeading icon="globe" :title="t('envCheck.exit.title')" :state="traceState" />
        <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
          {{ t('envCheck.exit.hint') }}
        </p>

        <div v-if="traceState === 'failed'" class="text-sm text-amber-600 dark:text-amber-400">
          {{ t('envCheck.exit.failed') }}
        </div>
        <div v-else class="grid gap-3 sm:grid-cols-2">
          <div
            v-for="trace in traces"
            :key="trace.host"
            class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
          >
            <div class="text-xs uppercase tracking-wide text-gray-400">{{ trace.host }}</div>
            <div class="mt-1 font-mono text-lg text-gray-900 dark:text-white">{{ trace.ip }}</div>
            <div class="mt-2 flex flex-wrap items-center gap-2">
              <span :class="trace.loc === 'CN' ? 'badge badge-danger' : 'badge badge-success'">
                {{ trace.loc || '-' }}
              </span>
              <span class="badge badge-secondary">{{
                t('envCheck.exit.colo', { colo: trace.colo || '-' })
              }}</span>
              <span v-if="trace.warp" class="badge badge-warning">
                {{ t('envCheck.exit.warpOn') }}
              </span>
            </div>
          </div>
        </div>
      </section>

      <!-- WebRTC -->
      <section class="card p-6">
        <SectionHeading icon="shield" :title="t('envCheck.webrtc.title')" :state="webRtcState" />
        <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
          {{ t('envCheck.webrtc.hint') }}
        </p>

        <div v-if="webRtcState === 'failed'" class="text-sm text-amber-600 dark:text-amber-400">
          {{ t('envCheck.webrtc.failed') }}
        </div>
        <template v-else>
          <div v-if="!webRtcLeaked" class="flex items-center gap-2 text-sm">
            <Icon name="checkCircle" size="sm" class="text-primary-500" />
            <span class="text-gray-700 dark:text-gray-300">
              {{
                (webRtc?.mdnsHosts.length ?? 0) > 0
                  ? t('envCheck.webrtc.safeMdns')
                  : t('envCheck.webrtc.safe')
              }}
            </span>
          </div>
          <div v-else class="space-y-2">
            <div class="flex items-center gap-2 text-sm">
              <Icon name="exclamationCircle" size="sm" class="text-red-500" />
              <span class="font-medium text-red-600 dark:text-red-400">
                {{ t('envCheck.webrtc.leaked') }}
              </span>
            </div>
            <div
              v-for="address in leakedAddresses"
              :key="address.ip"
              class="flex items-center justify-between gap-3 rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-700"
            >
              <span class="font-mono text-sm text-gray-700 dark:text-gray-300">
                {{ address.ip }}
              </span>
              <span class="badge badge-danger">{{ address.family }}</span>
            </div>
          </div>
        </template>
      </section>

      <!-- DNS 泄露 -->
      <section class="card p-6">
        <SectionHeading icon="server" :title="t('envCheck.dns.title')" :state="dnsState" />
        <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
          {{ t('envCheck.dns.hint') }}
        </p>

        <div v-if="dnsState === 'failed'" class="text-sm text-amber-600 dark:text-amber-400">
          {{ t('envCheck.dns.failed') }}
        </div>
        <template v-else-if="resolvers.length > 0">
          <div class="mb-3 flex items-center gap-2 text-sm">
            <Icon
              :name="leakedResolvers.length > 0 ? 'exclamationCircle' : 'checkCircle'"
              size="sm"
              :class="leakedResolvers.length > 0 ? 'text-red-500' : 'text-primary-500'"
            />
            <span
              :class="
                leakedResolvers.length > 0
                  ? 'font-medium text-red-600 dark:text-red-400'
                  : 'text-gray-700 dark:text-gray-300'
              "
            >
              {{
                leakedResolvers.length > 0
                  ? t('envCheck.dns.leaked', { count: leakedResolvers.length })
                  : t('envCheck.dns.safe', { count: resolvers.length })
              }}
            </span>
          </div>
          <ul class="max-h-72 space-y-1.5 overflow-y-auto">
            <li
              v-for="resolver in sortedResolvers"
              :key="resolver.ip"
              class="flex items-center justify-between gap-3 rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-700"
            >
              <span class="truncate font-mono text-xs text-gray-700 dark:text-gray-300">
                {{ resolver.ip }}
              </span>
              <span class="flex flex-shrink-0 items-center gap-2">
                <span class="text-xs text-gray-400">{{ resolver.isp }}</span>
                <span :class="resolver.isChina ? 'badge badge-danger' : 'badge badge-success'">
                  {{ resolver.isChina ? t('envCheck.dns.cnResolver') : resolver.countryCode || '-' }}
                </span>
              </span>
            </li>
          </ul>
        </template>
      </section>

      <!-- 中文环境评分 -->
      <section class="card p-6">
        <SectionHeading icon="infoCircle" :title="t('envCheck.risk.title')" state="done" />

        <div v-if="risk" class="flex flex-col items-center gap-2 py-4">
          <div :class="['text-5xl font-bold', riskColorClass]">{{ risk.score }}</div>
          <div class="text-xs text-gray-400">{{ t('envCheck.risk.outOf') }}</div>
          <span :class="riskBadgeClass">{{ t(`envCheck.risk.level.${risk.level}`) }}</span>
          <p class="mt-1 text-center text-sm text-gray-600 dark:text-gray-300">
            {{ t(`envCheck.risk.verdict.${risk.level}`) }}
          </p>
        </div>

        <div class="mt-4 space-y-2">
          <div
            v-for="signal in risk?.signals ?? []"
            :key="signal.id"
            class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
          >
            <div class="flex items-center justify-between gap-3">
              <span class="text-sm font-medium text-gray-800 dark:text-gray-200">
                {{ t(`envCheck.risk.signal.${signal.id}`) }}
              </span>
              <span class="flex flex-shrink-0 items-center gap-2">
                <span class="text-xs text-gray-400">
                  {{ t('envCheck.risk.weight', { weight: signal.weight }) }}
                </span>
                <span :class="signal.hit ? 'badge badge-danger' : 'badge badge-success'">
                  +{{ signal.score }}
                </span>
              </span>
            </div>
            <p class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-gray-400">
              {{ signal.detail || '-' }}
            </p>
            <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {{ t(`envCheck.risk.signalHint.${signal.id}`) }}
            </p>
          </div>
        </div>
      </section>

      <!-- 建议 -->
      <section class="card p-6">
        <h2 class="mb-3 text-base font-semibold text-gray-900 dark:text-white">
          {{ t('envCheck.advice.title') }}
        </h2>
        <ul class="space-y-2 text-sm text-gray-600 dark:text-gray-300">
          <li v-for="key in adviceKeys" :key="key" class="flex gap-2">
            <span class="text-primary-500">•</span>
            <span>{{ t(`envCheck.advice.${key}`) }}</span>
          </li>
        </ul>
        <p class="mt-4 text-xs text-gray-400 dark:text-gray-500">
          {{ t('envCheck.advice.disclaimer') }}
        </p>
        <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
          {{ t('envCheck.advice.credit') }}
        </p>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, h } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import { useClaudeEnvCheck, type ProbeState } from '@/composables/useClaudeEnvCheck'

const { t } = useI18n()

const {
  traceState,
  webRtcState,
  dnsState,
  traces,
  webRtc,
  webRtcLeaked,
  leakedAddresses,
  resolvers,
  leakedResolvers,
  risk,
  running,
  lastScannedAt,
  scan,
} = useClaudeEnvCheck()

// 泄露的解析器排在前面，方便一眼看到问题
const sortedResolvers = computed(() =>
  [...resolvers.value].sort((a, b) => Number(b.isChina) - Number(a.isChina)),
)

const riskColorClass = computed(() => {
  switch (risk.value?.level) {
    case 'low':
      return 'text-primary-500'
    case 'medium':
      return 'text-amber-500'
    default:
      return 'text-red-500'
  }
})

const riskBadgeClass = computed(() => {
  switch (risk.value?.level) {
    case 'low':
      return 'badge badge-success'
    case 'medium':
      return 'badge badge-warning'
    default:
      return 'badge badge-danger'
  }
})

const adviceKeys = ['timezone', 'tunnel', 'dns', 'webrtc'] as const

type IconName = InstanceType<typeof Icon>['$props']['name']

const SectionHeading = (props: { icon: IconName; title: string; state: ProbeState }) =>
  h('div', { class: 'mb-1 flex items-center gap-2' }, [
    h(Icon, { name: props.icon, size: 'md', class: 'text-gray-400' }),
    h('h2', { class: 'text-base font-semibold text-gray-900 dark:text-white' }, props.title),
    props.state === 'running'
      ? h(Icon, { name: 'refresh', size: 'sm', class: 'animate-spin text-gray-400' })
      : null,
  ])

onMounted(scan)
</script>
