<script lang="ts">
  import Icon from "./ui/Icon.svelte";
  import SyncDot from "./ui/SyncDot.svelte";

  type NavItem = { id: string; label: string; icon: string; kbd?: string };

  let {
    route,
    setRoute,
    items,
    llm,
  } = $props<{
    route: string;
    setRoute: (id: string) => void;
    items: NavItem[];
    llm?: {
      name: string;
      detail: string;
      status?: "ok" | "warn" | "error";
      onClick?: () => void;
      title?: string;
    };
  }>();
</script>

<aside class="sidebar">
  <nav class="nav">
    {#each items as it (it.id)}
      <button
        type="button"
        class="nav-item"
        class:active={route === it.id}
        onclick={() => setRoute(it.id)}
      >
        <span class="nav-bar"></span>
        <Icon name={it.icon} size={14} />
        <span class="label">{it.label}</span>
        {#if it.kbd}<span class="kbd-hint">{it.kbd}</span>{/if}
      </button>
    {/each}
  </nav>

  <div class="spacer-v"></div>

  {#if llm}
    {#if llm.onClick}
      <button
        type="button"
        class="llm llm-btn"
        title={llm.title}
        onclick={() => llm.onClick?.()}
      >
        <Icon name="cpu" size={14} />
        <div class="llm-text">
          <div class="llm-name">{llm.name}</div>
          <div class="llm-detail">{llm.detail}</div>
        </div>
        <SyncDot status={llm.status ?? "ok"} />
      </button>
    {:else}
      <div class="llm" title={llm.title}>
        <Icon name="cpu" size={14} />
        <div class="llm-text">
          <div class="llm-name">{llm.name}</div>
          <div class="llm-detail">{llm.detail}</div>
        </div>
        <SyncDot status={llm.status ?? "ok"} />
      </div>
    {/if}
  {/if}
</aside>

<style>
  .sidebar {
    width: 212px;
    flex-shrink: 0;
    background: var(--ink-1);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    padding: 10px 8px;
    gap: 16px;
    overflow: hidden;
    box-sizing: border-box;
  }
  .nav { display: flex; flex-direction: column; gap: 2px; }
  .nav-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 10px;
    border-radius: 5px;
    color: var(--fg-2);
    font-size: 13px;
    cursor: pointer;
    background: transparent;
    border: none;
    width: 100%;
    text-align: left;
    transition: background var(--dur-1) var(--ease-std), color var(--dur-1) var(--ease-std);
    font-family: var(--font-sans);
  }
  .nav-item:hover { background: var(--ink-3); color: var(--fg-1); }
  .nav-item.active { background: var(--ink-3); color: var(--fg-1); }
  .nav-item.active .nav-bar { background: var(--accent); }
  .nav-bar {
    width: 2px;
    height: 14px;
    background: transparent;
    border-radius: 2px;
    margin-left: -4px;
    display: inline-block;
  }
  .label { flex: 1; }
  .kbd-hint {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-4);
    letter-spacing: var(--tracking-mono);
  }
  .spacer-v { flex: 1; }
  .llm {
    padding: 8px 10px;
    border-radius: 5px;
    background: var(--ink-2);
    border: 1px solid var(--border);
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--accent);
  }
  .llm-btn {
    width: 100%;
    text-align: left;
    font: inherit;
    cursor: pointer;
    transition: background var(--dur-1) var(--ease-std), border-color var(--dur-1) var(--ease-std);
  }
  .llm-btn:hover { background: var(--ink-3); border-color: var(--border-strong, var(--border)); }
  .llm-btn:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  .llm-text { flex: 1; min-width: 0; }
  .llm-name {
    font-size: 11px;
    color: var(--fg-1);
    font-weight: 500;
  }
  .llm-detail {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-3);
    word-break: break-word;
    line-height: 1.35;
  }
</style>
