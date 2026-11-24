/** OAuth catalog sync only: never edits credentials, whitelist or scheduling. */
function createOAuthModelSync({ defineComponent, ref, watch, onUnmounted, h, useI18n, syncModels, extractError }) {
  return defineComponent({
    name: 'KittyOAuthModelSync',
    props: { account: Object, active: Boolean, disabled: Boolean },
    emits: ['busy', 'finished'],
    setup(props, { emit }) {
      const { t, locale } = useI18n();
      const busy = ref(false), models = ref([]), message = ref(''), severity = ref('info');
      let generation = 0, disposed = false;
      const copy = (zh, en) => String(locale.value).startsWith('zh') ? zh : en;
      const eligible = () => props.active && props.account?.platform === 'openai' && props.account?.type === 'oauth';
      const reset = () => {
        generation++; busy.value = false; models.value = []; message.value = ''; severity.value = 'info';
        emit('busy', false);
      };
      watch(() => [props.active, props.account?.id, props.account?.platform, props.account?.type], reset, { flush: 'sync' });
      onUnmounted(() => { disposed = true; generation++; emit('busy', false); });
      async function synchronize() {
        const id = props.account?.id;
        if (busy.value || props.disabled || !eligible() || !id) return;
        const token = ++generation;
        const current = () => !disposed && token === generation && eligible() && props.account?.id === id;
        busy.value = true; message.value = ''; models.value = []; emit('busy', true);
        try {
          const result = await syncModels(id);
          if (!current()) return;
          models.value = [...new Set((Array.isArray(result?.models) ? result.models : [])
            .filter(value => typeof value === 'string').map(value => value.trim()).filter(Boolean))];
          const warnings = Array.isArray(result?.warnings) ? result.warnings : [];
          const incomplete = warnings.some(w => w.code === 'upstream_model_metadata_incomplete');
          const partial = warnings.some(w => w.code === 'upstream_model_metadata_partial');
          const fallback = result?.discovery_source === 'configured_fallback';
          severity.value = incomplete || partial || fallback ? 'warning' : 'success';
          message.value = models.value.length
            ? copy(`已同步 ${models.value.length} 个上游模型。`, `Synced ${models.value.length} upstream models.`)
            : t('admin.accounts.syncUpstreamModelsEmpty');
          if (incomplete) message.value += ' ' + t('admin.accounts.syncUpstreamModelsMetadataIncomplete');
          else if (partial) message.value += ' ' + t('admin.accounts.syncUpstreamModelsMetadataPartial');
          if (fallback) message.value += ' ' + copy('上游目录暂不可用，当前为已配置模型。', 'The upstream catalog is unavailable; these are configured models.');
        } catch (error) {
          if (!current()) return;
          severity.value = 'error';
          message.value = extractError(error, t('admin.accounts.syncUpstreamModelsFailed'));
        } finally {
          if (current()) { busy.value = false; emit('busy', false); emit('finished', id); }
        }
      }
      return () => !eligible() ? null : h('section', {
        class: 'space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600',
        'data-testid': 'kitty-oauth-model-sync'
      }, [
        h('div', { class: 'flex flex-wrap items-center justify-between gap-3' }, [
          h('h3', { class: 'text-sm font-medium text-gray-900 dark:text-gray-100' }, copy('OpenAI OAuth 模型目录', 'OpenAI OAuth model catalog')),
          h('button', { type: 'button', class: 'btn btn-secondary', disabled: busy.value || props.disabled,
            'data-testid': 'kitty-oauth-sync-button', 'aria-busy': busy.value, onClick: synchronize
          }, busy.value ? copy('正在同步…', 'Syncing…') : t('admin.accounts.syncUpstreamModels'))
        ]),
        h('p', { class: 'text-xs text-gray-500 dark:text-gray-400' }, copy(
          '从 ChatGPT Codex 获取该账号的上游模型目录并同步能力信息；不会更改模型路由、白名单或账号凭据。同步立即生效，无需点击保存。',
          'Fetch this account’s ChatGPT Codex model catalog and sync capability metadata. Routing, whitelist and credentials are unchanged. Sync takes effect immediately without saving.')),
        message.value ? h('p', { role: 'status', 'aria-live': 'polite', 'data-severity': severity.value,
          class: severity.value === 'error' ? 'text-sm text-red-600 dark:text-red-400' : severity.value === 'warning' ? 'text-sm text-amber-600 dark:text-amber-400' : 'text-sm text-gray-700 dark:text-gray-200'
        }, message.value) : null,
        models.value.length ? h('details', { open: true }, [
          h('summary', { class: 'cursor-pointer text-xs text-gray-500' }, copy(`已获取的模型（${models.value.length}）`, `Fetched models (${models.value.length})`)),
          h('ul', { class: 'mt-2 space-y-1 overflow-auto', style: { maxHeight: '12rem' }, 'data-testid': 'kitty-oauth-model-list' },
            models.value.map(model => h('li', { key: model, class: 'break-all font-mono text-xs' }, model)))
        ]) : null
      ]);
    }
  });
}

