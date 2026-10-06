<script setup lang="ts">
import { computed } from "vue";
import { linePath, type Point, bytes } from "../domain/model";
const props = defineProps<{
  series: Point[];
  secondary?: Point[];
  unit?: string;
  max?: number;
}>();
const maximum = computed(
  () =>
    props.max ??
    Math.max(
      1,
      ...props.series.map((p) => p.value ?? 0),
      ...(props.secondary ?? []).map((p) => p.value ?? 0),
    ),
);
const first = computed(() => linePath(props.series, 440, 90, maximum.value));
const second = computed(() =>
  linePath(props.secondary ?? [], 440, 90, maximum.value),
);
</script>
<template>
  <div class="trend">
    <svg
      viewBox="0 0 440 100"
      role="img"
      aria-label="实际采样趋势，缺失区间留空"
      preserveAspectRatio="none"
    >
      <path class="gridline" d="M0 10H440 M0 50H440 M0 90H440" />
      <path :d="first" class="line" />
      <path :d="second" class="line secondary" />
    </svg>
    <p v-if="series.length < 2" class="muted">等待至少两个采样点</p>
    <details v-else class="samples">
      <summary>查看采样值 · {{ series.length }} 点</summary>
      <div class="sample-list">
        <p v-for="(p, i) in series" :key="p.at">
          {{ new Date(p.at).toLocaleTimeString() }} ·
          {{
            unit === "bytes"
              ? bytes(p.value) + "/s"
              : (p.value?.toFixed(1) ?? "缺失")
          }}
          <span v-if="secondary"> / {{ bytes(secondary[i]?.value) }}/s</span>
        </p>
      </div>
    </details>
  </div>
</template>
