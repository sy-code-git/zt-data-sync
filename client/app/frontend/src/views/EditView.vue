<script setup>
import {ref, computed, onMounted, nextTick} from 'vue'
import {useAppStore} from '../store'
import {api} from '../api'
import PasswordGenerator from '../components/PasswordGenerator.vue'

const store = useAppStore()

// 编辑上下文：store.editing 为条目；若为 null 则是新建
const isNew = computed(() => !store.editing?.id || store.editing._new)
const form = ref({
  title: '',
  type: 'account',
  group_id: '',
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

onMounted(() => {
  if (store.editing && !isNew.value) {
    form.value = {
      title: store.editing.title,
      type: store.editing.type,
      group_id: store.editing.group_id,
      parent_id: store.editing.parent_id || null, // 保留挂载位置，编辑后不可掉到根级
      fields: {...(store.editing.fields || {})},
      custom_fields: {...(store.editing.custom_fields || {})},
    }
  } else {
    // 新建：默认挂在当前选中条目下（若有）；无 id 的上下文（{_new:true}）视为新建根项目
    const parent = store.editing?.id ? store.editing : null
    const parentType = parent?.type
    // 五层骨架推进：project→env→ip_type→acc_type→account；custom 旁挂四层（§5.1）
    const nextType = parent ? nextTypeOf(parentType) : 'project'
    form.value = {
      title: '',
      type: nextType,
      // 组回退链：父条目组 → 同步状态组 → 任意已存在条目的组
      group_id: parent?.group_id || store.status.groups?.[0]?.id || store.entries.find((e) => e.group_id)?.group_id || '',
      fields: {},
      custom_fields: {},
    }
    if (parent) {
      form.value.parent_id = parent.id
    }
    if (nextType === 'account') {
      form.value.fields = {username: '', password: '', ip: '', port: ''}
    }
  }
})

// 可选五层模板：推荐子类型（不强制定层/跳层；仅用于新建时默认类型推荐）
function childTypesOf(t) {
  switch (t) {
    case 'project': return ['env', 'custom']
    case 'env': return ['ip_type', 'custom']
    case 'ip_type': return ['acc_type', 'custom']
    case 'acc_type': return ['account', 'custom']
    default: return [] // account / custom / 无父（顶层）
  }
}

function nextTypeOf(t) {
  const list = childTypesOf(t)
  return list[0] || 'project'
}

// 所属组选项（同步状态里的可用组；组名缺失时回退组 id 前缀展示）
const groupOptions = computed(() =>
  (store.status.groups || []).map((g) => ({id: g.id, label: g.name || g.id.slice(0, 8)}))
)

// 自由模式：所有类型可选（五层仅作推荐，不强制定层；编辑态下拉 disabled 仅展示当前类型）
const typeOptions = computed(() => Object.keys(typeMeta))

const fieldKeys = computed(() => {
  if (form.value.type === 'account') {
    // 固定模板字段 + 用户动态添加的字段（§4 UI 约定）
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
  await nextTick()
}

function removeField(key) {
  const f = {...form.value.fields}
  delete f[key]
  form.value.fields = f
}

async function addCustom() {
  if (!form.value.custom_fields) form.value.custom_fields = {}
  let n = 1
  while (form.value.custom_fields[`custom_${n}`]) n++
  form.value.custom_fields[`custom_${n}`] = ''
  await nextTick()
}

function removeCustom(key) {
  const f = {...form.value.custom_fields}
  delete f[key]
  form.value.custom_fields = f
}

async function save() {
  error.value = ''
  if (!form.value.title.trim()) {
    error.value = '标题不能为空'
    return
  }
  if (!form.value.group_id) {
    error.value = '缺少组 ID（请在列表中选择条目后新建子项）'
    return
  }
  saving.value = true
  try {
    const req = {
      id: isNew.value ? '' : store.editing.id,
      group_id: form.value.group_id,
      type: form.value.type,
      title: form.value.title.trim(),
      parent_id: form.value.parent_id || null,
      fields: cleanFields(form.value.fields),
      custom_fields: cleanFields(form.value.custom_fields),
    }
    await api.PutEntry(req)
    store.toast('已保存并待推送同步', 'success')
    await store.refreshEntries()
    store.goto('list')
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

// 组 id → 组名（编辑态只读展示用）
function groupLabel(gid) {
  const g = (store.status.groups || []).find((x) => x.id === gid)
  return g ? (g.name || g.id) : (gid || '')
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

        <div class="pb-field">
          <label class="pb-label">标题</label>
          <input v-model="form.title" class="pb-input" placeholder="如：生产环境 / root 账号" autofocus/>
        </div>

        <div class="pb-field">
          <label class="pb-label">类型</label>
          <select v-model="form.type" class="pb-input" :disabled="!isNew" style="appearance: auto; cursor: pointer;">
            <option v-for="t in typeOptions" :key="t" :value="t">{{ typeMeta[t].icon }} {{ typeMeta[t].label }}</option>
          </select>
          <p v-if="!isNew" class="pb-xs pb-muted">类型创建后不可变更</p>
        </div>

        <!-- 所属组（新建可切换；编辑态只读展示当前组） -->
        <div v-if="isNew && groupOptions.length" class="pb-field">
          <label class="pb-label">所属组</label>
          <select v-model="form.group_id" class="pb-input" style="appearance: auto; cursor: pointer;">
            <option v-for="g in groupOptions" :key="g.id" :value="g.id">{{ g.label }}</option>
          </select>
        </div>
        <div v-else-if="!isNew && form.group_id" class="pb-field">
          <label class="pb-label">所属组</label>
          <input class="pb-input" :value="groupLabel(form.group_id)" disabled />
        </div>

        <!-- account：固定模板字段（§4） -->
        <template v-if="form.type === 'account'">
          <div v-for="key in fieldKeys" :key="key" class="pb-field">
            <label class="pb-label">{{ key }}</label>
            <div class="pb-input-group">
              <input
                  v-model="form.fields[key]"
                  class="pb-input pb-input--mono"
                  :type="key === 'password' ? 'password' : 'text'"
                  :placeholder="key === 'password' ? '••••••••' : key"
                  autocomplete="off"
              />
              <button
                  v-if="key === 'password'"
                  class="pb-input-group__action"
                  title="生成密码"
                  @click="showGen = true"
              >⚡</button>
              <button
                  v-if="key !== 'username' && key !== 'password'"
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
