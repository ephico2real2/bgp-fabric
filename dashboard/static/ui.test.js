"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const ui = require("./ui.js");

test("esc encodes ampersand, angles, quotes and apostrophe", () => {
  assert.equal(ui.esc("O'Brien & <x> \"y\""), "O&#39;Brien &amp; &lt;x&gt; &quot;y&quot;");
});

test("relativeTime is 4s ago at a 4 second gap", () => {
  const now = Date.parse("2026-09-20T12:00:10.000Z");
  assert.equal(ui.relativeTime("2026-09-20T12:00:06.000Z", now), "4s ago");
});

test("relativeTime says never when the stamp is missing", () => {
  assert.equal(ui.relativeTime("", 0), "never");
  assert.equal(ui.relativeTime(undefined, 0), "never");
});

test("eventMatches filters by kind and by router", () => {
  const ev = { kind: "session", router: "spine" };
  assert.equal(ui.eventMatches(ev, { kind: "", router: "" }), true);
  assert.equal(ui.eventMatches(ev, { kind: "session", router: "" }), true);
  assert.equal(ui.eventMatches(ev, { kind: "route", router: "" }), false);
  assert.equal(ui.eventMatches(ev, { kind: "", router: "spine" }), true);
  assert.equal(ui.eventMatches(ev, { kind: "", router: "leaf1" }), false);
  assert.equal(ui.eventMatches(ev, { kind: "session", router: "leaf1" }), false);
});

test("groupECMP lists each prefix once and puts the best path first", () => {
  const g = ui.groupECMP([
    { prefix: "10.0.0.0/24", bestpath: false, nexthop: "1.1.1.1" },
    { prefix: "10.0.0.0/24", bestpath: true, nexthop: "2.2.2.2" },
    { prefix: "10.1.0.0/24", bestpath: true, nexthop: "3.3.3.3" },
  ]);
  assert.equal(g.length, 2);
  assert.equal(g[0].prefix, "10.0.0.0/24");
  assert.equal(g[0].paths.length, 2);
  assert.equal(g[0].paths[0].bestpath, true);
  assert.equal(g[0].paths[0].nexthop, "2.2.2.2");
  assert.equal(g[1].prefix, "10.1.0.0/24");
  assert.equal(g[1].paths.length, 1);
});

test("layoutGraph at 375 px does not overlap six externals", () => {
  const six = sixExternals();
  const layout = ui.layoutGraph({
    nodes: routers().concat(six.nodes),
    sessions: six.sessions,
    width: 375,
    height: 280,
    fontSize: 11,
    padding: 6,
  });
  assert.equal(layout.overlaps.length, 0, JSON.stringify(layout.overlaps));
  for (const id of Object.keys(layout.boxes)) {
    const b = layout.boxes[id];
    assert.ok(b.x - b.w / 2 >= -0.5, id + " left " + (b.x - b.w / 2));
    assert.ok(b.x + b.w / 2 <= 375.5, id + " right " + (b.x + b.w / 2));
  }
});

test("layoutGraph at 1200 px keeps only1 and both from sharing a colliding row", () => {
  const six = sixExternals();
  const layout = ui.layoutGraph({
    nodes: routers().concat(six.nodes),
    sessions: six.sessions,
    width: 1200,
    height: 520,
    fontSize: 12,
    padding: 8,
  });
  assert.equal(layout.overlaps.length, 0, JSON.stringify(layout.overlaps));
  const a = layout.boxes["172.19.0.20"];
  const b = layout.boxes["172.18.0.5"];
  assert.ok(a && b, "expected both nodes");
  const o = ui.overlapPx(a, b);
  assert.equal(o.w, 0);
});

