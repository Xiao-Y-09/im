<script setup>
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth.js'
import { useChatStore, peerOf } from '../stores/chat.js'
import { usePresenceStore } from '../stores/presence.js'
import * as ws from '../net/ws.js'
import { api } from '../net/http.js'
import { call, initCalls, teardownCalls, startCall } from '../call/webrtc.js'
import MessageItem from '../components/MessageItem.vue'
import CallModal from '../components/CallModal.vue'

const MAX_UPLOAD = 20 * 1024 * 1024

const auth = useAuthStore()
const chat = useChatStore()
const presence = usePresenceStore()
const router = useRouter()

const ready = ref(false)
const draft = ref('')
const searchQ = ref('')
const searchResults = ref([])
const newGroupName = ref('')
const joinGroupId = ref('')
const notice = ref('')
const uploading = ref(false)
const fileInput = ref(null)
const listEl = ref(null)

const me = computed(() => auth.user?.id || 0)
const wsStatus = ws.status
const statusText = { open: '已连接', connecting: '连接中…', closed: '已断开，重连中…' }

const convItems = computed(() => {
  const groups = chat.groups.map((g) => ({ id: `g_${g.id}`, title: g.name, sub: `群 #${g.id}`, peer: 0 }))
  const privates = chat.peers.map((uid) => ({
    id: uid < me.value ? `u_${uid}_${me.value}` : `u_${me.value}_${uid}`,
    title: chat.users[uid] || `#${uid}`,
    sub: '单聊',
    peer: uid,
  }))
  return [...groups, ...privates]
})

const activeItem = computed(() => convItems.value.find((c) => c.id === chat.active))
const activePeer = computed(() => (chat.active.startsWith('u_') ? peerOf(chat.active, me.value) : 0))
const isGroup = computed(() => chat.active.startsWith('g_'))
const memberCount = computed(() => chat.members[Number(chat.active.slice(2))]?.length || 0)
const callPeerName = computed(() => chat.users[call.peer] || `#${call.peer}`)

function flash(msg) {
  notice.value = msg
  setTimeout(() => notice.value === msg && (notice.value = ''), 4000)
}

async function guard(fn) {
  try {
    await fn()
  } catch (e) {
    flash(e.message)
  }
}

onMounted(async () => {
  try {
    const user = auth.user || (await auth.fetchMe())
    chat.init(user.id)
    initCalls(user.id)
    ws.connect(auth.token)
    await chat.loadGroups()
    presence.start(() => chat.peers)
    ready.value = true
  } catch (e) {
    flash(e.message)
  }
})

onUnmounted(() => {
  presence.stop()
})

// 会话里有新消息时滚到底部
watch(
  () => [chat.active, chat.activeMessages.length, chat.activePending.length],
  async () => {
    await nextTick()
    if (listEl.value) listEl.value.scrollTop = listEl.value.scrollHeight
  },
)
// 新出现的单聊对象立即查一次在线状态，不等 30 秒
watch(
  () => chat.peers.length,
  () => presence.refresh(chat.peers),
)

function sendText() {
  const text = draft.value.trim()
  if (!text || !chat.active) return
  chat.send(chat.active, 'chat', { text })
  draft.value = ''
}

async function onFile(e) {
  const f = e.target.files?.[0]
  e.target.value = ''
  if (!f || !chat.active) return
  if (f.size > MAX_UPLOAD) {
    flash('文件超过 20MB')
    return
  }
  uploading.value = true
  const form = new FormData()
  form.append('file', f)
  await guard(async () => {
    const meta = await api('/api/upload', { method: 'POST', form })
    chat.send(chat.active, 'file', meta)
  })
  uploading.value = false
}

async function search() {
  const q = searchQ.value.trim()
  if (!q) {
    searchResults.value = []
    return
  }
  await guard(async () => {
    const r = await api(`/api/users?q=${encodeURIComponent(q)}`)
    searchResults.value = r.users.filter((u) => u.id !== me.value)
    if (!searchResults.value.length) flash('没有找到用户')
  })
}

function openUser(u) {
  searchResults.value = []
  searchQ.value = ''
  guard(() => chat.openPrivate(u.id, u.username))
}

function createGroup() {
  const name = newGroupName.value.trim()
  if (!name) return
  guard(async () => {
    await chat.createGroup(name)
    newGroupName.value = ''
  })
}

function joinGroup() {
  const id = Number(joinGroupId.value)
  if (!id) return
  guard(async () => {
    await chat.joinGroup(id)
    joinGroupId.value = ''
  })
}

function logout() {
  teardownCalls()
  presence.stop()
  ws.disconnect()
  chat.teardown()
  auth.logout()
  router.push('/login')
}
</script>

