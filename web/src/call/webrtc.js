import { shallowReactive, markRaw } from 'vue'
import { v4 as uuidv4 } from 'uuid'
import * as ws from '../net/ws.js'

// 通话流程：主叫 call_invite → 被叫 call_accept → 主叫 call_offer → 被叫 call_answer
// → 双方互发 call_ice → 任一方 call_hangup。服务器只转发这些信令，音视频走点对点。

const ICE_SERVERS = [{ urls: 'stun:stun.l.google.com:19302' }]
const RING_TIMEOUT = 30000

// shallowReactive：MediaStream 被深层代理后不能赋给 video.srcObject
export const call = shallowReactive({
  state: 'idle', // idle | outgoing | incoming | connecting | in-call
  peer: 0,
  callId: '',
  local: null,
  remote: null,
  note: '', // 结束原因提示
})

let me = 0
let pc = null
let pendingIce = []
let ringTimer = null
let unsubs = []

const REASONS = {
  offline: '对方不在线',
  busy: '对方正在通话中',
  declined: '对方拒绝了通话',
  media: '对方无法打开摄像头/麦克风',
  timeout: '无人接听',
}

function convOf(a, b) {
  return a < b ? `u_${a}_${b}` : `u_${b}_${a}`
}

function signal(type, payload = {}, to = call.peer, callId = call.callId) {
  ws.send({
    type,
    to,
    conv_id: convOf(me, to),
    client_msg_id: uuidv4(),
    payload: { call_id: callId, ...payload },
  })
}

function isCurrent(env) {
  return env.from === call.peer && env.payload?.call_id === call.callId
}

export function initCalls(myUid) {
  me = myUid
  unsubs.forEach((u) => u())
  unsubs = [
    ws.on('call_invite', onInvite),
    ws.on('call_accept', (env) => isCurrent(env) && onAccept()),
    ws.on('call_reject', onReject),
    ws.on('call_offer', (env) => isCurrent(env) && onOffer(env)),
    ws.on('call_answer', (env) => isCurrent(env) && onAnswer(env)),
    ws.on('call_ice', (env) => isCurrent(env) && onIce(env)),
    ws.on('call_hangup', (env) => isCurrent(env) && reset('对方已挂断')),
  ]
}

export function teardownCalls() {
  hangup()
  unsubs.forEach((u) => u())
  unsubs = []
}

async function getMedia() {
  try {
    return markRaw(await navigator.mediaDevices.getUserMedia({ video: true, audio: true }))
  } catch {
    // 没有摄像头时退化为纯语音
    return markRaw(await navigator.mediaDevices.getUserMedia({ audio: true }))
  }
}

export async function startCall(peer) {
  if (call.state !== 'idle') return
  Object.assign(call, { state: 'outgoing', peer, callId: uuidv4(), note: '' })
  const callId = call.callId
  let stream
  try {
    stream = await getMedia()
  } catch (e) {
    reset(`无法打开摄像头/麦克风：${e.message}`)
    return
  }
  if (call.callId !== callId) {
    // 等待授权期间用户已经取消
    stream.getTracks().forEach((t) => t.stop())
    return
  }
  call.local = stream
  signal('call_invite')
  ringTimer = setTimeout(() => {
    if (call.state === 'outgoing' && call.callId === callId) {
      signal('call_hangup')
      reset(REASONS.timeout)
    }
  }, RING_TIMEOUT)
}

function onInvite(env) {
  if (call.state !== 'idle') {
    signal('call_reject', { reason: 'busy' }, env.from, env.payload?.call_id)
    return
  }
  Object.assign(call, { state: 'incoming', peer: env.from, callId: env.payload?.call_id || '', note: '' })
}

export async function accept() {
  if (call.state !== 'incoming') return
  call.state = 'connecting'
  const callId = call.callId
  let stream
  try {
    stream = await getMedia()
  } catch (e) {
    signal('call_reject', { reason: 'media' })
    reset(`无法打开摄像头/麦克风：${e.message}`)
    return
  }
  if (call.callId !== callId) {
    stream.getTracks().forEach((t) => t.stop())
    return
  }
  call.local = stream
  createPC()
  signal('call_accept')
}

export function reject() {
  if (call.state !== 'incoming') return
  signal('call_reject', { reason: 'declined' })
  reset('')
}

export function hangup() {
  if (call.state === 'idle') return
  signal('call_hangup')
  reset('')
}

async function onAccept() {
  if (call.state !== 'outgoing') return
  clearTimeout(ringTimer)
  call.state = 'connecting'
  createPC()
  const offer = await pc.createOffer()
  await pc.setLocalDescription(offer)
  signal('call_offer', { sdp: pc.localDescription.toJSON() })
}

async function onOffer(env) {
  if (!pc) return
  await pc.setRemoteDescription(env.payload.sdp)
  await flushIce()
  const answer = await pc.createAnswer()
  await pc.setLocalDescription(answer)
  signal('call_answer', { sdp: pc.localDescription.toJSON() })
}

async function onAnswer(env) {
  if (!pc) return
  await pc.setRemoteDescription(env.payload.sdp)
  await flushIce()
}

// 对方的 ICE 候选可能比 offer/answer 先到，先存着，等 remoteDescription 设好再加
async function onIce(env) {
  const c = env.payload?.candidate
  if (!c) return
  if (!pc || !pc.remoteDescription) {
    pendingIce.push(c)
    return
  }
  try {
    await pc.addIceCandidate(c)
  } catch (e) {
    console.warn('addIceCandidate', e)
  }
}

async function flushIce() {
  const list = pendingIce
  pendingIce = []
  for (const c of list) {
    try {
      await pc.addIceCandidate(c)
    } catch (e) {
      console.warn('addIceCandidate', e)
    }
  }
}

// 服务端对离线用户的拒绝不带 call_id（它不解析 payload），所以只要来自当前对方就认
function onReject(env) {
  if (call.state !== 'outgoing' || env.from !== call.peer) return
  if (env.payload?.call_id && env.payload.call_id !== call.callId) return
  reset(REASONS[env.payload?.reason] || '对方拒绝了通话')
}

function createPC() {
  pc = new RTCPeerConnection({ iceServers: ICE_SERVERS })
  call.local.getTracks().forEach((t) => pc.addTrack(t, call.local))
  pc.onicecandidate = (e) => {
    if (e.candidate) signal('call_ice', { candidate: e.candidate.toJSON() })
  }
  pc.ontrack = (e) => {
    call.remote = markRaw(e.streams[0])
    call.state = 'in-call'
  }
  pc.onconnectionstatechange = () => {
    if (pc?.connectionState === 'failed') {
      signal('call_hangup')
      reset('连接失败')
    }
  }
}

// reset 必须停掉本地所有 track，摄像头指示灯才会熄灭
function reset(note) {
  clearTimeout(ringTimer)
  if (pc) {
    pc.onicecandidate = pc.ontrack = pc.onconnectionstatechange = null
    pc.close()
    pc = null
  }
  call.local?.getTracks().forEach((t) => t.stop())
  pendingIce = []
  Object.assign(call, { state: 'idle', peer: 0, callId: '', local: null, remote: null, note })
}

export function clearNote() {
  call.note = ''
}
