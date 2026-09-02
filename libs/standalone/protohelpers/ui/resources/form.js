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
    var CONFIG = window.__CRE_DEBUG__ || {
        metadata: [], headerPrefix: "", subscriptions: [], triggerIdHeader: "", prefix: "",
        special: { bigInt: "", decimal: "", methods: {} }
    };

    // A method on one of these services registers a trigger rather than calling
    // something, so it takes a trigger ID and its events turn up on the fan-out
    // page rather than in the Response tab.
    function isSubscribing() {
        return (CONFIG.subscriptions || []).indexOf($("#grpc-service").val()) !== -1;
    }

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

        // The trigger ID, for a method that registers one. Filled in the same way
        // as the metadata - blank means the server mints one - and carried the same
        // way, since syncMetadataRows mirrors anything with a header attribute.
        //
        // Worth naming even though the events are shown elsewhere: the fan-out page
        // reattaches to whatever is running when it loads, so a subscription started
        // here under a name you chose is one you can go and watch there.
        //
        // Only useful for a subscription, so it is only shown for one: see
        // renderSubscribeFields.
        var $trigger = $("<div>", { id: "cre-trigger-field", "class": "cre-metadata-field" });
        $trigger.append($("<label>", { text: "Trigger ID:", "for": "cre-trigger-id" }));
        $trigger.append($("<input>", {
            type: "text",
            id: "cre-trigger-id",
            "class": "cre-trigger-id",
            placeholder: "one is minted if left blank; reuse one to join a running subscription",
            "data-cre-header": CONFIG.triggerIdHeader
        }));
        $trigger.append($("<div>", { "class": "cre-metadata-note" })
            .append(document.createTextNode("A trigger delivers for as long as it is registered, so its events are shown on the "))
            .append($("<a>", { href: (CONFIG.prefix || "") + "/request", target: "_blank", text: "fan-out page" }))
            .append(document.createTextNode(" rather than here.")));
        $details.append($trigger);

        // Bottom of the request tab, just above the Invoke button.
        $invoke.before($details);

        $details.on("input change", "input", syncMetadataRows);
    }

    // renderSubscribeFields shows the trigger ID only where it means something.
    //
    // Polled with everything else rather than bound to the picker: grpcui rebuilds
    // the form on a method change, and the loop below is already what keeps up with
    // that.
    function renderSubscribeFields() {
        var $field = $("#cre-trigger-field");
        if (!$field.length) {
            return;
        }
        var subscribing = isSubscribing();
        $field.toggle(subscribing);
        if (!subscribing) {
            // Or a trigger ID typed for one method would be sent with the next,
            // which ignores it - silently.
            $("#cre-trigger-id").val("");
        }
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

    // What is wrong with this form, one entry per field that cannot be sent, or an
    // empty array when it is ready to go.
    //
    // Separate from __creReadRequestBody because that is also how a field is copied
    // between requests, and a value that cannot be sent is still one worth copying.
    // The fan-out page asks this before sending and refuses on anything it returns,
    // which is the same refusal the Invoke button on this page makes for itself.
    window.__creRequestProblems = function () {
        if (document.activeElement && typeof document.activeElement.blur === "function") {
            document.activeElement.blur();
        }
        return flushBytesWidgets();
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
        // The fan-out page's Invoke button is on the parent page, so the flush that
        // rides on a click here never runs for it.
        flushBytesWidgets();

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


    // ---- special value types: BigInt and Decimal -----------------------------
    //
    // A values.v1.BigInt is a sign and a big-endian byte string, so filling one in
    // by hand means working out that -1000 is sign -1 and the base64 of 0x03e8.
    // A values.v1.Decimal is that plus an exponent. Neither is a number anybody
    // wants to type, so the page offers a box holding the number and does the
    // arithmetic.
    //
    // Which messages these are, and where a response holds them, comes from
    // CONFIG.special - built from the descriptors, see special.go. Nothing here
    // recognises one from the shape of the data.
    //
    // Request side: grpcui renders a nested message as its own <table>. That table
    // is left in the DOM, visually hidden, and its inputs are driven, because the
    // request is built from grpcui's own model rather than from the DOM - writing
    // the model directly would desync the Raw Request tab, the grpcurl text and
    // grpcui's history.
    //
    // Response side: grpcui dumps each message as JSON into a textarea. The
    // configured paths are followed into it and the numbers shown beside it, with
    // the raw JSON left in place so the exact server output is still there.

    var SPECIAL = CONFIG.special || { bigInt: "", decimal: "", methods: {} };

    // The arithmetic lives in values.js, shared with the fan-out page: the
    // encoding has to match Go's big.Int and decimal.Decimal exactly, and two
    // copies of that would be two things to keep right.
    var VALUES = window.__CRE_VALUES__;

    // The proto field names these messages are made of. Read from the descriptors
    // would be better, but they are the message's own contract - a BigInt without
    // an abs_val is not a BigInt - so a mismatch is a missing widget, not silently
    // wrong arithmetic: subfieldEditors below returns nothing and the raw fields
    // stay visible.
    var ABS_VAL = VALUES.fields.absVal;
    var SIGN = VALUES.fields.sign;
    var COEFFICIENT = VALUES.fields.coefficient;
    var EXPONENT = VALUES.fields.exponent;

    // ---- encoding ------------------------------------------------------------

    // ---- request side --------------------------------------------------------

    // Maps a nested message table to { protoFieldName: editorElement }.
    //
    // Only this table's own rows, and only rows whose editor is their own: a row
    // holding a nested message has no editor of its own, and querySelector would
    // otherwise reach into the nested table and report its first field as this
    // row's value.
    function subfieldEditors(table) {
        var editors = {};
        Array.prototype.forEach.call(table.rows, function (tr) {
            var label = tr.querySelector("td.name strong");
            var editor = tr.querySelector("textarea, input[type='text']");
            if (label && editor && editor.closest("tr") === tr) {
                editors[label.textContent.trim()] = editor;
            }
        });
        return editors;
    }

    // The table of a nested message field, by field name.
    function nestedTable(table, field) {
        var found = null;
        Array.prototype.forEach.call(table.rows, function (tr) {
            var label = tr.querySelector("td.name strong");
            if (!label || label.textContent.trim() !== field) {
                return;
            }
            var inner = tr.querySelector("td div.input_container > table");
            if (inner && inner.closest("tr") === tr) {
                found = inner;
            }
        });
        return found;
    }

    // Which special message a table is, or null. The type comes from the label
    // cell of the row enclosing it, where grpcui writes it straight from the
    // descriptor - so this reads the declared type rather than guessing from
    // which fields happen to be present.
    function specialKind(table) {
        var row = $(table).closest("tr.message_field")[0];
        if (!row) {
            return null;
        }
        var cell = row.querySelector("td.name");
        if (!cell) {
            return null;
        }

        // Only the cell's own text: the field name is in a <strong> and "repeated"
        // in an <em>, and a copy picker adds a <select> of its own.
        var declared = "";
        Array.prototype.forEach.call(cell.childNodes, function (node) {
            if (node.nodeType === 3) {
                declared += node.nodeValue;
            }
        });
        declared = declared.trim();

        if (SPECIAL.decimal && declared === SPECIAL.decimal) {
            return "decimal";
        }
        if (SPECIAL.bigInt && declared === SPECIAL.bigInt) {
            return "bigInt";
        }
        return null;
    }

    // The editors one widget drives, or null if the table is not fully built yet.
    function specialEditors(table, kind) {
        if (kind === "bigInt") {
            var own = subfieldEditors(table);
            if (!own[ABS_VAL] || !own[SIGN]) {
                return null;
            }
            return { absVal: own[ABS_VAL], sign: own[SIGN] };
        }

        var outer = subfieldEditors(table);
        var inner = nestedTable(table, COEFFICIENT);
        if (!outer[EXPONENT] || !inner) {
            return null;
        }
        var coefficient = subfieldEditors(inner);
        if (!coefficient[ABS_VAL] || !coefficient[SIGN]) {
            return null;
        }
        return { absVal: coefficient[ABS_VAL], sign: coefficient[SIGN], exponent: outer[EXPONENT] };
    }

    // Pushes a value into one of grpcui's own inputs. grpcui registers its
    // validator on focus and commits the value on blur, so both are required - and
    // they must be the native methods: jQuery's .trigger('blur') runs handlers
    // before the native blur, and grpcui's own handler ignores the event while
    // document.activeElement is still the element. Focus is restored afterwards so
    // committing does not yank the caret out of the widget.
    function commitToGrpcui(editor, value) {
        if (!editor || editor.value === value) {
            return;
        }
        var previous = document.activeElement;
        editor.focus();
        editor.value = value;
        editor.blur();
        if (previous && previous !== editor && typeof previous.focus === "function") {
            previous.focus();
        }
    }

    // The number currently held in the raw fields, for the box to open with.
    function readSpecial(editors, kind) {
        if (kind === "bigInt") {
            return VALUES.decodeBigInt(editors.absVal.value, editors.sign.value);
        }
        return VALUES.decodeDecimal(editors.absVal.value, editors.sign.value, editors.exponent.value);
    }

    // writeSpecial pushes what was typed back into the raw fields.
    function writeSpecial(table, kind, text, $box) {
        var editors = specialEditors(table, kind);
        if (!editors) {
            return;
        }

        var encoded = kind === "bigInt" ? VALUES.encodeBigInt(text) : VALUES.encodeDecimal(text);
        if (encoded === undefined) {
            // Not a number. Said so, and the raw fields are left as they were.
            $box.addClass("cre-special-invalid");
            return;
        }
        $box.removeClass("cre-special-invalid");

        if (encoded === null) {
            // Cleared, which is the message left at its zero value.
            commitToGrpcui(editors.absVal, "");
            commitToGrpcui(editors.sign, "0");
            if (kind === "decimal") {
                commitToGrpcui(editors.exponent, "0");
            }
            return;
        }

        if (kind === "bigInt") {
            commitToGrpcui(editors.absVal, encoded.absVal);
            commitToGrpcui(editors.sign, encoded.sign);
            return;
        }
        commitToGrpcui(editors.absVal, encoded.coefficient.absVal);
        commitToGrpcui(editors.sign, encoded.coefficient.sign);
        commitToGrpcui(editors.exponent, encoded.exponent);
    }

    function decorateSpecialTable(table, kind) {
        var $table = $(table);
        if ($table.data("creSpecial")) {
            return;
        }

        var editors = specialEditors(table, kind);
        if (!editors) {
            // Half-built; the poll below comes back to it.
            return;
        }
        $table.data("creSpecial", true);

        var $box = $("<input>", {
            type: "text",
            "class": "cre-special-input",
            inputmode: kind === "bigInt" ? "numeric" : "decimal",
            placeholder: kind === "bigInt" ? "whole number, any size" : "number, e.g. 123.45",
            title: kind === "bigInt" ? SPECIAL.bigInt : SPECIAL.decimal,
            value: readSpecial(editors, kind)
        });

        // Only digits go in, so the box cannot hold something that is not a number
        // in the first place.
        var allowed = kind === "bigInt" ? /[^0-9+-]/g : /[^0-9+.\-]/g;
        $box.on("input", function () {
            var cleaned = this.value.replace(allowed, "");
            if (cleaned !== this.value) {
                var at = this.selectionStart - (this.value.length - cleaned.length);
                this.value = cleaned;
                if (typeof this.setSelectionRange === "function") {
                    this.setSelectionRange(at, at);
                }
            }
        });

        // Committed on change, which fires when the box loses focus, so the caret
        // is never moved mid-number.
        $box.on("change", function () {
            writeSpecial(table, kind, this.value, $box);
        });

        var $widget = $("<span>", { "class": "cre-special" }).append($box);

        // Visually hidden rather than display:none - the raw fields have to stay
        // focusable, because focusing them is how a value is committed.
        $table.addClass("cre-visually-hidden").after($widget);
        $widget.data("creSpecialTable", table).data("creSpecialKind", kind);
    }

    function decorateSpecialTables() {
        // Document order, so an outer table is decorated before anything inside it.
        // That matters for a Decimal: its coefficient is a BigInt, so it would get
        // a box of its own - hidden inside the Decimal's hidden table, and writing
        // to the very fields the Decimal's box drives. Whichever flushed last would
        // win, so the inner one is not offered at all.
        $("#grpc-request-form div.input_container > table").each(function () {
            var kind = specialKind(this);
            if (!kind || $(this).parents("table.cre-visually-hidden").length) {
                return;
            }
            decorateSpecialTable(this, kind);
        });
    }

    // Safety net: flush every widget before an RPC goes out, in case a box is still
    // focused and has therefore not fired its change event yet.
    function flushSpecialWidgets() {
        $(".cre-special").each(function () {
            var $widget = $(this);
            var table = $widget.data("creSpecialTable");
            if (!table || !document.body.contains(table)) {
                return;
            }
            var $box = $widget.find(".cre-special-input");
            writeSpecial(table, $widget.data("creSpecialKind"), $box.val(), $box);
        });
    }

    // Capture phase, so this runs before grpcui's own click handler.
    document.addEventListener("mousedown", function (e) {
        if (e.target && $(e.target).is(".grpc-invoke")) {
            flushSpecialWidgets();
            flushBytesWidgets();
        }
    }, true);

    // The send itself is stopped on click rather than on mousedown, which is what
    // grpcui listens for: preventing a mousedown does not prevent the click that
    // follows it. Capture phase and stopImmediatePropagation, so grpcui's own
    // handler never runs.
    document.addEventListener("click", function (e) {
        if (!e.target || !$(e.target).is(".grpc-invoke")) {
            return;
        }
        if (flushBytesWidgets().length === 0) {
            return;
        }
        e.preventDefault();
        e.stopImmediatePropagation();
        bytesContainers().each(function () {
            var $box = typedBytesBox($(this));
            if ($box.hasClass("cre-special-invalid")) {
                $box.focus();
                return false;
            }
        });
    }, true);

    // ---- bytes fields --------------------------------------------------------
    //
    // grpcui takes a bytes field as base64, which is not how any of these values
    // are written anywhere else: an address, a hash, a workflow ID or a signature is
    // hex, in the logs, in the contracts and in everything you would paste from.
    //
    // One dropdown over the form picks which encoding every bytes field in it is
    // typed in, the same way the one over the response picks how they are read. A
    // dropdown per field meant setting the same thing several times for one request,
    // and left a form where two fields of the same type disagreed about what the
    // digits in them meant.
    //
    // base64 is still what is sent, and grpcui's own box is still what holds it -
    // the hex box is a second view onto it - so the file picker and grpcui's own
    // validation are left exactly as they are.

    // The encoding is the page's, not this form's - see VALUES.bytesEncoding. It is
    // also what fields built later open in: a method change, an added repeated entry
    // or a copy from another request all build boxes after the choice was made.

    function rawBytesBox($container) {
        return $container.children("textarea").not(".cre-bytes-hex").first();
    }

    function hexBytesBox($container) {
        return $container.children("textarea.cre-bytes-hex").first();
    }

    // The box a field's value is typed into, which is the one a complaint about that
    // value belongs on: the hex box while the dropdown says hex, grpcui's own
    // otherwise.
    function typedBytesBox($container) {
        return $container.data("creBytesEncoding") === "hex"
            ? hexBytesBox($container)
            : rawBytesBox($container);
    }

    // bytesProblem is what is wrong with a field, or null if nothing is.
    //
    // Only the box being typed into is judged. In hex the other one holds what this
    // widget put there, and in base64 the hex box is a stale view that is about to
    // be refilled - complaining about either would be complaining about a value
    // nobody typed.
    function bytesProblem($container) {
        var $box = typedBytesBox($container);
        if (!$box.length || $box.prop("disabled")) {
            return null;
        }
        var text = VALUES.cleanBytes($box.val());
        if (text === "") {
            // The field left at its zero value, which is a value: an empty bytes
            // field is how a call with no data or no recipient is written.
            return null;
        }
        if ($container.data("creBytesEncoding") === "hex") {
            return VALUES.isHex(text) ? null : "not hex";
        }
        if (/^0[xX]/.test(text)) {
            // Caught by name rather than by decoding, because decoding cannot catch
            // it: "0x" and hex digits are all base64 characters, so a hex value
            // pasted into the base64 box decodes - to other bytes entirely, which
            // then go out as the value. The dropdown above the form is what this is
            // asking to be switched.
            return "hex in a base64 box - switch the dropdown to hex";
        }
        return VALUES.isBase64(text) ? null : "not base64";
    }

    // markBytesProblem puts the mark on the box, or takes it off, and says what the
    // problem was. The mark is what makes a value that cannot be sent visible
    // before the send rather than after it.
    function markBytesProblem($container) {
        var problem = bytesProblem($container);
        var $box = typedBytesBox($container);
        hexBytesBox($container).removeClass("cre-special-invalid");
        rawBytesBox($container).removeClass("cre-special-invalid");
        if (problem) {
            $box.addClass("cre-special-invalid");
        }
        return problem;
    }

    // cleanHexBox and cleanRawBox take the whitespace out of what was typed. Done on
    // the way out of the box rather than on every keystroke, so a value being pasted
    // in pieces is not fought with as it is assembled.
    function cleanHexBox($hex) {
        var cleaned = VALUES.cleanBytes($hex.val());
        if (cleaned !== $hex.val()) {
            $hex.val(cleaned);
        }
        return cleaned;
    }

    function cleanRawBox($raw) {
        var cleaned = VALUES.cleanBytes($raw.val());
        if (cleaned !== $raw.val()) {
            // grpcui's own box, so it goes through the same commit as any other
            // write: setting .value alone leaves the model holding the old text.
            commitToGrpcui($raw[0], cleaned);
        }
        return cleaned;
    }

    // syncHexFromRaw refills the hex box from the value grpcui holds.
    function syncHexFromRaw($container) {
        var $hex = hexBytesBox($container);
        var hex = VALUES.hexOfBase64(rawBytesBox($container).val());
        if (hex === null) {
            // Not base64, so there is no hex to show it as. What is in grpcui's box
            // is shown as typed instead, which repairs the common way of getting
            // here - hex pasted into the base64 box - since that text is hex and
            // committing it back is all it needed. Anything else is marked and
            // blocks the send.
            $hex.val(VALUES.cleanBytes(rawBytesBox($container).val()));
            markBytesProblem($container);
            return;
        }
        $hex.val(hex);
        markBytesProblem($container);
    }

    // writeBytesHex pushes what was typed as hex back into grpcui's box.
    //
    // live is a keystroke rather than a finished value: it commits what parses and
    // says nothing about what does not. Half a value is half typed, not wrong, and
    // tidying the box while it is being typed into moves the caret out from under
    // the typing. Both are done on the way out instead.
    function writeBytesHex($container, live) {
        var $hex = hexBytesBox($container);
        if (!$hex.length) {
            return;
        }
        var text = live ? $hex.val() : cleanHexBox($hex);
        var encoded = VALUES.base64OfHex(text);
        if (encoded === undefined) {
            // Not hex. Marked, and grpcui's box keeps the value it had - which is
            // why the send is blocked rather than allowed to carry the old value.
            if (!live) {
                markBytesProblem($container);
            }
            return;
        }
        if (!live) {
            markBytesProblem($container);
        }
        commitToGrpcui(rawBytesBox($container)[0], encoded);
    }

    function applyBytesEncoding($container, encoding) {
        var previous = $container.data("creBytesEncoding");
        var $raw = rawBytesBox($container);
        var $hex = hexBytesBox($container);

        // Committed on the way out of hex, so switching back shows what was just
        // typed rather than dropping it.
        //
        // Only on the way out. A field being decorated has no previous encoding and
        // an empty hex box, and an empty hex box is a value: committing it would
        // write nothing over whatever grpcui's box already held. Forms arrive
        // already filled in - copied from another request, replayed from history,
        // edited as raw JSON, mirrored across the fan-out - and that is a bytes
        // field silently emptied between being written and being sent.
        if (previous === "hex" && encoding !== "hex") {
            writeBytesHex($container);
        }

        $container.data("creBytesEncoding", encoding);
        if (encoding === "hex") {
            syncHexFromRaw($container);
            $raw.addClass("cre-visually-hidden");
            $hex.show();
            return;
        }
        $hex.hide();
        $raw.removeClass("cre-visually-hidden");
        markBytesProblem($container);
    }

    function decorateBytesField(container) {
        var $container = $(container);
        if ($container.data("creBytes")) {
            return;
        }
        var $raw = rawBytesBox($container);
        if (!$raw.length) {
            return;
        }
        $container.data("creBytes", true);

        var $hex = $("<textarea>", {
            "class": "cre-bytes-hex",
            cols: 40,
            rows: 1,
            spellcheck: false,
            placeholder: "hex, e.g. 0x1a2b3c"
        });
        $raw.after($hex);
        $hex.hide();

        // Committed as it is typed, not on the way out of the box.
        //
        // On the way out meant on this box's change event, and on the flush that
        // rides on Invoke. Both are one ordering away from a value that was typed
        // but never sent: the Invoke that matters is on the fan-out page, in another
        // document, and what fires where when focus crosses that boundary is not
        // something this widget should be betting a request on. Typing straight into
        // hex has to work the first time, without a trip through base64 first.
        //
        // commitToGrpcui moves focus to grpcui's box and back, so the caret goes
        // with it; it is put back where it was.
        $hex.on("input", function () {
            var box = this;
            var start = box.selectionStart;
            var end = box.selectionEnd;
            writeBytesHex($container, true);
            if (document.activeElement === box && box.setSelectionRange) {
                box.setSelectionRange(start, end);
            }
        });

        // Still taken on the way out, for anything that changes the box without
        // typing in it - a paste by mouse, an autofill, a drag.
        $hex.on("change", function () {
            writeBytesHex($container);
        });

        // grpcui's own box gets the same treatment while it is the one being typed
        // into: trimmed and judged on the way out, so base64 that cannot be sent is
        // marked where it was typed instead of at the far end of an RPC.
        $raw.on("change", function () {
            if ($container.data("creBytesEncoding") === "hex") {
                // Written by this widget rather than typed, and already clean.
                return;
            }
            cleanRawBox($raw);
            markBytesProblem($container);
        });

        applyBytesEncoding($container, VALUES.bytesEncoding());
    }

    // refreshBytesField follows a value that changed behind the hex box: the file
    // picker writes base64 straight into grpcui's box, and so does a copy from
    // another request.
    function refreshBytesField(container) {
        var $container = $(container);
        if ($container.data("creBytesEncoding") !== "hex") {
            // Base64: nothing to follow, but the mark is still kept current, since
            // the file picker and a copy from another request both write straight
            // into grpcui's box without a change event.
            if (rawBytesBox($container)[0] !== document.activeElement) {
                markBytesProblem($container);
            }
            return;
        }
        var $hex = hexBytesBox($container);
        if (!$hex.length || $hex[0] === document.activeElement) {
            return;
        }
        // Being edited into something that is not hex yet, or typed wrong and left
        // that way. Either is the user's text, not something to overwrite.
        if ($hex.hasClass("cre-special-invalid")) {
            return;
        }
        var $raw = rawBytesBox($container);
        if ($hex.prop("disabled") !== $raw.prop("disabled")) {
            $hex.prop("disabled", $raw.prop("disabled"));
        }
        // Compared as the bytes each side means, so a value the user wrote 0x- or
        // space-separated - or one grpcui holds in the web-safe alphabet - is not
        // rewritten out from under them on every poll.
        var typed = String($hex.val()).replace(/\s+/g, "").replace(/^0[xX]/, "").toLowerCase();
        if (typed === VALUES.hexOfBase64($raw.val())) {
            return;
        }
        syncHexFromRaw($container);
    }

    // The bytes fields the dropdown drives.
    //
    // A BigInt's magnitude is a bytes field too, but it is driven by that widget and
    // hidden behind it, so it is left out - two things writing the same field is one
    // too many.
    function bytesContainers() {
        return $("#grpc-request-form div.grpc-bytes-container").filter(function () {
            return $(this).parents("table").filter(function () {
                return $(this).hasClass("cre-visually-hidden") || specialKind(this);
            }).length === 0;
        });
    }

    // The one dropdown, above the form, shown only while the method being called has
    // a bytes field to type into.
    function ensureBytesControl(present) {
        var $control = $("#cre-bytes-control");
        if (!present || insideFanout()) {
            $control.remove();
            return;
        }
        var $form = $("#grpc-request-form");
        if ($control.length || !$form.length) {
            return;
        }

        var $select = $("<select>", {
            "class": "cre-bytes-encoding",
            title: "How bytes fields are typed and shown, here and in the response. They are sent as bytes either way."
        });
        $select.append($("<option>", { value: "base64", text: "base64" }));
        $select.append($("<option>", { value: "hex", text: "hex" }));
        $select.val(VALUES.bytesEncoding());
        $select.on("change", function () {
            VALUES.setBytesEncoding(this.value);
        });

        $form.before($("<div>", { id: "cre-bytes-control", "class": "cre-encoding-controls" })
            .append($("<span>", { "class": "cre-encoding-label", text: "Bytes:" }))
            .append($select));
    }

    // insideFanout is whether this form is one of the fan-out page's frames. That
    // page shows the one dropdown above its requests, so a form inside it shows
    // none: one setting, one place to set it.
    function insideFanout() {
        try {
            return window.top !== window;
        } catch (e) {
            return false;
        }
    }

    // Every view of a bytes field, redrawn in the encoding just picked. Registered
    // once; the setting itself lives on the top window.
    VALUES.onBytesEncoding(function (encoding) {
        bytesContainers().each(function () {
            applyBytesEncoding($(this), encoding);
        });
        $("#cre-bytes-control").find("select.cre-bytes-encoding").val(encoding);
        // The response is shown in the same encoding, so it is redrawn too. The
        // stamp is what would otherwise make this a no-op.
        $("#grpc-response-data textarea.grpc-response-textarea").removeData("creSpecialFor");
        decorateResponseSpecials();
    });

    function decorateBytesFields() {
        var $containers = bytesContainers();
        $containers.each(function () {
            decorateBytesField(this);
            refreshBytesField(this);
        });
        ensureBytesControl($containers.length > 0);
    }

    // Safety net, the same one the special widgets get: flush every bytes box before
    // an RPC goes out, in case one is still focused and has therefore not fired its
    // change event yet.
    //
    // Returns what is wrong with the form, one entry per field that cannot be sent.
    // A hex box that will not parse leaves grpcui's box holding whatever it held
    // before, so a send that went ahead anyway would quietly carry the previous
    // value - or none, for a field that never had one. That is what this is for.
    function flushBytesWidgets() {
        $("#grpc-request-form div.grpc-bytes-container").each(function () {
            if ($(this).data("creBytesEncoding") === "hex") {
                writeBytesHex($(this));
            }
        });

        var problems = [];
        bytesContainers().each(function () {
            var problem = markBytesProblem($(this));
            if (problem) {
                problems.push(bytesFieldName(this) + ": " + problem);
            }
        });
        showBytesProblems(problems);
        return problems;
    }

    // The field a container belongs to, as the form labels it, so a complaint names
    // the box it is about rather than making the reader hunt for the red one.
    function bytesFieldName(container) {
        // The same label the form is built from - see subfieldEditors - so a
        // complaint names the field the way the row above the box does.
        var name = $(container).closest("tr").find("td.name strong").first().text().trim();
        return name || "bytes field";
    }

    // The complaint, shown beside the dropdown that chose the encoding it is about.
    function showBytesProblems(problems) {
        var $control = $("#cre-bytes-control");
        if (!$control.length) {
            return;
        }
        var $note = $control.find(".cre-bytes-problem");
        if (!problems.length) {
            $note.remove();
            return;
        }
        if (!$note.length) {
            $note = $("<span>", { "class": "cre-bytes-problem" });
            $control.append($note);
        }
        $note.text(problems.length === 1
            ? "Not sent - " + problems[0]
            : "Not sent - " + problems.length + " fields: " + problems.join(", "));
    }

    // ---- response side -------------------------------------------------------

    function currentMethod() {
        var service = $("#grpc-service").val();
        var method = $("#grpc-method").val();
        if (!service || !method) {
            return null;
        }
        return "/" + service + "/" + method;
    }

    // The bytes paths for a method's response, or none if it has no bytes fields -
    // in which case there is nothing for the dropdown to do and it is not offered.
    function responseBytesPaths(method) {
        return (SPECIAL.bytesMethods || {})[method] || [];
    }

    // shownResponse is the response as it is displayed: special messages as the
    // numbers they stand for, and - when the one dropdown says so - bytes as hex.
    // The same setting the boxes above are typed in, so a value goes out and comes
    // back written the same way.
    //
    // The textarea above it keeps the raw response either way. That is the one
    // worth copying: it is what the server actually sent, and what a replay
    // somewhere else has to send back.
    function shownResponse(response, method) {
        var shown = VALUES.parsed(response, SPECIAL.methods[method] || [], SPECIAL);
        if (VALUES.bytesEncoding() === "hex") {
            shown = VALUES.hexed(shown, responseBytesPaths(method));
        }
        return shown;
    }

    function decorateResponseSpecials() {
        var method = currentMethod();
        var paths = (method && SPECIAL.methods[method]) || [];
        var bytesPaths = method ? responseBytesPaths(method) : [];

        $("#grpc-response-data textarea.grpc-response-textarea").each(function () {
            var $textArea = $(this);
            var raw = $textArea.val();
            // The encoding is part of the stamp, so picking one re-renders rather
            // than being taken as "already shown".
            var stamp = method + " " + VALUES.bytesEncoding() + " " + raw;
            if ($textArea.data("creSpecialFor") === stamp) {
                return;
            }
            $textArea.data("creSpecialFor", stamp);

            var $container = $textArea.parent();
            $container.find(".cre-response-view").remove();
            if (!paths.length && !bytesPaths.length) {
                return;
            }

            var response;
            try {
                response = JSON.parse(raw);
            } catch (e) {
                return;
            }

            var $view = $("<div>", { "class": "cre-response-view" });

            // No dropdown here. The one above the request picks the encoding for
            // both directions, so this only reads it.
            var shown = shownResponse(response, method);
            if (JSON.stringify(shown) !== JSON.stringify(response)) {
                $view.append($("<div>", {
                    "class": "cre-special-response-title",
                    text: "As shown (the raw response is above)"
                }));
                $view.append($("<pre>", { "class": "cre-special-json", text: JSON.stringify(shown, null, 2) }));
            }

            // Nothing to add when the response is already shown as it stands.
            if ($view.children().length) {
                $container.append($view);
            }
        });
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
        renderSubscribeFields();
        syncMetadataRows();
        decorateFieldCopies();
        decorateSpecialTables();
        decorateBytesFields();
        decorateResponseSpecials();
    }, 100);
});
