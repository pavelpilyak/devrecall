<script lang="ts">
  import { api, type SearchResult } from "../lib/api";

  let query = $state("");
  let sourceFilter = $state("");
  let results = $state<SearchResult[]>([]);
  let loading = $state(false);
  let error = $state("");
  let searched = $state(false);

  const sources = ["", "git", "slack", "calendar", "github", "gitlab", "bitbucket", "jira", "confluence", "linear"];

  async function doSearch() {
    if (!query.trim()) return;
    loading = true;
    error = "";
    searched = true;
    try {
      const resp = await api.search(query.trim(), {
        source: sourceFilter || undefined,
        limit: 50,
      });
      results = resp.results || [];
    } catch (e) {
      error = e instanceof Error ? e.message : "Search failed";
      results = [];
    } finally {
      loading = false;
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      doSearch();
    }
  }

  function formatTime(ts: string): string {
    return new Date(ts).toLocaleDateString(undefined, {
      year: "numeric", month: "short", day: "numeric",
    });
  }

  function sourceColor(source: string): string {
    const colors: Record<string, string> = {
      git: "bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400",
      slack: "bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400",
      calendar: "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400",
      github: "bg-zinc-200 text-zinc-700 dark:bg-zinc-700 dark:text-zinc-300",
      gitlab: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
      bitbucket: "bg-sky-100 text-sky-700 dark:bg-sky-900/30 dark:text-sky-400",
      jira: "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400",
      confluence: "bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400",
      linear: "bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400",
    };
    return colors[source] || "bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400";
  }
</script>

<div class="flex flex-col h-full">
  <!-- Search bar -->
  <div class="px-4 py-3 border-b border-zinc-200 dark:border-zinc-700 space-y-2">
    <div class="flex gap-2">
      <input
        type="text"
        bind:value={query}
        onkeydown={handleKeydown}
        placeholder="Search activities..."
        class="flex-1 px-3 py-2 text-sm rounded-lg border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-devrecall-500"
      />
      <button
        onclick={doSearch}
        disabled={loading || !query.trim()}
        class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
               hover:bg-devrecall-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        Search
      </button>
    </div>
    <div class="flex gap-2 items-center">
      <select
        bind:value={sourceFilter}
        class="text-xs px-2 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800"
      >
        <option value="">All sources</option>
        {#each sources.slice(1) as src}
          <option value={src}>{src}</option>
        {/each}
      </select>
      {#if searched && !loading}
        <span class="text-xs text-zinc-500">{results.length} results</span>
      {/if}
    </div>
  </div>

  <!-- Results -->
  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <div class="flex items-center justify-center h-32">
        <p class="text-sm text-zinc-500">Searching...</p>
      </div>
    {:else if error}
      <div class="mx-4 mt-3 text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">
        {error}
      </div>
    {:else if searched && results.length === 0}
      <div class="flex items-center justify-center h-32">
        <p class="text-sm text-zinc-500">No results found for "{query}".</p>
      </div>
    {:else if !searched}
      <div class="flex items-center justify-center h-32">
        <p class="text-sm text-zinc-500">Enter a search query to find activities.</p>
      </div>
    {:else}
      <div class="divide-y divide-zinc-100 dark:divide-zinc-800">
        {#each results as { activity }}
          <div class="px-4 py-3 hover:bg-zinc-50 dark:hover:bg-zinc-800/50 transition-colors">
            <div class="flex items-start gap-2">
              <span class="shrink-0 text-xs px-1.5 py-0.5 rounded {sourceColor(activity.source)}">
                {activity.source}
              </span>
              <div class="min-w-0 flex-1">
                <div class="text-sm font-medium">{activity.title}</div>
                {#if activity.content}
                  <div class="text-xs text-zinc-500 dark:text-zinc-400 mt-0.5 line-clamp-3">
                    {activity.content}
                  </div>
                {/if}
                <div class="text-xs text-zinc-400 mt-1">
                  {formatTime(activity.timestamp)} &middot; {activity.type}
                </div>
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
