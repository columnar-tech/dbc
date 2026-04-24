import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import Page from "./+page.svelte";

const mockInvoke = vi.mocked(invoke);
const mockListen = vi.mocked(listen);

beforeEach(() => {
  vi.clearAllMocks();
  mockListen.mockResolvedValue(() => {});
});

describe("Installed page", () => {
  it("shows empty state when no drivers installed", async () => {
    mockInvoke.mockResolvedValue([]);
    render(Page);
    await waitFor(() => {
      expect(screen.getByText(/no drivers installed/i)).toBeInTheDocument();
    });
  });

  it("renders installed drivers in a table", async () => {
    mockInvoke.mockResolvedValue([
      { name: "duckdb", version: "1.2.3", path: "/tmp/duckdb" },
    ]);
    render(Page);
    await waitFor(() => {
      expect(screen.getByText("duckdb")).toBeInTheDocument();
      expect(screen.getByText("1.2.3")).toBeInTheDocument();
    });
  });

  it("shows uninstall button for each driver", async () => {
    mockInvoke.mockResolvedValue([
      { name: "duckdb", version: "1.2.3", path: "/tmp/duckdb" },
    ]);
    render(Page);
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /uninstall/i })).toBeInTheDocument();
    });
  });

  it("renders multiple drivers", async () => {
    mockInvoke.mockResolvedValue([
      { name: "duckdb", version: "1.2.3", path: "/tmp/duckdb" },
      { name: "snowflake", version: "0.9.0", path: "/tmp/snowflake" },
    ]);
    render(Page);
    await waitFor(() => {
      expect(screen.getByText("duckdb")).toBeInTheDocument();
      expect(screen.getByText("snowflake")).toBeInTheDocument();
    });
  });

  it("calls list_installed on mount", async () => {
    mockInvoke.mockResolvedValue([]);
    render(Page);
    await waitFor(() => {
      expect(mockInvoke).toHaveBeenCalledWith("list_installed", { level: "user" });
    });
  });

  it("shows page heading", async () => {
    mockInvoke.mockResolvedValue([]);
    render(Page);
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /installed drivers/i })).toBeInTheDocument();
    });
  });
});