<template>
  <div class="layout">
    <aside class="side">
      <header class="me">
        <div>
          <strong>{{ auth.user?.username }}</strong>
          <span class="uid">#{{ me }}</span>
        </div>
        <button class="link" @click="logout">退出</button>
      </header>
      <div class="ws" :class="wsStatus">{{ statusText[wsStatus] }}</div>

      <form class="row" @submit.prevent="search">
        <input v-model="searchQ" placeholder="搜索用户名发起单聊" />
        <button>搜</button>
      </form>
      <ul v-if="searchResults.length" class="results">
        <li v-for="u in searchResults" :key="u.id" @click="openUser(u)">{{ u.username }} <span class="uid">#{{ u.id }}</span></li>
      </ul>

      <form class="row" @submit.prevent="createGroup">
        <input v-model="newGroupName" placeholder="新群名称" />
        <button>建群</button>
      </form>
      <form class="row" @submit.prevent="joinGroup">
        <input v-model="joinGroupId" placeholder="群号" inputmode="numeric" />
        <button>加群</button>
      </form>

      <ul class="convs">
        <li
          v-for="c in convItems"
          :key="c.id"
          :class="{ active: c.id === chat.active }"
          @click="guard(() => chat.open(c.id))"
        >
          <span v-if="c.peer" class="dot" :class="{ on: presence.online[c.peer] }"></span>
          <span v-else class="dot group">#</span>
          <span class="title">{{ c.title }}</span>
          <span v-if="chat.convs[c.id]?.unread" class="badge">{{ chat.convs[c.id].unread }}</span>
        </li>
        <li v-if="ready && !convItems.length" class="empty">还没有会话：搜索用户或建群开始聊天</li>
      </ul>
    </aside>

    <main class="main">
      <template v-if="chat.active">
        <header class="conv-head">
          <div>
            <strong>{{ activeItem?.title || chat.active }}</strong>
            <span v-if="isGroup" class="sub">群号 {{ chat.active.slice(2) }} · {{ memberCount }} 人</span>
            <span v-else class="sub">{{ presence.online[activePeer] ? '在线' : '离线' }}</span>
          </div>
          <button v-if="!isGroup" :disabled="call.state !== 'idle'" @click="startCall(activePeer)">📹 视频通话</button>
        </header>

        <div ref="listEl" class="list">
          <MessageItem
            v-for="m in chat.activeMessages"
            :key="m.seq"
            :msg="m"
            :mine="m.from === me"
            :sender="chat.users[m.from] || `#${m.from}`"
            :show-sender="isGroup"
          />
          <MessageItem
            v-for="p in chat.activePending"
            :key="p.client_msg_id"
            :msg="p"
            mine
            @resend="chat.resend"
          />
        </div>

        <form class="composer" @submit.prevent="sendText">
          <button type="button" :disabled="uploading" title="发送文件" @click="fileInput.click()">
            {{ uploading ? '上传中…' : '📎' }}
          </button>
          <input ref="fileInput" type="file" hidden @change="onFile" />
          <input v-model="draft" placeholder="输入消息，回车发送" />
          <button class="primary">发送</button>
        </form>
      </template>
      <div v-else class="placeholder">选择左侧会话开始聊天</div>
    </main>

    <div v-if="notice" class="notice" @click="notice = ''">{{ notice }}</div>
    <CallModal :peer-name="callPeerName" />
  </div>
</template>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: 280px 1fr;
  height: 100vh;
}
.side {
  background: var(--panel);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  overflow: hidden;
}
.me {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.uid {
  color: var(--muted);
  font-size: 12px;
  margin-left: 4px;
}
.ws {
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 10px;
  align-self: flex-start;
  background: var(--bubble);
}
.ws.open {
  color: var(--ok);
}
.ws.closed {
  color: var(--danger);
}
.row {
  display: flex;
  gap: 6px;
}
.row input {
  flex: 1;
  min-width: 0;
}
.results,
.convs {
  list-style: none;
  margin: 0;
  padding: 0;
}
.results {
  border: 1px solid var(--border);
  border-radius: 6px;
}
.results li {
  padding: 6px 10px;
  cursor: pointer;
}
.results li:hover {
  background: var(--bubble);
}
.convs {
  flex: 1;
  overflow-y: auto;
  margin-top: 6px;
}
.convs li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 6px;
  cursor: pointer;
}
.convs li:hover {
  background: var(--bubble);
}
.convs li.active {
  background: var(--bubble-mine);
}
.convs li.empty {
  color: var(--muted);
  font-size: 13px;
  cursor: default;
}
.dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: #bbb;
  flex: none;
}
.dot.on {
  background: var(--ok);
}
.dot.group {
  background: none;
  width: auto;
  height: auto;
  color: var(--muted);
  font-size: 12px;
}
.title {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.badge {
  background: var(--danger);
  color: #fff;
  font-size: 11px;
  border-radius: 9px;
  padding: 0 6px;
}
.main {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.conv-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  background: var(--panel);
}
.sub {
  color: var(--muted);
  font-size: 12px;
  margin-left: 8px;
}
.list {
  flex: 1;
  overflow-y: auto;
  padding: 12px 16px;
}
.composer {
  display: flex;
  gap: 8px;
  padding: 12px 16px;
  border-top: 1px solid var(--border);
  background: var(--panel);
}
.composer input:not([type='file']) {
  flex: 1;
}
.placeholder {
  margin: auto;
  color: var(--muted);
}
.notice {
  position: fixed;
  bottom: 20px;
  left: 50%;
  transform: translateX(-50%);
  background: var(--danger);
  color: #fff;
  padding: 8px 16px;
  border-radius: 8px;
  cursor: pointer;
}
</style>
