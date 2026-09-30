// mock.js —— **仅浏览器预览**（非 Wails 环境）用的演示数据。
//
// 单独成文件并由 store.bootstrap() 动态 import：
//   ① 演示条目与假口令不再打进交付主包（Vite 会切成独立 chunk，预览时才按需加载）；
//   ② 演示数据集中一处，便于核对"交付物里不该有的东西"。
// 口令一律使用假值（demo-password），禁止出现任何真实凭据。
export const seedEntries = [
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
]
