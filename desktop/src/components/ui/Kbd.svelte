<script lang="ts">
  let { children } = $props<{ children?: any }>();
</script>

<kbd>{@render children?.()}</kbd>

<style>
  kbd {
    display: inline-flex;
    align-items: center;
    height: 18px;
    padding: 0 5px;
    margin: 0 1px;
    border-radius: var(--r-1);
    border: 1px solid var(--border-strong);
    background: var(--ink-3);
    color: var(--fg-2);
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 500;
    letter-spacing: var(--tracking-mono);
  }
</style>
