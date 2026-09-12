<script setup>
import {ref, computed, onMounted, onUnmounted} from 'vue'
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
const localUsername = ref('') // 本地身份工号（登录/锁定态显示，不可改）
// 方案 C：注册码（邀请码）+ 审核状态
const inviteCode = ref('')
const regSubmitted = ref(false) // 是否已提交注册申请
const regStatus = ref('')       // pending | approved | rejected
const regPollTimer = ref(null)
const keyfilePath = ref('')    // 公私钥备份地址（完整 .key 路径，可选；不填则仅入库）
const genModal = ref(false)     // 公私钥生成结果弹窗（无邀请码手动流程）
const genResult = ref({ username: '', pub: '', path: '' })
const forceLogin = ref(false) // 跳过注册，强制进入登录界面
// seg 手动覆盖：''（按本地身份自动推导）| 'login' | 'register'（用户点「登录/注册」分段后生效）
const manualPage = ref('')
// 注册子页面：invite=邀请码注册 | manual=公私钥手动注册（两个独立页面）
const regPage = ref('invite')
// 邀请码注册失败弹窗：提示是否切换到手动注册流程
const regFailModal = ref(false)
const regFailMsg = ref('')

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
  if (manualPage.value === 'register') return 'userRegister'
  if (manualPage.value === 'login' || forceLogin.value) return 'userLogin'
  return identityRole.value === 'member' ? 'userLogin' : 'userRegister'
})

// 卡片头标题/徽章随模式切换（对齐原型：注册→注册新账号/新成员；登录→安全解锁/端到端加密）
const cardTitle = computed(() => {
  if (mode.value === 'adminDeploy') return '管理员首次部署'
  if (mode.value === 'adminLogin') return '管理员登录'
  if (mode.value === 'userRegister') return regPage.value === 'manual' ? '手动注册 · 生成密钥对' : '注册新账号'
  return '安全解锁'
})
const cardBadge = computed(() =>
    (mode.value === 'userRegister' || mode.value === 'adminDeploy') ? '新成员' : '端到端加密')

const modeTitle = computed(() => ({
  adminDeploy: '管理员首次部署',
  adminLogin: '管理员登录',
  userRegister: regPage.value === 'manual' ? '手动生成密钥对' : '邀请码注册',
  userLogin: '登录',
}[mode.value] || ''))

// seg 分段切换（登录/注册）：手动选择优先于身份自动推导
function setPage(p) {
  manualPage.value = p
  tip.value = ''
  if (p === 'login') {
    switchRegPage('invite')
  } else {
    forceLogin.value = false
    importOpen.value = false
  }
}

// 有私钥导入：切到登录页并展开私钥导入区块
function goImport() {
  setPage('login')
  importOpen.value = true
}

