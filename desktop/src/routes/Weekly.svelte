<script lang="ts">
  import { api, type WeeklyResponse } from "../lib/api";
  import { save, load } from "../lib/persist";
  import PanelHeader from "../components/ui/PanelHeader.svelte";
  import Btn from "../components/ui/Btn.svelte";
  import Icon from "../components/ui/Icon.svelte";
  import ReportView from "../components/ReportView.svelte";

  type WeeklyCache = Record<number, WeeklyResponse>;
  let cache = $state<WeeklyCache>(load<WeeklyCache>("weekly:cache") ?? {});

  let weeksBack = $state(0);
  const initialReport = cache[0] ?? null;
  let report = $state<WeeklyResponse | null>(initialReport);
  let loading = $state(false);
  let error = $state("");
  let copied = $state(false);
  let generated = $state(initialReport !== null);

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
    error = "";
  }

  function generate() {
    generated = true;
    loadWeekly();
  }

  function weekLabel(): string {
    if (weeksBack === 0) return "This week";
    if (weeksBack === 1) return "Last week";
    return `${weeksBack} weeks ago`;
  }

  function weekRange(): string {
    const now = new Date();
    const start = new Date(now);
    start.setDate(start.getDate() - start.getDay() + 1 - weeksBack * 7);
    const end = new Date(start);
    end.setDate(end.getDate() + 6);
    const fmt = (d: Date) => d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
    return `${fmt(start)} – ${fmt(end)}`;
  }

  const titleText = $derived(`Weekly summary · ${weekLabel()}`);
  const metaText = $derived.by(() => {
    if (!report) return `${weekRange()} · tap generate to summarize`;
    return `${report.week_start} to ${report.week_end} · ${report.activity_count} activities`;
  });
</script>

<div class="route-body">
  <PanelHeader title="Weekly summary" meta="7 days">
    {#snippet actions()}
      <Btn size="sm" variant="ghost" onclick={() => changeWeek(-1)}>
        {#snippet children()}<Icon name="chevron-left" size={12} /><span>Older</span>{/snippet}
      </Btn>
      <Btn size="sm" variant="ghost" disabled={weeksBack === 0} onclick={() => changeWeek(1)}>
        {#snippet children()}<span>Newer</span><Icon name="chevron-right" size={12} />{/snippet}
      </Btn>
      <Btn size="sm" variant="ghost" disabled={!report || report.activity_count === 0} onclick={copyReport}>
        {#snippet children()}
          <Icon name="copy" size={12} />
          <span>{copied ? "Copied" : "Copy"}</span>
        {/snippet}
      </Btn>
      <Btn size="sm" variant="primary" disabled={loading} onclick={generate}>
        {#snippet children()}
          <Icon name={loading ? "loader" : "sparkles"} size={12} />
          <span>{loading ? "Generating…" : generated ? "Regenerate" : "Generate"}</span>
        {/snippet}
      </Btn>
    {/snippet}
  </PanelHeader>

  <div class="scroll">
    {#if !generated}
      <div class="cta">
        <div class="cta-eyebrow">Weekly summary</div>
        <h1 class="cta-title">{titleText}</h1>
        <div class="cta-meta">{weekRange()}</div>
        <div class="cta-btn">
          <Btn variant="primary" onclick={generate}>
            {#snippet children()}
              <Icon name="sparkles" size={14} />
              <span>Generate summary</span>
            {/snippet}
          </Btn>
        </div>
      </div>
    {:else}
      <ReportView
        eyebrow="Weekly summary"
        title={titleText}
        meta={metaText}
        loading={loading}
        error={error}
        empty={!!report && report.activity_count === 0}
        emptyLabel="No activities found for this week."
        content={report?.report ?? ""}
      />
    {/if}
  </div>
</div>

<style>
  .route-body { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
  .scroll {
    flex: 1;
    overflow: auto;
    background: var(--ink-1);
  }
  .cta {
    margin: 80px 0;
    padding: 0 40px;
  }
  .cta-eyebrow {
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: var(--tracking-caps);
    text-transform: uppercase;
    color: var(--fg-3);
    font-weight: 500;
  }
  .cta-title {
    margin: 6px 0 8px;
    font-size: 28px;
    font-weight: 600;
    letter-spacing: -0.018em;
    color: var(--fg-1);
    line-height: 1.2;
  }
  .cta-meta {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }
  .cta-btn { margin-top: 28px; }
</style>
