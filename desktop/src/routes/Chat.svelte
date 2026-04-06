<script lang="ts">
  import { api } from "../lib/api";

  let message = $state("");
  let chatHistory = $state<{ role: string; content: string }[]>([]);
  let loading = $state(false);
  let error = $state("");
  let messagesEl: HTMLDivElement;

  async function sendMessage() {
    if (!message.trim() || loading) return;

    const userMessage = message.trim();
    message = "";
    error = "";
    loading = true;

    chatHistory = [...chatHistory, { role: "user", content: userMessage }];
    scrollToBottom();

    try {
      const resp = await api.chat(
        userMessage,
        chatHistory.slice(0, -1)
      );
      chatHistory = [
        ...chatHistory,
        { role: "assistant", content: resp.response },
      ];
    } catch (e) {
      error = e instanceof Error ? e.message : "Chat failed";
    } finally {
      loading = false;
      scrollToBottom();
    }
  }

  function scrollToBottom() {
    setTimeout(() => {
      if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight;
    }, 50);
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }

  function clearHistory() {
    chatHistory = [];
    error = "";
  }

  async function copyLastResponse() {
    const last = chatHistory.findLast(m => m.role === "assistant");
    if (last) {
      await navigator.clipboard.writeText(last.content);
    }
  }
</script>

<div class="flex flex-col h-full">
  <!-- Messages -->
  <div class="flex-1 overflow-y-auto px-4 py-3 space-y-3" bind:this={messagesEl}>
    {#if chatHistory.length === 0}
      <div class="flex items-center justify-center h-full">
        <div class="text-center space-y-3">
          <div class="text-3xl">&#128172;</div>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">Ask anything about your work history.</p>
          <div class="flex flex-wrap gap-2 justify-center">
            {#each [
              "What did I do yesterday?",
              "How many PRs did I review this month?",
              "Summarize my work this week",
            ] as suggestion}
              <button
                onclick={() => { message = suggestion; sendMessage(); }}
                class="text-xs px-3 py-1.5 rounded-full border border-zinc-300 dark:border-zinc-600
                       hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
              >
                {suggestion}
              </button>
            {/each}
          </div>
        </div>
      </div>
    {:else}
      {#each chatHistory as msg}
        <div class="text-sm {msg.role === 'user' ? 'text-right' : ''}">
          <div class="inline-block max-w-[80%] px-3 py-2 rounded-lg {msg.role === 'user'
            ? 'bg-devrecall-600 text-white'
            : 'bg-zinc-100 dark:bg-zinc-800'}">
            <p class="whitespace-pre-wrap">{msg.content}</p>
          </div>
        </div>
      {/each}

      {#if loading}
        <div class="text-sm">
          <div class="inline-block px-3 py-2 rounded-lg bg-zinc-100 dark:bg-zinc-800 text-zinc-500 animate-pulse">
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
      {#if chatHistory.length > 0}
        <button
          onclick={copyLastResponse}
          class="px-3 py-2 text-sm rounded-lg border border-zinc-300 dark:border-zinc-600
                 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
          title="Copy last response"
        >
          Copy
        </button>
        <button
          onclick={clearHistory}
          class="px-3 py-2 text-sm rounded-lg border border-zinc-300 dark:border-zinc-600
                 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
          title="Clear chat"
        >
          Clear
        </button>
      {/if}
    </div>
  </div>
</div>
