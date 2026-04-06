<script lang="ts">
  import { onMount } from "svelte";
  import { api, type Activity } from "../lib/api";

  const sources = ["", "git", "slack", "calendar", "github", "gitlab", "bitbucket", "jira", "linear"];
  const types = ["", "commit", "message", "meeting", "ticket", "review", "pull_request", "merge_request", "issue"];

  let activities = $state<Activity[]>([]);
  let loading = $state(false);
  let error = $state("");
  let totalCount = $state(0);

  // Filters
  let sourceFilter = $state("");
  let typeFilter = $state("");
  let afterDate = $state(defaultAfter());
  let beforeDate = $state(todayStr());
  let limit = $state(50);

  function todayStr(): string {
    return new Date().toISOString().slice(0, 10);
  }

  function defaultAfter(): string {
    const d = new Date();
    d.setDate(d.getDate() - 7);
    return d.toISOString().slice(0, 10);
  }

  async function loadActivities() {
    loading = true;
    error = "";
    try {
      const resp = await api.activities({
        source: sourceFilter || undefined,
        type: typeFilter || undefined,
        after: afterDate || undefined,
        before: beforeDate || undefined,
        limit,
      });
      activities = resp.activities || [];
      totalCount = resp.count;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load activities";
      activities = [];
    } finally {
      loading = false;
    }
  }

  function formatTime(ts: string): string {
    const d = new Date(ts);
    return d.toLocaleString(undefined, {
      month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
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
      linear: "bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400",
    };
    return colors[source] || "bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400";
  }

  onMount(() => {
    loadActivities();
  });
</script>

<div class="flex flex-col h-full">
  <!-- Filters bar -->
  <div class="px-4 py-3 border-b border-zinc-200 dark:border-zinc-700 space-y-2">
    <div class="flex gap-2 flex-wrap">
      <select
        bind:value={sourceFilter}
        onchange={loadActivities}
        class="text-xs px-2 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800"
      >
        <option value="">All sources</option>
        {#each sources.slice(1) as src}
          <option value={src}>{src}</option>
        {/each}
      </select>

      <select
        bind:value={typeFilter}
        onchange={loadActivities}
        class="text-xs px-2 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800"
      >
        <option value="">All types</option>
        {#each types.slice(1) as typ}
          <option value={typ}>{typ}</option>
        {/each}
      </select>

      <input
        type="date"
        bind:value={afterDate}
        onchange={loadActivities}
        class="text-xs px-2 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800"
      />
      <span class="text-xs text-zinc-400 self-center">to</span>
      <input
        type="date"
        bind:value={beforeDate}
        onchange={loadActivities}
        class="text-xs px-2 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800"
      />
    </div>
    {#if totalCount > 0}
      <div class="text-xs text-zinc-500">{totalCount} activities</div>
    {/if}
  </div>

  <!-- Activity list -->
  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <div class="flex items-center justify-center h-32">
        <p class="text-sm text-zinc-500">Loading activities...</p>
      </div>
    {:else if error}
      <div class="mx-4 mt-3 text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">
        {error}
      </div>
    {:else if activities.length === 0}
      <div class="flex items-center justify-center h-32">
        <p class="text-sm text-zinc-500">No activities found for this period.</p>
      </div>
    {:else}
      <div class="divide-y divide-zinc-100 dark:divide-zinc-800">
        {#each activities as activity}
          <div class="px-4 py-3 hover:bg-zinc-50 dark:hover:bg-zinc-800/50 transition-colors">
            <div class="flex items-start gap-2">
              <span class="shrink-0 text-xs px-1.5 py-0.5 rounded {sourceColor(activity.source)}">
                {activity.source}
              </span>
              <div class="min-w-0 flex-1">
                <div class="text-sm font-medium truncate">{activity.title}</div>
                {#if activity.content}
                  <div class="text-xs text-zinc-500 dark:text-zinc-400 mt-0.5 line-clamp-2">
                    {activity.content}
                  </div>
                {/if}
                <div class="text-xs text-zinc-400 mt-1">
                  {formatTime(activity.timestamp)}
                  {#if activity.type}
                    &middot; {activity.type}
                  {/if}
                </div>
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
