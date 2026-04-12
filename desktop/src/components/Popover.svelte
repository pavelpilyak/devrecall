<script lang="ts">
  import { api } from "../lib/api";
  import { apiStatus } from "../lib/stores";
  import Markdown from "./Markdown.svelte";

  let message = $state("");
  let chatHistory = $state<{ role: string; content: string }[]>([]);
  let loading = $state(false);
  let error = $state("");

  async function sendMessage() {
    if (!message.trim() || loading) return;

    const userMessage = message.trim();
    message = "";
    error = "";
    loading = true;

    chatHistory = [...chatHistory, { role: "user", content: userMessage }];

    try {
      const resp = await api.chat(
        userMessage,
        chatHistory.slice(0, -1) // history before this message
      );
      chatHistory = [
        ...chatHistory,
        { role: "assistant", content: resp.response },
      ];
    } catch (e) {
      error = e instanceof Error ? e.message : "Chat failed";
    } finally {
      loading = false;
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }

  // Quick actions
  let standupText = $state("");
  let standupLoading = $state(false);

  async function copyStandup() {
    standupLoading = true;
    try {
      const resp = await api.standup();
      standupText = resp.report;
      await navigator.clipboard.writeText(resp.report);
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to generate standup";
    } finally {
      standupLoading = false;
    }
  }
</script>

<div class="flex flex-col h-screen">
  <!-- Header -->
  <header class="flex items-center justify-between px-4 py-3 border-b border-zinc-200 dark:border-zinc-700">
    <h1 class="text-base font-semibold">DevRecall</h1>
    <div class="flex items-center gap-1">
      {#if $apiStatus}
        <span class="w-2 h-2 rounded-full bg-green-500" title="Connected"></span>
      {/if}
    </div>
  </header>

  <!-- Chat messages -->
  <div class="flex-1 overflow-y-auto px-4 py-3 space-y-3">
    {#if chatHistory.length === 0}
      <!-- Empty state with quick actions -->
      <div class="space-y-4 pt-4">
        <p class="text-sm text-zinc-500 dark:text-zinc-400">Ask about your work history, or use a quick action:</p>

        <div class="grid grid-cols-2 gap-2">
          <button
            onclick={copyStandup}
            disabled={standupLoading}
            class="text-left p-3 rounded-lg border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
          >
            <div class="text-sm font-medium">Copy Standup</div>
            <div class="text-xs text-zinc-500">Yesterday's report</div>
          </button>

          <button
            onclick={() => { message = "What did I work on this week?"; sendMessage(); }}
            class="text-left p-3 rounded-lg border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
          >
            <div class="text-sm font-medium">Weekly Summary</div>
            <div class="text-xs text-zinc-500">This week's highlights</div>
          </button>
        </div>
      </div>
    {:else}
      {#each chatHistory as msg}
        <div class="text-sm {msg.role === 'user' ? 'text-right' : ''}">
          <div class="inline-block max-w-[85%] px-3 py-2 rounded-lg {msg.role === 'user'
            ? 'bg-devrecall-600 text-white'
            : 'bg-zinc-100 dark:bg-zinc-800'}">
            {#if msg.role === "user"}
              <p class="whitespace-pre-wrap">{msg.content}</p>
            {:else}
              <Markdown content={msg.content} class="text-sm" />
            {/if}
          </div>
        </div>
      {/each}

      {#if loading}
        <div class="text-sm">
          <div class="inline-block px-3 py-2 rounded-lg bg-zinc-100 dark:bg-zinc-800 text-zinc-500">
            Thinking...
          </div>
        </div>
      {/if}
    {/if}

    {#if error}
      <div class="text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">
        {error}
      </div>
    {/if}

    {#if standupText}
      <div class="text-sm text-green-600 dark:text-green-400 px-3 py-2 bg-green-50 dark:bg-green-900/20 rounded-lg">
        Standup copied to clipboard!
      </div>
    {/if}
  </div>

  <!-- Input -->
  <div class="border-t border-zinc-200 dark:border-zinc-700 px-4 py-3">
    <div class="flex gap-2">
      <input
        type="text"
        bind:value={message}
        onkeydown={handleKeydown}
        placeholder="Ask about your work..."
        disabled={loading}
        class="flex-1 px-3 py-2 text-sm rounded-lg border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-devrecall-500
               disabled:opacity-50"
      />
      <button
        onclick={sendMessage}
        disabled={loading || !message.trim()}
        class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
               hover:bg-devrecall-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        Send
      </button>
    </div>
  </div>
</div>
