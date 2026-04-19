<script lang="ts">
  import { api } from "../lib/api";
  import { buildLogRequest } from "../lib/log";
  import { lastSyncAt } from "../lib/stores";
  import PanelHeader from "../components/ui/PanelHeader.svelte";
  import Btn from "../components/ui/Btn.svelte";
  import Icon from "../components/ui/Icon.svelte";
  import Kbd from "../components/ui/Kbd.svelte";
  import DateTimeField from "../components/ui/DateTimeField.svelte";

  let text = $state("");
  let tags = $state("");
  let people = $state("");
  let at = $state("");
  let showAdvanced = $state(false);

  let saving = $state(false);
  let error = $state("");
  let savedTitle = $state("");
  let savedTimer: ReturnType<typeof setTimeout> | null = null;

  let textarea = $state<HTMLTextAreaElement | null>(null);

  export function focus() {
    textarea?.focus();
  }

  async function submit() {
    if (saving) return;
    error = "";

    let req;
    try {
      req = buildLogRequest({ text, at, tags, people });
    } catch (e) {
      error = e instanceof Error ? e.message : "Invalid input";
      return;
    }

    saving = true;
    try {
      const resp = await api.log(req);
      savedTitle = resp.title;
      lastSyncAt.set(Date.now());
      text = "";
      tags = "";
      people = "";
      at = "";
      showAdvanced = false;
      if (savedTimer) clearTimeout(savedTimer);
      savedTimer = setTimeout(() => { savedTitle = ""; }, 3000);
      textarea?.focus();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to log event";
    } finally {
      saving = false;
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      submit();
    }
  }
</script>

<div class="route-body">
  <PanelHeader title="Log" meta="capture things the collectors miss" />

  <div class="scroll">
    <div class="form">
      <label class="field">
        <span class="field-label">What happened?</span>
        <textarea
          bind:this={textarea}
          bind:value={text}
          onkeydown={handleKeydown}
          placeholder="Talked to mobile team about the API contract…"
          rows="6"
        ></textarea>
      </label>

      <button
        type="button"
        class="advanced-toggle"
        onclick={() => (showAdvanced = !showAdvanced)}
      >
        <Icon name={showAdvanced ? "chevron-down" : "chevron-right"} size={12} />
        <span>{showAdvanced ? "Hide" : "Add"} tags, people, time</span>
      </button>

      {#if showAdvanced}
        <div class="advanced">
          <label class="field">
            <span class="field-label">Tags</span>
            <input type="text" bind:value={tags} placeholder="decision, deploy" />
          </label>
          <label class="field">
            <span class="field-label">People</span>
            <input type="text" bind:value={people} placeholder="anna@example.com, bob" />
          </label>
          <div class="field">
            <span class="field-label">When</span>
            <DateTimeField bind:value={at} placeholder="now" />
          </div>
        </div>
      {/if}

      {#if error}
        <div class="error-box">{error}</div>
      {/if}

      {#if savedTitle}
        <div class="ok-box">
          <Icon name="check" size={12} />
          <span>Logged: {savedTitle}</span>
        </div>
      {/if}
    </div>
  </div>

  <div class="foot">
    <span class="hint">
      <Kbd>{#snippet children()}⌘{/snippet}</Kbd>
      <Kbd>{#snippet children()}↵{/snippet}</Kbd>
      <span>to submit</span>
    </span>
    <span class="spacer"></span>
    <Btn variant="primary" disabled={saving || !text.trim()} onclick={submit}>
      {#snippet children()}
        <Icon name={saving ? "loader" : "edit-3"} size={12} />
        <span>{saving ? "Logging…" : "Log event"}</span>
      {/snippet}
    </Btn>
  </div>
</div>

<style>
  .route-body { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
  .scroll { flex: 1; overflow: auto; background: var(--ink-1); }

  .form {
    margin: 32px 0;
    padding: 0 40px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .field-label {
    font-family: var(--font-mono);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: var(--tracking-caps);
    color: var(--fg-3);
    font-weight: 500;
  }
  .field textarea,
  .field input {
    background: var(--ink-2);
    border: 1px solid var(--border-strong);
    border-radius: var(--r-2);
    padding: 10px 12px;
    color: var(--fg-1);
    font-family: var(--font-sans);
    font-size: 13px;
    outline: none;
    resize: vertical;
    line-height: 1.5;
  }
  .field textarea:focus,
  .field input:focus {
    border-color: var(--accent-line);
    box-shadow: 0 0 0 3px var(--accent-wash);
  }
  .field textarea::placeholder,
  .field input::placeholder { color: var(--fg-4); }

  .advanced-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: transparent;
    border: none;
    color: var(--fg-3);
    font-size: 12px;
    cursor: pointer;
    padding: 0;
    align-self: flex-start;
  }
  .advanced-toggle:hover { color: var(--fg-1); }

  .advanced {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 12px;
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-2);
  }

  .error-box {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--danger);
    background: var(--danger-wash);
    border: 1px solid rgba(255, 107, 107, 0.2);
    border-radius: var(--r-2);
    padding: 10px 12px;
  }
  .ok-box {
    display: flex;
    align-items: center;
    gap: 8px;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--ok);
    background: rgba(124, 240, 168, 0.08);
    border: 1px solid var(--accent-line);
    border-radius: var(--r-2);
    padding: 10px 12px;
  }

  .foot {
    padding: 12px 20px;
    border-top: 1px solid var(--border);
    background: var(--ink-1);
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .hint {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-4);
  }
  .spacer { flex: 1; }
</style>
