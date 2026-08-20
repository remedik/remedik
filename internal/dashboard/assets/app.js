/*
 * Keeps the page current without taking it away from the reader.
 *
 * A meta refresh or a location.reload() would work, and would also throw
 * away the scroll position every ten seconds — which is exactly when
 * someone is reading a long list during an incident. So the page fetches
 * itself and swaps the <main> element instead.
 *
 * Everything here is an enhancement. With JavaScript off the dashboard
 * renders, navigates and reads identically; it simply does not update on
 * its own.
 */
(function () {
  "use strict";

  var INTERVAL_MS = 10000;
  var STORAGE_KEY = "remedik.autorefresh";

  // The running build's asset fingerprint. Only #content is swapped, so the
  // stylesheet, this script and the whole page shell are whatever the tab
  // loaded — for ever, in a tab left open across an operator upgrade. Its
  // data would keep updating through last week's markup, which is the most
  // convincing way for a page to be wrong.
  var assetMeta = document.querySelector('meta[name="remedik-asset"]');
  var asset = assetMeta ? assetMeta.content : "";

  // LIVE_ID is the region a page may mark as the only part worth replacing.
  var LIVE_ID = "live";

  var toggle = document.getElementById("refresh-toggle");
  var content = document.getElementById("content");
  var updated = document.getElementById("updated");
  if (!toggle || !content) {
    return;
  }

  var label = toggle.querySelector(".refresh-label");
  var enabled = read() !== "off";
  var timer = null;
  var inFlight = false;

  function read() {
    try {
      return window.localStorage.getItem(STORAGE_KEY);
    } catch (e) {
      // Storage can be denied outright; the default is simply used.
      return null;
    }
  }

  function write(value) {
    try {
      window.localStorage.setItem(STORAGE_KEY, value);
    } catch (e) {
      /* not worth reporting: the preference just does not persist */
    }
  }

  function paint() {
    toggle.setAttribute("aria-pressed", enabled ? "true" : "false");
    if (label) {
      label.textContent = enabled ? "Auto-refresh" : "Refresh paused";
    }
    toggle.title = enabled
      ? "Reloading the page content every 10 seconds, without losing your place"
      : "Automatic reloading is paused; the page will not change until you reload it";
  }

  function schedule() {
    window.clearTimeout(timer);
    if (enabled) {
      timer = window.setTimeout(refresh, INTERVAL_MS);
    }
  }

  function refresh() {
    // A hidden tab is not being read, and polling it would cost the
    // operator work for nobody.
    if (!enabled || inFlight || document.hidden) {
      schedule();
      return;
    }
    inFlight = true;

    window
      .fetch(window.location.href, { credentials: "same-origin", cache: "no-store" })
      .then(function (response) {
        if (!response.ok) {
          throw new Error("HTTP " + response.status);
        }
        return response.text();
      })
      .then(function (html) {
        // The footer's timestamp lives outside <main>, so it survives the
        // swap and can be updated in place.
        var doc = new DOMParser().parseFromString(html, "text/html");

        // A new build is serving this page. Swapping only #content would
        // render it through the stylesheet and script the tab still holds,
        // so take the whole page instead.
        var fresh = doc.querySelector('meta[name="remedik-asset"]');
        if (asset && fresh && fresh.content && fresh.content !== asset) {
          window.location.reload();
          return;
        }

        // A page may mark a narrower live region. The list does, because its
        // filter controls hold what the reader typed or chose, and replacing
        // them every ten seconds is what made the filter appear broken twice
        // before. Pages with no live region swap their whole content.
        var target = document.getElementById(LIVE_ID) || content;
        var next = doc.getElementById(target.id);
        if (!next) {
          return;
        }
        target.innerHTML = next.innerHTML;

        var stamp = doc.getElementById("updated");
        if (updated && stamp) {
          updated.textContent = stamp.textContent;
          updated.classList.remove("is-stale");
        }
      })
      .catch(function () {
        // The operator may be restarting, or the token may have changed.
        // Saying so is better than leaving a page that quietly stopped
        // being true.
        if (updated) {
          updated.textContent = "not updating — reload the page";
          updated.classList.add("is-stale");
        }
      })
      .then(function () {
        inFlight = false;
        schedule();
      });
  }

  toggle.addEventListener("click", function () {
    enabled = !enabled;
    write(enabled ? "on" : "off");
    paint();
    schedule();
  });

  document.addEventListener("visibilitychange", function () {
    if (!document.hidden) {
      schedule();
    }
  });

  paint();
  schedule();
})();

