Dragonfly.format = function (s /*, args... */)
{
    var d = Dragonfly;
    var f = d.format;

    var prof = new d.Profiler ('Format');
    prof.toggle ('format');

    var parts = f.splitParts (s);

    for (var i = 0; i < parts.length; i++) {
        var part = parts[i];

        /* handle escaped {{, }}, and } */
        if (part.charAt(0) != '{') {
            if (part.charAt(0) != '}') {
                parts[i] = f.unescapeBraces (part);
                continue;
            }
            parts[i] = part.charAt(0);
            continue;
        }
        if (part.charAt(1) == '{') {
            parts[i] = '{';
            continue;
        }

        var matches = f.matchPart (part);
        var arg = parseInt (matches[1]);
        var minWidth = parseInt (matches[2]);
        var fmtStr = matches[3];

        //logDebug ('arg:', arg, 'minWidth:', minWidth, 'fmtStr:', fmtStr);

        if (arg < 0 || arg + 1 >= arguments.length) {
            throw new Error ('Argument index out of range');
        }

        part = arguments[arg + 1];
        //logDebug ('part:', part, 'type:', typeof part);

        if (fmtStr) {
            fmtStr = f.unescapeBraces (fmtStr);
        }
        if (part.format) {
            part = part.format (fmtStr);
        } else if (part instanceof Date) {
            part = d.formatDate (part, fmtStr);
        } else if (typeof part == 'number') {
            part = d.formatNumber (part, fmtStr);
        } else if (fmtStr) {
            part = fmtStr;
        }
        parts[i] = part;
    }

    var ret = parts.join ('');
    prof.toggle ('format');
    return ret;
};

Dragonfly.format.unescapeBraces = function (s)
{
    return s.replace ('}}', '}').replace ('{{', '{');
};

//Dragonfly.format.splitRegExp = /((?:\{\{)|(?:\{[^:}]*(?::(?:[^}]|(?:}}))*)?})|(?:}}))/;
Dragonfly.format.splitParts = function (s)
{
    var d = Dragonfly;

    // return s.split (d.format.splitRegExp);

    var prof = new d.Profiler ('Format');
    prof.toggle ('splitParts');

    var res = [ ];
    var braceOpen, braceClose = 0;

    do {
        braceOpen = s.indexOf ('{', braceClose);
        // no more open braces
        if (braceOpen == -1) {
            res.push (s.substring (braceClose));
            break;
        }

        if (braceOpen == s.length - 1) {
            prof.toggle ('splitParts');
            throw new Error ('Format exception');
        }

        // peek ahead, and escape {{ as {
        if ('{' == s.charAt (braceOpen + 1)) {
            res.push (s.substring (braceClose, braceOpen + 2));
            braceClose = braceOpen + 2;
            continue;
        }

        if (braceOpen > braceClose) {
            res.push (s.substring (braceClose, braceOpen));
        }

        braceClose = braceOpen + 1;
        var reg = '[}:]';
        do {
            braceClose = s.indexOf ('}', braceClose);
            // no matching close brace
            if (braceClose == -1) {
                prof.toggle ('splitParts');
                throw new Error ('Format exception');
            }

            // peek ahead, and handle escaped }}
            if (braceClose < s.length - 1 &&
                '}' == s.charAt (braceClose + 1) &&
                s.substring (braceOpen, braceClose).indexOf (':') != -1) {
                braceClose += 2;
                continue;
            }
        } while (false);

        res.push (s.substring (braceOpen, ++braceClose));
    } while (braceClose < s.length);

    prof.toggle ('splitParts');

    //logDebug (s, '->', serializeJSON (res));

    return res;
};

