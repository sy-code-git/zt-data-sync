<script setup>
// 工作台首页：统计卡片 + 项目环境 chips + 最近使用 + 快捷操作。
// 解锁后默认视图；点统计/环境 chips 直接跳转方案 J 列表对应位置。
import {computed, ref, onUnmounted} from 'vue'
import {useAppStore} from '../store'

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

/* ---- 账号查看弹窗（对齐原型：最近使用点开=查看/复制，非直接进编辑） ---- */
const accModal = ref(null) // {entry, revealed}
let pwTimer = null

const PW_RE = /pass|secret|token|pwd|密码/i
function pwFieldOf(e) {
  return Object.entries(e.fields || {}).find(([k]) => PW_RE.test(k))
}

function showAccount(a) {
  accModal.value = {entry: a, revealed: false}
  clearTimeout(pwTimer)
}

function revealPw() {
  if (!accModal.value) return
  accModal.value.revealed = !accModal.value.revealed
  if (accModal.value.revealed) {
    // 显示 20 秒后自动重新掩码（与 ListView 一致）
    clearTimeout(pwTimer)
    pwTimer = setTimeout(() => {
      if (accModal.value) accModal.value.revealed = false
    }, 20000)
  }
}

function closeAccModal() {
  accModal.value = null
  clearTimeout(pwTimer)
}

// 编辑入口：先取出条目再关弹窗。顺序颠倒（模板写成 closeAccModal(); store.openEdit(accModal.entry)）
// 会因 accModal 先被置空而抛 TypeError，导致「编辑此账号」点了没反应（与 ListView 同款缺陷）。
function openEditFromModal() {
  const entry = accModal.value?.entry
  closeAccModal()
  if (entry) store.openEdit(entry)
}

// 删除入口：与编辑入口同理——先取出条目再关弹窗（顺序颠倒会读到已置空的 accModal）。
// 实际删除走 store.askDelete → 确认弹窗（含子树条数）→ 级联墓碑。
function askDeleteFromModal() {
  const entry = accModal.value?.entry
  closeAccModal()
  if (entry) store.askDelete(entry)
}

function copyText(t) {
  navigator.clipboard?.writeText(t)
  store.toast('已复制到剪贴板，30 秒后自动清空', 'success')
  clearTimeout(copyText._t)
  copyText._t = setTimeout(() => navigator.clipboard?.writeText(''), 30000)
}

onUnmounted(() => {
  clearTimeout(pwTimer)
  clearTimeout(copyText._t)
})
</script>

<template>
  <div class="wb-view">
    <!-- 顶部：搜索 + 快捷操作 -->
    <div class="wb-hero">
      <div class="wb-hero__search">
        <span class="wb-hero__icon">⌕</span>
        <input placeholder="搜索账号 / IP / 备注…（⌘K）" spellcheck="false" @keydown.enter="store.goto('list')">
      </div>
      <div style="margin-left: auto; display: flex; gap: 8px">
        <button class="pb-btn pb-btn--primary pb-btn--sm" @click="store.openNew({type: 'account'})">＋ 新建</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="syncNow">⟳ 同步</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('list')">📋 密码本</button>
        <button v-if="store.isAdmin" class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('admin')">🛡 管理</button>
        <button class="pb-iconbtn" title="设置" @click="store.goto('settings')">⚙</button>
        <button class="pb-iconbtn" title="锁定" @click="store.lock()">⏻</button>
      </div>
    </div>

    <!-- 统计卡片 -->
    <div class="wb-stats">
      <div class="wb-stat" @click="store.goto('list')">
        <div class="wb-stat__label">账号总数</div>
        <div class="wb-stat__value">{{ accounts.length }}</div>
      </div>
      <div class="wb-stat" @click="store.goto('list')">
        <div class="wb-stat__label">待同步</div>
        <div class="wb-stat__value" :class="{'wb-stat__value--warn': dirtyCnt}">{{ dirtyCnt }}</div>
      </div>
      <div class="wb-stat" @click="gotoConflicts">
        <div class="wb-stat__label">待解决冲突</div>
        <div class="wb-stat__value" :class="{'wb-stat__value--danger': conflictCnt}">{{ conflictCnt }}</div>
      </div>
      <div class="wb-stat" @click="store.goto('list')">
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
                    @click="gotoEnv(p, env)"
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
              @click="showAccount(a)"
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

    <!-- 账号密码弹窗（最近使用查看；与 ListView 弹窗同交互：掩码/20s 重掩/复制 30s 清空） -->
    <div v-if="accModal" class="pb-modal-mask" @click.self="closeAccModal">
      <div class="pb-modal pb-glass pb-glass--strong" role="dialog" aria-modal="true">
        <div class="pb-modal__head">
          <span class="pb-modal__title">账号 · {{ accModal.entry.fields?.username || accModal.entry.title }}</span>
          <button class="pb-iconbtn" @click="closeAccModal">✕</button>
        </div>
        <div class="pb-modal__body">
          <div class="pb-xs pb-muted">
            {{ [envOf(accModal.entry)?.title, accModal.entry.fields?.ip].filter(Boolean).join(' · ') }}
            {{ accModal.entry.fields?.remark ? ' · ' + accModal.entry.fields.remark : '' }}
          </div>
          <div v-for="(v, k) in accModal.entry.fields" :key="k">
            <div v-if="!PW_RE.test(k)" class="wb-detail-field">
              <span class="wb-detail-field__label">{{ k }}</span>
              <div class="wb-detail-field__value">
                <span class="pb-truncate pb-fill pb-mono">{{ v }}</span>
                <button class="pb-iconbtn" title="复制" @click="copyText(v)">⧉</button>
              </div>
            </div>
          </div>
          <template v-if="pwFieldOf(accModal.entry)">
            <div class="wb-detail-field">
              <span class="wb-detail-field__label">{{ pwFieldOf(accModal.entry)[0] }}</span>
              <div class="wb-detail-field__value">
                <span class="pb-truncate pb-fill pb-mono">{{ accModal.revealed ? pwFieldOf(accModal.entry)[1] : '••••••••' }}</span>
                <button class="wb-detail-field__reveal" title="显示/隐藏" @click="revealPw">👁</button>
                <button class="pb-iconbtn" title="复制" @click="copyText(pwFieldOf(accModal.entry)[1])">⧉</button>
              </div>
            </div>
            <p class="pb-xs pb-muted">密码显示 20 秒后自动重新掩码 · 复制后 30 秒自动清空剪贴板</p>
          </template>
        </div>
        <div class="pb-modal__foot">
          <button
              class="pb-btn pb-btn--danger"
              @click="askDeleteFromModal"
          >🗑 删除此账号</button>
          <button
              class="pb-btn pb-btn--ghost"
              @click="openEditFromModal"
          >✏ 编辑此账号</button>
          <button
              class="pb-btn pb-btn--primary"
              @click="copyText(`${accModal.entry.fields?.username || accModal.entry.title}\n${pwFieldOf(accModal.entry)?.[1] || ''}`)"
          >⧉ 复制账号+密码</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 账号弹窗字段行（与 ListView 的 detail-field 同款式） */
.wb-detail-field {
  display: grid;
  grid-template-columns: 96px 1fr;
  gap: 8px;
  align-items: center;
  padding: 7px 0;
  border-bottom: 1px dashed var(--glass-border);
}
.wb-detail-field__label {
  font-size: 12.5px;
  color: var(--text-2);
}
.wb-detail-field__value {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
.wb-detail-field__reveal {
  background: none;
  border: none;
  cursor: pointer;
  font-size: 14px;
  padding: 2px 4px;
}
</style>
