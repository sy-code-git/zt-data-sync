<script setup>
// 编辑页（方案 J 版）：
// - 新建态：归属级联下拉（项目 › 环境，自绘 PbSelect）+ 类型下拉 + ip 上下文预填
// - 编辑态：只读归属链
// - 保存后自动定位到新条目所在环境/IP（方案 J 列表页状态联动）
import {ref, computed, onMounted} from 'vue'
import {useAppStore} from '../store'
import {api} from '../api'
import PbSelect from '../components/PbSelect.vue'
import PasswordGenerator from '../components/PasswordGenerator.vue'

const store = useAppStore()

const isNew = computed(() => !store.editing?.id || store.editing?._new)
const form = ref({
  title: '',
  type: 'account',
  group_id: '',
  parent_id: null,
  fields: {},
  custom_fields: {},
})

const saving = ref(false)
const error = ref('')

// account 固定模板字段（§4 UI 约定）
const accountTemplate = ['username', 'password', 'ip', 'port', 'remark']

const typeMeta = {
  project: {icon: '🗂', label: '项目'},
  env: {icon: '🌐', label: '环境'},
  ip_type: {icon: '🖥', label: 'IP 类型'},
  acc_type: {icon: '👤', label: '账号类型'},
  account: {icon: '🔑', label: '账号'},
  custom: {icon: '📎', label: '补充卡片'},
}

const showGen = ref(false)

/* ---- 归属状态（新建级联选择） ---- */
const parentProj = ref('') // 项目 id
const parentEnv = ref('') // 环境 id

const entries = computed(() => store.entries.filter((e) => !e.deleted))
const projects = computed(() => entries.value.filter((e) => e.type === 'project'))
const envsOf = (pid) => entries.value.filter((e) => e.type === 'env' && e.parent_id === pid)

const projOptions = computed(() => projects.value.map((p) => ({value: p.id, label: p.title})))
const envOptions = computed(() => envsOf(parentProj.value).map((e) => ({value: e.id, label: e.title})))
const typeOptions = computed(() => Object.entries(typeMeta).map(([k, m]) => ({value: k, label: m.label, icon: m.icon})))

// 新建时切换项目：环境重置为该项目第一个
function onProjChange(pid) {
  parentProj.value = pid
  const first = envsOf(pid)[0]
  parentEnv.value = first ? first.id : ''
}

// 归属链展示（编辑态只读）：idIndex 直查，避免逐层全表 find
const idIndex = computed(() => {
  const m = new Map()
  for (const e of entries.value) m.set(e.id, e)
  return m
})
function pathOf(e) {
  const path = []
  let p = e?.parent_id
  while (p) {
    const n = idIndex.value.get(p)
    if (!n) break
    path.unshift(n)
    p = n.parent_id
  }
  return path
}
const editPath = computed(() => pathOf(store.editing))

onMounted(() => {
  const ctx = store.newCtx || {}
  if (store.editing && !isNew.value) {
    const e = store.editing
    form.value = {
      title: e.title,
      type: e.type,
      group_id: e.group_id,
      parent_id: e.parent_id || null,
      fields: {...(e.fields || {})},
      custom_fields: {...(e.custom_fields || {})},
    }
    const path = pathOf(e)
    parentProj.value = path.find((n) => n.type === 'project')?.id || ''
    parentEnv.value = path.find((n) => n.type === 'env')?.id || ''
  } else {
    // 新建：优先用上下文（方案 J 入口带归属与预填），否则回退当前浏览位置
    const projId = ctx.parentProjId || store.pjProject || (projects.value[0]?.id ?? '')
    parentProj.value = projId
    const envList = envsOf(projId)
    parentEnv.value = ctx.parentEnvId || (envList[0]?.id ?? '')
    form.value = {
      title: '',
      type: ctx.type || 'account',
      // 组回退链：上下文环境组 → 同步状态组 → 任意已存在条目的组
      group_id: (ctx.parentEnvId && entries.value.find((e) => e.id === ctx.parentEnvId)?.group_id)
        || store.status.groups?.[0]?.id
        || entries.value.find((e) => e.group_id)?.group_id
        || '',
      parent_id: null,
      fields: {},
      custom_fields: {},
    }
    if (form.value.type === 'account') {
      form.value.fields = {
        username: '',
        password: '',
        ip: ctx.prefillIp || '',
        port: '',
      }
    }
  }
})

/* ---- 表单字段操作 ---- */
const fieldKeys = computed(() => {
  if (form.value.type === 'account') {
    const extra = Object.keys(form.value.fields || {}).filter((k) => !accountTemplate.includes(k))
    return [...accountTemplate, ...extra]
  }
  return Object.keys(form.value.fields || {})
})

async function addField() {
  if (!form.value.fields) form.value.fields = {}
  let n = 1
  while (form.value.fields[`field_${n}`]) n++
  form.value.fields[`field_${n}`] = ''
}

function removeField(key) {
  // 对齐原型：删除字段需弹窗确认，防误触丢值
  fieldConfirm.value = {kind: 'field', key}
}

