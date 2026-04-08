<script lang="ts">
  import { api, type StandupResponse } from "../lib/api";

  let date = $state(yesterdayStr());
  let report = $state<StandupResponse | null>(null);
  let loading = $state(false);
  let error = $state("");
  let copied = $state(false);

  function yesterdayStr(): string {
    const d = new Date();
    d.setDate(d.getDate() - 1);
    return d.toISOString().slice(0, 10);
  }

  async function loadStandup() {
    loading = true;
    error = "";
    copied = false;
    try {
      report = await api.standup(date);
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load standup";
      report = null;
    } finally {
      loading = false;
    }
  }

  async function copyReport() {
    if (!report) return;
    await navigator.clipboard.writeText(report.report);
    copied = true;
    setTimeout(() => { copied = false; }, 2000);
  }

  function changeDate(delta: number) {
    const d = new Date(date);
    d.setDate(d.getDate() + delta);
    date = d.toISOString().slice(0, 10);
    loadStandup();
  }

</script>

<div class="flex flex-col h-full">
  <!-- Date picker header -->
  <div class="flex items-center justify-between px-4 py-3 border-b border-zinc-200 dark:border-zinc-700">
    <button
      onclick={() => changeDate(-1)}
      class="p-1.5 rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors text-zinc-600 dark:text-zinc-400"
      title="Previous day"
    >
      &larr;
    </button>

    <div class="flex items-center gap-2">
      <input
        type="date"
        bind:value={date}
        onchange={loadStandup}
        class="text-sm px-2 py-1 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-devrecall-500"
      />
      {#if report}
        <span class="text-xs text-zinc-500">{report.activity_count} activities</span>
      {/if}
    </div>

    <button
      onclick={() => changeDate(1)}
      class="p-1.5 rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors text-zinc-600 dark:text-zinc-400"
      title="Next day"
    >
      &rarr;
    </button>
  </div>

  <!-- Report content -->
  <div class="flex-1 overflow-y-auto px-4 py-3">
    {#if loading}
      <div class="flex items-center justify-center h-32">
        <p class="text-sm text-zinc-500">Generating standup...</p>
      </div>
    {:else if error}
      <div class="text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">
        {error}
      </div>
    {:else if report}
      {#if report.activity_count === 0}
        <div class="flex items-center justify-center h-32">
          <p class="text-sm text-zinc-500">No activities found for {date}.</p>
        </div>
      {:else}
        <div class="text-sm whitespace-pre-wrap leading-relaxed">{report.report}</div>
      {/if}
    {:else}
      <div class="flex items-center justify-center h-32">
        <button
          onclick={loadStandup}
          class="px-6 py-2.5 text-sm font-medium rounded-lg bg-devrecall-600 text-white
                 hover:bg-devrecall-700 transition-colors"
        >
          Generate Standup
        </button>
      </div>
    {/if}
  </div>

  <!-- Actions footer -->
  <div class="border-t border-zinc-200 dark:border-zinc-700 px-4 py-3 flex gap-2">
    <button
      onclick={copyReport}
      disabled={!report || report.activity_count === 0}
      class="flex-1 px-4 py-2 text-sm font-medium rounded-lg border border-zinc-300 dark:border-zinc-600
             hover:bg-zinc-50 dark:hover:bg-zinc-800 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
    >
      {copied ? "Copied!" : "Copy to Clipboard"}
    </button>
    <button
      onclick={loadStandup}
      disabled={loading}
      class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
             hover:bg-devrecall-700 disabled:opacity-50 transition-colors"
    >
      Refresh
    </button>
  </div>
</div>
