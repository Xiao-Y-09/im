<script setup>
import { ref, watch, onMounted } from 'vue'
import { call, accept, reject, hangup, clearNote } from '../call/webrtc.js'

const props = defineProps({
  peerName: { type: String, default: '' },
})

const localVideo = ref(null)
const remoteVideo = ref(null)

function bind() {
  if (localVideo.value) localVideo.value.srcObject = call.local
  if (remoteVideo.value) remoteVideo.value.srcObject = call.remote
}
watch(() => [call.local, call.remote, call.state], bind, { flush: 'post' })
onMounted(bind)

// 结束提示几秒后自动消失
watch(
  () => call.note,
  (n) => n && setTimeout(() => call.note === n && clearNote(), 4000),
)
</script>

<template>
  <div v-if="call.state !== 'idle'" class="overlay">
    <div class="modal">
      <template v-if="call.state === 'incoming'">
        <h3>{{ props.peerName }} 邀请你视频通话</h3>
        <div class="actions">
          <button class="primary" @click="accept">接听</button>
          <button class="danger" @click="reject">拒绝</button>
        </div>
      </template>
      <template v-else>
        <h3 v-if="call.state === 'outgoing'">正在呼叫 {{ props.peerName }}…</h3>
        <h3 v-else-if="call.state === 'connecting'">正在连接…</h3>
        <h3 v-else>与 {{ props.peerName }} 通话中</h3>
        <div class="videos">
          <video ref="remoteVideo" class="remote" autoplay playsinline></video>
          <video ref="localVideo" class="local" autoplay playsinline muted></video>
        </div>
        <div class="actions">
          <button class="danger" @click="hangup">挂断</button>
        </div>
      </template>
    </div>
  </div>
  <div v-else-if="call.note" class="toast" @click="clearNote">{{ call.note }}</div>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10;
}
.modal {
  background: var(--panel);
  border-radius: 12px;
  padding: 20px;
  width: min(720px, 92vw);
  text-align: center;
}
.videos {
  position: relative;
  background: #000;
  border-radius: 8px;
  overflow: hidden;
  aspect-ratio: 16 / 9;
}
.remote {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.local {
  position: absolute;
  right: 10px;
  bottom: 10px;
  width: 28%;
  border-radius: 6px;
  border: 2px solid #fff;
  background: #222;
}
.actions {
  margin-top: 14px;
  display: flex;
  gap: 12px;
  justify-content: center;
}
.toast {
  position: fixed;
  top: 16px;
  left: 50%;
  transform: translateX(-50%);
  background: var(--text);
  color: var(--panel);
  padding: 8px 16px;
  border-radius: 8px;
  z-index: 10;
  cursor: pointer;
}
</style>
