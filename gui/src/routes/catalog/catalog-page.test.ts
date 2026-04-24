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

describe("Catalog page", () => {
  it("shows empty state when no drivers returned", async () => {
    mockInvoke.mockResolvedValue({ drivers: [] });
    render(Page);
    await waitFor(() => {
      expect(screen.getByText(/no drivers match/i)).toBeInTheDocument();
    }, { timeout: 2000 });
  });

  it("renders driver cards after successful fetch", async () => {
    mockInvoke.mockResolvedValue({
      drivers: [
        { driver: "duckdb", description: "DuckDB ADBC driver" },
        { driver: "snowflake", description: "Snowflake ADBC driver" },
      ],
    });
    render(Page);
    await waitFor(() => {
      expect(screen.getByText("duckdb")).toBeInTheDocument();
      expect(screen.getByText("snowflake")).toBeInTheDocument();
    }, { timeout: 2000 });
  });

  it("shows driver description in card", async () => {
    mockInvoke.mockResolvedValue({
      drivers: [{ driver: "duckdb", description: "DuckDB ADBC driver" }],
    });
    render(Page);
    await waitFor(() => {
      expect(screen.getByText("DuckDB ADBC driver")).toBeInTheDocument();
    }, { timeout: 2000 });
  });

  it("shows loading skeletons while fetching", async () => {
    mockInvoke.mockReturnValue(new Promise(() => {}));
    render(Page);
    await waitFor(() => {
      const skeletons = document.querySelectorAll("[data-slot='skeleton']");
      expect(skeletons.length).toBeGreaterThan(0);
    }, { timeout: 2000 });
  });

  it("shows error message and retry button on failure", async () => {
    mockInvoke.mockRejectedValue(new Error("network error"));
    render(Page);
    await waitFor(() => {
      expect(screen.getByText(/network error/i)).toBeInTheDocument();
      expect(screen.getByText(/retry/i)).toBeInTheDocument();
    }, { timeout: 2000 });
  });

  it("calls search_drivers with null query when empty", async () => {
    mockInvoke.mockResolvedValue({ drivers: [] });
    render(Page);
    await waitFor(() => {
      expect(mockInvoke).toHaveBeenCalledWith("search_drivers", expect.objectContaining({
        query: null,
        verbose: false,
      }));
    }, { timeout: 2000 });
  });

  it("shows Installed badge only for user-scope installs", async () => {
    mockInvoke.mockResolvedValue({
      drivers: [
        { driver: "user-installed", description: "d", installed: ["user=>1.0.0"] },
        { driver: "env-installed", description: "d", installed: ["env=>1.0.0"] },
        { driver: "not-installed", description: "d" },
      ],
    });
    render(Page);
    await waitFor(() => {
      expect(screen.getByText("user-installed")).toBeInTheDocument();
    }, { timeout: 2000 });
    const badges = screen.getAllByText("Installed");
    expect(badges).toHaveLength(1);
  });

  it("does not show Installed badge for env-scope installs", async () => {
    mockInvoke.mockResolvedValue({
      drivers: [
        { driver: "env-only", description: "d", installed: ["env=>1.0.0"] },
      ],
    });
    render(Page);
    await waitFor(() => {
      expect(screen.getByText("env-only")).toBeInTheDocument();
    }, { timeout: 2000 });
    expect(screen.queryByText("Installed")).not.toBeInTheDocument();
  });
});
