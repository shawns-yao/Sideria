<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  Cpu,
  MemoryStick,
  HardDrive,
  Activity,
  ArrowDown,
  ArrowUp,
  Radio,
} from "@lucide/vue";
import {
  bytes,
  memory,
  percent,
  time,
  samplingGap,
  type Host,
} from "../domain/model";
import TrendChart from "./TrendChart.vue";
const props = defineProps<{ host: Host; demo: boolean }>();
const mount = ref(""),
  network = ref("");
watch(
  () => props.host.id,
  () => {
    mount.value = "";
    network.value = "";
  },
);
const snapshot = computed(() => props.host.snapshot);
const disk = computed(
  () =>
    snapshot.value?.disks?.find((d) => d.mount === mount.value) ??
    snapshot.value?.disks?.[0],
);
const net = computed(
  () =>
    snapshot.value?.networks?.find((n) => n.name === network.value) ??
    snapshot.value?.networks?.find((n) => n.name !== "lo") ??
    snapshot.value?.networks?.[0],
);
const history = computed(() => props.host.history ?? []);
const cpuSeries = computed(() =>
  history.value.map((s) => ({ at: Date.parse(s.sampled_at), value: s.cpu })),
);
const memSeries = computed(() =>
  history.value.map((s) => ({
    at: Date.parse(s.sampled_at),
    value: memory(s),
  })),
);
const rx = computed(() =>
  history.value.map((s) => ({
    at: Date.parse(s.sampled_at),
    value: s.networks?.find((n) => n.name === net.value?.name)?.rx ?? null,
  })),
);
const tx = computed(() =>
  history.value.map((s) => ({
    at: Date.parse(s.sampled_at),
    value: s.networks?.find((n) => n.name === net.value?.name)?.tx ?? null,
  })),
);
const diskPercent = computed(() =>
  disk.value?.total ? (100 * disk.value.used) / disk.value.total : null,
);
</script>
<template>
  <div class="instrument-grid">
    <section class="instrument cpu">
      <div class="instrument-title">
        <Cpu :size="18" />
        <h2>CPU</h2>
        <span>处理器</span>
      </div>
      <div class="cpu-content">
        <div class="gauge">
          <svg viewBox="0 0 220 175" aria-hidden="true">
            <path
              class="gauge-track"
              d="M40 150 A90 90 0 1 1 180 150"
              pathLength="100"
            />
            <path
              class="gauge-value"
              d="M40 150 A90 90 0 1 1 180 150"
              pathLength="100"
              :stroke-dasharray="`${snapshot?.cpu ?? 0} 100`"
            />
            <path class="gauge-inner" d="M52 140 A74 74 0 1 1 168 140" />
          </svg>
          <div class="gauge-number">
            <strong>{{ percent(snapshot?.cpu) }}</strong
            ><small>%</small>
            <p>{{ snapshot?.cores ?? "—" }} 逻辑核心</p>
          </div>
        </div>
        <div class="cpu-trend">
          <p class="eyebrow">实际短窗口</p>
          <TrendChart
            :gap-ms="samplingGap(snapshot)"
            :series="cpuSeries"
            :max="100"
          />
          <dl class="load-values">
            <div
              v-for="(label, i) in ['1 min', '5 min', '15 min']"
              :key="label"
            >
              <dt>Load · {{ label }}</dt>
              <dd>{{ snapshot?.load?.[i]?.toFixed(2) ?? "—" }}</dd>
            </div>
          </dl>
        </div>
      </div>
    </section>
    <section class="instrument memory">
      <div class="instrument-title">
        <MemoryStick :size="18" />
        <h2>Memory</h2>
        <span>内存</span>
      </div>
      <div class="big-readout">
        <strong>{{ percent(memory(snapshot)) }}</strong
        ><span>%</span>
        <p>
          {{
            snapshot?.memory_valid
              ? bytes(snapshot.memory_total - snapshot.memory_available)
              : "—"
          }}
          <small>/ {{ bytes(snapshot?.memory_total) }}</small>
        </p>
      </div>
      <div
        class="memory-scale"
        role="meter"
        :aria-valuenow="memory(snapshot) ?? undefined"
        aria-label="内存占用"
        aria-valuemin="0"
        aria-valuemax="100"
      >
        <div :style="{ width: `${memory(snapshot) ?? 0}%` }" />
      </div>
      <div class="spread muted">
        <span>已用 = Total − Available</span
        ><span
          >可用
          {{
            snapshot?.memory_valid ? bytes(snapshot.memory_available) : "—"
          }}</span
        >
      </div>
      <TrendChart
        :gap-ms="samplingGap(snapshot)"
        :series="memSeries"
        :max="100"
      />
    </section>
    <section class="instrument disk">
      <div class="instrument-title">
        <HardDrive :size="18" />
        <h2>Disk</h2>
      </div>
      <label class="select-label"
        >挂载点<select v-model="mount" aria-label="挂载点">
          <option value="">
            {{ snapshot?.disks?.[0]?.mount ?? "无数据" }}
          </option>
          <option
            v-for="d in snapshot?.disks?.slice(1)"
            :key="d.mount"
            :value="d.mount"
          >
            {{ d.mount }}
          </option>
        </select></label
      >
      <div class="disk-orbit">
        <svg viewBox="0 0 230 150" aria-hidden="true">
          <ellipse class="orbit-track" cx="115" cy="75" rx="99" ry="55" />
          <ellipse
            class="orbit-value"
            cx="115"
            cy="75"
            rx="99"
            ry="55"
            pathLength="100"
            :stroke-dasharray="`${diskPercent ?? 0} 100`"
          /></svg
        ><strong>{{ percent(diskPercent) }}<small>%</small></strong>
      </div>
      <p class="center">
        {{ bytes(disk?.used) }}
        <span class="muted">/ {{ bytes(disk?.total) }}</span>
      </p>
      <p class="muted center">
        inode {{ disk?.inodes_used?.toLocaleString() ?? "—" }} /
        {{ disk?.inodes_total?.toLocaleString() ?? "—" }}
      </p>
    </section>
    <section class="instrument network">
      <div class="instrument-title">
        <Activity :size="18" />
        <h2>Network</h2>
      </div>
      <label class="select-label"
        >网卡<select v-model="network" aria-label="网卡">
          <option value="">
            {{
              snapshot?.networks?.find((n) => n.name !== "lo")?.name ??
              snapshot?.networks?.[0]?.name ??
              "无数据"
            }}
          </option>
          <option v-for="n in snapshot?.networks" :key="n.name" :value="n.name">
            {{ n.name }}
          </option>
        </select></label
      >
      <div class="network-rates">
        <div>
          <ArrowDown :size="16" /><span>接收</span
          ><strong>{{ bytes(net?.rx) }}<small>/s</small></strong>
        </div>
        <div>
          <ArrowUp :size="16" /><span>发送</span
          ><strong>{{ bytes(net?.tx) }}<small>/s</small></strong>
        </div>
      </div>
      <TrendChart
        :gap-ms="samplingGap(snapshot)"
        :series="rx"
        :secondary="tx"
        unit="bytes"
      />
      <p class="muted">单网卡统计，不累加虚拟接口 · 实线接收 / 虚线发送</p>
    </section>
    <section class="instrument system">
      <div class="instrument-title">
        <Radio :size="18" />
        <h2>System</h2>
      </div>
      <p class="eyebrow">能力与新鲜度</p>
      <dl>
        <div v-for="(value, key) in host.capabilities" :key="key">
          <dt>{{ key }}</dt>
          <dd :title="value">
            {{ value === "available" ? "可用" : "未启用" }}
          </dd>
        </div>
      </dl>
      <div class="system-time">
        <p>
          采集 <b>{{ time(snapshot?.sampled_at) }}</b>
        </p>
        <p>
          接收 <b>{{ time(snapshot?.received_at) }}</b>
        </p>
      </div>
      <p class="muted">连接状态不代表应用健康</p>
    </section>
  </div>
  <div class="observation-footer">
    <span class="status-dot" :class="{ online: host.online }" /><span>{{
      demo
        ? "UI 演示样本 · 不连接远程服务"
        : "真实 Agent 数据 · 不足窗口按实际显示"
    }}</span
    ><span v-if="history.length"
      >窗口 {{ time(history[0]?.sampled_at) }} —
      {{ time(history[history.length - 1]?.sampled_at) }}</span
    >
  </div>
  <p v-if="snapshot?.errors?.length" class="notice">
    采集缺口：{{ snapshot.errors.join("；") }}
  </p>
</template>
