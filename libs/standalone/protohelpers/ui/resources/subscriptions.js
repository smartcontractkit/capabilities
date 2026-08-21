// The subscription sidebar and its tables.
//
// A trigger is not a call: it is registered once and then delivers whatever it
// delivers. So it does not fit the Response tab, which shows what one invoke
// answered. It gets a sidebar of live subscriptions instead, and a table per
// subscription: the event down the side, one column per instance.
//
// The table is what the whole thing is for. Every instance is meant to deliver the
// same payload for the same event, so the payloads are hashed and the row shows
// the distinct hashes. One hash is agreement. Two is a bug, and the row says so.
//
// Closing the window closes the subscription - the stream ends, and the server
// unregisters once nobody comes back. Which is why this reattaches to everything
// live as soon as it loads: a reload is a window closing, and coming straight back
// is what tells the server it was a reload.

$(function () {
    var CONFIG = window.__CRE_REQUEST__ || {
        instances: [], services: {}, uiPath: "", prefix: "", metadata: [],
        subscriptions: [], triggerIdHeader: ""
    };
    var COOKIE = "_cre_debug_csrf_token";
    var HEADER = "x-cre-debug-csrf-token";

    // The tables are cached so a reload comes back to the table it left, even for
    // a subscription the server has since dropped: the server keeps a bounded ring
    // for a reattaching reader, and this keeps what the browser was shown.
    // Versioned: a cached table is a row shape as well as some data, so a build
    // that changes the shape must not read the last one's back.
    var CACHE_KEY = "cre-debug-subscriptions-v2-" + window.location.host;
    var MAX_CACHED_ROWS = 200;
    var MAX_CACHED_SUBSCRIPTIONS = 20;

    // ---- state ---------------------------------------------------------------
    //
    // status : what the server says about each subscription, by trigger ID.
    // tables : the rows this browser has been shown, by trigger ID. Cached.
    // streams: the open EventSource per trigger ID.
    // acked  : events already acknowledged, so a reload does not acknowledge them
    //          again - which would say "seen" about something nobody has seen.

    var status = {};
    var tables = {};
    var streams = {};
    var acked = {};
    var selected = null;
    var minimised = false;

    // ---- cache ---------------------------------------------------------------

    function readCache() {
        try {
            var parsed = JSON.parse(localStorage.getItem(CACHE_KEY) || "{}");
            tables = parsed.tables || {};
            acked = parsed.acked || {};
            minimised = !!parsed.minimised;
            selected = parsed.selected || null;
        } catch (e) {
            tables = {};
            acked = {};
        }
    }

    function writeCache() {
        // Oldest subscriptions go first, and each table is capped, so a trigger
        // firing all day cannot fill the quota and take the rest with it.
        var ids = Object.keys(tables).sort(function (a, b) {
            return (tables[a].created || 0) - (tables[b].created || 0);
        });
        while (ids.length > MAX_CACHED_SUBSCRIPTIONS) {
            delete tables[ids.shift()];
        }

        var trimmed = {};
        ids.forEach(function (id) {
            var table = tables[id];
            var order = table.order.slice(Math.max(0, table.order.length - MAX_CACHED_ROWS));
            var rows = {};
            order.forEach(function (rowId) { rows[rowId] = table.rows[rowId]; });
            trimmed[id] = { meta: table.meta, created: table.created, order: order, rows: rows };
        });

        try {
            localStorage.setItem(CACHE_KEY, JSON.stringify({
                tables: trimmed, acked: acked, minimised: minimised, selected: selected
            }));
        } catch (e) {
            console.trace(e);
        }
    }

    // ---- table state ---------------------------------------------------------

    function table(triggerId) {
        if (!tables[triggerId]) {
            tables[triggerId] = { meta: {}, created: Date.now(), order: [], rows: {} };
        }
        return tables[triggerId];
    }

    function mergeRow(triggerId, row) {
        var t = table(triggerId);
        if (!t.rows[row.id]) {
            t.order.push(row.id);
        }
        // Whole rows are sent, so the latest is the truth rather than something to
        // fold the previous one into.
        t.rows[row.id] = row;
    }

    function mergeStatus(triggerId, s) {
        if (!s) {
            return;
        }
        status[triggerId] = s;
        table(triggerId).meta = {
            capabilityId: s.capabilityId,
            service: s.service,
            method: s.method,
            instances: s.instances || []
        };
    }

    // ---- streams -------------------------------------------------------------

    function open(triggerId) {
        if (streams[triggerId]) {
            return;
        }
        var source = new EventSource(
            CONFIG.prefix + "/request/subscriptions/stream?trigger=" + encodeURIComponent(triggerId));

        source.onmessage = function (e) {
            var message;
            try {
                message = JSON.parse(e.data);
            } catch (err) {
                return;
            }
            handle(triggerId, message);
        };

        // A subscription the server has dropped answers the reconnect with a 400,
        // which EventSource treats as fatal and stops retrying. Anything else is a
        // hiccup it retries on its own, so there is nothing to do here.
        source.onerror = function () {
            if (source.readyState === EventSource.CLOSED) {
                delete streams[triggerId];
                if (status[triggerId]) {
                    status[triggerId].closed = true;
                }
                render();
            }
        };

        streams[triggerId] = source;
    }

    function close(triggerId) {
        if (streams[triggerId]) {
            streams[triggerId].close();
            delete streams[triggerId];
        }
    }

    function handle(triggerId, message) {
        switch (message.type) {
            case "snapshot":
                mergeStatus(triggerId, message.status);
                (message.status.rows || []).forEach(function (row) {
                    mergeRow(triggerId, row);
                    ack(triggerId, row);
                });
                break;
            case "row":
                mergeRow(triggerId, message.row);
                ack(triggerId, message.row);
                break;
            case "attached":
                mergeStatus(triggerId, message.status);
                break;
            case "closed":
                mergeStatus(triggerId, message.status);
                close(triggerId);
                break;
            default:
                return;
        }
        if (!selected) {
            selected = triggerId;
        }
        writeCache();
        render();
    }

    // ---- acknowledging -------------------------------------------------------
    //
    // The browser acks, not the server on delivery: a capability that redelivers
    // what was not acknowledged is behaving correctly, and acking on delivery would
    // hide exactly that. So an event is acknowledged once it has been put in a
    // table a person is looking at.

    function ack(triggerId, row) {
        if (!row || !row.id) {
            return;
        }
        var seen = acked[triggerId] || (acked[triggerId] = {});
        if (seen[row.id]) {
            return;
        }
        seen[row.id] = true;

        post("/request/subscriptions/ack", { triggerId: triggerId, eventId: row.id })
            .fail(function (xhr) {
                // Left un-acked so it is retried if it arrives again, rather than
                // silently recorded as acknowledged.
                delete seen[row.id];
                console.warn("failed to acknowledge " + row.id + ": " + (xhr.responseText || xhr.status));
            });
    }

    function csrfToken() {
        return document.cookie.replace(
            new RegExp("(?:(?:^|.*;\\s*)" + COOKIE + "\\s*\\=\\s*([^;]*).*$)|^.*$"), "$1");
    }

    function post(path, body) {
        return $.ajax({
            url: CONFIG.prefix + path,
            type: "POST",
            contentType: "application/json",
            data: JSON.stringify(body),
            beforeSend: function (xhr) {
                xhr.setRequestHeader(HEADER, csrfToken());
            }
        });
    }

    // ---- rendering: sidebar --------------------------------------------------

    function badge(s) {
        if (!s) {
            return { text: "cached", cls: "cre-badge-cached" };
        }
        if (s.closed) {
            return { text: "closed", cls: "cre-badge-closed" };
        }
        if (s.inGrace) {
            return { text: "no readers", cls: "cre-badge-grace" };
        }
        return { text: "live", cls: "cre-badge-live" };
    }

    function renderSidebar() {
        var $panel = $("#cre-subscriptions");
        $panel.toggleClass("cre-sidebar-minimised", minimised);
        $("#cre-sidebar-toggle").text(minimised ? "«" : "»")
            .attr("title", minimised ? "Show subscriptions" : "Minimise");

        var ids = Object.keys(tables).sort(function (a, b) {
            return (tables[a].created || 0) - (tables[b].created || 0);
        });

        var $list = $("#cre-subscription-list").empty();
        $("#cre-sidebar-count").text(ids.length ? String(ids.length) : "");

        if (ids.length === 0) {
            $list.append($("<div>", { "class": "cre-sidebar-empty", text: "No subscriptions yet." }));
            return;
        }

        ids.forEach(function (id) {
            var t = tables[id];
            var s = status[id];
            var mark = badge(s);

            var $row = $("<div>", {
                "class": "cre-subscription" + (id === selected ? " cre-subscription-selected" : "")
            });

            var $close = $("<button>", { "class": "cre-subscription-close", text: "×", title: "Close this subscription" });
            $close.on("click", function (e) {
                e.stopPropagation();
                closeSubscription(id);
            });
            $row.append($close);

            $row.append($("<div>", { "class": "cre-subscription-id", text: id, title: id }));
            $row.append($("<div>", {
                "class": "cre-subscription-method",
                text: (t.meta.service ? t.meta.service + "." : "") + (t.meta.method || "")
            }));

            var instances = (t.meta.instances || []).length;
            $row.append($("<div>", { "class": "cre-subscription-facts" })
                .append($("<span>", { "class": "cre-badge " + mark.cls, text: mark.text }))
                .append($("<span>", { text: instances + (instances === 1 ? " instance" : " instances") }))
                .append($("<span>", { text: t.order.length + (t.order.length === 1 ? " event" : " events") })));

            $row.on("click", function () {
                selected = id;
                writeCache();
                render();
            });

            $list.append($row);
        });
    }

    function closeSubscription(triggerId) {
        post("/request/subscriptions/close", { triggerId: triggerId })
            .always(function () {
                close(triggerId);
                if (status[triggerId]) {
                    status[triggerId].closed = true;
                }
                render();
            });
    }

    // ---- rendering: the table ------------------------------------------------

    // Columns are every instance that is attached or has ever delivered, so an
    // instance that has been detached keeps the column its events are in.
    function columns(triggerId) {
        var t = table(triggerId);
        var byIndex = {};

        (t.meta.instances || []).forEach(function (i) {
            byIndex[i.instance] = i.label;
        });
        t.order.forEach(function (id) {
            (t.rows[id].nodes || []).forEach(function (n) {
                if (byIndex[n.instance] === undefined) {
                    byIndex[n.instance] = n.label;
                }
            });
        });

        return Object.keys(byIndex).map(Number).sort(function (a, b) { return a - b; })
            .map(function (index) { return { instance: index, label: byIndex[index] }; });
    }

    function stamp(iso) {
        var at = new Date(iso);
        if (isNaN(at.getTime())) {
            return "";
        }
        return at.toLocaleTimeString([], { hour12: false }) +
            "." + String(at.getMilliseconds()).padStart(3, "0");
    }

    // An instance cell says when this process saw that instance's delivery, and -
    // only when the row has more than one payload - which of them it sent. With one
    // payload there is nothing to point at: everyone sent the one payload.
    function nodeCell(node, split) {
        var $cell = $("<td>", { "class": "cre-event-node" });
        if (!node) {
            return $cell.addClass("cre-event-missing").append($("<span>", { text: "—" }));
        }
        if (node.error) {
            return $cell.addClass("cre-event-error")
                .append($("<div>", { "class": "cre-event-time", text: "Arrived at: " + stamp(node.at) }))
                .append($("<div>", { "class": "cre-error", text: node.error }));
        }

        $cell.append($("<div>", { "class": "cre-event-time", text: "Arrived at: " + stamp(node.at) }));
        if (split) {
            $cell.append($("<div>", {
                "class": "cre-event-index",
                text: "index: " + (node.payloadIndex >= 0 ? node.payloadIndex : "—")
            }));
        }
        return $cell;
    }

    function pretty(payload) {
        if (payload === undefined || payload === null) {
            return "";
        }
        try {
            return JSON.stringify(payload, null, 2);
        } catch (e) {
            return String(payload);
        }
    }

    function renderTable() {
        var $view = $("#cre-subscription-view").empty();
        if (!selected || !tables[selected]) {
            return;
        }

        var triggerId = selected;
        var t = tables[triggerId];
        var s = status[triggerId];
        var mark = badge(s);
        var cols = columns(triggerId);

        var $head = $("<div>", { "class": "cre-subscription-view-head" });
        $head.append($("<h3>", {
            text: (t.meta.service ? t.meta.service + "." : "") + (t.meta.method || "subscription")
        }));
        $head.append($("<div>", { "class": "cre-subscription-view-facts" })
            .append($("<span>", { "class": "cre-badge " + mark.cls, text: mark.text }))
            .append($("<code>", { text: triggerId })));
        $view.append($head);

        if (t.order.length === 0) {
            $view.append($("<div>", { "class": "cre-sidebar-empty", text: "Nothing has fired yet." }));
            return;
        }

        // The payload column is split once any row in the table has more than one
        // distinct payload, so the columns do not move about as rows arrive. A row
        // with fewer leaves the trailing ones empty.
        var widest = 1;
        t.order.forEach(function (id) {
            widest = Math.max(widest, ((t.rows[id].payloadIds) || []).length);
        });
        var split = widest > 1;

        var $table = $("<table>", { "class": "cre-event-table" });

        var $header = $("<tr>");
        $header.append($("<th>", { text: "Event ID", rowspan: split ? 2 : 1 }));
        $header.append($("<th>", { text: "Hash", rowspan: split ? 2 : 1 }));
        $header.append($("<th>", { text: "Payload", colspan: widest }));
        cols.forEach(function (c) {
            $header.append($("<th>", {
                text: c.label || ("instance " + (c.instance + 1)),
                rowspan: split ? 2 : 1
            }));
        });
        $table.append($header);

        if (split) {
            var $sub = $("<tr>");
            for (var i = 0; i < widest; i++) {
                $sub.append($("<th>", { "class": "cre-payload-subhead", text: "index " + i }));
            }
            $table.append($sub);
        }

        // Newest first: what just fired is what is being watched.
        t.order.slice().reverse().forEach(function (id) {
            var row = t.rows[id];
            var $tr = $("<tr>", { "class": row.diverged ? "cre-event-diverged" : "" });

            $tr.append($("<td>", { "class": "cre-event-id", text: row.id }));

            // The hashes, in the same order as the payload columns beside them, so
            // the two read across together.
            var $hashes = $("<td>", { "class": "cre-event-hashes" });
            (row.payloadIds || []).forEach(function (payloadId, index) {
                $hashes.append($("<div>", { "class": "cre-hash-line" })
                    .append(split ? $("<span>", { "class": "cre-hash-index", text: index + ": " }) : null)
                    .append($("<code>", {
                        "class": "cre-payload-id-tag" + (row.diverged ? " cre-payload-id-diverged" : ""),
                        text: payloadId
                    })));
            });
            if (row.diverged) {
                $hashes.append($("<div>", { "class": "cre-diverged-note", text: "instances disagree" }));
            }
            $tr.append($hashes);

            // One cell per distinct payload, in that same order.
            for (var p = 0; p < widest; p++) {
                var payload = (row.payloads || [])[p];
                $tr.append($("<td>", { "class": "cre-event-payload" })
                    .append(payload === undefined
                        ? $("<span>", { "class": "cre-event-missing", text: "—" })
                        : $("<pre>", { "class": "cre-payload", text: pretty(payload) })));
            }

            var byInstance = {};
            (row.nodes || []).forEach(function (n) { byInstance[n.instance] = n; });
            cols.forEach(function (c) {
                $tr.append(nodeCell(byInstance[c.instance], split));
            });

            $table.append($tr);
        });

        $view.append($table);
    }

    function render() {
        renderSidebar();
        renderTable();
    }

    // ---- startup -------------------------------------------------------------

    // Reattach to everything the server still has. This is what makes a reload
    // survivable: the server gives an abandoned subscription a grace period, and
    // coming back inside it is what says the window was reloaded rather than closed.
    function reattach() {
        $.ajax({ url: CONFIG.prefix + "/request/subscriptions", dataType: "json" })
            .done(function (data) {
                (data.subscriptions || []).forEach(function (s) {
                    mergeStatus(s.triggerId, s);
                    open(s.triggerId);
                });
                if (!selected && (data.subscriptions || []).length) {
                    selected = data.subscriptions[0].triggerId;
                }
                writeCache();
                render();
            });
    }

    // The bridge the request page uses: it knows a subscription was just opened,
    // and this knows what to do about it.
    window.__creSubscriptions = {
        watch: function (triggerId) {
            if (!triggerId) {
                return;
            }
            table(triggerId);
            open(triggerId);
            selected = triggerId;
            minimised = false;
            writeCache();
            render();
        },
        // isLive answers what a history replay needs: rejoin something still
        // running, or subscribe again.
        isLive: function (triggerId) {
            var s = status[triggerId];
            return !!s && !s.closed;
        },
        refresh: reattach
    };

    $("#cre-sidebar-toggle").on("click", function () {
        minimised = !minimised;
        writeCache();
        render();
    });

    readCache();
    render();
    reattach();
});
