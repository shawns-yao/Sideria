<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from "vue";
import { api } from "../domain/api";
import { stateLabel, type Task } from "../domain/model";
const tasks = ref<Task[]>([]),
  error = ref("");
let timer: ReturnType<typeof setInterval>;
const controller = new AbortController();
async function load() {
  try {
    tasks.value = await api<Task[]>("/api/tasks", {
      signal: controller.signal,
    });
  } catch (e) {
    if (!controller.signal.aborted) error.value = (e as Error).message;
  }
}
onMounted(() => {
  load();
  timer = setInterval(load, 3000);
});
onBeforeUnmount(() => {
  clearInterval(timer);
  controller.abort();
});
</script>
<template>
  <section class="workspace-panel">
    <div class="section-heading">
      <div>
        <h2>任务中心</h2>
        <p>持久执行事实 · 页面关闭后继续追踪</p>
      </div>
      <button @click="load">刷新</button>
    </div>
    <p v-if="error" class="notice">{{ error }}</p>
    <p v-if="!tasks.length" class="empty">暂无任务</p>
    <article v-for="t in tasks" :key="t.id" class="task-row">
      <div class="spread">
        <h3>{{ t.action }}</h3>
        <span class="badge" :class="t.state">{{ stateLabel(t.state) }}</span>
      </div>
      <p class="mono muted">
        Host {{ t.host_id.slice(0, 12) }} · Task {{ t.id.slice(0, 12) }} ·
        Attempt {{ t.attempt_id.slice(0, 12) }}
      </p>
      <div class="task-stages">
        <span>持久受理</span><i /><span>{{
          t.state === "pending" ? "等待投递" : "执行尝试"
        }}</span
        ><i /><span>{{ stateLabel(t.state) }}</span>
      </div>
      <p v-if="t.error" class="notice">{{ t.error }}</p>
      <details>
        <summary>目标参数与实际结果</summary>
        <pre>{{
          JSON.stringify({ params: t.params, result: t.result }, null, 2)
        }}</pre>
      </details>
      <a
        v-if="t.action === 'file.download' && t.state === 'succeeded'"
        class="button"
        :href="`/api/tasks/${t.id}/download`"
        >领取下载文件</a
      >
    </article>
  </section>
</template>
