<script setup>
import {ref, reactive, computed, onMounted} from 'vue'
import {useAppStore} from '../store'
import {api} from '../api'

const store = useAppStore()
const conflict = ref(null) // api.ConflictDetail { id, base, ours, theirs }
const loading = ref(true)
const busy = ref(false)

// 逐字段解决（§7.3）：key -> 'local' | 'server' | 'manual'
const choices = reactive({})
const manualVals = reactive({})

onMounted(async () => {
  const id = store.editing?.id
  if (!id) {
    store.toast('缺少冲突条目', 'error')
    store.goto('list')
    return
  }
  try {
    conflict.value = await api.GetConflict(id)
    // 手动编辑底稿：默认取 ours 的值
    for (const key of allKeys.value) {
      manualVals[key] = rawVal(conflict.value.ours, key)
    }
  } catch (e) {
    store.toast(String(e.message || e), 'error')
    store.goto('list')
  } finally {
    loading.value = false
  }
})

const isCustom = computed(() => (conflict.value?.ours?.type || conflict.value?.base?.type || '') === 'custom')

// 参与 diff 的字段 key 集（base/ours/theirs 并集，不含 title）
const fieldKeys = computed(() => {
  const set = []
  const seen = new Set()
  for (const v of [conflict.value?.base, conflict.value?.ours, conflict.value?.theirs]) {
    if (!v) continue
    for (const k of Object.keys(v.fields || {})) {
      if (!seen.has(k)) { seen.add(k); set.push(k) }
    }
    for (const k of Object.keys(v.custom_fields || {})) {
      if (!seen.has(k)) { seen.add(k); set.push(k) }
    }
  }
  return set
})

// 所有可解决字段（title + 字段键）
const allKeys = computed(() => ['title', ...fieldKeys.value])

function rawVal(v, key) {
  if (!v) return ''
  if (key === 'title') return v.title || ''
  const f = v.fields && key in v.fields ? v.fields[key]
      : (v.custom_fields && key in v.custom_fields ? v.custom_fields[key] : '')
  return f == null ? '' : String(f)
}

// 三态判断（§7.3 逐字段）
function stateOf(key) {
  const b = rawVal(conflict.value?.base, key)
  const o = rawVal(conflict.value?.ours, key)
  const t = rawVal(conflict.value?.theirs, key)
  if (b === o && o === t) return 'same'   // 三方一致
  if (b === o) return 'server'            // 本地未改 → 采用服务端
  if (b === t) return 'local'             // 服务端未改 → 采用本地
  if (o === t) return 'same'              // 双方改成一样
  return 'conflict'                       // 双方改不同 → 需用户选择
}

// 冲突字段（双方改不同，需逐字段选择）
const conflictKeys = computed(() => allKeys.value.filter((k) => stateOf(k) === 'conflict'))

// 某字段是否有差异（三栏 diff 高亮用）
function isDiff(key) {
  return stateOf(key) !== 'same'
}

// 密码类字段掩码显示
function masked(key, val) {
  if (!val) return val
  return /pass|secret|token|pwd|密码/i.test(key) ? '••••••••' : val
}