async function addCustom() {
  if (!form.value.custom_fields) form.value.custom_fields = {}
  let n = 1
  while (form.value.custom_fields[`custom_${n}`]) n++
  form.value.custom_fields[`custom_${n}`] = ''
}

function removeCustom(key) {
  fieldConfirm.value = {kind: 'custom', key}
}

// 字段删除确认弹窗状态（field=字段 / custom=自定义字段）
const fieldConfirm = ref(null)

function confirmRemoveField() {
  const c = fieldConfirm.value
  if (!c) return
  const src = c.kind === 'field' ? form.value.fields : form.value.custom_fields
  const f = {...src}
  delete f[c.key]
  if (c.kind === 'field') form.value.fields = f
  else form.value.custom_fields = f
  fieldConfirm.value = null
}

/* ---- 保存 ---- */
// 保存后定位：account → 选中环境（并尽量定位到其 IP）；env → 项目本身；project → 切到该项目
function locateAfterSave(savedType) {
  const ctx = store.newCtx || {}
  store.newCtx = null
  if (savedType === 'account' && parentEnv.value) {
    store.pjFocus = parentEnv.value
    store.pjIp = ctx.prefillIp || form.value.fields?.ip || ''
  } else if (savedType === 'env') {
    if (parentProj.value) store.pjProject = parentProj.value
    store.pjFocus = ''
    store.pjIp = ''
  } else if (savedType === 'project') {
    store.pjProject = ''
    store.pjFocus = ''
    store.pjIp = ''
  }
  store.goto('list')
}

async function save() {
  error.value = ''
  if (!form.value.title.trim()) {
    error.value = '标题不能为空'
    return
  }
  // 新建时必须有归属（account→环境；env→项目；project→根）
  if (isNew.value) {
    if (form.value.type === 'account' && !parentEnv.value) {
      error.value = '请先在归属中选择项目与环境（无环境可先创建环境）'
      return
    }
    if (form.value.type === 'env' && !parentProj.value) {
      error.value = '缺少项目归属（无项目可先创建项目）'
      return
    }
  }
  if (!form.value.group_id) {
    // 本地同步状态中没有可用组：新成员尚未被管理员加入任何组，或入组后还没同步。
    // （连接正常也会走到这里，故不再提示"检查服务端连接"，避免误导排查方向）
    error.value = '尚无可写入的组：请先让管理员把你加入某个组，然后在工具栏点「⟳ 同步」拉取组信息后重试'
    return
  }
  saving.value = true
  try {
    // 新建时推导 parent_id：account/custom→环境；env→项目；project→根
    let parentId = form.value.parent_id || null
    if (isNew.value) {
      if (form.value.type === 'project') parentId = null
      else if (form.value.type === 'env') parentId = parentProj.value || null
      else parentId = parentEnv.value || parentProj.value || null
    }
    const req = {
      id: isNew.value ? '' : store.editing.id,
      group_id: form.value.group_id,
      type: form.value.type,
      title: form.value.title.trim(),
      parent_id: parentId,
      fields: cleanFields(form.value.fields),
      custom_fields: cleanFields(form.value.custom_fields),
    }
    await api.PutEntry(req)
    store.toast('已保存并待推送同步', 'success')
    await store.refreshEntries()
    locateAfterSave(req.type)
  } catch (e) {
    error.value = String(e.message || e)
  } finally {
    saving.value = false
  }
}

function cleanFields(obj) {
  const out = {}
  for (const k of Object.keys(obj || {})) {
    if (obj[k] !== undefined && obj[k] !== null && String(obj[k]) !== '') {
      out[k] = typeof obj[k] === 'string' ? JSON.stringify(obj[k]) : obj[k]
    }
  }
  return out
}

function fillPassword(pw) {
  if (!form.value.fields) form.value.fields = {}
  form.value.fields.password = pw
  showGen.value = false
}
</script>

