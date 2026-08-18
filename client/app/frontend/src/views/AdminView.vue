<script setup>
import {ref, computed, onMounted} from 'vue'
import {api} from '../api'
import {useAppStore} from '../store'

const store = useAppStore()

// ---- 状态 ----
const groups = ref([])
const users = ref([])
const busy = ref(false)
const tip = ref('')
const tipType = ref('info')

// 建组
const newGroupName = ref('')
// 开户
const newUser = ref({username: '', name: '', publicKey: ''})
// 加成员
const addMember = ref({groupID: '', userID: ''})
// 显示管理员开关（默认关闭：成员列表不显示管理员账号）
const showAdmins = ref(false)
// 待确认删除的组 id（两步确认）
const confirmDeleteGroupID = ref('')
// 选中的组 + 组内成员（含在线状态）
const selectedGroup = ref(null)
const members = ref([])
// 待确认移除的成员 user_id（两步确认）
const confirmRemoveMemberID = ref('')
// 设备（主机信息）面板
const showDevices = ref(false)
const devices = ref([])
const devicesBusy = ref(false)

// 过滤后的成员列表（默认隐藏管理员）
const visibleUsers = computed(() => {
  if (showAdmins.value) return users.value
  return users.value.filter((u) => u.role !== 'admin')
})

async function refresh() {
  try {
    const [g, u] = await Promise.all([api.AdminListGroups(), api.AdminListUsers()])
    groups.value = g || []
    users.value = u || []
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  }
}

onMounted(refresh)

// 选中组 → 加载该组成员列表（含在线状态）
async function doSelectGroup(g) {
  if (selectedGroup.value && selectedGroup.value.id === g.id) {
    selectedGroup.value = null
    members.value = []
    return
  }
  selectedGroup.value = g
  try {
    members.value = await api.AdminListMembers(g.id)
  } catch (e) {
    flash(String(e.message || e), 'err')
  }
}

function flash(text, type = 'ok') {
  tip.value = text
  tipType.value = type
}

