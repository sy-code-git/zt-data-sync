<script setup>
// TreeNode.vue — 递归树节点（§4 五层骨架：project→env→ip_type→acc_type→account + custom 旁挂）。
// 任意深度的子节点都走本组件递归渲染，不再受固定层级数限制。
// 展开态存于 store.expandedIds（受控），视图切换后保持展开状态。
import {computed} from 'vue'
import {useAppStore} from '../store'

const props = defineProps({
  node: {type: Object, required: true}, // { entry, children }
  depth: {type: Number, default: 0},
  typeMeta: {type: Object, default: () => ({})},
  selectedId: {type: String, default: ''},
})
const emit = defineEmits(['select'])
const store = useAppStore()

const hasChildren = computed(() => props.node.children.length > 0)
const isExpanded = computed(() => store.expandedIds.includes(props.node.entry.id))

function toggle() {
  store.toggleExpand(props.node.entry.id)
}
</script>

<template>
  <div class="tree-branch">
    <div
        class="pb-tree-node"
        :class="{'pb-tree-node--active': node.entry.id === selectedId}"
        :style="{ paddingLeft: 12 + depth * 12 + 'px' }"
        @click="emit('select', node)"
    >
      <span
          v-if="hasChildren"
          class="pb-tree-node__arrow"
          :class="{'pb-tree-node__arrow--open': isExpanded}"
          @click.stop="toggle"
      >▶</span>
      <span v-else class="pb-tree-node__arrow"></span>
      <span class="pb-tree-node__icon">{{ typeMeta[node.entry.type]?.icon || '📄' }}</span>
      <span class="pb-truncate pb-fill">{{ node.entry.title }}</span>
      <span v-if="node.entry.dirty" class="pb-dot pb-dot--warn" title="未推送"></span>
      <span v-if="node.entry.conflict_of" class="pb-badge pb-badge--danger" style="height:18px;padding:0 8px;font-size:11px">冲突</span>
    </div>

    <div v-if="isExpanded && hasChildren" class="tree-children">
      <TreeNode
          v-for="c in node.children"
          :key="c.entry.id"
          :node="c"
          :depth="depth + 1"
          :type-meta="typeMeta"
          :selected-id="selectedId"
          @select="emit('select', $event)"
      />
    </div>
  </div>
</template>

<style scoped>
.tree-branch {
  display: flex;
  flex-direction: column;
}
.tree-children {
  display: flex;
  flex-direction: column;
}
</style>
