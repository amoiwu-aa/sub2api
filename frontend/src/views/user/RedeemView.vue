<template>
  <AppLayout>
    <RedeemPanel v-if="!foldedIntoPurchase" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import RedeemPanel from '@/components/user/RedeemPanel.vue'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

const router = useRouter()
const foldedIntoPurchase = computed(() => isFeatureFlagEnabled(FeatureFlags.payment))

watch(
  foldedIntoPurchase,
  (folded) => {
    if (folded) {
      void router.replace({ path: '/purchase', query: { tab: 'redeem' } })
    }
  },
  { immediate: true },
)
</script>
