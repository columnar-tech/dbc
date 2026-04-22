<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';

  interface LogEntry {
    timestamp: string;
    command: string;
    args: string[];
    exit_code: number | null;
    stderr_tail: string;
  }

  let logs = $state<LogEntry[]>([]);
  let filter = $state('');

  async function load() {
    logs = await invoke<LogEntry[]>('get_logs', {
      limit: 100,
      filterCommand: filter || null,
    });
  }

  async function clearLogs() {
    if (!confirm('Clear all logs?')) return;
    await invoke('clear_logs');
    logs = [];
  }

  $effect(() => { load(); });
</script>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold">Logs</h1>
    <div class="flex gap-2">
      <Input placeholder="Filter by command..." bind:value={filter} class="max-w-xs" oninput={load} />
      <Button variant="outline" onclick={clearLogs}>Clear</Button>
    </div>
  </div>

  {#if logs.length === 0}
    <p class="text-muted-foreground">No logs yet. Run some commands to see them here.</p>
  {:else}
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Time</TableHead>
          <TableHead>Command</TableHead>
          <TableHead>Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {#each logs as log}
          <TableRow>
            <TableCell class="text-xs text-muted-foreground">{log.timestamp}</TableCell>
            <TableCell class="font-mono text-sm">{log.command} {log.args.join(' ')}</TableCell>
            <TableCell>
              <Badge variant={log.exit_code === 0 ? 'default' : 'destructive'}>
                {log.exit_code ?? '?'}
              </Badge>
            </TableCell>
          </TableRow>
        {/each}
      </TableBody>
    </Table>
  {/if}
</div>
