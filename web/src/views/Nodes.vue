<script setup lang="ts">
/**
 * 节点管理 —— 原「入口节点」与「出口节点」合并后的唯一节点页面。
 *
 * 角色不再是两个菜单，而是同一份节点库上的一个字段：入站 / 出站 / 双向，
 * 卡片上可直接切换。合并不是把两个列表拼在一起：这里只有一个列表、一个筛选器，
 * 避免同一台 VPS 在两处出现两份状态。
 *
 * v2.13.0：卡片同时承载三件事——绑定「协议配置」里的配置文件、面板经双向 TLS
 * 连入节点读取的真实系统资源（CPU / 内存 / 硬盘 / 流量）、以及部署脚本入口。
 * 节点没连上时不显示任何伪造数值，只显示原因。全程没有 SSH，也没有节点侧探针。
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
    subtitle="绑定协议配置文件 · 查看真实 CPU / 内存 / 硬盘 / 流量 · 一键部署到节点服务器"
  />
</template>
