<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'

const props = defineProps<{ current: string }>()
const router = useRouter()

const items = [
  { key: 'pipeline', label: '链路编排', page: 'pipeline' },
  { key: 'protocols', label: '协议配置', page: 'protocols' },
  { key: 'nodes', label: '节点管理', page: 'nodes' },
  { key: 'rules', label: '规则配置', page: 'rules' },
  { key: 'dns', label: 'DNS配置', page: 'dns' },
  { key: 'users', label: '用户管理', page: 'users' },
]

const activeIndex = computed(() => Math.max(0, items.findIndex((i) => i.key === props.current)))

function open(page: string) {
  router.push({ name: page })
}
</script>

<template>
  <div class="chain-strip">
    <span class="chain-strip__label">代理链路</span>
    <template v-for="(it, i) in items" :key="it.key">
      <button
        type="button"
        class="chain-chip"
        :class="{ 'is-active': i === activeIndex, 'is-done': i < activeIndex }"
        @click="open(it.page)"
      >
        <span class="chain-chip__idx">{{ i + 1 }}</span>
        {{ it.label }}
      </button>
      <span v-if="i < items.length - 1" class="chain-strip__sep">›</span>
    </template>
    <button type="button" class="chain-strip__all" @click="open('pipeline')">链路总览 →</button>
  </div>
</template>

<style scoped>
.chain-strip {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  padding: 8px 10px;
  margin-bottom: 12px;
  background: #fff;
  border: 1px solid #ececf1;
  border-radius: 12px;
}
.chain-strip__label {
  font-size: 11px;
  color: #8e8e93;
  letter-spacing: 0.04em;
  margin-right: 6px;
}
.chain-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 24px;
  padding: 0 9px;
  border-radius: 12px;
  border: 1px solid transparent;
  background: #f5f5f7;
  color: #6e6e73;
  font-size: 11.5px;
  cursor: pointer;
  transition: background 0.16s ease, color 0.16s ease, border-color 0.16s ease;
}
.chain-chip:hover {
  background: #ececf1;
  color: #1d1d1f;
}
.chain-chip__idx {
  font-size: 9.5px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: #e2e2e7;
  color: #6e6e73;
}
.chain-chip.is-done .chain-chip__idx {
  background: #d1ebd8;
  color: #1f7a3a;
}
.chain-chip.is-active {
  background: #eaf3ff;
  border-color: #c9e0ff;
  color: #0058b0;
  font-weight: 600;
}
.chain-chip.is-active .chain-chip__idx {
  background: #0071e3;
  color: #fff;
}
.chain-strip__sep {
  color: #d2d2d7;
  font-size: 12px;
}
.chain-strip__all {
  margin-left: auto;
  border: 0;
  background: none;
  color: #0071e3;
  font-size: 11.5px;
  cursor: pointer;
}
.chain-strip__all:hover {
  text-decoration: underline;
}
</style>