test("edgeCardText shows both sides of a session", () => {
  const text = ui.edgeCardText({
    aRouter: "leaf1", aPeer: "172.19.0.2", aState: "Established",
    aUptime: "00:04:11", aPfxRcd: 2, aPfxSnt: 7,
    bRouter: "eg-poc1", bPeer: "172.19.0.2", bState: "Established",
    bUptime: "00:04:10", bPfxRcd: 4, bPfxSnt: 2,
  });
  assert.match(text, /leaf1 172\.19\.0\.2 Established 00:04:11 rcd 2 snt 7/);
  assert.match(text, /eg-poc1 172\.19\.0\.2 Established/);
});

test("contrastRatio of the state tokens meets 3:1 on both backgrounds", () => {
  const light = { bg: "#f4f1ea", panel: "#fffdf8" };
  const dark = { bg: "#161410", panel: "#1f1b16" };
  const tokens = {
    est: { light: "#2f7d32", dark: "#4ade80" },
    trans: { light: "#a35f00", dark: "#fbbf24" },
    down: { light: "#b42318", dark: "#f87171" },
    stale: { light: "#6b6570", dark: "#a8a29e" },
  };
  for (const name of Object.keys(tokens)) {
    const t = tokens[name];
    for (const surface of [light.bg, light.panel]) {
      assert.ok(ui.contrastRatio(t.light, surface) >= 3, name + " light on " + surface);
    }
    for (const surface of [dark.bg, dark.panel]) {
      assert.ok(ui.contrastRatio(t.dark, surface) >= 3, name + " dark on " + surface);
    }
  }
});

function routers() {
  return [
    { id: "edge", kind: "router", label: "edge\nAS 65000" },
    { id: "spine", kind: "router", label: "spine\nAS 65100" },
    { id: "leaf1", kind: "router", label: "leaf1\nAS 65101" },
    { id: "leaf2", kind: "router", label: "leaf2\nAS 65102" },
  ];
}

function sixExternals() {
  const nodes = [
    { id: "172.19.0.2", kind: "external", label: "eg-poc1 (kube-vip)\n172.19.0.2" },
    { id: "172.19.0.3", kind: "external", label: "eg-poc1 (kube-vip)\n172.19.0.3" },
    { id: "172.19.0.20", kind: "external", label: "eg-poc1 (kube-vip)\n172.19.0.20" },
    { id: "172.18.0.4", kind: "external", label: "eg-poc2 (MetalLB)\n172.18.0.4" },
    { id: "172.18.0.5", kind: "external", label: "eg-poc2 (MetalLB)\n172.18.0.5" },
    { id: "172.18.0.6", kind: "external", label: "eg-poc2 (MetalLB)\n172.18.0.6" },
  ];
  const sessions = [
    { router: "leaf1", peer: "172.19.0.2" },
    { router: "leaf1", peer: "172.19.0.3" },
    { router: "leaf1", peer: "172.19.0.20" },
    { router: "leaf2", peer: "172.18.0.4" },
    { router: "leaf2", peer: "172.18.0.5" },
    { router: "leaf1", peer: "172.18.0.6" },
    { router: "leaf2", peer: "172.18.0.6" },
  ];
  return { nodes: nodes, sessions: sessions };
}

// ---- signal -----------------------------------------------------------

test("sessionHealth treats reaching the keepalive interval as normal", () => {
  // Measured on the fabric: quietMsec sawtooths 0 -> 1000 -> 2000 -> 3000 -> 0
  // with keepalive 3000 and hold 9000, so 3000 is the instant before the next
  // keepalive, not a fault.
  const base = { hasTimers: true, holdMsec: 9000, keepaliveMsec: 3000 };
  for (const quiet of [0, 1000, 2000, 3000]) {
    const h = ui.sessionHealth(Object.assign({ quietMsec: quiet }, base));
    assert.equal(h.kind, "ok", "quiet=" + quiet + " must be ok");
  }
  assert.equal(ui.sessionHealth(Object.assign({ quietMsec: 4000 }, base)).kind, "late");
  assert.equal(ui.sessionHealth(Object.assign({ quietMsec: 6000 }, base)).kind, "critical");
  assert.equal(ui.sessionHealth(Object.assign({ quietMsec: 8000 }, base)).kind, "critical");
});

