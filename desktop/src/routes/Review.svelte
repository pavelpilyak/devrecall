<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { api, type ReviewResponse } from "../lib/api";
  import { save, load } from "../lib/persist";
  import Markdown from "../components/Markdown.svelte";

  type ReviewType = "brag" | "perf-review";

  type ReviewCache = Record<string, ReviewResponse>;
  let cache = $state<ReviewCache>(load<ReviewCache>("review:cache") ?? {});

  function cacheKey(type: ReviewType, after: string, before: string): string {
    return `${type}:${after}:${before}`;
  }

  let reviewType = $state<ReviewType>("brag");
  let afterDate = $state(defaultAfter());
  let beforeDate = $state(todayStr());
  const initialReport = cache[cacheKey("brag", defaultAfter(), todayStr())] ?? null;
  let report = $state<ReviewResponse | null>(initialReport);
  let loading = $state(false);
  let error = $state("");
  let copied = $state(false);
  let generated = $state(initialReport !== null);

  function todayStr(): string {
    return new Date().toISOString().slice(0, 10);
  }

  function defaultAfter(): string {
    const d = new Date();
    d.setMonth(d.getMonth() - 1);
    return d.toISOString().slice(0, 10);
  }

  function setPreset(preset: string) {
    const now = new Date();
    let after: Date;
    let before: Date;

    switch (preset) {
      case "last-month": {
        after = new Date(now.getFullYear(), now.getMonth() - 1, 1);
        before = new Date(now.getFullYear(), now.getMonth(), 0);
        break;
      }
      case "last-quarter": {
        const q = Math.floor(now.getMonth() / 3);
        after = new Date(now.getFullYear(), (q - 1) * 3, 1);
        before = new Date(now.getFullYear(), q * 3, 0);
        break;
      }
      case "last-6-months": {
        after = new Date(now.getFullYear(), now.getMonth() - 6, 1);
        before = new Date(now.getFullYear(), now.getMonth(), 0);
        break;
      }
      default:
        return;
    }

    afterDate = after.toISOString().slice(0, 10);
    beforeDate = before.toISOString().slice(0, 10);
    report = cache[cacheKey(reviewType, afterDate, beforeDate)] ?? null;
    generated = !!report;
  }

  async function generate() {
    loading = true;
    error = "";
    copied = false;
    generated = true;
    try {
      if (reviewType === "brag") {
        report = await api.brag(afterDate, beforeDate);
      } else {
        report = await api.perfReview(afterDate, beforeDate);
      }
      if (report) {
        cache[cacheKey(reviewType, afterDate, beforeDate)] = report;
        save("review:cache", cache);
      }
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to generate";
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

  async function openInFinder() {
    if (!report?.file_path) return;
    try {
      await invoke("reveal_file", { path: report.file_path });
    } catch {
      // Fallback: ignore if not running in Tauri.
    }
  }

  function switchType(type: ReviewType) {
    reviewType = type;
    report = cache[cacheKey(type, afterDate, beforeDate)] ?? null;
    generated = !!report;
  }
</script>

<div class="flex flex-col h-full">
  <!-- Header -->
  <div class="px-4 py-3 border-b border-zinc-200 dark:border-zinc-700 space-y-2">
    <!-- Type toggle -->
    <div class="flex gap-1 bg-zinc-100 dark:bg-zinc-800 rounded-lg p-0.5">
      <button
        onclick={() => switchType("brag")}
        class="flex-1 px-3 py-1.5 text-xs font-medium rounded-md transition-colors
               {reviewType === 'brag'
                 ? 'bg-white dark:bg-zinc-700 text-zinc-900 dark:text-zinc-100 shadow-sm'
                 : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-300'}"
      >
        Brag Doc
      </button>
      <button
        onclick={() => switchType("perf-review")}
        class="flex-1 px-3 py-1.5 text-xs font-medium rounded-md transition-colors
               {reviewType === 'perf-review'
                 ? 'bg-white dark:bg-zinc-700 text-zinc-900 dark:text-zinc-100 shadow-sm'
                 : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-300'}"
      >
        Perf Review
      </button>
    </div>

    <!-- Date range + presets -->
    <div class="flex gap-2 items-center flex-wrap">
      <input
        type="date"
        bind:value={afterDate}
        class="text-xs px-2 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800"
      />
      <span class="text-xs text-zinc-400">to</span>
      <input
        type="date"
        bind:value={beforeDate}
        class="text-xs px-2 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800"
      />
    </div>
    <div class="flex gap-1.5">
      {#each [["last-month", "Last month"], ["last-quarter", "Last quarter"], ["last-6-months", "Last 6 months"]] as [value, label]}
        <button
          onclick={() => setPreset(value)}
          class="text-xs px-2 py-1 rounded-md border border-zinc-200 dark:border-zinc-700
                 text-zinc-600 dark:text-zinc-400 hover:bg-zinc-50 dark:hover:bg-zinc-800
                 transition-colors"
        >
          {label}
        </button>
      {/each}
    </div>
  </div>

  <!-- Report content -->
  <div class="flex-1 overflow-y-auto px-4 py-3">
    {#if loading}
      <div class="flex items-center justify-center h-32">
        <p class="text-sm text-zinc-500">
          Generating {reviewType === "brag" ? "brag doc" : "perf review"}...
        </p>
      </div>
    {:else if error}
      <div class="text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">
        {error}
      </div>
    {:else if report}
      {#if report.activity_count === 0}
        <div class="flex items-center justify-center h-32">
          <p class="text-sm text-zinc-500">No activities found for this period.</p>
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
          Generate {reviewType === "brag" ? "Brag Doc" : "Perf Review"}
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
      {#if report?.file_path}
        <button
          onclick={openInFinder}
          class="px-4 py-2 text-sm font-medium rounded-lg border border-zinc-300 dark:border-zinc-600
                 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
          title="Show saved file in Finder"
        >
          Show in Finder
        </button>
      {/if}
      <button
        onclick={generate}
        disabled={loading}
        class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
               hover:bg-devrecall-700 disabled:opacity-50 transition-colors"
      >
        Generate Again
      </button>
    </div>
  {/if}
</div>
