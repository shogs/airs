// airs page script: live reload, keep-reading banners, offline downloads.
// Rule: an open page never replaces good content with an error.
(function () {
  "use strict";

  var MSG = {
    gone: "No longer shared — this is the last version",
    offline: "Offline — live updates paused, retrying",
    missing: "File missing on disk",
  };

  var docEl = document.getElementById("airs-doc");
  if (!docEl) return;
  var doc;
  try { doc = JSON.parse(docEl.textContent); } catch (e) { return; }

  var banner = document.getElementById("airs-banner");
  var eventsURL = "/_airs/events/" + encodeURIComponent(doc.key);
  var renderURL = "/_airs/render/" + encodeURIComponent(doc.key) + "/" +
    (doc.rel || "").split("/").map(encodeURIComponent).join("/");

  var es = null;
  var stopped = false;
  var disconnected = false;
  var backoff = 1000;
  var retryTimer = null;

  function showBanner(kind) {
    if (!banner) return;
    banner.textContent = MSG[kind];
    banner.hidden = false;
    banner.dataset.kind = kind;
  }
  function clearBanner() {
    if (banner && banner.dataset.kind !== "gone") {
      banner.hidden = true;
      banner.dataset.kind = "";
    }
  }

  function stop() {
    stopped = true;
    if (es) es.close();
    if (retryTimer) clearTimeout(retryTimer);
    showBanner("gone");
  }

  function scheduleRetry() {
    if (stopped) return;
    if (retryTimer) clearTimeout(retryTimer);
    retryTimer = setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, 10000);
  }

  // Refresh the content from the server; failures leave the page as it is.
  function refresh() {
    if (doc.kind === "listing") return refreshListing();
    fetch(renderURL, { cache: "no-store" }).then(function (r) {
      if (r.status === 410) return stop();
      if (r.status === 404) return showBanner("missing");
      if (!r.ok) return;
      return r.json().then(function (d) {
        var article = document.getElementById("airs-article");
        if (!article) return;
        var y = window.scrollY;
        article.innerHTML = d.html;
        window.scrollTo(0, y);
        doc.markdown = d.markdown;
        doc.title = d.title;
        document.title = d.title;
        clearBanner();
      });
    }).catch(function () { /* keep content; reconnect logic handles banner */ });
  }

  function refreshListing() {
    fetch(location.href, { cache: "no-store" }).then(function (r) {
      if (!r.ok) return;
      return r.text().then(function (html) {
        var next = new DOMParser().parseFromString(html, "text/html").querySelector("main");
        var cur = document.querySelector("main");
        if (next && cur) cur.innerHTML = next.innerHTML;
        clearBanner();
      });
    }).catch(function () {});
  }

  // After the stream fails, ask the server why (EventSource hides status codes).
  function probe() {
    var ctl = typeof AbortController !== "undefined" ? new AbortController() : null;
    fetch(eventsURL, { cache: "no-store", signal: ctl && ctl.signal }).then(function (r) {
      if (ctl) ctl.abort();
      if (r.status === 410) return stop();
      if (r.status !== 200) showBanner("offline");
      scheduleRetry();
    }).catch(function () {
      showBanner("offline");
      scheduleRetry();
    });
  }

  function connect() {
    if (stopped || typeof EventSource === "undefined") return;
    es = new EventSource(eventsURL);
    es.addEventListener("ready", function () {
      backoff = 1000;
      if (disconnected) {
        disconnected = false;
        refresh(); // catch up on edits made while we were away
      }
      if (!banner || banner.dataset.kind !== "missing") clearBanner();
    });
    es.addEventListener("change", refresh);
    es.addEventListener("gone", stop);
    es.onerror = function () {
      if (stopped) return;
      es.close();
      disconnected = true;
      probe();
    };
  }

  // --- downloads (work without the server) ---

  function save(blob, name) {
    var url = URL.createObjectURL(blob);
    var a = document.createElement("a");
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 10000);
  }

  function baseName() {
    return (doc.file || "document.md").replace(/\.(md|markdown)$/i, "");
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
    });
  }

  function downloadHTML() {
    var live = document.getElementById("airs-article");
    if (!live) return;
    var clone = live.cloneNode(true);
    var liveImgs = live.querySelectorAll("img");
    var cloneImgs = clone.querySelectorAll("img");
    for (var i = 0; i < liveImgs.length; i++) {
      var img = liveImgs[i];
      if (!img.complete || !img.naturalWidth) continue;
      try {
        var c = document.createElement("canvas");
        c.width = img.naturalWidth;
        c.height = img.naturalHeight;
        c.getContext("2d").drawImage(img, 0, 0);
        cloneImgs[i].src = c.toDataURL("image/png");
        cloneImgs[i].removeAttribute("srcset");
      } catch (e) { /* cross-origin or unsupported: keep original src */ }
    }
    var style = document.getElementById("airs-style");
    var html = "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n" +
      "<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n" +
      "<meta name=\"color-scheme\" content=\"light dark\">\n" +
      "<title>" + escapeHTML(doc.title || baseName()) + "</title>\n" +
      "<style>" + (style ? style.textContent : "") + "</style>\n</head>\n<body>\n<main>\n" +
      "<article class=\"markdown-body\">" + clone.innerHTML + "</article>\n</main>\n</body>\n</html>\n";
    save(new Blob([html], { type: "text/html" }), baseName() + ".html");
  }

  function downloadMD() {
    save(new Blob([doc.markdown || ""], { type: "text/markdown" }), doc.file || "document.md");
  }

  var dlHTML = document.getElementById("airs-dl-html");
  var dlMD = document.getElementById("airs-dl-md");
  if (dlHTML) dlHTML.addEventListener("click", downloadHTML);
  if (dlMD) dlMD.addEventListener("click", downloadMD);

  connect();
})();
