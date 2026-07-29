<template>
  <div class="chart-empty">
    <span class="chart-empty__mark" aria-hidden="true">
      <Icon :name="icon" size="lg" :stroke-width="1.5" />
    </span>
    <p class="chart-empty__title">{{ title }}</p>
    <p v-if="hint" class="chart-empty__hint">{{ hint }}</p>
  </div>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'

type IconName = InstanceType<typeof Icon>['$props']['name']

/* 图表没有数据时，原来只在一个巨大的空框正中放一行灰色小字「暂无数据」。
 * 全新实例第一次进后台看到的就是两块荒地，既不好看也没告诉用户该做什么。
 * 这里给一个克制的图形锚点 + 一句可选的下一步提示。 */
withDefaults(
  defineProps<{
    title: string
    hint?: string
    icon?: IconName
  }>(),
  { icon: 'chart' }
)
</script>

<style scoped>
.chart-empty {
  display: flex;
  height: 100%;
  min-height: 8rem;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.15rem;
  padding: 1rem;
  text-align: center;
}

.chart-empty__mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.75rem;
  height: 2.75rem;
  margin-bottom: 0.55rem;
  border-radius: 0.75rem;
  border: 1px dashed var(--surface-muted-border);
  color: var(--text-faint);
}


.chart-empty__title {
  font-size: 0.8125rem;
  font-weight: 500;
  color: var(--text-muted);
}


.chart-empty__hint {
  max-width: 22rem;
  font-size: 0.75rem;
  line-height: 1.5;
  color: var(--text-faint);
}

</style>
