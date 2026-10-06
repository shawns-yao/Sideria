<script setup lang="ts">
import {
  computed,
  ref,
  onMounted,
  onBeforeUnmount,
  defineAsyncComponent,
} from "vue";
import {
  Orbit,
  Server,
  LayoutDashboard,
  GitBranch,
  ListChecks,
  ShieldCheck,
  Settings,
  Sparkles,
  TerminalSquare,
  Folder,
  Menu,
  X,
  ChevronRight,
  Plus,
  LogOut,
  Radio,
} from "@lucide/vue";
import { z } from "zod";
import { api, APIError } from "./domain/api";
import {
  hostSchema,
  bytes,
  memory,
  percent,
  stale,
  type Host,
} from "./domain/model";
import { demoHosts } from "./demo/hosts";
import HostDashboard from "./components/HostDashboard.vue";
import FilePanel from "./components/FilePanel.vue";
import DockerPanel from "./components/DockerPanel.vue";
import ProjectPanel from "./components/ProjectPanel.vue";
import TaskPanel from "./components/TaskPanel.vue";
import AIPanel from "./components/AIPanel.vue";
const TerminalPanel = defineAsyncComponent(
  () => import("./components/TerminalPanel.vue"),
);
const demo = new URLSearchParams(location.search).get("demo") === "1";
const signedIn = ref(demo),
  token = ref(""),
  error = ref(""),
  hosts = ref<Host[]>(demo ? demoHosts : []),
  settingsHosts = ref<Host[]>(demo ? demoHosts : []),
  selected = ref(demo ? demoHosts[0]!.id : ""),
  page = ref("host"),
  tab = ref("概览"),
  aiOpen = ref(false),
  mobileNav = ref(false),
  aiEnabled = ref(false),
  newName = ref(""),
  enrollment = ref<any>(null),
  audit = ref<any[]>([]);
const sessions = ref<{ id: string; hostID: string; hostName: string }[]>([]),
  activeSession = ref(""),
  terminalOpen = ref(false),
  fullscreen = ref(false);
const host = computed(() => hosts.value.find((h) => h.id === selected.value));
const nav = [
  { id: "overview", name: "总览", icon: LayoutDashboard },
  { id: "host", name: "服务器", icon: Server },
  { id: "projects", name: "项目", icon: GitBranch },
  { id: "tasks", name: "任务", icon: ListChecks },
  { id: "audit", name: "审计", icon: ShieldCheck },
  { id: "settings", name: "设置", icon: Settings },
];
let interval: ReturnType<typeof setInterval>,
  refreshing = false,
  disposed = false;
