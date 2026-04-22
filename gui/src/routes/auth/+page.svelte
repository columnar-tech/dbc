<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Card, CardContent, CardHeader, CardTitle } from '$lib/components/ui/card';
  import { Tabs, TabsContent, TabsList, TabsTrigger } from '$lib/components/ui/tabs';

  let registryUrl = $state('https://dbc-cdn-private.columnar.tech');
  let apiKey = $state('');
  let logging = $state(false);
  let message = $state('');

  async function loginDevice() {
    logging = true;
    message = '';
    try {
      await invoke('auth_login_device', {
        registryUrl,
        jobId: crypto.randomUUID(),
      });
      message = 'Login successful';
    } catch (e) {
      message = String(e);
    } finally {
      logging = false;
    }
  }

  async function loginApiKey() {
    logging = true;
    message = '';
    try {
      await invoke('auth_login_apikey', {
        registryUrl,
        apiKey,
      });
      apiKey = '';
      message = 'Login successful';
    } catch (e) {
      message = String(e);
    } finally {
      logging = false;
    }
  }

  async function logout() {
    try {
      await invoke('auth_logout', { registryUrl, purge: false });
      message = 'Logged out';
    } catch (e) {
      message = String(e);
    }
  }
</script>

<div class="p-6">
  <h1 class="text-2xl font-bold mb-6">Authentication</h1>

  <Card class="max-w-md">
    <CardHeader>
      <CardTitle>Registry Login</CardTitle>
    </CardHeader>
    <CardContent class="space-y-4">
      <Input placeholder="Registry URL" bind:value={registryUrl} />

      <Tabs value="device">
        <TabsList>
          <TabsTrigger value="device">Device Code</TabsTrigger>
          <TabsTrigger value="apikey">API Key</TabsTrigger>
        </TabsList>
        <TabsContent value="device" class="pt-4">
          <Button onclick={loginDevice} disabled={logging} class="w-full">
            {logging ? 'Authenticating...' : 'Login with Device Code'}
          </Button>
        </TabsContent>
        <TabsContent value="apikey" class="pt-4 space-y-2">
          <Input type="password" placeholder="API Key" bind:value={apiKey} />
          <Button onclick={loginApiKey} disabled={logging} class="w-full">
            {logging ? 'Authenticating...' : 'Login with API Key'}
          </Button>
        </TabsContent>
      </Tabs>

      {#if message}
        <p class="text-sm {message.includes('successful') ? 'text-green-600' : 'text-destructive'}">{message}</p>
      {/if}

      <Button variant="outline" onclick={logout} class="w-full">Logout</Button>
    </CardContent>
  </Card>
</div>
