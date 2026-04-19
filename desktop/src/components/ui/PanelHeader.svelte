<script lang="ts">
  let {
    title,
    meta,
    actions,
  } = $props<{ title: string; meta?: string; actions?: any }>();
</script>

<div class="panel-header">
  <div class="title">{title}</div>
  {#if meta}<div class="meta">{meta}</div>{/if}
  <div class="spacer"></div>
  {#if actions}<div class="actions">{@render actions()}</div>{/if}
</div>

<style>
  .panel-header {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 20px;
    border-bottom: 1px solid var(--border);
    min-height: 48px;
    box-sizing: border-box;
    flex-wrap: nowrap;
    flex-shrink: 0;
    background: var(--ink-1);
  }
  .title {
    font-size: 15px;
    font-weight: 600;
    color: var(--fg-1);
    letter-spacing: -0.012em;
    white-space: nowrap;
  }
  .meta {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    white-space: nowrap;
  }
  .spacer { flex: 1; }
  .actions {
    display: flex;
    gap: 8px;
    flex-shrink: 0;
    align-items: center;
  }
</style>
