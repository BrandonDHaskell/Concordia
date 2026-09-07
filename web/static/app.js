// Live update: when the daemon signals a data change, silently re-fetch the
// current view, keeping the active filters. Hand-written, no build step.
(function () {
  if (typeof EventSource === "undefined") return;

  var es = new EventSource("/events");
  es.addEventListener("change", function () {
    var here = location.pathname + location.search;
    if (window.htmx) {
      htmx.ajax("GET", here, { target: "#view", swap: "innerHTML" });
    } else {
      location.reload();
    }
  });
})();
