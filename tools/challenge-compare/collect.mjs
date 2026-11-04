// 让真实 Chrome 在 duck.ai 的 #jsa 沙箱 iframe 中求解一批挑战，保存为对照语料。
//
//   node collect.mjs <输出目录> [数量=40] [CDP 地址=http://127.0.0.1:9222]
//
// 需要一个开启了远程调试的 Chrome，例如：
//   docker run -d -p 9222:9222 chromedp/headless-shell
// 然后在仓库根目录运行：
//   CHALLENGE_CORPUS=<输出目录> go test ./internal/duckgo -run TestChallengeCorpus -v
import fs from 'node:fs';
import path from 'node:path';

const [outDir, countArg = '40', cdpArg = 'http://127.0.0.1:9222'] = process.argv.slice(2);
if (!outDir) {
  console.error('usage: node collect.mjs <out-dir> [count] [cdp-url]');
  process.exit(2);
}
fs.mkdirSync(outDir, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// --- 最小 CDP 客户端 ---
const cdp = new URL(cdpArg);
const version = await (await fetch(new URL('/json/version', cdp))).json();
const wsURL = new URL(version.webSocketDebuggerUrl);
wsURL.host = cdp.host; // 容器内返回的地址可能不可达
const ws = new WebSocket(wsURL);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let seq = 0;
const pending = new Map();
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data);
  const p = pending.get(msg.id);
  if (!p) return;
  pending.delete(msg.id);
  msg.error ? p.rej(new Error(JSON.stringify(msg.error))) : p.res(msg.result);
};
const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
  const id = ++seq;
  pending.set(id, { res, rej });
  ws.send(JSON.stringify({ id, method, params, sessionId }));
});
const evaluate = async (s, expression) => {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, s);
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  return r.result.value;
};

// --- 打开 duck.ai，等待页面创建 #jsa ---
const { targetId } = await send('Target.createTarget', { url: 'https://duck.ai/chat?duckai=1' });
const { sessionId: s } = await send('Target.attachToTarget', { targetId, flatten: true });
for (let i = 0; i < 30 && !(await evaluate(s, '!!document.querySelector("#jsa")').catch(() => false)); i++) await sleep(1000);

// 与网页一致：沙箱 CSP 禁止 eval，用内联 <script> 注入挑战
const solveInSandbox = `(async () => {
  const res = await fetch('/duckchat/v1/status', { headers: { 'x-vqd-accept': '1', 'Cache-Control': 'no-store' } });
  const b64 = res.headers.get('x-vqd-hash-1');
  if (!b64) return JSON.stringify({ err: 'no challenge, status ' + res.status });
  const js = new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));
  const w = document.querySelector('#jsa').contentWindow, d = w.document, sc = d.createElement('script');
  w.__res = undefined;
  sc.textContent = 'window.__res = (' + js + ');';
  d.body.appendChild(sc); d.body.removeChild(sc);
  return JSON.stringify({ b64, raw: await w.__res });
})()`;

let saved = 0;
for (let i = 0; i < +countArg; i++) {
  const r = JSON.parse(await evaluate(s, solveInSandbox));
  if (r.err) { console.error(i, r.err); await sleep(3000); continue; }
  const base = path.join(outDir, `${Date.now()}_${i}`);
  fs.writeFileSync(`${base}.b64`, r.b64);
  fs.writeFileSync(`${base}.raw.json`, JSON.stringify(r.raw));
  saved++;
  await sleep(700);
}
await send('Target.closeTarget', { targetId });
ws.close();
console.log(`saved ${saved} challenges to ${outDir}`);
