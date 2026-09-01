// Fan-out page: one or more requests, sent across every instance this process runs.
//
// Each request is the real per-instance page, embedded in a same-origin iframe.
// That is deliberate: grpcui builds its form from a closure keyed to a single
// #grpc-request-form, so it cannot be asked to render a second form on one page.
// An iframe gives a genuine duplicate - same widgets, same validation - and because
// it is same-origin the body can be read straight out of it through the hooks
// form.js exports.
//
// The metadata is the page's own, not each request's: it is filled in here, sent
// with every group, and so identical across instances. A difference between their
// answers is then the capability's rather than the call's.

$(function () {
    var CONFIG = window.__CRE_REQUEST__ || {
        instances: [], services: {}, uiPath: "", prefix: "", metadata: [],
        subscriptions: [], triggerIdHeader: "",
        special: { bigInt: "", decimal: "", methods: {}, requests: {} }
    };
    var COOKIE = "_cre_debug_csrf_token";
    var HEADER = "x-cre-debug-csrf-token";

    // ---- special value types -------------------------------------------------
    //
    // A values.v1.BigInt is a sign and base64 bytes, a values.v1.Decimal is that
    // plus an exponent, and neither reads as the number it stands for. So what is
    // shown is a copy with those replaced.
    //
    // A copy, deliberately. History keeps the raw response, and Save History writes
    // what history holds, so a saved file is still the messages a replay needs -
    // paste it into Postman and it is the request and response as they went over
    // the wire, not this page's rendering of them.

    var SPECIAL = CONFIG.special || { bigInt: "", decimal: "", methods: {} };
    var VALUES = window.__CRE_VALUES__;

    // The response paths for a method. See pathsFor.
    function specialPathsFor(method) {
        return pathsFor(SPECIAL.methods, method);
    }

    function specialRequestPathsFor(method) {
        return pathsFor(SPECIAL.requests, method);
    }

    // ---- bytes -----------------------------------------------------------------
    //
    // A bytes field arrives as base64, which is not how any of these values are
    // written anywhere else. The dropdown over the response picks which encoding is
    // shown; history keeps the base64 either way, for the same reason it keeps the
    // raw messages - a saved history is still what a replay has to send.
    //
    // Page-wide rather than per-field: a response is read whole, and hunting for
    // the one field whose box says hex is not reading it.
    var bytesEncoding = "base64";

    function bytesPathsFor(method) {
        return pathsFor(SPECIAL.bytesMethods, method);
    }

    function bytesRequestPathsFor(method) {
        return pathsFor(SPECIAL.bytesRequests, method);
    }

    // The paths for a method, as the server named it. The page names a method
    // "service.method"; the paths are keyed "/service/method".
    function pathsFor(paths, method) {
        if (!method || !VALUES) {
            return [];
        }
        var dot = method.lastIndexOf(".");
        if (dot === -1) {
            return [];
        }
        return (paths || {})["/" + method.slice(0, dot) + "/" + method.slice(dot + 1)] || [];
    }

    // Whether a method has anything for the dropdown to act on. Offered whenever the
    // type has a bytes field in it, whether or not this particular exchange filled
    // one in: a control that came and went with the data would be one you could not
    // reach for in advance.
    function hasBytes(method) {
        return bytesPathsFor(method).length > 0 || bytesRequestPathsFor(method).length > 0;
    }

    // requestAsNumbers is a request body with its special messages replaced.
    //
    // Keyed lowerCamelCase, because a request body here is grpcui's own model
    // rather than something off the wire.
    function requestAsNumbers(body, method) {
        if (!VALUES) {
            return body;
        }
        var shown = VALUES.parsed(body, specialRequestPathsFor(method), SPECIAL, true);
        if (bytesEncoding === "hex") {
            shown = VALUES.hexed(shown, bytesRequestPathsFor(method), true);
        }
        return shown;
    }

    // asNumbers is one instance's response with its special messages replaced.
    //
    // The payload is grpcui's envelope, so the configured paths are followed from
    // each message inside it rather than from the top.
    function asNumbers(payload, method) {
        var paths = specialPathsFor(method);
        var bytes = bytesEncoding === "hex" ? bytesPathsFor(method) : [];
        if ((!paths.length && !bytes.length) || !payload || typeof payload !== "object") {
            return payload;
        }

        var copy;
        try {
            copy = JSON.parse(JSON.stringify(payload));
        } catch (e) {
            return payload;
        }
        if (!(copy.responses instanceof Array)) {
            return copy;
        }
        copy.responses = copy.responses.map(function (entry) {
            if (!entry || typeof entry !== "object" || entry.message === undefined) {
                return entry;
            }
            entry.message = VALUES.parsed(entry.message, paths, SPECIAL);
            entry.message = VALUES.hexed(entry.message, bytes);
            return entry;
        });
        return copy;
    }

    // ---- state ---------------------------------------------------------------
    //
    // selected : instances the fan-out addresses at all. Unselected ones report
    //            N/A without being contacted.
    // mirror   : one body for every selected instance.
    // requests : the per-request claim sets, always disjoint, so an instance
    //            belongs to exactly one request.
    //
    // A request's uid never changes: it keys the live iframe, so re-rendering never
    // reloads a form already filled in. The number shown is the position in the
    // list, so numbering is always 1..N with no gaps.

    var state = {
        selected: CONFIG.instances.map(function (i) { return i.index; }),
        mirror: true,
        requests: [{ uid: 1, claims: CONFIG.instances.map(function (i) { return i.index; }) }]
    };
    var nextUid = 2;
    var requestEls = {};

    // ---- history persistence -------------------------------------------------
    //
    // The same scheme grpcui uses for its own History tab: localStorage, capped at
    // maxHistory entries and maxHistorySize bytes, dropping the oldest until it
    // fits, with every access guarded because incognito mode and a full quota both
    // throw.

    var HISTORY_KEY = "cre-debug-history-" + window.location.host;
    var maxHistory = 100;
    var maxHistorySize = 1024 * 1024;
    var history = [];

    function getStorageItem(key) {
        try {
            return localStorage.getItem(key);
        } catch (e) {
            return null;
        }
    }

    function setStorageItem(key, value) {
        try {
            localStorage.setItem(key, value);
        } catch (e) {
            console.trace(e);
        }
    }

    function loadHistory() {
        var json = getStorageItem(HISTORY_KEY);
        if (!json) {
            return;
        }
        try {
            var parsed = JSON.parse(json);
            if (parsed instanceof Array) {
                history = parsed;
            }
        } catch (e) {
            history = [];
        }
    }

    function onHistoryChange() {
        var data;
        while (true) {
            data = JSON.stringify(history);
            if (data.length <= maxHistorySize || history.length === 0) {
                break;
            }
            history = history.slice(0, history.length - 1);
        }
        setStorageItem(HISTORY_KEY, data);
        renderHistory();
    }

    function addHistory(item) {
        history = history.slice(0, maxHistory - 1);
        history.unshift(item);
        onHistoryChange();
    }

    // ---- helpers -------------------------------------------------------------

    function instanceByIndex(idx) {
        return CONFIG.instances.filter(function (i) { return i.index === idx; })[0];
    }

    function isSelected(idx) {
        return state.selected.indexOf(idx) !== -1;
    }

    function claimsOf(request) {
        return request.claims.filter(isSelected);
    }

    function unclaimed() {
        var taken = {};
        state.requests.forEach(function (r) {
            claimsOf(r).forEach(function (i) { taken[i] = true; });
        });
        return state.selected.filter(function (i) { return !taken[i]; });
    }

    function currentMethodPath() {
        var service = $("#grpc-service").val();
        var method = $("#grpc-method").val();
        if (!service || !method) {
            return null;
        }
        return service + "." + method;
    }

    function iframeSrc() {
        return CONFIG.uiPath + "/?embed=1&serviceName=" + encodeURIComponent($("#grpc-service").val()) +
            "&methodName=" + encodeURIComponent($("#grpc-method").val());
    }

    // A subscription is filled in and sent like anything else - the form is the
    // trigger's own input, which is its configuration - but what comes back is a
    // registration rather than a response. So the page has to know which of the two
    // it is looking at: what to call the button, and what to do with the answer.
    function isSubscribing() {
        return (CONFIG.subscriptions || []).indexOf($("#grpc-service").val()) !== -1;
    }

    // renderMode switches the page between the two.
    function renderMode() {
        var subscribing = isSubscribing();
        $("#cre-trigger-row").toggle(subscribing);
        $("#cre-invoke").text(subscribing ? "Subscribe" : "Invoke");
    }

    // ---- request cards -------------------------------------------------------

    function buildRequestCard(request) {
        var $card = $("<div>", { "class": "cre-request", "data-request-uid": request.uid });

        var $head = $("<div>", { "class": "cre-request-head" });
        var $title = $("<span>", { "class": "cre-request-title" });
        var $copy = $("<span>", { "class": "cre-request-copy" });
        var $targets = $("<span>", { "class": "cre-request-targets" });
        $head.append($title).append($copy).append($targets);
        $card.append($head);

        var $frame = $("<iframe>", { "class": "cre-frame", src: iframeSrc() });
        $card.append($frame);

        requestEls[request.uid] = {
            card: $card, title: $title, copy: $copy, targets: $targets, frame: $frame
        };
        return $card;
    }

    // "Copy from N" for the whole body, shown once there is another request.
    function renderCopyControl(request, number) {
        var $copy = requestEls[request.uid].copy;
        $copy.empty();

        var peers = state.requests
            .map(function (r, i) { return { request: r, number: i + 1 }; })
            .filter(function (p) { return p.request.uid !== request.uid; });
        if (peers.length === 0) {
            return;
        }

        $copy.append($("<span>", { text: "Copy from:" }));
        peers.forEach(function (p) {
            var $btn = $("<button>", { "class": "cre-copy-btn", text: p.number });
            $btn.attr("title", "Replace this request's fields with those of request " + p.number);
            $btn.on("click", function () {
                var win = requestEls[request.uid].frame[0].contentWindow;
                var body;
                try {
                    body = readBody(p.request, p.number);
                } catch (e) {
                    renderError(e.message);
                    return;
                }
                if (!win || typeof win.__creWriteRequestBody !== "function") {
                    renderError("Request " + number + " is still loading");
                    return;
                }
                win.__creWriteRequestBody(body);
            });
            $copy.append($btn);
        });
    }

    // The per-request instance checkboxes. Checking an instance takes it away from
    // whichever request held it, keeping the claim sets disjoint.
    //
    // Rebuilt only when the set of selectable instances changes; otherwise the
    // boxes are updated in place. Recreating them on every change would leave
    // detached nodes whose handlers still mutate state, so a click landing
    // mid-rebuild could be applied to the wrong request.
    function renderTargets(request) {
        var els = requestEls[request.uid];
        var $targets = els.targets;

        if (state.mirror) {
            els.renderedFor = null;
            $targets.empty().text("all selected instances");
            return;
        }

        var signature = state.selected.join(",");
        if (els.renderedFor === signature) {
            $targets.find("input[type=checkbox]").each(function () {
                var idx = Number($(this).attr("data-instance"));
                this.checked = request.claims.indexOf(idx) !== -1;
            });
            return;
        }
        els.renderedFor = signature;
        $targets.empty();

        state.selected.forEach(function (idx) {
            var inst = instanceByIndex(idx);
            var $label = $("<label>", { "class": "cre-target" });
            var $box = $("<input>", {
                type: "checkbox",
                "data-instance": idx,
                checked: request.claims.indexOf(idx) !== -1
            });
            $box.on("change", function () {
                if (this.checked) {
                    // Exclusive: drop it from every other request first.
                    state.requests.forEach(function (r) {
                        if (r.uid !== request.uid) {
                            r.claims = r.claims.filter(function (i) { return i !== idx; });
                        }
                    });
                    if (request.claims.indexOf(idx) === -1) {
                        request.claims.push(idx);
                    }
                } else {
                    request.claims = request.claims.filter(function (i) { return i !== idx; });
                }
                render();
            });
            $label.append($box).append($("<span>", { text: " " + inst.label }));
            $targets.append($label);
        });
    }

    // Brings state.requests in line with the current selection.
    function reconcileRequests() {
        if (state.mirror) {
            state.requests = [state.requests[0]];
            state.requests[0].claims = state.selected.slice();
            return;
        }

        state.requests.forEach(function (r) {
            r.claims = claimsOf(r);
        });

        if (unclaimed().length > 0) {
            // Somewhere to put the leftovers: this is what makes unchecking an
            // instance in request 1 produce a request 2.
            var hasEmpty = state.requests.some(function (r) { return r.claims.length === 0; });
            if (!hasEmpty) {
                state.requests.push({ uid: nextUid++, claims: [] });
            }
            return;
        }

        // Everything is assigned, so empty cards serve no purpose.
        var withClaims = state.requests.filter(function (r) { return r.claims.length > 0; });
        if (withClaims.length > 0) {
            state.requests = withClaims;
        }
    }

    function render() {
        reconcileRequests();

        var $container = $("#cre-requests");
        state.requests.forEach(function (request, i) {
            if (!requestEls[request.uid]) {
                // Appended only on creation. Re-appending an existing card would
                // move its iframe in the DOM, which reloads it and loses the form.
                $container.append(buildRequestCard(request));
            }
            requestEls[request.uid].title.text("Request " + (i + 1));
            renderCopyControl(request, i + 1);
            renderTargets(request);
        });

        Object.keys(requestEls).forEach(function (uid) {
            var alive = state.requests.some(function (r) { return String(r.uid) === String(uid); });
            if (!alive) {
                requestEls[uid].card.remove();
                delete requestEls[uid];
            }
        });

        renderInstanceSelect();
        renderPending();
    }

    // Unassigned instances block the fan-out: sending would silently skip them.
    function renderPending() {
        var left = unclaimed();
        var $note = $("#cre-pending");
        var blocked = !state.mirror && left.length > 0;

        if (blocked) {
            $note.show().text("Not assigned to any request: " + left.map(function (i) {
                return instanceByIndex(i).label;
            }).join(", "));
        } else {
            $note.hide().empty();
        }
        $("#cre-invoke").prop("disabled", blocked);
    }

    // ---- advanced ------------------------------------------------------------

    function renderInstanceSelect() {
        var $box = $("#cre-instance-select");
        if ($box.children().length) {
            $box.find("input[type=checkbox]").each(function () {
                this.checked = isSelected(Number($(this).attr("data-instance")));
            });
            return;
        }
        CONFIG.instances.forEach(function (inst) {
            var $label = $("<label>", { "class": "cre-target" });
            var $cb = $("<input>", { type: "checkbox", checked: true, "data-instance": inst.index });
            $cb.on("change", function () {
                var idx = Number($(this).attr("data-instance"));
                if (this.checked) {
                    if (!isSelected(idx)) {
                        state.selected.push(idx);
                        state.selected.sort(function (a, b) { return a - b; });
                    }
                } else {
                    state.selected = state.selected.filter(function (i) { return i !== idx; });
                }
                render();
            });
            $label.append($cb).append($("<span>", { text: " " + inst.label }));
            $box.append($label);
        });
    }

    // The metadata inputs, built from the RequestMetadata fields the server sent.
    function renderMetadata() {
        var $grid = $("#cre-metadata");
        CONFIG.metadata.forEach(function (field) {
            var id = "cre-fanout-md-" + field.name;
            var $row = $("<span>", { "class": "cre-metadata-field" });
            $row.append($("<label>", { text: metadataLabel(field), "for": id }));
            $row.append($("<input>", {
                type: metadataInputType(field),
                id: id,
                placeholder: field.default || "",
                "data-cre-header": field.header,
                "data-cre-repeated": field.repeated ? "1" : ""
            }));
            $grid.append($row);
        });
    }

    function metadataLabel(field) {
        var text = field.name.replace(/([a-z0-9])([A-Z])/g, "$1 $2");
        if (field.repeated) {
            text += " (one per line, type=limit)";
        }
        return text + ":";
    }

    function metadataInputType(field) {
        switch (field.kind) {
            case "number":
                return "number";
            case "timestamp":
                return "datetime-local";
            default:
                return "text";
        }
    }

    // The metadata every group is sent with, as header name -> values.
    //
    // Collected once per invoke and sent with all of them, which is what makes the
    // instances comparable: they are asked the same thing, not merely asked at the
    // same time. Blank fields are left out so the server fills in its own valid
    // value - the same value for every instance, since it is one request.
    function collectMetadata() {
        var out = {};
        $("#cre-metadata input[data-cre-header]").each(function () {
            var header = $(this).attr("data-cre-header");
            var repeated = $(this).attr("data-cre-repeated") === "1";
            var raw = $(this).val();
            if (raw === null || String(raw).trim() === "") {
                return;
            }
            var values = repeated ? String(raw).split(/\r?\n/) : [String(raw)];
            values.forEach(function (value) {
                if (value.trim() === "") {
                    return;
                }
                out[header] = (out[header] || []).concat([value]);
            });
        });

        // The trigger ID travels the same way, and is settled the same way: named
        // here it is used, left blank the server mints one - once, for the whole
        // fan-out, so every instance registers under the same subscription.
        if (isSubscribing()) {
            var triggerId = String($("#cre-trigger-id").val() || "").trim();
            if (triggerId !== "") {
                out[CONFIG.triggerIdHeader] = [triggerId];
            }
        }
        return out;
    }

    // ---- invoke --------------------------------------------------------------

    function readBody(request, number) {
        var win = requestEls[request.uid].frame[0].contentWindow;
        if (!win || typeof win.__creReadRequestBody !== "function") {
            throw new Error("Request " + number + " is still loading");
        }
        return win.__creReadRequestBody();
    }

    function csrfToken() {
        return document.cookie.replace(
            new RegExp("(?:(?:^|.*;\\s*)" + COOKIE + "\\s*\\=\\s*([^;]*).*$)|^.*$"), "$1");
    }

    function invoke() {
        var method = currentMethodPath();
        if (!method) {
            return;
        }

        var groups = [];
        try {
            state.requests.forEach(function (request, i) {
                var targets = state.mirror ? state.selected.slice() : claimsOf(request);
                if (targets.length === 0) {
                    return;
                }
                groups.push({ instances: targets, body: readBody(request, i + 1) });
            });
        } catch (e) {
            renderError(e.message);
            return;
        }
        if (groups.length === 0) {
            renderError("No instances selected");
            return;
        }

        var metadata = collectMetadata();
        var subscribing = isSubscribing();
        var $button = $("#cre-invoke");
        $button.prop("disabled", true);
        var started = Date.now();

        $.ajax({
            url: CONFIG.prefix + "/request/fanout",
            type: "POST",
            contentType: "application/json",
            data: JSON.stringify({ method: method, groups: groups, metadata: metadata }),
            beforeSend: function (xhr) {
                xhr.setRequestHeader(HEADER, csrfToken());
            }
        }).done(function (data) {
            renderResults(data);
            addHistory({
                startTime: started,
                durationMS: Date.now() - started,
                method: method,
                groups: groups,
                metadata: metadata,
                subscribe: subscribing,
                triggerId: data.triggerId || "",
                data: data
            });

            if (subscribing && data.triggerId) {
                // Shown, because a caller that named none still has to know which
                // subscription this was - it is how the events are found again, and
                // how another instance joins this one later.
                $("#cre-trigger-id").val(data.triggerId);
                if (window.__creSubscriptions) {
                    window.__creSubscriptions.watch(data.triggerId);
                }
            }
        }).fail(function (xhr) {
            renderError("Fan-out failed: " + xhr.status + " " + (xhr.responseText || ""));
        }).always(function () {
            renderPending();
        });
    }

    // ---- results -------------------------------------------------------------

    // The responses are shown the way a trigger's events are: the distinct answers
    // once each, and a column per instance saying which of them it gave.
    //
    // A card per instance repeated the same JSON once per instance, which for four
    // instances agreeing is three copies of the answer and no way to see at a
    // glance that they agreed. Here one hash means they did.
    function resultGrid(data) {
        var results = data.results || [];
        var hashes = data.payloadIds || [];
        var payloads = data.payloads || [];
        var widest = Math.max(hashes.length, 1);

        // Only one group means every instance was asked the same thing, so
        // different answers are the capability disagreeing with itself. With several
        // groups they were asked different things, and differing is the point.
        var groups = {};
        results.forEach(function (r) {
            if (r.status !== "na") { groups[r.group] = true; }
        });
        var comparable = Object.keys(groups).length <= 1;
        var diverged = !!data.diverged && comparable;

        var $table = $("<table>", { "class": "cre-event-table" });

        var $header = $("<tr>", { "class": diverged ? "cre-event-diverged" : "" });
        $header.append($("<th>", { text: "Hash", rowspan: widest > 1 ? 2 : 1 }));
        $header.append($("<th>", { text: "Response", colspan: widest }));
        results.forEach(function (r) {
            $header.append($("<th>", { text: r.label, rowspan: widest > 1 ? 2 : 1 }));
        });
        $table.append($header);

        if (widest > 1) {
            var $sub = $("<tr>", { "class": diverged ? "cre-event-diverged" : "" });
            for (var i = 0; i < widest; i++) {
                $sub.append($("<th>", { "class": "cre-payload-subhead", text: "index " + i }));
            }
            $table.append($sub);
        }

        var $row = $("<tr>", { "class": diverged ? "cre-event-diverged" : "" });

        var $hashes = $("<td>", { "class": "cre-event-hashes" });
        hashes.forEach(function (hash, index) {
            $hashes.append($("<div>", { "class": "cre-hash-line" })
                .append(widest > 1 ? $("<span>", { "class": "cre-hash-index", text: index + ": " }) : null)
                .append($("<code>", {
                    "class": "cre-payload-id-tag" + (diverged ? " cre-payload-id-diverged" : ""),
                    text: hash
                })));
        });
        if (diverged) {
            $hashes.append($("<div>", { "class": "cre-diverged-note", text: "instances disagree" }));
        }
        if (hashes.length === 0) {
            $hashes.append($("<span>", { "class": "cre-event-missing", text: "—" }));
        }
        $row.append($hashes);

        for (var p = 0; p < widest; p++) {
            var payload = payloads[p];
            $row.append($("<td>", { "class": "cre-event-payload" })
                .append(payload === undefined
                    ? $("<span>", { "class": "cre-event-missing", text: "—" })
                    : $("<pre>", { "class": "cre-payload", text: JSON.stringify(asNumbers(payload, data.method), null, 2) })));
        }

        results.forEach(function (r) {
            $row.append(resultCell(r, widest > 1));
        });
        $table.append($row);

        return $table;
    }

    // One instance's answer: which response it gave, which request it was sent, and
    // the failure instead if it did not answer.
    function resultCell(result, split) {
        var $cell = $("<td>", { "class": "cre-event-node cre-result-" + result.status });

        if (result.status === "na") {
            return $cell.addClass("cre-event-missing").append($("<span>", { text: "N/A" }));
        }
        if (result.status === "error") {
            return $cell.append($("<pre>", { "class": "cre-error", text: result.error }));
        }

        if (split) {
            $cell.append($("<div>", {
                "class": "cre-event-index",
                text: "index: " + (result.responseIndex >= 0 ? result.responseIndex : "—")
            }));
        } else {
            $cell.append($("<div>", { "class": "cre-event-index", text: "answered" }));
        }
        $cell.append($("<div>", { "class": "cre-result-group", text: "request " + result.group }));
        return $cell;
    }

    // The dropdown itself, one per place a response is shown. They share the one
    // setting, so picking hex in either shows hex in both.
    function bytesEncodingControl() {
        var $select = $("<select>", {
            "class": "cre-bytes-encoding",
            title: "How bytes fields are shown. History keeps the base64 either way."
        });
        $select.append($("<option>", { value: "base64", text: "base64" }));
        $select.append($("<option>", { value: "hex", text: "hex" }));
        $select.val(bytesEncoding);
        $select.on("change", function () {
            bytesEncoding = this.value;
            var inHistory = $(this).closest("#grpc-history-list").length > 0;
            if (lastResults) {
                // Rendered where it stands: switching encoding from the History tab
                // should not throw the page back to the Response one.
                renderResults(lastResults, true);
            }
            renderHistory();
            // Both renders replace this very dropdown, so the focus that was on it
            // is put back on the one that took its place.
            $(inHistory ? "#grpc-history-list" : "#cre-response")
                .find("select.cre-bytes-encoding").first().focus();
        });
        return $("<div>", { "class": "cre-encoding-controls" })
            .append($("<span>", { "class": "cre-encoding-label", text: "Bytes:" }))
            .append($select);
    }

    // The last fan-out, kept so the Response tab can be re-rendered in another
    // encoding without sending it again.
    var lastResults = null;

    function renderResults(data, keepTab) {
        lastResults = data;
        var $out = $("#cre-response").empty();
        $out.append($("<div>", { "class": "cre-method", text: data.method }));
        if (hasBytes(data.method)) {
            $out.append(bytesEncodingControl());
        }
        $out.append(resultGrid(data));
        if (!keepTab) {
            selectTab(0);
        }
    }

    function renderError(message) {
        $("#cre-response").empty().append($("<pre>", { "class": "cre-error", text: message }));
        selectTab(0);
    }

    // ---- history -------------------------------------------------------------
    //
    // The same shape as grpcui's own History tab: a jQuery UI accordion of
    // .history-item-header rows over .history-item-panel details, reusing its class
    // names so the styling comes along.

    function summarise(results) {
        var counts = { ok: 0, error: 0, na: 0 };
        results.forEach(function (r) { counts[r.status] = (counts[r.status] || 0) + 1; });
        var parts = [];
        if (counts.ok) { parts.push(counts.ok + " OK"); }
        if (counts.error) { parts.push(counts.error + " failed"); }
        if (counts.na) { parts.push(counts.na + " N/A"); }
        return { text: parts.join(", "), failed: counts.error > 0 };
    }

    function renderHistory() {
        var $list = $("#grpc-history-list").empty();
        if (history.length === 0) {
            $list.append($("<div>", { "class": "cre-history-empty", text: "No requests yet." }));
            return;
        }

        if (history.some(function (item) { return hasBytes(item.method); })) {
            $list.append(bytesEncodingControl());
        }

        var $accordion = $("<div>");
        $list.append($accordion);

        history.forEach(function (item, i) {
            var summary = summarise(item.data.results);
            var groups = item.groups.map(function (g, gi) {
                return "req " + (gi + 1) + " → [" + g.instances.join(", ") + "]";
            }).join("   ");

            var $header = $("<div>", { "class": "history-item-header" });

            var $delete = $("<span>", { "class": "history-item-delete" });
            var $deleteBtn = $("<button>", { "class": "delete", text: "X" });
            $deleteBtn.on("click", function (e) {
                e.preventDefault();
                e.stopImmediatePropagation();
                history.splice(i, 1);
                onHistoryChange();
            });
            $delete.append($deleteBtn);

            var $load = $("<span>", { "class": "history-item-load" });
            var $loadBtn = $("<button>", { "class": "load", text: "Load" });
            $loadBtn.on("click", function (e) {
                // Stops the accordion toggling on the button click.
                e.preventDefault();
                e.stopImmediatePropagation();
                loadHistoryItem(i);
            });
            $load.append($loadBtn);

            $header.append($delete).append($load);
            $header.append($("<span>", {
                "class": "history-item-time",
                text: new Date(item.startTime).toLocaleString()
            }));
            $header.append($("<span>", {
                "class": "history-item-duration",
                text: item.durationMS.toFixed(2) + "ms"
            }));
            $header.append($("<span>", {
                "class": "history-item-result" + (summary.failed ? " error" : ""),
                text: summary.text
            }));
            $header.append($("<span>", { "class": "history-item-method", text: item.method }));
            $header.append($("<span>", { "class": "history-item-messages", text: groups }));
            $accordion.append($header);

            var $panel = $("<div>", { "class": "history-item-panel" });
            item.groups.forEach(function (g, gi) {
                $panel.append($("<div>", {
                    "class": "history-detail-heading",
                    text: "Request " + (gi + 1) + "  →  instances [" + g.instances.join(", ") + "]"
                }));
                $panel.append($("<pre>", {
                    "class": "request-json",
                    text: JSON.stringify(requestAsNumbers(g.body.data, item.method), null, 2)
                }));
            });
            if (item.metadata && Object.keys(item.metadata).length > 0) {
                $panel.append($("<div>", { "class": "history-detail-heading", text: "Metadata" }));
                $panel.append($("<pre>", { "class": "request-json", text: JSON.stringify(item.metadata, null, 2) }));
            }
            $panel.append($("<div>", { "class": "history-detail-heading", text: "Responses" }));
            $panel.append(resultGrid(item.data));
            $accordion.append($panel);
        });

        $accordion.accordion({
            animate: 200,
            active: false,
            collapsible: true,
            icons: false,
            header: ".history-item-header",
            heightStyle: "content"
        });
    }

    // Replays a history entry: restores the method, the metadata, the instance
    // selection and one request per recorded group, then writes each body back into
    // its form once the iframe has reloaded.
    function loadHistoryItem(index) {
        var item = history[index];
        if (!item || !item.groups || item.groups.length === 0) {
            return;
        }

        var dot = item.method.lastIndexOf(".");
        var service = item.method.slice(0, dot);
        var method = item.method.slice(dot + 1);
        if (!CONFIG.services[service] || CONFIG.services[service].indexOf(method) === -1) {
            renderError("Cannot load: " + item.method + " is no longer available");
            return;
        }

        $("#grpc-service").val(service);
        populateMethods();
        $("#grpc-method").val(method);
        renderMode();

        $("#cre-metadata input[data-cre-header]").each(function () {
            var values = (item.metadata || {})[$(this).attr("data-cre-header")];
            $(this).val(values ? values.join("\n") : "");
        });
        $("#cre-trigger-id").val(item.triggerId || "");

        var selected = [];
        state.requests = item.groups.map(function (g) {
            g.instances.forEach(function (i) {
                if (selected.indexOf(i) === -1) {
                    selected.push(i);
                }
            });
            return { uid: nextUid++, claims: g.instances.slice() };
        });
        selected.sort(function (a, b) { return a - b; });
        state.selected = selected;
        state.mirror = item.groups.length === 1;
        $("#cre-mirror").prop("checked", state.mirror);

        // Drop every existing card so each request gets a frame on the right
        // method, then render creates them fresh.
        Object.keys(requestEls).forEach(function (uid) {
            requestEls[uid].card.remove();
            delete requestEls[uid];
        });
        render();

        state.requests.forEach(function (request, i) {
            writeWhenReady(request, item.groups[i].body, 60);
        });
        selectTab(0);

        if (item.subscribe) {
            replaySubscription(item);
        }
    }

    // A replayed subscription rejoins the one it recorded if it is still running,
    // and subscribes again if it is not.
    //
    // Rejoining rather than re-registering matters: registering an instance that is
    // already registered under that trigger ID is refused, so replaying a live
    // subscription would be an error rather than the "show me that again" it was
    // meant to be.
    function replaySubscription(item) {
        if (!item.triggerId || !window.__creSubscriptions) {
            return;
        }
        if (window.__creSubscriptions.isLive(item.triggerId)) {
            window.__creSubscriptions.watch(item.triggerId);
            return;
        }
        // Not running any more, so it has to be registered again - once the forms
        // have the bodies that were just written into them.
        setTimeout(invoke, 400);
    }

    // The iframe has to finish loading before it can accept a body.
    function writeWhenReady(request, body, triesLeft) {
        var els = requestEls[request.uid];
        if (!els) {
            return;
        }
        var win = els.frame[0].contentWindow;
        if (win && typeof win.__creWriteRequestBody === "function") {
            win.__creWriteRequestBody(body);
            return;
        }
        if (triesLeft <= 0) {
            renderError("Timed out waiting for a request form to load");
            return;
        }
        setTimeout(function () { writeWhenReady(request, body, triesLeft - 1); }, 200);
    }

    // Saved raw, deliberately.
    //
    // What the page shows is a rendering - special messages replaced by the number
    // they stand for - and a rendering is not something anything else can send. The
    // file holds what went over the wire, so it can be replayed from Postman or
    // anything else that speaks the real messages.
    function saveHistory() {
        var blob = new Blob([JSON.stringify(history, null, 2)], { type: "application/json" });
        var a = document.createElement("a");
        a.href = URL.createObjectURL(blob);
        a.download = "history.json";
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(a.href);
    }

    function selectTab(index) {
        $("#grpc-request-response").tabs("option", "active", index);
    }

    // Grow each iframe to fit its form instead of leaving it scrolling inside a
    // fixed box. Same-origin, so the content height is readable. Polled because the
    // form changes height as rows are added inside it.
    function resizeFrames() {
        Object.keys(requestEls).forEach(function (uid) {
            var frame = requestEls[uid].frame[0];
            var doc = frame.contentDocument;
            if (!doc || !doc.body) {
                return;
            }
            var height = Math.max(doc.body.scrollHeight, doc.documentElement.scrollHeight);
            if (height > 0 && Math.abs(frame.offsetHeight - height) > 4) {
                frame.style.height = height + "px";
            }
        });
    }

    // Bridge for the embedded pages: per-field copy needs to see its siblings.
    // Same-origin, so an iframe can call straight into this.
    window.__creFanout = {
        peerCount: function (win) {
            return state.requests.filter(function (r) {
                var els = requestEls[r.uid];
                return els && els.frame[0].contentWindow !== win;
            }).length;
        },
        peers: function (win) {
            var out = [];
            state.requests.forEach(function (r, i) {
                var els = requestEls[r.uid];
                if (!els || els.frame[0].contentWindow === win) {
                    return;
                }
                try {
                    out.push({ number: i + 1, body: readBody(r, i + 1) });
                } catch (e) {
                    // Still loading; leave it out of the list.
                }
            });
            return out;
        }
    };

    // ---- service / method pickers -------------------------------------------

    function populateMethods() {
        var service = $("#grpc-service").val();
        var $method = $("#grpc-method").empty();
        (CONFIG.services[service] || []).forEach(function (m) {
            $method.append($("<option>", { value: m, text: m }));
        });
    }

    // Changing the method invalidates every form, so reload the iframes.
    function reloadFrames() {
        Object.keys(requestEls).forEach(function (uid) {
            requestEls[uid].frame.attr("src", iframeSrc());
        });
    }

    function init() {
        var $service = $("#grpc-service");
        Object.keys(CONFIG.services).sort().forEach(function (s) {
            $service.append($("<option>", { value: s, text: s }));
        });
        populateMethods();

        $service.on("change", function () {
            populateMethods();
            renderMode();
            reloadFrames();
        });
        $("#grpc-method").on("change", function () {
            renderMode();
            reloadFrames();
        });

        $("#grpc-request-response").tabs();
        $("#cre-mirror").on("change", function () {
            state.mirror = this.checked;
            if (!state.mirror) {
                // Start from "request 1 owns everything" and let instances be
                // carved out of it.
                state.requests[0].claims = state.selected.slice();
            }
            render();
        });
        $("#cre-invoke").on("click", invoke);
        $("#cre-history-clear").on("click", function () {
            if (!confirm("Are you sure you wish to delete all history? This action is permanent and cannot be undone.")) {
                return;
            }
            history = [];
            onHistoryChange();
        });
        $("#cre-history-save").on("click", saveHistory);

        renderMetadata();
        renderMode();
        render();
        loadHistory();
        renderHistory();
        setInterval(resizeFrames, 250);
    }

    init();
});
