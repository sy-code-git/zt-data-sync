<script setup>
import {computed, ref, watch, onMounted} from 'vue'
import {useAppStore} from '../store'
import {api} from '../api'
import TreeNode from '../components/TreeNode.vue'

// 可添加子项的结构性层级（§5.1：account/custom 为叶子，不挂子节点）
const branchTypes = ['project', 'env', 'ip_type', 'acc_type']

const store = useAppStore()

const search = ref('')
const selected = ref(null)
const treeRoots = computed(() => buildTree(store.entries, null))

// 类型 icon / 层级元数据（§5.1 五层骨架）
const typeMeta = {
  project: {icon: '🗂', label: '项目'},
  env: {icon: '🌐', label: '环境'},
  ip_type: {icon: '🖥', label: 'IP 类型'},
  acc_type: {icon: '👤', label: '账号类型'},
  account: {icon: '🔑', label: '账号'},
  custom: {icon: '📎', label: '补充卡片'},
}

function buildTree(entries, parentId) {
  return entries
      .filter((e) => !e.deleted && e.parent_id === parentId)
      .map((e) => ({entry: e, children: buildTree(entries, e.id)}))
}

// 按 id 在树中查找节点（含任意深度）
function findNode(nodes, id) {
  for (const n of nodes) {
    if (n.entry.id === id) return n
    const r = findNode(n.children || [], id)
    if (r) return r
  }
  return null
}

// 搜索过滤（§1：title + account 字段 + custom 卡片键值均纳入检索）
const filteredRoots = computed(() => {
  if (!search.value.trim()) return treeRoots.value
  const kw = search.value.trim().toLowerCase()
  const fieldHit = (obj) =>
      Object.keys(obj || {}).some((k) =>
          k.toLowerCase().includes(kw) || String(obj[k]).toLowerCase().includes(kw))
  const hit = (e) =>
      e.title.toLowerCase().includes(kw) || fieldHit(e.fields) || fieldHit(e.custom_fields)
  const walk = (nodes) =>
      nodes
          .map((n) => ({...n, children: walk(n.children)}))
          .filter((n) => hit(n.entry) || n.children.length > 0)
  return walk(treeRoots.value)
})

function select(node) {
  selected.value = node
  store.selectedId = node.entry.id
}

