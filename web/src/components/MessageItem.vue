<script setup>
import { computed } from 'vue'

const props = defineProps({
  msg: { type: Object, required: true }, // 服务端 Envelope，或 pending 消息（带 status）
  mine: Boolean,
  sender: { type: String, default: '' },
  showSender: Boolean,
})
defineEmits(['resend'])

const p = computed(() => props.msg.payload || {})
const isImage = computed(() => props.msg.type === 'file' && String(p.value.mime || '').startsWith('image/'))
const time = computed(() => {
  const ts = props.msg.ts || props.msg.created
  return ts ? new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : ''
})

function fmtSize(n) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
</script>

<template>
  <div class="msg" :class="{ mine, pending: msg.status === 'sending', failed: msg.status === 'failed' }">
    <div v-if="showSender && !mine" class="sender">{{ sender }}</div>
    <div class="bubble">
      <template v-if="msg.type === 'chat'">{{ p.text }}</template>
      <template v-else-if="isImage">
        <a :href="p.url" target="_blank" rel="noopener">
          <img :src="p.url" :alt="p.name" class="thumb" />
        </a>
      </template>
      <template v-else-if="msg.type === 'file'">
        <div class="file">
          <span class="file-icon">📄</span>
          <div>
            <div class="file-name">{{ p.name }}</div>
            <div class="file-meta">
              {{ fmtSize(p.size || 0) }} ·
              <a :href="p.url" :download="p.name">下载</a>
            </div>
          </div>
        </div>
      </template>
    </div>
    <div class="meta">
      <span v-if="msg.status === 'sending'">发送中…</span>
      <template v-else-if="msg.status === 'failed'">
        <span class="err">{{ msg.error || '发送失败' }}</span>
        <button class="link" @click="$emit('resend', msg.client_msg_id)">重发</button>
      </template>
      <span v-else>{{ time }}</span>
    </div>
  </div>
</template>

<style scoped>
.msg {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  margin: 6px 0;
}
.msg.mine {
  align-items: flex-end;
}
.sender {
  font-size: 12px;
  color: var(--muted);
  margin: 0 4px 2px;
}
.bubble {
  max-width: 70%;
  padding: 8px 12px;
  border-radius: 10px;
  background: var(--bubble);
  white-space: pre-wrap;
  word-break: break-word;
}
.mine .bubble {
  background: var(--bubble-mine);
}
.pending .bubble {
  opacity: 0.5;
}
.failed .bubble {
  outline: 1px solid var(--danger);
}
.thumb {
  display: block;
  max-width: 220px;
  max-height: 220px;
  border-radius: 6px;
}
.file {
  display: flex;
  gap: 8px;
  align-items: center;
}
.file-icon {
  font-size: 24px;
}
.file-name {
  font-weight: 500;
}
.file-meta {
  font-size: 12px;
  color: var(--muted);
}
.meta {
  font-size: 11px;
  color: var(--muted);
  margin: 2px 4px 0;
}
.err {
  color: var(--danger);
  margin-right: 6px;
}
</style>