test("sessionHealth reports unknown rather than a heartbeat of zero", () => {
  assert.equal(ui.sessionHealth({ hasTimers: false, quietMsec: 0 }).kind, "unknown");
  assert.equal(ui.sessionHealth({ hasTimers: false }).fraction, null);
  assert.equal(ui.sessionHealth(null).kind, "unknown");
  // hasTimers true but no hold time is still no measurement
  assert.equal(ui.sessionHealth({ hasTimers: true, holdMsec: 0, quietMsec: 5 }).kind, "unknown");
});

test("sessionTraffic reports nothing on a tick with no measured delta", () => {
  const t = ui.sessionTraffic({ hasDelta: false, dRcvd: 9, dSent: 9 });
  assert.equal(t.known, false);
  assert.equal(t.messages, 0);
  assert.equal(t.prefixes, 0);
});

test("a withdrawal is volume, not a negative width", () => {
  // pfxRcd 2 -> 1 with no session reset: the delta is -1. The page needs the
  // magnitude to draw and the sign to say which way.
  const t = ui.sessionTraffic({ hasDelta: true, dRcvd: 2, dSent: 1, dPfxRcd: -1, dPfxSnt: 0 });
  assert.equal(t.prefixes, 1);
  assert.equal(t.withdrew, true);
  assert.equal(t.messages, 3);
  const add = ui.sessionTraffic({ hasDelta: true, dRcvd: 2, dSent: 1, dPfxRcd: 3, dPfxSnt: 0 });
  assert.equal(add.withdrew, false);
  assert.equal(add.prefixes, 3);
});

test("routerSignal reads accepting from FRR, not from a session count", () => {
  const leaf = { name: "leaf1", reachable: true, dynamicPeers: 2, hasDelta: true, dTableVersion: 0 };
  const spine = { name: "spine", reachable: true, dynamicPeers: 0, hasDelta: true, dTableVersion: 0 };
  const sessions = [
    { router: "leaf1", hasDelta: true, dRcvd: 1, dSent: 0, hasTimers: true, holdMsec: 9000, keepaliveMsec: 3000, quietMsec: 1000 },
    { router: "spine", hasDelta: true, dRcvd: 1, dSent: 1, hasTimers: true, holdMsec: 9000, keepaliveMsec: 3000, quietMsec: 0 },
  ];
  assert.equal(ui.routerSignal(leaf, sessions).accepting, true);
  assert.equal(ui.routerSignal(spine, sessions).accepting, false);
  // a spine with three healthy sessions is still not "accepting": no cluster peers it
  assert.equal(ui.routerSignal(spine, sessions).beat, true);
});

test("an unreachable router has no beat and claims nothing", () => {
  const r = { name: "leaf2", reachable: false, dynamicPeers: 2 };
  const sig = ui.routerSignal(r, [{ router: "leaf2", hasDelta: true, dRcvd: 5 }]);
  assert.equal(sig.beat, false);
  assert.equal(sig.known, false);
  assert.equal(sig.worst, "unknown");
});

test("a stale session contributes no beat", () => {
  const r = { name: "leaf1", reachable: true, dynamicPeers: 0 };
  const sig = ui.routerSignal(r, [{ router: "leaf1", stale: true, hasDelta: true, dRcvd: 9 }]);
  assert.equal(sig.beat, false);
});

test("routerSignal takes the worst session's health", () => {
  const r = { name: "leaf1", reachable: true, dynamicPeers: 1 };
  const sessions = [
    { router: "leaf1", hasTimers: true, holdMsec: 9000, keepaliveMsec: 3000, quietMsec: 0 },
    { router: "leaf1", hasTimers: true, holdMsec: 9000, keepaliveMsec: 3000, quietMsec: 7000 },
  ];
  assert.equal(ui.routerSignal(r, sessions).worst, "critical");
});