function fmtTime(ts) {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

// 剪贴板复制：30 秒后自动清空（§5 剪贴板约定），多次复制重置计时
let clipboardClearTimer = null
function copyText(t) {
  navigator.clipboard?.writeText(t)
  store.toast('已复制到剪贴板，30 秒后自动清空', 'success')
  clearTimeout(clipboardClearTimer)
  clipboardClearTimer = setTimeout(() => {
    navigator.clipboard?.writeText('')
  }, 30000)
}

async function syncNow() {
  try {
    await store.syncNow()
    await store.refreshEntries()
    store.toast('同步完成', 'success')
  } catch (e) {
    store.toast(String(e.message || e), 'error')
  }
}

// 条目刷新（同步/编辑/删除）后按 selectedId 重定位选中节点：
// 条目已删则清空选择，避免详情面板残留已删/旧数据
watch(() => store.entries, () => {
  if (!store.selectedId) return
  selected.value = findNode(treeRoots.value, store.selectedId)
  if (!selected.value) store.selectedId = ''
})

// 搜索时清空选中（详情面板基于过滤前节点，子项已被过滤会误导）
watch(() => search.value, (kw) => {
  if (kw) selected.value = null
})

onMounted(() => {
  // 首次进入（无展开记录）默认展开顶层项目；有记录则保持上次状态
  if (!store.expandedIds.length) {
    treeRoots.value.forEach((n) => store.expandedIds.push(n.entry.id))
  }
  // 恢复上次选中（编辑/冲突页返回后仍选中原条目）
  if (store.selectedId) {
    selected.value = findNode(treeRoots.value, store.selectedId)
    if (!selected.value) store.selectedId = ''
  }
})
</script>

<template>
  <div class="list-view">
    <!-- 顶部工具栏 -->
    <div class="list-toolbar">
      <div class="list-toolbar__left">
        <div class="list-search">
          <span class="list-search__icon">⌕</span>
          <input
              v-model="search"
              class="list-search__input"
              placeholder="搜索标题 / 账号 / IP…"
              spellcheck="false"
          />
        </div>
      </div>

      <div class="list-toolbar__right">
        <span v-if="store.syncBadge" class="pb-badge" :class="`pb-badge--${store.syncBadge.type}`">
          <span class="pb-dot" :class="`pb-dot--${store.syncBadge.type === 'success' ? 'ok' : store.syncBadge.type === 'danger' ? 'err' : store.syncBadge.type === 'warning' ? 'warn' : 'idle'}`"></span>
          {{ store.syncBadge.text }}
        </span>
        <button class="pb-btn pb-btn--primary pb-btn--sm" @click="store.openEdit({_new: true})">＋ 新建</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="syncNow">⟳ 同步</button>
        <button v-if="store.isAdmin" class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('admin')">🛡 管理</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('settings')">⚙ 设置</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.lock()">⏻ 锁定</button>
      </div>
    </div>

    <!-- 主体 -->
    <div class="list-body">
      <!-- 树形浏览 -->
      <div class="list-tree pb-glass pb-glass--flat">
        <div class="list-pane-title">
          <span>条目树</span>
          <span class="pb-xs pb-muted">{{ store.entries.filter((e) => !e.deleted).length }} 项</span>
        </div>
        <div class="list-tree__scroll">
          <template v-if="filteredRoots.length">
            <TreeNode
                v-for="node in filteredRoots"
                :key="node.entry.id"
                :node="node"
                :depth="0"
                :type-meta="typeMeta"
                :selected-id="store.selectedId"
                @select="select"
            />
          </template>
          <div v-else class="pb-empty">
            <span class="pb-empty__icon">🔍</span>
            <span class="pb-empty__title">{{ search ? '无匹配结果' : '暂无条目' }}</span>
            <span class="pb-empty__desc">{{ search ? '换个关键词试试' : '管理员添加成员后，加密条目将自动同步到本机' }}</span>
          </div>
        </div>
      </div>

      <!-- 详情面板 -->
      <div class="list-detail pb-glass pb-glass--flat">
        <template v-if="selected">
          <div class="detail-head">
            <div class="detail-head__title">
              <span class="detail-head__icon">{{ typeMeta[selected.entry.type]?.icon }}</span>
              <h2>{{ selected.entry.title }}</h2>
            </div>
            <div class="detail-head__badges">
              <span v-if="selected.entry.archived" class="pb-badge pb-badge--warning">已归档（只读）</span>
              <span v-if="selected.entry.dirty" class="pb-badge pb-badge--warning">未推送</span>
              <span v-if="selected.entry.conflict_of" class="pb-badge pb-badge--danger">待解决冲突</span>
              <span class="pb-badge pb-badge--neutral">{{ typeMeta[selected.entry.type]?.label }}</span>
            </div>
          </div>

          <div class="detail-meta pb-xs pb-muted">
            更新于 {{ fmtTime(selected.entry.updated_at) }} · seq {{ selected.entry.seq }}
          </div>

          <hr class="pb-divider"/>

          <!-- 账号字段 -->
          <template v-if="selected.entry.type === 'account'">
            <div v-for="(val, key) in selected.entry.fields" :key="key" class="detail-field">
              <span class="detail-field__label">{{ key }}</span>
              <div class="detail-field__value">
                <span class="pb-truncate pb-fill pb-mono">{{ val }}</span>
                <button class="pb-iconbtn" title="复制" @click="copyText(val)">⧉</button>
              </div>
            </div>
          </template>

          <!-- custom 补充卡片 -->
          <template v-else-if="selected.entry.type === 'custom'">
            <div v-for="(val, key) in selected.entry.custom_fields" :key="key" class="detail-field">
              <span class="detail-field__label">{{ key }}</span>
              <div class="detail-field__value">
                <span class="pb-truncate pb-fill pb-mono">{{ val }}</span>
              </div>
            </div>
          </template>

          <!-- 中间节点：子项概览 -->
          <template v-else>
            <div class="detail-children">
              <div v-if="selected.children.length" v-for="c in selected.children" :key="c.entry.id" class="detail-child">
                <span class="pb-tree-node__icon">{{ typeMeta[c.entry.type]?.icon }}</span>
                <span class="pb-fill pb-truncate">{{ c.entry.title }}</span>
                <span v-if="c.children.length" class="pb-xs pb-muted">{{ c.children.length }} 子项</span>
              </div>
              <div v-else class="pb-empty" style="padding: 32px 16px">
                <span class="pb-empty__icon">📂</span>
                <span class="pb-empty__title">暂无子条目</span>
              </div>
            </div>
          </template>

          <hr class="pb-divider"/>

          <div class="detail-actions">
            <template v-if="selected.entry.conflict_of">
              <button class="pb-btn pb-btn--primary" @click="store.openConflict(selected.entry)">⚠ 解决冲突</button>
            </template>
            <template v-else>
              <button
                  class="pb-btn pb-btn--primary"
                  :disabled="selected.entry.archived"
                  @click="store.openEdit(selected.entry)"
              >✏ 编辑</button>
              <button
                  v-if="branchTypes.includes(selected.entry.type)"
                  class="pb-btn pb-btn--ghost"
                  :disabled="selected.entry.archived"
                  @click="store.openEdit({...selected.entry, _new: true})"
              >＋ 添加子项</button>
              <button
                  class="pb-btn pb-btn--ghost"
                  :disabled="selected.entry.archived"
                  @click="store.askDelete(selected.entry)"
              >🗑 删除</button>
            </template>
          </div>
        </template>

        <div v-else class="pb-empty" style="padding: 80px 32px">
          <span class="pb-empty__icon">🔐</span>
          <span class="pb-empty__title">从左侧选择一条条目</span>
          <span class="pb-empty__desc">树形浏览项目 → 环境 → IP 类型 → 账号类型 → 账号 五层骨架</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.list-view {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 16px;
  gap: 14px;
}

.list-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-shrink: 0;
}
.list-toolbar__left { display: flex; flex: 1; }
.list-toolbar__right { display: flex; align-items: center; gap: 8px; }

