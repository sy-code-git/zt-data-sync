<script setup>
// 工作台首页：统计卡片 + 项目环境 chips + 最近使用 + 快捷操作。
// 解锁后默认视图；点统计/环境 chips 直接跳转方案 J 列表对应位置。
import {computed, ref} from 'vue'
import {useAppStore} from '../store'
import AccountModal from '../components/AccountModal.vue'

const store = useAppStore()

const entries = computed(() => store.entries.filter((e) => !e.deleted))

/* ---- 统计 ---- */
const accounts = computed(() => entries.value.filter((e) => e.type === 'account'))
// 对齐原型：待同步/冲突统计覆盖全部实体（account + custom），不仅账号
const dirtyCnt = computed(() => entries.value.filter((e) => e.dirty || e.conflict_of).length)
const conflictCnt = computed(() => entries.value.filter((e) => e.conflict_of).length)
const latest = computed(() => Math.max(0, ...entries.value.map((e) => e.updated_at || 0)))

const projects = computed(() => entries.value.filter((e) => e.type === 'project'))

// children 索引（一次构建，供环境账号统计 / envOf 使用，避免 O(n²) 递归）
const childIndex = computed(() => {
  const m = new Map()
  for (const e of entries.value) {
    const arr = m.get(e.parent_id ?? null) || []
    arr.push(e)
    m.set(e.parent_id ?? null, arr)
  }
  return m
})

// 环境账号数：沿子树遍历，用索引避免每环境全表扫描
function envAccountCount(envId) {
  let cnt = 0
  const stack = [...(childIndex.value.get(envId) || [])]
  while (stack.length) {
    const e = stack.pop()
    if (e.type === 'account') cnt++
    stack.push(...(childIndex.value.get(e.id) || []))
  }
  return cnt
}

function envChipsOf(p) {
  return entries.value
    .filter((e) => e.type === 'env' && e.parent_id === p.id)
    .map((env) => ({env, cnt: envAccountCount(env.id)}))
}

/* ---- 最近使用 ---- */
const recent = computed(() =>
  [...accounts.value].sort((a, b) => (b.updated_at || 0) - (a.updated_at || 0)).slice(0, 5)
)

// 沿 parent_id 链向上找环境节点（idIndex 直查，O(深度)）
const idIndex = computed(() => {
  const m = new Map()
  for (const e of entries.value) m.set(e.id, e)
  return m
})
function envOf(e) {
  let p = e.parent_id
  while (p) {
    const n = idIndex.value.get(p)
    if (!n) break
    if (n.type === 'env') return n
    p = n.parent_id
  }
  return null
}

