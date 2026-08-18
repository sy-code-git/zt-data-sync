<script setup>
import {ref, computed, onMounted} from 'vue'
import {api} from '../api'
import {useAppStore} from '../store'
import {OpenFileDialog} from '../../wailsjs/go/main/App'

const store = useAppStore()

// ---- 状态 ----
const serverURL = ref('')
const caPath = ref('') // 自签 CA 证书路径（§8.3；空 = 系统默认验证）
const username = ref('') // 工号（登录标识，唯一、不可改）
const password = ref('')
const showPass = ref(false)
const busy = ref(false)
const verifying = ref(false)
const hasSavedURL = ref(false)
const serverMode = ref('saved') // 'saved' | 'new'
const identityRole = ref('') // 本地身份角色：admin | member | ''（空=未初始化）
const forceLogin = ref(false) // 跳过注册，强制进入登录界面

// 导入私钥备份（跳过注册后，用已有私钥恢复身份登录）
const importOpen = ref(false)
const importKeyfilePath = ref('')
const importPass = ref('')

// 管理员首次部署（§6.3：服务端刚部署，bootstrap token 建首个 admin）
const adminToken = ref('')
const adminName = ref('')
const adminRegSecret = ref('')
const adminDeviceName = ref('')

// 首次注册设备（§9.1）
const needRegister = ref(false)
const regUsername = ref('')
const regDeviceName = ref('')
const generatedPub = ref('') // 首次初始化生成的公钥（交管理员开户用）

const tip = ref('')
const tipType = ref('info') // info | ok | err

// 自动解锁（§9.1，Windows DPAPI）
const autoBusy = ref(false)

// 页面模式：管理员部署 / 管理员登录 / 用户注册 / 用户登录
// 「是否首次」按角色独立判断：管理员模式只看本地是否已有 admin 身份，
// 普通用户模式只看本地是否已有 member 身份，互不干扰。
// forceLogin：用户在注册界面点「跳过」→ 强制进入登录界面（可导入私钥备份登录）。
const mode = computed(() => {
  if (store.adminMode) return identityRole.value === 'admin' ? 'adminLogin' : 'adminDeploy'
  if (forceLogin.value) return 'userLogin'
  return identityRole.value === 'member' ? 'userLogin' : 'userRegister'
})

const modeTitle = computed(() => ({
  adminDeploy: '管理员首次部署',
  adminLogin: '管理员登录',
  userRegister: '注册新账号',
  userLogin: '登录',
}[mode.value] || ''))

onMounted(async () => {
  serverURL.value = store.serverURL || ''
  hasSavedURL.value = !!store.serverURL
  try {
    caPath.value = (await api.GetCA()) || ''
  } catch {
    caPath.value = ''
  }
  // 读取本地身份角色（决定当前模式的「首次」vs「登录」）
  try {
    identityRole.value = (await api.Role()) || ''
  } catch {
    identityRole.value = ''
  }
  // 启动时若已开启自动解锁，自动尝试免口令解锁
  if (store.autoUnlockEnabled) {
    await tryAutoUnlock()
  }
})

async function tryAutoUnlock() {
  if (autoBusy.value) return
  autoBusy.value = true
  tip.value = '正在自动解锁（Windows DPAPI）…'
  tipType.value = 'info'
  try {
    await store.tryAutoUnlock()
    tip.value = ''
  } catch (e) {
    tip.value = `自动解锁失败，请输口令解锁（${e.message || e}）`
    tipType.value = 'err'
  } finally {
    autoBusy.value = false
  }
}

const serverInvalid = computed(() => {
  const u = serverURL.value.trim()
  if (!u) return '请输入服务端地址'
  if (!/^https?:\/\/.+/.test(u)) return '地址需以 http:// 或 https:// 开头'
  return ''
})

const canProceed = computed(() => {
  if (hasSavedURL.value && serverMode.value === 'saved') {
    return !serverInvalid.value
  }
  return !serverInvalid.value && !verifying.value
})

// ---- 动作 ----
async function pickCA() {
  if (window.go) {
    const p = await OpenFileDialog('选择自签 CA 证书（.crt / .pem）')
    if (p) caPath.value = p
  }
}

async function verifyServer() {
  const err = serverInvalid.value
  if (err) {
    tip.value = err
    tipType.value = 'err'
    return
  }
  verifying.value = true
  tip.value = '正在验证服务端连通性…'
  tipType.value = 'info'
  try {
    await api.VerifyServer(serverURL.value.trim(), caPath.value.trim())
    tip.value = '服务端连接正常，配置已就绪'
    tipType.value = 'ok'
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    verifying.value = false
  }
}

