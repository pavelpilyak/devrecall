<script lang="ts">
  import { tick } from "svelte";
  import Icon from "./ui/Icon.svelte";
  import Kbd from "./ui/Kbd.svelte";
  import SyncDot from "./ui/SyncDot.svelte";

  type Command = {
    group: string;
    cmd: string;
    icon: string;
    kbd?: string[];
    run: () => void;
  };

  let {
    open = false,
    commands,
    onClose,
  } = $props<{ open?: boolean; commands: Command[]; onClose: () => void }>();

  let q = $state("");
  let sel = $state(0);
  let inputEl: HTMLInputElement | undefined = $state();

  const filtered = $derived.by<Command[]>(() => {
    const ql = q.toLowerCase();
    return (commands as Command[]).filter(
      (c: Command) => !ql || c.cmd.toLowerCase().includes(ql) || c.group.toLowerCase().includes(ql),
    );
  });

  const groups = $derived.by(() => {
    const out: Record<string, Command[]> = {};
    for (const c of filtered) {
      (out[c.group] = out[c.group] || []).push(c);
    }
    return out;
  });

  $effect(() => {
    if (open) {
      q = "";
      sel = 0;
      void tick().then(() => inputEl?.focus());
    }
  });

  $effect(() => {
    if (!open) return;
    function onWindowKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        sel = Math.min(sel + 1, filtered.length - 1);
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        sel = Math.max(sel - 1, 0);
      } else if (e.key === "Enter" && filtered[sel]) {
        e.preventDefault();
        run(filtered[sel]);
      }
    }
    window.addEventListener("keydown", onWindowKey);
    return () => window.removeEventListener("keydown", onWindowKey);
  });

  function run(c: Command) {
    c.run();
    onClose();
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="scrim" onclick={onClose}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="palette" onclick={(e) => e.stopPropagation()}>
      <div class="search-row">
        <Icon name="search" size={14} />
        <input
          bind:this={inputEl}
          bind:value={q}
          oninput={() => (sel = 0)}
          placeholder="Search commands, sources, reports…"
        />
        <Kbd>{#snippet children()}esc{/snippet}</Kbd>
      </div>
      <div class="list">
        {#each Object.entries(groups) as [group, items] (group)}
          <div class="group">
            <div class="group-head">{group}</div>
            {#each items as c (c.cmd)}
              {@const idx = filtered.indexOf(c)}
              {@const isSel = idx === sel}
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <div
                class="item"
                class:selected={isSel}
                onmouseenter={() => (sel = idx)}
                onclick={() => run(c)}
              >
                <Icon name={c.icon} size={14} />
                <span class="cmd">{c.cmd}</span>
                {#if c.kbd}
                  <span class="kbds">
                    {#each c.kbd as k, i (i)}
                      <Kbd>{#snippet children()}{k}{/snippet}</Kbd>
                    {/each}
                  </span>
                {/if}
              </div>
            {/each}
          </div>
        {/each}
        {#if filtered.length === 0}
          <div class="empty">No matching commands.</div>
        {/if}
      </div>
      <div class="footer">
        <Kbd>{#snippet children()}↑{/snippet}</Kbd>
        <Kbd>{#snippet children()}↓{/snippet}</Kbd>
        <span>navigate</span>
        <span class="sep"></span>
        <Kbd>{#snippet children()}↵{/snippet}</Kbd>
        <span>select</span>
        <span class="spacer"></span>
        <SyncDot status="ok" />
        <span>local</span>
      </div>
    </div>
  </div>
{/if}

<style>
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 200;
    background: rgba(8, 9, 11, 0.5);
    backdrop-filter: blur(4px);
    -webkit-backdrop-filter: blur(4px);
    display: flex;
    justify-content: center;
    padding-top: 14vh;
  }
  .palette {
    width: 540px;
    max-height: 70vh;
    background: var(--ink-3);
    border: 1px solid var(--border-strong);
    border-radius: var(--r-4);
    box-shadow: 0 1px 0 rgba(255, 255, 255, 0.05) inset, 0 20px 60px -10px rgba(0, 0, 0, 0.7);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    animation: drFadeIn 160ms var(--ease-out);
  }
  @keyframes drFadeIn {
    from { opacity: 0; transform: translateY(2px); }
    to { opacity: 1; transform: none; }
  }
  .search-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border);
    color: var(--fg-3);
  }
  .search-row input {
    flex: 1;
    background: transparent;
    border: none;
    outline: none;
    color: var(--fg-1);
    font-family: var(--font-sans);
    font-size: 14px;
  }
  .search-row input::placeholder { color: var(--fg-4); }
  .list { overflow: auto; padding: 6px; }
  .group { margin-bottom: 6px; }
  .group-head {
    padding: 6px 10px 4px;
    font-family: var(--font-mono);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: var(--tracking-caps);
    color: var(--fg-3);
    font-weight: 500;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 10px;
    border-radius: var(--r-2);
    cursor: pointer;
    color: var(--fg-1);
  }
  .item.selected {
    background: var(--accent-wash);
    color: var(--mint-200);
  }
  .cmd { font-size: 13px; flex: 1; }
  .kbds { display: flex; gap: 4px; }
  .empty {
    padding: 20px;
    text-align: center;
    color: var(--fg-3);
    font-size: 12px;
  }
  .footer {
    padding: 8px 14px;
    border-top: 1px solid var(--border);
    display: flex;
    align-items: center;
    gap: 6px;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-4);
  }
  .sep { width: 10px; }
  .spacer { flex: 1; }
</style>