//Dragonfly.format.formatRegExp = /^\{([^,:}]*)(?:,([^:}]*))?(?::((?:[^}]|(?:}}))*))?}$/;
Dragonfly.format.matchPart = function (s)
{
    var d = Dragonfly;

    var prof = new d.Profiler ('Format');
    prof.toggle ('matchPart');

    // return s.match (d.format.formatRegExp);

    if (s.charAt(s.length - 1) != '}') {
        prof.toggle ('matchPart');
        throw new Error ('Invalid format string: >>' + s + '<<');
    }

    var res = [ s ];

    var comma = s.indexOf (',', 1);
    var colon = s.indexOf (':', 1);
    if (comma > colon && colon != -1) {
        comma = -1;
    }

    res[1] = s.substring (1, comma != -1 ? comma : colon != -1 ? colon : s.length - 1);
    res[2] = comma != -1 ? s.substring (comma + 1, colon != -1 ? colon : s.length - 1) : null;
    res[3] = colon != -1 ? s.substring (colon + 1, s.length - 1) : null;

    //logDebug (s, '=>', serializeJSON (res));

    prof.toggle ('matchPart');

    return res;
};

Dragonfly.formatNumber = function (n, s)
{
    var d = Dragonfly;

    var prof = new d.Profiler ('Format');
    prof.toggle ('formatNumber');

    if (arguments.length < 2 || s == undefined || s == null) {
        s = 'G';
    }

    function nChars (c, n)
    {
        var s = [ ];
        for (var i = 0; i < n; i++) {
            s.push (c);
        }
        return s.join ('');
    }

    var ret;
    if (s.match (/^[cCdDeEfFgGnNpPrRxX]\d*$/)) {
        var hasPrecision = s.length > 1;
        var precision = hasPrecision && parseInt (s.substr (1));
        switch (s.charAt (0).toLowerCase()) {
            case 'c':
                precision = hasPrecision ? nChars ('0', precision) : '00';
                if (n < 0) {
                    n = -n;
                    s = '($#,##0.' + precision + ')';
                } else {
                    s = '$#,##0.' + precision;
                }
                break;

            case 'd':
                if (!d.isInteger (n)) {
                    throw new Error ('Integer format on non-integer value');
                }
                s = nChars ('0', hasPrecision ? precision : 1);
                break;

            case 'e':
                var e = n.toExponential (hasPrecision ? precision : 6);
                var idx = e.indexOf ('e');
                var mantissa = e.substr (idx + 2);
                
                ret = e.substring (0, idx) + s.charAt(0) + e.charAt(idx + 1) + nChars ('0', 3 - mantissa.length) + mantissa;
                break;
            case 'f':
                ret = n.toFixed (2);
                break;
            case 'g':
                var g = n.toString ();
                ret = s.charAt(0) == 'G' ? g.replace ('e', 'E') : g;
                break;
            case 'n':
                s = '-#,##0.' + nChars ('0', hasPrecision ? precision : 2);
                break;

            case 'p':
                s = '-0%';
                break;

            case 'r':
                ret = n.toString ();
                break;
            case 'x':
                var x = n.toString (16);
                x = s.charAt(0) == 'X' ? x.toUpperCase () : x.toLowerCase ();
                ret = hasPrecision ? nChars ('0', precision - x.length) + x : x;
                break;
            default:
                prof.toggle ('formatNumber');
                throw new Error ('Format matched pattern, but no handler found');
        }
    }
    
    if (ret == null) {
        try {
            ret = numberFormatter (s)(n);
        } catch (e) {
            logWarning ('numberFormatter failed:', e, '(' + s + ')');
            ret = s;
        }
    }
    prof.toggle ('formatNumber');
    return ret;
};

Dragonfly.DateFormat = {
    d:    1,
    dd:   2,
    ddd:  3,
    dddd: 4,

    f:    5,

    g:    9,

    H:    13,
    HH:   14,

    h:    17,
    hh:   18,

    M:    21,
    MM:   22,
    MMM:  23,
    MMMM: 24,

    m:    25,
    mm:   26,

    s:    29,
    ss:   30,
    
    t:    33,
    tt:   34,

    y:    37,
    yy:   38,
    yyy:  39,
    yyyy: 40,
    
    z:    41,

    ':':  45,

    '/':  46,

    '%':  47,

    '\\': 48,

    '"':  49,
    "'":  49
};