test("flowDirection points at the router that learned the prefix", () => {
  const edges = [{ id: "e1", source: "leaf1", target: "n-172.20.0.3", aRouter: "leaf1", aPeer: "172.20.0.3", bRouter: "", bPeer: "" }];
  // leaf1 learned 10.198.0.10/32 from 172.20.0.3 — the arrow runs toward leaf1
  const ev = { kind: "route", router: "leaf1", prefix: "10.198.0.10/32", advertisedBy: "172.20.0.3" };
  const f = ui.flowDirection(ev, [{ id: "e1", aRouter: "leaf1", aPeer: "", bRouter: "", bPeer: "172.20.0.3", source: "n", target: "leaf1" }]);
  assert.ok(f, "expected a direction");
  assert.equal(f.to, "leaf1");
  assert.equal(f.prefix, "10.198.0.10/32");
  void edges;
});

test("a locally originated route produces no flow", () => {
  // advertisedBy is empty for the router's own loopback
  const ev = { kind: "route", router: "leaf2", prefix: "10.200.255.12/32", advertisedBy: "" };
  assert.equal(ui.flowDirection(ev, [{ id: "e1", aRouter: "leaf2", bPeer: "10.200.1.11" }]), null);
  // and a session event is not a flow at all
  assert.equal(ui.flowDirection({ kind: "session", router: "leaf2", advertisedBy: "x" }, []), null);
});

test("freshness is driven by the server's own age, not the browser clock", () => {
  assert.equal(ui.freshness(1249, 2000).state, "live");
  assert.equal(ui.freshness(4000, 2000).state, "live");
  assert.equal(ui.freshness(6000, 2000).state, "lagging");
  assert.equal(ui.freshness(30000, 2000).state, "stale");
  assert.equal(ui.freshness(null, 2000).state, "unknown");
  assert.equal(ui.freshness(undefined, 2000).state, "unknown");
});

test("the RIB says 'self' rather than FRR's (unspec) sentinel", () => {
  assert.equal(ui.fromLabel("(unspec)"), "self");
  assert.equal(ui.fromLabel(""), "self");
  assert.equal(ui.fromLabel(null), "self");
  assert.equal(ui.fromLabel("172.20.0.4"), "172.20.0.4");
});

// ---- traffic ----------------------------------------------------------

const TRAFFIC_EDGES = [
  { id: "leaf1|spine", state: "established", aRouter: "spine", aPeer: "10.200.1.2", bRouter: "leaf1", bPeer: "10.200.1.3" },
  { id: "172.20.0.3|leaf1", state: "established", aRouter: "leaf1", aPeer: "172.20.0.3", bRouter: "", bPeer: "" },
];
const TRAFFIC_SESSIONS = [
  { router: "spine", peer: "10.200.1.2", state: "Established", hasDelta: true, dRcvd: 1, dSent: 1, dPfxRcd: 0, dPfxSnt: 0,
    hasTimers: true, quietMsec: 1000, holdMsec: 9000, keepaliveMsec: 3000, pfxRcd: 2, pfxSnt: 6, flaps: 8, inq: 0, outq: 0 },
  { router: "leaf1", peer: "10.200.1.3", state: "Established", hasDelta: true, dRcvd: 1, dSent: 0, dPfxRcd: 0, dPfxSnt: 0,
    hasTimers: true, quietMsec: 2000, holdMsec: 9000, keepaliveMsec: 3000, pfxRcd: 5, pfxSnt: 6, flaps: 8, inq: 0, outq: 0 },
  { router: "leaf1", peer: "172.20.0.3", state: "Established", hasDelta: true, dRcvd: 1, dSent: 0, dPfxRcd: 0, dPfxSnt: 0,
    hasTimers: true, quietMsec: 1000, holdMsec: 9000, keepaliveMsec: 3000, pfxRcd: 1, pfxSnt: 0, flaps: 0, inq: 0, outq: 0 },
];

