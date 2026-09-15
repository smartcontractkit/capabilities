// The special value types, shared by both pages.
//
// A values.v1.BigInt is a sign and a big-endian byte string; a values.v1.Decimal
// is that plus an exponent. Neither is a number anybody wants to type or read, so
// the pages show the number and this does the arithmetic.
//
// Shared because both pages need it and neither should have its own copy: the
// encoding has to match what Go's big.Int and decimal.Decimal produce exactly,
// and two copies of that is two things to keep right.
//
// Which messages are special, and where a response holds them, is always passed
// in - it comes from the descriptors (see special.go). Nothing here recognises one
// from the shape of the data.

(function () {
    var api = {};

    // base64 of a non-negative BigInt's big-endian bytes, which is what
    // big.Int.Bytes() produces - empty for zero.
    function bytesOf(n) {
        if (n === 0n) {
            return "";
        }
        var binary = "";
        while (n > 0n) {
            binary = String.fromCharCode(Number(n & 0xffn)) + binary;
            n >>= 8n;
        }
        return btoa(binary);
    }

    // The inverse: big-endian bytes back to a non-negative BigInt.
    function intOfBytes(base64) {
        var binary;
        try {
            binary = atob(String(base64 || ""));
        } catch (e) {
            return null;
        }
        var n = 0n;
        for (var i = 0; i < binary.length; i++) {
            n = (n << 8n) | BigInt(binary.charCodeAt(i) & 0xff);
        }
        return n;
    }

    // encodeBigInt turns a typed number into the fields the message carries, the
    // same way Go does it: the sign on its own, and the magnitude as bytes.
    //
    // Returns null for an empty box, which is a field left unset, and undefined for
    // something that is not a number - which leaves the form alone rather than
    // writing a guess into it.
    function encodeBigInt(text) {
        var trimmed = String(text == null ? "" : text).trim();
        if (trimmed === "" || trimmed === "-" || trimmed === "+") {
            return null;
        }
        if (!/^[+-]?\d+$/.test(trimmed)) {
            return undefined;
        }

        var n = BigInt(trimmed);
        var negative = n < 0n;
        return {
            absVal: bytesOf(negative ? -n : n),
            sign: n === 0n ? "0" : (negative ? "-1" : "1")
        };
    }

    function decodeBigInt(absVal, sign) {
        var magnitude = intOfBytes(absVal);
        if (magnitude === null) {
            return "";
        }
        if (magnitude === 0n) {
            return "0";
        }
        return (Number(sign) < 0 ? "-" : "") + magnitude.toString();
    }

    // encodeDecimal splits the number on its decimal point: the digits with the
    // point taken out are the coefficient, and how many digits followed it is the
    // exponent, negated. So 123.45 is 12345 with an exponent of -2, which is what
    // Go's decimal.Decimal reports for the same number.
    function encodeDecimal(text) {
        var trimmed = String(text == null ? "" : text).trim();
        if (trimmed === "" || trimmed === "-" || trimmed === "+" || trimmed === ".") {
            return null;
        }
        if (!/^[+-]?(\d+\.?\d*|\.\d+)$/.test(trimmed)) {
            return undefined;
        }

        var negative = trimmed.charAt(0) === "-";
        var digits = trimmed.replace(/^[+-]/, "");
        var point = digits.indexOf(".");

        var exponent = 0;
        if (point !== -1) {
            exponent = -(digits.length - point - 1);
            digits = digits.slice(0, point) + digits.slice(point + 1);
        }

        var coefficient = encodeBigInt((negative ? "-" : "") + digits);
        if (!coefficient) {
            return undefined;
        }
        return { coefficient: coefficient, exponent: String(exponent) };
    }

    // decodeDecimal puts the point back: the coefficient's digits with the point
    // moved left by the exponent.
    function decodeDecimal(absVal, sign, exponent) {
        var digits = decodeBigInt(absVal, "1");
        if (digits === "") {
            return "";
        }
        var negative = Number(sign) < 0 && digits !== "0";
        var shift = Number(exponent);
        if (!isFinite(shift)) {
            shift = 0;
        }

        var out;
        if (shift >= 0) {
            out = digits + new Array(shift + 1).join("0");
        } else {
            var places = -shift;
            while (digits.length <= places) {
                digits = "0" + digits;
            }
            out = digits.slice(0, digits.length - places) + "." + digits.slice(digits.length - places);
        }
        return (negative ? "-" : "") + out;
    }

    // Follows one configured path into a response, reporting where each match sits
    // as well as what it is: a "[]" segment expands over array indices and "{}"
    // over map keys, so one template yields an entry per actual value.
    //
    // Missing keys are skipped rather than reported as empty: a response leaves
    // defaults out, so an all-zero message is simply absent.
    //
    // The parent and key come back too, because showing a number in place of a
    // message means replacing it where it sits.
    //
    // One spelling per caller, never both. A response is keyed by the original
    // proto names, which is what the configured paths are built from; grpcui's own
    // request model is keyed by lowerCamelCase. So the caller says which it has and
    // the segments are spelled to match, rather than trying each in turn and
    // accepting something that could never arrive.
    function resolvePath(root, template, lowerCamel) {
        var found = [{ node: root, parent: null, key: null, label: "" }];

        template.split(".").forEach(function (segment) {
            var isList = /\[\]$/.test(segment);
            var isMap = /\{\}$/.test(segment);
            var name = segment.replace(/(\[\]|\{\})$/, "");
            if (lowerCamel) {
                name = camelCase(name);
            }
            var next = [];

            found.forEach(function (entry) {
                if (!entry.node || typeof entry.node !== "object") {
                    return;
                }
                var value = entry.node[name];
                if (value === undefined || value === null) {
                    return;
                }
                var base = entry.label ? entry.label + "." + name : name;

                if (isList) {
                    if (value instanceof Array) {
                        value.forEach(function (item, i) {
                            next.push({ node: item, parent: value, key: i, label: base + "[" + i + "]" });
                        });
                    }
                } else if (isMap) {
                    Object.keys(value).forEach(function (key) {
                        next.push({ node: value[key], parent: value, key: key, label: base + "{" + key + "}" });
                    });
                } else {
                    next.push({ node: value, parent: entry.node, key: name, label: base });
                }
            });

            found = next;
        });

        return found.filter(function (entry) {
            return entry.node && typeof entry.node === "object" && !(entry.node instanceof Array);
        });
    }

    // ---- shared helpers ------------------------------------------------------

    // camelCase is the spelling grpcui's request model uses for a proto field.
    function camelCase(name) {
        return name.replace(/_([a-z0-9])/g, function (all, c) { return c.toUpperCase(); });
    }

    // kindOf maps a configured type name to which arithmetic it takes.
    function kindOf(typeName, special) {
        if (special && typeName === special.decimal) {
            return "decimal";
        }
        if (special && typeName === special.bigInt) {
            return "bigInt";
        }
        return null;
    }

    // The proto field names these messages are made of. They are the message's own
    // contract - a BigInt without an abs_val is not a BigInt - so a mismatch shows
    // up as a missing widget rather than as wrong arithmetic.
    var ABS_VAL = "abs_val";
    var SIGN = "sign";
    var COEFFICIENT = "coefficient";
    var EXPONENT = "exponent";

    // numberOf is the number a decoded message stands for. Absent fields are the
    // zero value, since a response leaves defaults out.
    function numberOf(node, kind, lowerCamel) {
        if (!node || typeof node !== "object") {
            return "";
        }
        var name = lowerCamel ? camelCase : function (n) { return n; };

        if (kind === "bigInt") {
            return decodeBigInt(node[name(ABS_VAL)] || "", node[name(SIGN)] || 0);
        }
        var coefficient = node[name(COEFFICIENT)] || {};
        return decodeDecimal(
            coefficient[name(ABS_VAL)] || "",
            coefficient[name(SIGN)] || 0,
            node[name(EXPONENT)] || 0);
    }

    // parsed is a copy of a response with every special message replaced by the
    // number it stands for.
    //
    // A copy, and only for showing: the original is what gets saved, so a saved
    // history is still the raw messages a replay needs. See saveHistory.
    function parsed(root, entries, special, lowerCamel) {
        if (!entries || !entries.length || root === null || root === undefined) {
            return root;
        }

        var copy;
        try {
            copy = JSON.parse(JSON.stringify(root));
        } catch (e) {
            return root;
        }

        entries.forEach(function (entry) {
            var kind = kindOf(entry.type, special);
            if (!kind) {
                return;
            }
            resolvePath(copy, entry.path, lowerCamel).forEach(function (hit) {
                if (!hit.parent) {
                    return;
                }
                hit.parent[hit.key] = numberOf(hit.node, kind, lowerCamel);
            });
        });
        return copy;
    }

    api.bytesOf = bytesOf;
    api.intOfBytes = intOfBytes;
    api.encodeBigInt = encodeBigInt;
    api.decodeBigInt = decodeBigInt;
    api.encodeDecimal = encodeDecimal;
    api.decodeDecimal = decodeDecimal;
    api.resolvePath = resolvePath;
    api.camelCase = camelCase;
    api.kindOf = kindOf;
    api.numberOf = numberOf;
    api.parsed = parsed;
    api.fields = { absVal: ABS_VAL, sign: SIGN, coefficient: COEFFICIENT, exponent: EXPONENT };

    window.__CRE_VALUES__ = api;
}());
