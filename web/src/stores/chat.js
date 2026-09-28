import { defineStore } from 'pinia'
import { v4 as uuidv4 } from 'uuid'
import { api } from '../net/http.js'
import * as ws from '../net/ws.js'

const ACK_TIMEOUT = 5000
const PAGE = 200

// 定时器不放进 state：它们不需要响应式，放进去反而会被 devtools 序列化
const ackTimers = new Map()
let unsubs = []

export function privateConvId(a, b) {
  return a < b ? `u_${a}_${b}` : `u_${b}_${a}`
}

export function peerOf(convId, me) {
  const [, a, b] = convId.split('_').map(Number)
  return a === me ? b : a
}

function newConv() {
  return { messages: [], cmids: {}, maxSeq: 0, loaded: false, unread: 0 }
}

export const useChatStore = defineStore('chat', {
  state: () => ({
    me: 0,
    convs: {}, // convId -> { messages(按 seq 升序), cmids, maxSeq, loaded, unread }
    pending: {}, // clientMsgId -> { client_msg_id, conv_id, type, payload, status: sending|failed, error }
    groups: [], // [{ id, name, owner_id }]
    peers: [], // 最近单聊的对方 uid
    users: {}, // uid -> username
    members: {}, // gid -> [uid]
    active: '',
  }),

  getters: {
    activeMessages(state) {
      const c = state.convs[state.active]
      return c ? c.messages : []
    },
    activePending(state) {
      return Object.values(state.pending).filter((p) => p.conv_id === state.active)
    },
  },

  actions: {
    init(me) {
      this.me = me
      this.peers = JSON.parse(sessionStorage.getItem(this.peersKey()) || '[]')
      unsubs.forEach((u) => u())
      unsubs = [
        ws.on('chat', (env) => this.addIncoming(env)),
        ws.on('file', (env) => this.addIncoming(env)),
        ws.on('error', (env) => this.onError(env)),
        ws.onOpen(() => this.resync()),
      ]
      this.ensureUsers(this.peers)
    },

    teardown() {
      unsubs.forEach((u) => u())
      unsubs = []
      ackTimers.forEach((t) => clearTimeout(t))
      ackTimers.clear()
      this.$reset()
    },

    peersKey() {
      return `im_peers_${this.me}`
    },

    ensureConv(convId) {
      if (!this.convs[convId]) this.convs[convId] = newConv()
      return this.convs[convId]
    },

    // addIncoming 处理实时消息和历史消息：按 seq 插入，按 seq 和 client_msg_id 去重，
    // 顺便把同 client_msg_id 的 pending 消息确认掉（这就是发送确认）。
    addIncoming(env) {
      if (env.type !== 'chat' && env.type !== 'file') return
      const conv = this.ensureConv(env.conv_id)

      if (this.pending[env.client_msg_id]) {
        clearTimeout(ackTimers.get(env.client_msg_id))
        ackTimers.delete(env.client_msg_id)
        delete this.pending[env.client_msg_id]
      }
      if (conv.cmids[env.client_msg_id]) return // 重发或重连补齐带来的重复

      const msgs = conv.messages
      let lo = 0
      let hi = msgs.length
      while (lo < hi) {
        const mid = (lo + hi) >> 1
        if (msgs[mid].seq < env.seq) lo = mid + 1
        else hi = mid
      }
      if (msgs[lo]?.seq === env.seq) return
      msgs.splice(lo, 0, env)
      conv.cmids[env.client_msg_id] = true
      if (env.seq > conv.maxSeq) conv.maxSeq = env.seq

      if (env.conv_id !== this.active && env.from !== this.me) conv.unread++
      if (env.conv_id.startsWith('u_')) this.rememberPeer(peerOf(env.conv_id, this.me))
      if (!this.users[env.from]) this.ensureUsers([env.from])
    },

    onError(env) {
      const p = this.pending[env.client_msg_id]
      if (!p) return
      clearTimeout(ackTimers.get(env.client_msg_id))
      ackTimers.delete(env.client_msg_id)
      p.status = 'failed'
      p.error = env.payload?.message || env.payload?.code || 'error'
    },

    send(convId, type, payload) {
      const msg = {
        client_msg_id: uuidv4(),
        conv_id: convId,
        type,
        payload,
        from: this.me,
        status: 'sending',
        error: '',
        created: Date.now(),
      }
      this.pending[msg.client_msg_id] = msg
      this.transmit(msg.client_msg_id)
    },

    // 重发用同一个 client_msg_id：如果第一次其实已经成功，服务端 INSERT IGNORE、前端按 cmid 去重
    resend(cmid) {
      if (this.pending[cmid]) this.transmit(cmid)
    },

    transmit(cmid) {
      const p = this.pending[cmid]
      p.status = 'sending'
      p.error = ''
      clearTimeout(ackTimers.get(cmid))
      ws.send({ type: p.type, conv_id: p.conv_id, client_msg_id: cmid, payload: p.payload }, () => {
        // 从真正写出 socket 开始计时，离线排队的时间不算
        ackTimers.set(
          cmid,
          setTimeout(() => {
            ackTimers.delete(cmid)
            const q = this.pending[cmid]
            if (q && q.status === 'sending') {
              q.status = 'failed'
              q.error = '发送超时'
            }
          }, ACK_TIMEOUT),
        )
      })
    },

    async loadHistory(convId, afterSeq) {
      const conv = this.ensureConv(convId)
      let after = afterSeq ?? (conv.loaded ? conv.maxSeq : 0)
      for (;;) {
        const r = await api(`/api/messages?conv_id=${encodeURIComponent(convId)}&after_seq=${after}&limit=${PAGE}`)
        r.messages.forEach((m) => this.addIncoming(m))
        if (r.messages.length < PAGE) break
        after = r.messages[r.messages.length - 1].seq
      }
      conv.loaded = true
    },

    // 重连后补齐：对每个已加载的会话，从断线前的 maxSeq 往后拉。
    // 必须在 onopen 里同步记下各会话的 maxSeq，否则重连后先到的实时消息会把它推高，漏掉中间一段。
    resync() {
      const marks = Object.entries(this.convs)
        .filter(([, c]) => c.loaded)
        .map(([id, c]) => [id, c.maxSeq])
      for (const [id, seq] of marks) {
        this.loadHistory(id, seq).catch((e) => console.warn('resync', id, e))
      }
    },

    async open(convId) {
      this.active = convId
      const conv = this.ensureConv(convId)
      conv.unread = 0
      if (convId.startsWith('g_')) this.loadMembers(Number(convId.slice(2))).catch(() => {})
      if (!conv.loaded) await this.loadHistory(convId)
    },

    openPrivate(uid, username) {
      if (username) this.users[uid] = username
      this.rememberPeer(uid)
      return this.open(privateConvId(this.me, uid))
    },

    rememberPeer(uid) {
      if (!uid || uid === this.me || this.peers.includes(uid)) return
      this.peers.unshift(uid)
      sessionStorage.setItem(this.peersKey(), JSON.stringify(this.peers))
      this.ensureUsers([uid])
    },

    async loadGroups() {
      const r = await api('/api/my/groups')
      this.groups = r.groups
    },

    async loadMembers(gid) {
      const r = await api(`/api/groups/${gid}/members`)
      r.members.forEach((u) => (this.users[u.id] = u.username))
      this.members[gid] = r.members.map((u) => u.id)
    },

    async ensureUsers(ids) {
      const missing = [...new Set(ids)].filter((id) => id && !this.users[id])
      if (!missing.length) return
      missing.forEach((id) => (this.users[id] = `#${id}`)) // 占位，避免并发重复请求
      try {
        const r = await api(`/api/users?ids=${missing.join(',')}`)
        r.users.forEach((u) => (this.users[u.id] = u.username))
      } catch {
        missing.forEach((id) => delete this.users[id])
      }
    },

    async createGroup(name) {
      const g = await api('/api/groups', { method: 'POST', body: { name } })
      await this.loadGroups()
      await this.open(`g_${g.id}`)
    },

    async joinGroup(gid) {
      const g = await api(`/api/groups/${gid}/join`, { method: 'POST' })
      await this.loadGroups()
      await this.open(`g_${g.id}`)
    },
  },
})
