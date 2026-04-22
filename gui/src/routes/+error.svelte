<script lang="ts">
  import { page } from '$app/stores';
</script>
<div class="p-6">
  <h1 class="text-2xl font-bold mb-2">Error {$page.status}</h1>
  <p class="text-muted-foreground">{$page.error?.message ?? 'Something went wrong'}</p>
</div>
