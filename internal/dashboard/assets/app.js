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
      label.textContent = enabled ? "Auto" : "Paused";
    }
    toggle.title = enabled
      ? "Reloading the page content every 10 seconds"
      : "Automatic reloading is paused";
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
