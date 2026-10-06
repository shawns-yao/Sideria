<script setup lang="ts">
import { ref, onBeforeUnmount, watch, computed } from "vue";
import { Sparkles, X } from "@lucide/vue";
import { api } from "../domain/api";
import { stateLabel } from "../domain/model";
const props = defineProps<{
  hostID: string;
  hostName: string;
  demo: boolean;
  enabled: boolean;
  hosts: { id: string; name: string }[];
}>();
defineEmits<{ close: [] }>();
const prompt = ref("分析这台服务器的当前状态，说明证据、不确定性与建议。"),
  mode = ref("Suggest"),
  busy = ref(false),
  error = ref(""),
  result = ref<any>(null),
  target = ref("");
const selectedHosts = ref(props.hostID ? [props.hostID] : []);
const selectionName = computed(() =>
  props.hosts
    .filter((h) => selectedHosts.value.includes(h.id))
    .map((h) => h.name)
    .join("、"),
);
watch(
  () => props.hostID,
  (id) => {
    if (!busy.value) selectedHosts.value = id ? [id] : [];
  },
);
let controller = new AbortController();
async function analyze() {
  if (props.demo || !props.enabled) return;
  controller = new AbortController();
  busy.value = true;
  error.value = "";
  result.value = null;
  target.value = selectionName.value;
  const scope = [...selectedHosts.value];
  try {
    result.value = await api("/api/analyses", {
      method: "POST",
      body: JSON.stringify({
        hosts: scope,
        prompt: prompt.value,
        mode: mode.value,
      }),
      signal: controller.signal,
    });
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
onBeforeUnmount(() => controller.abort());
</script>
<template>
  <aside class="ai-panel" aria-label="AI 分析">
    <header>
      <h2><Sparkles :size="18" />巡星 AI</h2>
      <button aria-label="关闭 AI" @click="$emit('close')">
        <X :size="18" />
      </button>
    </header>
    <p class="eyebrow">OBSERVE / SUGGEST</p>
    <div class="scope">
      <span class="status-dot" />目标
      <strong>{{ busy || result ? target : selectionName }}</strong>
    </div>
    <p class="muted">依据读取证据解释现状。建议不会自动执行。</p>
    <p v-if="demo || !enabled" class="notice">
      {{
        demo
          ? "演示模式：未连接模型，不生成模拟回答。"
          : "模型未配置或尚未授权外发。真实 AI 验收未执行。"
      }}
    </p>
    <form @submit.prevent="analyze">
      <fieldset class="ai-targets" :disabled="busy">
        <legend>授权分析范围（最多 10 台）</legend>
        <label v-for="h in hosts" :key="h.id"
          ><input v-model="selectedHosts" type="checkbox" :value="h.id" />{{
            h.name
          }}</label
        >
      </fieldset>
      <label
        >模式<select v-model="mode">
          <option>Suggest</option>
          <option>Observe</option>
        </select></label
      ><label
        >调查目标<textarea v-model="prompt" rows="5" maxlength="4000" /></label
      ><button
        class="primary"
        :disabled="
          demo ||
          !enabled ||
          busy ||
          !selectedHosts.length ||
          selectedHosts.length > 10
        "
      >
        <Sparkles :size="16" />{{
          busy ? "读取证据并分析…" : "开始分析"
        }}</button
      ><button v-if="busy" type="button" @click="controller.abort()">
        取消调查
      </button>
    </form>
    <p v-if="error" class="notice" role="alert">{{ error }}</p>
    <template v-if="result"
      ><h3>{{ stateLabel(result.result.state) }}</h3>
      <p v-if="result.result.error" class="notice">{{ result.result.error }}</p>
      <pre class="ai-answer">{{ result.result.text }}</pre>
      <details v-for="e in result.result.evidence" :key="e.call_id">
        <summary>{{ e.action }} · {{ e.call_id }}</summary>
        <p>{{ e.host_id }} · {{ e.at }}</p>
        <pre>{{ JSON.stringify(e.result, null, 2) }}</pre>
      </details></template
    >
  </aside>
</template>
