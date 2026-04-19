<script lang="ts">
  type Variant = "default" | "primary" | "ghost";
  type Size = "sm" | "md" | "lg";

  let {
    variant = "default",
    size = "md",
    disabled,
    title,
    type = "button",
    onclick,
    children,
  } = $props<{
    variant?: Variant;
    size?: Size;
    disabled?: boolean;
    title?: string;
    type?: "button" | "submit" | "reset";
    onclick?: (e: MouseEvent) => void;
    children?: any;
  }>();
</script>

<button
  class="btn"
  class:primary={variant === "primary"}
  class:ghost={variant === "ghost"}
  class:sm={size === "sm"}
  class:lg={size === "lg"}
  {disabled}
  {title}
  {type}
  {onclick}
>
  {@render children?.()}
</button>

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 28px;
    padding: 0 10px;
    border-radius: var(--r-2);
    border: 1px solid var(--border-strong);
    background: var(--ink-3);
    color: var(--fg-1);
    font-family: var(--font-sans);
    font-size: var(--t-sm);
    font-weight: 500;
    letter-spacing: var(--tracking-tight);
    cursor: pointer;
    transition:
      background var(--dur-1) var(--ease-std),
      border-color var(--dur-1) var(--ease-std),
      transform var(--dur-1) var(--ease-std);
    white-space: nowrap;
  }
  .btn:hover { background: var(--ink-4); border-color: var(--ink-5); }
  .btn:active { transform: translateY(0.5px); background: var(--ink-2); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .primary {
    background: var(--accent);
    color: var(--accent-ink);
    border-color: transparent;
    box-shadow: 0 0 0 1px var(--accent-line);
  }
  .primary:hover { background: var(--accent-hover); }
  .primary:active { background: var(--accent-press); }
  .ghost {
    background: transparent;
    border-color: transparent;
    color: var(--fg-2);
  }
  .ghost:hover { background: var(--ink-3); color: var(--fg-1); }
  .sm { height: 24px; padding: 0 8px; font-size: var(--t-xs); }
  .lg { height: 34px; padding: 0 14px; font-size: var(--t-md); }
</style>
