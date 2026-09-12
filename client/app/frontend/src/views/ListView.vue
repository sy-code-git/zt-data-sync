<script setup>
// 方案 J 列表页：项目 tab + 左侧环境→IP 子树 + 右侧下属列表（层级自适应）。
// 核心：IP 直查（左树点 IP → 该 IP 账号表）、项目浏览（1-2 跳窥全貌）、
// 账号 chips 弹窗（掩码+显示切换+复制）、节点级列配置（localStorage，不上传）。
import {computed, ref, watch, onBeforeUnmount} from 'vue'
import {useAppStore} from '../store'
import {useModalFocus} from '../composables/useModalFocus'

const store = useAppStore()

/* ---- 基础工具 ---- */
const typeMeta = {
  project: {icon: '🗂', label: '项目'},
  env: {icon: '🌐', label: '环境'},
  ip_type: {icon: '🖥', label: 'IP 类型'},
  acc_type: {icon: '👤', label: '账号类型'},
  account: {icon: '🔑', label: '账号'},
  custom: {icon: '📎', label: '补充卡片'},
}

const entries = computed(() => store.entries.filter((e) => !e.deleted))

function fmtTime(ts) {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function copyText(t) {
  navigator.clipboard?.writeText(t)
  store.toast('已复制到剪贴板，30 秒后自动清空', 'success')
  clearTimeout(copyText._t)
  copyText._t = setTimeout(() => navigator.clipboard?.writeText(''), 30000)
}

/* ---- 项目 tab ---- */
const projects = computed(() => entries.value.filter((e) => e.type === 'project'))
const proj = computed(() =>
  projects.value.find((p) => p.id === store.pjProject) || projects.value[0] || null
)

watch(proj, (p) => {
  if (p && store.pjProject !== p.id) store.pjProject = p.id
}, {immediate: true})

function switchProject(id) {
  store.pjProject = id
  store.pjFocus = ''
  store.pjIp = ''
}

/* ---- 左树数据：环境 → IP（聚合 account.fields.ip） ---- */
const envs = computed(() => entries.value.filter((e) => e.type === 'env' && e.parent_id === proj.value?.id))

// children 索引（一次构建）：envAccounts / ipGroups / 路径回溯共用，避免对 entries 反复全表递归
const childIndex = computed(() => {
  const m = new Map()
  for (const e of entries.value) {
    const k = e.parent_id ?? null
    const arr = m.get(k) || []
    arr.push(e)
    m.set(k, arr)
  }
  return m
})
// id → entry 索引（路径回溯用）
const idIndex = computed(() => {
  const m = new Map()
  for (const e of entries.value) m.set(e.id, e)
  return m
})

function envAccounts(envId) {
  const out = []
  const stack = [...(childIndex.value.get(envId) || [])]
  while (stack.length) {
    const e = stack.pop()
    if (e.type === 'account' || e.type === 'custom') out.push(e)
    stack.push(...(childIndex.value.get(e.id) || []))
  }
  return out
}

function ipGroups(envId) {
  const accs = envAccounts(envId)
  const m = new Map()
  for (const a of accs) {
    const ip = a.fields?.ip || '（未填 IP）'
    if (!m.has(ip)) m.set(ip, [])
    m.get(ip).push(a)
  }
  return [...m.entries()].map(([ip, list]) => ({ip, accounts: list}))
}

// 左树某环境是否展开（选中该环境或其下任一 IP 时展开）
function envOpen(envId) {
  if (store.pjFocus === envId) return true
  if (!store.pjIp) return false
  const groups = ipGroups(envId)
  return groups.some((g) => g.ip === store.pjIp)
}

function focusEnv(id) {
  store.pjFocus = id
  store.pjIp = ''
}

function focusIp(ip, envId) {
  store.pjFocus = envId
  store.pjIp = ip
}

/* ---- 搜索过滤（面板内过滤 IP / 账号） ---- */
const search = ref('')
const kw = computed(() => search.value.trim().toLowerCase())
function kwHit(...vals) {
  if (!kw.value) return true
  return vals.join('\n').toLowerCase().includes(kw.value)
}

/* ---- 列配置（节点级，localStorage，不上传不参与同步） ---- */
const allCols = ['ip', 'info', 'ssh', 'accounts']
const extCols = ['port', 'remark', 'updated']
const colLabels = {
  ip: 'IP', info: '关联信息', ssh: '快速 SSH 命令', accounts: '账号',
  port: '端口', remark: '备注', updated: '最后修改',
}
const colCfgOpen = ref(false)

const colCfgKey = computed(() =>
  store.pjIp ? ('pj_colcfg_ip_' + store.pjIp) : ('pj_colcfg_' + (store.pjFocus || proj.value?.id || '__all'))
)

const colCfg = computed(() => {
  try {
    const raw = localStorage.getItem(colCfgKey.value)
    const cfg = raw ? JSON.parse(raw) : null
    if (cfg && Array.isArray(cfg.cols) && cfg.cols.length) return cfg
  } catch { /* 损坏配置回退默认 */ }
  return {cols: ['ip', 'info', 'ssh', 'accounts']}
})

function toggleCol(col) {
  const cols = [...colCfg.value.cols]
  const i = cols.indexOf(col)
  if (i >= 0) cols.splice(i, 1)
  else cols.push(col)
  if (!cols.length) cols.push('ip')
  try {
    localStorage.setItem(colCfgKey.value, JSON.stringify({cols}))
  } catch { /* localStorage 不可用时仅本次生效 */ }
}

/* ---- 右侧面板（层级自适应） ---- */
const pane = computed(() => {
  if (store.pjIp) {
    // 选中 IP：账号清单（group 存在性由下方 watch 保证回退；此处防御性兜底）
    const group = ipGroups(store.pjFocus).find((g) => g.ip === store.pjIp)
    if (group) {
      const accs = group.accounts
        .filter((a) => a.type === 'account' && kwHit(a.title, a.fields?.username || '', a.fields?.remark || ''))
      return {
        title: store.pjIp + ' · 账号清单',
        sub: `${accs.length} 个账号`,
        kind: 'account',
        accs,
      }
    }
    // group 不存在（entries 尚未刷新到位）：临时降级为空清单，避免崩溃；watch 会随后回退
    return {
      title: store.pjIp + ' · 账号清单',
      sub: '0 个账号',
      kind: 'account',
      accs: [],
    }
  }
  if (store.pjFocus) {
    // 选中环境：IP 清单
    const env = entries.value.find((e) => e.id === store.pjFocus)
    const groups = ipGroups(store.pjFocus)
      .filter((g) => kwHit(g.ip, ...g.accounts.map((a) => a.title + ' ' + (a.fields?.username || '') + ' ' + (a.fields?.remark || ''))))
    return {
      title: (env?.title || '') + ' · IP 清单',
      sub: `${groups.length} 个 IP · ${groups.reduce((s, g) => s + g.accounts.filter((a) => a.type === 'account').length, 0)} 个账号`,
      kind: 'ip',
      groups,
    }
  }
  // 项目本身：环境汇总
  const envRows = envs.value
    .map((env) => {
      const groups = ipGroups(env.id)
      return {env, ipCnt: groups.length, accCnt: groups.reduce((s, g) => s + g.accounts.filter((a) => a.type === 'account').length, 0)}
    })
    .filter((r) => kwHit(r.env.title))
  return {
    title: (proj.value?.title || '密码本') + ' · 环境汇总',
    sub: `${envRows.length} 个环境`,
    kind: 'env',
    envRows,
  }
})

// SSH 主账号：优先 root，否则第一个
function sshUser(accs) {
  return accs.find((a) => a.fields?.username === 'root') || accs[0]
}

/* ---- 账号密码弹窗 ---- */
const accModal = ref(null) // {entry, path, revealed}
const accModalRef = ref(null)
useModalFocus(accModalRef, computed(() => !!accModal.value))

let pwTimer = null

function showAccount(id) {
  const e = entries.value.find((x) => x.id === id)
  if (!e) return
  const path = []
  let p = e.parent_id
  while (p) {
    const n = idIndex.value.get(p)
    if (!n) break
    path.unshift(n)
    p = n.parent_id
  }
  accModal.value = {entry: e, path, revealed: false}
  clearTimeout(pwTimer)
}

function revealPw() {
  if (!accModal.value) return
  accModal.value.revealed = !accModal.value.revealed
  if (accModal.value.revealed) {
    // 显示 20 秒后自动重新掩码
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

// 编辑入口：始终以最新 entries 中的条目打开（弹窗持有的是旧引用，删除/同步后可能过期）。
// 注意：关闭弹窗必须放在「取到条目之后」——若模板写成 `closeAccModal(); openEditFresh(accModal.entry)`，
// accModal 会先被置空，随后读取 .entry 抛 TypeError 使编辑入口失效（实测复现）。
function openEditFresh(entry) {
  if (!entry) return closeAccModal()
  const fresh = entries.value.find((x) => x.id === entry.id) || entry
  closeAccModal()
  store.openEdit(fresh)
}

// 删除入口：与编辑入口同理——先取出条目再关弹窗（顺序颠倒会读到已置空的 accModal）。
// 实际删除走 store.askDelete → 确认弹窗（含子树条数）→ 级联墓碑，推送后同步给其他成员。
function askDeleteFromModal() {
  const entry = accModal.value?.entry
  closeAccModal()
  if (entry) store.askDelete(entry)
}

const PW_RE = /pass|secret|token|pwd|密码/i
function pwFieldOf(e) {
  return Object.entries(e.fields || {}).find(([k]) => PW_RE.test(k))
}

/* ---- IP 详情弹窗 ---- */
const ipModal = ref(null) // {ip, path, envs, accs, customs}
const ipModalRef = ref(null)
useModalFocus(ipModalRef, computed(() => !!ipModal.value))

function showIpDetail(ip) {
  const list = entries.value
    .filter((e) => (e.type === 'account' || e.type === 'custom') && (e.fields?.ip || '（未填 IP）') === ip)
    .map((e) => {
      const path = []
      let p = e.parent_id
      while (p) {
        const n = idIndex.value.get(p)
        if (!n) break
        path.unshift(n)
        p = n.parent_id
      }
      return {entry: e, path}
    })
  if (!list.length) {
    store.toast('未找到该 IP', 'error')
    return
  }
  ipModal.value = {
    ip,
    path: list[0].path.map((n) => n.title).join(' › '),
    envs: [...new Set(list.map((it) => it.path.find((n) => n.type === 'env')?.title).filter(Boolean))],
    accs: list.map((it) => it.entry).filter((e) => e.type === 'account'),
    customs: list.map((it) => it.entry).filter((e) => e.type === 'custom'),
  }
}

/* ---- 同步 ---- */
async function syncNow() {
  try {
    await store.syncAll()
    store.toast('同步完成', 'success')
  } catch (e) {
    store.toast(String(e.message || e), 'error')
  }
}

/* ---- Esc 关闭弹窗 / 清理 ---- */
const onKeydown = (e) => {
  if (e.key !== 'Escape') return
  if (accModal.value) return closeAccModal()
  if (ipModal.value) ipModal.value = null
  if (colCfgOpen.value) colCfgOpen.value = false
}
if (typeof document !== 'undefined') {
  document.addEventListener('keydown', onKeydown)
}
onBeforeUnmount(() => {
  if (typeof document !== 'undefined') {
    document.removeEventListener('keydown', onKeydown)
  }
  clearTimeout(pwTimer)
  clearTimeout(copyText._t)
})

// 条目变化后若聚焦的 IP / 环境已不存在则回退
watch(() => store.entries, () => {
  if (store.pjIp && store.pjFocus) {
    const still = ipGroups(store.pjFocus).some((g) => g.ip === store.pjIp)
    if (!still) store.pjIp = ''
  }
  if (store.pjFocus && !entries.value.find((e) => e.id === store.pjFocus)) {
    store.pjFocus = ''
    store.pjIp = ''
  }
})
</script>

<template>
  <div class="pj-view">
    <!-- 顶部：项目 tab -->
    <div class="pj-tabs">
      <div
          v-for="p in projects"
          :key="p.id"
          class="pj-tab"
          :class="{'pj-tab--on': p.id === proj?.id}"
          @click="switchProject(p.id)"
      >{{ p.title }}</div>
      <div class="pj-tab pj-tab--new" title="新建项目" @click="store.openNew({type: 'project'})">＋ 新项目</div>
    </div>

    <!-- 工具栏 -->
    <div class="pj-toolbar">
      <div class="pj-toolbar__right">
        <span v-if="store.syncBadge" class="pb-badge" :class="`pb-badge--${store.syncBadge.type}`">
          <span class="pb-dot" :class="`pb-dot--${store.syncBadge.type === 'success' ? 'ok' : store.syncBadge.type === 'danger' ? 'err' : store.syncBadge.type === 'warning' ? 'warn' : 'idle'}`"></span>
          {{ store.syncBadge.text }}
        </span>
        <!-- 上下文感知新建按钮：随选中层级变化 -->
        <button v-if="store.pjIp" class="pb-btn pb-btn--primary pb-btn--sm" @click="store.openNew({type: 'account', parentEnvId: store.pjFocus, prefillIp: store.pjIp})">＋ 在此 IP 新建账号</button>
        <button v-else-if="store.pjFocus" class="pb-btn pb-btn--primary pb-btn--sm" @click="store.openNew({type: 'account', parentEnvId: store.pjFocus})">＋ 在此环境新建账号</button>
        <button v-else-if="proj" class="pb-btn pb-btn--primary pb-btn--sm" @click="store.openNew({type: 'env', parentProjId: proj.id})">＋ 新建环境</button>
        <button v-else class="pb-btn pb-btn--primary pb-btn--sm" @click="store.openNew({type: 'project'})">＋ 新建项目</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="syncNow">⟳ 同步</button>
        <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('workbench')">🏠 工作台</button>
        <button v-if="store.isAdmin" class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('admin')">🛡 管理</button>
        <button class="pb-iconbtn" title="设置" @click="store.goto('settings')">⚙</button>
        <button class="pb-iconbtn" title="锁定" @click="store.lock()">⏻</button>
      </div>
    </div>

    <!-- 主体：左子树 + 右面板 -->
    <div class="pj-body">
      <div class="pj-subtree">
        <div class="pj-subtree__title">
          <span class="pb-truncate">{{ proj?.title || '' }}</span>
          <span v-if="proj" class="pj-tree-row__acts">
            <span
                class="pj-act"
                title="重命名 / 编辑此项目"
                @click.stop="openEditFresh(proj)"
            >✏</span>
            <span
                class="pj-act pj-act--danger"
                title="删除此项目（连同其环境与账号）"
                @click.stop="store.askDelete(proj)"
            >🗑</span>
          </span>
        </div>
        <template v-if="envs.length">
          <template v-for="env in envs" :key="env.id">
            <div
                class="pj-tree-row"
                :class="{'pj-tree-row--on': store.pjFocus === env.id}"
                @click="focusEnv(env.id)"
            >
              <span class="pj-tree-row__caret">{{ envOpen(env.id) ? '▾' : '▸' }}</span>
              <span class="pb-truncate">{{ env.title }}</span>
              <span class="pj-tree-row__cnt">
                {{ ipGroups(env.id).length }} IP · {{ ipGroups(env.id).reduce((s, g) => s + g.accounts.filter((a) => a.type === 'account').length, 0) }} 账号
              </span>
              <span class="pj-tree-row__acts">
                <span
                    class="pj-act"
                    title="在此环境新建账号"
                    @click.stop="store.openNew({type: 'account', parentEnvId: env.id})"
                >＋</span>
                <span
                    class="pj-act"
                    title="重命名 / 编辑此环境"
                    @click.stop="openEditFresh(env)"
                >✏</span>
                <span
                    class="pj-act pj-act--danger"
                    title="删除此环境（连同其下账号）"
                    @click.stop="store.askDelete(env)"
                >🗑</span>
              </span>
            </div>
            <template v-if="envOpen(env.id)">
              <div
                  v-for="g in ipGroups(env.id)"
                  :key="g.ip"
                  class="pj-tree-row"
                  :class="{'pj-tree-row--on': store.pjIp === g.ip}"
                  style="padding-left: 30px"
                  @click="focusIp(g.ip, env.id)"
              >
                <span class="pj-tree-row__caret">·</span>
                <span class="pb-mono pb-truncate" style="font-size: 11.5px">{{ g.ip }}</span>
                <span class="pj-tree-row__cnt">{{ g.accounts.filter((a) => a.type === 'account').length }}</span>
                <span class="pj-tree-row__acts">
                  <span
                      class="pj-act"
                      title="在此 IP 新建账号"
                      @click.stop="store.openNew({type: 'account', parentEnvId: env.id, prefillIp: g.ip === '（未填 IP）' ? '' : g.ip})"
                  >＋</span>
                </span>
              </div>
            </template>
          </template>
        </template>
        <!-- 空状态文案随上下文：无项目时工具栏主按钮是「＋ 新建项目」（见 .pj-toolbar__right 的 v-if 链） -->
        <div v-else class="pj-empty">
          {{ proj ? '暂无环境' : '暂无项目' }}<br>
          <span style="font-size: 11.5px">点上方「{{ proj ? '＋ 新建环境' : '＋ 新建项目' }}」创建{{ proj ? '第一个环境' : '第一个项目' }}</span>
        </div>
      </div>

      <div class="pj-pane">
        <div class="pj-pane__head">
          <div>
            <div class="pj-pane__title">{{ pane.title }}</div>
            <div class="pj-pane__sub">{{ pane.sub }}</div>
          </div>
          <div class="pj-pane__right">
            <input
                v-model="search"
                class="pj-pane__filter"
                placeholder="🔍 过滤 IP / 账号"
                spellcheck="false"
            />
            <div class="pj-colcfg-pop">
              <button class="pj-colcfg-btn" @click="colCfgOpen = !colCfgOpen">⚙ 列配置</button>
              <div v-if="colCfgOpen" class="pj-colcfg-panel">
                <div class="pj-colcfg-panel__title">列显示（保存到此节点 · 仅本地）</div>
                <label
                    v-for="c in [...allCols, ...extCols]"
                    :key="c"
                    class="pj-colcfg-row"
                    @click.prevent="toggleCol(c)"
                >
                  <input type="checkbox" :checked="colCfg.cols.includes(c)"/>
                  <span>{{ colLabels[c] }}</span>
                </label>
                <div class="pj-colcfg-hint">配置保存在本机（不上传服务器、不参与同步）</div>
              </div>
            </div>
          </div>
        </div>

        <div class="pj-pane__body">
          <!-- 环境汇总表 -->
          <table v-if="pane.kind === 'env'" class="pj-table">
            <thead>
            <tr>
              <th style="width: 160px">环境</th>
              <th>IP 数</th>
              <th>账号数</th>
            </tr>
            </thead>
            <tbody>
            <tr v-if="!pane.envRows.length">
              <td colspan="3"><div class="pj-empty">无匹配</div></td>
            </tr>
            <tr
                v-for="r in pane.envRows"
                :key="r.env.id"
                style="cursor: pointer"
                @click="focusEnv(r.env.id)"
            >
              <td>{{ r.env.title }}</td>
              <td>{{ r.ipCnt }}</td>
              <td>{{ r.accCnt }}</td>
            </tr>
            </tbody>
          </table>

          <!-- IP 清单表 -->
          <template v-else-if="pane.kind === 'ip'">
            <div v-if="!pane.groups.length" class="pj-empty">
              无匹配 IP<br>
              <span style="font-size: 11.5px">左侧选择环境后，点 <b style="color: var(--accent)">＋</b> 可在此环境新建账号</span>
            </div>
            <table v-else class="pj-table">
              <thead>
              <tr>
                <th v-if="colCfg.cols.includes('ip')" style="width: 110px">IP</th>
                <th v-if="colCfg.cols.includes('info')">关联信息</th>
                <th v-if="colCfg.cols.includes('ssh')" style="width: 170px">快速 SSH 命令</th>
                <th v-if="colCfg.cols.includes('accounts')">账号（点击查看密码）</th>
                <th v-if="colCfg.cols.includes('port')" style="width: 70px">端口</th>
                <th v-if="colCfg.cols.includes('remark')">备注</th>
                <th v-if="colCfg.cols.includes('updated')" style="width: 130px">最后修改</th>
                <th style="width: 56px"></th>
              </tr>
              </thead>
              <tbody>
              <tr v-for="g in pane.groups" :key="g.ip">
                <td v-if="colCfg.cols.includes('ip')" class="pb-mono">
                  {{ g.ip }}<span v-if="g.accounts.some((a) => a.dirty || a.conflict_of)" class="pj-dirty-dot" title="有未推送/冲突条目"></span>
                </td>
                <td v-if="colCfg.cols.includes('info')" style="color: var(--text-2)">
                  {{ g.accounts.map((a) => a.fields?.remark || '').filter(Boolean)[0] || '—' }}
                </td>
                <td v-if="colCfg.cols.includes('ssh')">
                  <span class="pj-ssh">
                    <span class="pj-ssh__cmd">ssh {{ sshUser(g.accounts.filter((a) => a.type === 'account'))?.fields?.username || 'root' }}@{{ g.ip }}</span>
                    <button
                        class="pb-iconbtn"
                        style="width: 22px; height: 22px; font-size: 12px"
                        title="复制命令"
                        @click="copyText(`ssh ${sshUser(g.accounts.filter((a) => a.type === 'account'))?.fields?.username || 'root'}@${g.ip}`)"
                    >⧉</button>
                  </span>
                </td>
                <td v-if="colCfg.cols.includes('accounts')">
                  <span class="pj-chips">
                    <span
                        v-for="a in g.accounts.filter((x) => x.type === 'account').slice(0, 4)"
                        :key="a.id"
                        class="pj-chip"
                        :title="a.fields?.remark || a.title"
                        @click="showAccount(a.id)"
                    >{{ a.fields?.username || a.title }}</span>
                    <span
                        v-if="g.accounts.filter((x) => x.type === 'account').length > 4"
                        class="pj-chip pj-chip--more"
                        title="点击 IP 查看全部账号"
                        @click="focusIp(g.ip, store.pjFocus)"
                    >+{{ g.accounts.filter((x) => x.type === 'account').length - 4 }}</span>
                  </span>
                </td>
                <td v-if="colCfg.cols.includes('port')" class="pb-mono" style="color: var(--text-2)">
                  {{ g.accounts.map((a) => a.fields?.port || '').filter(Boolean)[0] || '—' }}
                </td>
                <td v-if="colCfg.cols.includes('remark')" style="color: var(--text-2)">
                  {{ g.accounts.map((a) => a.fields?.remark || '').filter(Boolean).join('；') || '—' }}
                </td>
                <td v-if="colCfg.cols.includes('updated')" style="color: var(--text-3); font-size: 11.5px">
                  {{ fmtTime(Math.max(0, ...g.accounts.map((a) => a.updated_at || 0))) }}
                </td>
                <td>
                  <button class="pj-detail-btn" @click="showIpDetail(g.ip)">详情</button>
                </td>
              </tr>
              </tbody>
            </table>
          </template>

          <!-- 账号清单表（选中 IP 后） -->
          <template v-else>
            <div v-if="!pane.accs.length" class="pj-empty">
              无匹配账号<br>
              <span style="font-size: 11.5px">点下方按钮在此 IP 新建第一个账号</span>
            </div>
            <table v-else class="pj-table">
              <thead>
              <tr>
                <th style="width: 110px">账号</th>
                <th>用途</th>
                <th>密码</th>
                <th style="width: 56px"></th>
              </tr>
              </thead>
              <tbody>
              <tr v-for="a in pane.accs" :key="a.id">
                <td class="pb-mono">{{ a.fields?.username || a.title }}</td>
                <td style="color: var(--text-2)">{{ a.fields?.remark || '—' }}</td>
                <td class="pb-mono" style="color: var(--text-3)">••••••••</td>
                <td>
                  <button class="pj-detail-btn" @click="showAccount(a.id)">查看</button>
                </td>
              </tr>
              </tbody>
            </table>
            <div
                class="pj-add-row"
                @click="store.openNew({type: 'account', parentEnvId: store.pjFocus, prefillIp: store.pjIp})"
            >＋ 在此 IP 添加账号</div>
          </template>
        </div>
      </div>
    </div>

    <!-- 账号密码弹窗 -->
    <div v-if="accModal" class="pb-modal-mask" @click.self="closeAccModal">
      <div ref="accModalRef" class="pb-modal pb-glass pb-glass--strong" role="dialog" aria-modal="true">
        <div class="pb-modal__head">
          <span class="pb-modal__title">账号 · {{ accModal.entry.fields?.username || accModal.entry.title }}</span>
          <button class="pb-iconbtn" @click="closeAccModal">✕</button>
        </div>
        <div class="pb-modal__body">
          <div class="pb-xs pb-muted">
            {{ [accModal.path.find((n) => n.type === 'env')?.title, accModal.entry.fields?.ip].filter(Boolean).join(' · ') }}
            {{ accModal.entry.fields?.remark ? ' · ' + accModal.entry.fields.remark : '' }}
          </div>
          <div v-for="(v, k) in accModal.entry.fields" :key="k">
            <div v-if="!PW_RE.test(k)" class="detail-field">
              <span class="detail-field__label">{{ k }}</span>
              <div class="detail-field__value">
                <span class="pb-truncate pb-fill pb-mono">{{ v }}</span>
                <button class="pb-iconbtn" title="复制" @click="copyText(v)">⧉</button>
              </div>
            </div>
          </div>
          <template v-if="pwFieldOf(accModal.entry)">
            <div class="detail-field">
              <span class="detail-field__label">{{ pwFieldOf(accModal.entry)[0] }}</span>
              <div class="detail-field__value">
                <span class="pb-truncate pb-fill pb-mono">{{ accModal.revealed ? pwFieldOf(accModal.entry)[1] : '••••••••' }}</span>
                <button class="detail-field__reveal" title="显示/隐藏" @click="revealPw">👁</button>
                <button class="pb-iconbtn" title="复制" @click="copyText(pwFieldOf(accModal.entry)[1])">⧉</button>
              </div>
            </div>
            <p class="pb-xs pb-muted" style="font-size: 12px">密码显示 20 秒后自动重新掩码 · 复制后 30 秒自动清空剪贴板</p>
          </template>
        </div>
        <div class="pb-modal__foot">
          <button
              class="pb-btn pb-btn--danger"
              @click="askDeleteFromModal"
          >🗑 删除此账号</button>
          <button
              class="pb-btn pb-btn--ghost"
              @click="openEditFresh(accModal.entry)"
          >✏ 编辑此账号</button>
          <button
              class="pb-btn pb-btn--primary"
              @click="copyText(`${accModal.entry.fields?.username || accModal.entry.title}\n${pwFieldOf(accModal.entry)?.[1] || ''}`)"
          >⧉ 复制账号+密码</button>
        </div>
      </div>
    </div>

    <!-- IP 详情弹窗 -->
    <div v-if="ipModal" class="pb-modal-mask" @click.self="ipModal = null">
      <div ref="ipModalRef" class="pb-modal pb-glass pb-glass--strong" role="dialog" aria-modal="true">
        <div class="pb-modal__head">
          <span class="pb-modal__title">IP 详情 · {{ ipModal.ip }}</span>
          <button class="pb-iconbtn" @click="ipModal = null">✕</button>
        </div>
        <div class="pb-modal__body">
          <div class="pb-xs pb-muted">归属：{{ ipModal.path }}</div>
          <div class="pb-xs pb-muted">
            环境：{{ ipModal.envs.join('、') || '—' }} · {{ ipModal.accs.length }} 个账号
            <template v-if="ipModal.customs.length"> · {{ ipModal.customs.length }} 张补充卡片</template>
          </div>
          <hr class="pb-divider"/>
          <table class="pj-table">
            <thead>
            <tr>
              <th style="width: 100px">账号</th>
              <th>用途</th>
              <th>密码</th>
              <th style="width: 170px"></th>
            </tr>
            </thead>
            <tbody>
            <tr v-for="a in ipModal.accs" :key="a.id">
              <td class="pb-mono">{{ a.fields?.username || a.title }}</td>
              <td style="color: var(--text-2)">{{ a.fields?.remark || '—' }}</td>
              <td class="pb-mono" style="color: var(--text-3)">••••••••</td>
              <td>
                <button class="pj-detail-btn" @click="ipModal = null; showAccount(a.id)">查看密码</button>
              </td>
            </tr>
            </tbody>
          </table>
          <template v-if="ipModal.customs.length">
            <hr class="pb-divider"/>
            <div style="font-size: 12.5px; font-weight: 600; color: var(--text-1); margin-bottom: 6px">补充卡片</div>
            <div v-for="c in ipModal.customs" :key="c.id">
              <div v-for="(v, k) in c.custom_fields" :key="k" class="detail-field" style="margin-bottom: 6px">
                <span class="detail-field__label">{{ k }}</span>
                <div class="detail-field__value">
                  <span class="pb-truncate pb-fill pb-mono">{{ v }}</span>
                </div>
              </div>
            </div>
          </template>
        </div>
        <div class="pb-modal__foot">
          <button class="pb-btn pb-btn--ghost" @click="ipModal = null">关闭</button>
          <button
              class="pb-btn pb-btn--primary"
              @click="copyText(`ssh ${sshUser(ipModal.accs)?.fields?.username || 'root'}@${ipModal.ip}`)"
          >⧉ 复制 SSH 命令</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
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
</style>