test("a fabric link reports both directions; a cluster link says the far end is not polled", () => {
  const rows = ui.trafficRows(TRAFFIC_EDGES, TRAFFIC_SESSIONS);
  const fabric = rows.find((r) => r.id === "leaf1|spine");
  const cluster = rows.find((r) => r.id === "172.20.0.3|leaf1");

  assert.equal(fabric.kind, "fabric");
  assert.equal(fabric.a.polled, true);
  assert.equal(fabric.b.polled, true);
  assert.equal(fabric.messages, 3); // 1+1 from spine, 1+0 from leaf1

  assert.equal(cluster.kind, "cluster");
  assert.equal(cluster.a.polled, true);
  // The far end is a BGP peer we cannot read. It must NOT report zero, which
  // would read as "that side sent nothing".
  assert.equal(cluster.b.polled, false);
  assert.equal(cluster.b.messages, undefined);
});

test("a link where neither end measured anything says so rather than showing zero", () => {
  const sessions = TRAFFIC_SESSIONS.map((s) => Object.assign({}, s, { hasDelta: false, dRcvd: 0, dSent: 0 }));
  const rows = ui.trafficRows(TRAFFIC_EDGES, sessions);
  for (const r of rows) {
    assert.equal(r.known, false, r.id + " must report no measurement");
    assert.equal(r.messages, 0);
  }
  // and with a measurement, known flips
  const live = ui.trafficRows(TRAFFIC_EDGES, TRAFFIC_SESSIONS);
  assert.ok(live.every((r) => r.known), "a measured tick must be known");
});

test("the busiest link sorts first and ties keep a stable order", () => {
  const busy = TRAFFIC_SESSIONS.map((s) =>
    s.peer === "172.20.0.3" ? Object.assign({}, s, { dRcvd: 40 }) : s);
  const rows = ui.trafficRows(TRAFFIC_EDGES, busy);
  assert.equal(rows[0].id, "172.20.0.3|leaf1");

  // a tie must not shuffle between renders
  const flat = TRAFFIC_SESSIONS.map((s) => Object.assign({}, s, { dRcvd: 0, dSent: 0 }));
  const a = ui.trafficRows(TRAFFIC_EDGES, flat).map((r) => r.id);
  const b = ui.trafficRows(TRAFFIC_EDGES.slice().reverse(), flat).map((r) => r.id);
  assert.deepEqual(a, b, "equally quiet links must sort the same regardless of input order");
});

test("a session with no timers reports a null quiet time, not zero", () => {
  const sessions = TRAFFIC_SESSIONS.map((s) => Object.assign({}, s, { hasTimers: false }));
  const rows = ui.trafficRows(TRAFFIC_EDGES, sessions);
  assert.equal(rows.find((r) => r.id === "leaf1|spine").a.quietMsec, null);
  assert.equal(rows.find((r) => r.id === "leaf1|spine").a.health, "unknown");
});

test("an edge whose session is missing entirely is not polled", () => {
  const rows = ui.trafficRows([{ id: "x|y", state: "down", aRouter: "ghost", aPeer: "1.1.1.1" }], TRAFFIC_SESSIONS);
  assert.equal(rows[0].a.polled, false);
  assert.equal(rows[0].known, false);
});

