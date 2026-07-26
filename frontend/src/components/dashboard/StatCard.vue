<template>
  <div class="stat-tile">
    <div class="stat-tile__head">
      <Icon :name="icon" size="sm" :stroke-width="2" class="stat-tile__icon" />
      <span class="stat-tile__label">{{ label }}</span>
    </div>

    <p class="stat-tile__value" :title="String(value)">
      <span v-if="prefix" class="stat-tile__prefix">{{ prefix }}</span>{{ value }}<span
        v-if="unit"
        class="stat-tile__unit"
        >{{ unit }}</span
      >
    </p>

    <!-- 脚注留给「这个数字的上下文」：启用数、错误数、占比。
         没有内容时不占位，避免一排卡片高度参差。 -->
    <p v-if="$slots.footnote" class="stat-tile__foot">
      <slot name="footnote" />
    </p>
  </div>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'

// 从 Icon 组件反推图标名的联合类型，而不是放宽成 string：
// 写错图标名应该在编译期就报错。Icon.vue 没有导出这个类型，
// 所以从它的 props 上取，避免为此改动那个被全站引用的文件。
type IconName = InstanceType<typeof Icon>['$props']['name']

defineProps<{
  /** Icon 组件的图标名 */
  icon: IconName
  label: string
  value: string | number
  /** 数值前缀，如货币符号 */
  prefix?: string
  /** 紧跟数值的单位，如 ms / RPM */
  unit?: string
}>()
</script>

<style scoped>
/* 这张卡刻意不给图标上色。
 *
 * 原来 8 张卡各自一个色（蓝/紫/绿/青/橙…），色相不携带任何信息，纯装饰——
 * 这正是通用后台模板最典型的特征。更糟的是，它把页面里真正有意义的颜色
 * （启用数的绿、错误数的红）淹没在一片彩色噪音里。
 * 这里图标一律中性，颜色全部让位给脚注中的状态。 */
.stat-tile {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  border-radius: 0.75rem;
  border: 1px solid var(--surface-card-border);
  background: var(--surface-card);
  padding: 0.95rem 1.05rem;
  transition: border-color 0.2s ease;
}


.stat-tile:hover {
  border-color: var(--surface-card-border-hover);
}


.stat-tile__head {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  color: var(--text-muted);
}


.stat-tile__icon {
  flex-shrink: 0;
  opacity: 0.75;
}

.stat-tile__label {
  font-size: 0.75rem;
  font-weight: 500;
  letter-spacing: 0.01em;
  line-height: 1.2;
}

/* 数字是主角：给它足够的字号和紧凑的字距，
 * 并用 tabular-nums 让相邻卡片的数字竖向对齐、刷新时不跳动。 */
.stat-tile__value {
  font-size: 1.6rem;
  font-weight: 650;
  line-height: 1.15;
  letter-spacing: -0.02em;
  color: var(--text-strong);
  font-variant-numeric: tabular-nums;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}


.stat-tile__prefix {
  margin-right: 0.05em;
  font-size: 0.95rem;
  font-weight: 550;
  color: var(--text-muted);
}


.stat-tile__unit {
  /* 数值本身有 -0.02em 的负字距，会把紧随其后的单位吸过去（"0RPM"）。
   * 这里显式给一个正的间距把它推开。 */
  margin-left: 0.3em;
  font-size: 0.78rem;
  font-weight: 500;
  letter-spacing: 0.02em;
  color: var(--text-muted);
}


.stat-tile__foot {
  font-size: 0.72rem;
  line-height: 1.3;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

</style>
