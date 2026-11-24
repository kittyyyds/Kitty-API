const test = require('node:test'), assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm');
const { createRequire } = require('node:module');
const repoRequire = createRequire(path.resolve(__dirname, '../sub2api-kitty/frontend/package.json'));
const { JSDOM } = repoRequire('jsdom');
const dom = new JSDOM('<html><body></body></html>', { url: 'https://kittyapi.xyz', pretendToBeVisual: true });
for (const name of ['window', 'document', 'Element', 'SVGElement', 'HTMLElement', 'Node']) globalThis[name] = dom.window[name];
const vue = repoRequire('vue');
const edit = fs.readFileSync(path.join(__dirname, 'patched-assets/EditAccountModal-CNS9cVsq.js'), 'utf8');
const accountsChunk = fs.readFileSync(path.join(__dirname, 'patched-assets/AccountsView-TALAMsyU.js'), 'utf8');
const oauthInitializer = edit.slice(edit.indexOf('kittyOAuthModelSync=') + 'kittyOAuthModelSync='.length, edit.indexOf(',kittyPrepareOAuthSave='));
const prepareSave = vm.runInNewContext('(' + edit.slice(edit.indexOf('kittyPrepareOAuthSave=') + 'kittyPrepareOAuthSave='.length, edit.indexOf(',qf=Va({__name:"EditAccountModal"')) + ')');
const hostStart = accountsChunk.indexOf('Xu=be({__name:"KittyAccountIntelligenceHost"');
const hostEnd = accountsChunk.indexOf(',Zu=["aria-label"]', hostStart);
const hostInitializer = accountsChunk.slice(hostStart + 3, hostEnd);
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await vue.nextTick(); };
const oauthAccount = id => ({ id, platform: 'openai', type: 'oauth', credentials: { model_mapping: { 'gpt-6.1-sol': 'gpt-6.1-sol' } }, extra: { keep: true } });
function mount(t, component, props) {
  const el = document.createElement('div'); document.body.appendChild(el);
  const app = vue.createApp(component, props), instance = app.mount(el);
  let removed = false;
  const unmount = () => { if (!removed) { removed = true; app.unmount(); el.remove(); } };
  t.after(unmount); return { el, instance, unmount };
}
function oauthFixture(t, load = async () => ({ models: [' gpt-6.1-sol ', 'gpt-6.1-sol', 'gpt-6.2-sol'] }), account = oauthAccount(39)) {
  const calls = [], events = [];
  const Component = vm.runInNewContext(oauthInitializer, {
    Va: vue.defineComponent, m: vue.ref, Nt: vue.watch, wn: vue.onBeforeUnmount, Ot: vue.h,
    uo: () => ({ locale: vue.ref('zh-CN'), t: key => ({
      'admin.accounts.syncUpstreamModels': '同步上游模型', 'admin.accounts.syncUpstreamModelsEmpty': '上游模型列表为空',
      'admin.accounts.syncUpstreamModelsFailed': '模型同步失败', 'admin.accounts.syncUpstreamModelsMetadataPartial': '部分能力已保存',
      'admin.accounts.syncUpstreamModelsMetadataIncomplete': '能力信息不完整'
    })[key] || key }),
    ae: { accounts: { syncUpstreamModels: id => { calls.push(id); return load(id); } } },
    zt: (error, fallback) => error.message || fallback
  });
  const props = vue.reactive({ account, active: true, disabled: false });
  const wrapper = vue.defineComponent({ setup: () => () => vue.h(Component, { ...props,
    onBusy: value => events.push(['busy', value]), onFinished: id => events.push(['finished', id])
  }) });
  const mounted = mount(t, wrapper);
  return { ...mounted, props, calls, events, click: () => mounted.el.querySelector('button')?.click() };
}
function intelligenceFixture(t, load = async ids => ids.map(account_id => ({ account_id, id: 'record-1', status: 'incorrect' })), list = [oauthAccount(39)]) {
  let visibility = 'visible', serial = 0;
  const timers = new Map(), listeners = new Map(), calls = [];
  const localDocument = {
    get visibilityState() { return visibility; },
    addEventListener(name, callback) { listeners.set(name, callback); },
    removeEventListener(name, callback) { if (listeners.get(name) === callback) listeners.delete(name); }
  };
  const Component = vm.runInNewContext(hostInitializer, {
    be: vue.defineComponent, A: vue.ref, ge: vue.watch, Nt: vue.onMounted, Wt: vue.onUnmounted,
    o: vue.openBlock, c: vue.createElementBlock, le: vue.Fragment, ee: vue.createBlock, $: vue.createCommentVNode,
    mu: {}, Ju: {}, pe: { accounts: { getAvailableModels: async () => [] } },
    Ut: { recent: ids => { calls.push([...ids]); return load([...ids]); } },
    document: localDocument,
    setInterval(fn, ms) { assert.equal(ms, 15000); timers.set(++serial, fn); return serial; },
    clearInterval(id) { timers.delete(id); }
  });
  const listRef = vue.ref(list), exposed = vue.ref(null);
  const wrapper = vue.defineComponent({ setup: () => () => vue.h(Component, { ref: exposed, accounts: listRef.value }) });
  const mounted = mount(t, wrapper);
  t.after(() => { mounted.unmount(); assert.equal(timers.size, 0); assert.equal(listeners.size, 0); });
  return { ...mounted, calls, listRef, timers, exposed,
    summary: () => exposed.value.byAccount,
    refresh: () => exposed.value.refresh(),
    tick: () => { for (const callback of timers.values()) callback(); },
    visible: value => { visibility = value ? 'visible' : 'hidden'; listeners.get('visibilitychange')?.(); }
  };
}

