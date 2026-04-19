<script lang="ts">
  import { api, type SearchResult } from "../lib/api";
  import PanelHeader from "../components/ui/PanelHeader.svelte";
  import Icon from "../components/ui/Icon.svelte";
  import Chip from "../components/ui/Chip.svelte";
  import SourceDot from "../components/ui/SourceDot.svelte";

  const SOURCES = ["all", "git", "github", "gitlab", "bitbucket", "slack", "calendar", "jira", "linear"] as const;

  let query = $state("");
  let sourceFilter = $state<string>("all");
  let results = $state<SearchResult[]>([]);
  let loading = $state(false);
  let error = $state("");
  let searched = $state(false);

  async function doSearch() {
    const q = query.trim();
    if (!q) return;
    loading = true;
    error = "";
    searched = true;
    try {
      const resp = await api.search(q, {
        source: sourceFilter === "all" ? undefined : sourceFilter,
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

  function formatDate(ts: string): string {
    return new Date(ts).toLocaleDateString(undefined, {
      year: "numeric", month: "short", day: "numeric",
    });
  }
</script>

<div class="route-body">
  <PanelHeader title="Search" meta={searched && !loading ? `${results.length} results` : undefined} />

  <div class="search-bar">
    <Icon name="search" size={14} />
    <input
      type="text"
      bind:value={query}
      onkeydown={handleKeydown}
      placeholder="Search across your local activity…"
    />
    {#if loading}<span class="loading">searching…</span>{/if}
  </div>

  <div class="filters">
    {#each SOURCES as s}
      <button
        class="chip"
        class:active={sourceFilter === s}
        onclick={() => { sourceFilter = s; if (searched) doSearch(); }}
      >
        {#if s !== "all"}<SourceDot source={s} size={6} />{/if}
        <span>{s}</span>
      </button>
    {/each}
  </div>

  <div class="scroll">
    {#if error}
      <div class="error-box">{error}</div>
    {:else if !searched}
      <div class="state">Type a query and press ↵ to search your local index.</div>
    {:else if loading}
      <div class="state">Searching…</div>
    {:else if results.length === 0}
      <div class="state">No results for "{query}".</div>
    {:else}
      {#each results as { activity } (activity.id ?? activity.timestamp + activity.title)}
        <div class="row">
          <SourceDot source={activity.source} />
          <div class="main">
            <div class="title">{activity.title}</div>
            {#if activity.content}
              <div class="preview">{activity.content}</div>
            {/if}
            <div class="meta">
              {formatDate(activity.timestamp)}
              {#if activity.type} · {activity.type}{/if}
            </div>
          </div>
          <Chip>
            {#snippet children()}<span>{activity.source}</span>{/snippet}
          </Chip>
        </div>
      {/each}
    {/if}
  </div>
</div>

<style>
  .route-body { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
  .scroll { flex: 1; overflow: auto; background: var(--ink-1); }

  .search-bar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 20px;
    border-bottom: 1px solid var(--border);
    background: var(--ink-1);
    color: var(--fg-3);
  }
  .search-bar input {
    flex: 1;
    background: transparent;
    border: none;
    outline: none;
    color: var(--fg-1);
    font-family: var(--font-sans);
    font-size: 14px;
  }
  .search-bar input::placeholder { color: var(--fg-4); }
  .loading {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-4);
  }

  .filters {
    display: flex;
    gap: 4px;
    padding: 10px 20px;
    border-bottom: 1px solid var(--border);
    background: var(--ink-1);
    flex-wrap: wrap;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 24px;
    padding: 0 8px;
    border-radius: var(--r-1);
    border: 1px solid transparent;
    background: transparent;
    color: var(--fg-2);
    font-family: var(--font-mono);
    font-size: 11px;
    cursor: pointer;
    text-transform: capitalize;
    white-space: nowrap;
  }
  .chip:hover { background: var(--ink-2); }
  .chip.active {
    border-color: var(--accent-line);
    background: var(--accent-wash);
    color: var(--mint-200);
  }

  .row {
    display: grid;
    grid-template-columns: 12px 1fr auto;
    gap: 12px;
    align-items: flex-start;
    padding: 12px 20px;
    border-bottom: 1px solid var(--hairline);
    cursor: pointer;
    transition: background 80ms;
  }
  .row:hover { background: var(--ink-2); }
  .main { min-width: 0; }
  .title {
    font-size: 13px;
    color: var(--fg-1);
    font-weight: 500;
  }
  .preview {
    font-size: 12px;
    color: var(--fg-3);
    margin-top: 4px;
    line-height: 1.5;
    display: -webkit-box;
    -webkit-line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .meta {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-4);
    margin-top: 6px;
  }

  .state {
    padding: 60px 20px;
    text-align: center;
    font-size: 13px;
    color: var(--fg-3);
  }
  .error-box {
    margin: 16px 20px;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--danger);
    background: var(--danger-wash);
    border: 1px solid rgba(255, 107, 107, 0.2);
    border-radius: var(--r-2);
    padding: 10px 12px;
  }
</style>
