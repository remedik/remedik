// Drives a real Chrome through the DevTools Protocol to answer one question:
// when you choose a namespace from the filter's select, does the page filter?
//
// It exists because that control was reported broken four times, and the cause
// was invisible to every other kind of test: the dashboard's own
// Content-Security-Policy carried `form-action \'none\'`, so the browser
// blocked every submission and said so only in the console. The markup was
// right, the handler was right, the server was right, and the control did
// nothing.
//
// So this is the test that reads the console. It is not in `make verify`,
// because it needs a running cluster and a real browser; it is what you run
// when something in the page looks correct and behaves otherwise.
//
// No dependencies: Node 21+ has a WebSocket client built in.
//
// Usage, with the dashboard reachable and authentication off:
//
//   chrome --headless=new --remote-debugging-port=9222 about:blank &
//   node hack/browser-check.mjs
//
// On WSL, Chrome binds the debugging port on the Windows side, so run this
// with the Windows node against a Windows Chrome; the dashboard\'s port-forward
// is reachable from there through localhost.
const BASE = process.env.BASE ?? "http://localhost:8082";
const CDP = process.env.CDP ?? "http://127.0.0.1:9222";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function target() {
  for (let i = 0; i < 40; i++) {
    try {
      const list = await (await fetch(`${CDP}/json/list`)).json();
      const page = list.find((t) => t.type === "page" && t.webSocketDebuggerUrl);
      if (page) return page.webSocketDebuggerUrl;
    } catch {}
    await sleep(500);
  }
  throw new Error("no debuggable page; is Chrome running with --remote-debugging-port?");
}

const url = await target();
const ws = new WebSocket(url);
await new Promise((r) => (ws.onopen = r));

let id = 0;
const waiting = new Map();
ws.onmessage = (e) => {
  const msg = JSON.parse(e.data);
  if (msg.id && waiting.has(msg.id)) {
    waiting.get(msg.id)(msg);
    waiting.delete(msg.id);
  }
};

function send(method, params = {}) {
  const n = ++id;
  return new Promise((resolve) => {
    waiting.set(n, resolve);
    ws.send(JSON.stringify({ id: n, method, params }));
  });
}

async function evaluate(expression) {
  const res = await send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (res.result?.exceptionDetails) {
    return { error: res.result.exceptionDetails.text + " " +
      (res.result.exceptionDetails.exception?.description ?? "") };
  }
  return { value: res.result?.result?.value };
}

await send("Page.enable");
await send("Runtime.enable");
await send("Log.enable");

// Collect console errors, which is the thing a screenshot cannot show.
const consoleErrors = [];
ws.addEventListener("message", (e) => {
  const msg = JSON.parse(e.data);
  if (msg.method === "Log.entryAdded" && msg.params?.entry?.level === "error") {
    consoleErrors.push(msg.params.entry.text);
  }
  if (msg.method === "Runtime.exceptionThrown") {
    consoleErrors.push(msg.params.exceptionDetails?.text ?? "exception");
  }
});

console.log(`==> navigating to ${BASE}/remediations`);
await send("Page.navigate", { url: `${BASE}/remediations` });
await sleep(2500);

const probe = await evaluate(`(() => {
  const form = document.querySelector("form.filter-select");
  const select = form && form.querySelector("select");
  return JSON.stringify({
    href: location.href,
    formFound: !!form,
    formClass: form ? form.className : null,
    formMethod: form ? form.getAttribute("method") : null,
    formAction: form ? form.getAttribute("action") : null,
    selectName: select ? select.name : null,
    optionCount: select ? select.options.length : 0,
    sampleOption: select && select.options[1] ? select.options[1].value : null,
    buttonVisible: form && form.querySelector("button")
      ? getComputedStyle(form.querySelector("button")).display
      : null,
    scriptTags: [...document.querySelectorAll("script")].map(s => s.src || "inline"),
  });
})()`);
console.log("   " + (probe.value ?? JSON.stringify(probe)));

if (consoleErrors.length) {
  console.log("==> console errors on load:");
  for (const e of consoleErrors) console.log("   " + e);
}

// Now do what a person does: choose an option and let the browser fire change.
const chosen = await evaluate(`(() => {
  const select = document.querySelector("form.filter-select select");
  if (!select) return "NO SELECT";
  const opt = [...select.options].find(o => o.value);
  if (!opt) return "NO OPTION WITH A VALUE";
  select.value = opt.value;
  select.dispatchEvent(new Event("change", { bubbles: true }));
  return opt.value;
})()`);
console.log(`==> chose ${chosen.value ?? JSON.stringify(chosen)} and dispatched change`);

await sleep(2500);

const after = await evaluate(`JSON.stringify({
  href: location.href,
  rows: document.querySelectorAll("#live tbody tr, tbody tr").length,
  hidden: (document.body.innerText.match(/[\\d,]+ hidden/) || [""])[0],
})`);
console.log("   after: " + (after.value ?? JSON.stringify(after)));

if (consoleErrors.length) {
  console.log("==> console errors total:");
  for (const e of consoleErrors) console.log("   " + e);
}

const href = JSON.parse(after.value ?? "{}").href ?? "";
console.log(href.includes("namespace=")
  ? "\n==> PASS the page navigated and the filter is in the URL"
  : "\n==> FAIL choosing an option did not navigate");
ws.close();
