<script lang="ts">
  import { api } from "../lib/api";
  import { buildLogRequest } from "../lib/log";

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

  // Focus the textarea when this view becomes visible (called from App.svelte
  // via the `focus` prop / tray menu event).
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
      // Reset form for the next entry.
      text = "";
      tags = "";
      people = "";
      at = "";
      showAdvanced = false;
      // Clear the "saved" pill after a few seconds.
      if (savedTimer) clearTimeout(savedTimer);
      savedTimer = setTimeout(() => {
        savedTitle = "";
      }, 3000);
      // Re-focus for rapid entry.
      textarea?.focus();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to log event";
    } finally {
      saving = false;
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    // Cmd/Ctrl + Enter submits.
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      submit();
    }
  }
</script>

<div class="flex flex-col h-full">
  <!-- Header -->
  <div class="px-4 py-3 border-b border-zinc-200 dark:border-zinc-700">
    <h2 class="text-sm font-semibold">Log Event</h2>
    <p class="text-xs text-zinc-500 dark:text-zinc-400 mt-0.5">
      Capture in-person chats, calls, decisions — anything the collectors miss.
    </p>
  </div>

  <!-- Form -->
  <div class="flex-1 overflow-y-auto px-4 py-3 space-y-3">
    <div>
      <label for="log-text" class="sr-only">Event description</label>
      <textarea
        id="log-text"
        bind:this={textarea}
        bind:value={text}
        onkeydown={handleKeydown}
        placeholder="Talked to mobile team about the API contract…"
        rows="4"
        class="w-full text-sm px-3 py-2 rounded-md border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-devrecall-500
               resize-none"
      ></textarea>
    </div>

    <button
      type="button"
      onclick={() => (showAdvanced = !showAdvanced)}
      class="text-xs text-zinc-500 dark:text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-300"
    >
      {showAdvanced ? "− Hide" : "+ Tags, people, time"}
    </button>

    {#if showAdvanced}
      <div class="space-y-2">
        <div>
          <label for="log-tags" class="block text-xs text-zinc-500 dark:text-zinc-400 mb-1">
            Tags (comma-separated)
          </label>
          <input
            id="log-tags"
            type="text"
            bind:value={tags}
            placeholder="decision, deploy"
            class="w-full text-sm px-3 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
                   bg-white dark:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-devrecall-500"
          />
        </div>

        <div>
          <label for="log-people" class="block text-xs text-zinc-500 dark:text-zinc-400 mb-1">
            People (names or emails, comma-separated)
          </label>
          <input
            id="log-people"
            type="text"
            bind:value={people}
            placeholder="anna@example.com, bob"
            class="w-full text-sm px-3 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
                   bg-white dark:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-devrecall-500"
          />
        </div>

        <div>
          <label for="log-at" class="block text-xs text-zinc-500 dark:text-zinc-400 mb-1">
            When (default: now)
          </label>
          <input
            id="log-at"
            type="datetime-local"
            bind:value={at}
            class="w-full text-sm px-3 py-1.5 rounded-md border border-zinc-300 dark:border-zinc-600
                   bg-white dark:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-devrecall-500"
          />
        </div>
      </div>
    {/if}

    {#if error}
      <div class="text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">
        {error}
      </div>
    {/if}

    {#if savedTitle}
      <div class="text-sm text-green-700 dark:text-green-400 px-3 py-2 bg-green-50 dark:bg-green-900/20 rounded-lg">
        Logged: {savedTitle}
      </div>
    {/if}
  </div>

  <!-- Footer -->
  <div class="border-t border-zinc-200 dark:border-zinc-700 px-4 py-3 flex items-center gap-2">
    <span class="text-xs text-zinc-400 dark:text-zinc-500">⌘↵ to submit</span>
    <div class="flex-1"></div>
    <button
      onclick={submit}
      disabled={saving || !text.trim()}
      class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
             hover:bg-devrecall-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
    >
      {saving ? "Logging…" : "Log Event"}
    </button>
  </div>
</div>
