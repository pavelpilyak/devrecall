<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { api, type ReviewResponse } from "../lib/api";
  import { save, load } from "../lib/persist";
  import PanelHeader from "../components/ui/PanelHeader.svelte";
  import Btn from "../components/ui/Btn.svelte";
  import Icon from "../components/ui/Icon.svelte";
  import DateTimeField from "../components/ui/DateTimeField.svelte";
  import ReportView from "../components/ReportView.svelte";

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
      case "last-month":
        after = new Date(now.getFullYear(), now.getMonth() - 1, 1);
        before = new Date(now.getFullYear(), now.getMonth(), 0);
        break;
      case "last-quarter": {
        const q = Math.floor(now.getMonth() / 3);
        after = new Date(now.getFullYear(), (q - 1) * 3, 1);
        before = new Date(now.getFullYear(), q * 3, 0);
        break;
      }
      case "last-6-months":
        after = new Date(now.getFullYear(), now.getMonth() - 6, 1);
        before = new Date(now.getFullYear(), now.getMonth(), 0);
        break;
      default:
        return;
    }

    afterDate = after.toISOString().slice(0, 10);
    beforeDate = before.toISOString().slice(0, 10);
    report = cache[cacheKey(reviewType, afterDate, beforeDate)] ?? null;
    generated = !!report;
    error = "";
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
      // Not in Tauri.
    }
  }

  function switchType(type: ReviewType) {
    reviewType = type;
    report = cache[cacheKey(type, afterDate, beforeDate)] ?? null;
    generated = !!report;
    error = "";
  }

  const TAB_META: Record<ReviewType, { label: string; meta: string; eyebrow: string }> = {
    "brag": { label: "Brag doc", meta: "career wins", eyebrow: "Brag doc" },
    "perf-review": { label: "Perf review", meta: "self-review", eyebrow: "Perf review" },
  };

  const titleText = $derived.by(() => {
    const label = reviewType === "brag" ? "Brag doc" : "Perf review";
    return `${label} · ${afterDate} → ${beforeDate}`;
  });
  const metaText = $derived.by(() => {
    if (!report) return `${afterDate} → ${beforeDate}`;
    return `${afterDate} → ${beforeDate} · ${report.activity_count} activities`;
  });
</script>

<div class="route-body">
  <PanelHeader title="Review">
    {#snippet actions()}
      <Btn size="sm" variant="ghost" disabled={!report || report.activity_count === 0} onclick={copyReport}>
        {#snippet children()}
          <Icon name="copy" size={12} />
          <span>{copied ? "Copied" : "Copy"}</span>
        {/snippet}
      </Btn>
      {#if report?.file_path}
        <Btn size="sm" variant="ghost" onclick={openInFinder}>
          {#snippet children()}
            <Icon name="folder" size={12} />
            <span>Reveal</span>
          {/snippet}
        </Btn>
      {/if}
      <Btn size="sm" variant="primary" disabled={loading} onclick={generate}>
        {#snippet children()}
          <Icon name={loading ? "loader" : "sparkles"} size={12} />
          <span>{loading ? "Generating…" : generated ? "Regenerate" : "Generate"}</span>
        {/snippet}
      </Btn>
    {/snippet}
  </PanelHeader>

  <div class="tabs">
    {#each (["brag", "perf-review"] as ReviewType[]) as t}
      <button
        class="tab"
        class:active={reviewType === t}
        onclick={() => switchType(t)}
      >
        {TAB_META[t].label}
        <span class="tab-meta">{TAB_META[t].meta}</span>
      </button>
    {/each}
    <div class="tabs-spacer"></div>
  </div>

  <div class="controls">
    <div class="range">
      <DateTimeField
        bind:value={afterDate}
        mode="date"
        onchange={() => { report = cache[cacheKey(reviewType, afterDate, beforeDate)] ?? null; generated = !!report; }}
      />
      <span class="range-arrow">→</span>
      <DateTimeField
        bind:value={beforeDate}
        mode="date"
        onchange={() => { report = cache[cacheKey(reviewType, afterDate, beforeDate)] ?? null; generated = !!report; }}
      />
    </div>
    <div class="presets">
      {#each [["last-month", "Last month"], ["last-quarter", "Last quarter"], ["last-6-months", "Last 6 months"]] as [value, label] (value)}
        <button class="preset" onclick={() => setPreset(value)}>{label}</button>
      {/each}
    </div>
  </div>

  <div class="scroll">
    {#if !generated}
      <div class="cta">
        <div class="cta-eyebrow">{TAB_META[reviewType].eyebrow}</div>
        <h1 class="cta-title">{titleText}</h1>
        <div class="cta-meta">{afterDate} → {beforeDate}</div>
        <div class="cta-btn">
          <Btn variant="primary" onclick={generate}>
            {#snippet children()}
              <Icon name="sparkles" size={14} />
              <span>Generate {reviewType === "brag" ? "brag doc" : "perf review"}</span>
            {/snippet}
          </Btn>
        </div>
      </div>
    {:else}
      <ReportView
        eyebrow={TAB_META[reviewType].eyebrow}
        title={titleText}
        meta={metaText}
        loading={loading}
        error={error}
        empty={!!report && report.activity_count === 0}
        emptyLabel="No activities found for this period."
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
  .tabs {
    display: flex;
    gap: 2px;
    padding: 8px 20px 0;
    border-bottom: 1px solid var(--border);
    background: var(--ink-1);
  }
  .tab {
    padding: 8px 12px;
    border: none;
    background: transparent;
    color: var(--fg-3);
    font-size: 13px;
    cursor: pointer;
    position: relative;
    font-weight: 500;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
    font-family: var(--font-sans);
    transition: color var(--dur-1) var(--ease-std);
  }
  .tab.active {
    color: var(--fg-1);
    border-bottom-color: var(--accent);
  }
  .tab:hover:not(.active) { color: var(--fg-2); }
  .tab-meta {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-4);
    margin-left: 6px;
  }
  .tabs-spacer { flex: 1; }

  .controls {
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 10px 20px;
    border-bottom: 1px solid var(--border);
    background: var(--ink-1);
    flex-wrap: wrap;
  }
  .range {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .range-arrow {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-4);
  }
  .presets { display: flex; gap: 6px; }
  .preset {
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-1);
    padding: 3px 8px;
    color: var(--fg-2);
    font-family: var(--font-mono);
    font-size: 10px;
    cursor: pointer;
    transition: background var(--dur-1) var(--ease-std);
  }
  .preset:hover { background: var(--ink-3); color: var(--fg-1); }

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