async function refresh() {
  if (demo || !signedIn.value || refreshing) return;
  refreshing = true;
  const target = selected.value;
  try {
    const parsed = z
      .array(hostSchema)
      .parse(
        await api(
          `/api/hosts?history=${encodeURIComponent(target)}&include_revoked=${page.value === "settings" ? "1" : "0"}`,
        ),
      );
    if (disposed) return;
    settingsHosts.value = parsed;
    hosts.value = parsed.filter((h) => !h.revoked);
    if (!selected.value && hosts.value.length)
      selected.value = hosts.value[0]!.id;
  } catch (e) {
    if (e instanceof APIError && e.status === 401) signedIn.value = false;
    else error.value = (e as Error).message;
  } finally {
    refreshing = false;
  }
}
async function initialize() {
  try {
    const config = await api("/api/config");
    if (disposed) return;
    signedIn.value = true;
    aiEnabled.value = config.ai_enabled;
    await refresh();
  } catch (e) {
    if (!(e instanceof APIError && e.status === 401))
      error.value = (e as Error).message;
  }
}
onMounted(() => {
  if (!demo) {
    initialize();
    interval = setInterval(refresh, 5000);
  }
});
onBeforeUnmount(() => {
  disposed = true;
  clearInterval(interval);
});
async function login() {
  error.value = "";
  try {
    await api("/api/login", {
      method: "POST",
      body: JSON.stringify({ token: token.value }),
    });
    token.value = "";
    await initialize();
  } catch (e) {
    error.value = (e as Error).message;
  }
}
function choose(id: string) {
  selected.value = id;
  page.value = "host";
  mobileNav.value = false;
  error.value = "";
  refresh();
}
async function navigate(id: string) {
  page.value = id;
  mobileNav.value = false;
  if (id === "settings") await refresh();
  if (id === "audit" && !demo) {
    try {
      audit.value = await api("/api/audit");
    } catch (e) {
      error.value = (e as Error).message;
    }
  }
}
async function addHost() {
  if (demo) return;
  try {
    enrollment.value = await api("/api/hosts", {
      method: "POST",
      body: JSON.stringify({ name: newName.value }),
    });
    newName.value = "";
    await refresh();
  } catch (e) {
    error.value = (e as Error).message;
  }
}
async function reenroll(id: string, name: string) {
  if (
    !confirm(
      `重新绑定 ${name}？旧凭据立即失效，旧任务停止重传并保留待确认事实。请仅在明确重装/身份恢复时使用。`,
    )
  )
    return;
  try {
    enrollment.value = await api(`/api/hosts/${id}/reenroll`, {
      method: "POST",
      body: JSON.stringify({ confirm: true }),
    });
    await refresh();
  } catch (e) {
    error.value = (e as Error).message;
  }
}
async function revoke(id: string, name: string) {
  if (
    !confirm(
      `撤销 ${name} 的 Agent 身份？将断开连接并阻止后续操作；已发生副作用不会回滚。`,
    )
  )
    return;
  try {
    await api(`/api/hosts/${id}/revoke`, { method: "POST", body: "{}" });
    if (selected.value === id) selected.value = "";
    await refresh();
  } catch (e) {
    error.value = (e as Error).message;
  }
}
function openTerminal() {
  if (!host.value || demo) {
    terminalOpen.value = true;
    return;
  }
  if (
    !confirm(
      `打开 ${host.value.name} 的交互 Shell？这允许以 Agent 用户执行任意命令；内容不会提供给 AI。`,
    )
  )
    return;
  const id = crypto.randomUUID();
  sessions.value.push({ id, hostID: host.value.id, hostName: host.value.name });
  activeSession.value = id;
  terminalOpen.value = true;
}
function closeSession(id: string) {
  sessions.value = sessions.value.filter((s) => s.id !== id);
  activeSession.value = sessions.value[0]?.id ?? "";
  if (!sessions.value.length) terminalOpen.value = false;
}
async function logout() {
  await api("/api/logout", { method: "POST", body: "{}" });
  sessions.value = [];
  terminalOpen.value = false;
  hosts.value = [];
  signedIn.value = false;
}
</script>
<template>
  <div v-if="!signedIn" class="login-screen">
    <form class="login-card" @submit.prevent="login">
      <Orbit :size="42" />
      <p class="eyebrow">SIDERIA / 巡星</p>
      <h1>接入你的工作台</h1>
      <p class="muted">单一视野，观察多台服务器。</p>
      <label
        >管理员访问令牌<input
          v-model="token"
          type="password"
          autocomplete="current-password"
          required
          minlength="32"
      /></label>
      <p v-if="error" class="notice" role="alert">{{ error }}</p>
      <button class="primary">登录工作台</button
      ><a href="?demo=1">查看明确标记的 UI 演示</a>
    </form>
  </div>
  <div v-else class="app-shell" :class="{ 'has-ai': aiOpen }">
    <a class="skip-link" href="#main">跳到主要内容</a>
    <div v-if="mobileNav" class="nav-backdrop" @click="mobileNav = false" />
    <aside class="sidebar" :class="{ open: mobileNav }">
      <a class="brand" href="/" @click.prevent="navigate('host')"
        ><Orbit :size="29" /><b>Sideria</b><span>巡星</span></a
      >
      <p class="sidebar-caption">YOUR OPERATIONS OBSERVATORY</p>
      <nav aria-label="主导航">
        <button
          v-for="item in nav"
          :key="item.id"
          :class="{ active: page === item.id }"
          @click="navigate(item.id)"
        >
          <component :is="item.icon" :size="18" />{{ item.name
          }}<ChevronRight v-if="page === item.id" :size="14" />
        </button>
      </nav>
      <div class="sidebar-hosts">
        <p class="eyebrow">
          HOSTS <span>{{ hosts.length }}</span>
        </p>
        <button
          v-for="h in hosts"
          :key="h.id"
          :class="{ active: selected === h.id }"
          @click="choose(h.id)"
        >
          <span class="status-dot" :class="{ online: h.online }" /><span>{{
            h.name
          }}</span>
        </button>
      </div>
      <div class="sidebar-bottom">
        <div class="mini-orbit" />
        <p>ACROSS SERVERS<br />BEYOND BOUNDARIES</p>
        <span class="muted">单 Workspace · v0.1</span>
      </div>
    </aside>
    <div class="main-shell">
      <header class="topbar">
        <button
          class="mobile-only"
          aria-label="打开导航"
          @click="mobileNav = true"
        >
          <Menu :size="20" /></button
        ><span class="breadcrumb"
          >工作台 <ChevronRight :size="13" />
          {{ nav.find((n) => n.id === page)?.name }}</span
        ><span v-if="demo" class="demo-badge">UI 演示 · 固定样本</span>
        <div class="top-actions">
          <button @click="aiOpen = !aiOpen">
            <Sparkles :size="17" /><span>AI 分析</span></button
          ><button v-if="!demo" aria-label="退出登录" @click="logout">
            <LogOut :size="17" /></button
          ><a v-else class="button" href="/">真实工作台</a
          ><span class="avatar">S</span>
        </div>
      </header>
      <main id="main" tabindex="-1">
        <p v-if="error" class="notice" role="alert">{{ error }}</p>
        <template v-if="page === 'host' || page === 'projects'"
          ><template v-if="host"
            ><div class="host-heading">
              <div>
                <p class="eyebrow">HOST / {{ selected.slice(0, 12) }}</p>
                <div class="host-title">
                  <div class="host-icon"><Server :size="25" /></div>
                  <h1>{{ host.name }}</h1>
                  <span class="connection" :class="{ online: host.online }"
                    ><span
                      class="status-dot"
                      :class="{ online: host.online }"
                    />{{
                      host.online
                        ? "在线"
                        : host.snapshot
                          ? "Agent 失联"
                          : "等待接入"
                    }}</span
                  >
                </div>
                <p class="host-metadata">
                  <span>{{ host.snapshot?.os || "系统信息未知" }}</span
                  ><span
                    >{{ host.snapshot?.cores ?? "—" }} 核 /
                    {{ bytes(host.snapshot?.memory_total) }}</span
                  ><span
                    >运行
                    {{
                      host.snapshot
                        ? (host.snapshot.uptime / 86400).toFixed(1) + " 天"
                        : "—"
                    }}</span
                  ><span>Agent 协议 v1</span>
                </p>
              </div>
              <div class="host-actions">
                <button @click="tab = '文件'"><Folder :size="16" />文件</button
                ><button @click="openTerminal">
                  <TerminalSquare :size="16" />终端</button
                ><button class="primary" @click="aiOpen = true">
                  <Sparkles :size="16" />分析状态
                </button>
              </div>
            </div>
            <p
              v-if="host.snapshot && (!host.online || (!demo && stale(host)))"
              class="notice"
            >
              数据已过期，下方数值是上次采样。Agent
              失联或采集延迟不代表服务器关机。
            </p>
            <div class="tabs" role="tablist" aria-label="主机功能">
              <button
                v-for="t in ['概览', '文件', 'Docker', '项目与 Git']"
                :key="t"
                role="tab"
                :aria-selected="tab === t"
                :class="{ active: tab === t }"
                @click="
                  tab = t;
                  page = 'host';
                "
              >
                {{ t }}</button
              ><span class="muted">{{
                demo ? "DEMO DATA" : "LIVE AGENT"
              }}</span>
            </div>
            <HostDashboard
              v-if="page === 'host' && tab === '概览'"
              :host="host"
              :demo="demo"
            />
            <div v-else-if="demo" class="empty workspace-panel">
              <Radio :size="28" />
              <h2>此功能未接入演示</h2>
              <p>演示不发起远程请求，不模拟操作成功。</p>
            </div>
            <FilePanel
              v-else-if="tab === '文件' && page !== 'projects'"
              :host="host"
            /><DockerPanel
              v-else-if="tab === 'Docker' && page !== 'projects'"
              :host="host"
            /><ProjectPanel v-else :host="host" />
          </template>
          <div v-else class="empty workspace-panel">
            <Server :size="36" />
            <h1>从第一台服务器开始</h1>
            <p>在设置中创建一次性接入令牌，再启动 Linux Agent。</p>
            <button class="primary" @click="navigate('settings')">
              添加服务器
            </button>
          </div></template
        >
        <section v-else-if="page === 'overview'" class="workspace-panel">
          <div class="section-heading">
            <div>
              <p class="eyebrow">CONSTELLATION / WORKSPACE</p>
              <h1>服务器总览</h1>
              <p>节点表示工作台归属，连线不表示业务流量。</p>
            </div>
            <span class="badge"
              >{{ hosts.filter((h) => h.online).length }} /
              {{ hosts.length }} 在线</span
            >
          </div>
          <div class="constellation">
            <div class="central-node"><Orbit :size="32" /><b>Sideria</b></div>
            <div class="host-nodes">
              <button v-for="h in hosts" :key="h.id" @click="choose(h.id)">
                <span class="node" :class="{ online: h.online }"
                  ><Server :size="21" /></span
                ><b>{{ h.name }}</b
                ><small>{{ h.online ? "在线" : "未知 / 失联" }}</small>
              </button>
            </div>
          </div>
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>服务器</th>
                  <th>连接</th>
                  <th>CPU</th>
                  <th>内存</th>
                  <th>系统</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="h in hosts" :key="h.id">
                  <td>
                    <button class="text-button" @click="choose(h.id)">
                      {{ h.name }}
                    </button>
                  </td>
                  <td>{{ h.online ? "在线" : "失联 / 未接入" }}</td>
                  <td>{{ percent(h.snapshot?.cpu) }}%</td>
                  <td>{{ percent(memory(h.snapshot)) }}%</td>
                  <td>{{ h.snapshot?.os ?? "—" }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <TaskPanel v-else-if="page === 'tasks' && !demo" />
        <section v-else-if="page === 'audit'" class="workspace-panel">
          <div class="section-heading">
            <div>
              <h1>审计时间线</h1>
              <p>入口、目标、Action 与执行结果</p>
            </div>
            <button v-if="!demo" @click="navigate('audit')">刷新</button>
          </div>
          <p v-if="!audit.length" class="empty">
            {{ demo ? "演示模式没有真实审计记录" : "暂无审计记录" }}
          </p>
          <article
            v-for="e in audit"
            :key="e.request_id + e.at"
            class="audit-row"
          >
            <span class="status-dot" />
            <div>
              <b>{{ e.action }}</b>
              <p class="mono muted">
                {{ e.host_id.slice(0, 12) }} · {{ e.source }} ·
                {{ e.request_id.slice(0, 12) }}
              </p>
            </div>
            <span>{{ e.state }}</span
            ><time>{{ new Date(e.at).toLocaleString() }}</time>
          </article>
        </section>
        <section v-else-if="page === 'settings'" class="workspace-panel">
          <div class="section-heading">
            <div>
              <h1>设置与 Agent 接入</h1>
              <p>每台主机使用独立身份，注册令牌有效期十分钟。</p>
            </div>
          </div>
          <form class="toolbar" @submit.prevent="addHost">
            <input
              v-model="newName"
              aria-label="新主机名称"
              placeholder="主机名称"
              maxlength="120"
              required
            /><button class="primary" :disabled="demo">
              <Plus :size="16" />创建接入令牌
            </button>
          </form>
          <div v-if="enrollment" class="notice">
            <p>
              一次性令牌仅在此显示，请在可信目标的安装环境注入。不要放入 URL
              或共享日志。
            </p>
            <code class="secret-display">{{ enrollment.enrollment_token }}</code
            ><button @click="enrollment = null">隐藏令牌</button>
          </div>
          <article v-for="h in settingsHosts" :key="h.id" class="settings-host">
            <div>
              <h3>{{ h.name }}</h3>
              <p class="mono muted">{{ h.id }}</p>
              <p v-if="h.revoked" class="notice">
                身份已撤销。核对 Agent journal 和目标状态后才可重新绑定。
              </p>
              <p>
                {{
                  Object.entries(h.capabilities)
                    .map(([k, v]) => `${k}: ${v}`)
                    .join(" · ") || "等待 Agent 报告能力"
                }}
              </p>
            </div>
            <button :disabled="demo" @click="reenroll(h.id, h.name)">
              重新绑定</button
            ><button
              :disabled="demo || h.revoked"
              @click="revoke(h.id, h.name)"
            >
              撤销身份
            </button>
          </article>
          <h2>模型配置</h2>
          <p class="notice">
            {{
              aiEnabled
                ? "中心已启用模型和外发许可。"
                : "未配置真实模型；AI 验收未执行。"
            }}
            API Key 仅在中心环境配置，浏览器不接收模型凭据。
          </p>
          <h2>当前边界</h2>
          <p class="muted">
            单用户 / 单 Workspace。Kubernetes、AI 写操作、Git
            写操作、Compose、告警与配置快照尚未开放。
          </p>
        </section>
        <section v-else class="empty workspace-panel">
          <h2>演示没有真实任务</h2>
          <p>登录并接入 Agent 后查看实际执行事实。</p>
        </section>
      </main>
      <footer class="app-footer">
        <span>SIDERIA · 巡星</span
        ><span>{{ demo ? "UI TEST FIXTURE" : "OBSERVE WITH EVIDENCE" }}</span>
      </footer>
    </div>
    <AIPanel
      v-if="aiOpen"
      :host-i-d="host?.id ?? ''"
      :host-name="host?.name ?? '尚未选择目标'"
      :demo="demo"
      :enabled="aiEnabled"
      :hosts="hosts"
      @close="aiOpen = false"
    />
    <div v-show="terminalOpen" class="terminal-dock" :class="{ fullscreen }">
      <header>
        <div class="terminal-tabs">
          <button
            v-for="s in sessions"
            :key="s.id"
            :class="{ active: activeSession === s.id }"
            @click="activeSession = s.id"
          >
            <TerminalSquare :size="15" />{{ s.hostName
            }}<span
              role="button"
              tabindex="0"
              :aria-label="`结束 ${s.hostName} 会话`"
              @click.stop="closeSession(s.id)"
              @keydown.enter.stop="closeSession(s.id)"
              ><X :size="14"
            /></span>
          </button>
        </div>
        <div class="toolbar">
          <button @click="openTerminal">新会话</button
          ><button @click="fullscreen = !fullscreen">
            {{ fullscreen ? "还原" : "全屏" }}</button
          ><button @click="terminalOpen = false">收起</button>
        </div>
      </header>
      <p v-if="demo" class="empty">演示未连接 PTY，不模拟命令成功。</p>
      <TerminalPanel
        v-for="s in sessions"
        v-show="activeSession === s.id"
        :key="s.id"
        :host-i-d="s.hostID"
        :host-name="s.hostName"
      />
    </div>
    <button
      v-if="sessions.length && !terminalOpen"
      class="terminal-reopen"
      @click="terminalOpen = true"
    >
      <TerminalSquare :size="17" />{{ sessions.length }} 个会话
    </button>
  </div>
</template>
