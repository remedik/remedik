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
    getAttribute(name) {
      return el.attributes[name] ?? null;
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

function makeDocument(forms) {
  return {
    querySelector(sel) {
      if (sel === 'meta[name="remedik-asset"]') return { content: "abc123" };
      return null;
    },
    querySelectorAll(sel) {
      if (sel === "form.filter-select") return forms;
      return [];
    },
    getElementById() {
      // No refresh toggle and no live region: the refresh block bails out,
      // which is what we want — this is a test of the filter block.
      return null;
    },
    addEventListener() {},
    hidden: false,
  };
}

// A clock we control, so the debounce is testable without waiting.
function makeWindow() {
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
    location: { href: "http://localhost/remediations", reload() {} },
    localStorage: { getItem: () => null, setItem: () => {} },
    fetch: () => Promise.reject(new Error("not used")),
    clearInterval() {},
    setInterval: () => 0,
  };
}

function run() {
  const select = makeElement("select", { name: "namespace" });
  const button = makeElement("button", { type: "submit" });
  const form = makeElement("form", { class: "filter-select", method: "get", action: "/remediations" });
  form.children.push(select, button);

  const win = makeWindow();
  const doc = makeDocument([form]);

  // app.js is two top-level IIFEs referring to `window` and `document`. Giving
  // them as parameters runs the real file rather than a copy of it.
  new Function("window", "document", "console", source)(win, doc, {
    log() {},
    error() {},
  });

  return { form, select, win };
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

console.log(failures === 0
  ? "==> app.js: all assertions passed"
  : `==> app.js: ${failures} failed`);
process.exit(failures === 0 ? 0 : 1);
