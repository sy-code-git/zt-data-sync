<script setup>
// 管理面板（tab 导航式，按管理端原型）：
// 概览 / 组管理 / 成员 / 注册审核 / 邀请码 / 设备 六个 tab；
// 保留既有全部 API 功能（两步确认、5 秒轮询、注册信息导入、公钥指纹、通用确认/输入弹窗）。
import {ref, reactive, computed, onMounted, onBeforeUnmount} from 'vue'
import {api} from '../api'
import {useAppStore} from '../store'
import PbSelect from '../components/PbSelect.vue'

const store = useAppStore()

// ---- tab ----
const currentTab = ref('overview')

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
// 显示管理员开关（默认关闭：成员列表不显示管理员账号）
const showAdmins = ref(false)
// 选中的组 + 组内成员（含在线状态）
const selectedGroup = ref(null)
const members = ref([])
// 组内下拉直接加成员（选中值 per group）
const addMemberSel = ref({})
// 方案 C：邀请码 + 注册申请审核（§6.3）
const inviteUsername = ref('')
const inviteAutoApprove = ref(false)
const inviteTTLDays = ref(3)
const invites = ref([])
const regRequests = ref([])
// 审核显示名：按申请 id 独立保存（共享单个 ref 会在多条申请同屏时互相串值）
const regReviewNames = reactive({})

// 过滤后的成员列表（默认隐藏管理员）
const visibleUsers = computed(() => {
  if (showAdmins.value) return users.value
  return users.value.filter((u) => u.role !== 'admin')
})

let refreshing = false
// 全量刷新（进入面板 / 手动操作后）：组 + 成员
async function refresh(silent = false) {
  if (refreshing) return // 防重入：上一次未完成时跳过本轮（轮询+手动刷新并发）
  refreshing = true
  try {
    const [g, u] = await Promise.all([api.AdminListGroups(), api.AdminListUsers()])
    groups.value = g || []
    users.value = u || []
  } catch (e) {
    // 轮询触发的失败静默（服务端短暂断开时避免每 5 秒刷一次错误提示）；手动刷新才提示
    if (!silent) {
      tip.value = String(e.message || e)
      tipType.value = 'err'
    }
  } finally {
    refreshing = false
  }
  loadInvites()
  loadRegisterRequests()
}

// 手动「⟳ 刷新」：全量 + 设备（设备只在首次进面板与此处拉取，避免轮询开销；见 P2-1）
async function refreshAll() {
  await refresh()
  await loadDevices().catch(() => {})
}

// ---- 轮询（§8.3：admin 30/min/token，key=设备 ID，故 20 个管理端各自独立配额、互不抢占）----
// 设计目标（按 20 客户端同时挂面板的容量设计）：
//   ① 每个 tab 轮询只发该 tab 所需请求（概览统计需 4 个，故单独降频到 20s），
//      彻底去掉 refresh() 的 4 请求扇出——这是 09-11「审核时触发限流」未修净的根因；
//   ② 轮询自留预算 18 次/60s（占配额 60%），余 ≥12 次留给写操作（审核/开户/生成邀请码），
//      预算用尽则本轮跳过，写操作永不被轮询挤占；
//   ③ 窗口不可见（最小化/切走）完全暂停，避免面板长期挂后台空转。
// 单客户端最坏：12 次/分（概览全量）；20 客户端合计 ≤240 次/分（分属 20 个设备配额）。
const POLL_INTERVAL = {
  review: 10000, // 待审申请 6 次/分
  invite: 15000, // 邀请码列表 4 次/分
  groups: 10000, // 组与在线状态 6 次/分
  open: 15000, // 开户页候选成员 4 次/分
  overview: 20000, // 统计卡（全量 4 请求）12 次/分
  devices: 30000, // 设备在线状态 2 次/分
}
const POLL_COST = {overview: 4} // 概览走全量刷新，按 4 次计
const POLL_BUDGET_PER_MIN = 18
let pollTicks = []
function pollBudgetOk(cost) {
  const now = Date.now()
  pollTicks = pollTicks.filter((t) => now - t < 60000)
  if (pollTicks.length + cost > POLL_BUDGET_PER_MIN) return false
  for (let i = 0; i < cost; i++) pollTicks.push(now)
  return true
}

