<script setup lang="ts">
import { ref } from 'vue'
import { NTabPane, NTabs } from 'naive-ui'
import AgentConsole from '../components/AgentConsole.vue'
import ChainStrip from '../components/ChainStrip.vue'
import HostManager from '../components/HostManager.vue'

const tab = ref<'hosts' | 'agents'>('hosts')
</script>

<template>
  <div class="ob">
    <div class="ob-strip">
      <ChainStrip current="outbound" />
    </div>
    <NTabs v-model:value="tab" type="line" size="small" class="ob-tabs">
      <NTabPane name="hosts" tab="主机节点">
        <HostManager
          role="outbound"
          title="出口节点"
          subtitle="配置引擎 · 任意 VPS / 电脑 / 软路由作为出站，角色可随时切换"
        />
      </NTabPane>
      <NTabPane name="agents" tab="远程代理节点">
        <AgentConsole />
      </NTabPane>
    </NTabs>
  </div>
</template>

<style scoped>
.ob {
  padding-bottom: 24px;
}
.ob-strip {
  padding: 16px 24px 0;
}
.ob-tabs :deep(.n-tabs-nav) {
  padding: 0 24px;
}
.ob-tabs :deep(.n-tab-pane) {
  padding: 0;
}
</style>
