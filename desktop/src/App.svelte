<script lang="ts">
  import { onMount } from "svelte";
  import { api, type SyncStatus } from "./lib/api";
  import { connected, checkConnection, serverError, lastSyncAt, apiStatus, nowTick, llmHealth, checkLLMHealth } from "./lib/stores";
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
    { id: "standup", label: "Daily", icon: "zap", kbd: "g s" },
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
    { group: "Navigate", cmd: "Go to Daily", icon: "zap", kbd: ["g", "s"], run: () => setRoute("standup") },
    { group: "Navigate", cmd: "Go to Weekly", icon: "calendar", kbd: ["g", "w"], run: () => setRoute("weekly") },
    { group: "Navigate", cmd: "Go to Review", icon: "file-text", kbd: ["g", "r"], run: () => setRoute("review") },
    { group: "Navigate", cmd: "Go to Timeline", icon: "list", kbd: ["g", "t"], run: () => setRoute("timeline") },
    { group: "Navigate", cmd: "Go to Search", icon: "search", run: () => setRoute("search") },
    { group: "Navigate", cmd: "Go to Log", icon: "edit-3", run: () => setRoute("log") },
    { group: "Navigate", cmd: "Open Settings", icon: "settings", kbd: ["⌘", ","], run: () => setRoute("settings") },
    { group: "Actions", cmd: "Generate daily recap", icon: "zap", kbd: ["⌘", "G"], run: () => setRoute("standup") },
    { group: "Actions", cmd: "Generate weekly summary", icon: "calendar", kbd: ["⌘", "⇧", "W"], run: () => setRoute("weekly") },
    { group: "Actions", cmd: "Sync all sources", icon: "refresh-cw", kbd: ["⌘", "R"], run: () => triggerSync() },
  ];

  let syncing = $state(false);
  let syncError = $state("");
  // Per-source progress for the in-flight sync, keyed by source name. The
  // server emits multiple `freshness` frames per source (syncing → synced)
  // so each entry shows the latest status — used to drive the tooltip and
  // the "Syncing X…" label.
  let syncProgress = $state<Record<string, { status: SyncStatus; added?: number; error?: string }>>({});
  // Most recent source the server reported `syncing` on. When that source
  // transitions to a terminal state we clear it so the label can pick up
  // whichever syncer started after it.
  let activeSyncSource = $state<string | null>(null);

  const sourceLabels: Record<string, string> = {
    git: "Git",
    slack: "Slack",
    calendar: "Calendar",
    github: "GitHub",
    gitlab: "GitLab",
    bitbucket: "Bitbucket",
    jira: "Jira",
    linear: "Linear",
    confluence: "Confluence",
  };
  const sourceLabel = (src: string) => sourceLabels[src] ?? src;

  async function triggerSync() {
    if (syncing) return;
    syncing = true;
    syncError = "";
    syncProgress = {};
    activeSyncSource = null;
    try {
      await api.syncStream((ev) => {
        if (ev.type !== "freshness") return;
        syncProgress = {
          ...syncProgress,
          [ev.source]: { status: ev.status, added: ev.added, error: ev.error },
        };
        if (ev.status === "syncing") {
          activeSyncSource = ev.source;
        } else if (activeSyncSource === ev.source) {
          // The source we were displaying just finished — fall back to
          // any other still-in-flight source so the label keeps moving.
          const stillSyncing = Object.entries(syncProgress).find(
            ([, v]) => v.status === "syncing"
          );
          activeSyncSource = stillSyncing ? stillSyncing[0] : null;
        }
      });
      lastSyncAt.set(Date.now());
      await checkConnection();
    } catch (e) {
      // EventSource fallback: if streaming isn't reachable (e.g. older
      // server), fall back to the buffered POST /api/sync so the button
      // still works against pre-streaming binaries.
      try {
        await api.sync();
        lastSyncAt.set(Date.now());
        await checkConnection();
      } catch {
        syncError = e instanceof Error ? e.message : "Sync failed";
      }
    } finally {
      syncing = false;
      activeSyncSource = null;
    }
  }

  // Tooltip body shown on the sync button. While syncing, lists every
  // source's lifecycle status with a leading glyph. Idle, falls back to
  // "last synced X ago" so users still see freshness at a glance.
  const syncTooltip = $derived.by<string>(() => {
    const entries = Object.entries(syncProgress);
    if (entries.length > 0) {
      const lines = entries.map(([src, p]) => {
        const name = sourceLabel(src);
        switch (p.status) {
          case "syncing":
            return `… ${name}`;
          case "synced":
            return `✓ ${name}${p.added ? ` (${p.added} new)` : ""}`;
          case "fresh":
            return `· ${name} (fresh)`;
          case "error":
            return `⚠ ${name} — ${p.error ?? "failed"}`;
          case "skipped":
            return `· ${name} (skipped)`;
          case "disabled":
            return `· ${name} (disabled)`;
          default:
            return `· ${name}`;
        }
      });
      return lines.join("\n");
    }
    if (syncing) return "Syncing…";
    if (lastSyncTs > 0) return `Last synced ${syncAgo}`;
    return "Sync now";
  });

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
    const delta = Math.max(0, $nowTick - lastSyncTs);
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
    if (syncing) {
      const label = activeSyncSource ? `Syncing ${sourceLabel(activeSyncSource)}…` : "Syncing…";
      return { status: "ok" as const, label };
    }
    if (syncError) return { status: "error" as const, label: "sync failed" };
    return { status: "ok" as const, label: "Sync" };
  });

  // The persistent error badge is driven by the status endpoint's last_error,
  // which is authoritative: it's refreshed after every sync (checkConnection
  // runs post-stream) and also reflects background/freshness syncs the in-app
  // stream never saw. A source that has since recovered clears itself, and —
  // because Settings reads the same field — the badge and Settings can't
  // disagree. Live per-source stream results (syncProgress) still surface in
  // the Sync button's own tooltip; they're transient, not a persisted error.
  const sourceErrors = $derived.by(() =>
    ($apiStatus?.sources ?? [])
      .filter((s) => s.enabled && s.last_error)
      .map((s) => ({ name: s.name, error: s.last_error as string })),
  );
  const hasErrors = $derived(sourceErrors.length > 0);
  const errorTooltip = $derived.by(() => {
    if (sourceErrors.length === 0) return "";
    const lines = sourceErrors.map((e) => `⚠ ${sourceLabel(e.name)} — ${e.error}`);
    return `${sourceErrors.length} source${sourceErrors.length > 1 ? "s" : ""} failed to sync\n${lines.join("\n")}\n\nClick to view in Settings`;
  });

  // Tracks the system light/dark preference so the tray glyph can be colored
  // to match the menu bar (see the tray $effect). Kept in sync by a
  // matchMedia listener in onMount.
  let prefersDark = $state(
    typeof window !== "undefined" && window.matchMedia
      ? window.matchMedia("(prefers-color-scheme: dark)").matches
      : true
  );

  // Mirror the error state onto the macOS tray icon (red badge). Re-runs on
  // every hasErrors OR theme transition (the badge is non-template, so the
  // glyph color has to follow the appearance). No-ops silently outside Tauri.
  $effect(() => {
    const errored = hasErrors;
    const dark = prefersDark;
    (async () => {
      try {
        const { invoke } = await import("@tauri-apps/api/core");
        await invoke("set_tray_error", { hasError: errored, dark });
      } catch {
        // Not running inside Tauri, or command unavailable.
      }
    })();
  });

  const llmInfo = $derived.by(() => {
    const llm = $apiStatus?.llm;
    const health = $llmHealth;
    const onClick = () => setRoute("settings");
    if (!llm || !llm.provider) {
      return {
        name: "LLM not configured",
        detail: "open Settings to set it up",
        status: "warn" as const,
        onClick,
        title: "Open Settings to configure",
      };
    }
    const labels: Record<string, string> = {
      ollama: "Ollama · local",
      openai: "OpenAI · BYOK",
      anthropic: "Anthropic · BYOK",
    };
    const name = labels[llm.provider] ?? llm.provider;
    const model = llm.model || "(default model)";

    // Reflect live reachability, not just "a provider is configured".
    switch (health.state) {
      case "error": {
        const reason = health.error || "unreachable — responses fall back to a template";
        return {
          name,
          detail: reason.length > 42 ? `${reason.slice(0, 41)}…` : reason,
          status: "error" as const,
          onClick,
          title: `${name} isn't responding:\n${reason}\n\nClick to open Settings`,
        };
      }
      case "ok":
        return { name, detail: model, status: "ok" as const, onClick, title: `${name} · reachable` };
      case "unsupported":
        // Old server without the health route — can't probe, so show the
        // provider plainly rather than a stuck spinner or a false error.
        return {
          name,
          detail: model,
          status: "ok" as const,
          onClick,
          title: `${name} · live status needs a newer devrecall server (run: brew upgrade devrecall)`,
        };
      default: // unknown / checking
        return { name, detail: "checking…", status: "syncing" as const, onClick, title: "Checking LLM connection…" };
    }
  });

  onMount(() => {
    checkConnection();
    const interval = setInterval(checkConnection, 30_000);

    // Probe LLM reachability on load, then periodically. The probe is cheap
    // (Ollama: a no-inference /api/tags check; BYOK: a 1-token ping), so a
    // few-minute cadence keeps the sidebar badge honest without noticeable
    // cost. Report routes also re-check right after a generation so a
    // template fallback flips the badge red immediately.
    checkLLMHealth();
    const llmInterval = setInterval(checkLLMHealth, 180_000);

    // Keep prefersDark in sync so the tray glyph recolors when the system
    // appearance changes while the app is open.
    const darkMq = window.matchMedia?.("(prefers-color-scheme: dark)");
    const onThemeChange = (e: MediaQueryListEvent) => (prefersDark = e.matches);
    darkMq?.addEventListener?.("change", onThemeChange);

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

    // Vim-style "g <letter>" leader shortcuts. The set of follow-up keys is
    // derived from navItems' kbd hints ("g c" → "c" navigates to chat), so
    // the sidebar labels and the handler can't drift out of sync.
    const leaderRoutes: Record<string, Tab> = (() => {
      const out: Record<string, Tab> = {};
      for (const item of navItems) {
        const m = item.kbd?.match(/^g\s+(\w)$/i);
        if (m) out[m[1].toLowerCase()] = item.id;
      }
      return out;
    })();
    let leaderArmed = false;
    let leaderTimer: ReturnType<typeof setTimeout> | undefined;
    function disarmLeader() {
      leaderArmed = false;
      if (leaderTimer) clearTimeout(leaderTimer);
      leaderTimer = undefined;
    }

    function isTextInput(target: EventTarget | null): boolean {
      if (!(target instanceof HTMLElement)) return false;
      if (target.isContentEditable) return true;
      const tag = target.tagName;
      return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
    }

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
      if (e.key === "Escape") {
        if (paletteOpen) return; // palette handles its own Esc
        if (leaderArmed) {
          disarmLeader();
          return;
        }
        window.close();
        return;
      }

      // Skip leader-key handling while typing in an input, while the
      // palette is open, or when modifier keys are held.
      if (paletteOpen || mod || e.altKey || isTextInput(e.target)) {
        disarmLeader();
        return;
      }

      const key = e.key.toLowerCase();
      if (leaderArmed) {
        const route = leaderRoutes[key];
        disarmLeader();
        if (route) {
          e.preventDefault();
          setRoute(route);
        }
        return;
      }
      if (key === "g") {
        leaderArmed = true;
        // Auto-disarm after a short window so a stray "g" doesn't swallow
        // the next single-letter input forever.
        leaderTimer = setTimeout(disarmLeader, 1200);
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
      clearInterval(llmInterval);
      darkMq?.removeEventListener?.("change", onThemeChange);
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
    syncTooltip={syncTooltip}
    syncing={syncing}
    onSync={triggerSync}
    hasErrors={hasErrors}
    errorTooltip={errorTooltip}
    onErrorsClick={() => setRoute("settings")}
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
