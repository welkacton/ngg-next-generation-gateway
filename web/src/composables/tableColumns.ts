import { h } from 'vue'
import { NTag } from 'naive-ui'
import { prettyJson } from '../utils/format'

/**
 * 依据数据首行动态生成表格列，覆盖后端返回的任意字段。
 * 布尔值渲染为标签，对象/数组渲染为 JSON 文本，避免字段被遗漏。
 */
export function makeColumns(rows: any[]): any[] {
  const keys = Object.keys(rows[0] || {})
  if (!keys.length) {
    return [
      {
        title: '原始内容',
        key: '__raw',
        render: (row: any) => h('span', { class: 'mono' }, prettyJson(row)),
      },
    ]
  }
  return keys.map((k) => ({
    title: k,
    key: k,
    minWidth: 110,
    render: (row: any) => {
      const v = row[k]
      if (v === null || v === undefined) return h('span', { class: 'hint' }, '—')
      if (typeof v === 'boolean') {
        return h(
          NTag,
          { size: 'small', bordered: false, type: v ? 'success' : 'default' },
          { default: () => (v ? '是' : '否') },
        )
      }
      if (typeof v === 'object') {
        return h('span', { class: 'mono' }, prettyJson(v))
      }
      return String(v)
    },
  }))
}