Dragonfly.DefaultDateFormat = {
    d: 'MM/dd/yyyy',
    D: 'dddd, MMMM dd, yyyy',
    f: 'dddd, MMMM dd, yyyy h:mm tt',
    F: 'dddd, MMMM dd, yyyy h:mm:ss tt',
    g: 'MM/dd/yyyy h:mm tt',
    G: 'MM/dd/yyyy h:mm:ss tt',
    m: 'MMMM dd',
    M: 'MMMM dd',
    r: 'ddd, dd MMM yyyy HH:mm:ss "GMT"',
    R: 'ddd, dd MMM yyyy HH:mm:ss "GMT"',
    s: 'yyyy-MM-ddTHH:mm:ss',
    S: 'yyyy-MM-ddTHH:mm:ss "GMT"',
    t: 'h:mm tt',
    T: 'h:mm:ss tt',
    u: 'yyyy-MM-dd HH:mm:ssZ',
    U: 'dddd, MMMM dd, yyyy h:mm:ss tt',
    y: 'MMMM yyyy',
    Y: 'MMMM yyyy'
};

Dragonfly.parseDateFormat = function (s)
{
    var d = Dragonfly;
    var df = d.DateFormat;

    var prof = new d.Profiler ('Format');
    prof.toggle ('parseDateFormat');

    var ret = [ ];
    var sLength = s.length;

    var j, c;
    for (var i = 0; i < sLength; i++) {

        // skip strings we don't recognize in one 'push'
        for (j = i; j < sLength; j++) {
            c = s.charAt (j);
            
            if (d.DateFormat[c]) {
                break;
            }
        }

        if (j > i) {
            ret.push (s.substring (i, j));
            if (j >= sLength) {
                // done!
                break;
            }
        }

        i = j;

        switch (c) {
        // literals
        case ':':
        case '/':
        case '%':
            ret.push (c);
            break;

        case '\\':
            ret.push (s.charAt(++i));
            break;

        case '"':
        case "'":
            for (j = ++i; j < sLength; j++) {
                var cj = s.charAt (j);
                if (cj == '\\') {
                    if (j > i) {
                        ret.push (s.substring (i, j));
                    }
                    i = j + 1;
                } else if (cj == c) {
                    break;
                }
            }
            if (j > i) {
                ret.push (s.substring (i, j));
            }
            i = j;
            break;

        default:
            // get a group of consecutive characters
            for (j = i + 1; j < sLength && s.charAt(j) == c; j++) {
                ;
            }
            
            var tok = df[s.substring (i, j)];
            ret.push (tok || s.substring (i, j));
            i = j - 1;
            break;
        }
    }   
    prof.toggle ('parseDateFormat');
    return ret;
};