let lastPollAt = 0
async function pollTab(force = false) {
  if (document.hidden && !force) return // ③ 后台暂停
  const interval = POLL_INTERVAL[currentTab.value] || 30000
  if (!force && Date.now() - lastPollAt < interval) return // 未到该 tab 的刷新间隔
  const cost = POLL_COST[currentTab.value] || 1
  if (!pollBudgetOk(cost)) return // ② 预算用尽：跳过本轮，写操作优先
  lastPollAt = Date.now()
  switch (currentTab.value) {
    case 'review':
      await loadRegisterRequests()
      break
    case 'invite':
      await loadInvites()
      break
    case 'groups':
      await loadGroups()
      break
    case 'open':
      await loadUsers()
      break
    case 'overview': // 统计卡依赖 groups/users/regRequests → 全量
      await refresh(true)
      break
    case 'devices':
      await loadDevices().catch(() => {})
      break
  }
}

// 单数据源加载：供 pollTab 使用，避免 refresh() 的 4 请求扇出
async function loadGroups() {
  try {
    groups.value = (await api.AdminListGroups()) || []
  } catch (e) {
    // 轮询静默（服务端短暂断开/限流时不刷错误提示），下轮重试
  }
}

async function loadUsers() {
  try {
    users.value = (await api.AdminListUsers()) || []
  } catch (e) {
    // 轮询静默，下轮重试
  }
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
  } catch {
    // 忽略（老服务端无此接口时静默）
  }
}

// 邀请码状态语义（服务端 §6.3：status 只有 unused/used 两值，过期靠 expires_at 判定）：
// unused + 未到期 = 有效；unused + 已到期 = 已过期；used = 已使用（无论是否到期）。
function inviteActive(inv) {
  return inv.status === 'unused' && (inv.expires_at * 1000) > Date.now()
}

// 注册申请：列表 / 通过（开户）/ 拒绝
async function loadRegisterRequests() {
  try {
    regRequests.value = (await api.AdminListRegisterRequests('pending')) || []
    // 显示名预填工号（对齐原型 value=rq.username；按申请 id 独立）
    for (const rq of regRequests.value) {
      if (regReviewNames[rq.id] === undefined) regReviewNames[rq.id] = rq.username
    }
  } catch {
    // 忽略
  }
}

