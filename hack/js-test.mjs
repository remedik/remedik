// Tests internal/dashboard/assets/app.js against a stub DOM.
//
// It exists because the filter has now looked broken to its owner four times,
// and the last cause was in this file: the handler meant to submit on Enter
// cancelled the pending submit and never made one, so choosing a namespace
// with the keyboard did nothing. Every static check passed. The interaction had
// no test.
//
// A stub rather than a real browser: what needs pinning is this file's own
// logic — which events lead to a submit, and how many — not whether Chrome
// fires `change` when you pick an option. Node runs it with no dependencies.
//
// Usage:  node hack/js-test.mjs        (run by `make verify`)
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const source = readFileSync(join(root, "internal/dashboard/assets/app.js"), "utf8");

let failures = 0;
const pass = (m) => console.log(`    PASS ${m}`);
const fail = (m) => {
  failures++;
  console.log(`    FAIL ${m}`);
};
const check = (cond, m) => (cond ? pass(m) : fail(m));

// --------------------------------------------------------------------------
// A DOM with only what app.js touches
// --------------------------------------------------------------------------

function makeElement(tag, attrs = {}) {
  const el = {
    tagName: tag.toUpperCase(),
    className: attrs.class ?? "",
    attributes: attrs,
    children: [],
    listeners: {},
    submits: 0,
    classList: {
      add(name) {
        el.className = (el.className + " " + name).trim();
      },
      remove(name) {
        el.className = el.className.split(/\s+/).filter((c) => c !== name).join(" ");
      },
      contains(name) {
        return el.className.split(/\s+/).includes(name);
      },
    },
    textContent: attrs.text ?? "",
    parentNode: null,
    getAttribute(name) {
      return el.attributes[name] ?? null;
    },
    setAttribute(name, value) {
      el.attributes[name] = value;
    },
    appendChild(child) {
      el.children.push(child);
      child.parentNode = el;
      return child;
    },
    insertBefore(node, before) {
      const at = el.children.indexOf(before);
      el.children.splice(at < 0 ? el.children.length : at, 0, node);
      node.parentNode = el;
      return node;
    },
    removeChild(child) {
      const at = el.children.indexOf(child);
      if (at >= 0) el.children.splice(at, 1);
      child.parentNode = null;
      return child;
    },
    // The palette focuses its input; the stub records that it was asked to.
    focused: 0,
    focus() {
      el.focused++;
    },
    value: attrs.value ?? "",
    // Every text node under this element, for asserting what a rendered list
    // says without reaching into its shape.
    text() {
      return [el.textContent, ...el.children.map((c) => c.text())].join(" ").trim();
    },
    addEventListener(type, fn) {
      (el.listeners[type] ??= []).push(fn);
    },
    dispatch(type, event = {}) {
      for (const fn of el.listeners[type] ?? []) fn(event);
    },
    querySelector(sel) {
      if (sel === "select") return el.children.find((c) => c.tagName === "SELECT") ?? null;
      if (sel === "button") return el.children.find((c) => c.tagName === "BUTTON") ?? null;
      if (sel === "code") return el.children.find((c) => c.tagName === "CODE") ?? null;
      return null;
    },
    requestSubmit() {
      el.submits++;
    },
    submit() {
      el.submits++;
    },
  };
  return el;
}

function makeDocument(forms, copyable = []) {
  const body = makeElement("body");
  const listeners = {};
  return {
    body,
    listeners,
    activeElement: null,
    createElement(tag) {
      return makeElement(tag);
    },
    // The palette listens on the document, and a test needs to press keys.
    press(event) {
      for (const fn of listeners.keydown ?? []) fn({ preventDefault() {}, ...event });
    },
    querySelector(sel) {
      if (sel === 'meta[name="remedik-asset"]') return { content: "abc123" };
      return null;
    },
    querySelectorAll(sel) {
      if (sel === "form.filter-select") return forms;
      if (sel === "[data-copy]") return copyable;
      return [];
    },
    getElementById() {
      // No refresh toggle and no live region: the refresh block bails out,
      // which is what we want — this is a test of the filter block.
      return null;
    },
    addEventListener(type, fn) {
      (listeners[type] ??= []).push(fn);
    },
    hidden: false,
  };
}

