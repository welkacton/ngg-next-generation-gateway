<script setup lang="ts">
/**
 * 节点管理 —— 原「入口节点」与「出口节点」合并后的唯一节点页面。
 *
 * 角色不再是两个菜单，而是同一份节点库上的一个字段：入站 / 出站 / 双向，
 * 卡片上可直接切换。合并不是把两个列表拼在一起：这里只有一个列表、一个筛选器，
 * 避免同一台 VPS 在两处出现两份状态。
 */
import ChainStrip from '../components/ChainStrip.vue'
import HostManager from '../components/HostManager.vue'
</script>

<template>
  <div style="padding: 16px 24px 0">
    <ChainStrip current="nodes" />
  </div>
  <HostManager
    role="all"
    title="节点管理"
    subtitle="一台节点一个角色：入站 / 出站 / 双向，随时一键切换；部署时可绑定协议配置里的配置文件"
  />
</template>
