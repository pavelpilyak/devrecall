<script lang="ts">
  import Icon from "./Icon.svelte";

  interface Props {
    value: string;
    placeholder?: string;
    mode?: "date" | "datetime";
    onchange?: (v: string) => void;
  }
  let {
    value = $bindable(""),
    placeholder = "now",
    mode = "datetime",
    onchange,
  }: Props = $props();

  function emit(v: string) {
    value = v;
    onchange?.(v);
  }

  let open = $state(false);
  let anchorEl: HTMLButtonElement | undefined = $state();
  let popoverEl: HTMLDivElement | undefined = $state();
  let alignRight = $state(false);

  const POPOVER_WIDTH = 256 + 20; // .popover width + padding + border

  $effect(() => {
    if (!open || !anchorEl) return;
    requestAnimationFrame(() => {
      if (!anchorEl) return;
      const rect = anchorEl.getBoundingClientRect();
      alignRight = rect.left + POPOVER_WIDTH + 8 > window.innerWidth;
    });
  });

  // Parse incoming value into parts; fall back to "now". Handles both
  // "YYYY-MM-DD" (parsed in local time to avoid TZ drift) and
  // "YYYY-MM-DDTHH:mm".
  function parse(v: string): { y: number; m: number; d: number; h: number; min: number } {
    if (v) {
      const dateOnly = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v);
      if (dateOnly) {
        return {
          y: parseInt(dateOnly[1], 10),
          m: parseInt(dateOnly[2], 10) - 1,
          d: parseInt(dateOnly[3], 10),
          h: 0,
          min: 0,
        };
      }
      const d = new Date(v);
      if (!isNaN(d.getTime())) {
        return { y: d.getFullYear(), m: d.getMonth(), d: d.getDate(), h: d.getHours(), min: d.getMinutes() };
      }
    }
    const n = new Date();
    return { y: n.getFullYear(), m: n.getMonth(), d: n.getDate(), h: n.getHours(), min: n.getMinutes() };
  }

  function serialize(y: number, m: number, d: number, h: number, min: number): string {
    const pad = (x: number) => String(x).padStart(2, "0");
    if (mode === "date") {
      return `${y}-${pad(m + 1)}-${pad(d)}`;
    }
    return `${y}-${pad(m + 1)}-${pad(d)}T${pad(h)}:${pad(min)}`;
  }

  const parsed = $derived(parse(value));

  // viewMonth tracks the calendar's visible month; resets each time the popover opens.
  let viewYear = $state(new Date().getFullYear());
  let viewMonth = $state(new Date().getMonth());

  // Hour/minute strings used for the two inputs (so users can type partial values).
  let hourStr = $state("00");
  let minuteStr = $state("00");

  function openPopover() {
    const p = parsed;
    viewYear = p.y;
    viewMonth = p.m;
    hourStr = String(p.h).padStart(2, "0");
    minuteStr = String(p.min).padStart(2, "0");
    open = true;
  }

  function closePopover() { open = false; }

  function selectDay(y: number, m: number, d: number) {
    const h = clampInt(hourStr, 0, 23, parsed.h);
    const mi = clampInt(minuteStr, 0, 59, parsed.min);
    emit(serialize(y, m, d, h, mi));
    hourStr = String(h).padStart(2, "0");
    minuteStr = String(mi).padStart(2, "0");
    if (mode === "date") open = false;
  }

  function clampInt(s: string, lo: number, hi: number, fallback: number): number {
    const n = parseInt(s, 10);
    if (Number.isNaN(n)) return fallback;
    return Math.min(hi, Math.max(lo, n));
  }

  function applyTime() {
    if (!value || mode === "date") return;
    const h = clampInt(hourStr, 0, 23, parsed.h);
    const mi = clampInt(minuteStr, 0, 59, parsed.min);
    emit(serialize(parsed.y, parsed.m, parsed.d, h, mi));
    hourStr = String(h).padStart(2, "0");
    minuteStr = String(mi).padStart(2, "0");
  }

  function preset(minsAgo: number) {
    const n = new Date(Date.now() - minsAgo * 60_000);
    emit(serialize(n.getFullYear(), n.getMonth(), n.getDate(), n.getHours(), n.getMinutes()));
    viewYear = n.getFullYear();
    viewMonth = n.getMonth();
    hourStr = String(n.getHours()).padStart(2, "0");
    minuteStr = String(n.getMinutes()).padStart(2, "0");
  }

  function datePreset(daysAgo: number) {
    const n = new Date();
    n.setDate(n.getDate() - daysAgo);
    emit(serialize(n.getFullYear(), n.getMonth(), n.getDate(), 0, 0));
    viewYear = n.getFullYear();
    viewMonth = n.getMonth();
    open = false;
  }

  function clear() { emit(""); }

  function prevMonth() {
    if (viewMonth === 0) { viewMonth = 11; viewYear -= 1; } else viewMonth -= 1;
  }
  function nextMonth() {
    if (viewMonth === 11) { viewMonth = 0; viewYear += 1; } else viewMonth += 1;
  }

  // Build the 6x7 grid for the current view, including leading/trailing padding days.
  type Cell = { y: number; m: number; d: number; inMonth: boolean };
  const cells = $derived.by<Cell[]>(() => {
    const first = new Date(viewYear, viewMonth, 1);
    // Monday-first: 0 for Mon, 6 for Sun.
    const lead = (first.getDay() + 6) % 7;
    const daysInMonth = new Date(viewYear, viewMonth + 1, 0).getDate();
    const out: Cell[] = [];
    // Leading pad from previous month
    for (let i = lead - 1; i >= 0; i--) {
      const prev = new Date(viewYear, viewMonth, -i);
      out.push({ y: prev.getFullYear(), m: prev.getMonth(), d: prev.getDate(), inMonth: false });
    }
    // Current month
    for (let d = 1; d <= daysInMonth; d++) {
      out.push({ y: viewYear, m: viewMonth, d, inMonth: true });
    }
    // Trailing pad to fill 6 rows (42 cells)
    while (out.length < 42) {
      const last = out[out.length - 1];
      const next = new Date(last.y, last.m, last.d + 1);
      out.push({ y: next.getFullYear(), m: next.getMonth(), d: next.getDate(), inMonth: false });
    }
    return out;
  });

  const monthLabel = $derived(new Date(viewYear, viewMonth, 1).toLocaleDateString(undefined, { month: "long", year: "numeric" }));
  const todayKey = (() => { const t = new Date(); return `${t.getFullYear()}-${t.getMonth()}-${t.getDate()}`; })();
  const selKey = $derived(value ? `${parsed.y}-${parsed.m}-${parsed.d}` : "");

  const displayLabel = $derived.by(() => {
    if (!value) return placeholder;
    if (mode === "date") {
      const d = new Date(parsed.y, parsed.m, parsed.d);
      return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
    }
    const d = new Date(value);
    if (isNaN(d.getTime())) return placeholder;
    return d.toLocaleString(undefined, {
      month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false,
    });
  });

  function onDocClick(e: MouseEvent) {
    if (!open) return;
    const t = e.target as Node;
    if (anchorEl?.contains(t)) return;
    if (popoverEl?.contains(t)) return;
    closePopover();
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape" && open) { e.stopPropagation(); closePopover(); }
  }
