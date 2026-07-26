<template>
  <span class="cost-triplet">
    <span class="cost-triplet__actual" :title="t('admin.dashboard.actual')">${{ actual }}</span>
    <span class="cost-triplet__sep">/</span>
    <span class="cost-triplet__account" :title="t('admin.dashboard.accountCost')">${{ account }}</span>
    <span class="cost-triplet__sep">/</span>
    <span class="cost-triplet__standard" :title="t('admin.dashboard.standard')">${{ standard }}</span>
  </span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// 只收已格式化好的字符串：金额格式化逻辑归调用方所有，
// 这个组件只负责三个口径的视觉关系。
defineProps<{
  /** 实际成本（我方真实支出） */
  actual: string
  /** 账号成本 */
  account: string
  /** 标准价（按官方价目表折算） */
  standard: string
}>()

const { t } = useI18n()
</script>

<style scoped>
/* 三个口径的成本原本在两张卡里各写一遍，且用了绿/橙/灰三色。
 *
 * 这里保留可区分性但压低饱和度：实际支出是运营最常看的数字，给它最高对比度；
 * 另外两个退成中性。原来的绿色会和「启用数」的绿撞义——同一屏里同一个绿
 * 既表示"健康"又表示"某一种成本口径"，读者得靠位置去猜。 */
.cost-triplet {
  display: inline-flex;
  align-items: baseline;
  gap: 0.25rem;
  font-variant-numeric: tabular-nums;
}

.cost-triplet__actual {
  font-weight: 550;
  color: var(--text-body);
}


.cost-triplet__account,
.cost-triplet__standard {
  color: var(--text-faint);
}


.cost-triplet__sep {
  color: var(--divider-soft);
}

</style>
