import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import DriverDrawer from "./DriverDrawer.svelte";

const mockInvoke = vi.mocked(invoke);
const mockListen = vi.mocked(listen);

beforeEach(() => {
  vi.clearAllMocks();
  mockListen.mockResolvedValue(() => {});
});

describe("DriverDrawer", () => {
  it("renders without crashing when closed", () => {
    mockInvoke.mockResolvedValue({
      driver: "duckdb",
      version: "1.2.3",
      title: "DuckDB",
      license: "MIT",
      description: "An in-process SQL OLAP database",
      packages: ["macos_arm64"],
    });
    const { container } = render(DriverDrawer, { props: { driverName: "duckdb", open: false } });
    expect(container).toBeTruthy();
  });

  it("fetches driver info when opened", async () => {
    mockInvoke.mockResolvedValue({
      driver: "duckdb",
      version: "1.2.3",
      title: "DuckDB",
      license: "MIT",
      description: "An in-process SQL OLAP database",
      packages: ["macos_arm64"],
    });
    render(DriverDrawer, { props: { driverName: "duckdb", open: true } });
    await waitFor(() => {
      expect(mockInvoke).toHaveBeenCalledWith("get_driver_info", { name: "duckdb" });
    });
  });

  it("does not fetch when closed", () => {
    render(DriverDrawer, { props: { driverName: "duckdb", open: false } });
    expect(mockInvoke).not.toHaveBeenCalled();
  });

  it("shows driver info after successful fetch", async () => {
    mockInvoke.mockResolvedValue({
      driver: "duckdb",
      version: "1.2.3",
      title: "DuckDB",
      license: "MIT",
      description: "An in-process SQL OLAP database",
      packages: ["macos_arm64", "linux_amd64"],
    });
    render(DriverDrawer, { props: { driverName: "duckdb", open: true } });
    await waitFor(() => {
      expect(screen.getByText("An in-process SQL OLAP database")).toBeInTheDocument();
    });
  });

  it("shows error state when fetch fails", async () => {
    mockInvoke.mockRejectedValue(new Error("not found"));
    render(DriverDrawer, { props: { driverName: "missing-driver", open: true } });
    await waitFor(() => {
      expect(screen.getByText(/not found/i)).toBeInTheDocument();
    });
  });

  it("shows install button when info is loaded", async () => {
    mockInvoke.mockResolvedValue({
      driver: "duckdb",
      version: "1.2.3",
      title: "DuckDB",
      license: "MIT",
      description: "An in-process SQL OLAP database",
      packages: ["macos_arm64"],
    });
    render(DriverDrawer, { props: { driverName: "duckdb", open: true } });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /install/i })).toBeInTheDocument();
    });
  });
});
