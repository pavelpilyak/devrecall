<script lang="ts">
  import { SOURCE_HUES } from "./tokens";

  let { source, size = 8 } = $props<{ source: string; size?: number }>();
</script>

<span
  class="source-dot"
  style="--size:{size}px; background:{SOURCE_HUES[source] ?? 'var(--fg-3)'}"
></span>

<style>
  .source-dot {
    display: inline-block;
    width: var(--size);
    height: var(--size);
    border-radius: 50%;
    flex-shrink: 0;
  }
</style>
