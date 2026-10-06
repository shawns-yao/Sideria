<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from "vue";
import { api, query } from "../domain/api";
import type { Host } from "../domain/model";
const props = defineProps<{ host: Host }>();
const projects = ref<any[]>([]),
  name = ref(""),
  path = ref("."),
  error = ref(""),
  status = ref<any>(null),
  selected = ref("");
let generation = 0,
  controller = new AbortController();
async function load() {
  controller.abort();
  controller = new AbortController();
  const current = ++generation;
  status.value = null;
  selected.value = "";
  try {
    const result = await api<any[]>("/api/projects", {
      signal: controller.signal,
    });
    if (current === generation)
      projects.value = result.filter((p) => p.host_id === props.host.id);
  } catch (e) {
    if (current === generation) error.value = (e as Error).message;
  }
}
watch(() => props.host.id, load, { immediate: true });
onBeforeUnmount(() => {
  generation++;
  controller.abort();
});
async function register() {
  const target = props.host.id;
  try {
    await api(`/api/hosts/${target}/projects`, {
      method: "POST",
      body: JSON.stringify({ name: name.value, path: path.value }),
    });
    if (target === props.host.id) {
      name.value = "";
      await load();
    }
  } catch (e) {
    error.value = (e as Error).message;
  }
}
async function inspect(p: any) {
  const current = generation;
  selected.value = p.id;
  error.value = "";
  try {
    const value = await query(
      props.host.id,
      "git.status",
      { path: p.path },
      controller.signal,
    );
    if (current === generation && selected.value === p.id) status.value = value;
  } catch (e) {
    if (current === generation) error.value = (e as Error).message;
  }
}
</script>
<template>
  <section class="workspace-panel">
    <div class="section-heading">
      <div>
        <h2>项目与 Git</h2>
        <p>{{ host.name }} · 本地仓库状态，不执行 fetch 或写操作</p>
      </div>
    </div>
    <form class="toolbar" @submit.prevent="register">
      <input
        v-model="name"
        placeholder="项目名称"
        aria-label="项目名称"
        required
      /><input
        v-model="path"
        placeholder="授权根内相对路径"
        aria-label="项目路径"
        required
      /><button>登记项目</button>
    </form>
    <p v-if="error" class="notice" role="alert">{{ error }}</p>
    <div class="project-layout">
      <div>
        <button
          v-for="p in projects"
          :key="p.id"
          class="project-row"
          :class="{ selected: selected === p.id }"
          @click="inspect(p)"
        >
          <b>{{ p.name }}</b
          ><span class="mono">{{ p.path }}</span>
        </button>
        <p v-if="!projects.length" class="empty">尚未登记项目</p>
      </div>
      <div class="result-panel">
        <template v-if="status"
          ><h3>工作区状态</h3>
          <p class="mono">HEAD {{ status.head || "空仓库" }}</p>
          <div v-if="status.summary" class="git-summary">
            <span class="badge">{{
              status.summary.detached ? "Detached HEAD" : status.summary.branch
            }}</span
            ><span>暂存 {{ status.summary.staged }}</span
            ><span>未暂存 {{ status.summary.unstaged }}</span
            ><span>未跟踪 {{ status.summary.untracked }}</span
            ><span>冲突 {{ status.summary.conflicts }}</span>
          </div>
          <p v-if="status.summary" class="muted">
            上游 {{ status.summary.upstream || "未配置" }} · ahead
            {{ status.summary.ahead ?? "不可比较" }} / behind
            {{ status.summary.behind ?? "不可比较" }}
          </p>
          <pre>{{ status.status }}</pre>
          <h3>分支与最近提交</h3>
          <p
            v-for="remote in status.remotes"
            :key="remote.name"
            class="mono muted"
          >
            {{ remote.name }} · {{ remote.location }}
          </p>
          <pre
            >{{ status.branches }}
{{ status.recent_commits }}</pre>
          <p class="notice">
            远端新鲜度未知，仅比较已有本地引用。实际运行版本未核实。
          </p></template
        >
        <p v-else class="empty">选择项目获取当前证据</p>
      </div>
    </div>
  </section>
</template>
