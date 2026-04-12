<script lang="ts">
  import { api, type WeeklyResponse } from "../lib/api";
  import { save, load } from "../lib/persist";
  import Markdown from "../components/Markdown.svelte";

  type WeeklyCache = Record<number, WeeklyResponse>;
  let cache = $state<WeeklyCache>(load<WeeklyCache>("weekly:cache") ?? {});

  let weeksBack = $state(0);
  const initialReport = cache[0] ?? null;
  let report = $state<WeeklyResponse | null>(initialReport);
  let loading = $state(false);
  let error = $state("");
  let copied = $state(false);

  async function loadWeekly() {
    loading = true;
    error = "";
    copied = false;
    try {
      report = await api.week(weeksBack);
      if (report) {
        cache[weeksBack] = report;
        save("weekly:cache", cache);
      }
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load weekly summary";
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

  function changeWeek(delta: number) {
    weeksBack = Math.max(0, weeksBack - delta);
    report = cache[weeksBack] ?? null;
    generated = !!report;
  }

  function weekLabel(): string {
    if (weeksBack === 0) return "This week";
    if (weeksBack === 1) return "Last week";
    const now = new Date();
    const start = new Date(now);
    start.setDate(start.getDate() - start.getDay() + 1 - weeksBack * 7); // Monday
    const end = new Date(start);
    end.setDate(end.getDate() + 6); // Sunday
    const fmt = (d: Date) => d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
    return `${fmt(start)} – ${fmt(end)}`;
  }

  let generated = $state(initialReport !== null);

  function generate() {
    generated = true;
    loadWeekly();
  }
</script>

<div class="flex flex-col h-full">
  <!-- Week selector header -->
  <div class="flex items-center justify-between px-4 py-3 border-b border-zinc-200 dark:border-zinc-700">
    <button
      onclick={() => changeWeek(-1)}
      class="p-1.5 rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors text-zinc-600 dark:text-zinc-400"
      title="Previous week"
    >
      &larr;
    </button>

    <div class="text-center">
      <div class="text-sm font-medium">{weekLabel()}</div>
      {#if report}
        <div class="text-xs text-zinc-500">{report.week_start} to {report.week_end} &middot; {report.activity_count} activities</div>
      {/if}
    </div>

    <button
      onclick={() => changeWeek(1)}
      disabled={weeksBack === 0}
      class="p-1.5 rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors text-zinc-600 dark:text-zinc-400
             disabled:opacity-30 disabled:cursor-not-allowed"
      title="Next week"
    >
      &rarr;
    </button>
  </div>

  <!-- Report content -->
  <div class="flex-1 overflow-y-auto px-4 py-3">
    {#if loading}
      <div class="flex items-center justify-center h-32">
        <p class="text-sm text-zinc-500">Generating weekly summary...</p>
      </div>
    {:else if error}
      <div class="text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">
        {error}
      </div>
    {:else if report}
      {#if report.activity_count === 0}
        <div class="flex items-center justify-center h-32">
          <p class="text-sm text-zinc-500">No activities found for this week.</p>
        </div>
      {:else}
        <Markdown content={report.report} class="text-sm leading-relaxed" />
      {/if}
    {:else}
      <div class="flex items-center justify-center h-32">
        <button
          onclick={generate}
          class="px-6 py-2.5 text-sm font-medium rounded-lg bg-devrecall-600 text-white
                 hover:bg-devrecall-700 transition-colors"
        >
          Generate Summary
        </button>
      </div>
    {/if}
  </div>

  <!-- Actions footer -->
  {#if generated}
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
        onclick={generate}
        disabled={loading}
        class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
               hover:bg-devrecall-700 disabled:opacity-50 transition-colors"
      >
        {loading ? "Generating..." : "Generate Again"}
      </button>
    </div>
  {/if}
</div>
