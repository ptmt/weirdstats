const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

test('explicit sync posts discovery, polling remains read-only', async () => {
 const handlers = new Map();
 const nodes = new Map();
 const node = id => { if (!nodes.has(id)) nodes.set(id, {dataset:{unix:'0'},textContent:'',addEventListener(event,fn){handlers.set(id+':'+event,fn)},setAttribute(){},replaceChildren(){},append(){}});return nodes.get(id); };
 node('sync-status').dataset={completed:'0'};node('sync-status').querySelector=()=>node('summary');
 const requests=[];
 vm.runInNewContext(fs.readFileSync(__dirname+'/sync-status.js','utf8'),{document:{getElementById:node,querySelector:node,addEventListener(){},createElement:node,createTextNode:x=>x},setInterval(){},fetch:async(url,options)=>{requests.push({url,method:options.method});return {ok:true,json:async()=>({completed:0,pending:0,discovering:1,label:'Finding activities',state:'working',message:'Finding activities',errors:[]})}}});
 assert.equal(typeof handlers.get('sync-strava:click'),'function');
 await handlers.get('sync-strava:click')();
 assert.deepEqual(requests,[{url:'/api/sync/latest',method:'POST'}]);
 assert.equal(node('sync-strava').disabled,false);
});

test('polling renders optional failures separately and only reads status', async () => {
  const nodes = new Map();
  const node = id => {
    if (!nodes.has(id)) nodes.set(id, {dataset: {unix: '0'}, hidden: false, textContent: '', addEventListener() {}, setAttribute() {}, replaceChildren() {}, append() {}});
    return nodes.get(id);
  };
  const root = node('sync-status');
  root.dataset = {completed: '7'};
  root.querySelector = () => node('summary');
  let poll;
  const requests = [];
  const document = {hidden: false, getElementById: node, querySelector: node, addEventListener() {}, createElement: node, createTextNode: text => text};
  vm.runInNewContext(fs.readFileSync(__dirname + '/sync-status.js', 'utf8'), {
    document, setInterval: fn => {poll = fn;}, fetch: async (url, options) => {
      requests.push({url, method: options.method});
      return {ok: true, json: async () => ({completed: 7, pending: 0, failed: 0, blocked: 0, map_failed: 7, label: 'Map details unavailable', state: 'attention', message: 'Imported. Map details unavailable.', errors: []})};
    }
  });
  poll();
  await new Promise(resolve => setImmediate(resolve));
  assert.match(node('sync-counts').textContent, /7 map details unavailable/);
  assert.doesNotMatch(node('sync-counts').textContent, /failed attempts|import failures/);
  assert.equal(node('retry-import').hidden, false);
  assert.equal(node('sync-label').textContent, 'Map details unavailable');
  assert.deepEqual(requests, [{url: '/api/sync', method: 'GET'}]);
});