async function doApproveRequest(rq) {
  const name = (regReviewNames[rq.id] || '').trim() || rq.username
  if (!await showConfirm({
    title: '通过注册申请并开户',
    message: `确认通过「${rq.username}」的申请并开户？\n显示名：${name}\n公钥/设备名/IP 已核对`,
    okText: '通过并开户',
  })) return
  busy.value = true
  try {
    await api.AdminApproveRegisterRequest(rq.id, name)
    flash(`已开户「${rq.username}」（显示名 ${name}）`)
    delete regReviewNames[rq.id]
    loadRegisterRequests()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

async function doRejectRequest(rq) {
  if (!await showConfirm({
    title: '拒绝注册申请',
    message: `确认拒绝「${rq.username}」的注册申请？`,
    okText: '拒绝',
    danger: true,
  })) return
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

// 公钥指纹（短哈希展示，避免整串公钥刷屏）
// FNV-1a 32 位：分布更均匀、碰撞率低于简单乘法哈希；仅用于展示缩短，非认证用途。
function pubFingerprint(pub) {
  const s = String(pub || '')
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  const hex = (h >>> 0).toString(16).toUpperCase().padStart(8, '0')
  return hex.slice(0, 4) + ':' + hex.slice(4)
}

// 复制（30 秒后清剪贴板）
function copyText(text, label) {
  navigator.clipboard?.writeText(text)
  store.toast(label + '，30 秒后自动清空', 'success')
  clearTimeout(copyText._t)
  copyText._t = setTimeout(() => navigator.clipboard?.writeText(''), 30000)
}

// 自动轮询：5s tick 触发 pollTab，由 pollTab 按当前 tab 的间隔 + 轮询预算决定是否真发请求
// （见 pollTab 注释；概览的全量刷新已合并进 POLL_INTERVAL.overview，不再单独设慢定时器）
let adminPollTimer = null
onMounted(() => {
  refresh(true) // 进入面板先全量一次
  adminPollTimer = setInterval(() => pollTab(), 5000)
  // 概览 tab 显示设备在线统计，进入面板即后台预载（无需等切到设备 tab）
  if (!devicesLoaded.value) loadDevices().catch(() => {})
  // 窗口重新可见时立即补一轮（与 pollTab 的后台暂停配对）
  document.addEventListener('visibilitychange', onVisibilityChange)
})
function onVisibilityChange() {
  if (!document.hidden) pollTab(true)
}
onBeforeUnmount(() => {
  if (adminPollTimer) clearInterval(adminPollTimer)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  clearTimeout(copyText._t)
  copyText._t = null
})

// ---- 通用确认弹窗（替换浏览器原生 confirm/prompt，风格与项目一致，半透明玻璃） ----
const confirmModal = ref({show: false, title: '', message: '', okText: '确定', cancelText: '取消', resolve: null})
// danger=true 用于破坏性操作（删除/移除/吊销/拒绝）：确认按钮走危险色，与主操作区分
function showConfirm({title, message, okText = '确定', cancelText = '取消', danger = false}) {
  return new Promise((resolve) => {
    confirmModal.value = {show: true, title, message, okText, cancelText, danger, resolve}
  })
}
function closeConfirm(ok) {
  const r = confirmModal.value.resolve
  confirmModal.value.show = false
  confirmModal.value.resolve = null
  if (r) r(ok)
}

// 通用输入弹窗（替换浏览器原生 prompt：WebView2 下原生 prompt 可能被禁用返回 null，功能静默失效）
const promptModal = ref({show: false, title: '', message: '', placeholder: '', value: '', resolve: null})
function showPrompt({title, message, placeholder = ''}) {
  return new Promise((resolve) => {
    promptModal.value = {show: true, title, message, placeholder, value: '', resolve}
  })
}
function closePrompt(ok) {
  const r = promptModal.value.resolve
  const v = promptModal.value.value
  promptModal.value.show = false
  promptModal.value.resolve = null
  if (r) r(ok ? (v || '').trim() : null)
}

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

// 组内下拉直接加成员（管理端原型交互：组管理页展开组后选人加入）
async function doAddMemberInline(g) {
  const uid = addMemberSel.value[g.id]
  if (!uid) {
    flash('请选择成员', 'err')
    return
  }
  const u = users.value.find((x) => x.user_id === uid)
  if (!u) return
  busy.value = true
  try {
    await api.AdminAddMember(g.id, uid)
    flash(`成员「${u.name}」已加入组`)
    addMemberSel.value[g.id] = '' // 清空已加入选择
    members.value = await api.AdminListMembers(g.id)
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

// 加人下拉候选：非管理员且未入组（PbSelect options）
function candidateMembers(g) {
  return users.value
    .filter((x) => x.role !== 'admin' && !members.value.some((m) => m.user_id === x.user_id))
    .map((u) => ({value: u.user_id, label: `${u.name}（${u.username}）`}))
}

// 删除组（归档）：弹窗确认（对齐原型 confirmModal；服务端仍要求组名二次确认，由 API 参数传递）
async function doArchiveGroup(g) {
  if (!await showConfirm({
    title: '删除组',
    message: `确定删除组「${g.name}」吗？删除后组归档（可恢复），期间成员无法同步该组数据。`,
    okText: '确认删除',
    danger: true,
  })) return
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
async function pasteRegInfo() {
  const raw = await showPrompt({
    title: '从注册信息导入',
    message: '粘贴成员发来的「注册信息」（成员端「复制注册信息」按钮的内容）：',
    placeholder: '工号：zhangsan\n公钥：MFkwEwYHKoZIzj0CAQYI…',
  })
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

// 移除组内成员：弹窗确认（服务端成员名二次确认由 API 参数传递）
async function doRemoveMember(m) {
  if (!selectedGroup.value) return
  if (!await showConfirm({
    title: '移除成员',
    message: `确定将成员「${m.name}」移出组「${selectedGroup.value.name}」吗？`,
    okText: '确认移除',
    danger: true,
  })) return
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

// 吊销成员：弹窗确认 + 服务端成员名二次确认；吊销后空组告警
async function doRevokeMember(m) {
  if (!await showConfirm({
    title: '吊销成员',
    message: `确定吊销成员「${m.name}」吗？吊销后其所有设备 token 立即失效，组内成员将自动重加密。`,
    okText: '确认吊销',
    danger: true,
  })) return
  busy.value = true
  try {
    const emptyGroups = await api.AdminRevoke(m.user_id, m.name)
    if (emptyGroups && emptyGroups.length) {
      flash(`成员「${m.name}」已吊销 ⚠ 以下组已无成员：${emptyGroups.join('、')}（组密钥重加密将无人执行）`, 'err')
    } else {
      flash(`成员「${m.name}」已吊销，组内成员将自动重加密`)
    }
    // 刷新组内成员（若已选组）与成员列表（吊销后从 users 移除）
    if (selectedGroup.value) {
      members.value = await api.AdminListMembers(selectedGroup.value.id)
    }
    users.value = await api.AdminListUsers()
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    busy.value = false
  }
}

// 设备：进入设备 tab 时加载
const devices = ref([])
const devicesLoaded = ref(false)
const devicesBusy = ref(false)

async function loadDevices() {
  devicesBusy.value = true
  try {
    devices.value = await api.AdminListDevices()
    devicesLoaded.value = true
  } catch (e) {
    flash(String(e.message || e), 'err')
  } finally {
    devicesBusy.value = false
  }
}

function gotoTab(t) {
  currentTab.value = t
  if (t === 'devices' && !devicesLoaded.value) loadDevices()
  // 切 tab 立即拉该 tab 数据（force 仅跳过刷新间隔，仍受轮询预算约束，见 pollTab）
  pollTab(true)
}

// 最后在线时间格式化（对齐原型：YYYY-MM-DD HH:mm，无秒；空值显示 —）
function fmtTime(ts) {
  if (!ts) return '—'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

// Esc 关闭弹窗
const onKeydown = (e) => {
  if (e.key !== 'Escape') return
  if (confirmModal.value.show) closeConfirm(false)
  else if (promptModal.value.show) closePrompt(false)
}
if (typeof document !== 'undefined') {
  document.addEventListener('keydown', onKeydown)
}
onBeforeUnmount(() => {
  if (typeof document !== 'undefined') {
    document.removeEventListener('keydown', onKeydown)
  }
})

// 副标题
const subTitle = computed(() => {
  switch (currentTab.value) {
    case 'overview': return '团队与密钥 · 全貌'
    case 'groups': return '组与成员 · 在线状态'
    case 'open': return '开户 · 绑定工号与公钥'
    case 'review': return `注册申请 · ${regRequests.value.length} 待审`
    case 'invite': return '邀请码 · 注册码（绑定工号）'
    case 'devices': return '设备 / 主机信息'
    default: return ''
  }
})
</script>

<template>
  <div class="admin-wrap">
    <!-- tab 导航 -->
    <nav class="admin-nav">
      <button class="admin-nav__item" :class="{'admin-nav__item--on': currentTab === 'overview'}" @click="gotoTab('overview')">📊 概览</button>
      <button class="admin-nav__item" :class="{'admin-nav__item--on': currentTab === 'groups'}" @click="gotoTab('groups')">
        👥 组管理
        <span class="pb-badge pb-badge--neutral">{{ groups.filter((g) => !g.archived).length }}</span>
      </button>
      <button class="admin-nav__item" :class="{'admin-nav__item--on': currentTab === 'open'}" @click="gotoTab('open')">🧑‍💼 成员</button>
      <button class="admin-nav__item" :class="{'admin-nav__item--on': currentTab === 'review'}" @click="gotoTab('review')">
        ✅ 注册审核
        <span class="pb-badge pb-badge--warning">{{ regRequests.length }}</span>
      </button>
      <button class="admin-nav__item" :class="{'admin-nav__item--on': currentTab === 'invite'}" @click="gotoTab('invite')">
        🎟 邀请码
        <span class="pb-badge pb-badge--neutral">{{ invites.filter(inviteActive).length }}</span>
      </button>
      <button class="admin-nav__item" :class="{'admin-nav__item--on': currentTab === 'devices'}" @click="gotoTab('devices')">🖥 设备</button>
    </nav>

    <div class="admin">
      <div class="admin__head">
        <div>
          <h2>管理面板</h2>
          <span class="pb-xs pb-muted">{{ subTitle }}</span>
        </div>
        <div class="admin__head-actions">
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="gotoTab('devices')">🖥 设备（{{ devices.length }}）</button>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="refreshAll()">⟳ 刷新</button>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.lock()">⏻ 锁定</button>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('workbench')">← 返回</button>
        </div>
      </div>

      <div v-if="tip" class="admin__tip" :class="`admin__tip--${tipType}`">{{ tip }}</div>

      <!-- ====== 概览 ====== -->
      <template v-if="currentTab === 'overview'">
        <div class="admin__grid">
          <div class="pb-glass admin__panel">
            <div class="admin__panel-title">快速入口</div>
            <div class="admin__quick-grid">
              <button class="pb-btn pb-btn--ghost" @click="gotoTab('review')">✅ 注册申请审核（<b>{{ regRequests.length }}</b> 待审）</button>
              <button class="pb-btn pb-btn--ghost" @click="gotoTab('invite')">🎟 生成邀请码</button>
              <button class="pb-btn pb-btn--ghost" @click="gotoTab('groups')">👥 组管理</button>
              <button class="pb-btn pb-btn--ghost" @click="gotoTab('open')">🧑‍💼 成员开户</button>
            </div>
          </div>
          <div class="pb-glass admin__panel">
            <div class="admin__panel-title">统计</div>
            <div class="admin__stats-grid">
              <div class="admin__stat">
                <span class="admin__stat-val">{{ visibleUsers.length }}</span>
                <span class="pb-xs pb-muted">成员数</span>
              </div>
              <div class="admin__stat">
                <span class="admin__stat-val">{{ groups.filter((g) => !g.archived).length }}</span>
                <span class="pb-xs pb-muted">活跃组</span>
              </div>
              <div class="admin__stat">
                <span class="admin__stat-val">{{ regRequests.length }}</span>
                <span class="pb-xs pb-muted">待审申请</span>
              </div>
              <div class="admin__stat">
                <span class="admin__stat-val">{{ devices.filter((d) => d.online).length }}<span class="admin__stat-sub"> / {{ devices.length }}</span></span>
                <span class="pb-xs pb-muted">设备在线</span>
              </div>
            </div>
            <p class="pb-xs pb-muted" style="margin-top: 6px">
              管理功能集成桌面端：成员开户 / 组管理 / 注册审核 / 邀请码 / 设备监控，均在本面板完成。
            </p>
          </div>
        </div>
      </template>

      <!-- ====== 组管理 ====== -->
      <template v-else-if="currentTab === 'groups'">
        <div class="pb-glass admin__panel">
          <div class="admin__panel-title">组</div>
          <div class="admin__input-row">
            <input v-model="newGroupName" class="pb-input" placeholder="新组名" @keyup.enter="doCreateGroup"/>
            <button class="pb-btn pb-btn--primary pb-btn--sm" :disabled="busy" @click="doCreateGroup">建组</button>
          </div>
          <div class="admin__list">
            <template v-for="g in groups" :key="g.id">
              <div class="admin__item">
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
                  <button v-if="!g.archived" class="pb-btn pb-btn--ghost pb-btn--sm pb-btn--danger" @click="doArchiveGroup(g)">
                    删除
                  </button>
                  <button v-else class="pb-btn pb-btn--ghost pb-btn--sm" @click="doUnarchiveGroup(g)">恢复</button>
                </div>
              </div>

              <!-- 组内成员（选中组后展示，含在线状态 + 下拉直接加人） -->
              <div v-if="selectedGroup && selectedGroup.id === g.id" class="admin__members">
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
                        v-if="m.role !== 'admin'"
                        class="pb-btn pb-btn--ghost pb-btn--sm pb-btn--danger"
                        :disabled="busy"
                        @click="doRemoveMember(m)"
                    >
                      移除
                    </button>
                    <button
                        v-if="m.role !== 'admin'"
                        class="pb-btn pb-btn--danger pb-btn--sm"
                        :disabled="busy"
                        @click="doRevokeMember(m)"
                    >
                      吊销
                    </button>
                  </div>
                </div>
                <p v-if="!members.length" class="pb-xs pb-muted">该组暂无成员</p>
                <!-- 下拉直接加人（候选 = 非管理员且未入组；无候选时提示，对齐原型） -->
                <div class="admin__input-row" style="margin-top: 4px">
                  <div style="flex: 1; min-width: 0">
                    <PbSelect
                        v-if="candidateMembers(g).length"
                        :model-value="addMemberSel[g.id] || ''"
                        :options="candidateMembers(g)"
                        placeholder="选择要加入的成员…"
                        @update:model-value="addMemberSel[g.id] = $event"
                    />
                    <input v-else class="pb-input" disabled placeholder="无可用成员（均已加入或不存在）"/>
                  </div>
                  <button
                      class="pb-btn pb-btn--primary pb-btn--sm"
                      :disabled="busy || !addMemberSel[g.id] || !candidateMembers(g).length"
                      @click="doAddMemberInline(g)"
                  >＋ 加入本组</button>
                </div>
              </div>
            </template>
            <p v-if="!groups.length" class="pb-xs pb-muted">暂无组</p>
          </div>
        </div>
      </template>

      <!-- ====== 成员开户 ====== -->
      <template v-else-if="currentTab === 'open'">
        <div class="pb-glass admin__panel">
          <div class="admin__panel-title">成员开户</div>
          <div class="admin__form">
            <div class="admin__form-row">
              <input v-model="newUser.username" class="pb-input pb-input--mono" placeholder="工号（唯一、不可改）"/>
              <input v-model="newUser.name" class="pb-input" placeholder="显示名"/>
            </div>
            <textarea v-model="newUser.publicKey" class="pb-input pb-input--mono admin__textarea" placeholder="公钥（base64，成员客户端生成后复制给你）"></textarea>
            <div class="admin__form-row">
              <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="pasteRegInfo">📋 从注册信息导入</button>
              <button class="pb-btn pb-btn--primary pb-btn--block" style="flex: 1" :disabled="busy" @click="doCreateUser">开户</button>
            </div>
            <p v-if="tip" class="pb-xs" :class="`admin__tip--${tipType}`">{{ tip }}</p>
          </div>

          <div class="admin__panel-title" style="margin-top: 16px">
            <span>成员列表</span>
            <label class="admin__switch">
              <input type="checkbox" v-model="showAdmins"/>
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
                  v-if="u.role !== 'admin'"
                  class="pb-btn pb-btn--ghost pb-btn--sm pb-btn--danger"
                  @click="doRevokeMember({user_id: u.user_id, name: u.name, username: u.username})"
              >
                吊销
              </button>
            </div>
            <p v-if="!visibleUsers.length" class="pb-xs pb-muted">暂无成员</p>
          </div>
        </div>
      </template>

      <!-- ====== 注册申请审核 ====== -->
      <template v-else-if="currentTab === 'review'">
        <div class="pb-glass admin__panel">
          <div class="admin__panel-title">
            注册申请审核
            <span class="pb-badge pb-badge--warning">{{ regRequests.length }} 待审</span>
          </div>
          <div v-if="regRequests.length" class="admin__list">
            <div v-for="rq in regRequests" :key="rq.id" class="admin__item admin__item--col">
              <div class="admin__item-main">
                <span class="admin__item-title">工号 {{ rq.username }}</span>
                <span class="pb-xs pb-muted">
                  设备：{{ rq.device_name || '—' }} · IP：{{ rq.ip || '—' }} · 申请时间 {{ fmtTime(rq.created_at) }}
                </span>
                <div class="admin__pubrow">
                  <span class="pb-xs pb-muted">公钥指纹：</span>
                  <span class="pb-mono pb-xs" style="color: var(--accent)">SM2 {{ pubFingerprint(rq.sm2_public_key) }}</span>
                  <button
                      class="pb-btn pb-btn--ghost pb-btn--sm"
                      style="height: 22px; padding: 0 8px; font-size: 11px"
                      @click="copyText(rq.sm2_public_key, '公钥已复制')"
                  >⧉ 复制公钥</button>
                  <span class="pb-xs pb-muted pb-truncate" :title="rq.sm2_public_key">{{ (rq.sm2_public_key || '').slice(0, 28) }}…</span>
                </div>
              </div>
              <div class="admin__form-row" style="margin-top: 4px">
                <input v-model="regReviewNames[rq.id]" class="pb-input pb-input--mono" style="flex: 1" placeholder="显示名（默认=工号）" spellcheck="false"/>
                <button class="pb-btn pb-btn--primary pb-btn--sm" :disabled="busy" @click="doApproveRequest(rq)">✓ 通过并开户</button>
                <button class="pb-btn pb-btn--ghost pb-btn--sm pb-btn--danger" :disabled="busy" @click="doRejectRequest(rq)">✕ 拒绝</button>
              </div>
            </div>
          </div>
          <p v-else class="pb-xs pb-muted">暂无待审核的注册申请</p>
        </div>
        <p class="pb-xs pb-muted">· 通过后自动开户并关联公钥，成员端注册申请将自动完成设备注册。每 5 秒自动刷新。</p>
      </template>

      <!-- ====== 邀请码 ====== -->
      <template v-else-if="currentTab === 'invite'">
        <div class="pb-glass admin__panel">
          <div class="admin__panel-title">生成邀请码</div>
          <div class="admin__form-row">
            <input v-model="inviteUsername" class="pb-input pb-input--mono" style="flex: 1" placeholder="工号（该工号注册用）" spellcheck="false"/>
            <input v-model.number="inviteTTLDays" type="number" min="1" class="pb-input pb-input--mono" style="width: 70px" title="有效期天数（默认3天）"/>
            <label class="admin__switch" title="免审核：提交申请即自动开户">
              <input type="checkbox" v-model="inviteAutoApprove"/>
              <span class="pb-xs">免审核</span>
            </label>
            <button class="pb-btn pb-btn--ghost pb-btn--sm" :disabled="busy" @click="doCreateInvite">生成</button>
          </div>
        </div>
        <div class="pb-glass admin__panel">
          <div class="admin__panel-title">邀请码列表</div>
          <div v-if="invites.length" class="admin__list">
            <div v-for="inv in invites" :key="inv.code" class="admin__item" :style="inviteActive(inv) ? '' : 'opacity:.62'">
              <div class="admin__item-main">
              <span class="admin__item-title">
                <span class="pb-mono">{{ inv.code }}</span>
                <span v-if="inviteActive(inv)" class="pb-badge pb-badge--success">有效</span>
                <span v-else-if="inv.status === 'used'" class="pb-badge pb-badge--neutral">已使用</span>
                <span v-else class="pb-badge pb-badge--danger">已过期</span>
              </span>
                <span class="pb-xs pb-muted">
                工号 {{ inv.username }} ·
                <span :style="inv.auto_approve ? 'color: var(--accent)' : ''">{{ inv.auto_approve ? '免审核' : '需审核' }}</span> ·
                {{ inviteActive(inv) ? `有效至 ${new Date(inv.expires_at * 1000).toLocaleDateString()}` : inv.status === 'used' ? '已使用' : '过期于 ' + new Date(inv.expires_at * 1000).toLocaleDateString() }}
              </span>
              </div>
              <button
                  v-if="inviteActive(inv)"
                  class="pb-btn pb-btn--ghost pb-btn--sm"
                  @click="copyText(inv.code, '邀请码已复制')"
              >复制</button>
            </div>
          </div>
          <p v-else class="pb-xs pb-muted">暂无邀请码</p>
        </div>
      </template>

      <!-- ====== 设备 ====== -->
      <template v-else-if="currentTab === 'devices'">
        <div class="pb-glass admin__panel">
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
      </template>
    </div>

    <!-- 通用确认弹窗（替换浏览器原生 confirm：避免原生弹窗位置/风格不一致） -->
    <div v-if="confirmModal.show" class="admin__confirm-mask" @click.self="closeConfirm(false)">
      <div class="pb-glass pb-glass--strong admin__confirm-modal">
        <h3 class="admin__confirm-title">{{ confirmModal.title }}</h3>
        <p class="admin__confirm-msg">{{ confirmModal.message }}</p>
        <div class="admin__confirm-actions">
          <button class="pb-btn pb-btn--ghost" @click="closeConfirm(false)">{{ confirmModal.cancelText }}</button>
          <button
              class="pb-btn"
              :class="confirmModal.danger ? 'pb-btn--danger' : 'pb-btn--primary'"
              @click="closeConfirm(true)"
          >{{ confirmModal.okText }}</button>
        </div>
      </div>
    </div>

    <!-- 通用输入弹窗（替换浏览器原生 prompt：WebView2 下可能被禁用） -->
    <div v-if="promptModal.show" class="admin__confirm-mask" @click.self="closePrompt(false)">
      <div class="pb-glass pb-glass--strong admin__confirm-modal">
        <h3 class="admin__confirm-title">{{ promptModal.title }}</h3>
        <p class="admin__confirm-msg">{{ promptModal.message }}</p>
        <textarea
            v-model="promptModal.value"
            class="pb-input pb-input--mono admin__prompt-input"
            :placeholder="promptModal.placeholder"
            spellcheck="false"
        ></textarea>
        <div class="admin__confirm-actions">
          <button class="pb-btn pb-btn--ghost" @click="closePrompt(false)">取消</button>
          <button class="pb-btn pb-btn--primary" @click="closePrompt(true)">导入</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin-wrap {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

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

.admin__quick-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.admin__stats-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 10px;
}
.admin__stat {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  padding: 12px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
}
.admin__stat-val {
  font-size: 22px;
  font-weight: 700;
  font-family: var(--font-mono);
}
.admin__stat-sub { font-size: 12px; font-weight: 400; color: var(--text-3); }

.admin__panel {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.admin__panel-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-2);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.admin__input-row { display: flex; gap: 8px; }
.admin__input-row .pb-input { flex: 1; }

.admin__form { display: flex; flex-direction: column; gap: 8px; }
.admin__form-row { display: flex; gap: 8px; align-items: center; }
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
.admin__item--col { flex-direction: column; align-items: stretch; }
.admin__item-main { display: flex; flex-direction: column; min-width: 0; gap: 2px; }
.admin__item-title { font-size: 13px; font-weight: 600; display: flex; align-items: center; gap: 6px; }
.admin__item-actions { display: flex; align-items: center; gap: 6px; flex-shrink: 0; }

.admin__switch { display: flex; align-items: center; gap: 6px; cursor: pointer; }
.admin__switch input { cursor: pointer; }

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
.admin__pubrow { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-width: 0; }

/* 通用确认弹窗 */
.admin__confirm-mask {
  position: fixed; inset: 0; z-index: 100;
  background: rgba(0, 0, 0, .45);
  display: flex; align-items: center; justify-content: center;
}
.admin__confirm-modal {
  width: min(440px, 92vw);
  max-height: 80vh; overflow-y: auto;
  padding: 20px;
  border-radius: 12px;
  display: flex; flex-direction: column; gap: 12px;
  white-space: pre-line;
}
.admin__confirm-title { margin: 0; font-size: 16px; font-weight: 600; }
.admin__confirm-msg { margin: 0; font-size: 13.5px; line-height: 1.6; color: var(--text-2); }
.admin__confirm-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 4px; }
.admin__prompt-input {
  min-height: 88px;
  resize: vertical;
  font-size: 12.5px;
  line-height: 1.6;
  white-space: pre-wrap;
}
</style>
