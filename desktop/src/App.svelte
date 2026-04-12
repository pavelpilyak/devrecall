<script lang="ts">
  import { onMount } from "svelte";
  import { connected, checkConnection } from "./lib/stores";
  import { isPro } from "./lib/license";
  import AppPaywall from "./components/AppPaywall.svelte";
  import Chat from "./routes/Chat.svelte";
  import Standup from "./routes/Standup.svelte";
  import Weekly from "./routes/Weekly.svelte";
  import Timeline from "./routes/Timeline.svelte";
  import Search from "./routes/Search.svelte";
  import Log from "./routes/Log.svelte";
  import Review from "./routes/Review.svelte";
  import Settings from "./routes/Settings.svelte";

  type Tab = "chat" | "standup" | "weekly" | "review" | "timeline" | "search" | "log" | "settings";

  const tabs: { id: Tab; label: string }[] = [
    { id: "chat", label: "Chat" },
    { id: "standup", label: "Standup" },
    { id: "weekly", label: "Weekly" },
    { id: "review", label: "Review" },
    { id: "timeline", label: "Timeline" },
    { id: "search", label: "Search" },
    { id: "log", label: "Log" },
    { id: "settings", label: "Settings" },
  ];

  let activeTab = $state<Tab>("chat");
  let logView = $state<{ focus: () => void } | null>(null);

  onMount(() => {
    checkConnection();
    const interval = setInterval(checkConnection, 30_000);

    function onKeydown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        window.close();
      }
    }
    window.addEventListener("keydown", onKeydown);

    // Listen for the tray "Log Event…" menu item, which switches to the
    // Log tab and focuses the text area for rapid entry.
    let unlisten: (() => void) | undefined;
    (async () => {
      try {
        const { listen } = await import("@tauri-apps/api/event");
        unlisten = await listen("open-log-quickadd", () => {
          activeTab = "log";
          // Wait a tick so the textarea is mounted/visible before focusing.
          setTimeout(() => logView?.focus(), 50);
        });
      } catch {
        // Not running inside Tauri (e.g. plain `vite dev` in a browser) — ignore.
      }
    })();

    return () => {
      clearInterval(interval);
      window.removeEventListener("keydown", onKeydown);
      unlisten?.();
    };
  });
</script>

<main class="h-screen flex flex-col bg-white dark:bg-zinc-900">
  {#if !$connected}
    <div class="flex-1 flex items-center justify-center p-8">
      <div class="text-center space-y-3">
        <div class="text-4xl">&#128268;</div>
        <h2 class="text-lg font-semibold">Connecting to DevRecall...</h2>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">
          Make sure <code class="bg-zinc-100 dark:bg-zinc-800 px-1 rounded">devrecall serve</code> is running.
        </p>
      </div>
    </div>
  {:else if !$isPro}
    <!-- License required — show paywall -->
    <AppPaywall />
  {:else}
    <!-- Tab bar -->
    <nav class="flex border-b border-zinc-200 dark:border-zinc-700 px-1 pt-1 overflow-x-auto">
      {#each tabs as tab}
        <button
          onclick={() => activeTab = tab.id}
          class="px-3 py-2 text-xs font-medium border-b-2 transition-colors whitespace-nowrap
                 {activeTab === tab.id
                   ? 'border-devrecall-600 text-devrecall-600 dark:text-devrecall-500'
                   : 'border-transparent text-zinc-500 dark:text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-300'}"
        >
          {tab.label}
        </button>
      {/each}
    </nav>

    <!-- Tab content — all mounted, hidden via CSS to preserve state -->
    <div class="flex-1 overflow-hidden relative">
      <div class="absolute inset-0" class:hidden={activeTab !== "chat"}><Chat /></div>
      <div class="absolute inset-0" class:hidden={activeTab !== "standup"}><Standup /></div>
      <div class="absolute inset-0" class:hidden={activeTab !== "weekly"}><Weekly /></div>
      <div class="absolute inset-0" class:hidden={activeTab !== "timeline"}><Timeline /></div>
      <div class="absolute inset-0" class:hidden={activeTab !== "review"}><Review /></div>
      <div class="absolute inset-0" class:hidden={activeTab !== "search"}><Search /></div>
      <div class="absolute inset-0" class:hidden={activeTab !== "log"}><Log bind:this={logView} /></div>
      <div class="absolute inset-0" class:hidden={activeTab !== "settings"}><Settings /></div>
    </div>
  {/if}
</main>