</script>

<svelte:document onclick={onDocClick} onkeydown={onKey} />

<div class="wrap">
  <button
    type="button"
    bind:this={anchorEl}
    class="trigger"
    class:empty={!value}
    onclick={() => (open ? closePopover() : openPopover())}
  >
    <Icon name="calendar" size={12} />
    <span>{displayLabel}</span>
    {#if value}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <span class="clear" onclick={(e) => { e.stopPropagation(); clear(); }} title="clear">×</span>
    {/if}
  </button>

  {#if open}
    <div class="popover" class:align-right={alignRight} bind:this={popoverEl} role="dialog">
      <div class="cal-head">
        <button type="button" class="nav" onclick={prevMonth} aria-label="Previous month">
          <Icon name="chevron-left" size={12} />
        </button>
        <div class="month-label">{monthLabel}</div>
        <button type="button" class="nav" onclick={nextMonth} aria-label="Next month">
          <Icon name="chevron-right" size={12} />
        </button>
      </div>

      <div class="dow">
        {#each ["Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"] as d}
          <span>{d}</span>
        {/each}
      </div>

      <div class="grid">
        {#each cells as c (c.y + "-" + c.m + "-" + c.d)}
          {@const key = `${c.y}-${c.m}-${c.d}`}
          <button
            type="button"
            class="day"
            class:out={!c.inMonth}
            class:today={key === todayKey}
            class:sel={key === selKey}
            onclick={() => selectDay(c.y, c.m, c.d)}
          >
            {c.d}
          </button>
        {/each}
      </div>

      {#if mode === "datetime"}
        <div class="time-row">
          <span class="time-label">time</span>
          <input
            type="text"
            inputmode="numeric"
            maxlength="2"
            bind:value={hourStr}
            onblur={applyTime}
            onkeydown={(e: KeyboardEvent) => { if (e.key === "Enter") applyTime(); }}
          />
          <span class="time-sep">:</span>
          <input
            type="text"
            inputmode="numeric"
            maxlength="2"
            bind:value={minuteStr}
            onblur={applyTime}
            onkeydown={(e: KeyboardEvent) => { if (e.key === "Enter") applyTime(); }}
          />
        </div>
      {/if}

      <div class="presets">
        {#if mode === "datetime"}
          <button type="button" onclick={() => preset(0)}>now</button>
          <button type="button" onclick={() => preset(15)}>-15m</button>
          <button type="button" onclick={() => preset(60)}>-1h</button>
          <button type="button" onclick={() => preset(120)}>-2h</button>
        {:else}
          <button type="button" onclick={() => datePreset(0)}>today</button>
          <button type="button" onclick={() => datePreset(1)}>yesterday</button>
          <button type="button" onclick={() => datePreset(7)}>-7d</button>
          <button type="button" onclick={() => datePreset(30)}>-30d</button>
        {/if}
        <button type="button" class="danger" onclick={clear}>clear</button>
      </div>
    </div>
  {/if}
</div>

<style>
  .wrap { position: relative; display: inline-block; }

  .trigger {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    height: 32px;
    padding: 0 10px;
    background: var(--ink-2);
    border: 1px solid var(--border-strong);
    border-radius: var(--r-2);
    color: var(--fg-1);
    font-family: var(--font-mono);
    font-size: 12px;
    cursor: pointer;
    transition: border-color var(--dur-1) var(--ease-std);
  }
  .trigger:hover { border-color: var(--accent-line); }
  .trigger.empty { color: var(--fg-4); }
  .clear {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    color: var(--fg-4);
    margin-left: 2px;
  }
  .clear:hover { background: var(--ink-4); color: var(--fg-1); }

  .popover {
    position: absolute;
    top: calc(100% + 6px);
    left: 0;
    z-index: 100;
    width: 256px;
    background: var(--ink-3);
    border: 1px solid var(--border-strong);
    border-radius: var(--r-3);
    box-shadow: 0 1px 0 rgba(255,255,255,0.05) inset, 0 20px 60px -10px rgba(0,0,0,0.7);
    padding: 10px;
    animation: drFadeIn 140ms var(--ease-out);
  }
  .popover.align-right { left: auto; right: 0; }
  @keyframes drFadeIn {
    from { opacity: 0; transform: translateY(2px); }
    to { opacity: 1; transform: none; }
  }

  .cal-head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 2px 2px 8px;
  }
  .nav {
    width: 22px;
    height: 22px;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 1px solid var(--border);
    border-radius: var(--r-1);
    background: var(--ink-2);
    color: var(--fg-2);
    cursor: pointer;
  }
  .nav:hover { background: var(--ink-4); color: var(--fg-1); }
  .month-label {
    flex: 1;
    text-align: center;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-1);
    text-transform: lowercase;
    letter-spacing: var(--tracking-mono);
  }

  .dow {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 2px;
    padding: 4px 0;
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--fg-4);
    text-transform: uppercase;
    letter-spacing: var(--tracking-caps);
    text-align: center;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 2px;
  }
  .day {
    height: 28px;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 1px solid transparent;
    border-radius: var(--r-1);
    background: transparent;
    color: var(--fg-2);
    font-family: var(--font-mono);
    font-size: 11px;
    cursor: pointer;
    font-variant-numeric: tabular-nums;
    transition: background var(--dur-1) var(--ease-std), color var(--dur-1) var(--ease-std);
  }
  .day:hover { background: var(--ink-2); color: var(--fg-1); }
  .day.out { color: var(--fg-4); }
  .day.today { border-color: var(--border-strong); }
  .day.sel {
    background: var(--accent-wash);
    border-color: var(--accent-line);
    color: var(--mint-200);
  }

  .time-row {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 10px;
    padding: 6px 8px;
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-2);
  }
  .time-label {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-4);
    text-transform: uppercase;
    letter-spacing: var(--tracking-caps);
    flex: 1;
  }
  .time-row input {
    width: 28px;
    text-align: center;
    background: transparent;
    border: none;
    outline: none;
    color: var(--fg-1);
    font-family: var(--font-mono);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  .time-row input:focus { color: var(--accent); }
  .time-sep { color: var(--fg-4); font-family: var(--font-mono); }

  .presets {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 8px;
    align-items: center;
  }
  .presets button {
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-1);
    padding: 3px 7px;
    color: var(--fg-2);
    font-family: var(--font-mono);
    font-size: 10px;
    cursor: pointer;
  }
  .presets button:hover { background: var(--ink-4); color: var(--fg-1); }
  .presets button.danger { color: var(--fg-3); }
  .presets button.danger:hover { color: var(--danger); }
</style>