/*
 * Makes the namespace and strategy selects apply the moment you choose.
 *
 * They are inside a GET form with a submit button, which works with
 * JavaScript off and is why it is built that way. But every other control on
 * that row is a link that applies on one click, so a select that needed a
 * second gesture read as broken -- reported as "the buttons work, the
 * dropdown does nothing", which is exactly what it looked like.
 *
 * So with JavaScript the form submits on change and the button is hidden as
 * redundant; without it, the button is the control and nothing here runs.
 * Filtering is still navigation either way: the form is a GET, so the result
 * is a URL somebody can paste.
 */
(function () {
  "use strict";

  // How long a run of change events is allowed to settle before navigating.
  var SETTLE_MS = 250;

  var forms = document.querySelectorAll("form.filter-select");
  if (!forms.length || typeof window.requestAnimationFrame !== "function") {
    return;
  }

  Array.prototype.forEach.call(forms, function (form) {
    var select = form.querySelector("select");
    if (!select) {
      return;
    }

    // The button is now redundant, but it is what works without this script,
    // so it is hidden here rather than left out of the markup.
    form.classList.add("is-live");

    var pending = null;

    // requestSubmit keeps the browser's own form handling -- the GET
    // serialisation, the history entry -- where submit() would bypass it.
    function apply() {
      window.clearTimeout(pending);
      pending = null;
      if (typeof form.requestSubmit === "function") {
        form.requestSubmit();
      } else {
        form.submit();
      }
    }

    // A closed, focused select fires change on every arrow press in some
    // browsers, which would be one page load per keystroke. Coalescing means
    // a run of arrow presses navigates once, when it settles.
    select.addEventListener("change", function () {
      window.clearTimeout(pending);
      pending = window.setTimeout(apply, SETTLE_MS);
    });

    // Enter commits the native dropdown, so waiting out the delay after it
    // would feel broken. Apply straight away instead.
    //
    // The first version of this cleared the pending submit here and never made
    // one, so choosing with the keyboard -- open, type to search, Enter, which
    // is the interaction the control's own label suggests -- did nothing at
    // all. That is the bug this whole block exists to fix, reintroduced inside
    // the fix.
    select.addEventListener("keydown", function (event) {
      if (event.key === "Enter") {
        apply();
      }
    });
  });

})();

/*
 * Copying a command.
 *
 * These pages print commands a person is meant to run: the patch that decides
 * an approval, and the kubectl each step would have been. At 04:00 nobody
 * retypes a patch correctly, and dragging a mouse across a wrapped command is
 * worse than retyping it.
 *
 * The button is built here rather than written into the template so that it
 * cannot exist when it would not work. Clipboard access needs a secure
 * context, so a dashboard put behind plain HTTP would otherwise show a button
 * that silently does nothing — which is worse than no button, because the
 * reader believes they copied it.
 *
 * Its own block, and that is the whole bug it shipped with: it lived inside
 * the filter's, which returns early on any page with no filter form — that
 * is, on every page that prints a command. Six commands on /approvals, a
 * secure context, and no button on any of them. Nothing could see it: the
 * markup was right, the handler was right, and the stub DOM the unit tests
 * build always has a form in it. hack/browser-check.mjs asks the browser
 * whether the button is there, which is the only thing that knows.
 */
