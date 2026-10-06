<script setup lang="ts">
import { ref, watch, onBeforeUnmount, computed } from "vue";
import { Folder, FileText, ArrowUp, Download, Upload } from "@lucide/vue";
import { query, api, task } from "../domain/api";
import { bytes, type Host } from "../domain/model";
const props = defineProps<{ host: Host }>();
const path = ref("."),
  entries = ref<any[]>([]),
  preview = ref(""),
  selected = ref(""),
  error = ref(""),
  busy = ref(false),
  more = ref(false),
  offset = ref(0);
const showHidden = ref(false);
const visibleEntries = computed(() =>
  entries.value.filter((e) => showHidden.value || !e.name.startsWith(".")),
);
let controller = new AbortController(),
  generation = 0;
async function load(append = false) {
  controller.abort();
  controller = new AbortController();
  const current = ++generation;
  busy.value = true;
  error.value = "";
  if (!append) {
    offset.value = 0;
    entries.value = [];
    preview.value = "";
    selected.value = "";
  }
  try {
    const result = await query(
      props.host.id,
      "file.list",
      { path: path.value, offset: offset.value },
      controller.signal,
    );
    if (current !== generation) return;
    entries.value = append
      ? [...entries.value, ...result.entries]
      : result.entries;
    more.value = result.more;
  } catch (e) {
    if (current === generation && !controller.signal.aborted)
      error.value = (e as Error).message;
  } finally {
    if (current === generation) busy.value = false;
  }
}
watch(
  () => props.host.id,
  () => {
    path.value = ".";
    load();
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  generation++;
  controller.abort();
});
function child(name: string) {
  return path.value === "." ? name : `${path.value}/${name}`;
}
async function open(entry: any) {
  if (entry.directory) {
    path.value = child(entry.name);
    await load();
    return;
  }
  const target = props.host.id,
    current = generation;
  selected.value = child(entry.name);
  const file = selected.value;
  error.value = "";
  try {
    const result = await query(
      target,
      "file.read",
      { path: file },
      controller.signal,
    );
    if (current === generation && selected.value === file)
      preview.value = result.text + (result.truncated ? "\n[预览已截断]" : "");
  } catch (e) {
    if (current === generation) error.value = (e as Error).message;
  }
}
function up() {
  path.value = path.value.includes("/")
    ? path.value.slice(0, path.value.lastIndexOf("/"))
    : ".";
  load();
}
async function download() {
  const target = props.host.id,
    file = selected.value;
  if (!file) return;
  try {
    const t = await task(target, "file.download", { path: file });
    error.value = `下载任务 ${t.id.slice(0, 8)} 已受理，请到任务页领取文件。`;
  } catch (e) {
    error.value = (e as Error).message;
  }
}
async function upload(event: Event) {
  const input = event.target as HTMLInputElement,
    file = input.files?.[0];
  if (!file) return;
  const target = props.host.id,
    destination = child(file.name);
  if (
    !confirm(
      `上传到 ${props.host.name} / ${destination}？只新增，不覆盖。最大 32 MiB。`,
    )
  )
    return;
  try {
    await api(
      `/api/hosts/${target}/uploads?path=${encodeURIComponent(destination)}`,
      {
        method: "POST",
        body: file,
        headers: {
          "Content-Type": "application/octet-stream",
          "Idempotency-Key": crypto.randomUUID(),
          "X-Confirm-Target": target,
        },
      },
    );
    error.value = "上传任务已受理，可在任务页查看实际结果。";
  } catch (e) {
    error.value = (e as Error).message;
  }
  input.value = "";
}
</script>
<template>
  <section class="workspace-panel">
    <div class="section-heading">
      <div>
        <h2>文件</h2>
        <p>目标 {{ host.name }} · Agent 授权根目录内</p>
      </div>
      <label class="button"
        ><Upload :size="16" />上传新文件<input
          type="file"
          class="sr-only"
          @change="upload"
      /></label>
    </div>
    <form class="toolbar" @submit.prevent="load()">
      <button type="button" aria-label="上一级" @click="up">
        <ArrowUp :size="16" /></button
      ><input v-model="path" aria-label="相对路径" /><button :disabled="busy">
        打开目录
      </button>
    </form>
    <p v-if="error" role="status" class="notice">{{ error }}</p>
    <label class="hidden-toggle"
      ><input v-model="showHidden" type="checkbox" />显示隐藏文件</label
    >
    <div class="file-layout">
      <div class="file-list">
        <p v-if="busy">读取中…</p>
        <p v-else-if="!entries.length" class="empty">此目录为空</p>
        <button
          v-for="entry in visibleEntries"
          :key="entry.name"
          class="file-row"
          @click="open(entry)"
        >
          <Folder v-if="entry.directory" :size="17" /><FileText
            v-else
            :size="17"
          /><span
            >{{ entry.name
            }}<small v-if="entry.symlink"> → {{ entry.symlink }}</small></span
          ><small>{{ entry.mode }} · {{ bytes(entry.size) }}</small></button
        ><button
          v-if="more"
          @click="
            offset += 200;
            load(true);
          "
        >
          读取下一批
        </button>
      </div>
      <div class="file-preview">
        <div class="spread">
          <span class="mono">{{ selected || "选择文件以预览" }}</span
          ><button v-if="selected" @click="download">
            <Download :size="15" />下载
          </button>
        </div>
        <pre>{{
          preview || "受限文本预览 · 最大 64 KiB\n编辑与覆盖尚未开放。"
        }}</pre>
      </div>
    </div>
  </section>
</template>
