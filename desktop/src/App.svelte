<script lang="ts">
  import { onMount } from "svelte";
  import { api } from "./lib/api";
  import { connected, checkConnection, serverError, lastSyncAt, apiStatus } from "./lib/stores";
  import { isPro } from "./lib/license";
  import AppPaywall from "./components/AppPaywall.svelte";
  import Titlebar from "./components/Titlebar.svelte";
  import Sidebar from "./components/Sidebar.svelte";
  import CommandPalette from "./components/CommandPalette.svelte";
  import Chat from "./routes/Chat.svelte";
  import Standup from "./routes/Standup.svelte";
  import Weekly from "./routes/Weekly.svelte";
  import Timeline from "./routes/Timeline.svelte";
  import Search from "./routes/Search.svelte";
  import Log from "./routes/Log.svelte";
  import Review from "./routes/Review.svelte";
  import Settings from "./routes/Settings.svelte";

  type Tab =
    | "chat"
    | "standup"
    | "weekly"
    | "review"
    | "timeline"
    | "search"
    | "log"
    | "settings";

  const navItems: { id: Tab; label: string; icon: string; kbd?: string }[] = [
    { id: "chat", label: "Chat", icon: "message-square", kbd: "g c" },
    { id: "standup", label: "Standup", icon: "zap", kbd: "g s" },
    { id: "weekly", label: "Weekly", icon: "calendar", kbd: "g w" },
    { id: "review", label: "Review", icon: "file-text", kbd: "g r" },
    { id: "timeline", label: "Timeline", icon: "list", kbd: "g t" },
    { id: "search", label: "Search", icon: "search" },
    { id: "log", label: "Log", icon: "edit-3" },
    { id: "settings", label: "Settings", icon: "settings", kbd: "⌘ ," },
  ];

  let activeTab = $state<Tab>("chat");
  let logView = $state<{ focus: () => void } | null>(null);
  let paletteOpen = $state(false);

  function setRoute(id: string) {
    activeTab = id as Tab;
  }

  const commands = [
    { group: "Navigate", cmd: "Go to Chat", icon: "message-square", kbd: ["g", "c"], run: () => setRoute("chat") },
    { group: "Navigate", cmd: "Go to Standup", icon: "zap", kbd: ["g", "s"], run: () => setRoute("standup") },
    { group: "Navigate", cmd: "Go to Weekly", icon: "calendar", kbd: ["g", "w"], run: () => setRoute("weekly") },
    { group: "Navigate", cmd: "Go to Review", icon: "file-text", kbd: ["g", "r"], run: () => setRoute("review") },
    { group: "Navigate", cmd: "Go to Timeline", icon: "list", kbd: ["g", "t"], run: () => setRoute("timeline") },
    { group: "Navigate", cmd: "Go to Search", icon: "search", run: () => setRoute("search") },
    { group: "Navigate", cmd: "Go to Log", icon: "edit-3", run: () => setRoute("log") },
    { group: "Navigate", cmd: "Open Settings", icon: "settings", kbd: ["⌘", ","], run: () => setRoute("settings") },
    { group: "Actions", cmd: "Generate standup", icon: "zap", kbd: ["⌘", "G"], run: () => setRoute("standup") },
    { group: "Actions", cmd: "Generate weekly summary", icon: "calendar", kbd: ["⌘", "⇧", "W"], run: () => setRoute("weekly") },
    { group: "Actions", cmd: "Sync all sources", icon: "refresh-cw", kbd: ["⌘", "R"], run: () => triggerSync() },
  ];

  let syncing = $state(false);
  let syncError = $state("");
  let nowTick = $state(Date.now());

  async function triggerSync() {
    if (syncing) return;
    syncing = true;
    syncError = "";
    try {
      await api.sync();
      lastSyncAt.set(Date.now());
      await checkConnection();
    } catch (e) {
      syncError = e instanceof Error ? e.message : "Sync failed";
    } finally {
      syncing = false;
    }
  }

  const lastSyncTs = $derived.by<number>(() => {
    let max = 0;
    for (const s of $apiStatus?.sources ?? []) {
      if (!s.synced_at) continue;
      const t = Date.parse(s.synced_at);
      if (!Number.isNaN(t) && t > max) max = t;
    }
    return Math.max(max, $lastSyncAt);
  });

  const syncAgo = $derived.by<string>(() => {
    if (!lastSyncTs) return "never";
    const delta = Math.max(0, nowTick - lastSyncTs);
    const m = Math.floor(delta / 60_000);
    if (m < 1) return "just now";
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ago`;
    return `${Math.floor(h / 24)}d ago`;
  });

  const syncMeta = $derived.by(() => {
    if ($serverError) return { status: "error" as const, label: "server error" };
    if (!$connected) return { status: "warn" as const, label: "not connected" };
    if (syncing) return { status: "ok" as const, label: "Syncing…" };
    if (syncError) return { status: "error" as const, label: "sync failed" };
    return { status: "ok" as const, label: "Sync" };
  });

  const llmInfo = $derived.by(() => {
    const llm = $apiStatus?.llm;
    const onClick = () => setRoute("settings");
    const title = "Open Settings to configure";
    if (!llm || !llm.provider) {
      return {
        name: "LLM not configured",
        detail: "open Settings to set it up",
        status: "warn" as const,
        onClick,
        title,
      };
    }
    const labels: Record<string, string> = {
      ollama: "Ollama · local",
      openai: "OpenAI · BYOK",
      anthropic: "Anthropic · BYOK",
    };
    return {
      name: labels[llm.provider] ?? llm.provider,
      detail: llm.model || "(default model)",
      status: "ok" as const,
      onClick,
      title,
    };
  });

  onMount(() => {
    checkConnection();
    const interval = setInterval(checkConnection, 30_000);
    const tickInterval = setInterval(() => { nowTick = Date.now(); }, 60_000);

    let unlistenServerErr: (() => void) | undefined;
    (async () => {
      try {
        const { listen } = await import("@tauri-apps/api/event");
        unlistenServerErr = await listen<string>("server-error", (event) => {
          serverError.set(event.payload);
        });
      } catch {
        // Not running inside Tauri.
      }
    })();

    function onKeydown(e: KeyboardEvent) {
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "k") {
        e.preventDefault();
        paletteOpen = true;
        return;
      }
      if (mod && e.key === ",") {
        e.preventDefault();
        setRoute("settings");
        return;
      }
      if (e.key === "Escape" && !paletteOpen) {
        window.close();
      }
    }
    window.addEventListener("keydown", onKeydown);

    let unlisten: (() => void) | undefined;
    (async () => {
      try {
        const { listen } = await import("@tauri-apps/api/event");
        unlisten = await listen("open-log-quickadd", () => {
          activeTab = "log";
          setTimeout(() => logView?.focus(), 50);
        });
      } catch {
        // Not in Tauri.
      }
    })();

    async function openExternal(href: string) {
      try {
        const { invoke } = await import("@tauri-apps/api/core");
        await invoke("open_path", { path: href });
      } catch (err) {
        console.error("failed to open external link", href, err);
      }
    }

    function onLinkClick(e: MouseEvent) {
      if (e.defaultPrevented) return;
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const anchor = (e.target as HTMLElement | null)?.closest("a");
      if (!anchor) return;
      const href = anchor.getAttribute("href");
      if (!href) return;
      if (!/^(https?:|mailto:)/i.test(href)) return;
      e.preventDefault();
      openExternal(href);
    }
    document.addEventListener("click", onLinkClick);

    return () => {
      clearInterval(interval);
      clearInterval(tickInterval);
      window.removeEventListener("keydown", onKeydown);
      document.removeEventListener("click", onLinkClick);
      unlisten?.();
      unlistenServerErr?.();
    };
  });
</script>

<div class="window-shell">
  <Titlebar
    syncStatus={syncMeta.status}
    syncLabel={syncMeta.label}
    syncAgo={syncing ? "" : syncAgo}
    syncing={syncing}
    onSync={triggerSync}
    onCmdK={() => (paletteOpen = true)}
  />

  {#if !$connected}
    <div class="state-pane">
      <div class="state-inner">
        {#if $serverError}
          <div class="eyebrow danger">server failed to start</div>
          <h2 class="state-title">Can't reach the DevRecall server</h2>
          <pre class="error-box">{$serverError}</pre>
        {:else}
          <div class="eyebrow">starting up</div>
          <h2 class="state-title">Connecting to DevRecall…</h2>
          <p class="state-body">
            Make sure <code>devrecall serve</code> is running.
          </p>
        {/if}
      </div>
    </div>
  {:else if !$isPro}
    <AppPaywall />
  {:else}
    <div class="window-body">
      <Sidebar
        route={activeTab}
        setRoute={setRoute}
        items={navItems}
        llm={llmInfo}
      />

      <div class="content">
        <div class="route" class:hidden={activeTab !== "chat"}><Chat /></div>
        <div class="route" class:hidden={activeTab !== "standup"}><Standup /></div>
        <div class="route" class:hidden={activeTab !== "weekly"}><Weekly /></div>
        <div class="route" class:hidden={activeTab !== "timeline"}><Timeline /></div>
        <div class="route" class:hidden={activeTab !== "review"}><Review /></div>
        <div class="route" class:hidden={activeTab !== "search"}><Search /></div>
        <div class="route" class:hidden={activeTab !== "log"}><Log bind:this={logView} /></div>
        <div class="route" class:hidden={activeTab !== "settings"}><Settings /></div>
      </div>
    </div>
  {/if}

  <CommandPalette open={paletteOpen} {commands} onClose={() => (paletteOpen = false)} />
</div>

<style>
  .window-shell {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    background: var(--ink-0);
    overflow: hidden;
    color: var(--fg-1);
  }
  .window-body {
    flex: 1;
    display: flex;
    overflow: hidden;
    position: relative;
  }
  .content {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    background: var(--bg-canvas);
    position: relative;
  }
  .route {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .route.hidden { display: none; }
  /* Titlebar already reserves space; content fills the rest. */
  .content > .route:first-child { position: absolute; inset: 0; }

  .state-pane {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 32px;
    background: var(--bg-canvas);
  }
  .state-inner {
    max-width: 440px;
    text-align: center;
  }
  .eyebrow {
    font-family: var(--font-mono);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: var(--tracking-caps);
    color: var(--fg-3);
    font-weight: 500;
    margin-bottom: 8px;
  }
  .eyebrow.danger { color: var(--danger); }
  .state-title {
    margin: 0 0 12px;
    font-size: 20px;
    font-weight: 600;
    color: var(--fg-1);
    letter-spacing: -0.012em;
  }
  .state-body {
    font-size: 13px;
    color: var(--fg-3);
    margin: 0;
  }
  .state-body code {
    font-family: var(--font-mono);
    font-size: 12px;
    padding: 2px 6px;
    border-radius: var(--r-1);
    background: var(--ink-3);
    color: var(--fg-1);
  }
  .error-box {
    margin: 0;
    text-align: left;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--danger);
    background: var(--danger-wash);
    border: 1px solid rgba(255, 107, 107, 0.2);
    border-radius: var(--r-2);
    padding: 10px 12px;
    white-space: pre-wrap;
    max-height: 220px;
    overflow: auto;
  }
</style>