// A clock we control, so the debounce is testable without waiting.
function makeWindow(entries) {
  let seq = 0;
  const timers = new Map();
  return {
    timers,
    setTimeout(fn, ms) {
      const id = ++seq;
      timers.set(id, { fn, ms });
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    requestAnimationFrame(fn) {
      return fn;
    },
    // Fire every timer that is still pending, as time passing would.
    tick() {
      const due = [...timers.values()];
      timers.clear();
      for (const t of due) t.fn();
    },
    location: {
      href: "http://localhost/remediations",
      went: [],
      reload() {},
      assign(url) {
        this.went.push(url);
      },
    },
    localStorage: { getItem: () => null, setItem: () => {} },
    fetch: entries
      ? () => Promise.resolve({ ok: true, json: () => Promise.resolve({ entries }) })
      : () => Promise.reject(new Error("not used")),
    clearInterval() {},
    setInterval: () => 0,
  };
}

function run(options = {}) {
  const select = makeElement("select", { name: "namespace" });
  const button = makeElement("button", { type: "submit" });
  const form = makeElement("form", { class: "filter-select", method: "get", action: "/remediations" });
  form.children.push(select, button);

  const win = makeWindow(options.entries);
  const doc = makeDocument([form], options.copyable ?? []);

  // app.js refers to `window`, `document` and `navigator`. Giving them as
  // parameters runs the real file rather than a copy of it.
  new Function("window", "document", "console", "navigator", source)(
    win,
    doc,
    { log() {}, error() {} },
    options.navigator ?? {},
  );

  return { form, select, win, doc };
}

// The palette, once it is open: the overlay, its input, and the labels it is
// currently showing.
function palette(doc) {
  const overlay = doc.body.children.find((c) => c.className === "palette");
  if (!overlay) return null;
  const box = overlay.children[0];
  const input = box.children.find((c) => c.tagName === "INPUT");
  const list = box.children.find((c) => c.tagName === "UL");
  return {
    overlay,
    input,
    list,
    labels: () =>
      list.children.map(
        (item) => (item.children.find((c) => c.className === "palette-label") ?? item).textContent,
      ),
  };
}

// A command block as the templates mark it: a <pre data-copy> holding the
// <code> whose text is what a person is meant to run.
function makeCommand(text) {
  const page = makeElement("div");
  const block = makeElement("pre", { class: "code", "data-copy": "" });
  const code = makeElement("code", { text });
  block.appendChild(code);
  page.appendChild(block);
  return { page, block };
}

function makeClipboard() {
  const written = [];
  return {
    written,
    clipboard: {
      writeText(text) {
        written.push(text);
        return Promise.resolve();
      },
    },
  };
}

// --------------------------------------------------------------------------
// The assertions
// --------------------------------------------------------------------------

console.log("==> app.js: the filter select");

{
  const { form } = run();
  check(form.classList.contains("is-live"),
    "the form is marked is-live, which is what hides the now-redundant button");
}

{
  const { form, select, win } = run();
  select.dispatch("change");
  check(form.submits === 0, "a change does not submit immediately");
  win.tick();
  check(form.submits === 1, "and submits once the change settles");
}

{
  const { form, select, win } = run();
  // A run of arrow presses on a closed select fires change repeatedly.
  for (let i = 0; i < 6; i++) select.dispatch("change");
  win.tick();
  check(form.submits === 1,
    "six changes in a row navigate once, not six times");
}

{
  // The bug this file exists for: Enter used to cancel the pending submit and
  // never make one, so open-type-Enter did nothing.
  const { form, select } = run();
  select.dispatch("change");
  select.dispatch("keydown", { key: "Enter" });
  check(form.submits === 1,
    "Enter applies the choice instead of cancelling it");
}

{
  const { form, select, win } = run();
  select.dispatch("change");
  select.dispatch("keydown", { key: "Enter" });
  win.tick();
  check(form.submits === 1,
    "and Enter does not submit twice when the timer would also have fired");
}

{
  const { form, select } = run();
  select.dispatch("keydown", { key: "ArrowDown" });
  check(form.submits === 0, "another key does not submit on its own");
}

{
  // With no select there is nothing to enhance, and nothing may throw.
  const bare = makeElement("form", { class: "filter-select" });
  const win = makeWindow();
  new Function("window", "document", "console", source)(
    win, makeDocument([bare]), { log() {}, error() {} });
  check(!bare.classList.contains("is-live"),
    "a form with no select is left alone, so its button stays visible");
}

console.log("");
console.log("==> app.js: copying a command");

{
  const { page, block } = makeCommand("kubectl -n remedik patch remediation x --type merge");
  const nav = makeClipboard();
  run({ copyable: [block], navigator: nav });

  const wrapper = page.children[0];
  check(wrapper.className === "copyable",
    "the command is wrapped, so the button cannot scroll away with the text");
  check(wrapper.children.includes(block),
    "and the block itself is inside that wrapper");

  const copy = wrapper.querySelector("button");
  check(copy !== null && copy.textContent === "Copy", "a Copy button is offered");
}

{
  const { page, block } = makeCommand("kubectl get remediations");
  const nav = makeClipboard();
  const { win } = run({ copyable: [block], navigator: nav });

  const copy = page.children[0].querySelector("button");
  copy.dispatch("click");
  await Promise.resolve();

  check(nav.written.length === 1 && nav.written[0] === "kubectl get remediations",
    "clicking it copies exactly the command that is printed");
  check(copy.textContent === "Copied", "and the button says so");

  win.tick();
  check(copy.textContent === "Copy", "then goes back to offering the next copy");
}

{
  // Clipboard access needs a secure context. A button that silently does
  // nothing is worse than none, because the reader believes they copied it.
  const { page, block } = makeCommand("kubectl get remediations");
  run({ copyable: [block], navigator: {} });

  check(page.children[0] === block,
    "with no clipboard available nothing is wrapped and no button is built");
}


// --------------------------------------------------------------------------
// The palette and the keys
//
// None of this exists in any page's markup: the overlay is built when somebody
// presses a key, which is why it is invisible to every test that reads HTML.
// --------------------------------------------------------------------------

console.log("==> app.js: the palette");

{
  const { doc, win } = run();

  doc.press({ key: "k", ctrlKey: true });
  const open = palette(doc);
  check(open !== null, "Ctrl+K opens it");
  check(open.input.focused === 1, "and puts the cursor in the input");

  // Without the fetch, the pages are still there: they are also the shortcuts,
  // and those are known to the script.
  check(open.labels().includes("Namespaces"), "with the pages offered even when the fetch fails");

  doc.press({ key: "Escape" });
  check(palette(doc) === null, "Escape closes it");
  check(win.location.went.length === 0, "and navigates nowhere");
}

{
  const entries = [
    { kind: "page", label: "Overview", url: "/" },
    { kind: "namespace", label: "payments", detail: "57 records", url: "/remediations?namespace=payments" },
    { kind: "namespace", label: "checkout", detail: "12 records", url: "/remediations?namespace=checkout" },
    { kind: "strategy", label: "pod-crashloop", detail: "200 records", url: "/remediations?strategy=pod-crashloop" },
  ];
  const { doc, win } = run({ entries });

  doc.press({ key: "k", metaKey: true });
  // The fetch resolves over a handful of microtasks -- the response, its body,
  // and the handlers between them -- so drain the queue rather than counting.
  for (let i = 0; i < 20; i++) await Promise.resolve();

  const open = palette(doc);
  check(open.labels().includes("payments"), "the fetched list is offered");

  open.input.value = "pay";
  open.input.dispatch("input");
  check(
    open.labels().join(",") === "payments",
    `typing narrows to what matches (got ${open.labels().join(",")})`,
  );

  open.input.dispatch("keydown", { key: "Enter", preventDefault() {} });
  check(
    win.location.went[0] === "/remediations?namespace=payments",
    "Enter goes to the chosen one",
  );
  check(palette(doc) === null, "and closes on the way");
}

{
  const { doc, win } = run();

  doc.press({ key: "g" });
  doc.press({ key: "n" });
  check(win.location.went[0] === "/namespaces", "g then n goes to the namespaces page");

  // A letter with nothing before it is just a letter.
  doc.press({ key: "s" });
  check(win.location.went.length === 1, "a bare letter navigates nowhere");
}

{
  // A key pressed into a control belongs to that control. The filter's select
  // is on the page this is most used on.
  const { doc, win } = run();

  doc.press({ key: "g", target: { tagName: "SELECT" } });
  doc.press({ key: "n", target: { tagName: "SELECT" } });
  check(win.location.went.length === 0, "typing into a select does not trigger a shortcut");
}

{
  const { doc } = run();

  doc.press({ key: "?" });
  const open = palette(doc);
  check(open !== null, "? opens the list of keys");
  check(
    open.list.children.some((item) => item.className === "palette-help"),
    "and it is the keys rather than results",
  );
}

console.log(failures === 0
  ? "==> app.js: all assertions passed"
  : `==> app.js: ${failures} failed`);
process.exit(failures === 0 ? 0 : 1);