async function doUnlock() {
  const err = serverInvalid.value
  if (err) {
    tip.value = err
    tipType.value = 'err'
    return
  }
  if (!username.value.trim()) {
    tip.value = '请输入工号'
    tipType.value = 'err'
    return
  }
  if (!password.value) {
    tip.value = '请输入口令'
    tipType.value = 'err'
    return
  }
  busy.value = true
  tip.value = '正在解锁并验证服务端…'
  tipType.value = 'info'
  try {
    // §9.2：首次配置/修改地址 → 持久化地址 + CA；使用已存地址 → 直接解锁
    const url = serverURL.value.trim()
    if (!hasSavedURL.value || serverMode.value === 'new') {
      await api.SetServerURL(url)
      await api.SetCA(caPath.value.trim())
    }
    // 工号+密码解锁（从本地库解私钥）
    const res = await store.unlock(username.value.trim(), password.value)
    password.value = ''
    if (res && res.need_register) {
      // 已初始化但本地无设备 token（换设备）→ 注册设备
      needRegister.value = true
      regUsername.value = username.value.trim()
      tip.value = '本地无设备 token，请输入设备名完成注册'
      tipType.value = 'info'
      return
    }
    needRegister.value = false
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    busy.value = false
  }
}

// 普通用户注册：生成公私钥（私钥加密存本地）+ 解锁，返回公钥（交管理员开户）
async function doGenerateKeypair() {
  const err = serverInvalid.value
  if (err) {
    tip.value = err
    tipType.value = 'err'
    return
  }
  if (!username.value.trim()) {
    tip.value = '请输入工号'
    tipType.value = 'err'
    return
  }
  if (!password.value) {
    tip.value = '请设置口令（保护本地私钥）'
    tipType.value = 'err'
    return
  }
  busy.value = true
  tip.value = '正在生成密钥对…'
  tipType.value = 'info'
  try {
    const url = serverURL.value.trim()
    if (!hasSavedURL.value || serverMode.value === 'new') {
      await api.SetServerURL(url)
      await api.SetCA(caPath.value.trim())
    }
    const pubB64 = await store.generateKeypair(username.value.trim(), 'member', password.value)
    password.value = ''
    generatedPub.value = pubB64 || ''
    needRegister.value = true
    regUsername.value = username.value.trim()
    tip.value = '密钥对已生成。请复制公钥交给管理员开户，再注册设备'
    tipType.value = 'ok'
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    busy.value = false
  }
}

// 保存私钥备份到指定文件（换设备恢复用）
async function doExportKeyfile() {
  if (!generatedPub.value) {
    tip.value = '请先生成密钥对'
    tipType.value = 'err'
    return
  }
  try {
    const path = await api.SaveFileDialog('保存私钥备份')
    if (!path) return
    await api.ExportKeyfile(path)
    tip.value = `私钥备份已保存到 ${path}`
    tipType.value = 'ok'
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  }
}

// 导入私钥备份：恢复身份（存本地）+ 解锁，本地无 token 则需注册设备
async function doImportKeyfile() {
  if (!importKeyfilePath.value) {
    importKeyfilePath.value = await api.OpenFileDialog('选择私钥备份（.key）')
    if (!importKeyfilePath.value) return
  }
  if (!username.value.trim()) {
    tip.value = '请输入工号'
    tipType.value = 'err'
    return
  }
  if (!importPass.value) {
    tip.value = '请输入私钥备份口令'
    tipType.value = 'err'
    return
  }
  busy.value = true
  tip.value = '正在导入私钥并恢复身份…'
  tipType.value = 'info'
  try {
    const res = await api.ImportKeyfile(importKeyfilePath.value, username.value.trim(), 'member', importPass.value)
    importPass.value = ''
    importKeyfilePath.value = ''
    if (res && res.need_register) {
      // 本地无设备 token → 注册设备
      needRegister.value = true
      regUsername.value = username.value.trim()
      tip.value = '身份已恢复，请输入设备名完成设备注册'
      tipType.value = 'info'
      return
    }
    await store.enterList()
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    busy.value = false
  }
}

// 选择私钥备份文件
async function pickImportKeyfile() {
  const p = await api.OpenFileDialog('选择私钥备份（.key）')
  if (p) importKeyfilePath.value = p
}

async function doRegister() {
  if (!regUsername.value.trim()) {
    tip.value = '请输入工号'
    tipType.value = 'err'
    return
  }
  busy.value = true
  tip.value = '正在注册设备…'
  tipType.value = 'info'
  try {
    await store.registerDevice(regUsername.value.trim(), regDeviceName.value.trim() || undefined)
    needRegister.value = false
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    busy.value = false
  }
}

