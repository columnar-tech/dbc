<script lang="ts">
  interface Toast {
    id: string;
    message: string;
    type: 'success' | 'error' | 'info';
  }

  let toasts = $state<Toast[]>([]);

  export function addToast(message: string, type: Toast['type'] = 'info') {
    const id = crypto.randomUUID();
    toasts = [...toasts, { id, message, type }];
    setTimeout(() => {
      toasts = toasts.filter(t => t.id !== id);
    }, 4000);
  }

  function dismiss(id: string) {
    toasts = toasts.filter(t => t.id !== id);
  }
</script>

<div class="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
  {#each toasts as toast (toast.id)}
    <div
      class="flex items-center gap-3 rounded-lg px-4 py-3 shadow-lg text-sm
        {toast.type === 'error' ? 'bg-destructive text-destructive-foreground' :
         toast.type === 'success' ? 'bg-green-600 text-white' :
         'bg-card text-card-foreground border'}"
    >
      <span>{toast.message}</span>
      <button onclick={() => dismiss(toast.id)} class="ml-auto opacity-70 hover:opacity-100">✕</button>
    </div>
  {/each}
</div>
