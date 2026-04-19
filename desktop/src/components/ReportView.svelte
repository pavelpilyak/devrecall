<script lang="ts">
  import Markdown from "./Markdown.svelte";
  import SyncDot from "./ui/SyncDot.svelte";

  interface Props {
    eyebrow: string;
    title: string;
    meta?: string;
    loading?: boolean;
    error?: string;
    empty?: boolean;
    emptyLabel?: string;
    content?: string;
  }

  let {
    eyebrow,
    title,
    meta,
    loading = false,
    error = "",
    empty = false,
    emptyLabel = "No activities for this period.",
    content = "",
  }: Props = $props();
</script>

<div class="report">
  <div class="hdr">
    <div class="eyebrow">{eyebrow}</div>
    <h1 class="title">{title}</h1>
    {#if meta}
      <div class="meta">
        <SyncDot status="ok" />
        <span>{meta}</span>
      </div>
    {/if}
  </div>

  {#if loading}
    <div class="skeleton">
      {#each [80, 92, 70, 85, 60] as w, i (i)}
        <div class="sk-line" style="width: {w}%; animation-delay: {i * 0.1}s"></div>
      {/each}
    </div>
  {:else if error}
    <div class="error-box">{error}</div>
  {:else if empty}
    <div class="empty-state">{emptyLabel}</div>
  {:else if content}
    <div class="body">
      <Markdown content={content} variant="serif" />
    </div>
  {/if}
</div>

<style>
  .report {
    margin: 40px 0;
    padding: 0 40px;
    box-sizing: border-box;
    width: 100%;
  }
  .eyebrow {
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: var(--tracking-caps);
    text-transform: uppercase;
    color: var(--fg-3);
    font-weight: 500;
  }
  .title {
    margin: 6px 0 8px;
    font-size: 28px;
    font-weight: 600;
    letter-spacing: -0.018em;
    color: var(--fg-1);
    line-height: 1.2;
  }
  .meta {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .body {
    margin-top: 32px;
    animation: drFadeIn 240ms var(--ease-out);
  }
  @keyframes drFadeIn {
    from { opacity: 0; transform: translateY(2px); }
    to { opacity: 1; transform: none; }
  }

  .skeleton {
    margin-top: 32px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .sk-line {
    height: 14px;
    background: var(--ink-3);
    border-radius: var(--r-1);
    animation: skPulse 1.5s ease-in-out infinite;
  }
  @keyframes skPulse {
    0%, 100% { opacity: 0.6; }
    50% { opacity: 1; }
  }

  .error-box {
    margin-top: 24px;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--danger);
    background: var(--danger-wash);
    border: 1px solid rgba(255, 107, 107, 0.2);
    border-radius: var(--r-2);
    padding: 10px 12px;
    white-space: pre-wrap;
  }
  .empty-state {
    margin-top: 40px;
    text-align: center;
    font-size: 13px;
    color: var(--fg-3);
  }

</style>
