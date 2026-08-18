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
    view: 'unlock', // unlock | list | edit | conflict | settings | admin
    editing: null, // 当前编辑/冲突条目
    serverURL: '',
    dataDir: '',
    adminMode: false, // 是否 --admin 启动（管理员模式：登录后进管理面板）
    isAdmin: false, // 当前用户是否管理员（决定是否显示管理面板入口）
    autoUnlockEnabled: false, // §9.1 自动解锁开关状态（Windows DPAPI）
    syncMode: 'auto', // 同步方式：auto（自动同步）| manual（手动同步）
    toasts: [],
    passwordGenOpen: false,
    deleteConfirm: null, // 待删除确认的 EntryView
    selectedId: '', // 列表页当前选中条目 id（视图切换后恢复选中态）
    expandedIds: [], // 树展开的节点 id（视图切换后保持展开状态）
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
        // 浏览器预览注入演示数据（仅 !inWails）
        if (!inWails) {
          __mockSeeds([
            {
              id: 'p1', group_id: 'g1', type: 'project', title: '企业基础设施',
              parent_id: null, fields: {}, custom_fields: {}, seq: 3, key_version: 1, updated_at: Date.now() / 1000, deleted: false,
            },
            {
              id: 'e1', group_id: 'g1', type: 'env', title: '生产环境',
              parent_id: 'p1', fields: {}, custom_fields: {}, seq: 5, key_version: 1, updated_at: Date.now() / 1000, deleted: false,
            },
            {
              id: 'a1', group_id: 'g1', type: 'account', title: 'admin',
              parent_id: 'e1', fields: {username: 'root', password: 'demo-password'}, custom_fields: {}, seq: 7, key_version: 1, updated_at: Date.now() / 1000, deleted: false,
            },
            {
              id: 'a2', group_id: 'g1', type: 'account', title: 'ops', parent_id: 'e1',
              fields: {username: 'opsuser', password: 'demo-password'}, custom_fields: {}, seq: 8, key_version: 1,
              updated_at: Date.now() / 1000, deleted: false, conflict_of: 'server', dirty: true,
            },
            {
              id: 'it1', group_id: 'g1', type: 'ip_type', title: '内网 IP', parent_id: 'e1',
              fields: {}, custom_fields: {}, seq: 9, key_version: 1, updated_at: Date.now() / 1000, deleted: false,
            },
            {
              id: 'at1', group_id: 'g1', type: 'acc_type', title: '运维账号', parent_id: 'it1',
              fields: {}, custom_fields: {}, seq: 10, key_version: 1, updated_at: Date.now() / 1000, deleted: false,
            },
            {
              id: 'a3', group_id: 'g1', type: 'account', title: 'deploy', parent_id: 'at1',
              fields: {username: 'deploy', password: 'demo-password', ip: '10.0.0.7'}, custom_fields: {}, seq: 11, key_version: 1,
              updated_at: Date.now() / 1000, deleted: false,
            },
            {
              id: 'c1', group_id: 'g1', type: 'custom', title: '机房信息', parent_id: 'at1',
              fields: {}, custom_fields: {机房: '深圳', 带宽: '10G'}, seq: 12, key_version: 1,
              updated_at: Date.now() / 1000, deleted: false,
            },
          ])
        }
        const unlocked = await api.IsUnlocked()
        this.unlocked = unlocked
        if (unlocked) {
          this.view = 'list'
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

    async refreshEntries() {
      try {
        const list = await api.ListEntries()
        this.entries = list || []
        return true
      } catch (e) {
        this.entries = []
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
      // 管理员模式（--admin）+ admin 角色 → 直接进管理面板；否则进密码本
      this.view = (this.adminMode && this.isAdmin) ? 'admin' : 'list'
      await this.refreshEntries()
    },

    async lock() {
      await api.Lock()
      this.unlocked = false
      this.entries = []
      this.selectedId = ''
      this.expandedIds = []
      this.view = 'unlock'
    },

    // 树展开/收起切换（展开态受控于 store，跨视图保持）
    toggleExpand(id) {
      const i = this.expandedIds.indexOf(id)
      if (i >= 0) this.expandedIds.splice(i, 1)
      else this.expandedIds.push(id)
    },

    async syncNow() {
      await api.SyncNow()
      await this.refreshStatus()
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

    openConflict(entry) {
      this.editing = entry
      this.view = 'conflict'
    },

    // 请求删除：计算子树条目数（§2：删除项目 = 批量推送子树墓碑），供确认弹窗提示
    askDelete(entry) {
      let n = 1
      const count = (e) => {
        this.entries.filter((x) => !x.deleted && x.parent_id === e.id).forEach((c) => {
          n++
          count(c)
        })
      }
      count(entry)
      this.deleteConfirm = {...entry, subtree_count: n}
    },

    async confirmDelete() {
      const target = this.deleteConfirm
      if (!target) return
      try {
        // 收集子树全部 id（含自身），逐条墓碑删除（§2 删除级联约定）
        const ids = []
        const collect = (e) => {
          ids.push(e.id)
          this.entries.filter((x) => !x.deleted && x.parent_id === e.id).forEach(collect)
        }
        collect(target)
        for (const id of ids) await api.DeleteEntry(id)
        this.deleteConfirm = null
        await this.refreshEntries()
        this.toast(`已删除 ${ids.length} 条（同步后将同步到其他成员）`, 'success')
      } catch (e) {
        this.toast(String(e.message || e), 'error')
      }
    },

    goto(view) {
      this.view = view
    },
  },
})