test("the traffic caption never reports zero for a tick it did not measure", () => {
  const unmeasured = TRAFFIC_SESSIONS.map((s) => Object.assign({}, s, { hasDelta: false, dRcvd: 0, dSent: 0 }));
  const none = ui.trafficRows(TRAFFIC_EDGES, unmeasured);
  assert.match(ui.trafficCaption(none), /nothing measured/);
  assert.doesNotMatch(ui.trafficCaption(none), /0 carried/);

  const all = ui.trafficRows(TRAFFIC_EDGES, TRAFFIC_SESSIONS);
  assert.equal(ui.trafficCaption(all), "2 links · 2 carried a message on the last poll");

  // one link measured, one not: the count is out of the MEASURED ones, and the
  // caption says so rather than implying the other one was silent
  const half = TRAFFIC_SESSIONS.map((s) =>
    (s.router === "spine" || s.peer === "10.200.1.3")
      ? Object.assign({}, s, { hasDelta: false, dRcvd: 0, dSent: 0 })
      : s);
  assert.match(ui.trafficCaption(ui.trafficRows(TRAFFIC_EDGES, half)),
    /1 of 1 measured carried a message/);

  assert.equal(ui.trafficCaption([]), "0 links · nothing measured on the last poll");
  assert.match(ui.trafficCaption(ui.trafficRows([TRAFFIC_EDGES[0]], TRAFFIC_SESSIONS)), /^1 link ·/);
});

test("a session that is not Established carries its state onto the row", () => {
  // The renderer needs this to tell a link that is DOWN from one we simply did
  // not measure: both arrive with hasDelta false.
  const idle = TRAFFIC_SESSIONS.map((s) =>
    s.router === "spine" ? Object.assign({}, s, { state: "Idle", hasDelta: false, dRcvd: 0, dSent: 0 }) : s);
  const row = ui.trafficRows(TRAFFIC_EDGES, idle).find((r) => r.id === "leaf1|spine");
  assert.equal(row.a.state, "Idle");
  assert.equal(row.a.known, false);
  assert.equal(row.b.state, "Established");
});

test("a state maps to a colour, and 'Idle (Admin)' is down rather than unknown", () => {
  // Matching the whole string is the trap: an administrative shutdown reads
  // "Idle (Admin)" and an exact-match table drops it into the unknown colour,
  // so the one session a human just took down looks like a parsing failure.
  assert.equal(ui.stateClass("Established"), "state-established");
  assert.equal(ui.stateClass("Idle"), "state-down");
  assert.equal(ui.stateClass("Idle (Admin)"), "state-down");
  assert.equal(ui.stateClass("Active"), "state-down");
  assert.equal(ui.stateClass("Connect"), "state-transitional");
  assert.equal(ui.stateClass("OpenConfirm"), "state-transitional");
  assert.equal(ui.stateClass("OpenSent"), "state-transitional");
  // case and padding are FRR's business, not the page's
  assert.equal(ui.stateClass("  established  "), "state-established");
  // a stale row is last-known whatever the state says
  assert.equal(ui.stateClass("Established", true), "state-stale");
  // nothing at all is not a colour
  assert.equal(ui.stateClass(""), "");
  assert.equal(ui.stateClass(null), "");
  // something FRR might add later is marked unknown, not silently coloured
  assert.equal(ui.stateClass("Weird"), "state-unknown");
});

test("a nexthop is not a state", () => {
  // A route bestpath event's from/to are addresses. stateClass answers for a
  // STATE, so the caller must not hand it a nexthop — this pins the value it
  // returns for one, so the caller's guard cannot be quietly dropped.
  assert.equal(ui.stateClass("10.200.1.3"), "state-unknown");
  assert.equal(ui.stateClass("172.20.0.4"), "state-unknown");
});

test("buildLabel names the build when the binary knows its commit", () => {
  const l = ui.buildLabel({
    revision: "4cf18646880b540d48b029ea25a35ebe2e1821c2",
    short: "4cf1864",
    built: "2026-09-23T10:32:58Z",
  });
  assert.equal(l.text, "build 4cf1864");
  assert.equal(l.unknown, false);
  assert.match(l.title, /4cf18646880b540d48b029ea25a35ebe2e1821c2/);
  assert.match(l.title, /built 2026-09-23T10:32:58Z/);
});

test("buildLabel falls back to the first seven characters when short is absent", () => {
  const l = ui.buildLabel({ revision: "4cf18646880b540d48b029ea25a35ebe2e1821c2" });
  assert.equal(l.text, "build 4cf1864");
});

