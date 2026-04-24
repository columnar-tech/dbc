import { describe, it, expect } from "vitest";
import {
  SCHEMA_VERSION,
  isInstallStatus,
  isInstallProgress,
  isSyncStatus,
  isError,
  type Envelope,
} from "./index.js";
import type { InstallStatus } from "./install.js";
import type { SearchDriverBasic } from "./search.js";
import type { DriverInfo } from "./info.js";
import type { SyncStatus } from "./sync.js";
import type { ErrorResponse } from "./error.js";

describe("SCHEMA_VERSION", () => {
  it("equals 1", () => {
    expect(SCHEMA_VERSION).toBe(1);
  });
});

describe("InstallStatus", () => {
  it("round-trips through JSON", () => {
    const status: InstallStatus = {
      status: "installed",
      driver: "duckdb",
      version: "1.2.3",
      location: "/home/user/.local/share/ADBC/Drivers",
    };
    const parsed: InstallStatus = JSON.parse(JSON.stringify(status));
    expect(parsed.status).toBe("installed");
    expect(parsed.driver).toBe("duckdb");
    expect(parsed.version).toBe("1.2.3");
    expect(parsed.checksum).toBeUndefined();
  });

  it("preserves optional fields", () => {
    const status: InstallStatus = {
      status: "installed",
      driver: "snowflake",
      version: "1.0.0",
      location: "/tmp",
      checksum: "sha256:abc123",
      message: "post-install note",
      conflict: "old-snowflake (version: 0.9.0)",
    };
    const parsed: InstallStatus = JSON.parse(JSON.stringify(status));
    expect(parsed.checksum).toBe("sha256:abc123");
    expect(parsed.message).toBe("post-install note");
    expect(parsed.conflict).toBe("old-snowflake (version: 0.9.0)");
  });

  it("status can be already installed", () => {
    const status: InstallStatus = {
      status: "already installed",
      driver: "duckdb",
      version: "1.2.3",
      location: "/tmp",
    };
    const parsed: InstallStatus = JSON.parse(JSON.stringify(status));
    expect(parsed.status).toBe("already installed");
  });
});

describe("SearchDriverBasic", () => {
  it("round-trips through JSON", () => {
    const driver: SearchDriverBasic = {
      driver: "duckdb",
      description: "DuckDB ADBC driver",
      installed: ["user=>1.2.3"],
      registry: "public",
    };
    const parsed: SearchDriverBasic = JSON.parse(JSON.stringify(driver));
    expect(parsed.driver).toBe("duckdb");
    expect(parsed.installed).toHaveLength(1);
  });

  it("handles missing optional fields", () => {
    const driver: SearchDriverBasic = {
      driver: "duckdb",
      description: "DuckDB ADBC driver",
    };
    const parsed: SearchDriverBasic = JSON.parse(JSON.stringify(driver));
    expect(parsed.installed).toBeUndefined();
    expect(parsed.registry).toBeUndefined();
  });
});

describe("DriverInfo", () => {
  it("round-trips through JSON", () => {
    const info: DriverInfo = {
      driver: "duckdb",
      version: "1.2.3",
      title: "DuckDB",
      license: "MIT",
      description: "An in-process SQL OLAP database",
      packages: ["macos_arm64", "linux_amd64"],
    };
    const parsed: DriverInfo = JSON.parse(JSON.stringify(info));
    expect(parsed.packages).toHaveLength(2);
    expect(parsed.packages[0]).toBe("macos_arm64");
  });

  it("preserves all required fields", () => {
    const info: DriverInfo = {
      driver: "snowflake",
      version: "0.9.0",
      title: "Snowflake",
      license: "Apache-2.0",
      description: "Snowflake ADBC driver",
      packages: ["linux_amd64"],
    };
    const parsed: DriverInfo = JSON.parse(JSON.stringify(info));
    expect(parsed.title).toBe("Snowflake");
    expect(parsed.license).toBe("Apache-2.0");
  });
});

describe("SyncStatus", () => {
  it("round-trips through JSON", () => {
    const status: SyncStatus = {
      installed: [{ name: "duckdb", version: "1.2.3" }],
      skipped: [],
      errors: [],
    };
    const parsed: SyncStatus = JSON.parse(JSON.stringify(status));
    expect(parsed.installed).toHaveLength(1);
    expect(parsed.installed[0].name).toBe("duckdb");
  });

  it("preserves errors array", () => {
    const status: SyncStatus = {
      installed: [],
      skipped: [],
      errors: [{ name: "broken", error: "network timeout" }],
    };
    const parsed: SyncStatus = JSON.parse(JSON.stringify(status));
    expect(parsed.errors).toHaveLength(1);
    expect(parsed.errors[0].error).toBe("network timeout");
  });
});

describe("ErrorResponse", () => {
  it("round-trips through JSON", () => {
    const err: ErrorResponse = {
      code: "checksum_mismatch",
      message: "sha256 mismatch",
    };
    const parsed: ErrorResponse = JSON.parse(JSON.stringify(err));
    expect(parsed.code).toBe("checksum_mismatch");
    expect(parsed.owner_pid).toBeUndefined();
  });

  it("preserves optional owner_pid", () => {
    const err: ErrorResponse = {
      code: "locked",
      message: "install locked by pid 42",
      owner_pid: 42,
    };
    const parsed: ErrorResponse = JSON.parse(JSON.stringify(err));
    expect(parsed.owner_pid).toBe(42);
  });
});

describe("Envelope type guards", () => {
  it("isInstallStatus identifies install.status envelopes", () => {
    const env: Envelope = {
      schema_version: 1,
      kind: "install.status",
      payload: { status: "installed", driver: "duckdb", version: "1.0", location: "/tmp" },
    };
    expect(isInstallStatus(env)).toBe(true);
    expect(isError(env)).toBe(false);
  });

  it("isInstallProgress identifies install.progress envelopes", () => {
    const env: Envelope = {
      schema_version: 1,
      kind: "install.progress",
      payload: { event: "download.start", driver: "duckdb" },
    };
    expect(isInstallProgress(env)).toBe(true);
    expect(isInstallStatus(env)).toBe(false);
  });

  it("isSyncStatus identifies sync.status envelopes", () => {
    const env: Envelope = {
      schema_version: 1,
      kind: "sync.status",
      payload: { installed: [], skipped: [], errors: [] },
    };
    expect(isSyncStatus(env)).toBe(true);
    expect(isError(env)).toBe(false);
  });

  it("isError identifies error envelopes", () => {
    const env: Envelope = {
      schema_version: 1,
      kind: "error",
      payload: { code: "not_found", message: "driver not found" },
    };
    expect(isError(env)).toBe(true);
    expect(isInstallStatus(env)).toBe(false);
  });

  it("type guards are mutually exclusive for different kinds", () => {
    const env: Envelope = {
      schema_version: 1,
      kind: "install.progress",
      payload: { event: "verify.start", driver: "duckdb" },
    };
    expect(isInstallStatus(env)).toBe(false);
    expect(isInstallProgress(env)).toBe(true);
    expect(isSyncStatus(env)).toBe(false);
    expect(isError(env)).toBe(false);
  });
});
