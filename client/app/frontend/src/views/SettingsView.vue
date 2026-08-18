<script setup>
import {ref, computed, onMounted} from 'vue'
import {useAppStore} from '../store'
import {api} from '../api'

const store = useAppStore()

const serverURL = ref('')
const saving = ref(false)
const verifying = ref(false)
const tip = ref('')
const tipType = ref('info') // info | ok | err

onMounted(async () => {
  serverURL.value = store.serverURL || ''
  await store.refreshStatus()
})

const serverInvalid = computed(() => {
  const u = serverURL.value.trim()
  if (!u) return '请输入服务端地址'
  if (!/^https?:\/\/.+/.test(u)) return '地址需以 http:// 或 https:// 开头'
  return ''
})

async function verifyServer() {
  const err = serverInvalid.value
  if (err) { tip.value = err; tipType.value = 'err'; return }
  verifying.value = true
  tip.value = '正在验证服务端连通性…'
  tipType.value = 'info'
  try {
    await api.VerifyServer(serverURL.value.trim())
    tip.value = '服务端连接正常'
    tipType.value = 'ok'
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    verifying.value = false
  }
}

async function saveServer() {
  const err = serverInvalid.value
  if (err) { tip.value = err; tipType.value = 'err'; return }
  saving.value = true
  try {
    await api.VerifyServer(serverURL.value.trim())
    await api.SetServerURL(serverURL.value.trim())
    store.serverURL = serverURL.value.trim()
    tip.value = '已保存，下次同步将使用新地址'
    tipType.value = 'ok'
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    saving.value = false
  }
}

const phaseText = computed(() => {
  const ph = store.status.phase
  if (ph === 'pulling') return '拉取中'
  if (ph === 'pushing') return '推送中'
  if (ph === 'rekey') return '组密钥升级'
  if (ph === 'offline') return '离线'
  return '空闲'
})