function fmtTime(ts) {
  if (!ts) return '—'
  const d = new Date(ts * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

// 一键全选
function allLocal() {
  for (const k of conflictKeys.value) choices[k] = 'local'
}
function allServer() {
  for (const k of conflictKeys.value) choices[k] = 'server'
}

// 字段归属 scope（fields / custom_fields）
function scopeOf(key) {
  for (const v of [conflict.value?.ours, conflict.value?.base, conflict.value?.theirs]) {
    if (v?.fields && key in v.fields) return 'fields'
    if (v?.custom_fields && key in v.custom_fields) return 'custom_fields'
  }
  return isCustom.value ? 'custom_fields' : 'fields'
}

// 某字段最终值（按选择或自动合并规则）
function resolveKey(key) {
  const st = stateOf(key)
  if (st === 'conflict') {
    const c = choices[key]
    if (c === 'local') return rawVal(conflict.value?.ours, key)
    if (c === 'server') return rawVal(conflict.value?.theirs, key)
    if (c === 'manual') return manualVals[key] || ''
    return ''
  }
  // 无冲突：自动合并（ours 改则 ours，否则 theirs）
  const b = rawVal(conflict.value?.base, key)
  const o = rawVal(conflict.value?.ours, key)
  const t = rawVal(conflict.value?.theirs, key)
  return o !== b ? o : t
}

// 提交解决：组装逐字段合并结果 → 走 manual 分支（core.ResolveConflict 直接采用）
async function resolve() {
  if (busy.value) return
  for (const k of conflictKeys.value) {
    if (!choices[k]) {
      store.toast(`请为字段「${k}」选择解决方式`, 'error')
      return
    }
    if (choices[k] === 'manual' && !String(manualVals[k]).trim()) {
      store.toast(`字段「${k}」手动值不能为空`, 'error')
      return
    }
  }
  const src = conflict.value.ours || conflict.value.base
  // 提交纯明文（id/group_id/seq 等同步元数据由 core 从本地条目取，无需前端传）
  const merged = {
    type: src?.type || 'account',
    title: resolveKey('title'),
    parent_id: src?.parent_id ?? null,
    fields: {},
    custom_fields: {},
  }
  if (!String(merged.title).trim()) {
    store.toast('标题不能为空', 'error')
    return
  }
  for (const key of fieldKeys.value) {
    const scope = scopeOf(key)
    // 后端字段为 json.RawMessage，值需 JSON 编码
    merged[scope][key] = JSON.stringify(String(resolveKey(key)))
  }
  busy.value = true
  try {
    await api.ResolveConflict(conflict.value.id, false, merged)
    store.toast('冲突已解决，变更将同步到服务端', 'success')
    store.editing = null
    await store.refreshEntries()
    store.goto('list')
  } catch (e) {
    store.toast(String(e.message || e), 'error')
  } finally {
    busy.value = false
  }
}

function back() {
  store.editing = null
  store.goto('list')
}
</script>

<template>
  <div class="conflict">
    <!-- 顶部 -->
    <div class="conflict-head">
      <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="back">← 返回列表</button>
      <div class="conflict-head__title">
        <span class="conflict-head__icon">⚔</span>
        <div>
          <h2>解决冲突</h2>
          <p class="pb-xs pb-muted">同一条目在多端被修改 · 按字段选择保留哪一份内容</p>
        </div>
      </div>
      <span class="pb-badge pb-badge--danger">
        <span class="pb-dot pb-dot--err"></span>{{ conflictKeys.length }} 处冲突
      </span>
    </div>

    <div v-if="loading" class="conflict-loading pb-center">
      <div class="pb-spinner" style="width:26px;height:26px"></div>
    </div>

    <template v-else-if="conflict">
      <!-- 三栏版本对比 -->
      <div class="conflict-cols">
        <div class="conflict-col pb-glass pb-glass--flat">
          <div class="conflict-col__head">
            <span class="pb-badge pb-badge--neutral">基础版本</span>
            <span class="pb-xs pb-muted">{{ fmtTime(conflict.base?.updated_at) }}</span>
          </div>
          <div class="conflict-col__body">
            <div class="conflict-col__title">{{ conflict.base?.title || '（无记录）' }}</div>
            <div v-for="key in fieldKeys" :key="'b' + key" class="conflict-col__field">
              <span class="conflict-col__key">{{ key }}</span>
              <span class="pb-mono">{{ masked(key, rawVal(conflict.base, key)) || '—' }}</span>
            </div>
          </div>
        </div>

        <div class="conflict-col pb-glass pb-glass--flat">
          <div class="conflict-col__head">
            <span class="pb-badge pb-badge--accent">本地版本（未推送）</span>
            <span class="pb-xs pb-muted">{{ fmtTime(conflict.ours?.updated_at) }}</span>
          </div>
          <div class="conflict-col__body">
            <div class="conflict-col__title" :class="{'conflict-col__title--diff': stateOf('title') !== 'same'}">{{ conflict.ours?.title || '（无记录）' }}</div>
            <div
                v-for="key in fieldKeys"
                :key="'o' + key"
                class="conflict-col__field"
                :class="{'conflict-col__field--diff': isDiff(key)}"
            >
              <span class="conflict-col__key">{{ key }}</span>
              <span class="pb-mono">{{ masked(key, rawVal(conflict.ours, key)) || '—' }}</span>
            </div>
          </div>
        </div>

        <div class="conflict-col pb-glass pb-glass--flat">
          <div class="conflict-col__head">
            <span class="pb-badge pb-badge--warning">服务端版本</span>
            <span class="pb-xs pb-muted">{{ fmtTime(conflict.theirs?.updated_at) }}</span>
          </div>
          <div class="conflict-col__body">
            <div class="conflict-col__title" :class="{'conflict-col__title--diff': stateOf('title') !== 'same'}">{{ conflict.theirs?.title || '（无记录）' }}</div>
            <div
                v-for="key in fieldKeys"
                :key="'t' + key"
                class="conflict-col__field"
                :class="{'conflict-col__field--diff': isDiff(key)}"
            >
              <span class="conflict-col__key">{{ key }}</span>
              <span class="pb-mono">{{ masked(key, rawVal(conflict.theirs, key)) || '—' }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 逐字段解决区（仅冲突字段） -->
      <div v-if="conflictKeys.length" class="conflict-resolve pb-glass pb-glass--flat" role="group" aria-label="逐字段解决冲突">
        <div class="conflict-resolve__head">
          <span class="pb-bold">逐字段解决</span>
          <div class="conflict-resolve__quick">
            <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="allLocal">全部采用本地</button>
            <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="allServer">全部采用服务端</button>
          </div>
        </div>
        <div v-for="key in conflictKeys" :key="key" class="conflict-resolve__row">
          <span class="conflict-resolve__key">{{ key === 'title' ? '标题' : key }}</span>
          <div class="conflict-resolve__opts">
            <button
                class="pb-btn pb-btn--ghost pb-btn--sm"
                :class="{'conflict-resolve__active': choices[key] === 'local'}"
                @click="choices[key] = 'local'"
            >本地<span class="pb-mono pb-xs">「{{ masked(key, rawVal(conflict.ours, key)) }}」</span></button>
            <button
                class="pb-btn pb-btn--ghost pb-btn--sm"
                :class="{'conflict-resolve__active': choices[key] === 'server'}"
                @click="choices[key] = 'server'"
            >服务端<span class="pb-mono pb-xs">「{{ masked(key, rawVal(conflict.theirs, key)) }}」</span></button>
            <button
                class="pb-btn pb-btn--ghost pb-btn--sm"
                :class="{'conflict-resolve__active': choices[key] === 'manual'}"
                @click="choices[key] = 'manual'"
            >手动</button>
          </div>
          <input
              v-if="choices[key] === 'manual'"
              v-model="manualVals[key]"
              class="pb-input pb-input--mono conflict-resolve__input"
              :placeholder="key"
              spellcheck="false"
          />
        </div>
      </div>

      <!-- 操作 -->
      <div class="conflict-actions">
        <span class="pb-fill"></span>
        <button
            class="pb-btn pb-btn--danger"
            :disabled="busy"
            @click="resolve"
        >
          <span v-if="busy" class="pb-spinner pb-spinner--sm"></span>
          <span>确认解决并同步</span>
        </button>
      </div>
      <p v-if="conflictKeys.length" class="conflict-hint">未列出的字段已按「仅一方修改则采用修改方」自动合并。</p>
    </template>
  </div>
</template>

<style scoped>
.conflict {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 16px;
  gap: 14px;
}

.conflict-head {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-shrink: 0;
}
.conflict-head__title {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
}
.conflict-head__icon {
  font-size: 22px;
}
.conflict-head h2 {
  font-size: 19px;
  font-weight: 700;
}

.conflict-loading {
  flex: 1;
}

.conflict-cols {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 14px;
  overflow-y: auto;
  padding-bottom: 2px;
}

.conflict-col {
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.conflict-col__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px 10px;
  flex-shrink: 0;
}
.conflict-col__body {
  flex: 1;
  overflow-y: auto;
  padding: 0 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.conflict-col__title {
  font-size: 15px;
  font-weight: 700;
  padding: 6px 6px 10px;
  border-bottom: 1px solid var(--glass-border);
  margin-bottom: 4px;
}
.conflict-col__title--diff {
  border-bottom-color: color-mix(in srgb, var(--warning) 50%, transparent);
}
.conflict-col__field {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 7px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid transparent;
  font-size: 12.5px;
  word-break: break-all;
}
.conflict-col__key {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-3);
}
.conflict-col__field--diff {
  border-color: color-mix(in srgb, var(--warning) 45%, transparent);
  background: var(--warning-soft);
}
.conflict-col__field--diff .conflict-col__key {
  color: var(--warning);
}

/* 逐字段解决区 */
.conflict-resolve {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  flex-shrink: 0;
  max-height: 40vh;
  overflow-y: auto;
}
.conflict-resolve__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 14px;
}
.conflict-resolve__quick {
  display: flex;
  gap: 8px;
}
.conflict-resolve__row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.conflict-resolve__key {
  font-size: 12.5px;
  font-weight: 700;
  min-width: 90px;
  color: var(--text-2);
  word-break: break-all;
}
.conflict-resolve__opts {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.conflict-resolve__active {
  border-color: var(--accent) !important;
  box-shadow: 0 0 0 2px var(--accent-soft);
}
.conflict-resolve__input {
  flex: 1;
  min-width: 160px;
  margin-left: 6px;
}

.conflict-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
  flex-wrap: wrap;
}
.conflict-hint {
  font-size: 12.5px;
  color: var(--text-3);
  flex-shrink: 0;
}
</style>