.list-search {
  position: relative;
  width: min(420px, 100%);
}
.list-search__icon {
  position: absolute;
  left: 13px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--text-3);
  font-size: 15px;
  pointer-events: none;
}
.list-search__input {
  width: 100%;
  height: 38px;
  padding: 0 14px 0 36px;
  border-radius: var(--radius-md);
  background: var(--input-bg);
  border: 1px solid var(--glass-border);
  color: var(--text-1);
  font-size: 13.5px;
  transition: all var(--dur) var(--ease);
}
.list-search__input:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.list-body {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: 320px 1fr;
  gap: 14px;
}

.list-tree {
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.list-pane-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px 10px;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.03em;
  flex-shrink: 0;
}
.list-tree__scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 4px 8px 12px;
}
.list-tree__children {
  display: flex;
  flex-direction: column;
}

.list-detail {
  min-height: 0;
  overflow-y: auto;
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.detail-head__title {
  display: flex;
  align-items: center;
  gap: 12px;
}
.detail-head__icon {
  font-size: 22px;
}
.detail-head__title h2 {
  font-size: 20px;
  font-weight: 700;
}
.detail-head__badges {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.detail-meta { letter-spacing: 0.02em; }

.detail-field {
  display: grid;
  grid-template-columns: 96px 1fr;
  gap: 10px;
  align-items: center;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
}
.detail-field__label {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-2);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.detail-field__value {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.detail-children {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.detail-child {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
  font-size: 13.5px;
}

.detail-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

/* 窄窗响应式（§4 窗口可缩放）：先收窄树列，再改单列堆叠 */
@media (max-width: 1080px) {
  .list-body {
    grid-template-columns: 260px 1fr;
  }
  .list-toolbar {
    flex-wrap: wrap;
  }
}
@media (max-width: 860px) {
  .list-body {
    grid-template-columns: 1fr;
    grid-template-rows: minmax(200px, 42%) 1fr;
  }
  .list-toolbar {
    flex-direction: column;
    align-items: stretch;
  }
  .list-toolbar__right {
    flex-wrap: wrap;
  }
}
</style>
