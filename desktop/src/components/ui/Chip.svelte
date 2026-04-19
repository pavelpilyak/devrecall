<script lang="ts">
  type Variant = "default" | "accent" | "warn" | "danger" | "info";
  let {
    variant = "default",
    onclick,
    children,
  } = $props<{ variant?: Variant; onclick?: (e: MouseEvent) => void; children?: any }>();
</script>

<span
  class="chip"
  class:accent={variant === "accent"}
  class:warn={variant === "warn"}
  class:danger={variant === "danger"}
  class:info={variant === "info"}
  class:clickable={!!onclick}
  {onclick}
  role={onclick ? "button" : undefined}
>
  {@render children?.()}
</span>

<style>
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    height: 20px;
    padding: 0 6px;
    border-radius: var(--r-1);
    background: var(--ink-3);
    color: var(--fg-2);
    font-family: var(--font-mono);
    font-size: 11px;
    letter-spacing: var(--tracking-mono);
    border: 1px solid var(--border);
  }
  .clickable { cursor: pointer; }
  .accent { background: var(--accent-wash); color: var(--mint-200); border-color: var(--accent-line); }
  .warn { background: var(--warn-wash); color: var(--warn); border-color: rgba(232,200,114,0.3); }
  .danger { background: var(--danger-wash); color: var(--danger); border-color: rgba(255,107,107,0.3); }
  .info { background: var(--info-wash); color: var(--info); border-color: rgba(106,169,255,0.3); }
</style>
