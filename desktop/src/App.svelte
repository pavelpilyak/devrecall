<script lang="ts">
  import { onMount } from "svelte";
  import { apiStatus, connected, checkConnection } from "./lib/stores";
  import Popover from "./components/Popover.svelte";

  let currentView = $state<"popover" | "standup" | "weekly" | "settings">("popover");

  onMount(() => {
    checkConnection();
    // Poll API status every 30 seconds.
    const interval = setInterval(checkConnection, 30_000);
    return () => clearInterval(interval);
  });
</script>

<main class="h-screen flex flex-col">
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
  {:else}
    <Popover />
  {/if}
</main>
