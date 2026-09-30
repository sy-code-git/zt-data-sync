// store.js — 全局状态（Pinia）。
// 前端不存敏感数据：条目明文仅驻留当前内存视图，锁定/退出即清空。
import {defineStore} from 'pinia'
import {api, inWails, __mockSeeds} from './api'

// 主题偏好（非敏感，存 localStorage；颜色变量挂在 :root[data-theme] 下，
// 启动时必须设置属性，否则全站无主题色）。
const savedTheme = (() => {
  try {
    const t = localStorage.getItem('pb_theme')
    return t === 'light' || t === 'dark' ? t : ''
  } catch {
    return ''
  }
})()
const initialTheme = savedTheme || 'dark'
if (typeof document !== 'undefined') {
  document.documentElement.setAttribute('data-theme', initialTheme)
}

export const useAppStore = defineStore('app', {
  state: () => ({
    theme: initialTheme, // 'dark' | 'light'
    unlocked: false,
    booting: true,
    entries: [], // api.EntryView[]
    status: {
      phase: 'idle',
      connected: false,
      groups: [],
      pending_entries: 0,
      bad_entries: 0,
      dirty_count: 0,
      server_seq: 0,
    },
    view: 'unlock', // unlock | workbench | list | edit | conflict | settings | admin
    editing: null, // 当前编辑/冲突条目
    newCtx: null, // 上下文感知新建：{type, parentProjId, parentEnvId, prefillIp}（编辑页消费后清空）
    serverURL: '',
    dataDir: '',
    adminMode: false, // 是否 --admin 启动（管理员模式：登录后进管理面板）
    isAdmin: false, // 当前用户是否管理员（决定是否显示管理面板入口）
    autoUnlockEnabled: false, // §9.1 自动解锁开关状态（Windows DPAPI）
    syncMode: 'auto', // 同步方式：auto（自动同步）| manual（手动同步）
    toasts: [],
    passwordGenOpen: false,
    deleteConfirm: null, // 待删除确认的 EntryView
    deleting: false, // 删除进行中（防重入：确认按钮可被连点）
    syncing: false, // 同步进行中（防重入：避免并发同步撞服务端限流）
    selectedId: '', // 列表页当前选中条目 id（视图切换后恢复选中态）
    expandedIds: [], // 树展开的节点 id（视图切换后保持展开状态）
    listSearch: '', // 列表页搜索词（工作台搜索框与列表页共用，跨视图保持）
    searchFocusTick: 0, // 递增即请求列表页聚焦搜索框（Ctrl/⌘+K）
    searchFocusHandled: 0, // 列表页已消费到的 tick（跨视图挂载时也能识别"有未处理的聚焦请求"）
    // 方案 J 浏览状态（跨视图保持）：当前项目 tab / 左树选中（env id 或 ''=项目本身）/ IP 聚焦
    pjProject: '', // 项目 id
    pjFocus: '', // 环境节点 id（'' = 项目本身 → 环境汇总）
    pjIp: '', // 聚焦的 IP 值（'' = 未聚焦 IP）
  }),

  getters: {
    isUnlockedView: (s) => s.unlocked,
    syncBadge: (s) => {
      if (!s.unlocked) return null
      const ph = s.status.phase
      if (ph === 'rekey') return {type: 'warning', text: '组密钥升级中'}
      if (s.status.dirty_count > 0) return {type: 'warning', text: `${s.status.dirty_count} 条待推送`}
      if (s.syncMode === 'manual') return {type: 'neutral', text: '手动同步'}
      if (ph === 'offline' || !s.status.connected) return {type: 'danger', text: '离线' }
      if (s.status.bad_entries > 0) return {type: 'danger', text: `${s.status.bad_entries} 条同步异常`}
      if (ph === 'pulling' || ph === 'pushing') return {type: 'accent', text: '同步中'}
      return {type: 'success', text: '已同步'}
    },
  },

  actions: {
    toggleTheme() {
      this.theme = this.theme === 'dark' ? 'light' : 'dark'
      document.documentElement.setAttribute('data-theme', this.theme)
      try {
        localStorage.setItem('pb_theme', this.theme)
      } catch {
        /* localStorage 不可用时忽略（仅失去持久化） */
      }
    },

    async bootstrap() {
      let initErr = ''
      try {
        const [saved, dataDir, reinit, autoUnlock, adminMode, syncMode] = await Promise.all([
          api.GetServerURL(),
          api.DataDir(),
          api.IsReinit(),
          api.AutoUnlockEnabled().catch(() => false),
          api.IsAdminMode().catch(() => false),
          api.SyncMode().catch(() => 'auto'),
        ])
        this.serverURL = reinit ? '' : (saved || '')
        this.dataDir = dataDir || ''
        this.autoUnlockEnabled = !!autoUnlock
        this.adminMode = !!adminMode
        this.syncMode = syncMode === 'manual' ? 'manual' : 'auto'
        // 浏览器预览注入演示数据（仅 !inWails；数据在 src/mock.js，动态加载以免打进交付主包）
        if (!inWails) {
          const {seedEntries} = await import('./mock')
          __mockSeeds(seedEntries)
        }
        const unlocked = await api.IsUnlocked()
        this.unlocked = unlocked
        if (unlocked) {
          this.view = 'workbench'
          await this.refreshEntries()
        } else {
          this.view = 'unlock'
        }
      } catch (e) {
        // 本地库未就绪等初始化异常：提示并停留在解锁页
        initErr = String(e.message || e)
        this.view = 'unlock'
      } finally {
        this.booting = false
        if (initErr) this.toast(initErr, 'error')
      }
    },

    async refreshEntries({toastErr = false} = {}) {
      try {
        const list = await api.ListEntries()
        this.entries = list || []
        return true
      } catch (e) {
        this.entries = []
        // 错误不吞：静默刷新（如后台 entries_changed 事件）不打断用户；主动刷新可提示
        if (toastErr) this.toast(`加载条目失败：${String(e.message || e)}`, 'error')
        return false
      }
    },

    async unlock(username, password) {
      const res = await api.Unlock(username, password)
      if (res && res.need_register) {
        // 首次使用：vault 已解锁但设备未注册，等注册完成后再进主界面（§9.1）
        this.unlocked = true
        return res
      }
      await this.enterList()
      return res
    },

    // 首次初始化（方案 A）：生成密钥对 + 加密存本地库 + 解锁，返回公钥（开户用）
    async generateKeypair(username, role, password) {
      return await api.GenerateKeypair(username, role, password)
    },

    // 首次注册设备（unlock 后 need_register 时调用，工号 + 设备名）
    async registerDevice(username, deviceName) {
      await api.RegisterDevice(username, deviceName)
      await this.enterList()
    },

    // §9.1 自动解锁：DPAPI 免口令（失败抛错由调用方回退口令）
    async tryAutoUnlock() {
      await api.TryAutoUnlock()
      await this.enterList()
    },

    // 进入主界面（解锁/注册完成后的公共逻辑：刷新状态 + 切列表 + 加载条目）
    async enterList() {
      this.unlocked = true
      try {
        this.isAdmin = (await api.Role()) === 'admin'
      } catch {
        this.isAdmin = false
      }
      await this.refreshStatus()
      // 管理员模式（--admin）+ admin 角色 → 直接进管理面板；否则进工作台
      this.view = (this.adminMode && this.isAdmin) ? 'admin' : 'workbench'
      await this.refreshEntries()
    },

    // 本地会话清理（不调后端）：凡"持有条目对象/会话视图态"的内存字段都必须在这里清空。
    // EntryView 的 Plaintext 就是明文 JSON（api.js 还会把明文展开到对象上），故新增任何
    // "暂存条目"的字段时，务必同步加进本函数——这是 §9.1「锁定即清空明文」的唯一落点。
    clearLocalSession() {
      this.unlocked = false
      this.entries = []
      this.editing = null
      this.deleteConfirm = null
      this.newCtx = null
      this.passwordGenOpen = false
      this.selectedId = ''
      this.expandedIds = []
      this.pjProject = ''
      this.pjFocus = ''
      this.pjIp = ''
      this.listSearch = ''
      this.searchFocusHandled = this.searchFocusTick // 消费掉未处理的聚焦请求，避免下次会话空聚焦
      this.isAdmin = false
      this.view = 'unlock'
    },

    async lock() {
      try {
        await api.Lock()
      } catch (e) {
        // 锁定失败也必须立刻清空本地内存态（安全兜底：内存密钥绝不因后端失败滞留）
        console.warn('调用后端 Lock 失败，已强制清空本地态:', e)
      }
      this.clearLocalSession()
    },

    // 树展开/收起切换（展开态受控于 store，跨视图保持）
    toggleExpand(id) {
      const i = this.expandedIds.indexOf(id)
      if (i >= 0) this.expandedIds.splice(i, 1)
      else this.expandedIds.push(id)
    },

    async syncNow() {
      if (this.syncing) return // 防重入：手抖连点不要发并发同步（服务端按 token 限流）
      this.syncing = true
      try {
        await api.SyncNow()
        await this.refreshStatus()
      } finally {
        this.syncing = false
      }
    },

    // 统一同步入口（同步 + 状态 + 条目刷新）：视图层只负责 toast，避免各视图重复实现
    async syncAll() {
      await this.syncNow()
      await this.refreshEntries()
    },

    // 切换同步方式（auto=自动同步 | manual=手动同步），持久化并即时生效
    async setSyncMode(mode) {
      const m = mode === 'manual' ? 'manual' : 'auto'
      await api.SetSyncMode(m)
      this.syncMode = m
      await this.refreshStatus()
    },

    async refreshStatus() {
      this.status = await api.Status()
    },

    toast(text, type = 'info') {
      const id = Date.now() + Math.random()
      this.toasts.push({id, text, type})
      setTimeout(() => {
        this.toasts = this.toasts.filter((t) => t.id !== id)
      }, 3600)
    },

    openEdit(entry) {
      this.editing = entry
      this.view = 'edit'
    },

    // 上下文感知新建（方案 J）：带归属/预填信息进编辑页
    openNew(ctx) {
      this.editing = null
      this.newCtx = ctx || {}
      this.view = 'edit'
    },

    openConflict(entry) {
      this.editing = entry
      this.view = 'conflict'
    },

    // 子树 id 收集（含自身）：一次遍历建父子索引，避免原实现"每层都 filter 全表"的 O(n²)
    subtreeIds(root) {
      const byParent = new Map()
      for (const e of this.entries) {
        if (e.deleted) continue
        const arr = byParent.get(e.parent_id)
        if (arr) arr.push(e)
        else byParent.set(e.parent_id, [e])
      }
      const ids = []
      const stack = [root]
      while (stack.length) {
        const cur = stack.pop()
        ids.push(cur.id)
        const kids = byParent.get(cur.id)
        if (kids) for (const k of kids) stack.push(k)
      }
      return ids
    },

    // 请求删除：计算子树条目数（§2：删除项目 = 批量推送子树墓碑），供确认弹窗提示
    askDelete(entry) {
      this.deleteConfirm = {...entry, subtree_count: this.subtreeIds(entry).length}
    },

    async confirmDelete() {
      if (this.deleting) return // 防重入：确认按钮可被连点，避免重复推送子树墓碑
      const target = this.deleteConfirm
      if (!target) return
      this.deleting = true
      try {
        // 收集子树全部 id（含自身），逐条墓碑删除（§2 删除级联约定）
        const ids = this.subtreeIds(target)
        for (const id of ids) await api.DeleteEntry(id)
        this.deleteConfirm = null
        await this.refreshEntries()
        this.toast(`已删除 ${ids.length} 条（同步后将同步到其他成员）`, 'success')
      } catch (e) {
        this.toast(String(e.message || e), 'error')
      } finally {
        this.deleting = false
      }
    },

    // Ctrl/⌘+K：打开列表页并聚焦搜索框（列表页 watch searchFocusTick 后聚焦）
    openSearch() {
      this.view = 'list'
      this.searchFocusTick += 1
    },

    goto(view) {
      this.view = view
    },
  },
})