function fmtTime(ts) {
  if (!ts) return '从未'
  const d = new Date(ts * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

async function syncNow() {
  try {
    await store.syncNow()
    store.toast('同步完成', 'success')
  } catch (e) {
    store.toast(String(e.message || e), 'error')
  }
}

// 切换同步方式（auto=自动同步 | manual=手动同步）
async function setMode(mode) {
  try {
    await store.setSyncMode(mode)
    store.toast(
        mode === 'manual'
            ? '已切换为手动同步：编辑仅保存到本地，点「立即同步」才联网拉取/推送'
            : '已切换为自动同步：SSE 实时推送 + 保存后自动同步',
        'success'
    )
  } catch (e) {
    store.toast(String(e.message || e), 'error')
  }
}

async function toggleAutoUnlock() {
  try {
    if (store.autoUnlockEnabled) {
      await api.DisableAutoUnlock()
      store.autoUnlockEnabled = false
      store.toast('自动解锁已关闭', 'success')
    } else {
      await api.EnableAutoUnlock()
      store.autoUnlockEnabled = true
      store.toast('自动解锁已开启（下次启动/锁屏恢复免口令）', 'success')
    }
  } catch (e) {
    store.toast(String(e.message || e), 'error')
  }
}
</script>

<template>
  <div class="settings">
    <!-- 顶部 -->
    <div class="settings-head">
      <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.goto('list')">← 返回列表</button>
      <h2>设置</h2>
      <span class="pb-fill"></span>
      <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.lock()">⏻ 锁定</button>
    </div>

    <div class="settings-body">
      <!-- 服务端连接 -->
      <section class="settings-card pb-glass pb-glass--flat">
        <div class="settings-card__head">
          <span class="settings-card__icon">🌐</span>
          <div>
            <h3>服务端连接</h3>
            <p class="pb-xs pb-muted">修改后需重新验证，配置持久化在本地库（app_config）</p>
          </div>
        </div>
        <div class="settings-card__body">
          <div class="pb-field">
            <label class="pb-label">服务端地址</label>
            <div class="settings-row">
              <input
                  v-model="serverURL"
                  class="pb-input pb-input--mono"
                  placeholder="https://pb.example.com:8443"
                  spellcheck="false"
                  @keyup.enter="saveServer"
              />
              <button class="pb-btn pb-btn--ghost" :disabled="verifying" @click="verifyServer">
                <span v-if="verifying" class="pb-spinner pb-spinner--sm"></span>
                <span v-else>验证</span>
              </button>
            </div>
          </div>
          <div v-if="tip" class="settings-tip" :class="`settings-tip--${tipType}`">{{ tip }}</div>
          <div class="settings-actions">
            <button class="pb-btn pb-btn--primary" :disabled="saving || !!serverInvalid" @click="saveServer">
              <span v-if="saving" class="pb-spinner pb-spinner--sm"></span>
              <span>保存并应用</span>
            </button>
            <span class="pb-xs pb-muted">也可通过 <span class="pb-mono">--reinit</span> 启动参数强制进入首次初始化引导</span>
          </div>
        </div>
      </section>

      <!-- 同步状态 -->
      <section class="settings-card pb-glass pb-glass--flat">
        <div class="settings-card__head">
          <span class="settings-card__icon">⟳</span>
          <div>
            <h3>同步状态</h3>
            <p class="pb-xs pb-muted">
              {{ store.syncMode === 'manual' ? '手动同步：编辑仅保存本地，点「立即同步」才联网' : '自动同步：离线可编辑 · 联网后自动合并推送' }}
            </p>
          </div>
          <span class="pb-fill"></span>
          <span
              class="pb-badge"
              :class="store.status.connected ? 'pb-badge--success' : 'pb-badge--danger'"
          >
            <span class="pb-dot" :class="store.status.connected ? 'pb-dot--ok' : 'pb-dot--err'"></span>
            {{ store.status.connected ? '已连接' : (store.syncMode === 'manual' ? '手动' : '未连接') }}
          </span>
        </div>
        <div class="settings-card__body">
          <!-- 同步方式选择 -->
          <div class="settings-syncmode">
            <span class="pb-label">同步方式</span>
            <div class="settings-syncmode__options">
              <label class="settings-syncmode__opt" :class="{on: store.syncMode === 'auto'}">
                <input type="radio" name="syncmode" value="auto" :checked="store.syncMode === 'auto'" @change="setMode('auto')" />
                <span>自动同步</span>
                <span class="pb-xs pb-muted">SSE 实时推送 · 保存后自动同步</span>
              </label>
              <label class="settings-syncmode__opt" :class="{on: store.syncMode === 'manual'}">
                <input type="radio" name="syncmode" value="manual" :checked="store.syncMode === 'manual'" @change="setMode('manual')" />
                <span>手动同步</span>
                <span class="pb-xs pb-muted">关闭后台连接 · 点「立即同步」才拉取/推送</span>
              </label>
            </div>
          </div>

          <div class="settings-stats">
            <div class="settings-stat">
              <span class="settings-stat__val">{{ phaseText }}</span>
              <span class="settings-stat__label">同步阶段</span>
            </div>
            <div class="settings-stat">
              <span class="settings-stat__val">{{ store.status.groups?.length || 0 }}</span>
              <span class="settings-stat__label">参与分组</span>
            </div>
            <div class="settings-stat" :class="{'settings-stat--warn': store.status.pending_entries > 0}">
              <span class="settings-stat__val">{{ store.status.pending_entries }}</span>
              <span class="settings-stat__label">待接收信封</span>
            </div>
            <div class="settings-stat" :class="{'settings-stat--warn': store.status.dirty_count > 0}">
              <span class="settings-stat__val">{{ store.status.dirty_count }}</span>
              <span class="settings-stat__label">未推送修改</span>
            </div>
            <div class="settings-stat" :class="{'settings-stat--danger': store.status.bad_entries > 0}">
              <span class="settings-stat__val">{{ store.status.bad_entries }}</span>
              <span class="settings-stat__label">同步异常</span>
            </div>
          </div>
          <div class="settings-actions">
            <span class="pb-xs pb-muted">服务端 seq {{ store.status.server_seq }} · 上次拉取 {{ fmtTime(store.status.last_pull_at) }}</span>
            <span class="pb-fill"></span>
            <button
                class="pb-btn pb-btn--sm"
                :class="store.syncMode === 'manual' ? 'pb-btn--primary' : 'pb-btn--ghost'"
                @click="syncNow"
            >立即同步</button>
          </div>
        </div>
      </section>

      <!-- 外观 -->
      <section class="settings-card pb-glass pb-glass--flat">
        <div class="settings-card__head">
          <span class="settings-card__icon">🎨</span>
          <div>
            <h3>外观</h3>
            <p class="pb-xs pb-muted">玻璃拟态 · 深色科技感 / 简洁明亮双主题</p>
          </div>
        </div>
        <div class="settings-card__body">
          <div class="settings-theme">
            <button
                class="settings-theme__opt"
                :class="{'settings-theme__opt--active': store.theme === 'dark'}"
                @click="store.theme !== 'dark' && store.toggleTheme()"
            >
              <span class="settings-theme__swatch settings-theme__swatch--dark">🌙</span>
              <span>深色科技感</span>
            </button>
            <button
                class="settings-theme__opt"
                :class="{'settings-theme__opt--active': store.theme === 'light'}"
                @click="store.theme !== 'light' && store.toggleTheme()"
            >
              <span class="settings-theme__swatch settings-theme__swatch--light">☀</span>
              <span>简洁明亮</span>
            </button>
          </div>
        </div>
      </section>

      <!-- 数据与安全 -->
      <section class="settings-card pb-glass pb-glass--flat">
        <div class="settings-card__head">
          <span class="settings-card__icon">🛡</span>
          <div>
            <h3>数据与安全</h3>
            <p class="pb-xs pb-muted">本地数据目录 · 锁定策略</p>
          </div>
        </div>
        <div class="settings-card__body settings-body__stack">
          <div class="settings-info-row">
            <span class="settings-info-row__label">数据目录</span>
            <span class="pb-mono pb-truncate pb-fill" style="text-align:right">{{ store.dataDir }}</span>
          </div>
          <div class="settings-info-row">
            <span class="settings-info-row__label">自动锁定</span>
            <span class="pb-subtle pb-sm">失焦 5 分钟自动锁定，清除内存密钥</span>
          </div>
          <div class="settings-info-row settings-info-row--col">
            <span class="settings-info-row__label">自动解锁</span>
            <div class="settings-autounlock">
              <span class="pb-subtle pb-sm">
                Windows DPAPI 免口令解锁（绑定本机+当前账户；keyfile 导入/导出仍强制口令）
              </span>
              <button
                  class="pb-btn"
                  :class="store.autoUnlockEnabled ? 'pb-btn--ghost' : 'pb-btn--primary'"
                  @click="toggleAutoUnlock"
              >
                {{ store.autoUnlockEnabled ? '关闭' : '开启' }}
              </button>
            </div>
          </div>
          <div class="settings-info-row">
            <span class="settings-info-row__label">密钥存储</span>
            <span class="pb-subtle pb-sm">Zero-Knowledge：服务端仅存密文信封，明文永不上传</span>
          </div>
          <div class="settings-actions">
            <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="store.lock()">⏻ 立即锁定</button>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.settings {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 16px;
  gap: 14px;
}

.settings-head {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-shrink: 0;
}
.settings-head h2 {
  font-size: 19px;
  font-weight: 700;
}

.settings-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding-bottom: 2px;
}

.settings-card {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 18px 20px;
  flex-shrink: 0;
}
.settings-card__head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}
.settings-card__icon {
  font-size: 20px;
  line-height: 1.4;
}
.settings-card__head h3 {
  font-size: 15px;
  font-weight: 700;
}
.settings-card__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-left: 32px;
}
.settings-body__stack {
  gap: 8px;
}