onMounted(async () => {
  // 锁定后重新挂载：从本地库实时读已保存地址（store.serverURL 是启动时缓存，注册 SetServerURL 后未同步会拿不到最新值）
  try {
    serverURL.value = (await api.GetServerURL()) || ''
  } catch {
    serverURL.value = store.serverURL || ''
  }
  hasSavedURL.value = !!serverURL.value
  try {
    caPath.value = (await api.GetCA()) || ''
  } catch {
    caPath.value = ''
  }
  // 读取本地身份角色（决定当前模式的「首次」vs「登录」）
  try {
    identityRole.value = (await api.Role()) || ''
    try {
      localUsername.value = (await api.Username()) || ''
      if (localUsername.value) username.value = localUsername.value // 默认填工号（可改）
    } catch {
      localUsername.value = ''
    }
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
    // 提示与实际行为对齐：验证只做连通性检查，地址/CA 由后续业务动作（解锁/注册/导入/生成密钥）保存
    tip.value = '服务端连接正常（地址会在继续操作时自动保存）'
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
  const uname = username.value.trim()
  if (!uname) {
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
    const res = await store.unlock(uname, password.value)
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

// 手动生成公私钥（无邀请码路径：生成后复制「注册信息」给管理员开户，与邀请码注册互斥）
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
    // 备份地址填写了 → 自动导出 .key 到该路径（可选，不填仅入库，解锁后设置中可导出）
    let savedPath = ''
    if (keyfilePath.value.trim()) {
      try {
        await api.ExportKeyfile(keyfilePath.value.trim())
        savedPath = keyfilePath.value.trim()
      } catch (e) {
        tip.value = '密钥已生成，但备份导出失败：' + String(e.message || e)
        tipType.value = 'err'
      }
    }
    // 弹窗展示公私钥信息（复制给管理员开户）
    genResult.value = { username: username.value.trim(), pub: pubB64 || '', path: savedPath }
    genModal.value = true
    if (!savedPath) {
      tip.value = '公私钥已生成（未填备份地址，仅加密入库；可在弹窗中导出备份）'
      tipType.value = 'ok'
    }
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
  } finally {
    busy.value = false
  }
}

// 邀请码注册（自动流程：生成密钥对 → 提交申请一步到位；pending 等审核 / approved 免审核直通）
async function doRegisterApply() {
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
  if (!inviteCode.value.trim()) {
    tip.value = '请输入注册码（管理员发放的邀请码）'
    tipType.value = 'err'
    return
  }
  busy.value = true
  tip.value = '正在生成密钥并提交申请…'
  tipType.value = 'info'
  try {
    // 防覆盖：本地已有身份（如手动流程已生成）时禁止再注册——
    // generateKeypair 会覆盖旧私钥，失败回滚（ClearIdentity）更会把旧身份一并删除，
    // 若服务端已按旧公钥开户将导致身份不可恢复丢失。已有身份请走登录/私钥导入。
    let existingRole = ''
    try { existingRole = (await api.Role()) || '' } catch {}
    if (existingRole) {
      tip.value = '本地已有身份，不能重复注册（重新注册会覆盖旧私钥且失败时会被清空）；请直接登录或用「有私钥导入」恢复'
      tipType.value = 'err'
      return
    }
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
    // 凭邀请码提交注册申请（pending 等审核 / approved 免审核直通）
    const status = await api.RegisterRequest(
        inviteCode.value.trim(), username.value.trim(), pubB64,
        regDeviceName.value.trim() || '')
    regSubmitted.value = true
    regStatus.value = status
    if (status === 'approved') {
      tip.value = '已开户（免审核码），正在注册设备…'
      await store.registerDevice(username.value.trim(), regDeviceName.value.trim() || '')
      tip.value = '注册成功，已进入密码本'
      tipType.value = 'ok'
    } else {
      tip.value = '申请已提交，等待管理员审核（每 30 秒自动刷新）'
      tipType.value = 'info'
      startRegPoll()
    }
  } catch (e) {
    // 注册失败回滚：generateKeypair 已在 RegisterRequest 之前写入本地库（identity+keyfileBlob），
    // 不清掉会让重启后误判为已注册用户（直接进登录页）。清空 identity+device_state 回到未注册态。
    // 回滚失败不致命（重启后仍进登录页，可手动重置），但按错误不吞原则留 warn 便于排查。
    let rolledBack = true
    try { await api.ClearIdentity() } catch (rollbackErr) {
      rolledBack = false
      console.warn('注册失败回滚 ClearIdentity 失败（重启后将停留在登录页，可用重置脚本清理）:', rollbackErr)
    }
    // 复位本次尝试留下的前端状态：不清 needRegister/generatedPub 会让界面停在「注册设备」页，
    // 与失败弹窗给出的「重新填写邀请码」选项不自洽，也会让用户误以为身份已生成。
    generatedPub.value = ''
    needRegister.value = false
    regSubmitted.value = false
    regStatus.value = ''
    const msg = String(e.message || e)
    tip.value = rolledBack ? msg : msg + '（本地回滚失败：请重启客户端后重试，或联系管理员）'
    tipType.value = 'err'
    regFailMsg.value = /邀请码|注册码/.test(msg) ? msg + '（请联系管理员核对注册码）' : msg
    regFailModal.value = true
  } finally {
    busy.value = false
  }
}

// 邀请码注册失败 → 确认进入公私钥手动注册流程
function confirmGoManual() {
  regFailModal.value = false
  regPage.value = 'manual'
  tip.value = '已切换到公私钥手动注册流程（生成后复制公钥给管理员开户）'
  tipType.value = 'info'
}

// 邀请码注册失败 → 取消，停留邀请码注册页等待更正信息
function cancelGoManual() {
  regFailModal.value = false
  tip.value = regFailMsg.value
  tipType.value = 'err'
}

// 切换注册子页面（invite ↔ manual），并清理目标页不相关的残留状态
function switchRegPage(page) {
  stopRegPoll()
  regPage.value = page
  if (page === 'invite') {
    generatedPub.value = ''
    needRegister.value = false
    regSubmitted.value = false
    genModal.value = false
  }
}

// 轮询审核状态（30s）：approved → 自动注册设备；rejected → 提示。
// 注意：/auth/* 在服务端有限流（多客户端并发注册时 /auth/device 可能撞 42901），
// 设备注册失败绝不能停轮询——否则用户永久卡在「审核已通过，正在注册设备…」，
// 必须保留下轮自动重试（每 30s 一次），成功后再停。
function startRegPoll() {
  stopRegPoll()
  regPollTimer.value = setInterval(async () => {
    try {
      const st = await api.RegisterStatus(inviteCode.value.trim())
      regStatus.value = st
      if (st === 'approved') {
        if (tip.value !== '审核已通过，正在注册设备…') {
          tip.value = '审核已通过，正在注册设备…'
          tipType.value = 'info'
        }
        try {
          await store.registerDevice(username.value.trim(), regDeviceName.value.trim() || '')
          stopRegPoll()
          tip.value = '注册成功，已进入密码本'
          tipType.value = 'ok'
        } catch (regErr) {
          console.warn('审核已通过但设备注册失败，下一轮自动重试:', regErr)
        }
      } else if (st === 'rejected') {
        stopRegPoll()
        // 被拒必须同样回滚本地身份：否则用户既无法登录（无设备 token），也无法重新注册
        // （会被「本地已有身份」预检拦住），只能手删数据目录（与邀请码无效路径同款问题）。
        let rolledBack = true
        try { await api.ClearIdentity() } catch (rollbackErr) {
          rolledBack = false
          console.warn('申请被拒后回滚 ClearIdentity 失败（可重启客户端后用重置脚本清理）:', rollbackErr)
        }
        // 复位到「未提交」态，使用户可直接换新邀请码重试
        generatedPub.value = ''
        needRegister.value = false
        regSubmitted.value = false
        regStatus.value = ''
        tip.value = rolledBack
          ? '申请已被拒绝，本地未完成注册，可直接用新的邀请码重试；请联系管理员确认原因'
          : '申请已被拒绝；本地回滚失败，请重启客户端后重试'
        tipType.value = 'err'
      }
    } catch (e) {
      // 网络波动/限流忽略，下轮重试
    }
  }, 30000)
}

function stopRegPoll() {
  if (regPollTimer.value) {
    clearInterval(regPollTimer.value)
    regPollTimer.value = null
  }
}

onUnmounted(stopRegPoll)

// 选择公私钥备份地址（完整 .key 路径）
async function pickKeyfilePath() {
  try {
    const path = await api.SaveFileDialog('选择私钥备份保存位置')
    if (path) keyfilePath.value = path
  } catch (e) {
    tip.value = String(e.message || e)
    tipType.value = 'err'
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
  const url = serverURL.value.trim()
  if (!url) {
    tip.value = '请先填写服务端地址'
    tipType.value = 'err'
    return
  }
  busy.value = true
  tip.value = '正在导入私钥并恢复身份…'
  tipType.value = 'info'
  try {
    // §9.2：与解锁/注册/生成密钥流程保持一致——首次配置（或改了地址）时先持久化地址 + CA。
    // 缺这一步时：导入后「注册设备」拿不到地址，报「未配置服务端地址」，新设备恢复流程走不完。
    if (!hasSavedURL.value || serverMode.value === 'new') {
      await api.SetServerURL(url)
      await api.SetCA(caPath.value.trim())
    }
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
  if (!username.value.trim()) {
    tip.value = '请输入工号'
    tipType.value = 'err'
    return
  }
  busy.value = true
  tip.value = '正在注册设备…'
  tipType.value = 'info'
  try {
    await store.registerDevice(username.value.trim(), regDeviceName.value.trim() || '')
    needRegister.value = false
  } catch (e) {
    const msg = String(e.message || e)
    tip.value = /用户不存在|未开户/.test(msg)
      ? '工号未开户或与公钥不匹配——请把「注册信息」发给管理员核对开户工号（管理员需用你提供的工号+公钥开户）'
      : msg
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

// 复制生成弹窗中的公钥
async function copyGenPub() {
  try {
    await navigator.clipboard.writeText(genResult.value.pub || '')
    tip.value = '公钥已复制'
    tipType.value = 'ok'
  } catch {
    tip.value = '复制失败，请手动选择复制'
    tipType.value = 'err'
  }
}

// 复制完整注册信息（工号+公钥）给管理员开户，避免工号口头传达出错（§6.3）
async function copyRegInfo() {
  try {
    await navigator.clipboard.writeText(`工号：${username.value.trim()}
公钥：${generatedPub.value}`)
    tip.value = '注册信息已复制（含工号+公钥），粘贴给管理员开户'
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
    adminRegSecret.value = '' // 敏感值即用即清，不驻留内存
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
  if (mode.value === 'userRegister') return regPage.value === 'manual' ? doGenerateKeypair() : doRegisterApply()
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
          <h2>{{ cardTitle }}</h2>
          <span class="pb-badge pb-badge--neutral">{{ cardBadge }}</span>
        </div>

        <!-- 登录/注册 分段切换（管理员模式与审核等待中不显示） -->
        <div v-if="!store.adminMode && !needRegister && !regSubmitted" class="unlock__seg">
          <button :class="{ on: mode === 'userLogin' }" @click="setPage('login')">登录</button>
          <button :class="{ on: mode === 'userRegister' }" @click="setPage('register')">注册</button>
        </div>

        <!-- 注册子页签：邀请码注册 / 手动生成密钥（对齐原型 regtabs） -->
        <div v-if="mode === 'userRegister' && !regSubmitted" class="unlock__regtabs">
          <button :class="{ on: regPage === 'invite' }" @click="switchRegPage('invite')">🎟 邀请码注册</button>
          <button :class="{ on: regPage === 'manual' }" @click="switchRegPage('manual')">🔑 手动生成密钥</button>
        </div>

        <!-- 服务端地址配置（§9.2） -->
        <div class="unlock__section">
          <div class="unlock__section-title">
            <span>服务端地址</span>
            <span v-if="hasSavedURL && serverMode === 'saved'" class="pb-badge pb-badge--success">已配置</span>
          </div>

          <div v-if="hasSavedURL" class="unlock__saved-row">
            <span class="pb-mono pb-truncate pb-fill">{{ serverURL }}</span>
            <button v-if="mode !== 'userLogin'" class="pb-btn pb-btn--ghost pb-btn--sm" @click="serverMode = serverMode === 'saved' ? 'new' : 'saved'">
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
            <div v-if="mode !== 'userLogin'" class="unlock__input-row">
              <input
                  v-model="caPath"
                  class="pb-input pb-input--mono"
                  placeholder="自签 CA 证书路径（.crt/.pem；公网受信证书可留空）"
                  spellcheck="false"
              />
              <button class="pb-btn pb-btn--ghost" @click="pickCA">选择…</button>
            </div>
            <p v-if="mode !== 'userLogin'" class="unlock__hint">首次使用必填，验证通过后自动保存，后续启动免配置；自签证书部署需指定 CA 证书</p>
          </template>
        </div>

        <div class="pb-divider"></div>

        <!-- 工号 + 口令（按模式渲染对应表单；审核等待中整段隐藏，对齐原型 regresult 替换表单） -->
        <div v-if="!(mode === 'userRegister' && regSubmitted)" class="unlock__section">
          <div class="unlock__section-title">
            <span>{{ modeTitle }}</span>
            <span v-if="mode === 'userRegister'" class="pb-xs pb-muted">{{ regPage === 'manual' ? '无邀请码' : '管理员发放' }}</span>
          </div>

          <!-- 管理员首次部署表单 -->
          <template v-if="mode === 'adminDeploy'">
            <div class="pb-field">
              <label class="pb-label">工号</label>
              <input
                  v-model="username"
                  class="pb-input pb-input--lg"
                  placeholder="管理员工号（唯一、不可改）"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
            <div class="pb-field">
              <label class="pb-label">口令（保护本地私钥）</label>
              <div class="pb-input-group">
                <input
                    v-model="password"
                    :type="showPass ? 'text' : 'password'"
                    class="pb-input pb-input--lg"
                    placeholder="设置口令"
                    autocomplete="off"
                    @keyup.enter="submit"
                />
                <button class="pb-input-group__action" type="button" title="显示/隐藏" @click="showPass = !showPass">
                  {{ showPass ? '🙈' : '👁' }}
                </button>
              </div>
            </div>
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

          <!-- 邀请码注册：工号 → 注册码 → 口令 → 设备名（可选）→ 说明 → 提交 -->
          <template v-else-if="mode === 'userRegister' && regPage === 'invite'">
            <div class="pb-field">
              <label class="pb-label">工号</label>
              <input
                  v-model="username"
                  class="pb-input pb-input--lg"
                  :placeholder="localUsername ? '默认 ' + localUsername + '（可修改）' : '输入工号（唯一、不可改）'"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
            <div class="pb-field">
              <label class="pb-label">注册码（邀请码）</label>
              <input
                  v-model="inviteCode"
                  class="pb-input pb-input--lg pb-input--mono"
                  placeholder="管理员发放的注册码（绑定你的工号）"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
            <div class="pb-field">
              <label class="pb-label">口令（保护本地私钥）</label>
              <div class="pb-input-group">
                <input
                    v-model="password"
                    :type="showPass ? 'text' : 'password'"
                    class="pb-input pb-input--lg"
                    placeholder="设置口令"
                    autocomplete="off"
                    @keyup.enter="submit"
                />
                <button class="pb-input-group__action" type="button" title="显示/隐藏" @click="showPass = !showPass">
                  {{ showPass ? '🙈' : '👁' }}
                </button>
              </div>
            </div>
            <div class="pb-field">
              <label class="pb-label">设备名（可选）</label>
              <input
                  v-model="regDeviceName"
                  class="pb-input pb-input--lg pb-input--mono"
                  placeholder="默认取主机名"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
            <p class="pb-xs pb-muted">将自动生成 SM2 密钥对并提交注册申请：免审核码直接开户，普通码等待管理员审核。</p>
            <button class="pb-btn pb-btn--primary pb-btn--lg pb-btn--block" :disabled="busy" @click="submit">
              <span v-if="busy" class="pb-spinner"></span>
              <span>{{ busy ? '处理中…' : '注册并等待审核' }}</span>
            </button>
          </template>

          <!-- 手动生成密钥对：工号 → 口令 → 备份地址（可选）→ 说明 → 提交 -->
          <template v-else-if="mode === 'userRegister'">
            <div class="pb-field">
              <label class="pb-label">工号</label>
              <input
                  v-model="username"
                  class="pb-input pb-input--lg"
                  :placeholder="localUsername ? '默认 ' + localUsername + '（可修改）' : '输入工号（唯一、不可改）'"
                  spellcheck="false"
                  autocomplete="off"
              />
            </div>
            <div class="pb-field">
              <label class="pb-label">口令（保护本地私钥）</label>
              <div class="pb-input-group">
                <input
                    v-model="password"
                    :type="showPass ? 'text' : 'password'"
                    class="pb-input pb-input--lg"
                    placeholder="设置口令"
                    autocomplete="off"
                    @keyup.enter="submit"
                />
                <button class="pb-input-group__action" type="button" title="显示/隐藏" @click="showPass = !showPass">
                  {{ showPass ? '🙈' : '👁' }}
                </button>
              </div>
            </div>
            <div class="pb-field">
              <label class="pb-label">公私钥备份地址（可选）</label>
              <div class="unlock__input-row">
                <input
                    v-model="keyfilePath"
                    class="pb-input pb-input--mono"
                    placeholder="私钥备份 .key 完整路径（不填则仅加密入库，解锁后可在设置中导出）"
                    spellcheck="false"
                    autocomplete="off"
                />
                <button class="pb-btn pb-btn--ghost" @click="pickKeyfilePath">选择…</button>
              </div>
            </div>
            <p class="pb-xs pb-muted">生成后复制「注册信息」发给管理员开户；本地私钥加密入库，可导出备份。</p>
            <button class="pb-btn pb-btn--primary pb-btn--lg pb-btn--block" :disabled="busy" @click="submit">
              <span v-if="busy" class="pb-spinner"></span>
              <span>{{ busy ? '处理中…' : '生成公私钥' }}</span>
            </button>
          </template>

          <!-- 登录 / 管理员登录：工号 + 口令 -->
          <template v-else>
            <div class="pb-field">
              <label class="pb-label">工号</label>
              <input
                  v-model="username"
                  class="pb-input pb-input--lg"
                  :placeholder="localUsername ? '默认 ' + localUsername + '（可修改）' : '输入工号（唯一、不可改）'"
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
                    placeholder="输入口令解锁"
                    autocomplete="off"
                    @keyup.enter="submit"
                />
                <button class="pb-input-group__action" type="button" title="显示/隐藏" @click="showPass = !showPass">
                  {{ showPass ? '🙈' : '👁' }}
                </button>
              </div>
            </div>
          </template>
        </div>

        <!-- 注册界面：导入私钥 / 去登录（手动注册入口已由上方页签承担） -->
        <div v-if="mode === 'userRegister' && !regSubmitted" class="unlock__skip">
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="goImport">🔑 有私钥导入</button>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="setPage('login')">已有密钥？去登录</button>
        </div>

        <!-- 登录界面：本地无身份时，可导入私钥备份恢复 -->
        <div v-if="mode === 'userLogin' && (importOpen || forceLogin) && identityRole !== 'member'" class="unlock__import">
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
          <!-- 方案 C：注册申请已提交 → 显示审核状态（自动轮询） -->
          <div v-if="regSubmitted" class="unlock__pub">
            <span class="pb-label">审核状态</span>
            <p class="pb-xs">
              <span v-if="regStatus === 'approved'" class="pb-badge pb-badge--success">已通过</span>
              <span v-else-if="regStatus === 'rejected'" class="pb-badge pb-badge--danger">被拒绝</span>
              <span v-else class="pb-badge pb-badge--neutral">待审核（每 30 秒自动刷新）</span>
            </p>
            <p v-if="regStatus === 'pending'" class="pb-xs pb-muted">申请已提交，管理员审核通过后将自动完成注册。</p>
            <p v-if="regStatus === 'rejected'" class="pb-xs pb-muted">请与管理员确认拒绝原因（通常为工号/公钥不符）。</p>
          </div>
          <!-- 工号即登录名，沿用注册时已输入的值，不再让用户重复输入 -->
          <div class="unlock__readonly">
            <span class="pb-label">工号（可修改，注册用修改后的值）</span>
            <span class="pb-mono pb-truncate">{{ username }}</span>
          </div>
          <input
              v-model="regDeviceName"
              class="pb-input pb-input--mono"
              placeholder="设备名（可选，默认取主机名）"
              spellcheck="false"
          />
          <!-- 首次初始化生成的公钥（交管理员开户用；仅手动流程显示，邀请码流程已自动提交） -->
          <div v-if="generatedPub && !regSubmitted" class="unlock__pub">
            <span class="pb-label">你的公钥（复制给管理员开户）</span>
            <div class="unlock__pub-row">
              <span class="pb-mono pb-truncate pb-fill">{{ generatedPub }}</span>
              <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="copyPub">复制公钥</button>
            </div>
            <div class="unlock__pub-row">
              <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="copyRegInfo">📋 复制注册信息（工号+公钥）</button>
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
        <!-- 注册模式的主按钮已移入表单区（对齐原型）；此处仅登录/管理员模式 -->
        <button
            v-else-if="mode !== 'userRegister'"
            class="pb-btn pb-btn--primary pb-btn--lg pb-btn--block"
            :disabled="busy || autoBusy || !canProceed"
            @click="submit"
        >
          <span v-if="busy" class="pb-spinner"></span>
          <span>{{ busy ? '处理中…' : (mode === 'adminDeploy' ? '部署并进入' : '解锁进入') }}</span>
        </button>

        <p class="unlock__foot">
          <span class="pb-dot pb-dot--idle"></span>
          失焦 5 分钟自动锁定并清除内存密钥
        </p>
      </div>
    </div>
  </div>
        <!-- 公私钥生成结果弹窗（无邀请码手动流程） -->
        <div v-if="genModal" class="unlock__modal-mask" @click.self="genModal = false">
          <div class="pb-glass pb-glass--strong unlock__modal">
            <h3 class="unlock__modal-title">公私钥已生成</h3>
            <div class="pb-field">
              <label class="pb-label">工号</label>
              <span class="pb-mono pb-truncate">{{ genResult.username }}</span>
            </div>
            <div class="pb-field">
              <label class="pb-label">公钥（复制给管理员开户）</label>
              <div class="unlock__input-row">
                <input :value="genResult.pub" class="pb-input pb-input--mono" readonly spellcheck="false" />
                <button class="pb-btn pb-btn--ghost" @click="copyGenPub">复制</button>
              </div>
            </div>
            <p v-if="genResult.path" class="pb-xs pb-muted">私钥备份：{{ genResult.path }}</p>
            <p v-else class="pb-xs pb-muted">未填备份地址——私钥已加密入库，解锁后可在「设置」中导出备份</p>
            <div class="unlock__modal-actions">
              <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="doExportKeyfile">💾 保存私钥备份到文件</button>
              <button class="pb-btn pb-btn--primary pb-btn--sm" @click="genModal = false">完成</button>
            </div>
          </div>
        </div>

        <!-- 邀请码注册失败弹窗：是否切换到公私钥手动注册流程 -->
        <div v-if="regFailModal" class="unlock__modal-mask">
          <div class="pb-glass pb-glass--strong unlock__modal">
            <h3 class="unlock__modal-title">邀请码注册失败</h3>
            <p class="pb-xs pb-muted pb-break-all">{{ regFailMsg }}</p>
            <p class="pb-xs pb-muted">「切到手动注册」生成公私钥后需复制给管理员开户；「重新填邀请码」可继续用邀请码注册流程（更推荐）。</p>
            <div class="unlock__modal-actions">
              <button class="pb-btn pb-btn--ghost pb-btn--sm" @click="confirmGoManual">切到手动注册</button>
              <button class="pb-btn pb-btn--primary pb-btn--sm" @click="cancelGoManual">重新填写邀请码（推荐）</button>
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

/* 登录/注册 分段切换器（对齐原型 unlock__seg） */
.unlock__seg {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px;
  padding: 4px;
  border-radius: var(--radius-md);
  background: var(--input-bg);
  border: 1px solid var(--glass-border);
}
.unlock__seg button {
  height: 34px;
  border-radius: var(--radius-sm);
  font-size: 13px;
  font-weight: 600;
  color: var(--text-2);
  transition: all var(--dur) var(--ease);
}
.unlock__seg button.on {
  background: var(--accent-soft);
  color: var(--accent);
}
.unlock__seg button:not(.on):hover {
  background: var(--hover);
  color: var(--text-1);
}

/* 注册子页签：邀请码注册 / 手动生成密钥（对齐原型 unlock__regtabs） */
.unlock__regtabs {
  display: flex;
  gap: 6px;
  margin-top: -6px;
}
.unlock__regtabs button {
  height: 28px;
  padding: 0 12px;
  border-radius: 99px;
  font-size: 12.5px;
  color: var(--text-2);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  transition: all var(--dur) var(--ease);
}
.unlock__regtabs button.on {
  color: var(--accent);
  border-color: var(--accent);
  background: var(--accent-soft);
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

.unlock__modal-mask {
  position: fixed; inset: 0; z-index: 100;
  background: rgba(0,0,0,.45);
  display: flex; align-items: center; justify-content: center;
}
.unlock__modal {
  width: min(560px, 92vw);
  max-height: 86vh; overflow-y: auto;
  padding: 20px;
  border-radius: 12px;
}
.unlock__modal-title { margin: 0 0 14px; font-size: 16px; font-weight: 600; }
.unlock__modal-actions {
  display: flex; gap: 8px; justify-content: flex-end;
  margin-top: 16px;
}

</style>