test("buildLabel says local build rather than the word unknown", () => {
  for (const v of [{ revision: "unknown", short: "unknown" }, { revision: "" }, {}, null]) {
    const l = ui.buildLabel(v);
    assert.equal(l.text, "local build", JSON.stringify(v));
    assert.equal(l.unknown, true);
    assert.doesNotMatch(l.text, /unknown/);
  }
});

test("buildLabel never claims a commit it was not given", () => {
  // The header links nothing and the tooltip says why, rather than showing a
  // number a reader could paste into git and not find.
  const l = ui.buildLabel({ revision: "unknown", built: "unknown" });
  assert.match(l.title, /not built by the pipeline/);
  assert.doesNotMatch(l.title, /[0-9a-f]{7}/);
});

test("buildLabel keeps a dirty marker: the sha alone names code that is not running", () => {
  const l = ui.buildLabel({
    revision: "4cf18646880b540d48b029ea25a35ebe2e1821c2-dirty",
    short: "4cf1864",
    built: "2026-09-23T10:32:58Z",
  });
  assert.equal(l.text, "build 4cf1864-dirty");
  assert.equal(l.dirty, true);
  assert.equal(l.unknown, false);
});

test("buildLabel shortens a sha and leaves anything that is not one alone", () => {
  assert.equal(ui.buildLabel({ revision: "4cf18646880b540d48b029ea25a35ebe2e1821c2" }).text, "build 4cf1864");
  assert.equal(ui.buildLabel({ revision: "v1.2.3-4-g4cf1864" }).text, "build v1.2.3-4-g4cf1864");
  assert.equal(ui.buildLabel({ revision: "v1.2" }).text, "build v1.2");
});

test("buildLabel trusts revision over a short that disagrees with it", () => {
  const l = ui.buildLabel({ revision: "4cf18646880b540d48b029ea25a35ebe2e1821c2", short: "0000000" });
  assert.equal(l.text, "build 4cf1864");
});

test("buildLabel does not throw on a revision that is not a string", () => {
  for (const v of [{ revision: 1234567890 }, { revision: ["a"] }, { revision: {} }]) {
    const l = ui.buildLabel(v);
    assert.equal(l.text, "local build", JSON.stringify(v));
    assert.equal(l.unknown, true);
  }
});

test("applyBuildLabel fills the element and unhides it", () => {
  const el = { dataset: {}, hidden: true, textContent: "", title: "" };
  const l = ui.applyBuildLabel(el, {
    revision: "4cf18646880b540d48b029ea25a35ebe2e1821c2",
    built: "2026-09-23T10:32:58Z",
  });
  assert.equal(el.textContent, "build 4cf1864");
  assert.match(el.title, /4cf18646880b540d48b029ea25a35ebe2e1821c2/);
  assert.equal(el.dataset.unknown, "no");
  assert.equal(el.dataset.dirty, "no");
  assert.equal(el.hidden, false, "an element that is never unhidden shows nothing");
  assert.equal(l.text, "build 4cf1864");
});

test("applyBuildLabel marks a build the pipeline did not make, and a dirty one", () => {
  const unknown = { dataset: {}, hidden: true };
  ui.applyBuildLabel(unknown, { revision: "unknown", built: "unknown" });
  assert.equal(unknown.textContent, "local build");
  assert.equal(unknown.dataset.unknown, "yes");

  const dirty = { dataset: {}, hidden: true };
  ui.applyBuildLabel(dirty, { revision: "4cf18646880b540d48b029ea25a35ebe2e1821c2-dirty" });
  assert.equal(dirty.dataset.dirty, "yes");
  assert.equal(dirty.dataset.unknown, "no");
});

test("applyBuildLabel on a page without the element does nothing and does not throw", () => {
  assert.equal(ui.applyBuildLabel(null, { revision: "unknown" }), null);
});