(function () {
  "use strict";

  var COPIED_MS = 1600;

  function canCopy() {
    return (
      typeof navigator !== "undefined" &&
      navigator.clipboard &&
      typeof navigator.clipboard.writeText === "function"
    );
  }

  function addCopyButton(block) {
    var source = block.querySelector("code");
    if (!source) {
      return;
    }

    var button = document.createElement("button");
    button.type = "button";
    button.className = "copy";
    button.textContent = "Copy";
    button.setAttribute("aria-label", "Copy this command");

    var reset = null;
    button.addEventListener("click", function () {
      navigator.clipboard.writeText(source.textContent).then(
        function () {
          button.textContent = "Copied";
          window.clearTimeout(reset);
          reset = window.setTimeout(function () {
            button.textContent = "Copy";
          }, COPIED_MS);
        },
        function () {
          // Say nothing rather than claim success. The command is still on
          // the page to select by hand, which is where the reader started.
        },
      );
    });

    // Wrapped rather than appended into the block: both kinds of command
    // scroll horizontally, and a button inside a scrolling box scrolls out
    // of reach with the text it is meant to copy.
    var parent = block.parentNode;
    if (!parent || typeof parent.insertBefore !== "function") {
      return;
    }
    var wrapper = document.createElement("div");
    wrapper.className = "copyable";
    parent.insertBefore(wrapper, block);
    wrapper.appendChild(block);
    wrapper.appendChild(button);
  }

  if (canCopy()) {
    var copyable = document.querySelectorAll("[data-copy]");
    for (var c = 0; c < copyable.length; c++) {
      addCopyButton(copyable[c]);
    }
  }
})();

/*
 * Reaching a page without the mouse.
 *
 * Everything here is an enhancement in the strict sense: with JavaScript off,
 * the navigation is the navigation, every filter is a link, and no function is
 * reachable only by a key. What this adds is the interaction an operator
 * already has in k9s and in every editor — press a key, type three letters of
 * a namespace, arrive — on the page they read while something is on fire.
 *
 * The list comes from /palette, which is the same data the pages already show,
 * behind the same authentication, fetched once. If that fetch fails the
 * palette still opens on the five pages, because those are also the shortcuts
 * and they are known here.
 *
 * Nothing is built with innerHTML: entries carry namespaces, alert names and
 * record names from a cluster, and the only safe way to put a cluster's
 * strings into a page is as text.
 */
