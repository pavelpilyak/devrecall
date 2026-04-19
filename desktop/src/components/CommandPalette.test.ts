import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/svelte";
import "@testing-library/jest-dom/vitest";
import CommandPalette from "./CommandPalette.svelte";

function sampleCommands() {
  const runs = {
    chat: vi.fn(),
    standup: vi.fn(),
    weekly: vi.fn(),
    timeline: vi.fn(),
    settings: vi.fn(),
  };
  const commands = [
    { group: "Navigate", cmd: "Go to Chat", icon: "message-square", run: runs.chat },
    { group: "Navigate", cmd: "Go to Standup", icon: "zap", run: runs.standup },
    { group: "Navigate", cmd: "Go to Weekly", icon: "calendar", run: runs.weekly },
    { group: "Navigate", cmd: "Go to Timeline", icon: "list", run: runs.timeline },
    { group: "Navigate", cmd: "Open Settings", icon: "settings", run: runs.settings },
  ];
  return { commands, runs };
}

function open(commands: ReturnType<typeof sampleCommands>["commands"]) {
  const onClose = vi.fn();
  const utils = render(CommandPalette, { open: true, commands, onClose });
  const input = utils.container.querySelector(".search-row input") as HTMLInputElement;
  return { ...utils, input, onClose };
}

describe("CommandPalette filtering", () => {
  it("shows all commands when query is empty", () => {
    const { commands } = sampleCommands();
    const { container } = open(commands);
    expect(container.querySelectorAll(".item").length).toBe(commands.length);
  });

  it("filters by command name (case-insensitive)", async () => {
    const { commands } = sampleCommands();
    const { container, input } = open(commands);
    await fireEvent.input(input, { target: { value: "weekly" } });
    const items = container.querySelectorAll(".item .cmd");
    expect(items.length).toBe(1);
    expect(items[0].textContent).toBe("Go to Weekly");
  });

  it("filters by group name", async () => {
    const { commands } = sampleCommands();
    const { container, input } = open(commands);
    await fireEvent.input(input, { target: { value: "navigate" } });
    expect(container.querySelectorAll(".item").length).toBe(commands.length);
  });

  it("shows empty state when nothing matches", async () => {
    const { commands } = sampleCommands();
    const { container, input } = open(commands);
    await fireEvent.input(input, { target: { value: "nonexistent-zzz" } });
    expect(container.querySelectorAll(".item").length).toBe(0);
    expect(container.querySelector(".empty")).toBeInTheDocument();
  });
});

describe("CommandPalette keyboard navigation", () => {
  it("highlights first item by default", () => {
    const { commands } = sampleCommands();
    const { container } = open(commands);
    const items = container.querySelectorAll(".item");
    expect(items[0]).toHaveClass("selected");
  });

  it("ArrowDown moves selection down", async () => {
    const { commands } = sampleCommands();
    const { container, input } = open(commands);
    await fireEvent.keyDown(window, { key: "ArrowDown" });
    const items = container.querySelectorAll(".item");
    expect(items[0]).not.toHaveClass("selected");
    expect(items[1]).toHaveClass("selected");
  });

  it("ArrowUp moves selection up", async () => {
    const { commands } = sampleCommands();
    const { container, input } = open(commands);
    await fireEvent.keyDown(window, { key: "ArrowDown" });
    await fireEvent.keyDown(window, { key: "ArrowDown" });
    await fireEvent.keyDown(window, { key: "ArrowUp" });
    const items = container.querySelectorAll(".item");
    expect(items[1]).toHaveClass("selected");
  });

  it("ArrowDown clamps at the last item", async () => {
    const { commands } = sampleCommands();
    const { container, input } = open(commands);
    for (let i = 0; i < commands.length + 3; i++) {
      await fireEvent.keyDown(window, { key: "ArrowDown" });
    }
    const items = container.querySelectorAll(".item");
    expect(items[items.length - 1]).toHaveClass("selected");
  });

  it("ArrowUp clamps at the first item", async () => {
    const { commands } = sampleCommands();
    const { container, input } = open(commands);
    await fireEvent.keyDown(window, { key: "ArrowUp" });
    await fireEvent.keyDown(window, { key: "ArrowUp" });
    const items = container.querySelectorAll(".item");
    expect(items[0]).toHaveClass("selected");
  });

  it("Enter runs the selected command and closes", async () => {
    const { commands, runs } = sampleCommands();
    const { input, onClose } = open(commands);
    await fireEvent.keyDown(window, { key: "ArrowDown" });
    await fireEvent.keyDown(window, { key: "ArrowDown" });
    await fireEvent.keyDown(window, { key: "Enter" });
    expect(runs.weekly).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("Escape closes the palette", async () => {
    const { commands } = sampleCommands();
    const { input, onClose } = open(commands);
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("arrow keys still work when input loses focus", async () => {
    const { commands } = sampleCommands();
    const { container, input } = open(commands);
    input.blur();
    await fireEvent.keyDown(window, { key: "ArrowDown" });
    await fireEvent.keyDown(window, { key: "ArrowDown" });
    const items = container.querySelectorAll(".item");
    expect(items[2]).toHaveClass("selected");
  });

  it("selection tracks filtered list after typing", async () => {
    const { commands, runs } = sampleCommands();
    const { input, onClose } = open(commands);
    await fireEvent.input(input, { target: { value: "go to" } });
    // Four "Go to …" commands match; sel is reset to 0 on input.
    await fireEvent.keyDown(window, { key: "ArrowDown" });
    await fireEvent.keyDown(window, { key: "Enter" });
    expect(runs.standup).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });
});