async function copyPub() {
  try {
    await navigator.clipboard.writeText(generatedPub.value)
    tip.value = '公钥已复制'
    tipType.value = 'ok'
  } catch {
    tip.value = '复制失败，请手动选择复制'
    tipType.value = 'err'
  }
}

async function doBootstrap() {
  const err = serverInvalid.value
  if (err) {
    tip.value = err
    tipType.value = 'err'
    return
  }
  if (!username.value.trim()) {
    tip.value = '请输入工号'
    tipType.value = 'err'
    return
  }
  if (!adminName.value.trim()) {
    tip.value = '请输入显示名'
    tipType.value = 'err'
    return
  }
  if (!password.value) {
    tip.value = '请设置口令'
    tipType.value = 'err'
    return
  }
  if (!adminToken.value.trim()) {
    tip.value = '请输入一次性 bootstrap token'
    tipType.value = 'err'
    return
  }
  if (!adminRegSecret.value.trim()) {
    tip.value = '请输入注册凭证密钥（PB_REG_SECRET）'
    tipType.value = 'err'
    return
  }
  busy.value = true
  tip.value = '正在初始化管理员并注册首个设备…'
  tipType.value = 'info'
  try {
    const url = serverURL.value.trim()
    await api.SetServerURL(url)
    await api.SetCA(caPath.value.trim())
    // 生成密钥对 + bootstrap 注册首个 admin + 建 engine
    await api.Bootstrap(username.value.trim(), password.value, adminToken.value.trim(),
        adminName.value.trim(), adminDeviceName.value.trim() || undefined)
    // 保存 REG_SECRET（供后续开户计算 attestation）
    await api.SetRegSecret(adminRegSecret.value.trim())
    password.value = ''
    adminToken.value = ''
    adminRegSecret.value = ''
    await store.enterList()
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    busy.value = false
  }
}

// 主按钮统一入口：根据当前模式分发
function submit() {
  if (mode.value === 'adminDeploy') return doBootstrap()
  if (mode.value === 'userRegister') return doGenerateKeypair()
  return doUnlock()
}
</script>