(function () {
  "use strict";

  // How long "g" waits for the letter that follows it.
  var GOTO_MS = 1200;
  // How many results are drawn. Past a dozen nobody is reading; they type
  // another letter.
  var SHOWN = 12;

  var PAGES = [
    { key: "o", kind: "page", label: "Overview", url: "/" },
    { key: "r", kind: "page", label: "Remediations", url: "/remediations" },
    { key: "n", kind: "page", label: "Namespaces", url: "/namespaces" },
    { key: "a", kind: "page", label: "Approvals", url: "/approvals" },
    { key: "s", kind: "page", label: "Strategies", url: "/strategies" },
  ];

  var KEYS = [
    { keys: "Ctrl K", what: "open this" },
    { keys: "g then o r n a s", what: "go to a page" },
    { keys: "? ", what: "these keys" },
    { keys: "Esc", what: "close" },
  ];

  if (!document.body || typeof document.createElement !== "function") {
    return;
  }

  var entries = null;
  var fetched = false;
  var overlay = null;
  var input = null;
  var list = null;
  var results = [];
  var active = 0;
  var pendingGoto = null;
  var restoreFocus = null;

  // ------------------------------------------------------------------
  // The list
  // ------------------------------------------------------------------

  function load() {
    if (fetched || typeof window.fetch !== "function") {
      return;
    }
    fetched = true;
    window
      .fetch("/palette", { headers: { Accept: "application/json" } })
      .then(function (response) {
        return response && response.ok ? response.json() : null;
      })
      .then(function (data) {
        if (data && data.entries && data.entries.length) {
          entries = data.entries;
          if (overlay) {
            render();
          }
        }
      })
      .catch(function () {
        // The pages are still links, and the shortcuts still work.
      });
  }

  function available() {
    return entries || PAGES;
  }

  // matches ranks by where the query lands: a name starting with what was
  // typed is what was meant, and everything else follows in the order the
  // server sent, which is busiest first.
  function matches(query) {
    var all = available();
    if (!query) {
      return all.slice(0, SHOWN);
    }

    var needle = query.toLowerCase();
    var scored = [];
    for (var i = 0; i < all.length; i++) {
      var at = String(all[i].label).toLowerCase().indexOf(needle);
      if (at >= 0) {
        scored.push({ entry: all[i], at: at, order: i });
      }
    }
    scored.sort(function (a, b) {
      return a.at - b.at || a.order - b.order;
    });

    var out = [];
    for (var s = 0; s < scored.length && out.length < SHOWN; s++) {
      out.push(scored[s].entry);
    }
    return out;
  }

  // ------------------------------------------------------------------
  // The overlay
  // ------------------------------------------------------------------

  function make(tag, className, text) {
    var element = document.createElement(tag);
    if (className) {
      element.className = className;
    }
    if (text !== undefined) {
      element.textContent = text;
    }
    return element;
  }

  function open(showKeys) {
    load();
    if (overlay) {
      return;
    }

    restoreFocus = document.activeElement || null;

    overlay = make("div", "palette");
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    overlay.setAttribute("aria-label", "Go to");

    var box = make("div", "palette-box");
    input = document.createElement("input");
    input.type = "text";
    input.className = "palette-input";
    input.setAttribute("aria-label", "Go to a page, namespace, strategy, alert or record");
    input.setAttribute("placeholder", "Go to…");
    input.setAttribute("autocomplete", "off");

    list = make("ul", "palette-list");
    list.setAttribute("role", "listbox");

    box.appendChild(input);
    box.appendChild(list);
    box.appendChild(make("div", "palette-foot", "↑↓ to choose · Enter to go · ? for keys · Esc to close"));
    overlay.appendChild(box);
    document.body.appendChild(overlay);

    input.addEventListener("input", function () {
      active = 0;
      render();
    });
    input.addEventListener("keydown", onInputKey);
    overlay.addEventListener("click", function (event) {
      if (event.target === overlay) {
        close();
      }
    });

    active = 0;
    render(showKeys);
    if (typeof input.focus === "function") {
      input.focus();
    }
  }

  function close() {
    if (!overlay) {
      return;
    }
    if (overlay.parentNode) {
      overlay.parentNode.removeChild(overlay);
    }
    overlay = null;
    input = null;
    list = null;
    results = [];
    if (restoreFocus && typeof restoreFocus.focus === "function") {
      restoreFocus.focus();
    }
  }

  function render(showKeys) {
    if (!list) {
      return;
    }
    while (list.children && list.children.length) {
      list.removeChild(list.children[list.children.length - 1]);
    }

    if (showKeys) {
      results = [];
      for (var k = 0; k < KEYS.length; k++) {
        var help = make("li", "palette-help");
        help.appendChild(make("kbd", "palette-key", KEYS[k].keys.trim()));
        help.appendChild(make("span", "palette-detail", KEYS[k].what));
        list.appendChild(help);
      }
      return;
    }

    results = matches(input ? input.value : "");
    if (!results.length) {
      list.appendChild(make("li", "palette-none", "Nothing here by that name."));
      return;
    }

    for (var i = 0; i < results.length; i++) {
      var entry = results[i];
      var item = make("li", i === active ? "palette-item is-active" : "palette-item");
      item.setAttribute("role", "option");
      item.setAttribute("aria-selected", i === active ? "true" : "false");
      item.appendChild(make("span", "palette-kind", entry.kind || ""));
      item.appendChild(make("span", "palette-label", entry.label || ""));
      if (entry.detail) {
        item.appendChild(make("span", "palette-detail", entry.detail));
      }
      item.addEventListener("click", chooser(i));
      list.appendChild(item);
    }
  }

  function chooser(index) {
    return function () {
      active = index;
      choose();
    };
  }

  function choose() {
    var entry = results[active];
    if (!entry || !entry.url) {
      return;
    }
    close();
    go(entry.url);
  }

  // Navigating rather than swapping content: a chosen result is a URL like
  // every other choice on these pages.
  function go(url) {
    if (window.location && typeof window.location.assign === "function") {
      window.location.assign(url);
      return;
    }
    if (window.location) {
      window.location.href = url;
    }
  }

  function onInputKey(event) {
    switch (event.key) {
      case "ArrowDown":
        active = results.length ? (active + 1) % results.length : 0;
        render();
        event.preventDefault();
        break;
      case "ArrowUp":
        active = results.length ? (active + results.length - 1) % results.length : 0;
        render();
        event.preventDefault();
        break;
      case "Enter":
        choose();
        event.preventDefault();
        break;
      case "Escape":
        close();
        event.preventDefault();
        break;
      default:
        break;
    }
  }

  // ------------------------------------------------------------------
  // The keys
  // ------------------------------------------------------------------

  // A key pressed into a control belongs to that control. The filter's select
  // is on the page this is most used on, and stealing "s" from somebody typing
  // a namespace into it would be the worst kind of shortcut.
  function typingInto(target) {
    if (!target || !target.tagName) {
      return false;
    }
    var tag = String(target.tagName).toUpperCase();
    return (
      tag === "INPUT" || tag === "SELECT" || tag === "TEXTAREA" || target.isContentEditable === true
    );
  }

  function pageFor(key) {
    for (var i = 0; i < PAGES.length; i++) {
      if (PAGES[i].key === key) {
        return PAGES[i].url;
      }
    }
    return null;
  }

  document.addEventListener("keydown", function (event) {
    if (event.defaultPrevented || event.altKey) {
      return;
    }

    var key = event.key || "";

    if ((event.metaKey || event.ctrlKey) && key.toLowerCase() === "k") {
      open(false);
      event.preventDefault();
      return;
    }
    if (event.metaKey || event.ctrlKey) {
      return;
    }

    if (key === "Escape" && overlay) {
      close();
      event.preventDefault();
      return;
    }
    // Everything below is a bare letter, which belongs to whatever has focus.
    if (overlay || typingInto(event.target)) {
      return;
    }

    if (key === "?") {
      open(true);
      event.preventDefault();
      return;
    }

    if (pendingGoto) {
      window.clearTimeout(pendingGoto);
      pendingGoto = null;
      var url = pageFor(key.toLowerCase());
      if (url) {
        go(url);
        event.preventDefault();
      }
      return;
    }

    if (key === "g") {
      pendingGoto = window.setTimeout(function () {
        pendingGoto = null;
      }, GOTO_MS);
      event.preventDefault();
    }
  });

  // A shortcut nobody knows about is a shortcut nobody has. The hint is built
  // here rather than written into the layout for the same reason the Copy
  // button is: it must not exist on a page where the script did not run, where
  // it would be a control that does nothing.
  var meta = document.querySelector(".topbar-meta");
  if (meta && typeof meta.insertBefore === "function") {
    var hint = document.createElement("button");
    hint.type = "button";
    hint.className = "palette-hint";
    hint.title = "Go to a page, namespace, strategy, alert or record";
    hint.textContent = "Go to…";
    hint.addEventListener("click", function () {
      open(false);
    });
    meta.insertBefore(hint, meta.children ? meta.children[0] : null);
  }
})();
