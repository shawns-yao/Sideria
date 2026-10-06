<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
const props = defineProps<{ hostID: string; hostName: string }>();
const element = ref<HTMLDivElement>(),
  state = ref("连接中"),
  identity = ref("");
let term: Terminal, ws: WebSocket, observer: ResizeObserver;
onMounted(() => {
  term = new Terminal({
    theme: { background: "#0b0d12", foreground: "#dce3ef", cursor: "#a5bcff" },
    fontSize: 13,
    fontFamily: "monospace",
    scrollback: 2000,
    convertEol: false,
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(element.value!);
  fit.fit();
  ws = new WebSocket(
    `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/hosts/${props.hostID}/terminal?confirm=shell`,
  );
  const send = (type: string, data: unknown) => {
    if (ws.readyState === WebSocket.OPEN)
      ws.send(JSON.stringify({ type, data }));
  };
  ws.onopen = () => {
    state.value = "已连接";
    send("terminal_resize", { cols: term.cols, rows: term.rows });
    term.focus();
  };
  ws.onmessage = (event) => {
    const m = JSON.parse(event.data);
    if (m.type === "terminal_closed") {
      state.value = m.error || "会话已结束";
      ws.close();
    }
    if (m.data?.bytes)
      term.write(Uint8Array.from(atob(m.data.bytes), (c) => c.charCodeAt(0)));
    if (m.data?.shell) identity.value = `${m.data.user} · ${m.data.shell}`;
  };
  ws.onclose = () => {
    state.value = "会话已结束 · 不自动重放命令";
  };
  term.onData((data) => {
    const bytes = new TextEncoder().encode(data);
    send("terminal_input", { bytes: btoa(String.fromCharCode(...bytes)) });
  });
  term.onResize(({ cols, rows }) => send("terminal_resize", { cols, rows }));
  observer = new ResizeObserver(() => {
    if (element.value?.clientWidth) fit.fit();
  });
  observer.observe(element.value!);
});
onBeforeUnmount(() => {
  observer?.disconnect();
  ws?.close();
  term?.dispose();
});
</script>
<template>
  <div class="terminal-session">
    <div class="terminal-identity">
      {{ hostName }} · Agent PTY · {{ identity }} <span>{{ state }}</span>
    </div>
    <div ref="element" class="terminal-canvas" />
  </div>
</template>
