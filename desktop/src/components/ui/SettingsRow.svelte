<script lang="ts">
  interface Props {
    title?: any;
    titleText?: string;
    meta?: string;
    right?: any;
  }
  let { title, titleText, meta, right }: Props = $props();
</script>

<div class="row">
  <div class="main">
    <div class="title">
      {#if title}{@render title()}{:else}{titleText}{/if}
    </div>
    {#if meta}<div class="meta">{meta}</div>{/if}
  </div>
  {#if right}<div class="right">{@render right()}</div>{/if}
</div>

<style>
  .row {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--hairline);
  }
  .row:last-child { border-bottom: none; }
  .main { flex: 1; min-width: 0; }
  .title {
    font-size: 13px;
    color: var(--fg-1);
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .meta {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    margin-top: 2px;
    word-break: break-all;
  }
  .right { flex-shrink: 0; display: flex; align-items: center; gap: 8px; }
</style>
