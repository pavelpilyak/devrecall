<script lang="ts">
  interface Props {
    title: string;
    desc?: string;
    children: any;
  }
  let { title, desc, children }: Props = $props();
</script>

<div class="section">
  <div class="head">
    <div class="title">{title}</div>
    {#if desc}<div class="desc">{desc}</div>{/if}
  </div>
  <div class="body">
    {@render children()}
  </div>
</div>

<style>
  .section { margin-bottom: 32px; }
  .head { padding: 0 0 10px; }
  .title {
    font-size: 14px;
    font-weight: 600;
    color: var(--fg-1);
  }
  .desc {
    font-size: 12px;
    color: var(--fg-3);
    margin-top: 2px;
    line-height: 1.5;
  }
  .body {
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-3);
    overflow: hidden;
  }
</style>