Dragonfly._formatDate = function (dt, tokens)
{
    var d = Dragonfly;
    var df = Dragonfly.DateFormat;

    var prof = new d.Profiler ('Format');
    prof.toggle ('_formatDate');

    var tokLength = tokens.length;
    var ret = [ ];
    for (var i = 0; i < tokLength; i++) {
        var tok = tokens[i];
        if (typeof tok == 'string') {
            ret.push (tok);
            continue;
        }

        var v = null;
        switch (tok) {

        case df.d:
            v = dt.getUTCDate();
            break;

        case df.dd:
            v = dt.getUTCDate();
            if (v < 10) {
                v = '0' + v;
            }
            break;

        case df.ddd:
            v = d.getDayAbbr (dt.getUTCDay());
            break;

        case df.dddd:
            v = d.getDayName (dt.getUTCDay());
            break;

        case df.M:
            v = dt.getUTCMonth() + 1;
            break;

        case df.MM:
            v = dt.getUTCMonth() + 1;
            if (v < 10) {
                v = '0' + v;
            }
            break;

        case df.MMM:
            v = d.getMonthAbbr (dt.getUTCMonth());
            break;

        case df.MMMM:
            v = d.getMonthName (dt.getUTCMonth());
            break;

        case df.y:
            v = dt.getUTCFullYear () % 100;
            break;

        case df.yy:
            v = dt.getUTCFullYear () % 100;
            if (v < 10) {
                v = '0' + v;
            }
            break;

        case df.yyy:
        case df.yyyy:
            v = dt.getUTCFullYear ();
            break;

        case df.g:
            break;

        case df.h:
            v = ((dt.getUTCHours () + 11) % 12) + 1;
            break;

        case df.hh:
            v = ((dt.getUTCHours () + 11) % 12) + 1;
            if (v < 10) {
                v = '0' + v;
            }
            break;

        case df.H:
            v = dt.getUTCHours ();
            break;

        case df.HH:
            v = dt.getUTCHours ();
            if (v < 10) {
                v = '0' + v;
            }
            break;

        case df.m:
            v = dt.getUTCMinutes ();
            break;

        case df.mm:
            v = dt.getUTCMinutes ();
            if (v < 10) {
                v = '0' + v;
            }
            break;

        case df.s:
            v = dt.getUTCSeconds ();
            break;

        case df.ss:
            v = dt.getUTCSeconds ();
            if (v < 10) {
                v = '0' + v;
            }
            break;

        case df.f:
            // FIXME: this should support N fs
            v = Math.floor (dt.getUTCMilliseconds() * Math.pow (10, Math.max (1, 7) - 3));
            break;

        case df.t:
            v = dt.getUTCHours () < 12 ? 'M' : 'M';
            break;

        case df.tt:
            v = dt.getUTCHours () < 12 ? 'AM' : 'PM';
            break;

        case df.z:
            break;

        case df[':']:
            v = ':';
            break;

        case df['/']:
            v = '/';
            break;

        case df['%']:
            v = '%';
            break;

        default:
            prof.toggle ('_formatDate');
            throw new Error ('Invalid date format token: ' + tok);
        }

        v && ret.push (v);
    }

    var ret = ret.join ('');
    prof.toggle ('_formatDate');
    return ret;
};

Dragonfly.formatDate = function (dt, s)
{
    var d = Dragonfly;
    var ddf = d.DefaultDateFormat;

    var prof = new d.Profiler ('Format');
    prof.toggle ('formatDate');

    if (arguments.length < 2 || s == undefined || s == null) {
        s = 'G';
    }

    var tokens;
    if (s.length == 1) {
        tokens = ddf[s];
        if (typeof tokens == 'string') {
            tokens = d.parseDateFormat (tokens);
            // logDebug ('formatDate: caching', s, '=', ddf[s], '->', serializeJSON (tokens));
            ddf[s] = tokens;
        }
    } else {
        tokens = d.parseDateFormat (s);
    }

    if (!tokens) {
        prof.toggle ('formatDate');
        throw new Error ('Input string was not in a correct format:', s);
    }

    // logDebug ('string:', s, '-> tokens:', serializeJSON (tokens));

    var ret = d._formatDate (dt, tokens);
    prof.toggle ('formatDate');
    return ret;
};

