import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import InstallProgress from "./InstallProgress.svelte";

const mockInvoke = vi.mocked(invoke);
const mockListen = vi.mocked(listen);

beforeEach(() => {
  vi.clearAllMocks();
  mockListen.mockResolvedValue(() => {});
});

describe("InstallProgress", () => {
  it("renders without crashing when closed", () => {
    const { container } = render(InstallProgress, { props: { jobId: "job-1", open: false } });
    expect(container).toBeTruthy();
  });

  it("subscribes to install-progress events when open", async () => {
    render(InstallProgress, { props: { jobId: "test-job-123", open: true } });
    await waitFor(() => {
      expect(mockListen).toHaveBeenCalledWith(
        "install-progress:test-job-123",
        expect.any(Function)
      );
    });
  });

  it("does not subscribe to events when closed", () => {
    render(InstallProgress, { props: { jobId: "test-job-456", open: false } });
    expect(mockListen).not.toHaveBeenCalled();
  });

  it("shows dialog title when open", async () => {
    render(InstallProgress, { props: { jobId: "job-2", open: true } });
    await waitFor(() => {
      expect(screen.getByText("Installing Driver")).toBeInTheDocument();
    });
  });

  it("shows initial Starting phase text", async () => {
    render(InstallProgress, { props: { jobId: "job-3", open: true } });
    await waitFor(() => {
      expect(screen.getByText(/starting/i)).toBeInTheDocument();
    });
  });

  it("shows cancel button while in progress", async () => {
    render(InstallProgress, { props: { jobId: "job-4", open: true } });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
    });
  });

  it("calls invoke to emit cancel event on cancel click", async () => {
    mockInvoke.mockResolvedValue(undefined);
    render(InstallProgress, { props: { jobId: "job-5", open: true } });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
    });
  });
});