.settings-row {
  display: flex;
  gap: 8px;
}
.settings-row .pb-input {
  flex: 1;
}

.settings-tip {
  font-size: 13px;
  padding: 9px 12px;
  border-radius: var(--radius-md);
  line-height: 1.6;
  word-break: break-all;
}
.settings-tip--info { background: var(--accent-soft); color: var(--accent); }
.settings-tip--ok { background: var(--success-soft); color: var(--success); }
.settings-tip--err { background: var(--danger-soft); color: var(--danger); }

.settings-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.settings-syncmode { display: flex; flex-direction: column; gap: 8px; }
.settings-syncmode__options { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
@media (max-width: 560px) { .settings-syncmode__options { grid-template-columns: 1fr; } }
.settings-syncmode__opt {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  cursor: pointer;
  transition: border-color .15s, background .15s;
}
.settings-syncmode__opt.on { border-color: var(--accent); background: var(--accent-soft); }
.settings-syncmode__opt input { cursor: pointer; }

.settings-stats {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 10px;
}
.settings-stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 12px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
}
.settings-stat__val {
  font-size: 18px;
  font-weight: 700;
  font-family: var(--font-mono);
}
.settings-stat__label {
  font-size: 11.5px;
  color: var(--text-3);
}
.settings-stat--warn .settings-stat__val { color: var(--warning); }
.settings-stat--danger .settings-stat__val { color: var(--danger); }

/* 窄窗响应式：统计卡 5 列 → 3 列 → 2 列 */
@media (max-width: 1080px) {
  .settings-stats { grid-template-columns: repeat(3, 1fr); }
}
@media (max-width: 720px) {
  .settings-stats { grid-template-columns: repeat(2, 1fr); }
}

.settings-theme {
  display: flex;
  gap: 10px;
}
.settings-theme__opt {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 16px;
  border-radius: var(--radius-md);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  font-size: 13px;
  font-weight: 600;
  transition: all var(--dur) var(--ease);
}
.settings-theme__opt:hover {
  border-color: var(--glass-border-strong);
}
.settings-theme__opt--active {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}
.settings-theme__swatch {
  width: 44px;
  height: 30px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 15px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--glass-border);
}
.settings-theme__swatch--dark {
  background: linear-gradient(135deg, #0b1226 0%, #0a1a33 100%);
  color: #38bdf8;
}
.settings-theme__swatch--light {
  background: linear-gradient(135deg, #ffffff 0%, #e6f0fb 100%);
  color: #0284c7;
}

.settings-info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 0;
  border-bottom: 1px dashed var(--glass-border);
}
.settings-info-row__label {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-2);
  flex-shrink: 0;
}

.settings-info-row--col {
  align-items: flex-start;
  flex-direction: column;
  gap: 6px;
}

.settings-autounlock {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
</style>
