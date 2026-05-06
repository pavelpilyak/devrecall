<script lang="ts">
  import { api, type StandupResponse } from "../lib/api";
  import { save, load } from "../lib/persist";
  import { today } from "../lib/stores";
  import PanelHeader from "../components/ui/PanelHeader.svelte";
  import Btn from "../components/ui/Btn.svelte";
  import Icon from "../components/ui/Icon.svelte";
  import DateTimeField from "../components/ui/DateTimeField.svelte";
  import ReportView from "../components/ReportView.svelte";

  type StandupCache = Record<string, StandupResponse>;
  let cache = $state<StandupCache>(load<StandupCache>("standup:cache") ?? {});

  let date = $state(yesterdayStr());
  const initialReport = cache[yesterdayStr()] ?? null;
  let report = $state<StandupResponse | null>(initialReport);
  let loading = $state(false);
  let error = $state("");
  let copied = $state(false);
  let generated = $state(initialReport !== null);

  function yesterdayStr(): string {
    const d = new Date();
    d.setDate(d.getDate() - 1);
    return d.toISOString().slice(0, 10);
  }

  let prevYesterday = yesterdayStr();
  $effect(() => {
    void $today;
    const newYesterday = yesterdayStr();
    if (newYesterday !== prevYesterday && date === prevYesterday) {
      date = newYesterday;
      report = cache[date] ?? null;
      generated = !!report;
      error = "";
    }
    prevYesterday = newYesterday;
  });

  async function loadStandup() {
    loading = true;
    error = "";
    copied = false;
    try {
      report = await api.standup(date);
      if (report) {
        cache[date] = report;
        save("standup:cache", cache);
      }
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
    report = cache[date] ?? null;
    generated = !!report;
    error = "";
  }

  function generate() {
    generated = true;
    loadStandup();
  }

  const eyebrow = "Standup";
  const titleText = $derived.by(() => {
    const d = new Date(date + "T00:00:00");
    return `Standup for ${d.toLocaleDateString(undefined, { weekday: "long", month: "short", day: "numeric" })}`;
  });
  const metaText = $derived.by(() => {
    if (!report) return `${date} · tap generate to summarize`;
    return `${date} · ${report.activity_count} activities`;
  });
</script>

<div class="route-body">
  <PanelHeader title="Standup" meta="daily summary">
    {#snippet actions()}
      <Btn size="sm" variant="ghost" onclick={() => changeDate(-1)}>
        {#snippet children()}<Icon name="chevron-left" size={12} />{/snippet}
      </Btn>
      <DateTimeField
        bind:value={date}
        mode="date"
        onchange={() => { report = cache[date] ?? null; generated = !!report; }}
      />
      <Btn size="sm" variant="ghost" onclick={() => changeDate(1)}>
        {#snippet children()}<Icon name="chevron-right" size={12} />{/snippet}
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
        <div class="cta-eyebrow">Standup</div>
        <h1 class="cta-title">{titleText}</h1>
        <div class="cta-meta">{date}</div>
        <div class="cta-btn">
          <Btn variant="primary" onclick={generate}>
            {#snippet children()}
              <Icon name="sparkles" size={14} />
              <span>Generate standup</span>
            {/snippet}
          </Btn>
        </div>
      </div>
    {:else}
      <ReportView
        eyebrow={eyebrow}
        title={titleText}
        meta={metaText}
        loading={loading}
        error={error}
        empty={!!report && report.activity_count === 0}
        emptyLabel={`No activities found for ${date}.`}
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
    text-align: left;
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
  .cta-btn {
    margin-top: 28px;
  }
</style>
