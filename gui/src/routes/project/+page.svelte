<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { open } from '@tauri-apps/plugin-dialog';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';

  interface DriverEntry { name: string; constraint?: string; }

  let projectPath = $state<string | null>(null);
  let drivers = $state<DriverEntry[]>([]);
  let newDriver = $state('');
  let syncing = $state(false);
  let loadError = $state('');

  async function loadDrivers(path: string) {
    loadError = '';
    try {
      const entries = await invoke<Array<{ name: string; version_constraint?: string }>>('load_driver_list', { path });
      drivers = entries.map(e => ({ name: e.name, constraint: e.version_constraint }));
    } catch {
      drivers = [];
    }
  }

  async function openProject() {
    const selected = await open({ directory: true, multiple: false });
    if (selected && typeof selected === 'string') {
      const path = selected + '/dbc.toml';
      projectPath = path;
      // Initialize if missing, then load
      try {
        await invoke('init_driver_list', { path });
      } catch {
        // already exists — fine
      }
      await loadDrivers(path);
    }
  }

  async function addDriver() {
    if (!projectPath || !newDriver) return;
    try {
      await invoke('add_driver', {
        projectPath,
        driver: newDriver,
        version: null,
        prerelease: false,
      });
      newDriver = '';
      await loadDrivers(projectPath);
    } catch (e) {
      alert(String(e));
    }
  }

  async function removeDriver(name: string) {
    if (!projectPath) return;
    try {
      await invoke('remove_driver', { projectPath, driver: name });
      await loadDrivers(projectPath);
    } catch (e) {
      alert(String(e));
    }
  }

  async function sync() {
    if (!projectPath) return;
    syncing = true;
    try {
      await invoke('sync_drivers', {
        projectPath,
        level: 'user',
        noVerify: false,
        jobId: crypto.randomUUID(),
      });
    } catch (e) {
      alert(String(e));
    } finally {
      syncing = false;
    }
  }
</script>

<div class="p-6">
  <h1 class="text-2xl font-bold mb-6">Project</h1>

  {#if !projectPath}
    <Button onclick={openProject}>Open Project</Button>
  {:else}
    <div class="space-y-4">
      <p class="text-sm text-muted-foreground">Project: {projectPath}</p>

      <div class="flex gap-2">
        <Input placeholder="Driver name" bind:value={newDriver} class="max-w-xs" />
        <Button onclick={addDriver}>Add Driver</Button>
        <Button onclick={sync} disabled={syncing} variant="outline">
          {syncing ? 'Syncing...' : 'Sync'}
        </Button>
      </div>

      {#if drivers.length > 0}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Driver</TableHead>
              <TableHead>Constraint</TableHead>
              <TableHead>Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {#each drivers as driver}
              <TableRow>
                <TableCell>{driver.name}</TableCell>
                <TableCell>{driver.constraint ?? 'latest'}</TableCell>
                <TableCell>
                  <Button variant="ghost" size="sm" onclick={() => removeDriver(driver.name)}>
                    Remove
                  </Button>
                </TableCell>
              </TableRow>
            {/each}
          </TableBody>
        </Table>
      {:else}
        <p class="text-muted-foreground">No drivers in project. Add one above.</p>
      {/if}
    </div>
  {/if}
</div>
