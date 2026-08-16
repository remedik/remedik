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
        var next = doc.getElementById("content");
        if (!next) {
          return;
        }
        content.innerHTML = next.innerHTML;

        var fresh = doc.getElementById("updated");
        if (updated && fresh) {
          updated.textContent = fresh.textContent;
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