function fmtTime(ts) {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const pd = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pd(d.getMonth() + 1)}-${pd(d.getDate())} ${pd(d.getHours())}:${pd(d.getMinutes())}`
}

/* ---- 跳转动作 ---- */
// 点环境 chip → 方案 J 列表页定位到该项目的该环境
function gotoEnv(p, env) {
  store.pjProject = p.id
  store.pjFocus = env.id
  store.pjIp = ''
  store.goto('list')
}

function gotoConflicts() {
  const first = entries.value.find((e) => e.conflict_of)
  if (first) store.openConflict(first)
  else store.toast('当前没有待解决的冲突', 'info')
}

async function syncNow() {
  try {
    await store.syncAll()
    store.toast('同步完成', 'success')
  } catch (e) {
    store.toast(String(e.message || e), 'error')
  }
}

/* ---- 账号查看弹窗（对齐原型：最近使用点开=查看/复制，非直接进编辑） ----
   UI 与交互（掩码/20s 重掩/复制 30s 清空/焦点陷阱/Esc）统一在 components/AccountModal.vue */
const accModal = ref(null) // {entry}

function showAccount(a) {
  accModal.value = {entry: a}
}

function closeAccModal() {
  accModal.value = null
}

// 归属行（环境 · IP · 备注）
const accSubtitle = computed(() => {
  const m = accModal.value
  if (!m) return ''
  return [envOf(m.entry)?.title, m.entry.fields?.ip].filter(Boolean).join(' · ') +
      (m.entry.fields?.remark ? ' · ' + m.entry.fields.remark : '')
})

// 编辑/删除入口：条目由弹窗组件 emit 传入（不再从已置空的 accModal 里读，避免顺序陷阱）
function openEditFromModal(entry) {
  closeAccModal()
  if (entry) store.openEdit(entry)
}

function askDeleteFromModal(entry) {
  closeAccModal()
  if (entry) store.askDelete(entry)
}
</script>

<template>
  <div class="wb-view">
    <!-- 顶部：搜索 + 快捷操作 -->
    <div class="wb-hero">
      <div class="wb-hero__search">
        <span class="wb-hero__icon">⌕</span>
        <!-- 搜索词与列表页共用 store.listSearch：输入后回车（或 Ctrl/⌘+K）跳到列表页即已生效 -->
        <input
            v-model="store.listSearch"
            placeholder="搜索账号 / IP / 备注…（Ctrl+K）"
            aria-label="搜索账号 / IP / 备注"
            spellcheck="false"
            @keydown.enter="store.openSearch()"
        >
      </div>
      <div style="margin-left: auto; display: flex; gap: 8px">
        <button class="pb-btn pb-btn--primary pb-btn--sm" @click="store.openNew({type: 'account'})">＋ 新建</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" :disabled="store.syncing" @click="syncNow">
          {{ store.syncing ? '同步中…' : '⟳ 同步' }}
        </button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('list')">📋 密码本</button>
        <button v-if="store.isAdmin" class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('admin')">🛡 管理</button>
        <button class="pb-iconbtn" title="设置" @click="store.goto('settings')">⚙</button>
        <button class="pb-iconbtn" title="锁定" @click="store.lock()">⏻</button>
      </div>
    </div>

    <!-- 统计卡片（键盘可达：Tab 聚焦 + Enter/Space 跳转） -->
    <div class="wb-stats">
      <div class="wb-stat" role="button" tabindex="0" @click="store.goto('list')"
           @keydown.enter="store.goto('list')" @keydown.space.prevent="store.goto('list')">
        <div class="wb-stat__label">账号总数</div>
        <div class="wb-stat__value">{{ accounts.length }}</div>
      </div>
      <div class="wb-stat" role="button" tabindex="0" @click="store.goto('list')"
           @keydown.enter="store.goto('list')" @keydown.space.prevent="store.goto('list')">
        <div class="wb-stat__label">待同步</div>
        <div class="wb-stat__value" :class="{'wb-stat__value--warn': dirtyCnt}">{{ dirtyCnt }}</div>
      </div>
      <div class="wb-stat" role="button" tabindex="0" @click="gotoConflicts"
           @keydown.enter="gotoConflicts" @keydown.space.prevent="gotoConflicts">
        <div class="wb-stat__label">待解决冲突</div>
        <div class="wb-stat__value" :class="{'wb-stat__value--danger': conflictCnt}">{{ conflictCnt }}</div>
      </div>
      <div class="wb-stat" role="button" tabindex="0" @click="store.goto('list')"
           @keydown.enter="store.goto('list')" @keydown.space.prevent="store.goto('list')">
        <div class="wb-stat__label">最近更新</div>
        <div class="wb-stat__value" style="font-size: 15px; padding-top: 5px">{{ latest ? fmtTime(latest) : '—' }}</div>
      </div>
    </div>

    <div class="wb-grid">
      <!-- 项目与环境 -->
      <div class="wb-card">
        <div class="wb-card__title">项目与环境</div>
        <div class="wb-env">
          <template v-if="projects.length">
            <template v-for="p in projects" :key="p.id">
              <div class="wb-env__proj">{{ p.title }}</div>
              <div class="wb-env__chips">
                <span
                    v-for="({env, cnt}) in envChipsOf(p)"
                    :key="env.id"
                    class="wb-env__chip"
                    title="跳转到该环境"
                    role="button"
                    tabindex="0"
                    @click="gotoEnv(p, env)"
                    @keydown.enter="gotoEnv(p, env)"
                    @keydown.space.prevent="gotoEnv(p, env)"
                >{{ env.title }} <span style="color: var(--text-3)">{{ cnt }}</span></span>
                <span v-if="!envChipsOf(p).length" style="font-size: 12px; color: var(--text-3)">暂无环境</span>
              </div>
            </template>
          </template>
          <span v-else style="font-size: 12.5px; color: var(--text-3)">
            暂无项目 · 点右上「＋ 新建」创建第一个账号或项目
          </span>
        </div>
      </div>

      <!-- 最近使用 + 快捷操作 -->
      <div class="wb-card">
        <div class="wb-card__title">最近使用</div>
        <div class="wb-recent">
          <div
              v-for="a in recent"
              :key="a.id"
              class="wb-recent__row"
              role="button"
              tabindex="0"
              @click="showAccount(a)"
              @keydown.enter="showAccount(a)"
              @keydown.space.prevent="showAccount(a)"
          >
            <span class="wb-recent__name">{{ a.fields?.username || a.title }}</span>
            <span class="wb-recent__meta">{{ envOf(a)?.title || '' }} · {{ fmtTime(a.updated_at) }}</span>
          </div>
          <span v-if="!recent.length" style="font-size: 12.5px; color: var(--text-3); padding: 8px 6px">暂无使用记录</span>
        </div>
        <div class="wb-quick">
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.openNew({type: 'account'})">＋ 新建账号</button>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.toast('账号批量导入将在后续版本提供', 'info')">📥 导入</button>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.toast('账号批量导出将在后续版本提供', 'info')">📤 导出</button>
        </div>
      </div>
    </div>

    <!-- 账号密码弹窗（组件：掩码 / 20s 重掩 / 复制 30s 清空 / 焦点陷阱 / Esc） -->
    <AccountModal
        v-if="accModal"
        :entry="accModal.entry"
        :subtitle="accSubtitle"
        @close="closeAccModal"
        @edit="openEditFromModal"
        @delete="askDeleteFromModal"
    />
  </div>
</template>