<template>
  <div class="edit-view">
    <div class="pb-glass pb-glass--strong edit-card">
      <div class="edit-head">
        <button class="pb-iconbtn" title="返回" @click="store.goto('list')">←</button>
        <h2>{{ isNew ? `新建${typeMeta[form.type]?.label || ''}` : '编辑条目' }}</h2>
        <span class="pb-fill"></span>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('list')">取消</button>
        <button class="pb-btn pb-btn--primary pb-btn--sm" :disabled="saving" @click="save">
          <span v-if="saving" class="pb-spinner pb-spinner--sm"></span>
          <span>保存</span>
        </button>
      </div>

      <hr class="pb-divider"/>

      <div class="edit-body">
        <div v-if="error" class="edit-error">{{ error }}</div>

        <!-- 归属：新建 = 级联下拉；编辑 = 只读链 -->
        <div v-if="isNew" class="edit-parent">
          <span class="pb-label" style="margin: 0">归属</span>
          <div style="min-width: 160px">
            <PbSelect
                v-model="parentProj"
                :options="projOptions"
                placeholder="选择项目"
                @change="onProjChange"
            />
          </div>
          <template v-if="form.type !== 'project'">
            <span style="color: var(--text-3)">›</span>
            <div v-if="form.type !== 'env'" style="min-width: 160px">
              <PbSelect
                  v-if="envOptions.length"
                  v-model="parentEnv"
                  :options="envOptions"
                  hint="账号将保存在此环境下"
              />
              <span v-else class="pb-xs pb-muted" style="padding: 8px 12px; border: 1px dashed var(--glass-border); border-radius: var(--radius-sm)">
                该项目暂无环境
              </span>
            </div>
          </template>
        </div>
        <div v-else class="edit-parent__chain">
          归属：<b>{{ editPath.map((n) => n.title).join(' › ') || '—（根级）' }}</b>
        </div>

        <div class="pb-field">
          <label class="pb-label">标题</label>
          <input v-model="form.title" class="pb-input" placeholder="如：生产环境 / root 账号" autofocus/>
        </div>

        <div class="pb-field">
          <label class="pb-label">类型</label>
          <PbSelect
              v-model="form.type"
              :options="typeOptions"
              :disabled="!isNew"
              :hint="isNew ? '常用：🔑 账号 / 🌐 环境' : ''"
          />
          <p v-if="!isNew" class="pb-xs pb-muted">类型创建后不可变更</p>
        </div>

        <!-- account：固定模板字段（§4） -->
        <template v-if="form.type === 'account'">
          <p v-if="isNew && store.newCtx?.prefillIp" class="pb-xs pb-muted">已按当前 IP（{{ store.newCtx.prefillIp }}）预填</p>
          <div v-for="key in fieldKeys" :key="key" class="pb-field">
            <label class="pb-label">{{ key }}</label>
            <div class="pb-input-group">
              <input
                  v-model="form.fields[key]"
                  class="pb-input pb-input--mono"
                  :type="key === 'password' ? 'password' : 'text'"
                  :placeholder="key === 'password' ? '••••••••' : (key === 'ip' ? '所属服务器 IP' : key)"
                  autocomplete="off"
              />
              <button
                  v-if="key === 'password'"
                  class="pb-input-group__action"
                  title="生成密码"
                  @click="showGen = true"
              >⚡</button>
              <!-- 核心字段 username/password/ip 不可删除（IP 是方案 J 聚合维度，删掉会丢聚合入口）；port/remark 等可选字段可删 -->
              <button
                  v-if="!['username', 'password', 'ip'].includes(key)"
                  class="pb-input-group__action"
                  title="删除字段"
                  @click="removeField(key)"
              >✕</button>
            </div>
          </div>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="addField">＋ 添加字段</button>
        </template>

        <!-- custom：通用动态表单 -->
        <template v-else-if="form.type === 'custom'">
          <div v-for="(val, key) in form.custom_fields" :key="key" class="pb-field">
            <label class="pb-label">{{ key }}</label>
            <div class="pb-input-group">
              <input v-model="form.custom_fields[key]" class="pb-input pb-input--mono" placeholder="值"/>
              <button class="pb-input-group__action" title="删除" @click="removeCustom(key)">✕</button>
            </div>
          </div>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="addCustom">＋ 添加自定义字段</button>
        </template>

        <!-- 其他节点：title 即信息 -->
        <div v-else class="edit-note">
          {{ typeMeta[form.type]?.label }} 节点只需填写标题，用于组织树形结构
        </div>
      </div>
    </div>

    <!-- 密码生成器弹窗 -->
    <PasswordGenerator
        v-if="showGen"
        @generate="fillPassword"
        @close="showGen = false"
    />

    <!-- 删除字段确认弹窗（对齐原型 removeField 确认） -->
    <div v-if="fieldConfirm" class="pb-modal-mask" @click.self="fieldConfirm = null">
      <div class="pb-modal pb-glass pb-glass--strong" role="dialog" aria-modal="true">
        <div class="pb-modal__head">
          <span class="pb-modal__title">删除字段</span>
          <button class="pb-iconbtn" @click="fieldConfirm = null">✕</button>
        </div>
        <div class="pb-modal__body">
          <p class="pb-sm">确定删除字段「{{ fieldConfirm.key }}」吗？保存后生效且不可撤销。</p>
        </div>
        <div class="pb-modal__foot">
          <button class="pb-btn pb-btn--ghost" @click="fieldConfirm = null">取消</button>
          <button class="pb-btn pb-btn--danger" @click="confirmRemoveField">确定删除</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.edit-view {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 24px;
  display: flex;
  justify-content: center;
}

.edit-card {
  width: 100%;
  max-width: 560px;
  height: fit-content;
  display: flex;
  flex-direction: column;
  padding: 20px 24px;
  gap: 14px;
  animation: pb-pop-in 0.28s var(--ease);
}

.edit-head {
  display: flex;
  align-items: center;
  gap: 10px;
}
.edit-head h2 {
  font-size: 17px;
  font-weight: 700;
}

.edit-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.edit-error {
  padding: 10px 12px;
  border-radius: var(--radius-md);
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 13px;
}

.edit-note {
  padding: 16px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px dashed var(--glass-border-strong);
  text-align: center;
  color: var(--text-2);
  font-size: 13.5px;
}
</style>
