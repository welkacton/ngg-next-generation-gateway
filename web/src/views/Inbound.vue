<script setup lang="ts">
import ChainStrip from '../components/ChainStrip.vue'
import HostManager from '../components/HostManager.vue'
</script>

<template>
  <div style="padding: 16px 24px 0">
    <ChainStrip current="inbound" />
  </div>
  <HostManager
    role="inbound"
    title="入口节点"
    subtitle="配置引擎 · 任意 VPS / 电脑 / 软路由作为入站，角色可随时切换"
  />
</template>
