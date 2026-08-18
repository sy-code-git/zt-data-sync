// api.js — Wails 绑定统一入口。
// 生产：调用 wailsjs 自动生成的绑定（window.go.main.App.*）；
// 浏览器预览（vite dev 无 Wails 运行时）→ 降级到内存 mock，便于无 Go 环境走查 UI。
import * as WailsApp from '../wailsjs/go/main/App'

const isWails = () => typeof window !== 'undefined' && !!window.go

export const inWails = isWails()

// ---- 内存 mock（浏览器预览用，不含任何真实密钥/数据） ----
const mock = {
  serverURL: '',
  unlocked: false,
  entries: [],
  put(key, val) {
    this[key] = val
  },
  get(key) {
    return this[key]
  },
}

async function call(fn, ...args) {
  if (inWails) {
    return fn(...args)
  }
  // 浏览器预览：返回占位（UI 走查用）
  const n = fn.name
  switch (n) {
    case 'GetServerURL':
      return mock.get('serverURL')
    case 'SetServerURL':
      mock.put('serverURL', args[0])
      return null
    case 'GetCA':
      return mock.get('caPath') || ''
    case 'SetCA':
      mock.put('caPath', args[0])
      return null
    case 'VerifyServer':
      if (!args[0]) throw new Error('服务端地址为空')
      return null
    case 'ImportKeyfile':
      mock.put('unlocked', true)
      return { user_id: 'preview', device_id: 'preview-dev', groups: 0, need_register: false }
    case 'OpenFileDialog':
      return 'D:\\preview\\backup.key'
    case 'GenerateKeypair':
      mock.put('unlocked', true)
      return 'preview-pub-key-b64'
    case 'Username':
      return mock.get('username') || ''
    case 'Role':
      return mock.get('role') || ''
    case 'Unlock':
      mock.put('unlocked', true)
      return { user_id: 'preview', device_id: 'preview-dev', groups: 1, need_register: false }
    case 'RegisterDevice':
      mock.put('unlocked', true)
      return null
    case 'Bootstrap':
      mock.put('unlocked', true)
      return null
    case 'SetRegSecret':
      mock.put('regSecret', args[0])
      return null
    case 'HasRegSecret':
      return !!mock.get('regSecret')
    case 'AdminCreateUser':
      return 'preview-uid'
    case 'AdminCreateGroup':
      return 'preview-gid'
    case 'AdminAddMember':
      return null
    case 'AdminListGroups':
      return mock.get('groups') || []
    case 'AdminListUsers':
      return mock.get('users') || []
    case 'AdminListMembers':
      return mock.get('members') || []
    case 'AdminRemoveMember':
      return null
    case 'AdminListDevices':
      return mock.get('devices') || []
    case 'TryAutoUnlock':
      throw new Error('预览环境不支持自动解锁（Windows DPAPI）')
    case 'EnableAutoUnlock':
      mock.put('autoUnlock', true)
      return null
    case 'DisableAutoUnlock':
      mock.put('autoUnlock', false)
      return null
    case 'AutoUnlockEnabled':
      return !!mock.get('autoUnlock')
    case 'Lock':
      mock.put('unlocked', false)
      return null
    case 'IsUnlocked':
      return mock.get('unlocked')
    case 'ListEntries':
      // 与 core.ListEntries 一致：同层按 title 排序（树组装保序）
      return [...mock.get('entries')].sort((a, b) =>
          a.title === b.title ? (a.id < b.id ? -1 : 1) : (a.title < b.title ? -1 : 1))
    case 'GetEntry':
      return mock.get('entries').find((e) => e.id === args[0]) || null
    case 'PutEntry': {
      const req = args[0]
      const list = mock.get('entries')
      const now = Date.now() / 1000
      if (req.id) {
        // 更新：保留 parent_id/seq 等既有属性
        const i = list.findIndex((e) => e.id === req.id)
        if (i >= 0) {
          list[i] = {
            ...list[i],
            group_id: req.group_id, type: req.type, title: req.title,
            parent_id: req.parent_id ?? list[i].parent_id ?? null,
            fields: decodeFields(req.fields), custom_fields: decodeFields(req.custom_fields),
            updated_at: now, dirty: true, deleted: false,
          }
        }
      } else {
        // 新建：生成预览 id
        list.push({
          id: 'm' + Math.random().toString(36).slice(2, 10),
          group_id: req.group_id, type: req.type, title: req.title,
          parent_id: req.parent_id ?? null, fields: decodeFields(req.fields),
          custom_fields: decodeFields(req.custom_fields), seq: 1, key_version: 1,
          updated_at: now, deleted: false, dirty: true,
        })
      }
      return null
    }
    case 'DeleteEntry':
      mock.put('entries', mock.get('entries').filter((e) => e.id !== args[0]))
      return null
    case 'ResolveConflict': {
      // 预览：清除冲突标记，dirty 待推送；手动解决时按 manual 合并字段（模拟后端行为）
      const id = args[0]
      const manual = args[2]
      mock.put('entries', mock.get('entries').map((e) => {
        if (e.id !== id) return e
        if (manual) {
          return {
            ...e, conflict_of: '', dirty: true,
            title: manual.title ?? e.title,
            fields: manual.fields ? decodeFields(manual.fields) : e.fields,
            custom_fields: manual.custom_fields ? decodeFields(manual.custom_fields) : e.custom_fields,
          }
        }
        return {...e, conflict_of: '', dirty: true}
      }))
      return null
    }
    case 'GetConflict': {
      // 预览：模拟真实三路冲突——本地改 password，服务端改 username+password（双方改不同）
      const e = mock.get('entries').find((x) => x.id === args[0]) || {}
      const ts = Date.now() / 1000
      const baseFields = {...(e.fields || {})}
      const oursFields = {...baseFields, password: 'local-pass-123'}
      const theirsFields = {...baseFields, username: String(baseFields.username || '') + '_server', password: 'server-pass-456'}
      const custom = {...(e.custom_fields || {})}
      const common = {
        id: e.id || args[0], group_id: e.group_id || 'g1', type: e.type || 'account',
        title: e.title || '条目', parent_id: e.parent_id ?? null,
        seq: e.seq || 0, key_version: e.key_version || 1,
      }
      return {
        id: args[0],
        base: {...common, fields: baseFields, custom_fields: {...custom}, updated_at: ts - 7200},
        ours: {...common, fields: oursFields, custom_fields: {...custom}, updated_at: ts - 3600},
        theirs: {...common, fields: theirsFields, custom_fields: {...custom}, updated_at: ts},
      }
    }
    case 'SyncNow':
      return null
    case 'SyncMode':
      return mock.get('syncMode') || 'auto'
    case 'SetSyncMode':
      mock.put('syncMode', args[0])
      return null
    case 'Status': {
      // 预览：模拟已连接 + 组信息（与 mock 种子 g1 一致）+ 未推送计数
      const list = mock.get('entries')
      const dirtyCount = list.filter((e) => e.dirty && !e.deleted).length
      return {
        phase: 'idle', server_seq: 0, last_seq: 0, connected: true,
        groups: [{id: 'g1', name: '默认组', key_version: 1, pending_rekey: false, archived: false}],
        pending_entries: 0, bad_entries: 0, dirty_count: dirtyCount,
      }
    }
    case 'GeneratePassword':
      return 'pR3v!wQ9xK#mZ2nL'
    case 'DataDir':
      return '/tmp/.passbook'
    case 'IsReinit':
      return false
    case 'IsAdminMode':
      return false
    default:
      return null
  }
}