async function doCreateGroup() {
  if (!newGroupName.value.trim()) {
    flash('请输入组名', 'err')
    return
  }
  busy.value = true
  try {
    const gid = await api.AdminCreateGroup(newGroupName.value.trim())
    newGroupName.value = ''
    flash(`组已创建（${gid}）`)
    await refresh()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

async function doCreateUser() {
  const u = newUser.value
  if (!u.username.trim() || !u.name.trim() || !u.publicKey.trim()) {
    flash('请填写工号、显示名、公钥', 'err')
    return
  }
  busy.value = true
  try {
    const uid = await api.AdminCreateUser(u.username.trim(), u.name.trim(), u.publicKey.trim())
    newUser.value = {username: '', name: '', publicKey: ''}
    flash(`用户已开户（工号 ${u.username.trim()}，user_id ${uid}）`)
    await refresh()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

async function doAddMember() {
  const m = addMember.value
  if (!m.groupID || !m.userID) {
    flash('请选择组和成员', 'err')
    return
  }
  busy.value = true
  try {
    await api.AdminAddMember(m.groupID, m.userID)
    flash('已加入成员')
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

// 删除组（归档）：两步确认，服务端要求组名二次确认
async function doArchiveGroup(g) {
  if (confirmDeleteGroupID.value !== g.id) {
    confirmDeleteGroupID.value = g.id
    flash(`再次点击确认删除组「${g.name}」`, 'info')
    return
  }
  confirmDeleteGroupID.value = ''
  busy.value = true
  try {
    await api.AdminArchiveGroup(g.id, g.name)
    flash(`组「${g.name}」已删除`)
    await refresh()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

// 恢复归档组
async function doUnarchiveGroup(g) {
  busy.value = true
  try {
    await api.AdminUnarchiveGroup(g.id)
    flash(`组「${g.name}」已恢复`)
    await refresh()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

// 移除组内成员（两步确认，服务端要求成员名二次确认）
async function doRemoveMember(m) {
  if (!selectedGroup.value) return
  if (confirmRemoveMemberID.value !== m.user_id) {
    confirmRemoveMemberID.value = m.user_id
    flash(`再次点击确认移除成员「${m.name}」`, 'info')
    return
  }
  confirmRemoveMemberID.value = ''
  busy.value = true
  try {
    await api.AdminRemoveMember(selectedGroup.value.id, m.user_id, m.name)
    flash(`成员「${m.name}」已移出组「${selectedGroup.value.name}」`)
    members.value = await api.AdminListMembers(selectedGroup.value.id)
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

// 设备（主机信息）面板：加载/切换
async function toggleDevices() {
  showDevices.value = !showDevices.value
  if (showDevices.value && !devices.value.length) {
    await loadDevices()
  }
}

async function loadDevices() {
  devicesBusy.value = true
  try {
    devices.value = await api.AdminListDevices()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    devicesBusy.value = false
  }
}

// 最后在线时间格式化
function fmtTime(ts) {
  if (!ts) return '从未'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
</script>

<template>
  <div class="admin">
    <div class="admin__head">
      <h2>管理面板</h2>
      <div class="admin__head-actions">
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="toggleDevices">
          {{ showDevices ? '收起设备' : `设备（${devices.length}）` }}
        </button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="refresh">⟳ 刷新</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('list')">← 返回</button>
      </div>
    </div>

    <div v-if="tip" class="admin__tip" :class="`admin__tip--${tipType}`">{{ tip }}</div>

    <div class="admin__grid">
      <!-- 组管理 -->
      <div class="pb-glass admin__panel">
        <div class="admin__panel-title">组</div>
        <div class="admin__input-row">
          <input v-model="newGroupName" class="pb-input" placeholder="新组名" @keyup.enter="doCreateGroup" />
          <button class="pb-btn pb-btn--primary pb-btn--sm" :disabled="busy" @click="doCreateGroup">建组</button>
        </div>
        <div class="admin__list">
          <div v-for="g in groups" :key="g.id" class="admin__item">
            <div class="admin__item-main">
              <span class="admin__item-title">
                {{ g.name }}
                <span v-if="g.archived" class="pb-badge pb-badge--neutral">已归档</span>
              </span>
              <span class="pb-xs pb-muted">{{ g.id }}</span>
            </div>
            <div class="admin__item-actions">
              <button v-if="!g.archived" class="pb-btn pb-btn--ghost pb-btn--sm" @click="doSelectGroup(g)">
                {{ selectedGroup && selectedGroup.id === g.id ? '收起成员' : '成员' }}
              </button>
              <button v-if="!g.archived" class="pb-btn pb-btn--ghost pb-btn--sm" @click="addMember.groupID = g.id">
                {{ addMember.groupID === g.id ? '已选' : '加成员' }}
              </button>
              <button v-if="!g.archived" class="pb-btn pb-btn--ghost pb-btn--sm pb-btn--danger" @click="doArchiveGroup(g)">
                {{ confirmDeleteGroupID === g.id ? '确认删除' : '删除' }}
              </button>
              <button v-else class="pb-btn pb-btn--ghost pb-btn--sm" @click="doUnarchiveGroup(g)">恢复</button>
            </div>
          </div>
          <p v-if="!groups.length" class="pb-xs pb-muted">暂无组</p>

          <!-- 组内成员（选中组后展示，含在线状态） -->
          <div v-if="selectedGroup" class="admin__members">
            <div class="admin__panel-title">组「{{ selectedGroup.name }}」成员（{{ members.length }}）</div>
            <div v-for="m in members" :key="m.user_id" class="admin__item">
              <div class="admin__item-main">
                <span class="admin__item-title">
                  <span class="pb-dot" :class="m.online ? 'pb-dot--ok' : 'pb-dot--idle'"></span>
                  {{ m.name }}
                </span>
                <span class="pb-xs pb-muted">
                  设备 {{ (m.devices || []).length }} 台
                  <template v-if="(m.devices || []).length">
                    · <span v-for="(d, i) in m.devices" :key="d.device_id">{{ i ? ' / ' : '' }}{{ d.hostname || d.name || '未知主机' }}（{{ d.ip || '无 IP' }}）</span>
                  </template>
                </span>
              </div>
              <div class="admin__item-actions">
                <span class="pb-badge" :class="m.online ? 'pb-badge--success' : 'pb-badge--neutral'">
                  {{ m.online ? '在线' : '离线' }}
                </span>
                <button
                    class="pb-btn pb-btn--ghost pb-btn--sm pb-btn--danger"
                    :disabled="busy"
                    @click="doRemoveMember(m)"
                >
                  {{ confirmRemoveMemberID === m.user_id ? '确认移除' : '移除' }}
                </button>
              </div>
            </div>
            <p v-if="!members.length" class="pb-xs pb-muted">该组暂无成员</p>
          </div>
        </div>
      </div>

      <!-- 用户/开户 -->
      <div class="pb-glass admin__panel">
        <div class="admin__panel-title">成员开户</div>
        <div class="admin__form">
          <input v-model="newUser.username" class="pb-input pb-input--mono" placeholder="工号（唯一、不可改）" />
          <input v-model="newUser.name" class="pb-input" placeholder="显示名" />
          <textarea v-model="newUser.publicKey" class="pb-input pb-input--mono admin__textarea" placeholder="公钥（base64，成员客户端生成后复制给你）"></textarea>
          <button class="pb-btn pb-btn--primary pb-btn--block" :disabled="busy" @click="doCreateUser">开户</button>
        </div>

        <div class="admin__panel-title" style="margin-top: 16px">
          <span>成员列表</span>
          <label class="admin__switch">
            <input type="checkbox" v-model="showAdmins" />
            <span class="pb-xs pb-muted">显示管理员</span>
          </label>
        </div>
        <div class="admin__list">
          <div v-for="u in visibleUsers" :key="u.user_id" class="admin__item">
            <div class="admin__item-main">
              <span class="admin__item-title">
                {{ u.name }}
                <span v-if="u.role === 'admin'" class="pb-badge pb-badge--accent">管理员</span>
              </span>
              <span class="pb-xs pb-muted">工号 {{ u.username || '（未设）' }}</span>
            </div>
            <button
                class="pb-btn pb-btn--ghost pb-btn--sm"
                @click="addMember.userID = u.user_id"
            >
              {{ addMember.userID === u.user_id ? '已选' : '选为成员' }}
            </button>
          </div>
          <p v-if="!visibleUsers.length" class="pb-xs pb-muted">暂无成员</p>
        </div>
      </div>
    </div>

    <!-- 加成员操作栏 -->
    <div class="pb-glass admin__addmember" v-if="addMember.groupID || addMember.userID">
      <span class="pb-xs">
        已选：组 <b>{{ addMember.groupID || '—' }}</b> · 成员 <b>{{ addMember.userID || '—' }}</b>
      </span>
      <button class="pb-btn pb-btn--primary pb-btn--sm" :disabled="busy || !addMember.groupID || !addMember.userID" @click="doAddMember">
        确认加入
      </button>
    </div>

    <!-- 设备（主机信息）面板 -->
    <div v-if="showDevices" class="pb-glass admin__panel">
      <div class="admin__panel-title">设备 / 主机信息（{{ devices.length }}）</div>
      <div class="admin__device-list">
        <div v-for="d in devices" :key="d.device_id" class="admin__device">
          <div class="admin__item-main">
            <span class="admin__item-title">
              <span class="pb-dot" :class="d.online ? 'pb-dot--ok' : 'pb-dot--idle'"></span>
              {{ d.name || '未命名设备' }}
              <span v-if="d.status === 'disabled'" class="pb-badge pb-badge--neutral">已禁用</span>
            </span>
            <span class="pb-xs pb-muted">
              用户：{{ d.user_name || '—' }} · 主机名：{{ d.hostname || '—' }}
            </span>
            <span class="pb-xs pb-muted">
              IP：{{ d.ip || '—' }} · 最后在线：{{ fmtTime(d.last_seen) }}
            </span>
          </div>
          <span class="pb-badge" :class="d.online ? 'pb-badge--success' : 'pb-badge--neutral'">
            {{ d.online ? '在线' : '离线' }}
          </span>
        </div>
        <p v-if="devicesBusy" class="pb-xs pb-muted">加载中…</p>
        <p v-else-if="!devices.length" class="pb-xs pb-muted">暂无设备</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.admin__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.admin__head h2 { font-size: 20px; font-weight: 700; }
.admin__head-actions { display: flex; gap: 8px; }

.admin__tip {
  font-size: 13px;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  line-height: 1.6;
  word-break: break-all;
}
.admin__tip--info { background: var(--accent-soft); color: var(--accent); }
.admin__tip--ok { background: var(--success-soft); color: var(--success); }
.admin__tip--err { background: var(--danger-soft); color: var(--danger); }

.admin__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
@media (max-width: 900px) { .admin__grid { grid-template-columns: 1fr; } }

.admin__panel {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.admin__panel-title { font-size: 14px; font-weight: 600; color: var(--text-2); }

.admin__input-row { display: flex; gap: 8px; }
.admin__input-row .pb-input { flex: 1; }

.admin__form { display: flex; flex-direction: column; gap: 8px; }
.admin__textarea { min-height: 72px; resize: vertical; font-size: 12px; }

.admin__list { display: flex; flex-direction: column; gap: 8px; }
.admin__members {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
  padding: 10px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
}
.admin__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
}
.admin__item-main { display: flex; flex-direction: column; min-width: 0; }
.admin__item-title { font-size: 13px; font-weight: 600; display: flex; align-items: center; gap: 6px; }
.admin__item-actions { display: flex; align-items: center; gap: 6px; flex-shrink: 0; }

.admin__switch { display: flex; align-items: center; gap: 6px; cursor: pointer; }
.admin__switch input { cursor: pointer; }

.admin__addmember {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
}

.admin__device-list { display: flex; flex-direction: column; gap: 8px; }
.admin__device {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
}
</style>
