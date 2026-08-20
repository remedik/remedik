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

let failed = 0;
const verdict = (ok, message) => {
  if (!ok) failed++;
  console.log(`   ${ok ? "PASS" : "FAIL"} ${message}`);
};

const href = JSON.parse(after.value ?? "{}").href ?? "";
verdict(href.includes("namespace="), "choosing an option navigates, and the filter is in the URL");

// --------------------------------------------------------------------------
// The palette
//
// It exists in no page's markup -- the script builds it when somebody presses
// a key -- so this is the only kind of test that can see it at all. What is
// being checked is not that the code runs, which app.js's own tests cover, but
// that the browser lets it: a fetch the CSP refuses and a stylesheet that never
// matched both look exactly like a working palette from the server's side.
// --------------------------------------------------------------------------
console.log("\n==> the palette");
consoleErrors.length = 0;

const opened = await evaluate(`(() => {
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "k", ctrlKey: true, bubbles: true }));
  const overlay = document.querySelector(".palette");
  if (!overlay) return JSON.stringify({ open: false });
  const style = getComputedStyle(overlay);
  return JSON.stringify({
    open: true,
    // A class the stylesheet never defined leaves the overlay laid out as a
    // plain div: in flow, static, and invisible against the page behind it.
    positioned: style.position,
    layered: style.zIndex,
  });
})()`);
const palette = JSON.parse(opened.value ?? "{}");
verdict(palette.open === true, "Ctrl+K builds the overlay");
verdict(palette.positioned === "fixed", `the stylesheet reached it (position: ${palette.positioned})`);

// The fetch is same-origin, which connect-src 'self' allows -- and which is
// exactly the kind of thing this policy has silently broken twice before.
await sleep(1200);
const filled = await evaluate(`(() => {
  const items = [...document.querySelectorAll(".palette-item .palette-label")].map(e => e.textContent.trim());
  return JSON.stringify({ items: items.slice(0, 5), count: items.length });
})()`);
const entries = JSON.parse(filled.value ?? "{}");
verdict((entries.count ?? 0) > 0, `the palette lists something (${entries.count} entries)`);
verdict(
  consoleErrors.length === 0,
  `nothing was refused while it opened${consoleErrors.length ? ": " + consoleErrors.join(" | ") : ""}`,
);

await evaluate(`document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }))`);

// --------------------------------------------------------------------------
// The Copy button
//
// Built by the script rather than written into the markup, so that it cannot
// exist where the clipboard does not -- which also means no test that reads
// HTML can see whether it exists where it should.
// --------------------------------------------------------------------------
console.log("\n==> the copy button");
consoleErrors.length = 0;

await send("Page.navigate", { url: `${BASE}/approvals` });
await sleep(1800);

const copies = await evaluate(`(() => {
  const blocks = document.querySelectorAll("[data-copy]");
  const buttons = document.querySelectorAll(".copyable .copy");
  return JSON.stringify({
    blocks: blocks.length,
    buttons: buttons.length,
    label: buttons[0] ? buttons[0].textContent.trim() : null,
    secure: window.isSecureContext,
  });
})()`);
const copy = JSON.parse(copies.value ?? "{}");
verdict(
  copy.blocks > 0 && copy.buttons === copy.blocks,
  `every printed command offers a Copy (${copy.buttons}/${copy.blocks}, secure context: ${copy.secure})`,
);

// --------------------------------------------------------------------------
// A phone
//
// Whoever is on call reads this from one. A table that scrolls sideways is
// invisible to every test that does not lay the page out.
// --------------------------------------------------------------------------
console.log("\n==> at 390 CSS pixels");
consoleErrors.length = 0;

await send("Emulation.setDeviceMetricsOverride", {
  width: 390, height: 844, deviceScaleFactor: 2, mobile: true,
});
await send("Page.navigate", { url: `${BASE}/remediations` });
await sleep(2000);

const narrow = await evaluate(`(() => {
  const cell = document.querySelector(".table-cards tbody td[data-label]");
  return JSON.stringify({
    scrollWidth: document.documentElement.scrollWidth,
    innerWidth: window.innerWidth,
    // The column name comes from the cell's own attribute through a ::before.
    // A rule that never matched leaves it as "none", and the card is a column
    // of unlabelled values.
    label: cell ? getComputedStyle(cell, "::before").content : "NO CELL",
    display: cell ? getComputedStyle(cell).display : null,
  });
})()`);
const phone = JSON.parse(narrow.value ?? "{}");
// Asserted, because an override that silently did not apply leaves every check
// below passing against a desktop-width page.
verdict(
  phone.innerWidth <= 420,
  `the emulation actually applied (viewport ${phone.innerWidth})`,
);
verdict(
  phone.scrollWidth <= phone.innerWidth + 1,
  `the page does not scroll sideways (${phone.scrollWidth} <= ${phone.innerWidth})`,
);
verdict(
  typeof phone.label === "string" && phone.label !== "none" && phone.label !== "NO CELL",
  `each cell prints its column name (${phone.label})`,
);
verdict(phone.display === "flex", `cells are laid out as card rows (${phone.display})`);

await send("Emulation.clearDeviceMetricsOverride");

// --------------------------------------------------------------------------
// Every page, read by the browser rather than by a handler test
// --------------------------------------------------------------------------
console.log("\n==> the console, on every page");
for (const path of ["/", "/remediations", "/namespaces", "/approvals", "/strategies"]) {
  consoleErrors.length = 0;
  await send("Page.navigate", { url: BASE + path });
  await sleep(1500);
  verdict(consoleErrors.length === 0, `${path}${consoleErrors.length ? ": " + consoleErrors.join(" | ") : ""}`);
}

console.log(failed === 0 ? "\n==> browser-check: everything the console knows is fine"
  : `\n==> browser-check: ${failed} failed`);
ws.close();
process.exit(failed === 0 ? 0 : 1);