// 浏览器预览 mock 专用：后端字段值为 json.RawMessage，前端保存时会 JSON 编码字符串；
// 解码还原为普通值，保证预览中字段不显示字面引号。
function decodeFields(obj) {
  const out = {}
  for (const [k, v] of Object.entries(obj || {})) {
    if (typeof v === 'string') {
      try {
        out[k] = JSON.parse(v)
      } catch {
        out[k] = v
      }
    } else {
      out[k] = v
    }
  }
  return out
}

// ---- 明文（plaintext）与业务字段的转换（core 纯管道：明文是 opaque bytes，UI 自行序列化/解析） ----

// 明文字节 → UTF-8 字符串（兼容 base64 字符串 / number[] 字节数组 / 已是字符串）
function plainToUtf8(p) {
  if (p == null || p === '') return ''
  if (typeof p === 'string') {
    try {
      return decodeURIComponent(escape(atob(p)))
    } catch {
      return p // 可能已是 UTF-8 原文
    }
  }
  if (Array.isArray(p) || p instanceof Uint8Array) {
    try {
      return new TextDecoder().decode(new Uint8Array(p))
    } catch {
      return ''
    }
  }
  return ''
}

// UTF-8 字符串 → 明文字节（number[]；Wails 对 Go []byte 用字节数组传输）
function utf8ToPlain(s) {
  return Array.from(new TextEncoder().encode(s))
}

// 解析 EntryView.plaintext 为业务明文对象（type/title/parent_id/fields/custom_fields）
function parsePlaintext(p) {
  const s = plainToUtf8(p)
  if (!s) return {}
  try {
    return JSON.parse(s)
  } catch {
    return {}
  }
}

// 将业务明文对象序列化为 plaintext 字节（base64）
function encodePlaintext(obj) {
  return utf8ToPlain(JSON.stringify(obj || {}))
}