Dragonfly.formatDateRange = function (s, d0, d1)
{
    var d = Dragonfly;

    var df = d.DateFormat;

    var prof = new d.Profiler ('Format');
    prof.toggle ('formatDateRange');

    var today = new Date();

    if (s.length > 1) {
        prof.toggle ('formatDateRange');
        throw new Error ('Input string was not in a correct format.');
    }

    var f = [ { 
        isThisYear: today.getFullYear() == d0.getUTCFullYear (),
        isThisMonth: today.getMonth() == d0.getUTCMonth ()
    }, {
        isThisYear: today.getFullYear() == d1.getUTCFullYear (),
        isThisMonth: today.getMonth() == d1.getUTCMonth (),
        isSameYear: d0.getUTCFullYear() == d1.getUTCFullYear(),
        isSameMonth: d0.getUTCMonth() == d1.getUTCMonth(),
        isSameDay: d0.getUTCDate() == d1.getUTCDate(),
        isSameHalfDay: (d0.getUTCHours() < 11) == (d1.getUTCHours() < 11)
    }];

    var s0 = s;
    var s1 = s;

    var ret;
    switch (s) {
    case 'd':
        if (f[1].isSameYear &&
            f[1].isSameMonth &&
            f[1].isSameDay) {
            ret = d.formatDate (d1, 'd');
            break;
        }
        break;

    case 'D':
        s0 = s1 = 'dddd, MMMM d';
        if (f[1].isSameYear) {
            if (f[1].isSameMonth) {
                if (f[1].isSameDay) {
                    ret = d.formatDate (d1, f[0].isThisYear ? 'dddd, MMMM d' : 'D');
                    break;
                }
            }
            if (!f[1].isThisYear) {
                s1 += ', yyyy';
            }
        } else {
            s0 += ', yyyy';
            s1 += ', yyyy';
        }
        break;

    case 'f':
    case 'F':
        var t = s == 'f' ? 'h:mm' : 'h:mm:ss';
        s0 = s1 = 'dddd, MMMM d ' + t;
        if (f[1].isSameYear) {
            if (f[1].isSameMonth && f[1].isSameDay) {
                if (!f[1].isSameHalfDay) {
                    s0 += ' tt';
                }
                s1 = t + ' tt';
            } else {
                s0 += ' tt';
                s1 += ' tt';
            }
            if (!f[1].isThisYear) {
                s1 += ', yyyy';
            }
        } else {
            s0 += ' tt, yyyy';
            s1 += ' tt, yyyy';
        }
        break;

    case 'g':
    case 'G':
        var t = s == 'g' ? 'h:mm' : 'h:mm:ss';
        s0 = s1 = 'M/d/yyyy ' + t;
        if (f[1].isSameYear) {
            if (f[1].isSameMonth && f[1].isSameDay) {
                if (!f[1].isSameHalfDay) {
                    s0 += ' tt';
                }
                s1 = t + ' tt';
            } else {
                s0 += ' tt';
                s1 += ' tt';
            }
        } else {
            s0 += ' tt';
            s1 += ' tt';
        }
        break;
        
    case 'm':
    case 'M':
        if (f[1].isSameYear) {
            if (f[1].isSameMonth) {
                s1 = 'd';
            }
        }
        break;
    case 's':
        s0 = s1 = 'h:mm';
        break;

    case 't':
    case 'T':
        break;

    case 'y':
    case 'Y':
        if (f[1].isSameYear) {
            s0 = 'MMMM';
        }
        break;

    default:
        prof.toggle ('formatDateRange');
        throw new Error ('Input string was not in a correct format.');
    }

    if (ret == null) {
        ret = d.formatDate (d0, s0) + ' - ' + d.formatDate (d1, s1);
    }
    prof.toggle ('formatDateRange');
    return ret;
};

Dragonfly.getDayAbbr = function (day)
{
    return [ 'Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat' ][day];
};

Dragonfly.getDayName = function (day)
{
    return [ 'Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday' ][day];
};

Dragonfly.getMonthAbbr = function (month)
{
    return [ 'Jan', 'Feb', 'Mar', 'Apr', 
             'May', 'Jun', 'Jul', 'Aug', 
             'Sep', 'Oct', 'Nov', 'Dec' ][month];
};

Dragonfly.getMonthName = function (month)
{
    return [ 'January', 'February', 'March', 'April', 
             'May', 'June', 'July', 'August', 
             'September', 'October', 'November', 'December' ][month];
};

f_ = function (s)
{
    s = _(s);
    return Dragonfly.format.apply (this, arguments);
};
