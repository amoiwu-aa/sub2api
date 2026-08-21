<template>
  <div class="app-canvas min-h-screen">
    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="relative min-h-screen transition-all duration-300"
      :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64']"
    >
      <!-- Header -->
      <AppHeader />

      <!-- Main Content -->
      <main class="p-4 md:p-6 lg:p-8">
        <slot />
      </main>
    </div>

    <NewUserOnboardingDialog
      :show="showNewUserOnboarding"
      @start="handleStartUserOnboarding"
      @skip="handleSkipUserOnboarding"
    />
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, nextTick, onMounted, ref } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import {
  recordNewUserOnboardingDecision,
  shouldPromptNewUserOnboarding
} from '@/utils/onboarding'
import NewUserOnboardingDialog from '@/components/Guide/NewUserOnboardingDialog.vue'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')
const showNewUserOnboarding = ref(false)

const { replayTour, markAsSeen } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
  showNewUserOnboarding.value =
    !authStore.isSimpleMode && shouldPromptNewUserOnboarding(authStore.user)
})

async function handleStartUserOnboarding() {
  const userID = authStore.user?.id
  if (!userID) return

  recordNewUserOnboardingDecision(userID, 'started')
  showNewUserOnboarding.value = false
  await nextTick()
  replayTour()
}

function handleSkipUserOnboarding() {
  const userID = authStore.user?.id
  if (!userID) return

  recordNewUserOnboardingDecision(userID, 'skipped')
  markAsSeen()
  showNewUserOnboarding.value = false
}

defineExpose({ replayTour })
</script>