<template>
  <div class="unlock">
    <div class="unlock__left">
      <div class="unlock__hero">
        <div class="unlock__logo">🔐</div>
        <h1 class="unlock__title">在线密码本</h1>
        <p class="unlock__subtitle">端到端加密 · 团队密钥管理</p>
        <ul class="unlock__feats">
          <li><span class="feat-dot"></span>零信任架构，服务端不可见明文</li>
          <li><span class="feat-dot"></span>国密 SM2/SM3/SM4 全链路加密</li>
          <li><span class="feat-dot"></span>离线可用 · 冲突字段级三路合并</li>
        </ul>
      </div>
    </div>

    <div class="unlock__right">
      <div class="pb-glass pb-glass--strong unlock__card">
        <div class="unlock__card-head">
          <h2>安全解锁</h2>
          <span class="pb-badge pb-badge--neutral">端到端加密</span>
        </div>

        <!-- 服务端地址配置（§9.2） -->
        <div class="unlock__section">
          <div class="unlock__section-title">
            <span>服务端地址</span>
            <span v-if="hasSavedURL && serverMode === 'saved'" class="pb-badge pb-badge--success">已配置</span>
          </div>

          <div v-if="hasSavedURL" class="unlock__saved-row">
            <span class="pb-mono pb-truncate pb-fill">{{ serverURL }}</span>
            <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="serverMode = serverMode === 'saved' ? 'new' : 'saved'">
              {{ serverMode === 'saved' ? '修改' : '使用已存' }}
            </button>
          </div>

          <template v-if="!hasSavedURL || serverMode === 'new'">
            <div class="unlock__input-row">
              <input
                  v-model="serverURL"
                  class="pb-input pb-input--mono"
                  placeholder="https://pb.example.com:8443"
                  spellcheck="false"
                  @keyup.enter="verifyServer"
              />
              <button class="pb-btn pb-btn--ghost" :disabled="verifying" @click="verifyServer">
                <span v-if="verifying" class="pb-spinner pb-spinner--sm"></span>
                <span v-else>验证</span>
              </button>
            </div>
            <div class="unlock__input-row">
              <input
                  v-model="caPath"
                  class="pb-input pb-input--mono"
                  placeholder="自签 CA 证书路径（.crt/.pem；公网受信证书可留空）"
                  spellcheck="false"
              />
              <button class="pb-btn pb-btn--ghost" @click="pickCA">选择…</button>
            </div>
            <p class="unlock__hint">首次使用必填，验证通过后自动保存，后续启动免配置；自签证书部署需指定 CA 证书</p>
          </template>
        </div>

        <div class="pb-divider"></div>

        <!-- 工号 + 口令 -->
        <div class="unlock__section">
          <div class="unlock__section-title">
            <span>{{ modeTitle }}</span>
          </div>

          <!-- 管理员首次部署表单 -->
          <template v-if="mode === 'adminDeploy'">
            <div class="pb-field">
              <label class="pb-label">显示名</label>
              <input
                  v-model="adminName"
                  class="pb-input pb-input--lg"
                  placeholder="管理员显示名（如 张三）"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
            <div class="pb-field">
              <label class="pb-label">一次性 bootstrap token</label>
              <input
                  v-model="adminToken"
                  class="pb-input pb-input--mono"
                  placeholder="服务端部署时生成的一次性 token"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
            <div class="pb-field">
              <label class="pb-label">注册凭证密钥（PB_REG_SECRET）</label>
              <input
                  v-model="adminRegSecret"
                  :type="showPass ? 'text' : 'password'"
                  class="pb-input pb-input--mono"
                  placeholder="部署时配置的 PB_REG_SECRET，用于后续开户"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
            <div class="pb-field">
              <label class="pb-label">设备名（可选）</label>
              <input
                  v-model="adminDeviceName"
                  class="pb-input pb-input--mono"
                  placeholder="默认取主机名"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
          </template>

          <div class="pb-field">
            <label class="pb-label">工号</label>
            <input
                v-model="username"
                class="pb-input pb-input--lg"
                placeholder="输入工号（唯一、不可改）"
                spellcheck="false"
                autocomplete="off"
            />
          </div>

          <div class="pb-field">
            <label class="pb-label">口令</label>
            <div class="pb-input-group">
              <input
                  v-model="password"
                  :type="showPass ? 'text' : 'password'"
                  class="pb-input pb-input--lg"
                  :placeholder="(mode === 'userRegister' || mode === 'adminDeploy') ? '设置口令（保护本地私钥）' : '输入口令解锁'"
                  autocomplete="off"
                  @keyup.enter="submit"
              />
              <button class="pb-input-group__action" type="button" title="显示/隐藏" @click="showPass = !showPass">
                {{ showPass ? '🙈' : '👁' }}
              </button>
            </div>
          </div>
        </div>

        <!-- 注册界面：已有密钥 → 跳过注册去登录 -->
        <div v-if="mode === 'userRegister'" class="unlock__skip">
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="forceLogin = true">
            已有密钥？跳过注册去登录
          </button>
        </div>

        <!-- 登录界面：本地无身份时，可导入私钥备份恢复 -->
        <div v-if="mode === 'userLogin' && forceLogin && identityRole !== 'member'" class="unlock__import">
          <div class="unlock__section-title"><span>已有密钥？导入私钥备份恢复登录</span></div>
          <div class="unlock__input-row">
            <input v-model="importKeyfilePath" class="pb-input pb-input--mono" placeholder="私钥备份文件（.key）" spellcheck="false" />
            <button class="pb-btn pb-btn--ghost" @click="pickImportKeyfile">选择…</button>
          </div>
          <input
              v-model="importPass"
              :type="showPass ? 'text' : 'password'"
              class="pb-input"
              placeholder="私钥备份口令"
              autocomplete="off"
              @keyup.enter="doImportKeyfile"
          />
          <button class="pb-btn pb-btn--primary pb-btn--block" :disabled="busy" @click="doImportKeyfile">导入并登录</button>
        </div>

        <!-- 提示 / 提交 -->
        <div v-if="tip" class="unlock__tip" :class="`unlock__tip--${tipType}`">
          <span>{{ tip }}</span>
        </div>

        <!-- 首次注册设备（§9.1：本地无设备 token） -->
        <div v-if="needRegister" class="unlock__section">
          <div class="unlock__section-title"><span>注册设备</span></div>
          <!-- 工号即登录名，沿用注册时已输入的值，不再让用户重复输入 -->
          <div class="unlock__readonly">
            <span class="pb-label">工号</span>
            <span class="pb-mono pb-truncate">{{ regUsername }}</span>
          </div>
          <input
              v-model="regDeviceName"
              class="pb-input pb-input--mono"
              placeholder="设备名（可选，默认取主机名）"
              spellcheck="false"
          />
          <!-- 首次初始化生成的公钥（交管理员开户用） -->
          <div v-if="generatedPub" class="unlock__pub">
            <span class="pb-label">你的公钥（复制给管理员开户）</span>
            <div class="unlock__pub-row">
              <span class="pb-mono pb-truncate pb-fill">{{ generatedPub }}</span>
              <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="copyPub">复制</button>
            </div>
            <div class="unlock__pub-row">
              <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="doExportKeyfile">💾 保存私钥备份到文件</button>
            </div>
          </div>
        </div>

        <div v-if="store.autoUnlockEnabled && !needRegister" class="unlock__autounlock">
          <button class="pb-btn pb-btn--ghost pb-btn--sm" :disabled="autoBusy" @click="tryAutoUnlock">
            <span v-if="autoBusy" class="pb-spinner pb-spinner--sm"></span>
            <span v-else>🔓 自动解锁</span>
          </button>
          <span class="pb-xs pb-muted">已开启自动解锁，可免口令进入</span>
        </div>

        <button
            v-if="needRegister"
            class="pb-btn pb-btn--primary pb-btn--lg pb-btn--block"
            :disabled="busy"
            @click="doRegister"
        >
          <span v-if="busy" class="pb-spinner"></span>
          <span>{{ busy ? '正在注册…' : '注册设备并进入' }}</span>
        </button>
        <button
            v-else
            class="pb-btn pb-btn--primary pb-btn--lg pb-btn--block"
            :disabled="busy || autoBusy || !canProceed"
            @click="submit"
        >
          <span v-if="busy" class="pb-spinner"></span>
          <span>{{ busy ? '处理中…' : (mode === 'adminDeploy' ? '部署并进入' : (mode === 'userRegister' ? '生成公私钥' : '解锁进入')) }}</span>
        </button>

        <p class="unlock__foot">
          <span class="pb-dot pb-dot--idle"></span>
          失焦 5 分钟自动锁定并清除内存密钥
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.unlock {
  flex: 1;
  min-height: 0;
  overflow: hidden; /* 整体不溢出；左右两栏各自滚动 */
  display: grid;
  grid-template-columns: 1.1fr 1fr;
  gap: 0;
}

