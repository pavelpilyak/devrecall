<script lang="ts">
  import { onMount } from "svelte";
  import { api, type Activity } from "../lib/api";
  import { lastSyncAt } from "../lib/stores";
  import PanelHeader from "../components/ui/PanelHeader.svelte";
  import Chip from "../components/ui/Chip.svelte";
  import SourceDot from "../components/ui/SourceDot.svelte";
  import DateTimeField from "../components/ui/DateTimeField.svelte";

  const SOURCES = ["all", "git", "github", "gitlab", "bitbucket", "slack", "calendar", "jira", "linear"] as const;

  let activities = $state<Activity[]>([]);
  let loading = $state(false);
  let error = $state("");
  let totalCount = $state(0);

  let sourceFilter = $state<string>("all");
  let afterDate = $state(defaultAfter());
  let beforeDate = $state(todayStr());
  let limit = $state(100);

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
        source: sourceFilter === "all" ? undefined : sourceFilter,
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

  type Group = { day: string; items: Activity[] };

  const groups = $derived.by<Group[]>(() => {
    const buckets = new Map<string, Activity[]>();
    for (const a of activities) {
      const day = new Date(a.timestamp).toISOString().slice(0, 10);
      const arr = buckets.get(day) ?? [];
      arr.push(a);
      buckets.set(day, arr);
    }
    const out: Group[] = [];
    for (const [day, items] of buckets.entries()) {
      items.sort((x, y) => y.timestamp.localeCompare(x.timestamp));
      out.push({ day, items });
    }
    out.sort((a, b) => b.day.localeCompare(a.day));
    return out;
  });

  function formatTime(ts: string): string {
    const d = new Date(ts);
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
  }

  function formatDay(day: string): string {
    const d = new Date(day + "T00:00:00");
    const today = new Date();
    const yest = new Date();
    yest.setDate(yest.getDate() - 1);
    if (day === today.toISOString().slice(0, 10)) return "Today";
    if (day === yest.toISOString().slice(0, 10)) return "Yesterday";
    return d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" });
  }

  function metaLine(a: Activity): string {
    const parts: string[] = [];
    if (a.type) parts.push(a.type);
    if (a.source === "git" && a.metadata) {
      try {
        const m = JSON.parse(a.metadata);
        if (m.repo) parts.push(m.repo);
      } catch { /* noop */ }
    }
    return parts.join(" · ");
  }

  onMount(() => {
    loadActivities();
    const unsub = lastSyncAt.subscribe((ts) => {
      if (ts > 0) loadActivities();
    });
    return unsub;
  });
</script>

<div class="route-body">
  <PanelHeader title="Timeline" meta={totalCount > 0 ? `${totalCount} events` : undefined}>
    {#snippet actions()}
      <DateTimeField bind:value={afterDate} mode="date" onchange={loadActivities} />
      <span class="arrow">→</span>
      <DateTimeField bind:value={beforeDate} mode="date" onchange={loadActivities} />
    {/snippet}
  </PanelHeader>

  <div class="filters">
    {#each SOURCES as s}
      <button
        class="chip"
        class:active={sourceFilter === s}
        onclick={() => { sourceFilter = s; loadActivities(); }}
      >
        {#if s !== "all"}<SourceDot source={s} size={6} />{/if}
        <span>{s}</span>
      </button>
    {/each}
  </div>

  <div class="scroll">
    {#if loading}
      <div class="state">Loading activities…</div>
    {:else if error}
      <div class="error-box">{error}</div>
    {:else if activities.length === 0}
      <div class="state">No activities found for this period.</div>
    {:else}
      {#each groups as g (g.day)}
        <div class="day-head">
          {formatDay(g.day)}
          <span class="spacer"></span>
          <span class="count">{g.items.length} events</span>
        </div>
        {#each g.items as a, i (a.id ?? i)}
          <div class="row">
            <div class="time">{formatTime(a.timestamp)}</div>
            <SourceDot source={a.source} />
            <div class="row-main">
              <div class="title">{a.title}</div>
              {#if metaLine(a)}
                <div class="meta">{metaLine(a)}</div>
              {/if}
            </div>
            {#if a.type}
              <Chip>
                {#snippet children()}<span>{a.type}</span>{/snippet}
              </Chip>
            {/if}
          </div>
        {/each}
      {/each}
    {/if}
  </div>
</div>

<style>
  .route-body { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
  .scroll { flex: 1; overflow: auto; background: var(--ink-1); }

  .arrow { font-family: var(--font-mono); font-size: 11px; color: var(--fg-4); }

  .filters {
    display: flex;
    gap: 4px;
    padding: 10px 20px;
    border-bottom: 1px solid var(--border);
    background: var(--ink-1);
    overflow-x: auto;
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
    transition: background var(--dur-1) var(--ease-std), color var(--dur-1) var(--ease-std);
    white-space: nowrap;
  }
  .chip:hover { background: var(--ink-2); }
  .chip.active {
    border-color: var(--accent-line);
    background: var(--accent-wash);
    color: var(--mint-200);
  }

  .day-head {
    padding: 10px 20px 8px;
    display: flex;
    align-items: center;
    gap: 10px;
    font-family: var(--font-mono);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: var(--tracking-caps);
    color: var(--fg-3);
    font-weight: 500;
    background: var(--ink-1);
    position: sticky;
    top: 0;
    z-index: 1;
    border-bottom: 1px solid var(--hairline);
  }
  .spacer { flex: 1; }
  .count { color: var(--fg-4); }

  .row {
    display: grid;
    grid-template-columns: 64px 12px 1fr auto;
    gap: 12px;
    align-items: center;
    padding: 9px 20px;
    border-bottom: 1px solid var(--hairline);
  }
  .time {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    font-variant-numeric: tabular-nums;
  }
  .row-main { min-width: 0; }
  .title {
    font-size: 13px;
    color: var(--fg-1);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    margin-top: 2px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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
