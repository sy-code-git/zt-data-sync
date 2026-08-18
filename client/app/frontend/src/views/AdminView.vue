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
// 待确认吊销的成员 user_id（两步确认）
const confirmRevokeUserID = ref('')
// 方案 C：邀请码 + 注册申请审核（§6.3）
const inviteUsername = ref('')
const inviteAutoApprove = ref(false)
const inviteTTLDays = ref(3)
const invites = ref([])
const regRequests = ref([])
const regReviewName = ref('')
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
  loadInvites()
  loadRegisterRequests()
}

// 邀请码：生成 / 列表
async function doCreateInvite() {
  if (!inviteUsername.value.trim()) {
    flash('请输入工号', 'err')
    return
  }
  busy.value = true
  try {
    const inv = await api.AdminCreateInvite(inviteUsername.value.trim(), inviteAutoApprove.value, inviteTTLDays.value || 0)
    inviteUsername.value = ''
    flash(`已生成邀请码 ${inv.code}${inv.auto_approve ? '（免审核）' : ''}，有效期至 ${new Date(inv.expires_at * 1000).toLocaleDateString()}`)
    loadInvites()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

async function loadInvites() {
  try {
    invites.value = (await api.AdminListInvites()) || []
  } catch (e) {
    // 忽略（老服务端无此接口时静默）
  }
}

// 注册申请：列表 / 通过（开户）/ 拒绝
async function loadRegisterRequests() {
  try {
    regRequests.value = (await api.AdminListRegisterRequests('pending')) || []
  } catch (e) {
    // 忽略
  }
}

async function doApproveRequest(rq) {
  const name = (regReviewName.value || '').trim() || rq.username
  if (!confirm(`通过申请并开户「${rq.username}」？（显示名：${name}；公钥/设备名/IP 已核对）`)) return
  busy.value = true
  try {
    await api.AdminApproveRegisterRequest(rq.id, name)
    flash(`已开户「${rq.username}」（显示名 ${name}）`)
    regReviewName.value = ''
    loadRegisterRequests()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

async function doRejectRequest(rq) {
  if (!confirm(`拒绝申请「${rq.username}」？`)) return
  busy.value = true
  try {
    await api.AdminRejectRegisterRequest(rq.id)
    flash(`已拒绝「${rq.username}」`)
    loadRegisterRequests()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
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

// 从「注册信息」（成员复制：工号+公钥）导入，避免手输工号出错（§6.3）
function pasteRegInfo() {
  const raw = prompt('粘贴成员发来的「注册信息」（格式：工号：xxx / 公钥：xxx）：')
  if (!raw) return
  const uname = raw.match(/工号[:：]\s*(\S+)/)
  const pub = raw.match(/公钥[:：]\s*(\S+)/)
  if (!uname || !pub) {
    flash('解析失败：未找到「工号」或「公钥」（请复制成员端「复制注册信息」的内容）', 'err')
    return
  }
  newUser.value.username = uname[1].trim()
  newUser.value.publicKey = pub[1].trim()
  flash(`已从注册信息导入工号「${newUser.value.username}」，核对后开户`, 'info')
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

// 吊销成员（两步确认 + 服务端成员名二次确认；吊销后空组告警）
async function doRevokeMember(m) {
  if (confirmRevokeUserID.value !== m.user_id) {
    confirmRevokeUserID.value = m.user_id
    flash(`再次点击确认吊销成员「${m.name}」`, 'info')
    return
  }
  confirmRevokeUserID.value = ''
  busy.value = true
  try {
    const emptyGroups = await api.AdminRevoke(m.user_id, m.name)
    if (emptyGroups && emptyGroups.length) {
      flash(`成员「${m.name}」已吊销 ⚠ 以下组已无成员：${emptyGroups.join('、')}（组密钥重加密将无人执行）`, 'err')
    } else {
      flash(`成员「${m.name}」已吊销，组内成员将自动重加密`)
    }
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
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.lock()">⏻ 锁定</button>
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
                <button
                    class="pb-btn pb-btn--danger pb-btn--sm"
                    :disabled="busy"
                    @click="doRevokeMember(m)"
                >
                  {{ confirmRevokeUserID === m.user_id ? '确认吊销' : '吊销' }}
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
          <div class="admin__form-row">
            <input v-model="newUser.username" class="pb-input pb-input--mono" placeholder="工号（唯一、不可改）" />
            <input v-model="newUser.name" class="pb-input" placeholder="显示名" />
          </div>
          <textarea v-model="newUser.publicKey" class="pb-input pb-input--mono admin__textarea" placeholder="公钥（base64，成员客户端生成后复制给你）"></textarea>
          <div class="admin__form-row">
            <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="pasteRegInfo">📋 从注册信息导入</button>
            <button class="pb-btn pb-btn--primary pb-btn--block" style="flex:1" :disabled="busy" @click="doCreateUser">开户</button>
          </div>
          <p v-if="tip" class="pb-xs" :class="`admin__tip--${tipType}`">{{ tip }}</p>
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

    <!-- 方案 C：注册申请审核面板 -->
    <div class="pb-glass admin__panel">
      <div class="admin__panel-title">注册申请审核（{{ regRequests.length }} 待审）</div>
      <div v-if="regRequests.length" class="admin__list">
        <div v-for="rq in regRequests" :key="rq.id" class="admin__item">
          <div class="admin__item-main">
            <span class="admin__item-title">工号 {{ rq.username }}</span>
            <span class="pb-xs pb-muted">
              设备：{{ rq.device_name || '—' }} · IP：{{ rq.ip || '—' }} · 申请时间 {{ new Date(rq.created_at * 1000).toLocaleString() }}
            </span>
            <span class="pb-xs pb-muted pb-break-all">公钥：{{ rq.sm2_public_key }}</span>
            <div class="admin__form-row" style="margin-top:8px">
              <input v-model="regReviewName" class="pb-input pb-input--mono" style="flex:1" placeholder="显示名（默认=工号）" spellcheck="false" />
              <button class="pb-btn pb-btn--primary pb-btn--sm" :disabled="busy" @click="doApproveRequest(rq)">✓ 通过并开户</button>
              <button class="pb-btn pb-btn--ghost pb-btn--sm pb-btn--danger" :disabled="busy" @click="doRejectRequest(rq)">✕ 拒绝</button>
            </div>
          </div>
        </div>
      </div>
      <p v-else class="pb-xs pb-muted">暂无待审核的注册申请</p>
    </div>

    <!-- 方案 C：邀请码面板 -->
    <div class="pb-glass admin__panel">
      <div class="admin__panel-title">邀请码（注册码，绑定工号）</div>
      <div class="admin__form">
        <div class="admin__form-row">
          <input v-model="inviteUsername" class="pb-input pb-input--mono" style="flex:1" placeholder="工号（该工号注册用）" spellcheck="false" />
          <input v-model.number="inviteTTLDays" type="number" min="1" class="pb-input pb-input--mono" style="width:70px" title="有效期天数（默认3天）" />
          <label class="admin__switch" title="免审核：提交申请即自动开户">
            <input type="checkbox" v-model="inviteAutoApprove" />
            <span class="pb-xs">免审核</span>
          </label>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" :disabled="busy" @click="doCreateInvite">生成</button>
        </div>
      </div>
      <div class="admin__list" v-if="invites.length">
        <div v-for="inv in invites" :key="inv.code" class="admin__item">
          <div class="admin__item-main">
            <span class="admin__item-title pb-mono">{{ inv.code }}</span>
            <span class="pb-xs pb-muted">
              工号 {{ inv.username }} · {{ inv.auto_approve ? '免审核' : '需审核' }} ·
              {{ inv.status === 'used' ? '已使用' : `有效至 ${new Date(inv.expires_at * 1000).toLocaleDateString()}` }}
            </span>
          </div>
        </div>
      </div>
      <p v-else class="pb-xs pb-muted">暂无邀请码</p>
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