.unlock__left {
  overflow-y: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px;
}

.unlock__hero {
  max-width: 400px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  animation: pb-view-in 0.4s var(--ease);
}

.unlock__logo {
  width: 56px;
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 26px;
  border-radius: 16px;
  background: var(--accent-grad);
  box-shadow: 0 8px 30px rgba(56, 189, 248, 0.35);
  margin-bottom: 12px;
}

.unlock__title {
  font-size: 32px;
  font-weight: 800;
  letter-spacing: 0.01em;
}

.unlock__subtitle {
  font-size: 15px;
  color: var(--text-2);
  margin-bottom: 20px;
}

.unlock__feats {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
  font-size: 13.5px;
  color: var(--text-2);
}

.unlock__feats li {
  display: flex;
  align-items: center;
  gap: 10px;
}

.feat-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 8px var(--accent);
  flex-shrink: 0;
}

.unlock__right {
  overflow-y: auto;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 32px;
}

.unlock__card {
  margin: auto; /* 内容少时垂直居中；内容多（如管理员部署）时顶部对齐可滚动 */
  width: 100%;
  max-width: 440px;
  padding: 32px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  animation: pb-pop-in 0.34s var(--ease);
}

.unlock__card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.unlock__card-head h2 {
  font-size: 19px;
  font-weight: 700;
}

.unlock__section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.unlock__section-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-2);
}

.unlock__saved-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
  font-size: 13px;
}

.unlock__input-row {
  display: flex;
  gap: 8px;
}
.unlock__input-row .pb-input {
  flex: 1;
}

.unlock__hint {
  font-size: 12px;
  color: var(--text-3);
  line-height: 1.6;
}

.unlock__tip {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 13px;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  line-height: 1.6;
  word-break: break-all;
}
.unlock__tip--info { background: var(--accent-soft); color: var(--accent); }
.unlock__tip--ok { background: var(--success-soft); color: var(--success); }
.unlock__tip--err { background: var(--danger-soft); color: var(--danger); }

.unlock__autounlock {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 10px;
  border-radius: var(--radius-md);
  border: 1px dashed var(--glass-border-strong);
}

.unlock__pub {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
}

/* 注册设备：工号只读展示（工号即登录名，沿用注册时输入值） */
.unlock__readonly {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
  min-width: 0;
}
.unlock__pub-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.unlock__skip {
  display: flex;
  justify-content: center;
  padding: 2px 0;
}

.unlock__import {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  border-radius: var(--radius-md);
  border: 1px dashed var(--glass-border-strong);
}

.unlock__foot {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  font-size: 12px;
  color: var(--text-3);
}

@media (max-width: 900px) {
  .unlock { grid-template-columns: 1fr; }
  .unlock__left { display: none; }
}
</style>
