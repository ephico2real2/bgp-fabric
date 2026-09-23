/* bgp-fabric dashboard — plain JS, Cytoscape vendored. */
(function () {
  const ui = globalThis.bgpUI;
  const params = new URLSearchParams(location.search);
  let selected = params.get("router") || "spine";
  let selectedWhy = params.get("router") ? "url" : "default";
  let snap = null;
  let cy = null;
  let focused = "";
  let backoff = 500;
  let wsAttempts = 0;
  let lastEventId = 0;
  let eventsPaused = false;
  const seenEvent = {};
  const eventStore = [];
  const pausedBuffer = [];
  const EVENT_CAP = 500;
  const shotHover = params.get("hover") || "";
  const shotTab = params.get("tab") || "";

  const $ = (id) => document.getElementById(id);
  const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

  function colours() {
    return {
      fg: css("--fg"),
      muted: css("--muted"),
      panel: css("--panel"),
      est: css("--est"),
      trans: css("--trans"),
      down: css("--down"),
      stale: css("--stale"),
      focus: css("--focus"),
      picked: css("--picked"),
      hover: css("--hover-fill"),
      accept: css("--accept"),
      late: css("--late"),
      critical: css("--critical"),
      select: css("--select"),
    };
  }

  function applyReady() {
    document.title = "bgp-fabric — live";
    document.body.dataset.ready = "1";
  }

  function graphMetrics() {
    const g = $("graph");
    const w = g.clientWidth || 1200;
    const h = g.clientHeight || 400;
    const k = Math.min(w / 1200, h / 620, 1);
    return {
      w: w,
      h: h,
      fontSize: Math.max(11, Math.round(12 * Math.max(k, 0.85))),
      padding: Math.max(6, Math.round(8 * Math.max(k, 0.75))),
    };
  }

  function renderHeader() {
    if (!snap) {
      $("poll").textContent = "waiting for the first poll";
      $("reach").textContent = "waiting for the first poll";
      $("sess").textContent = "waiting for the first poll";
      return;
    }
    $("poll").textContent = "poll " + (snap.poll || "—");
    const missing = (snap.routers || []).filter((r) => !r.reachable);
    if (!snap.routerCount) {
      $("reach").textContent = "waiting for the first poll";
    } else if (missing.length) {
      const bits = missing.map((r) => {
        const when = r.lastSeen ? ui.relativeTime(r.lastSeen) : "never";
        return "cannot reach " + r.name + " — last seen " + when;
      });
      $("reach").textContent = "routers " + snap.reachable + "/" + snap.routerCount + " · " + bits.join("; ");
    } else {
      $("reach").textContent = "routers " + snap.reachable + "/" + snap.routerCount;
    }
    $("sess").textContent = "fabric sessions " + snap.established + "/" + snap.sessionCount
      + " · server sessions " + (snap.serverEstablished || 0) + "/" + (snap.serverSessions || 0);
  }

  function rankState(s) {
    if (s === "established") return 1;
    if (s === "transitional") return 2;
    if (s === "stale") return 3;
    return 4;
  }

  function selectedState() {
    if (!snap) return "down";
    const r = (snap.routers || []).find((x) => x.name === selected);
    if (r && !r.reachable) return "stale";
    let worst = "";
    for (const e of snap.edges || []) {
      if (e.source !== selected && e.target !== selected) continue;
      if (!worst || rankState(e.state) > rankState(worst)) worst = e.state;
    }
    if (worst) return worst;
    if (r) return r.reachable ? "established" : "down";
    return "down";
  }

  function whyText() {
    if (selectedWhy === "url") return "opened with ?router=" + selected + " — click another node, or Tab to it and press Enter";
    if (selectedWhy === "click") return selected + " selected by click";
    if (selectedWhy === "keyboard") return selected + " selected from the keyboard";
    return selected + " is selected so there is a RIB to read — click a node, or Tab to it and press Enter";
  }

  function selectedRouter() {
    return (snap && snap.routers || []).find((x) => x.name === selected) || null;
  }

  // renderNeighbours is the per-router peer list: who this router talks to,
  // what state each session is in, and how many prefixes each way. The page
  // carried all of it and showed none of it — the RIB answers "what do I know"
  // and this answers "who told me", which is the other half of reading a
  // router.
  function renderNeighbours() {
    const tb = $("neighbours");
    const count = $("nbr-count");
    if (!tb) return;
    tb.replaceChildren();
    if (!snap) { count.textContent = ""; return; }
    const rows = (snap.sessions || [])
      .filter((s) => s.router === selected)
      .sort((a, b) => (a.peer < b.peer ? -1 : a.peer > b.peer ? 1 : 0));
    count.textContent = rows.length ? "(" + rows.length + ")" : "";
    if (!rows.length) {
      const tr = document.createElement("tr");
      const td = document.createElement("td");
      td.colSpan = 6;
      td.textContent = selected + " has no sessions in this snapshot";
      tr.appendChild(td);
      tb.appendChild(tr);
      return;
    }
    for (const s of rows) {
      const tr = document.createElement("tr");
      if (s.stale) tr.className = "stale-row";
      // The address on its own line and the hostname beneath it, truncated.
      // FRR reports a cluster node's full name — `eg-poc1-colima-control-plane`
      // — and on one line in a 32% column that wrapped to 21 lines (measured).
      const hostname = s.hostname
        ? "<div class=\"host\" title=\"" + ui.esc(s.hostname) + "\">" + ui.esc(s.hostname) + "</div>"
        : "";
      tr.innerHTML =
        "<td class=\"nexthop\">" + ui.esc(s.peer) + hostname + "</td>" +
        "<td>" + (s.peerAsn ? "AS" + ui.esc(String(s.peerAsn)) : "") + "</td>" +
        "<td class=\"" + ui.stateClass(s.state, s.stale) + "\">" + ui.esc(s.state || "") + "</td>" +
        "<td class=\"num\">" + ui.esc(String(s.pfxRcd == null ? "" : s.pfxRcd)) + "</td>" +
        "<td class=\"num\">" + ui.esc(String(s.pfxSnt == null ? "" : s.pfxSnt)) + "</td>" +
        "<td class=\"num\">" + ui.esc(s.uptime || "") + "</td>";
      tb.appendChild(tr);
    }
  }

  function ribRows() {
    const tb = $("rib");
    tb.replaceChildren();
    $("rib-name").textContent = selected;
    $("rib-dot").className = "dot " + selectedState();
    $("rib-why").textContent = whyText();
    const pane = $("rib-pane");
    if (!snap) {
      pane.classList.remove("stale");
      $("rib-status").textContent = "waiting for the first poll";
      renderNeighbours();
      return;
    }
    renderNeighbours();
    const router = selectedRouter();
    const node = (snap.nodes || []).find((n) => n.id === selected);
    const unreachable = !!(router && !router.reachable);
    pane.classList.toggle("stale", unreachable);
    if (unreachable) {
      const when = router.lastSeen ? ui.relativeTime(router.lastSeen) : "never";
      $("rib-status").textContent = "cannot reach " + selected + " — last seen " + when + ". Rows are last-known, not live.";
    } else {
      $("rib-status").textContent = "";
    }
    let rows = snap.routes || [];
    if (node && node.kind === "external") {
      rows = rows.filter((r) => r.peerId === selected);
    } else {
      rows = rows.filter((r) => r.router === selected);
    }
    const groups = ui.groupECMP(rows);
    if (!groups.length) {
      const tr = document.createElement("tr");
      const td = document.createElement("td");
      td.colSpan = 7;
      td.textContent = unreachable
        ? "no last-known routes for " + selected
        : selected + " has no routes in this snapshot";
      tr.appendChild(td);
      tb.appendChild(tr);
      return;
    }
    for (const g of groups) {
      g.paths.forEach((r, i) => {
        const tr = document.createElement("tr");
        if (i > 0) tr.className = "path";
        tr.innerHTML =
          "<td class=\"best\">" + (r.bestpath ? "*" : "") + "</td>" +
          "<td class=\"prefix\">" + (i === 0 ? ui.esc(g.prefix) : "") + "</td>" +
          "<td class=\"nexthop\">" + ui.esc(r.nexthop || "") + "</td>" +
          "<td>" + ui.esc(r.path || "") + "</td>" +
          // locPrf and metric are omitempty in the JSON, so an absent value is
          // FRR not sending one rather than a zero. Blank says that; "0" would
          // claim a local preference of nought.
          "<td class=\"num\">" + (r.locPrf == null ? "" : ui.esc(String(r.locPrf))) + "</td>" +
          "<td class=\"num\">" + (r.metric == null ? "" : ui.esc(String(r.metric))) + "</td>" +
          "<td>" + ui.esc(ui.fromLabel(r.peerId)) + "</td>";
        tb.appendChild(tr);
      });
    }
    if (cy) markPicked();
    renderEvents();
  }

  function setHover(edge) {
    const card = $("hover-card");
    if (!edge) {
      card.hidden = true;
      card.textContent = "";
      return;
    }
    const text = ui.edgeCardText(edge);
    if (!text) {
      card.hidden = true;
      card.textContent = "";
      return;
    }
    card.textContent = text;
    card.hidden = false;
  }

  function edgeData(e) {
    return {
      aRouter: e.aRouter, bRouter: e.bRouter,
      aPeer: e.aPeer, bPeer: e.bPeer,
      aState: e.aState, bState: e.bState,
      aUptime: e.aUptime, bUptime: e.bUptime,
      aPfxRcd: e.aPfxRcd, bPfxRcd: e.bPfxRcd,
      aPfxSnt: e.aPfxSnt, bPfxSnt: e.bPfxSnt,
    };
  }

  function markPicked() {
    if (!cy) return;
    cy.nodes().removeClass("picked");
    const n = cy.getElementById(selected);
    if (n && n.nonempty && n.nonempty()) n.addClass("picked");
  }

  function markFocused(id) {
    focused = id || "";
    if (!cy) return;
    cy.nodes().removeClass("focused");
    if (!focused) return;
    const n = cy.getElementById(focused);
    if (n && n.nonempty && n.nonempty()) n.addClass("focused");
  }

  function selectRouter(id, why) {
    // the strip describes the SELECTED router, so it moves with the selection
    selected = id;
    selectedWhy = why;
    const url = new URL(location.href);
    url.searchParams.set("router", id);
    history.replaceState({}, "", url);
    ribRows();
    renderSignalStrip();
    markPicked();
  }

  function rebuildNodeKeys() {
    const list = $("node-keys");
    list.replaceChildren();
    for (const n of (snap && snap.nodes) || []) {
      const li = document.createElement("li");
      const btn = document.createElement("button");
      btn.type = "button";
      btn.textContent = n.id;
      btn.dataset.node = n.id;
      btn.addEventListener("focus", () => markFocused(n.id));
      btn.addEventListener("click", () => selectRouter(n.id, "keyboard"));
      li.appendChild(btn);
      list.appendChild(li);
    }
  }

  function renderGraph() {
    const empty = $("graph-empty");
    if (!snap) {
      empty.hidden = false;
      empty.textContent = "waiting for the first poll";
      return;
    }
    if (typeof cytoscape === "undefined") {
      empty.hidden = false;
      empty.textContent = "graph library failed to load";
      return;
    }
    empty.hidden = true;
    const c = colours();
    const m = graphMetrics();
    const laid = ui.layoutGraph({
      nodes: snap.nodes || [],
      sessions: snap.sessions || [],
      width: m.w,
      height: m.h,
      fontSize: m.fontSize,
      padding: m.padding,
    });
    const elements = [];
    for (const n of snap.nodes || []) {
      // A node the reader has moved keeps where they put it. renderGraph runs
      // again on every state change, so without this the next tick would throw
      // their arrangement away and snap everything back to the layout.
      const p = placed[n.id] || laid.pos[n.id] || { x: m.w / 2, y: m.h / 2 };
      elements.push({
        data: Object.assign({ id: n.id, label: n.label, kind: n.kind }, nodeSignalData(n.id)),
        position: p,
      });
    }
    for (const e of snap.edges || []) {
      elements.push({
        data: Object.assign({
          id: e.id,
          source: e.source,
          target: e.target,
          state: e.state,
        }, edgeData(e)),
      });
    }
    const style = [
      {
        selector: "node",
        style: {
          label: "data(label)",
          "text-wrap": "wrap",
          "text-valign": "center",
          "text-halign": "center",
          "font-size": m.fontSize,
          "font-family": "ui-sans-serif, system-ui, sans-serif",
          color: c.fg,
          "background-color": c.panel,
          "border-width": 2,
          "border-color": c.fg,
          width: "label",
          height: "label",
          padding: m.padding,
          shape: "round-rectangle",
        },
      },
      {
        selector: "node[kind = \"external\"]",
        style: { shape: "ellipse", "border-style": "dashed" },
      },
      // "Accepting traffic" is a DIFFERENT statement from "Established": a
      // leaf whose listen range has members is carrying a cluster right now,
      // one whose range is empty is up and idle. FRR says which, so this is
      // read and not inferred.
      {
        selector: "node[accepting = 1]",
        style: { "border-color": c.accept, "border-width": 4 },
      },
      {
        selector: "node[signal = \"late\"]",
        style: { "border-color": c.late, "border-style": "double", "border-width": 5 },
      },
      {
        selector: "node[signal = \"critical\"]",
        style: { "border-color": c.critical, "border-style": "double", "border-width": 6 },
      },
      // A router the page cannot measure says so by fading, rather than
      // sitting there looking healthy.
      {
        selector: "node[known = 0]",
        style: { "border-style": "dotted", opacity: 0.75 },
      },
      {
        selector: "node.hover",
        style: { "background-color": c.hover, "border-width": 3 },
      },
      {
        selector: "node.picked",
        style: {
          "border-color": c.focus,
          "border-width": 4,
          "background-color": c.hover,
          "overlay-opacity": 0.16,
          "overlay-color": c.focus,
          "overlay-padding": 6,
        },
      },
      {
        selector: "node.focused",
        style: {
          "border-color": c.focus,
          "border-width": 3,
          "overlay-opacity": 0.18,
          "overlay-color": c.focus,
          "overlay-padding": 5,
        },
      },
      {
        selector: "edge",
        style: {
          width: 3,
          "curve-style": "bezier",
          "line-color": c.down,
          "target-arrow-color": c.down,
          "source-arrow-color": c.down,
          label: "",
          "font-size": 10,
          color: c.muted,
        },
      },
      // LAST among the node rules on purpose: Cytoscape takes the last matching
      // declaration, and node.picked came after this and erased it, so the
      // router you were reading never looked selected.
      //
      // Selection uses the OUTLINE, drawn outside the node. The border already
      // carries three other meanings (session state, accepting a cluster, the
      // picked router) and the overlay carries hover, so a fourth on either was
      // unreadable. An outline also follows the node's own shape and size,
      // which is why the small dashed ellipses looked obviously selected while
      // the wide routers did not: overlay-padding is an absolute number.
      {
        selector: "node:selected",
        style: {
          "outline-color": c.select,
          "outline-width": 4,
          "outline-opacity": 1,
          "outline-offset": 3,
        },
      },
      { selector: "edge[state = \"established\"]", style: { "line-color": c.est } },
      { selector: "edge[state = \"transitional\"]", style: { "line-color": c.trans } },
      { selector: "edge[state = \"stale\"]", style: { "line-color": c.stale, "line-style": "dashed" } },
      { selector: "edge[state = \"down\"]", style: { "line-color": c.down } },
    ];
    if (!cy) {
      cy = cytoscape({
        container: $("graph"),
        elements: elements,
        style: style,
        layout: { name: "preset" },
        userZoomingEnabled: false,
        userPanningEnabled: false,
        // Dragging the background draws a selection box, and Cytoscape moves
        // every selected node when one of them is grabbed.
        boxSelectionEnabled: true,
        selectionType: "additive",
      });
      window.__cy = cy;
      cy.on("tap", "node", (ev) => selectRouter(ev.target.id(), "click"));
      cy.on("mouseover", "node", (ev) => {
        ev.target.addClass("hover");
        $("graph").style.cursor = "pointer";
      });
      cy.on("mouseout", "node", (ev) => {
        ev.target.removeClass("hover");
        $("graph").style.cursor = "";
      });
      cy.on("dragfree", "node", (ev) => {
        const sel = cy.$("node:selected");
        recordPositions(sel.length > 1 && sel.contains(ev.target) ? sel : ev.target);
        updateSelectionCount();
      });
      cy.on("select unselect", "node", () => updateSelectionCount());
      cy.on("mouseover", "edge", (ev) => setHover(ev.target.data()));
      cy.on("mouseout", "edge", () => { if (!shotHover) setHover(null); });
      // Label-sized nodes have no resolved width on the instance's first
      // draw (measured 2026-09-20: pstyle("width").pfValue empty, visible()
      // false, one node painted — 12,158 px versus 79,955 after any second
      // render) until a style update; the cy.json() path below applies one,
      // the constructor does not. Without this the pane shows a single node
      // until the first WebSocket state arrives.
      cy.style().update();
    } else {
      cy.json({ elements: elements, style: style });
      cy.layout({ name: "preset" }).run();
    }
    cy.zoom(1);
    cy.pan({ x: 0, y: 0 });
    // Layout used an estimate of label width. After style().update() the
    // painted box is known — pull any node that still sits past the pane
    // (375 px / leaf2 was the measured case) back inside.
    const paneW = $("graph").clientWidth || m.w;
    const paneH = $("graph").clientHeight || m.h;
    const inset = 16;
    cy.nodes().forEach((n) => {
      const bb = n.boundingBox({ includeLabels: true, includeOverlays: false });
      // A node the reader placed is left where they put it — UNLESS none of it
      // is on the canvas. Arranging at one window width and opening at another
      // otherwise loses nodes with nothing on the page to say so: measured
      // 2026-09-21, a graph arranged at 1400px and reopened at 1200px showed
      // 4 of 6 nodes, no JS error, and the counter still claiming "2 placed by
      // hand". The rescue is for THIS render only — the recorded position is
      // untouched, so the arrangement returns at the width that made it.
      if (placed[n.id()] && bb.x2 > 0 && bb.x1 < paneW && bb.y2 > 0 && bb.y1 < paneH) return;
      let x = n.position("x");
      let y = n.position("y");
      const hw = bb.w / 2, hh = bb.h / 2;
      if (x - hw < inset) x = inset + hw;
      if (x + hw > paneW - inset) x = paneW - inset - hw;
      if (y - hh < inset) y = inset + hh;
      if (y + hh > paneH - inset) y = paneH - inset - hh;
      n.position({ x: x, y: y });
    });
    markPicked();
    if (focused) markFocused(focused);
    rebuildNodeKeys();
    if (shotHover) {
      const e = (snap.edges || []).find((x) => x.id === shotHover) || (snap.edges || [])[0];
      if (e) setHover(Object.assign({ id: e.id }, edgeData(e)));
    }
    writeMeasure(laid);
  }

  function writeMeasure(laid) {
    if (!cy) return;
    let painted = 0;
    const boxes = {};
    cy.nodes().forEach((n) => {
      const bb = n.renderedBoundingBox();
      boxes[n.id()] = { x1: bb.x1, y1: bb.y1, x2: bb.x2, y2: bb.y2, w: bb.w, h: bb.h };
      if (n.visible()) painted += bb.w * bb.h;
    });
    const overlaps = [];
    const ids = Object.keys(boxes);
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const a = boxes[ids[i]], b = boxes[ids[j]];
        const iw = Math.min(a.x2, b.x2) - Math.max(a.x1, b.x1);
        const ih = Math.min(a.y2, b.y2) - Math.max(a.y1, b.y1);
        if (iw > 0 && ih > 0) overlaps.push({ a: ids[i], b: ids[j], w: +iw.toFixed(1), h: +ih.toFixed(1) });
      }
    }
    document.body.dataset.measure = JSON.stringify({
      painted: Math.round(painted),
      nodeCount: ids.length,
      visible: cy.nodes().filter((n) => n.visible()).length,
      pane: graphMetrics(),
      layoutOverlaps: laid ? laid.overlaps.length : 0,
      paintedOverlaps: overlaps,
    });
  }

  function eventItem(ev) {
    const li = document.createElement("li");
    const dot = document.createElement("i");
    dot.className = "dot " + (ev.kind || "session");
    const ts = document.createElement("span");
    ts.className = "ts";
    ts.dataset.ts = ev.ts || "";
    ts.textContent = ui.relativeTime(ev.ts);
    ts.title = ev.ts || "";
    const kind = document.createElement("span");
    kind.className = "kind";
    kind.textContent = ev.kind || "";
    const router = document.createElement("span");
    router.className = "router";
    router.textContent = ev.router || "";
    const who = document.createElement("span");
    who.className = "who";
    who.textContent = ev.prefix || ev.peer || "";
    const change = document.createElement("span");
    // The dot says what KIND of event this is; the text now says what STATE it
    // reached. A list where "Established" and "Idle" are the same colour makes
    // the reader parse every line to find the one that matters.
    //
    // Only a SESSION event carries a state. A route event's from/to are
    // nexthop addresses, and colouring those by state renders a bestpath move
    // in the muted "unrecognised state" style — the address is not a state.
    change.className = "change" + (ev.kind === "session" ? " " + ui.stateClass(ev.to || ev.from, false) : "");
    if (ev.from || ev.to) change.textContent = (ev.from || "") + "→" + (ev.to || "");
    li.append(dot, ts, kind, router, who, change);
    return li;
  }

  function currentFilter() {
    return {
      kind: $("ev-kind").value,
      router: $("ev-selected").checked ? selected : "",
    };
  }

  function renderEvents() {
    const list = $("events");
    list.replaceChildren();
    const filter = currentFilter();
    let shown = 0;
    for (const ev of eventStore) {
      if (!ui.eventMatches(ev, filter)) continue;
      list.appendChild(eventItem(ev));
      shown += 1;
    }
    // A blank pane is indistinguishable from a broken one. Say which of the
    // three reasons it is: nothing has happened yet, the filter excludes
    // everything that has, or this kind of event does not occur on a fabric
    // that is behaving. `router` is the one that bites — it means a router
    // went unreachable, and on a healthy fabric it is empty for ever.
    const empty = $("events-empty");
    empty.hidden = shown > 0;
    if (shown === 0) empty.textContent = emptyEventsReason(filter);
  }

  function emptyEventsReason(filter) {
    if (!eventStore.length) return "no events yet — the fabric has not changed since this page loaded";
    const kinds = { session: "session", route: "route", router: "router" };
    const bits = [];
    if (filter.kind && kinds[filter.kind]) {
      const held = eventStore.some((e) => e.kind === filter.kind);
      if (!held && filter.kind === "router") {
        return "no router events: a router event is a router going unreachable or coming back, " +
          "and none has. For traffic between the routers, use the Traffic view.";
      }
      if (!held) {
        return "no " + kinds[filter.kind] + " events among the " + eventStore.length + " recorded";
      }
      bits.push("kind " + kinds[filter.kind]);
    }
    if (filter.router) bits.push("router " + filter.router);
    return "no events match " + (bits.join(" and ") || "this filter") +
      " — " + eventStore.length + " recorded";
  }

  // renderTraffic answers the question an empty Events list cannot: is
  // anything moving between these routers right now. Every number is a
  // measurement from the last tick; a link with nothing measured says so.
  function renderTraffic() {
    const host = $("traffic");
    const empty = $("traffic-empty");
    if (!snap) {
      host.replaceChildren();
      empty.hidden = false;
      empty.textContent = "waiting for the first poll";
      return;
    }
    const rows = ui.trafficRows(snap.edges || [], snap.sessions || []);
    if (!rows.length) {
      host.replaceChildren();
      empty.hidden = false;
      empty.textContent = "no links yet";
      return;
    }
    empty.hidden = true;

    // A list, not a table: five fixed columns in a 414px pane wrapped the link
    // name over four lines and fitted three rows on screen (measured). Each
    // link is one line of numbers with a muted second line of context.
    function msgs(s) {
      if (!s.polled) return "<span class=\"unpolled\" title=\"a cluster node, not an agent we poll\">not polled</span>";
      // A session the poller could not read and a session that is DOWN both
      // arrive with hasDelta false, so "unmeasured" was this view's answer for
      // both. It is the wrong answer for one of them: "is anything moving" is
      // answered by "this end is idle", not by silence.
      if (s.state && s.state !== "Established") {
        return "<span class=\"down\">" + ui.esc(s.state.toLowerCase()) + "</span>";
      }
      if (!s.known) return "<span class=\"idle\">unmeasured</span>";
      return "<span class=\"" + (s.messages > 0 ? "moving" : "idle") + "\">" + s.messages + "</span>";
    }
    function quiet(s) {
      if (!s.polled || s.quietMsec == null) return "<span class=\"unpolled\">—</span>";
      const cls = s.health === "ok" ? "idle" : s.health;
      return "<span class=\"" + cls + "\">" + (s.quietMsec / 1000).toFixed(1) + "s</span>";
    }

    let html = "<p class=\"traffic-caption\">" + ui.esc(ui.trafficCaption(rows)) +
      "</p><ul class=\"traffic-list\">";
    for (const r of rows) {
      const far = r.b.router || r.a.peer;
      // Every number below is the FIRST-NAMED end's. FRR counts prefixes, flaps
      // and queues per session, and the two ends of a fabric link disagree —
      // spine counts 10 flaps on leaf1 where leaf1 counts its own — so the row
      // has to say whose they are. Named once, on the first clause: naming it on
      // every clause wrapped the row to 59px and undid the layout this list
      // replaced the five-column table to get.
      const bits = [r.kind === "fabric" ? "fabric link" : "cluster node peering in"];
      if (r.a.polled) bits.push(r.a.router + ": " + r.a.pfxRcd + " in / " + r.a.pfxSnt + " out prefixes");
      if (r.a.polled && r.a.flaps > 0) bits.push(r.a.flaps + " flaps since boot");
      if (r.a.polled && r.a.queued > 0) bits.push(r.a.queued + " queued");
      html += "<li>" +
        "<span class=\"link\">" + ui.esc(r.a.router || "?") + " \u21c4 " + ui.esc(far) + "</span>" +
        // The unit only follows a pair of numbers: "2 \u21c4 not polled msg"
        // reads as though "not polled" were a quantity.
        "<span class=\"msgs\">" + msgs(r.a) + " <span class=\"arrows\">\u21c4</span> " + msgs(r.b) +
          (r.a.polled && r.b.polled ? " <span class=\"unit\">msg</span>" : "") + "</span>" +
        "<span class=\"heard\">heard " + quiet(r.a) + "</span>" +
        "<span class=\"meta\">" + ui.esc(bits.join(" · ")) + "</span>" +
        "</li>";
    }
    host.innerHTML = html + "</ul>";
  }

  function setActivityView(view) {
    const v = view === "traffic" ? "traffic" : "events";
    $("events-pane").dataset.view = v;
    $("view-events").setAttribute("aria-selected", v === "events" ? "true" : "false");
    $("view-traffic").setAttribute("aria-selected", v === "traffic" ? "true" : "false");
    if (v === "traffic") renderTraffic();
    else renderEvents();
  }

  function pruneSeen() {
    const keep = {};
    for (const ev of eventStore) {
      if (ev.id) keep[ev.id] = true;
    }
    for (const ev of pausedBuffer) {
      if (ev.id) keep[ev.id] = true;
    }
    for (const id of Object.keys(seenEvent)) {
      if (!keep[id]) delete seenEvent[id];
    }
  }

  function trimEvents() {
    while (eventStore.length > EVENT_CAP) eventStore.pop();
    pruneSeen();
  }

  function updatePausedLabel() {
    const el = $("ev-paused");
    if (!eventsPaused) {
      el.hidden = true;
      el.textContent = "paused, 0 new";
      $("ev-pause").textContent = "Pause";
      return;
    }
    el.hidden = false;
    el.textContent = "paused, " + pausedBuffer.length + " new";
    $("ev-pause").textContent = "Resume";
  }

  function ingestEvent(ev) {
    if (!ev) return;
    if (ev.id && seenEvent[ev.id]) return;
    if (ev.id) {
      seenEvent[ev.id] = true;
      lastEventId = Math.max(lastEventId, ev.id);
    }
    // The flow is drawn from the event itself, whether or not the Events pane
    // is paused: pausing the LIST should not stop the topology showing what
    // the fabric is doing.
    if (ev.kind === "route") flow(ev);
    if (eventsPaused) {
      pausedBuffer.push(ev);
      updatePausedLabel();
      return;
    }
    eventStore.unshift(ev);
    trimEvents();
    renderEvents();
  }

  function fetchEventsSince(id) {
    return fetch("/api/events?since=" + encodeURIComponent(id)).then((r) => {
      if (!r.ok) throw new Error("events");
      return r.json();
    }).then((events) => {
      window.__gapFill = { since: id, count: (events || []).length };
      for (const ev of events || []) ingestEvent(ev);
    }).catch(() => {});
  }

  // ---- arranging the picture ---------------------------------------------
  //
  // `placed` holds the nodes the reader has moved, by id. It is consulted when
  // the elements are rebuilt, which happens on every state change, and it is
  // remembered per browser so an arrangement survives a reload. A node that is
  // no longer in the topology is simply never looked up.

  let placed = readPlaced();

  // readPlaced accepts only what savePlaced writes: an object of id -> {x, y}
  // with FINITE numbers. Anything on the origin can write localStorage and
  // Cytoscape does not validate a position — measured 2026-09-21, {"x":"abc"}
  // painted a node at ("abc", 0) and {} put it at (0, 0), neither throwing. A
  // bad entry is dropped on its own so one corrupt node does not discard the
  // whole arrangement.
  function readPlaced() {
    let v = null;
    try {
      const raw = localStorage.getItem("bgp.placed");
      v = raw ? JSON.parse(raw) : null;
    } catch (err) { return {}; }
    if (!v || typeof v !== "object" || Array.isArray(v)) return {};
    const out = {};
    for (const id of Object.keys(v)) {
      const q = v[id];
      if (q && typeof q === "object" && Number.isFinite(q.x) && Number.isFinite(q.y)) {
        out[id] = { x: q.x, y: q.y };
      }
    }
    return out;
  }

  function savePlaced() {
    try { localStorage.setItem("bgp.placed", JSON.stringify(placed)); } catch (err) { /* private window */ }
  }

  // Cytoscape drags every SELECTED node when one of them is grabbed, so the
  // group move needs no code of its own — only the positions recorded after.
  function recordPositions(nodes) {
    nodes.forEach((n) => { placed[n.id()] = { x: n.position("x"), y: n.position("y") }; });
    savePlaced();
  }

  function updateSelectionCount() {
    const el = $("sel-count");
    if (!el) return;
    const n = cy ? cy.$("node:selected").length : 0;
    // Count the placements the reader can SEE. `placed` also remembers nodes
    // that have left the topology — a cluster peer that went away — and those
    // are kept so the arrangement is there when it returns, but "2 placed by
    // hand" over a picture with nothing moved is a lie. Reset stays enabled
    // while anything is stored, so the record can still be cleared.
    const moved = cy ? cy.nodes().filter((m) => placed[m.id()]).length : 0;
    const stored = Object.keys(placed).length;
    if (n > 0) {
      el.textContent = n + (n === 1 ? " node selected" : " nodes selected") + " — drag one to move them together";
      el.classList.add("active");
    } else {
      el.textContent = "drag the background to select · drag a selected node to move them together" +
        (moved ? " · " + moved + " placed by hand" : "");
      el.classList.remove("active");
    }
    const clear = $("clear-sel");
    const reset = $("reset-layout");
    if (clear) clear.disabled = n === 0;
    if (reset) reset.disabled = stored === 0;
  }

  function resetLayout() {
    placed = {};
    savePlaced();
    renderGraph();
    updateSelectionCount();
  }

  // renderRoles fills the strip under the topology. The text is configuration
  // (DASHBOARD_ROLES), not something the page infers: a leaf with no cluster
  // attached right now looks exactly like a spine.
  function renderRoles() {
    const el = $("roles");
    if (!el || !snap) return;
    const parts = [];
    for (const r of snap.routers || []) {
      if (!r.role) continue;
      const mark = r.dynamicPeers > 0 ? "mark accept" : "mark";
      parts.push(
        "<dt><i class=\"" + mark + "\" aria-hidden=\"true\"></i>" + ui.esc(r.name) +
          (r.asn ? " <span class=\"asn\">AS " + r.asn + "</span>" : "") + "</dt>" +
        "<dd>" + ui.esc(r.role) + "</dd>");
    }
    // The dashed ellipses are not routers and have no agent, so they are
    // described once rather than per node.
    const externals = (snap.nodes || []).filter((n) => n.kind === "external").length;
    if (externals) {
      parts.push(
        "<dt><i class=\"mark external\" aria-hidden=\"true\"></i>dynamic neighbour</dt>" +
        "<dd>" + externals + " peer" + (externals === 1 ? "" : "s") +
        " that arrived through a leaf's listen range — a cluster node, not a router this page polls</dd>");
    }
    el.innerHTML = parts.join("");
  }

  // ---- signal -----------------------------------------------------------
  //
  // Everything here is driven by a number the poller MEASURED. Nothing below
  // starts on a timer: no measurement means no motion, and a router we cannot
  // measure is drawn as unknown rather than left looking healthy.

  function routerByName(name) {
    return ((snap && snap.routers) || []).find((r) => r.name === name) || null;
  }

  // nodeSignalData is the per-node data Cytoscape styles on. Cytoscape
  // selectors compare against numbers and strings, so the booleans are 0/1.
  function nodeSignalData(id) {
    const r = routerByName(id);
    if (!r) return { accepting: 0, signal: "unknown", known: 0 };
    const sig = ui.routerSignal(r, (snap && snap.sessions) || []);
    return {
      accepting: sig.accepting ? 1 : 0,
      signal: sig.worst,
      known: sig.known ? 1 : 0,
    };
  }

  // beat is the heartbeat. It runs ONLY on a tick where a message was counted
  // on one of this router's sessions, so a silent fabric is visibly silent.
  // A timer-driven pulse would animate whether or not anything happened, which
  // is the thing this dashboard is not allowed to do.
  function beat(nodeId) {
    if (!cy || document.hidden) return;
    const n = cy.getElementById(nodeId);
    if (!n || n.empty()) return;
    if (n.scratch("_beating")) return;
    n.scratch("_beating", true);
    // The pulse uses the UNDERLAY, drawn beneath the node. The overlay belongs
    // to hover and to the picked router, and animating it to 0 left a picked
    // node without its highlight after the first beat.
    const c = colours();
    n.style("underlay-color", c.accept);
    n.style("underlay-padding", 8);
    n.animate({ style: { "underlay-opacity": 0.45 }, duration: 160 })
      .animate({ style: { "underlay-opacity": 0 }, duration: 420, complete: () => n.scratch("_beating", false) });
  }

  // flow draws an advertisement travelling along an edge, in the direction it
  // actually travelled, at a speed set by how many prefixes moved. It is fired
  // by a route EVENT, so there is nothing to draw when nothing was advertised.
  function flow(ev) {
    if (!cy || document.hidden || reduceMotion()) return;
    const dir = ui.flowDirection(ev, (snap && snap.edges) || []);
    if (!dir) return;
    const e = cy.getElementById(dir.edge);
    if (!e || e.empty()) return;
    const session = ((snap && snap.sessions) || []).find(
      (s) => s.router === ev.router && s.peer === ev.advertisedBy);
    const traffic = ui.sessionTraffic(session);
    // Volume without lying: one prefix is one dash-length of travel. An
    // unmeasured session still gets a single pass, because the EVENT is the
    // measurement in that case.
    const steps = Math.min(6, Math.max(1, traffic.prefixes || 1));
    // Cytoscape draws an edge from source to target. If the advertisement
    // travelled the other way the offset runs backwards.
    const forward = e.data("target") === dir.to;
    const offset = (forward ? -1 : 1) * 12 * steps;
    const c = colours();
    e.style("line-style", "dashed");
    e.style("line-dash-pattern", [6, 4]);
    e.style("line-color", traffic.withdrew ? c.late : c.accept);
    e.animate({
      style: { "line-dash-offset": offset },
      duration: 220 * steps,
      complete: () => {
        e.removeStyle("line-style");
        e.removeStyle("line-dash-pattern");
        e.removeStyle("line-dash-offset");
        e.removeStyle("line-color");
      },
    });
  }

  function reduceMotion() {
    return matchMedia("(prefers-reduced-motion: reduce)").matches;
  }

  // applySignal merges the cheap per-tick frame into the snapshot the page is
  // already holding. It deliberately does NOT re-render the graph: a full
  // state frame costs 11.5 KB and a re-layout, and arrives only when the
  // topology or the routes change.
  function applySignal(msg) {
    if (!snap) return;
    const byKey = {};
    for (const s of msg.sessions || []) byKey[s.router + "|" + s.peer] = s;
    for (const s of snap.sessions || []) {
      const fresh = byKey[s.router + "|" + s.peer];
      if (fresh) {
        Object.assign(s, fresh);
        continue;
      }
      // The frame lists every session the poller saw on this tick, so a
      // session missing from it was NOT measured on this tick. Leaving the
      // previous tick's numbers in place would let a vanished peer's last
      // delta drive the heartbeat for ever — measured 2026-09-21, a node kept
      // pulsing from a frame that carried no measurement for it at all.
      s.hasDelta = false;
      s.dRcvd = 0;
      s.dSent = 0;
      s.dPfxRcd = 0;
      s.dPfxSnt = 0;
      s.hasTimers = false;
    }
    const routers = {};
    for (const r of msg.routers || []) routers[r.name] = r;
    for (const r of snap.routers || []) {
      const fresh = routers[r.name];
      if (fresh) Object.assign(r, fresh);
      else { r.hasDelta = false; r.dTableVersion = 0; }
    }
    snap.ageMsec = msg.ageMsec;
    snap.ts = msg.ts || snap.ts;
    renderFreshness();
    renderSignalStrip();
    if ($("events-pane").dataset.view === "traffic") renderTraffic();
    if (!cy) return;
    for (const r of snap.routers || []) {
      const sig = ui.routerSignal(r, snap.sessions || []);
      const n = cy.getElementById(r.name);
      if (!n || n.empty()) continue;
      n.data("accepting", sig.accepting ? 1 : 0);
      n.data("signal", sig.worst);
      n.data("known", sig.known ? 1 : 0);
      if (sig.beat && !reduceMotion()) beat(r.name);
    }
  }

  function renderFreshness() {
    const el = $("age");
    if (!el) return;
    const pollMs = snap && snap.poll ? parsePoll(snap.poll) : 2000;
    const f = ui.freshness(snap ? snap.ageMsec : null, pollMs);
    el.textContent = "age " + (snap && snap.ageMsec != null ? Math.round(snap.ageMsec / 1000) + "s" : "—") +
      (f.state === "live" ? "" : " · " + f.label);
    el.className = f.state === "live" ? "" : f.state;
    document.body.classList.toggle("stale-data", f.state === "stale");
  }

  // "2s" / "1.5s" / "500ms" as the server prints a Go duration.
  function parsePoll(s) {
    const m = /^([\d.]+)(ms|s)$/.exec(String(s || ""));
    if (!m) return 2000;
    const n = parseFloat(m[1]);
    if (!Number.isFinite(n)) return 2000;
    return m[2] === "ms" ? n : n * 1000;
  }

  // renderSignalStrip fills the gutter beside the RIB with the numbers the
  // page already had and nowhere to put: what the selected router's sessions
  // are actually doing.
  function renderSignalStrip() {
    const el = $("signal-strip");
    if (!el) return;
    const r = selectedRouter();
    const name = r ? r.name : "";
    if (!snap || !r) {
      el.className = "signal-strip empty";
      el.textContent = "waiting for the first poll";
      return;
    }
    const sessions = (snap.sessions || []).filter((s) => s.router === name && !s.stale);
    const sig = ui.routerSignal(r, snap.sessions || []);
    const parts = [];
    const add = (k, v, cls) => parts.push(
      "<span class=\"k\">" + ui.esc(k) + "</span> <span class=\"v " + (cls || "") + "\">" + ui.esc(v) + "</span>");

    if (!r.reachable) {
      el.className = "signal-strip";
      el.innerHTML = "<span class=\"k\">unreachable</span> <span class=\"v critical\">last seen " +
        ui.esc(ui.relativeTime(r.lastSeen)) + "</span>";
      return;
    }
    add("sessions", String(sessions.length));
    add("cluster peers", String(sig.dynamicPeers), sig.accepting ? "accept" : "");
    if (sig.accepting) add("state", "accepting traffic", "accept");
    else add("state", "up, no cluster peers");

    let worstLabel = "unknown";
    if (sig.worst === "ok") worstLabel = "keepalives on time";
    else if (sig.worst === "late") worstLabel = "a keepalive is overdue";
    else if (sig.worst === "critical") worstLabel = "running out of hold time";
    add("peers", worstLabel, sig.worst === "unknown" ? "unknown" : sig.worst === "ok" ? "" : sig.worst);

    const quiet = sessions.filter((s) => s.hasTimers).map((s) => s.quietMsec);
    if (quiet.length) add("quietest", Math.max.apply(null, quiet) + "ms");
    const flaps = sessions.reduce((a, s) => a + (s.flaps || 0), 0);
    if (flaps > 0) add("flaps since boot", String(flaps), flaps > 4 ? "late" : "");
    const queued = sessions.reduce((a, s) => a + (s.inq || 0) + (s.outq || 0), 0);
    if (queued > 0) add("queued", String(queued), "late");
    if (r.hasDelta && r.dTableVersion > 0) add("table moved", "+" + r.dTableVersion, "accept");
    el.className = "signal-strip";
    el.innerHTML = parts.join(" ");
  }

  function applyState(msg) {
    snap = msg;
    renderHeader();
    renderGraph();
    ribRows();
    renderFreshness();
    renderSignalStrip();
    renderRoles();
    updateSelectionCount();
    if ($("events-pane").dataset.view === "traffic") renderTraffic();
    applyReady();
  }

  function connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(proto + "://" + location.host + "/ws");
    window.__ws = ws;
    $("wsdot").className = "dot transitional";
    $("wslabel").textContent = wsAttempts
      ? "WebSocket retrying (" + wsAttempts + ")"
      : "connecting";
    ws.onopen = function () {
      const wasRetry = wsAttempts > 0;
      backoff = 500;
      wsAttempts = 0;
      $("wsdot").className = "dot up";
      $("wslabel").textContent = "live";
      if (wasRetry || lastEventId > 0) fetchEventsSince(lastEventId);
    };
    ws.onclose = function () {
      wsAttempts += 1;
      $("wsdot").className = "dot down";
      $("wslabel").textContent = "WebSocket retrying (" + wsAttempts + ")";
      if (window.__holdReconnect) return;
      setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 8000);
    };
    ws.onerror = function () { ws.close(); };
    ws.onmessage = function (e) {
      let msg;
      try { msg = JSON.parse(e.data); } catch (err) { return; }
      if (msg.type === "state") applyState(msg);
      else if (msg.type === "signal") applySignal(msg);
      else if (msg.type === "event") ingestEvent(msg);
    };
  }

  function setTab(name) {
    const tab = name === "events" ? "events" : "rib";
    document.body.dataset.tab = tab;
    $("tab-rib").setAttribute("aria-selected", tab === "rib" ? "true" : "false");
    $("tab-events").setAttribute("aria-selected", tab === "events" ? "true" : "false");
  }

  // ---- resizable panes ---------------------------------------------------
  //
  // The layout was three fixed percentages, and the operator's point was that
  // the topology has room the page was not using. Each splitter writes a CSS
  // custom property, so the grid does the work; Cytoscape is told to re-fit
  // afterwards because a canvas does not reflow on its own.
  //
  // The size is remembered per browser, which is the one thing localStorage is
  // right for here. It is wrapped because a private window can throw on it.
  function readSaved(key, fallback) {
    try {
      const v = parseFloat(localStorage.getItem(key));
      return Number.isFinite(v) ? v : fallback;
    } catch (err) { return fallback; }
  }

  function saveSplit(key, pct) {
    try { localStorage.setItem(key, String(pct)); } catch (err) { /* private window */ }
  }

  // Each splitter is one entry here: the CSS property it drives, the button
  // that reports its value, and the range it may take.
  const SPLITS = {
    col: { prop: "--split-col", button: "split-col", min: 25, max: 80 },
    row: { prop: "--split-row", button: "split-row", min: 20, max: 85 },
    foot: { prop: "--split-foot", button: "split-foot", min: 8, max: 60 },
  };

  function applySplit(which, pct) {
    const main = document.querySelector("main");
    const spec = SPLITS[which];
    if (!main || !spec) return;
    const clamped = Math.min(spec.max, Math.max(spec.min, pct));
    main.style.setProperty(spec.prop, clamped + "%");
    const btn = $(spec.button);
    if (btn) btn.setAttribute("aria-valuenow", String(Math.round(clamped)));
    saveSplit("bgp.split." + which, clamped);
    refit();
    return clamped;
  }

  // Cytoscape holds a canvas sized at construction. Without resize() the graph
  // keeps the old pane's dimensions and the nodes sit in the wrong place; the
  // node-pullback in renderGraph then has stale bounds to work from, so the
  // graph is re-rendered rather than merely re-fitted.
  let refitTimer = null;
  function refit() {
    if (!cy) return;
    clearTimeout(refitTimer);
    refitTimer = setTimeout(() => {
      cy.resize();
      renderGraph();
    }, 60);
  }

  function wireSplitter(id, which) {
    const el = $(id);
    if (!el) return;
    const main = document.querySelector("main");
    let dragging = false;

    function pctFromEvent(e) {
      const box = main.getBoundingClientRect();
      if (which === "col") return ((e.clientX - box.left) / box.width) * 100;
      if (which === "foot") {
        // the legend is the BOTTOM track, so the percentage grows as the
        // pointer rises
        const pane = document.getElementById("graph-pane").getBoundingClientRect();
        return ((pane.bottom - e.clientY) / pane.height) * 100;
      }
      const side = document.querySelector(".side").getBoundingClientRect();
      return ((e.clientY - side.top) / side.height) * 100;
    }

    el.addEventListener("pointerdown", (e) => {
      dragging = true;
      el.setPointerCapture(e.pointerId);
      document.body.classList.add("dragging");
      e.preventDefault();
    });
    el.addEventListener("pointermove", (e) => {
      if (!dragging) return;
      applySplit(which, pctFromEvent(e));
    });
    function end(e) {
      if (!dragging) return;
      dragging = false;
      try { el.releasePointerCapture(e.pointerId); } catch (err) { /* already gone */ }
      document.body.classList.remove("dragging");
    }
    el.addEventListener("pointerup", end);
    el.addEventListener("pointercancel", end);

    // Dragging is not available to everyone: the arrow keys move it too.
    el.addEventListener("keydown", (e) => {
      const step = e.shiftKey ? 10 : 2;
      const now = parseFloat(el.getAttribute("aria-valuenow")) || 50;
      const spec = SPLITS[which];
      // The legend grows upwards, so Up must make it bigger, not smaller.
      const grow = which === "col" ? "ArrowRight" : which === "foot" ? "ArrowUp" : "ArrowDown";
      const shrink = which === "col" ? "ArrowLeft" : which === "foot" ? "ArrowDown" : "ArrowUp";
      if (e.key === shrink) applySplit(which, now - step);
      else if (e.key === grow) applySplit(which, now + step);
      else if (e.key === "Home") applySplit(which, spec.min);
      else if (e.key === "End") applySplit(which, spec.max);
      else return;
      e.preventDefault();
    });
  }

  function wireResize() {
    wireSplitter("split-col", "col");
    wireSplitter("split-row", "row");
    wireSplitter("split-foot", "foot");
    applySplit("col", readSaved("bgp.split.col", 65));
    applySplit("row", readSaved("bgp.split.row", 58));
    applySplit("foot", readSaved("bgp.split.foot", 26));
    // The window itself, and anything else that changes the pane, must re-fit
    // too — this is the half the old layout never did.
    if (typeof ResizeObserver !== "undefined") {
      const ro = new ResizeObserver(() => refit());
      const g = $("graph");
      if (g) ro.observe(g);
    } else {
      addEventListener("resize", refit);
    }
  }

  function boot() {
    $("legend-toggle").addEventListener("click", () => {
      const bar = document.querySelector(".legend-bar");
      const open = !bar.classList.contains("open");
      bar.classList.toggle("open", open);
      $("legend").hidden = !open && matchMedia("(max-width: 700px)").matches;
      $("legend-toggle").setAttribute("aria-expanded", open ? "true" : "false");
    });
    const wide = matchMedia("(min-width: 701px)");
    function syncLegend() {
      $("legend").hidden = !wide.matches && !document.querySelector(".legend-bar").classList.contains("open");
    }
    wide.addEventListener("change", syncLegend);
    syncLegend();

    $("ev-kind").addEventListener("change", renderEvents);
    $("ev-selected").addEventListener("change", renderEvents);
    $("ev-pause").addEventListener("click", () => {
      eventsPaused = !eventsPaused;
      if (!eventsPaused) {
        for (const ev of pausedBuffer) eventStore.unshift(ev);
        pausedBuffer.length = 0;
        trimEvents();
        renderEvents();
      }
      updatePausedLabel();
    });
    $("tab-rib").addEventListener("click", () => setTab("rib"));
    $("tab-events").addEventListener("click", () => setTab("events"));
    if (shotTab) setTab(shotTab);
    window.__ingestEvent = ingestEvent;
    window.__connect = connect;
    window.__applySignal = applySignal;
    window.__setActivityView = setActivityView;
    window.__renderTraffic = renderTraffic;

    for (const id of ["view-events", "view-traffic"]) {
      $(id).addEventListener("click", (e) => setActivityView(e.currentTarget.dataset.view));
    }
    window.__applySplit = applySplit;
    window.__signalOf = nodeSignalData;

    wireResize();

    $("select-all").addEventListener("click", () => {
      if (cy) cy.nodes().select();
      updateSelectionCount();
    });
    $("clear-sel").addEventListener("click", () => {
      if (cy) cy.nodes().unselect();
      updateSelectionCount();
    });
    $("reset-layout").addEventListener("click", resetLayout);
    // Ctrl/Cmd+A selects every node when the graph has focus, and Escape
    // clears. Both are scoped to the graph so they do not steal the shortcut
    // from the rest of the page.
    $("graph").addEventListener("keydown", (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "a") {
        if (cy) cy.nodes().select();
        updateSelectionCount();
        e.preventDefault();
      } else if (e.key === "Escape") {
        if (cy) cy.nodes().unselect();
        updateSelectionCount();
      }
    });
    window.__placed = () => placed;
    window.__resetLayout = resetLayout;

    setInterval(() => {
      document.querySelectorAll("[data-ts]").forEach((el) => {
        el.textContent = ui.relativeTime(el.dataset.ts);
      });
      if (snap) renderHeader();
      const router = selectedRouter();
      if (router && !router.reachable) ribRows();
    }, 1000);

    // Which build this is. Asked once — an image cannot change under a running
    // container — and failing is not worth breaking the page for.
    fetch("/api/version", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : null))
      .then((v) => {
        if (v) ui.applyBuildLabel($("build"), v);
      })
      .catch((err) => console.warn("build version unavailable", err));

    Promise.all([
      fetch("/api/state").then((r) => { if (!r.ok) throw new Error("state"); return r.json(); }),
      fetch("/api/events?since=0").then((r) => { if (!r.ok) throw new Error("events"); return r.json(); }),
    ]).then(([state, events]) => {
      applyState(state);
      for (const ev of events || []) ingestEvent(ev);
      connect();
    }).catch(() => {
      renderHeader();
      connect();
    });
  }

  boot();
  window.addEventListener("resize", () => { if (cy) { cy.resize(); renderGraph(); } });
})();
