// airs page script: live reload, Live/Offline pill, keep-reading banners,
// offline downloads, and reader notes.
// Rule: an open page never replaces good content with an error, and never
// throws away a note the reader typed.
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
  var pill = document.getElementById("airs-live");
  var relPath = (doc.rel || "").split("/").map(encodeURIComponent).join("/");
  var eventsURL = "/_airs/events/" + encodeURIComponent(doc.key);
  var renderURL = "/_airs/render/" + encodeURIComponent(doc.key) + "/" + relPath;

  var es = null;
  var stopped = false;
  var disconnected = false;
  var backoff = 1000;
  var retryTimer = null;
  var isLive = false;

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

  // Live/Offline pill. Notes can only be written while Live.
  function setLive(on) {
    isLive = on && !stopped;
    if (pill) {
      pill.dataset.state = isLive ? "live" : "offline";
      pill.textContent = isLive ? "● Live" : "● Offline";
    }
    notes.update();
  }

  function stop() {
    stopped = true;
    if (es) es.close();
    if (retryTimer) clearTimeout(retryTimer);
    showBanner("gone");
    setLive(false);
  }

  function scheduleRetry() {
    if (stopped) return;
    if (retryTimer) clearTimeout(retryTimer);
    retryTimer = setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, 10000);
  }

  // Refresh the content from the server; failures leave the page as it is.
  // While the reader is typing a note, the refresh waits: replacing the page
  // would close the iPad keyboard mid-sentence.
  var refreshPending = false;
  function refresh() {
    if (doc.kind === "listing") return refreshListing();
    if (notes.typing()) { refreshPending = true; return; }
    refreshPending = false;
    fetch(renderURL, { cache: "no-store" }).then(function (r) {
      if (r.status === 410) return stop();
      if (r.status === 404) return showBanner("missing");
      if (!r.ok) return;
      return r.json().then(function (d) {
        var article = document.getElementById("airs-article");
        if (!article) return;
        var y = window.scrollY;
        notes.beforeReplace();
        article.innerHTML = d.html;
        notes.afterReplace(d.notes || [], !!d.notesEnabled);
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
      setLive(true);
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
      setLive(false);
      probe();
    };
  }

  // --- notes ---

  var notes = (function () {
    var noop = { update: function () {}, beforeReplace: function () {}, afterReplace: function () {}, typing: function () { return false; } };
    var article = document.getElementById("airs-article");
    if (doc.kind !== "doc" || !article) return noop;

    // This rule is stored in users' notes files (block, index): never change it.
    var BLOCKS = "p,li,h1,h2,h3,h4,h5,h6,blockquote,pre,table";
    var SKIP = ".airs-note,.airs-note-box";
    var MAX_QUOTE = 2000;
    var notesURL = "/_airs/notes/" + encodeURIComponent(doc.key) + "/" + relPath;
    var list = doc.notes || [];
    var enabled = !!doc.notesEnabled;
    var box = null;
    var lastPointer = "mouse";
    var pendingSel = null;

    var addBtn = document.createElement("button");
    addBtn.type = "button";
    addBtn.id = "airs-add-note";
    addBtn.textContent = "Add note";
    addBtn.hidden = true;
    document.body.appendChild(addBtn);

    function canWrite() { return isLive && enabled && !stopped; }

    function el(tag, cls, text) {
      var e = document.createElement(tag);
      if (cls) e.className = cls;
      if (text != null) e.textContent = text;
      return e;
    }

    function blocks() {
      var all = article.querySelectorAll(BLOCKS), out = [];
      for (var i = 0; i < all.length; i++) if (!all[i].closest(SKIP)) out.push(all[i]);
      return out;
    }

    function blockOf(node) {
      var e = node && (node.nodeType === 1 ? node : node.parentElement);
      if (!e || e.closest(SKIP)) return null;
      var b = e.closest(BLOCKS);
      return b && b !== article && article.contains(b) ? b : null;
    }

    function fingerprint(b) {
      var c = b.cloneNode(true);
      var skip = c.querySelectorAll(SKIP);
      for (var i = 0; i < skip.length; i++) skip[i].remove();
      return Array.from(c.textContent.replace(/\s+/g, " ").trim()).slice(0, 80).join("");
    }

    // The nearest heading at or above position i in the block list.
    function sectionAt(bl, i) {
      for (var j = Math.min(i, bl.length - 1); j >= 0; j--) {
        if (/^H[1-6]$/.test(bl[j].tagName)) return { id: bl[j].id || "", text: bl[j].textContent.trim() };
      }
      return { id: "", text: "" };
    }

    function anchorFor(b) {
      var bl = blocks();
      var i = bl.indexOf(b);
      var sec = sectionAt(bl, i);
      return { block: fingerprint(b), index: i, section: sec.id, heading: sec.text || doc.title || "" };
    }

    function findBlock(a, bl, fps) {
      if (a.block) {
        for (var i = 0; i < bl.length; i++) if (fps[i] === a.block) return bl[i];
      }
      if (a.index >= 0 && a.index < bl.length && sectionAt(bl, a.index).id === (a.section || "")) return bl[a.index];
      if (a.section) {
        var h = document.getElementById(a.section);
        if (h && article.contains(h) && !h.closest(SKIP)) return h;
      }
      return null;
    }

    // Insert node after b and after any notes already placed there.
    function insertAfter(b, node) {
      var ref = b.nextSibling;
      while (ref && ref.nodeType === 1 && ref.classList.contains("airs-note")) ref = ref.nextSibling;
      b.parentNode.insertBefore(node, ref);
    }

    function fmtTime(iso) {
      var d = new Date(iso);
      if (!iso || isNaN(d)) return "";
      return d.toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
    }

    function card(n) {
      var c = el("div", "airs-note");
      c.dataset.id = n.id;
      if (n.quote) c.appendChild(el("div", "airs-note-quote", n.quote));
      if (n.text || !n.quote) c.appendChild(el("div", "airs-note-text", n.text || "(empty note)"));
      var t = fmtTime(n.time);
      if (n.edited) t += " · edited " + fmtTime(n.edited);
      c.appendChild(el("div", "airs-note-time", t));
      c.appendChild(binButton(n));
      return c;
    }

    // A bin icon on each card: delete without opening the editor.
    function binButton(n) {
      var btn = el("button", "airs-note-del");
      btn.type = "button";
      btn.title = "Delete note";
      btn.setAttribute("aria-label", "Delete note");
      var NS = "http://www.w3.org/2000/svg";
      var svg = document.createElementNS(NS, "svg");
      svg.setAttribute("viewBox", "0 0 24 24");
      svg.setAttribute("width", "18");
      svg.setAttribute("height", "18");
      svg.setAttribute("aria-hidden", "true");
      var path = document.createElementNS(NS, "path");
      path.setAttribute("d", "M4 7h16M10 11v6M14 11v6M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12M9 7V4h6v3");
      path.setAttribute("fill", "none");
      path.setAttribute("stroke", "currentColor");
      path.setAttribute("stroke-width", "2");
      path.setAttribute("stroke-linecap", "round");
      path.setAttribute("stroke-linejoin", "round");
      svg.appendChild(path);
      btn.appendChild(svg);
      btn.addEventListener("click", function (e) {
        e.stopPropagation(); // not an edit
        quickDelete(n.id);
      });
      return btn;
    }

    function cardFor(id) {
      var cs = article.querySelectorAll(".airs-note");
      for (var i = 0; i < cs.length; i++) if (cs[i].dataset.id === id) return cs[i];
      return null;
    }

    function render() {
      var old = article.querySelectorAll(".airs-note,.airs-notes-unplaced");
      for (var i = 0; i < old.length; i++) old[i].remove();
      var bl = blocks(), fps = bl.map(fingerprint), unplaced = [];
      list.forEach(function (n) {
        var b = findBlock(n, bl, fps);
        if (b) insertAfter(b, card(n));
        else unplaced.push(n);
      });
      if (unplaced.length) {
        var sec = el("section", "airs-notes-unplaced");
        sec.appendChild(el("h3", null, "Notes"));
        unplaced.forEach(function (n) { sec.appendChild(card(n)); });
        article.appendChild(sec);
      }
    }

    function upsert(n) {
      for (var i = 0; i < list.length; i++) if (list[i].id === n.id) { list[i] = n; return; }
      list.push(n);
    }

    function truncQuote(s) {
      s = s.trim();
      var enc = new TextEncoder();
      if (enc.encode(s).length <= MAX_QUOTE) return s;
      var cps = Array.from(s);
      while (cps.length && enc.encode(cps.join("") + "…").length > MAX_QUOTE) cps.pop();
      return cps.join("") + "…";
    }

    // --- the note box ---

    function makeBox(mode, quote, text) {
      var b = { mode: mode, quote: quote, saving: false, showCard: false };
      b.el = el("div", "airs-note-box");
      if (quote) b.el.appendChild(el("div", "airs-note-quote", quote));
      b.ta = el("textarea");
      b.ta.rows = 3;
      b.ta.placeholder = "Write a note — type, or write with Apple Pencil";
      b.ta.value = text || "";
      b.el.appendChild(b.ta);
      b.status = el("div", "airs-note-status");
      b.status.hidden = true;
      b.el.appendChild(b.status);
      b.err = el("div", "airs-note-error");
      b.err.hidden = true;
      b.el.appendChild(b.err);
      var actions = el("div", "airs-note-actions");
      if (mode === "edit") {
        b.del = el("button", "airs-note-delete", "Delete");
        b.del.type = "button";
        b.del.addEventListener("click", remove);
        actions.appendChild(b.del);
      }
      var cancel = el("button", "airs-note-cancel", "Cancel");
      cancel.type = "button";
      b.save = el("button", "airs-note-save", "Save");
      b.save.type = "button";
      actions.appendChild(cancel);
      actions.appendChild(b.save);
      b.el.appendChild(actions);
      cancel.addEventListener("click", closeBox);
      b.save.addEventListener("click", save);
      b.ta.addEventListener("blur", function () {
        if (refreshPending) setTimeout(refresh, 0);
      });
      b.ta.addEventListener("keydown", function (e) {
        if (e.key === "Escape" && !dirty()) closeBox();
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) save();
      });
      return b;
    }

    function placeBox() {
      if (!box) return;
      if (box.mode === "edit") {
        var c = cardFor(box.id);
        if (c) {
          c.hidden = !box.showCard;
          c.parentNode.insertBefore(box.el, box.showCard ? c.nextSibling : c);
          return;
        }
      }
      var bl = blocks();
      var b = findBlock(box.anchor, bl, bl.map(fingerprint));
      if (b) insertAfter(b, box.el);
      else article.appendChild(box.el);
    }

    function closeBox() {
      if (!box) return;
      box.el.remove();
      box = null;
      var hidden = article.querySelectorAll(".airs-note[hidden]");
      for (var i = 0; i < hidden.length; i++) hidden[i].hidden = false;
    }

    function dirty() {
      if (!box) return false;
      var start = box.mode === "edit" ? box.was : "";
      return box.ta.value.trim() !== String(start || "").trim();
    }

    // A box holding unsaved text is never replaced: focus it instead.
    function keepDirtyBox() {
      if (!dirty()) return false;
      box.ta.focus();
      return true;
    }

    function openAdd(b, quote) {
      if (keepDirtyBox()) return;
      closeBox();
      box = makeBox("add", quote, "");
      box.anchor = anchorFor(b);
      insertAfter(b, box.el);
      update();
      box.ta.focus();
    }

    function openEdit(id) {
      var n = null;
      for (var i = 0; i < list.length; i++) if (list[i].id === id) n = list[i];
      if (!n || keepDirtyBox()) return;
      closeBox();
      box = makeBox("edit", n.quote, n.text);
      box.id = id;
      box.was = n.text;
      box.anchor = { block: n.block, index: n.index, section: n.section };
      placeBox();
      update();
      box.ta.focus();
    }

    function showErr(msg) {
      if (!box) return;
      box.err.textContent = msg;
      box.err.hidden = !msg;
    }

    function save() {
      if (!box || box.saving || !canWrite()) return update();
      var b = box;
      var text = b.ta.value;
      var body = b.mode === "add"
        ? { block: b.anchor.block, index: b.anchor.index, section: b.anchor.section, heading: b.anchor.heading, quote: b.quote || "", text: text }
        : { id: b.id, was: b.was, text: text };
      b.saving = true;
      b.ta.readOnly = true; // nothing typed now could be saved
      showErr("");
      update();
      fetch(notesURL, {
        method: b.mode === "add" ? "POST" : "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
        cache: "no-store",
      }).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (d) { return { r: r, d: d }; });
      }).then(function (res) {
        b.saving = false;
        b.ta.readOnly = false;
        var r = res.r, d = res.d;
        if (r.ok) {
          upsert(d);
          if (box === b) closeBox();
          render();
          placeBox();
        } else if (r.status === 409 && d.note) {
          b.was = d.note.text;
          b.showCard = true;
          upsert(d.note);
          render();
          placeBox();
          showErr("Changed on the Mac — showing the new version");
        } else if (r.status === 410) {
          stop();
        } else {
          showErr("Couldn't save — " + String(d.error || "error " + r.status).replace(/^couldn't save note: /, ""));
        }
        update();
      }).catch(function () {
        b.saving = false;
        b.ta.readOnly = false;
        showErr("Couldn't save — not connected");
        update();
      });
    }

    var CONFIRM_DELETE = "Delete this note? This can't be undone.";

    function sendDelete(id, was) {
      return fetch(notesURL, {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id: id, was: was }),
        cache: "no-store",
      }).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (d) { return { r: r, d: d }; });
      });
    }

    function dropNote(id) {
      list = list.filter(function (n) { return n.id !== id; });
      if (box && box.id === id) closeBox();
      render();
      placeBox();
    }

    // Delete from the card's bin icon, after asking: there is no undo.
    function quickDelete(id) {
      var n = null;
      for (var i = 0; i < list.length; i++) if (list[i].id === id) n = list[i];
      if (!n || !canWrite()) return;
      if (!window.confirm(CONFIRM_DELETE)) return;
      sendDelete(n.id, n.text).then(function (res) {
        var r = res.r, d = res.d;
        if (r.ok || r.status === 404) { // 404: already gone on the Mac
          dropNote(n.id);
        } else if (r.status === 409 && d.note) {
          upsert(d.note);
          render();
          placeBox();
          window.alert("This note was changed on the Mac. It now shows the new version; delete again if you still want to.");
        } else if (r.status === 410) {
          stop();
        } else {
          window.alert("Couldn't delete — " + String(d.error || "error " + r.status).replace(/^couldn't save note: /, ""));
        }
        update();
      }).catch(function () {
        window.alert("Couldn't delete — not connected");
      });
    }

    // Delete the note being edited, after asking: there is no undo.
    function remove() {
      if (!box || box.mode !== "edit" || box.saving || !canWrite()) return update();
      if (!window.confirm(CONFIRM_DELETE)) return;
      var b = box;
      b.saving = true;
      b.ta.readOnly = true;
      showErr("");
      update();
      sendDelete(b.id, b.was).then(function (res) {
        b.saving = false;
        b.ta.readOnly = false;
        var r = res.r, d = res.d;
        if (r.ok || r.status === 404) { // 404: already gone on the Mac
          dropNote(b.id);
        } else if (r.status === 409 && d.note) {
          b.was = d.note.text;
          b.showCard = true;
          upsert(d.note);
          render();
          placeBox();
          showErr("Changed on the Mac — showing the new version; delete again if you still want to");
        } else if (r.status === 410) {
          stop();
        } else {
          showErr("Couldn't delete — " + String(d.error || "error " + r.status).replace(/^couldn't save note: /, ""));
        }
        update();
      }).catch(function () {
        b.saving = false;
        b.ta.readOnly = false;
        showErr("Couldn't delete — not connected");
        update();
      });
    }

    // Reflect whether notes can be written right now.
    function update() {
      var ok = canWrite();
      article.classList.toggle("airs-notes-on", ok);
      if (!ok) addBtn.hidden = true;
      if (!box) return;
      box.save.disabled = !ok || box.saving;
      if (box.del) box.del.disabled = !ok || box.saving;
      var msg = "";
      if (stopped) msg = "No longer shared — this note can't be saved; copy it if you want to keep it";
      else if (!isLive) msg = "Offline — your note is kept here; save it once the page is Live again";
      else if (!enabled) msg = "Notes can't be saved from this network";
      box.status.textContent = msg;
      box.status.hidden = !msg;
    }

    // --- input ---

    article.addEventListener("pointerdown", function (e) { lastPointer = e.pointerType || "mouse"; }, true);

    article.addEventListener("click", function (e) {
      var t = e.target;
      if (!t.closest) return;
      var c = t.closest(".airs-note");
      if (c && article.contains(c)) {
        if (canWrite() && c.dataset.id) openEdit(c.dataset.id);
        return;
      }
      if (t.closest("a,button,input,textarea,select,label,summary,.airs-note-box")) return;
      var sel = window.getSelection();
      if (sel && !sel.isCollapsed && sel.toString().trim()) return;
      if (box) {
        if (!box.ta.value.trim()) closeBox(); // tapping away closes an empty box
        return;
      }
      if (lastPointer !== "touch" && lastPointer !== "pen") return; // mouse clicks never open a box
      if (!canWrite()) return;
      var b = blockOf(t);
      if (b) openAdd(b, "");
    });

    function onSelection() {
      var sel = window.getSelection();
      if (!canWrite() || !sel || sel.isCollapsed || !sel.rangeCount) { addBtn.hidden = true; return; }
      var text = sel.toString().trim();
      var range = sel.getRangeAt(0);
      var b = blockOf(range.startContainer);
      if (!text || !b) { addBtn.hidden = true; return; }
      pendingSel = { quote: truncQuote(text), block: b };
      var rect = range.getBoundingClientRect();
      addBtn.hidden = false;
      var w = addBtn.offsetWidth || 110;
      addBtn.style.left = Math.max(8, Math.min(rect.left + rect.width / 2 - w / 2, window.innerWidth - w - 8)) + "px";
      addBtn.style.top = Math.max(8, Math.min(rect.bottom + 10, window.innerHeight - 60)) + "px";
    }
    document.addEventListener("selectionchange", onSelection);
    window.addEventListener("scroll", function () { if (!addBtn.hidden) onSelection(); }, { passive: true });

    addBtn.addEventListener("pointerdown", function (e) { e.preventDefault(); }); // keep the selection
    addBtn.addEventListener("mousedown", function (e) { e.preventDefault(); });
    addBtn.addEventListener("click", function () {
      if (!pendingSel || !canWrite()) return;
      var p = pendingSel;
      pendingSel = null;
      addBtn.hidden = true;
      var sel = window.getSelection();
      if (sel) sel.removeAllRanges();
      openAdd(p.block, p.quote);
    });

    render();
    update();

    return {
      update: update,
      typing: function () { return !!box && document.activeElement === box.ta; },
      beforeReplace: function () { if (box) box.el.remove(); },
      afterReplace: function (newList, newEnabled) {
        list = newList;
        enabled = newEnabled;
        render();
        placeBox();
        update();
      },
    };
  })();

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
    // Notes are part of the saved page; the editing box is not.
    var boxes = clone.querySelectorAll(".airs-note-box,.airs-note-del");
    for (var j = 0; j < boxes.length; j++) boxes[j].remove();
    var hidden = clone.querySelectorAll(".airs-note[hidden]");
    for (var k = 0; k < hidden.length; k++) hidden[k].removeAttribute("hidden");
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

  setLive(false);
  connect();
})();
