const fs = require('node:fs'), path = require('node:path'), crypto = require('node:crypto');
const { createOAuthModelSync, createIntelligencePoller, prepareOAuthSave } = require('./account-fixes.cjs');
const ROOT = __dirname;
const edits = [];
function replaceOnce(source, old, replacement, label) {
  const start = source.indexOf(old);
  if (start < 0 || source.indexOf(old, start + 1) !== -1) throw Error('Production marker changed: ' + label);
  edits.push({ label, original: old, replacement });
  return source.slice(0, start) + replacement + source.slice(start + old.length);
}
function digest(value) { return crypto.createHash('sha256').update(value).digest('hex'); }
const files = [];
for (const name of ['EditAccountModal-CNS9cVsq.js', 'AccountsView-TALAMsyU.js']) {
  const original = fs.readFileSync(path.join(ROOT, 'production-baseline', name), 'utf8');
  let patched = original;
  const firstEdit = edits.length;
  if (name.startsWith('Edit')) {
    const component = '(' + createOAuthModelSync.toString() + ')({defineComponent:Va,ref:m,watch:Nt,onUnmounted:wn,h:Ot,useI18n:uo,syncModels:id=>ae.accounts.syncUpstreamModels(id),extractError:zt})';
    patched = replaceOnce(patched, 'qf=Va({__name:"EditAccountModal",', 'kittyOAuthModelSync=' + component + ',kittyPrepareOAuthSave=' + prepareOAuthSave.toString() + ',qf=Va({__name:"EditAccountModal",', 'new isolated OAuth catalog component');
    patched = replaceOnce(patched, 'setup(p,{emit:Ce}){var os;const c=p,Q=Ce,', 'setup(p,{emit:Ce}){const kittyOAuthSyncBusy=m(false),kittyOAuthSyncedId=m(null);var os;const c=p,Q=Ce,', 'isolated sync lifecycle state');
    patched = replaceOnce(patched, 'p.account.type==="apikey"?(i(),u("div",td,', 'w(kittyOAuthModelSync,{account:p.account,active:p.show,disabled:I.value,onBusy:n=>kittyOAuthSyncBusy.value=n,onFinished:n=>kittyOAuthSyncedId.value=n},null,8,["account","active","disabled"]),p.account.type==="apikey"?(i(),u("div",td,', 'OAuth sync entry before untouched API key section');
    patched = replaceOnce(patched, 'To=async(o,t)=>{I.value=!0;try{let r=await ae.accounts.update(o,Tl(t));', 'To=async(o,t)=>{if(kittyOAuthSyncBusy.value){U.showWarning("模型同步中，请稍后再保存");return}I.value=!0;try{const kittyPayload=await kittyPrepareOAuthSave(ae.accounts,c.account,o,t,kittyOAuthSyncBusy.value,kittyOAuthSyncedId.value,U);if(!kittyPayload)return;let r=await ae.accounts.update(o,Tl(kittyPayload));', 'preserve freshly synchronized metadata on normal save');
  } else {
    const start = patched.indexOf('let y=0;async function h(){', patched.indexOf('Xu=be({__name:"KittyAccountIntelligenceHost"'));
    const end = patched.indexOf('function _(d)', start);
    if (start < 0 || end < start) throw Error('Host function boundary changed');
    const setup = 'const {refresh:h}=(' + createIntelligencePoller.toString() + ')({watch:ge,onMounted:Nt,onUnmounted:Wt},{getAccounts:()=>n.accounts,byAccount:b,loadRecent:ids=>Ut.recent(ids)});';
    patched = replaceOnce(patched, patched.slice(start, end), setup, 'lifecycle-safe intelligence polling');
    patched = replaceOnce(patched, 'return ge(()=>n.accounts.map(d=>d.id).join(","),()=>{h()},{immediate:!0}),p({open:_,openSchedule:w,refresh:h,byAccount:b})', 'return p({open:_,openSchedule:w,refresh:h,byAccount:b})', 'remove obsolete ID-only watcher');
    patched = replaceOnce(patched, 'Gn=async()=>{await Promise.all([At(),L(),Ss()]),gt.value+=1}', 'Gn=async()=>{await Promise.all([At(),L(),Ss()]);await rt.value?.refresh();gt.value+=1}', 'manual account-list refresh includes intelligence summaries');
  }
  let reconstructed = patched;
  const changes = edits.slice(firstEdit);
  for (const edit of [...changes].reverse()) {
    if (reconstructed.indexOf(edit.replacement) < 0 || reconstructed.indexOf(edit.replacement) !== reconstructed.lastIndexOf(edit.replacement)) throw Error('Non-reversible patch');
    reconstructed = reconstructed.replace(edit.replacement, edit.original);
  }
  if (reconstructed !== original) throw Error('Unexpected production changes outside explicit edits');
  fs.mkdirSync(path.join(ROOT, 'patched-assets'), { recursive: true });
  fs.writeFileSync(path.join(ROOT, 'patched-assets', name), patched);
  files.push({ name, original_sha256: digest(original), patched_sha256: digest(patched), changes: changes.map(e => e.label) });
}
const manifest = { site: 'https://kittyapi.xyz', image: 'kitty/sub2api-cs-zuchengchen:0.2.19.77-kitty.24', files, poll_interval_ms: 15000, untouched_bytes_verified: true };
fs.writeFileSync(path.join(ROOT, 'patch-manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
fs.writeFileSync(path.join(ROOT, 'patch-edits.json'), JSON.stringify(edits, null, 2) + '\n');
console.log(JSON.stringify(manifest, null, 2));