test('generated OAuth component shows exact sync button, deduplicates IDs, and never edits credentials', async t => {
  const account = oauthAccount(39), before = JSON.stringify(account);
  const f = oauthFixture(t, undefined, account);
  assert.equal(f.el.querySelector('button').textContent, '同步上游模型');
  f.click(); await flush();
  assert.deepEqual(f.calls, [39]);
  assert.equal(f.el.querySelectorAll('li').length, 2);
  assert.match(f.el.textContent, /已同步 2 个/);
  assert.equal(JSON.stringify(account), before);
  assert.ok(f.events.some(([name, id]) => name === 'finished' && id === 39));
});
test('API key and non-OpenAI accounts do not render the OAuth sync UI or call any API', async t => {
  const f = oauthFixture(t, undefined, { id: 1, platform: 'openai', type: 'apikey' });
  assert.equal(f.el.querySelector('section'), null); assert.equal(f.calls.length, 0);
  f.props.account = { id: 2, platform: 'anthropic', type: 'oauth' }; await flush();
  assert.equal(f.el.querySelector('section'), null);
});
test('OAuth sync coalesces clicks, disables while pending, and shows an empty result without false success', async t => {
  const wait = deferred(), f = oauthFixture(t, () => wait.promise);
  f.click(); f.click(); await vue.nextTick();
  assert.equal(f.calls.length, 1); assert.equal(f.el.querySelector('button').disabled, true);
  wait.resolve({ models: [] }); await flush();
  assert.match(f.el.textContent, /上游模型列表为空/); assert.equal(f.el.querySelector('button').disabled, false);
});
test('upstream error is visible and retry remains available', async t => {
  const f = oauthFixture(t, async () => { throw Error('upstream overloaded'); });
  f.click(); await flush();
  assert.equal(f.el.querySelector('[role=status]').dataset.severity, 'error');
  assert.match(f.el.textContent, /upstream overloaded/); assert.equal(f.el.querySelector('button').disabled, false);
});
test('partial metadata remains a warning with the fetched model IDs', async t => {
  const f = oauthFixture(t, async () => ({ models: ['gpt-model'], warnings: [{ code: 'upstream_model_metadata_partial' }] }));
  f.click(); await flush();
  assert.equal(f.el.querySelector('[role=status]').dataset.severity, 'warning'); assert.match(f.el.textContent, /部分能力已保存/);
});
test('account switch ignores an old OAuth sync and does not overwrite new loading state', async t => {
  const old = deferred(), next = deferred();
  const f = oauthFixture(t, id => id === 39 ? old.promise : next.promise);
  f.click(); f.props.account = oauthAccount(37); await flush(); f.click(); await flush();
  old.resolve({ models: ['old-secret-model'] }); await flush();
  assert.doesNotMatch(f.el.textContent, /old-secret-model/); assert.equal(f.el.querySelector('button').disabled, true);
  next.resolve({ models: ['new-model'] }); await flush();
  assert.match(f.el.textContent, /new-model/); assert.equal(f.el.querySelector('button').disabled, false);
  assert.deepEqual(f.events.filter(([name]) => name === 'finished'), [['finished', 37]]);
});
test('closed and unmounted OAuth components ignore late results', async t => {
  const wait = deferred(), f = oauthFixture(t, () => wait.promise);
  f.click(); f.props.active = false; await flush(); f.unmount();
  const count = f.events.length; wait.resolve({ models: ['late-model'] }); await flush(); assert.equal(f.events.length, count);
});
test('saving disables model synchronization', async t => {
  const f = oauthFixture(t); f.props.disabled = true; await flush(); f.click(); await flush(); assert.equal(f.calls.length, 0);
});
test('sync/save guard retains fresh server metadata and every unrelated edited field', async () => {
  const fresh = { source: 'upstream', synced_at: 'today', models: { model: {} } };
  const input = { name: 'edited', extra: { keep: 'edited', upstream_model_metadata: { old: true } }, credentials: { model_mapping: { stay: 'stay' } } };
  const before = JSON.stringify(input); let gets = 0;
  const output = await prepareSave({ getById: async id => { assert.equal(id, 39); gets++; return { extra: { upstream_model_metadata: fresh, unrelated: 'do-not-copy' } }; } }, oauthAccount(39), 39, input, false, 39, {});
  assert.equal(gets, 1); assert.equal(JSON.stringify(input), before); assert.equal(output.extra.upstream_model_metadata, fresh);
  assert.equal(output.extra.keep, 'edited'); assert.equal(output.extra.unrelated, undefined); assert.equal(output.credentials, input.credentials);
});
test('an unsynced or API-key normal save makes no extra network request; busy sync blocks saving', async () => {
  const api = { getById: async () => { throw Error('must not query'); } }, payload = { extra: {} };
  assert.equal(await prepareSave(api, oauthAccount(39), 39, payload, false, null, {}), payload);
  assert.equal(await prepareSave(api, { ...oauthAccount(39), type: 'apikey' }, 39, payload, false, 39, {}), payload);
  let warned = false;
  assert.equal(await prepareSave(api, oauthAccount(39), 39, payload, true, 39, { showWarning: () => { warned = true; } }), null);
  assert.equal(warned, true);
});
test('intelligence summaries load immediately and update automatically without changed account IDs', async t => {
  let index = 0;
  const f = intelligenceFixture(t, async ids => ids.map(account_id => ({ account_id, id: String(++index) })));
  await flush(); assert.equal(f.summary()[39][0].id, '1');
  f.tick(); await flush(); assert.equal(f.summary()[39][0].id, '2'); assert.equal(f.calls.length, 2);
});
test('hidden pages pause polling and visible pages refresh immediately', async t => {
  const f = intelligenceFixture(t); await flush(); f.visible(false); f.tick(); f.tick(); await flush();
  assert.equal(f.calls.length, 1); f.visible(true); await flush(); assert.equal(f.calls.length, 2);
});
test('slow requests cannot overlap periodic ticks; repeated forced refreshes coalesce into one follow-up', async t => {
  const old = deferred(), f = intelligenceFixture(t, async ids => f.calls.length === 1 ? old.promise : ids.map(account_id => ({ account_id, id: 'new' })));
  f.tick(); f.tick(); f.refresh(); f.refresh(); assert.equal(f.calls.length, 1);
  old.resolve([{ account_id: 39, id: 'old' }]); await flush();
  assert.equal(f.calls.length, 2); assert.equal(f.summary()[39][0].id, 'new');
});
test('account switches reject old summaries and load only the new account IDs', async t => {
  const old = deferred(), newer = deferred();
  const f = intelligenceFixture(t, ids => ids[0] === 39 ? old.promise : newer.promise);
  f.listRef.value = [oauthAccount(37)]; await flush(); assert.deepEqual(Object.keys(f.summary()), []);
  old.resolve([{ account_id: 39, id: 'old' }]); await flush(); assert.deepEqual(f.calls, [[39], [37]]);
  assert.equal(f.summary()[39], undefined);
  newer.resolve([{ account_id: 37, id: 'new' }, { account_id: 999, id: 'extraneous' }]); await flush();
  assert.equal(f.summary()[37][0].id, 'new'); assert.equal(f.summary()[999], undefined);
});
test('a platform change with the same ID stops unsupported polling and clears old data', async t => {
  const f = intelligenceFixture(t); await flush(); f.listRef.value = [{ id: 39, platform: 'grok' }]; await flush(); f.tick(); await flush();
  assert.deepEqual(Object.keys(f.summary()), []); assert.equal(f.calls.length, 1);
});
test('network failure retains last summary and the next periodic retry succeeds', async t => {
  let bad = false, version = 0;
  const f = intelligenceFixture(t, async ids => { if (bad) throw Error('offline'); return ids.map(account_id => ({ account_id, id: String(++version) })); });
  await flush(); bad = true; f.tick(); await flush(); assert.equal(f.summary()[39][0].id, '1');
  bad = false; f.tick(); await flush(); assert.equal(f.summary()[39][0].id, '2');
});
test('batch size stays at 100, unsupported accounts are excluded, and duplicate IDs do not duplicate requests', async t => {
  const list = Array.from({ length: 205 }, (_, i) => oauthAccount(i + 1));
  list.push({ id: 900, platform: 'grok' }, oauthAccount(1));
  const f = intelligenceFixture(t, undefined, list); await flush();
  assert.deepEqual(f.calls.map(ids => ids.length), [100, 100, 5]); assert.equal(Object.keys(f.summary()).length, 205);
});
test('unmount clears polling and listeners and rejects any late API result', async t => {
  const wait = deferred(), f = intelligenceFixture(t, () => wait.promise);
  const host = f.exposed.value; f.unmount(); wait.resolve([{ account_id: 39, id: 'late' }]); await flush();
  assert.deepEqual(Object.keys(host.byAccount), []); assert.equal(f.timers.size, 0); assert.equal(f.calls.length, 1);
});
test('manual list refresh calls the exposed intelligence refresh after list loading', async () => {
  const marker = 'Gn=async()=>', start = accountsChunk.indexOf(marker), end = accountsChunk.indexOf(',Ss=async()=>', start);
  const source = accountsChunk.slice(start + 3, end), calls = [], gt = { value: 0 };
  const handler = vm.runInNewContext('(' + source + ')', {
    At: async () => { calls.push('accounts'); }, L: async () => {}, Ss: async () => {},
    rt: { value: { refresh: async () => { calls.push('intelligence'); } } }, gt
  });
  await handler(); assert.deepEqual(calls, ['accounts', 'intelligence']); assert.equal(gt.value, 1);
});
test('every byte outside the seven explicit patch regions remains unchanged', () => {
  const edits = JSON.parse(fs.readFileSync(path.join(__dirname, 'patch-edits.json'), 'utf8'));
  for (const [name, count, chunk] of [['EditAccountModal-CNS9cVsq.js', 4, edit], ['AccountsView-TALAMsyU.js', 3, accountsChunk]]) {
    const regions = name.startsWith('Edit') ? edits.slice(0, count) : edits.slice(4);
    let restored = chunk;
    for (const region of [...regions].reverse()) restored = restored.replace(region.replacement, region.original);
    assert.equal(restored, fs.readFileSync(path.join(__dirname, 'production-baseline', name), 'utf8'));
  }
  assert.doesNotMatch(oauthInitializer, /credentials\s*=|model_mapping\s*=|\.update\(|intelligence.*run\(/);
});

test('the actual production entry marks account, visibility and save-state props as dynamic', async t => {
  const begin = edit.indexOf('w(kittyOAuthModelSync,{account:p.account,active:p.show,disabled:I.value,');
  const end = edit.indexOf(',p.account.type==="apikey"?', begin);
  assert.ok(begin > 0 && end > begin);
  const expression = edit.slice(begin, end);
  assert.match(expression, /null,8,\["account","active","disabled"\]\)/);
  const props = vue.reactive({ account: oauthAccount(39), show: true }), saving = vue.ref(false);
  let calls = 0;
  const context = { p: props, I: saving, w: vue.createVNode, kittyOAuthSyncBusy: vue.ref(false), kittyOAuthSyncedId: vue.ref(null),
    kittyOAuthModelSync: vm.runInNewContext(oauthInitializer, { Va: vue.defineComponent, m: vue.ref, Nt: vue.watch,
      wn: vue.onBeforeUnmount, Ot: vue.h, uo: () => ({ locale: vue.ref('zh'), t: key => key }),
      ae: { accounts: { syncUpstreamModels: async () => { calls++; return { models: ['model'] }; } } }, zt: () => 'error' }) };
  const renderEntry = vm.runInNewContext('()=>'+expression, context);
  const wrapper = vue.defineComponent({ setup: () => () => { vue.openBlock(); return vue.createElementBlock('div', null, [renderEntry()]); } });
  const f = mount(t, wrapper);
  assert.ok(f.el.querySelector('button'));
  saving.value = true; await flush(); assert.equal(f.el.querySelector('button').disabled, true);
  saving.value = false; props.show = false; await flush(); assert.equal(f.el.querySelector('button'), null);
  props.show = true; props.account = { id: 1, type: 'apikey', platform: 'openai' }; await flush(); assert.equal(f.el.querySelector('button'), null);
  props.account = oauthAccount(37); await flush(); f.el.querySelector('button').click(); await flush(); assert.equal(calls, 1);
});