// ---- 包装成 Promise API ----
export const api = {
  GetServerURL: () => call(WailsApp.GetServerURL),
  SetServerURL: (url) => call(WailsApp.SetServerURL, url),
  VerifyServer: (url, caPath) => call(WailsApp.VerifyServer, url, caPath),
  ImportKeyfile: (path, username, role, password) => call(WailsApp.ImportKeyfile, path, username, role, password),
  ExportKeyfile: (path) => call(WailsApp.ExportKeyfile, path),
  OpenFileDialog: (title) => call(WailsApp.OpenFileDialog, title),
  GenerateKeypair: (username, role, password) => call(WailsApp.GenerateKeypair, username, role, password),
  Username: () => call(WailsApp.Username),
  Role: () => call(WailsApp.Role),
  Unlock: (username, password) => call(WailsApp.Unlock, username, password),
  TryAutoUnlock: () => call(WailsApp.TryAutoUnlock),
  EnableAutoUnlock: () => call(WailsApp.EnableAutoUnlock),
  DisableAutoUnlock: () => call(WailsApp.DisableAutoUnlock),
  AutoUnlockEnabled: () => call(WailsApp.AutoUnlockEnabled),
  Lock: () => call(WailsApp.Lock),
  IsUnlocked: () => call(WailsApp.IsUnlocked),
  ListEntries: async () => {
    const list = await call(WailsApp.ListEntries)
    return (list || []).map((ev) => ({...ev, ...parsePlaintext(ev.plaintext)}))
  },
  GetEntry: async (id) => {
    const ev = await call(WailsApp.GetEntry, id)
    return ev ? {...ev, ...parsePlaintext(ev.plaintext)} : ev
  },
  PutEntry: (req) => {
    if (inWails) {
      // 前端传业务明文对象 → 序列化为 plaintext 字节（core 纯管道）
      const plaintext = encodePlaintext({
        schema_version: 1,
        type: req.type,
        title: req.title,
        parent_id: req.parent_id ?? null,
        fields: req.fields || {},
        custom_fields: req.custom_fields || {},
      })
      return call(WailsApp.PutEntry, {id: req.id, group_id: req.group_id, plaintext})
    }
    return call(WailsApp.PutEntry, req) // mock 透传明文对象
  },
  DeleteEntry: (id) => call(WailsApp.DeleteEntry, id),
  ResolveConflict: (id, useLocal, manual) => {
    if (inWails) {
      return call(WailsApp.ResolveConflict, id, useLocal, manual ? encodePlaintext(manual) : null)
    }
    return call(WailsApp.ResolveConflict, id, useLocal, manual) // mock 透传明文对象
  },
  GetConflict: async (id) => {
    const d = await call(WailsApp.GetConflict, id)
    if (!d) return d
    return {
      id: d.id,
      base: d.base ? parsePlaintext(d.base) : null,
      ours: d.ours ? parsePlaintext(d.ours) : null,
      theirs: d.theirs ? parsePlaintext(d.theirs) : null,
    }
  },
  SyncNow: () => call(WailsApp.SyncNow),
  SyncMode: () => call(WailsApp.SyncMode),
  SetSyncMode: (mode) => call(WailsApp.SetSyncMode, mode),
  Status: () => call(WailsApp.Status),
  GeneratePassword: (length, upper, lower, digits, symbols, excludeAmbiguous) =>
    call(WailsApp.GeneratePassword, length, upper, lower, digits, symbols, excludeAmbiguous),
  DataDir: () => call(WailsApp.DataDir),
  IsReinit: () => call(WailsApp.IsReinit),
  IsAdminMode: () => call(WailsApp.IsAdminMode),
  SaveFileDialog: (title) => call(WailsApp.SaveFileDialog, title),
  GetCA: () => call(WailsApp.GetCA),
  SetCA: (caPath) => call(WailsApp.SetCA, caPath),
  RegisterDevice: (username, deviceName) => call(WailsApp.RegisterDevice, username, deviceName),
  Bootstrap: (username, password, bootstrapToken, name, deviceName) =>
    call(WailsApp.Bootstrap, username, password, bootstrapToken, name, deviceName),
  SetRegSecret: (regSecret) => call(WailsApp.SetRegSecret, regSecret),
  HasRegSecret: () => call(WailsApp.HasRegSecret),
  AdminCreateUser: (username, name, publicKey) => call(WailsApp.AdminCreateUser, username, name, publicKey),
  AdminCreateGroup: (name) => call(WailsApp.AdminCreateGroup, name),
  AdminArchiveGroup: (groupID, confirmName) => call(WailsApp.AdminArchiveGroup, groupID, confirmName),
  AdminUnarchiveGroup: (groupID) => call(WailsApp.AdminUnarchiveGroup, groupID),
  AdminAddMember: (groupID, userID) => call(WailsApp.AdminAddMember, groupID, userID),
  AdminListGroups: () => call(WailsApp.AdminListGroups),
  AdminListUsers: () => call(WailsApp.AdminListUsers),
  AdminListMembers: (groupID) => call(WailsApp.AdminListMembers, groupID),
  AdminRemoveMember: (groupID, userID, confirmName) => call(WailsApp.AdminRemoveMember, groupID, userID, confirmName),
  AdminListDevices: () => call(WailsApp.AdminListDevices),
}

// 前端模块给 mock 注入预览数据（仅浏览器预览）
export function __mockSeeds(entries) {
  mock.put('entries', entries)
}