/** Visible-page polling; one request flight, with a coalesced forced refresh. */
function createIntelligencePoller({ watch, onMounted, onUnmounted }, { getAccounts, byAccount, loadRecent }) {
  let disposed = false, generation = 0, flight = null, queued = false, timer;
  const visible = () => typeof document === 'undefined' || document.visibilityState !== 'hidden';
  const ids = () => [...new Set(getAccounts().filter(a => ['openai', 'anthropic'].includes(a.platform)).map(a => a.id))];
  function request(force = false) {
    if (disposed || !visible()) return Promise.resolve();
    const accounts = ids();
    if (!accounts.length) { byAccount.value = {}; return Promise.resolve(); }
    if (flight) { if (force) queued = true; return flight; }
    const token = generation;
    flight = (async () => {
      try {
        const batches = [];
        for (let i = 0; i < accounts.length; i += 100) batches.push(accounts.slice(i, i + 100));
        const rows = (await Promise.all(batches.map(batch => loadRecent(batch)))).flat();
        if (disposed || token !== generation || !visible()) return;
        const next = {};
        const allowed = new Set(accounts.map(String));
        for (const row of rows) if (allowed.has(String(row.account_id))) (next[row.account_id] ||= []).push(row);
        byAccount.value = next;
      } catch { /* Retain the last successful summary; retry at the next visible tick. */ }
      finally {
        flight = null;
        if (queued && !disposed && visible()) { queued = false; return request(false); }
      }
    })();
    return flight;
  }
  const refresh = () => request(true);
  watch(() => ids().join(','), () => {
    generation++; queued = false; byAccount.value = {}; void refresh();
  }, { immediate: true, flush: 'sync' });
  function visibilityChanged() {
    generation++;
    if (visible()) void refresh();
    else queued = false;
  }
  onMounted(() => {
    timer = setInterval(() => { void request(false); }, 15000);
    if (typeof document !== 'undefined') document.addEventListener('visibilitychange', visibilityChanged);
  });
  onUnmounted(() => {
    disposed = true; generation++; queued = false;
    clearInterval(timer);
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', visibilityChanged);
  });
  return { refresh };
}

/** Preserve sync-owned metadata if a form opened before synchronization is saved. */
async function prepareOAuthSave(api, account, id, payload, busy, synchronizedId, store) {
  if (busy) { store.showWarning('模型同步中，请稍后再保存'); return null; }
  if (account?.id === id && account.platform === 'openai' && account.type === 'oauth' && synchronizedId === id && payload.extra) {
    const latest = await api.getById(id);
    const extra = { ...payload.extra };
    delete extra.upstream_model_metadata;
    if (latest.extra?.upstream_model_metadata) extra.upstream_model_metadata = latest.extra.upstream_model_metadata;
    return { ...payload, extra };
  }
  return payload;
}
module.exports = { createOAuthModelSync, createIntelligencePoller, prepareOAuthSave };
