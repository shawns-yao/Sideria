<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from "vue";
import { Box, RefreshCw } from "@lucide/vue";
import { query, task } from "../domain/api";
import type { Host } from "../domain/model";
const props = defineProps<{ host: Host }>();
const containers = ref<any[]>([]),
  detail = ref(""),
  error = ref(""),
  busy = ref(false),
  selected = ref("");
let generation = 0,
  controller = new AbortController();
async function load() {
  controller.abort();
  controller = new AbortController();
  const current = ++generation;
  containers.value = [];
  detail.value = "";
  selected.value = "";
  busy.value = true;
  error.value = "";
  try {
    const result = await query(
      props.host.id,
      "docker.list",
      {},
      controller.signal,
    );
    if (current === generation) containers.value = result;
  } catch (e) {
    if (current === generation) error.value = (e as Error).message;
  } finally {
    if (current === generation) busy.value = false;
  }
}
watch(() => props.host.id, load, { immediate: true });
onBeforeUnmount(() => {
  generation++;
  controller.abort();
});
async function inspect(c: any, action: string) {
  const current = generation;
  selected.value = c.Id;
  try {
    const result = await query(
      props.host.id,
      action,
      { container: c.Id },
      controller.signal,
    );
    if (current === generation && selected.value === c.Id)
      detail.value =
        action === "docker.logs"
          ? result.logs
          : JSON.stringify(result, null, 2);
  } catch (e) {
    if (current === generation) error.value = (e as Error).message;
  }
}
async function operate(c: any, action: string) {
  const target = props.host.id;
  if (
    !confirm(
      `目标：${props.host.name}\n容器：${c.Names?.[0] ?? c.Id}\n操作：${action}\n可能中断业务。确认提交？`,
    )
  )
    return;
  try {
    const t = await task(target, action, { container: c.Id });
    error.value = `任务 ${t.id.slice(0, 8)} 已受理；实际状态请查看任务页。`;
  } catch (e) {
    error.value = (e as Error).message;
  }
}
</script>
<template>
  <section class="workspace-panel">
    <div class="section-heading">
      <div>
        <h2>Docker</h2>
        <p>{{ host.name }} · 独立容器运行时</p>
      </div>
      <button @click="load"><RefreshCw :size="16" />刷新</button>
    </div>
    <p v-if="error" class="notice" role="status">{{ error }}</p>
    <p v-if="busy">读取容器中…</p>
    <div class="docker-layout">
      <div class="container-list">
        <p v-if="!busy && !containers.length" class="empty">没有可展示的容器</p>
        <article
          v-for="c in containers"
          :key="c.Id"
          class="container-row"
          :class="{ selected: selected === c.Id }"
        >
          <div class="spread">
            <h3><Box :size="17" />{{ c.Names?.[0]?.replace(/^\//, "") }}</h3>
            <span class="badge">{{ c.State }}</span>
          </div>
          <p class="mono muted">{{ c.Image }} · {{ c.Id.slice(0, 12) }}</p>
          <p>{{ c.Status }}</p>
          <div class="toolbar">
            <button @click="inspect(c, 'docker.inspect')">详情</button
            ><button @click="inspect(c, 'docker.logs')">日志</button
            ><button @click="inspect(c, 'docker.stats')">资源</button
            ><button @click="operate(c, 'docker.start')">启动</button
            ><button @click="operate(c, 'docker.stop')">停止</button
            ><button @click="operate(c, 'docker.restart')">重启</button>
          </div>
        </article>
      </div>
      <div class="result-panel">
        <h3>当前对象</h3>
        <p class="mono muted">{{ selected || "选择容器" }}</p>
        <pre>{{
          detail || "查看近期日志、状态或资源。\n容器运行不等于业务健康。"
        }}</pre>
      </div>
    </div>
  </section>
</template>
