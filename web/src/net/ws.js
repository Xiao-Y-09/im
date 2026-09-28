import { ref } from 'vue'

// 全局唯一的 WebSocket 连接。按 type 分发消息；断线指数退避重连；
// 断开期间发送的消息先排队，重连后按顺序补发。

export const status = ref('closed') // connecting | open | closed

const handlers = new Map() // type -> Set<fn>
const openHandlers = new Set()
const queue = [] // { data, onSent }

let sock = null
let token = ''
let retry = 0
let timer = null
let stopped = true

export function connect(t) {
  token = t
  stopped = false
  retry = 0
  open()
}

export function disconnect() {
  stopped = true
  clearTimeout(timer)
  queue.length = 0
  if (sock) {
    const s = sock
    sock = null
    s.close()
  }
  status.value = 'closed'
}

function open() {
  clearTimeout(timer)
  if (stopped) return
  status.value = 'connecting'
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const s = new WebSocket(`${proto}://${location.host}/ws?token=${encodeURIComponent(token)}`)
  sock = s

  s.onopen = () => {
    if (sock !== s) return
    status.value = 'open'
    retry = 0
    // 先通知订阅者（它们要同步地记下断线前的 maxSeq），再补发排队的消息
    openHandlers.forEach((fn) => fn())
    flush()
  }
  s.onmessage = (ev) => {
    let env
    try {
      env = JSON.parse(ev.data)
    } catch {
      return
    }
    handlers.get(env.type)?.forEach((fn) => fn(env))
    handlers.get('*')?.forEach((fn) => fn(env))
  }
  s.onclose = () => {
    if (sock !== s) return
    sock = null
    status.value = 'closed'
    scheduleReconnect()
  }
}

function scheduleReconnect() {
  if (stopped) return
  const delay = Math.min(30000, 1000 * 2 ** retry) // 1s, 2s, 4s … 上限 30s
  retry++
  clearTimeout(timer)
  timer = setTimeout(open, delay)
}

// 断网时浏览器不一定会关闭已有的 WebSocket（DevTools 的 Offline 就不会），
// 所以主动关掉，恢复网络时立即重连而不是等退避计时器。
window.addEventListener('offline', () => {
  if (sock) {
    const s = sock
    sock = null
    s.close()
    status.value = 'closed'
    scheduleReconnect()
  }
})
window.addEventListener('online', () => {
  if (stopped) return
  retry = 0
  if (!sock) open()
})

function flush() {
  while (queue.length && sock && sock.readyState === WebSocket.OPEN) {
    const { data, onSent } = queue.shift()
    sock.send(data)
    onSent?.()
  }
}

// send 的 onSent 在消息真正写进 socket 时调用（排队期间不调用），
// 调用方据此开始计算“5 秒无回显”超时。
export function send(env, onSent) {
  const data = JSON.stringify(env)
  if (sock && sock.readyState === WebSocket.OPEN && queue.length === 0) {
    sock.send(data)
    onSent?.()
  } else {
    queue.push({ data, onSent })
  }
}

export function on(type, fn) {
  if (!handlers.has(type)) handlers.set(type, new Set())
  handlers.get(type).add(fn)
  return () => handlers.get(type)?.delete(fn)
}

export function onOpen(fn) {
  openHandlers.add(fn)
  return () => openHandlers.delete(fn)
}
