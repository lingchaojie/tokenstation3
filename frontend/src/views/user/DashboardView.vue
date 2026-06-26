<template>
  <AppLayout>
    <UserDashboardContent :show-standard-costs="false" />
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import UserDashboardContent from '@/components/user/dashboard/UserDashboardContent.vue'
</script>
