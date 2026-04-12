<script lang="ts">
  import { marked } from "marked";
  import DOMPurify from "dompurify";

  interface Props {
    content: string;
    class?: string;
  }

  let { content, class: className = "" }: Props = $props();

  // Configure marked for sensible defaults.
  marked.setOptions({
    breaks: true,
    gfm: true,
  });

  let html = $derived(DOMPurify.sanitize(marked.parse(content || "") as string));
</script>

<div class="markdown {className}">
  {@html html}
</div>

<style>
  .markdown :global(h1) {
    font-size: 1.25rem;
    font-weight: 700;
    margin-top: 1rem;
    margin-bottom: 0.5rem;
  }
  .markdown :global(h2) {
    font-size: 1.125rem;
    font-weight: 600;
    margin-top: 0.75rem;
    margin-bottom: 0.375rem;
  }
  .markdown :global(h3) {
    font-size: 1rem;
    font-weight: 600;
    margin-top: 0.5rem;
    margin-bottom: 0.25rem;
  }
  .markdown :global(p) {
    margin-bottom: 0.5rem;
    line-height: 1.625;
  }
  .markdown :global(ul),
  .markdown :global(ol) {
    margin-left: 1.25rem;
    margin-bottom: 0.5rem;
  }
  .markdown :global(ul) {
    list-style-type: disc;
  }
  .markdown :global(ol) {
    list-style-type: decimal;
  }
  .markdown :global(li) {
    margin-bottom: 0.125rem;
    line-height: 1.5;
  }
  .markdown :global(code) {
    font-family: ui-monospace, monospace;
    font-size: 0.85em;
    background: rgba(127, 127, 127, 0.12);
    padding: 0.125rem 0.3rem;
    border-radius: 0.25rem;
  }
  .markdown :global(pre) {
    background: rgba(127, 127, 127, 0.1);
    padding: 0.75rem;
    border-radius: 0.375rem;
    overflow-x: auto;
    margin-bottom: 0.5rem;
  }
  .markdown :global(pre code) {
    background: none;
    padding: 0;
  }
  .markdown :global(blockquote) {
    border-left: 3px solid rgba(127, 127, 127, 0.3);
    padding-left: 0.75rem;
    margin-left: 0;
    margin-bottom: 0.5rem;
    color: rgba(127, 127, 127, 0.8);
  }
  .markdown :global(a) {
    color: #3b82f6;
    text-decoration: underline;
  }
  .markdown :global(a:hover) {
    color: #2563eb;
  }
  .markdown :global(strong) {
    font-weight: 600;
  }
  .markdown :global(hr) {
    border: none;
    border-top: 1px solid rgba(127, 127, 127, 0.2);
    margin: 0.75rem 0;
  }
  .markdown :global(table) {
    width: 100%;
    border-collapse: collapse;
    margin-bottom: 0.5rem;
    font-size: 0.875rem;
  }
  .markdown :global(th),
  .markdown :global(td) {
    border: 1px solid rgba(127, 127, 127, 0.2);
    padding: 0.375rem 0.5rem;
    text-align: left;
  }
  .markdown :global(th) {
    font-weight: 600;
    background: rgba(127, 127, 127, 0.06);
  }
  /* Remove bottom margin from last element */
  .markdown :global(*:last-child) {
    margin-bottom: 0;
  }
</style>
