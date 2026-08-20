// Per-instance capability form.
//
// grpcui renders the form; this adds what a capability call needs on top of it:
// every optional field marked present, the request metadata a host would have
// carried made editable, and - when embedded in the fan-out page - hooks for the
// parent to read and write a request body.
//
// Everything type-related comes from window.__CRE_DEBUG__, which the server builds
// from the RequestMetadata type and the capability descriptors. Nothing here
// infers a type from the shape of the data.

document.addEventListener("DOMContentLoaded", function () {
    var CONFIG = window.__CRE_DEBUG__ || { metadata: [], headerPrefix: "" };

    // ---- request metadata ----------------------------------------------------
    //
    // The metadata travels as one header per field, which grpcui forwards because
    // the server passed those names to PreserveHeaders. Each field is mirrored
    // into a row of grpcui's own (hidden) metadata table, so it rides along with
    // the request the same way a hand-typed header would.

    function ensureAdvancedSection() {
        if ($("#cre-advanced").length) {
            return;
        }
        var $metadata = $("#grpc-request-metadata");
        var $invoke = $("#grpc-request-tab > button.grpc-invoke").first();
        if (!$metadata.length || !$invoke.length) {
            return;
        }

        // The "Request Metadata" h3 has no id, so tag it for the CSS to hide.
        $metadata.prev("h3").addClass("cre-hidden");

        var $details = $("<details>", { id: "cre-advanced" });
        $details.append($("<summary>", { text: "Advanced" }));
        $details.append($("<div>", {
            "class": "cre-metadata-note",
            text: "Request metadata. Anything left blank is filled in with a valid value by the server."
        }));

        var $grid = $("<div>", { "class": "cre-metadata" });
        CONFIG.metadata.forEach(function (field) {
            var $row = $("<span>", { "class": "cre-metadata-field" });
            $row.append($("<label>", { text: label(field), "for": inputId(field) }));
            $row.append($("<input>", {
                type: inputType(field),
                id: inputId(field),
                placeholder: field.default || "",
                "data-cre-header": field.header,
                "data-cre-repeated": field.repeated ? "1" : ""
            }));
            $grid.append($row);
        });
        $details.append($grid);

        // Bottom of the request tab, just above the Invoke button.
        $invoke.before($details);

        $grid.on("input change", "input", syncMetadataRows);
    }

    function label(field) {
        var text = field.name.replace(/([a-z0-9])([A-Z])/g, "$1 $2");
        if (field.repeated) {
            text += " (one per line, type=limit)";
        }
        return text + ":";
    }

    function inputId(field) {
        return "cre-md-" + field.name;
    }

    // The box a field gets is the one its Go type asks for.
    function inputType(field) {
        switch (field.kind) {
            case "number":
                return "number";
            case "timestamp":
                return "datetime-local";
            default:
                return "text";
        }
    }

    // Mirrors the metadata inputs into grpcui's metadata table. invoke() walks
    // "#grpc-request-metadata-form tr" and reads the first <input> of each row as
    // the name and the second as the value, skipping rows with no inputs - so the
    // leading empty <td> matches grpcui's delete-button column without adding an
    // input of its own.
    //
    // A blank field is left off the wire entirely rather than sent empty: the
    // server fills in a valid value for anything absent, and an empty header would
    // override that with nothing.
    function syncMetadataRows() {
        var $form = $("#grpc-request-metadata-form");
        if (!$form.length) {
            return;
        }

        $form.find("tr.cre-metadata-row").remove();
        var $last = $form.find("tr:last-of-type");

        $("#cre-advanced input[data-cre-header]").each(function () {
            var header = $(this).attr("data-cre-header");
            var repeated = $(this).attr("data-cre-repeated") === "1";
            var raw = $(this).val();
            if (raw === null || String(raw).trim() === "") {
                return;
            }
            // A repeated field is repeated headers, one per line, since headers and
            // gRPC metadata are both name -> list of values.
            var values = repeated ? String(raw).split(/\r?\n/) : [String(raw)];
            values.forEach(function (value) {
                if (value.trim() === "") {
                    return;
                }
                var $row = $("<tr>", { "class": "cre-metadata-row" });
                $row.append($("<td>"));
                $row.append($("<td>").append($("<input>", { "class": "name", value: header })));
                $row.append($("<td>").append($("<input>", { "class": "value", value: value })));
                if ($last.length) {
                    $last.before($row);
                } else {
                    $form.append($row);
                }
            });
        });
    }

    // ---- writing and reading a body ------------------------------------------

    // Writes a whole request body into the form. grpcui's own path for this is the
    // Raw Request textarea: focus registers its validator and blur runs it, which
    // parses the JSON and rebuilds the form. The textarea lives in an inactive tab
    // panel, and jQuery UI hides inactive panels with display:none - which blocks
    // focus - so the tab is activated for the write and put back.
    window.__creWriteRequestBody = function (body) {
        var $tabs = $("#grpc-request-response");
        var raw = document.getElementById("grpc-request-raw-text");
        if (!raw || !body || !body.data) {
            return false;
        }

        var rawIndex = $("#grpc-request-response > div").index(document.getElementById("grpc-request-raw-tab"));
        var previous = $tabs.tabs("option", "active");
        if (rawIndex >= 0) {
            $tabs.tabs("option", "active", rawIndex);
        }

        try {
            // grpcui keeps the message itself in the textarea, not the
            // {metadata, data} envelope: an array only for streaming methods.
            var current = $("#grpc-request-form").data("request");
            var payload = (current instanceof Array) ? body.data : body.data[0];
            raw.focus();
            raw.value = JSON.stringify(payload, null, 2);
            raw.blur();
        } finally {
            $tabs.tabs("option", "active", previous);
        }
        return true;
    };

    // Returns the request body in the shape grpcui's invoke endpoint expects, after
    // committing anything still being edited: blurring the active element runs
    // grpcui's own validator for the field being typed into.
    //
    // The metadata rows are left out. The fan-out page owns the metadata, so that
    // every instance is called with the same one.
    window.__creReadRequestBody = function () {
        if (document.activeElement && typeof document.activeElement.blur === "function") {
            document.activeElement.blur();
        }

        var data = $("#grpc-request-form").data("request");
        if (!(data instanceof Array)) {
            data = [data];
        }
        return { metadata: [], data: data };
    };

    // ---- per-field copy ------------------------------------------------------
    //
    // Each field row gets a picker offering the other requests on the fan-out page.
    // Copying is a JSON splice followed by a full rewrite through
    // __creWriteRequestBody, which is why every field shape works the same way:
    // scalars, nested messages, repeated fields and maps all go back through
    // grpcui's own form rebuild rather than needing their own editor logic.
    //
    // Paths are built by walking the rendered form top-down, so a row always knows
    // its own position without reverse-engineering it from the DOM.

    function camelCase(name) {
        return name.replace(/_([a-z])/g, function (all, c) { return c.toUpperCase(); });
    }

    // The form is built from proto names but the model reads back lowerCamelCase,
    // so a lookup has to accept either spelling.
    function readKey(node, segment) {
        if (node === null || node === undefined || typeof node !== "object") {
            return undefined;
        }
        if (typeof segment === "number") {
            return node instanceof Array ? node[segment] : undefined;
        }
        if (Object.prototype.hasOwnProperty.call(node, segment)) {
            return node[segment];
        }
        var camel = camelCase(segment);
        return Object.prototype.hasOwnProperty.call(node, camel) ? node[camel] : undefined;
    }

    function writeKey(node, segment, value) {
        if (typeof segment === "number") {
            node[segment] = value;
            return;
        }
        if (Object.prototype.hasOwnProperty.call(node, segment)) {
            node[segment] = value;
            return;
        }
        var camel = camelCase(segment);
        node[Object.prototype.hasOwnProperty.call(node, camel) ? camel : segment] = value;
    }

    function valueAtPath(root, path) {
        var current = root;
        for (var i = 0; i < path.length; i++) {
            current = readKey(current, path[i]);
            if (current === undefined) {
                return undefined;
            }
        }
        return current;
    }

    function setAtPath(root, path, value) {
        var current = root;
        for (var i = 0; i < path.length - 1; i++) {
            var next = readKey(current, path[i]);
            if (next === null || next === undefined || typeof next !== "object") {
                next = typeof path[i + 1] === "number" ? [] : {};
                writeKey(current, path[i], next);
            }
            current = next;
        }
        writeKey(current, path[path.length - 1], value);
    }

    function parentAPI() {
        try {
            if (window.parent === window) {
                return null;
            }
            var api = window.parent.__creFanout;
            return api && typeof api.peers === "function" ? api : null;
        } catch (e) {
            // Cross-origin parent: nothing to copy from.
            return null;
        }
    }

    function peerCount() {
        var api = parentAPI();
        if (!api || typeof api.peerCount !== "function") {
            return 0;
        }
        try {
            return api.peerCount(window);
        } catch (e) {
            return 0;
        }
    }

    function copyFieldFrom(path, peer) {
        var body = window.__creReadRequestBody();
        var mine = body.data[0];
        var value = valueAtPath(peer.body.data[0], path);
        if (value === undefined) {
            return;
        }
        // Deep copy, so editing afterwards cannot reach back into the peer.
        setAtPath(mine, path, JSON.parse(JSON.stringify(value)));
        window.__creWriteRequestBody({ metadata: [], data: [mine] });
    }

    // Options are filled in on open: a nested field is only offered by a request
    // that actually has it set.
    function fillCopyOptions($select, path) {
        var api = parentAPI();
        $select.find("option:not(:first-child)").remove();
        if (!api) {
            return;
        }
        var peers;
        try {
            peers = api.peers(window) || [];
        } catch (e) {
            return;
        }
        peers.forEach(function (peer) {
            if (!peer.body || !peer.body.data || peer.body.data.length === 0) {
                return;
            }
            if (valueAtPath(peer.body.data[0], path) === undefined) {
                return;
            }
            $select.append($("<option>", { value: String(peer.number), text: "Request " + peer.number }));
            $select.data("peer-" + peer.number, peer);
        });
    }

    function attachFieldCopy(row, path) {
        var $row = $(row);
        if ($row.data("creFieldCopy")) {
            return;
        }
        $row.data("creFieldCopy", true);

        var $cell = $row.children("td.name").first();
        if (!$cell.length) {
            return;
        }

        var $select = $("<select>", { "class": "cre-copy-field" });
        $select.attr("title", "Copy this field from another request");
        $select.append($("<option>", { value: "", text: "copy" }));
        // Populated as the dropdown opens: mousedown and focus both land before
        // the list renders, and a peer's values change as it is edited.
        $select.on("mousedown focus", function () {
            fillCopyOptions($select, path);
        });
        $select.on("change", function () {
            var chosen = $select.val();
            $select.val("");
            if (!chosen) {
                return;
            }
            var peer = $select.data("peer-" + chosen);
            if (peer) {
                copyFieldFrom(path, peer);
            }
        });

        $cell.append($("<div>", { "class": "cre-copy-wrap" }).append($select));
    }

    // Walks a message table, attaching a picker per field and recursing into
    // nested messages and repeated-message elements.
    function walkFormTable(table, path) {
        Array.prototype.forEach.call(table.rows, function (row) {
            if (!row.classList.contains("message_field")) {
                return;
            }
            var label = row.querySelector("td.name strong");
            if (!label) {
                return;
            }
            var fieldPath = path.concat([label.textContent.trim()]);
            attachFieldCopy(row, fieldPath);

            // The row's own container, if it has one. querySelector returns the
            // nearest in document order, and a deeper table belongs to an inner
            // row rather than this one.
            var nested = row.querySelector("td div.input_container > table");
            if (!nested || nested.closest("tr") !== row) {
                return;
            }

            var elements = Array.prototype.filter.call(nested.rows, function (r) {
                return r.classList.contains("array_element");
            });
            if (elements.length === 0) {
                walkFormTable(nested, fieldPath);
                return;
            }
            elements.forEach(function (element, index) {
                var elementTable = element.querySelector("div.input_container > table");
                if (elementTable) {
                    walkFormTable(elementTable, fieldPath.concat([index]));
                }
            });
        });
    }

    function decorateFieldCopies() {
        // Only useful when there is a sibling request to copy from.
        if (peerCount() === 0) {
            $(".cre-copy-wrap").remove();
            $("#grpc-request-form tr.message_field").removeData("creFieldCopy");
            return;
        }
        var root = document.querySelector("#grpc-request-form #root > table");
        if (root) {
            walkFormTable(root, []);
        }
    }

    // ---- embedding -----------------------------------------------------------
    //
    // The fan-out page embeds this page once per request. In embed mode the
    // instance's own chrome - tab strip, Invoke button, method pickers, metadata -
    // is hidden, because the parent owns all of it.
    if (/(?:^|[?&])embed=1(?:&|$)/.test(window.location.search)) {
        document.body.classList.add("cre-embedded");
    }

    setInterval(function () {
        if (!window.$) {
            return;
        }

        // Mark every optional field present, and trigger jQuery's 'change' so
        // grpcui's internal state records the value. The cells themselves are
        // hidden by page.css.
        $("#grpc-request-form td.toggle_presence input[type='checkbox']:not(:checked)").each(function () {
            $(this).prop("checked", true).trigger("change");
        });

        ensureAdvancedSection();
        syncMetadataRows();
        decorateFieldCopies();
    }, 100);
});
