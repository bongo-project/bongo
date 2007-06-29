/*
 * Calendar Naming Conventions:
 * "Day" refers to internal Day objects, representing YYYY/MM/DD days
 * "Date" (generally) refers to either the month date or a js Date object
 * "Time" refers to a js Date object, representing an instant in time.
 *
 * Indexing Conventions:
 * String months are indexed by 1, integer months by 0.
 * Ranges of Days are typically inclusive
 */

// Javascript Dates are a poor abstraction for representing a calendar day.
// Using Days guards against weird timezone related errors and abstracts
// away most of the pain of calendar calculations and iteration.

/* This should be considered private */
Day = function (jsdate, intform, year, month, date)
{
    var prof = new Dragonfly.Profiler ('Day');
    if (arguments.length != 5) {
        throw new Error ("Not enough arguments passed to Day.");
    }
    prof.toggle ('Day');
    this.jsdate = jsdate;
    this.intform = intform;
    this.year = year;
    this.month = month;
    this.date = date;
    this.day = jsdate.getUTCDay();
    prof.toggle ('Day');
};

Day.prototype = { };

/*
 * This tries to return a cached, without creating any Date objects if
 * possible.
 *
 * It can get away with this when:
 *
 * a) it is passed a Day (which it just returns)
 * b) date is between 1 and 28, and month is between 0 and 11
 * c) it is passed a jsdate and told it can keep it with arguments[2]
 *
 * Day.get ()
 * Day.get (Day)
 * Day.get (Date, bool keepDate)
 */
Day.get = function (year, month, date) // expects integer arguments
{
    var prof = new Dragonfly.Profiler ('Day');
    var jsdate;
    var keepDate = false;
    var isUTC = false;

    prof.toggle ('Day.get');

    // assume we won't allocate a jsdate...
    Day._noallocs++;

    if (arguments.length == 0) {
        Day._noallocs--;
        jsdate = new Date();
        year = jsdate.getFullYear();
        month = jsdate.getMonth();
        date = jsdate.getDate();
    } else if (arguments.length == 3) {
        /* if these are in the standard calendar range then attempt to
         * avoid allocating an unecessary date if we happen to get a
         * cache hit */
        if (date < 1 || date > 28 || month < 0 || month > 11) {
            /* On Firefox, Date.UTC does not work with negative values, so
             * we have to build a normal date first */
            Day._noallocs--;
            jsdate = new Date (year, month, date);
            year = jsdate.getFullYear();
            month = jsdate.getMonth();
            date = jsdate.getDate();
        }
    } else {
        /* Day.get (Day) */
        if (year.intform) {
            Day._hits++;
            prof.toggle ('Day.get');
            return year;
        }
        /* Day.get (Date, keepDate) */
        jsdate = year;
        keepDate = month;
        year = jsdate.getUTCFullYear();
        month = jsdate.getUTCMonth();
        date = jsdate.getUTCDate();
        isUTC = true;
    }

    var intform = year * 10000 + month * 100 + date;

    var hit = Day._cache[intform];
    if (hit) {
        Day._hits++;
        prof.toggle ('Day.get');
        return hit;
    }

    if (!jsdate || !keepDate) {
        /* the only case that we haven't accounted for is when we were
         * passed a jsdate that we weren't told to keep, which is the
         * isUTC case */
        if (isUTC) {
            Day._noallocs--;
            /* we could do this, but i think not modifying the date is
             * probably faster */
            // jsdate = new Date (jsdate);
            // jsdate.setUTCHours (0, 0, 0);
        }
        jsdate = new Date (Date.UTC (year, month, date));
    }

    hit = new Day (jsdate, intform, year, month, date);
    Day._cache[intform] = hit;

    Day._misses++;
    Day._total++;

    prof.toggle ('Day.get');
    return hit;
};

Day.printCacheStats = function ()
{
    var total = (Day._hits + Day._misses) / 100;
    logDebug ('Day cache: hits:', Math.floor (Day._hits / total),
              'no alloc:', Math.floor (Day._noallocs / total),
              'size:', Day._total);
};

Day.resetCacheStats = function ()
{
    Day._noallocs = Day._hits = Day._misses = 0;
};

Day.resetCache = function ()
{
    Day._cache = { };
    Day._total = Day._noallocs = Day._hits = Day._misses = 0;
};
Day.resetCache();

// Some time calculation utility functions
Day.localToUTC = function (date)
{
    return new Date (Date.UTC (date.getFullYear (),
                               date.getMonth (),
                               date.getDate (),
                               date.getHours (),
                               date.getMinutes (),
                               date.getSeconds (),
                               date.getMilliseconds ()));
};

Day.getUTCToday = function (hrs, min)
{
    var today = Day.localToUTC (new Date());
    today.setUTCHours (hrs || 0, min || 0, 0, 0);
    return today;
};

Day.secondOfDay = function (date)
{
    return date.getUTCSeconds() + date.getUTCMinutes() * 60 + date.getUTCHours() * 3600;
};

Day.prototype.format = function (s)
{
    return Dragonfly.formatDate (this.jsdate, s);
};

Day.prototype.toString = function () // Client URL friendly string coersion
{
    return [this.year, this.month + 1, this.date].join ('/');
};

// This may not work for large offsets (>30 days) in some browers (e.g. Safari 2.0.3)
Day.prototype.offsetBy = function (offset)
{
    var prof = new Dragonfly.Profiler ('Day');
    prof.toggle ('Day.offsetBy');
    /*
    var date = new Date (this.jsdate);
    date.setUTCDate (date.getUTCDate() + offset);
    var ret = Day.get (date, true);
    */
    ret = Day.get (this.year, this.month, this.date + offset);
    prof.toggle ('Day.offsetBy');
    return ret;
};

Day.prototype.minus = function (day) // This is a kludge...
{
    return Math.round ((this.jsdate - day.jsdate) / (24 * 60 * 60 * 1000));
};

Day.prototype.getMonthStart = function ()
{
    return Day.get (this.year, this.month, 1);
};

Day.prototype.getPrevMonthStart = function ()
{
    return Day.get (this.year, this.month - 1, 1);
};

Day.prototype.getNextMonthStart = function ()
{
    return Day.get (this.year, this.month + 1, 1);
};

Day.prototype.getWeekStart = function ()
{
    return Day.get (this.year, this.month, this.date - this.day);
};

Day.prototype.getNextWeekStart = function ()
{
    return Day.get (this.year, this.month, this.date - this.day + 7);
};

Day.prototype.getMonthViewStart = function ()
{
    var date = new Date (this.jsdate);
    date.setUTCDate (1);
    date.setUTCDate (1 - date.getUTCDay ());
    return Day.get (date, true);
};

Day.prototype.getMonthViewEnd = function ()
{
    var date = new Date (this.jsdate);
    date.setUTCMonth (date.getUTCMonth() + 1, 0);
    date.setUTCDate (date.getUTCDate() + 6 - date.getUTCDay ());
    return Day.get (date, true);
};

Day.prototype.asDate = function ()
{
    return new Date (this.jsdate);
};


Day.prototype.asDateWithTime = function (hours, minutes, seconds)
{
    return new Date (Date.UTC (this.year, this.month, this.date, 
                               hours || 0, minutes || 0, seconds || 0));
};

Day.fromIntform = function (i)
{
    return Day.get ((i / 10000) | 0, ((i % 10000) / 100) | 0, i % 100);
}

Day.formatRange = function (s, a, b)
{
    return Dragonfly.formatDateRange (s, a.jsdate, b.jsdate);
};

Day.compare = function (a, b) {
    return (a.intform < b.intform) ? -1 : (a.intform > b.intform) ? 1 : 0;
};

registerComparator ('DayComparator', // Registers a comparator with Mochikit
                    function (a, b) { return (a instanceof Day) && (b instanceof Day); },
                    Day.compare);
    
// Mochikit-style iterator for Days. end parameter can be either a Day or 
// integer (if it's a Day, end will be included in the iteration). Increment
// is an optional integer.
Day.iter = function (day, end, increment) {
    increment = increment || 1;

    var stopAt;
    if (end instanceof Day) {
        stopAt = end.intform;
    } else {
        stopAt = day.offsetBy (increment * end - 1).intform;
    }
    //logDebug ('iter: day:', day, 'end:', end, 'increment:', increment, 'stopAt:', stopAt);
    return { 'next' : 
        function () {
            if (stopAt < day.intform) {
                throw StopIteration;
            } 
            var currentday = day;
            day = day.offsetBy (increment);
            return currentday;
        }};
};

/* -------- JCal -------- */
Dragonfly.JCal = { };
Dragonfly.JCal.instanceScope = 1;
Dragonfly.JCal.followingScope = 2;
Dragonfly.JCal.allScope = 3;

Dragonfly.JCal.parse = function (jsobs)
{
    var d = Dragonfly;
    var j = d.JCal;
    var objs = [ ];
    var start = new Date;
    forEach (isArrayLike (jsobs) ? jsobs : [ jsobs ],
             function (jsob) {
                 try {
                     objs.push (new j.VCalendar (jsob));
                 } catch (e) {
                     logWarning ('error parsing object: ' + d.reprError (e) + ': ' + serializeJSON (jsob));
                 }
             });
    //logDebug ('took ' + (new Date - start) / 1000 + 's to parse ' 
    //          + objs.length + ' object' + ((objs.length != 1) ? 's' : ''));
    return objs;
};

Dragonfly.JCal.isDate = function (s)
{
    return /^\d{8}$/.test (s);
};

Dragonfly.JCal.isDateTime = function (s)
{
    return /^\d{8}T\d{6}Z?$/.test (s);
};

Dragonfly.JCal.isUntimed = function (v)
{
    return (condGetProp (v.params, 'type') == 'DATE');
};

// Workaround for bug 203351
Dragonfly.JCal.isFloating = function (v)
{
    return !Dragonfly.JCal.isUntimed (v) &&
           !condGetProp (v.params, 'tzid') && 
           !($V(v).charAt (15) == 'Z');
};


Dragonfly.JCal.parseDay = function (s)
{
    return (!Dragonfly.JCal.isDate (s) && !Dragonfly.JCal.isDateTime (s))
        ? null
        : Day.get (Number (s.substr (0,  4)),     // year
                    Number (s.substr (4,  2)) - 1, // month
                    Number (s.substr (6,  2)));    // date
};

Dragonfly.JCal.serializeDay = function (day)
{
    function paddate (n) {
        return n < 10 ? '0' + n : String (n);
    }
    return String (day.year) + paddate (day.month + 1) + paddate (day.date);
};

Dragonfly.JCal.parseDateTime = function (s)
{
    return (!Dragonfly.JCal.isDateTime (s))
        ? null
        : new Date (Date.UTC (s.substr (0,  4),     // year
                              s.substr (4,  2) - 1, // month
                              s.substr (6,  2),     // day
                              s.substr (9,  2),     // hour
                              s.substr (11, 2),     // minutes
                              s.substr (13, 2)));   // seconds
};

Dragonfly.JCal.serializeDateTime = function (date)
{
    function pad (n) {
        return n < 10 ? '0' + n : n;
    }

    return [date.getUTCFullYear(), pad (date.getUTCMonth() + 1),
            pad (date.getUTCDate()), 'T', pad (date.getUTCHours()),
            pad (date.getUTCMinutes()), pad (date.getUTCSeconds())].join ('');
};

Dragonfly.JCal.updateDateTime = function (datetime, day, time, isUntimed, tzid)
{
    var JCal = Dragonfly.JCal;

    var value = [ ];
    var params = deepclone (datetime.params);
    
    value.push (day ? JCal.serializeDay (day) : datetime.value.slice (0, 8));
    if (isUntimed) {
        params = condSetProp (params, 'type', 'DATE');
        condDelProp (params, 'tzid');
    } else {
        value.push ('T');
        value.push (time ? 
            JCal.serializeDateTime(time).slice(9) : 
            datetime.value.slice (9, 15));
        if (tzid == '/bongo/floating') { // event should float
            condDelProp (params, 'tzid');
        } else if (tzid) {
            params = condSetProp (datetime.params, 'tzid', tzid);
        } 
        condDelProp (params, 'type');        
    }

    datetime.value = value.join ('');
    if (keys(params).length == 0) {
        delete datetime.params;
    } else {
        datetime.params = params;
    }
}

// offset is in days for date values, msecs for datetime values
Dragonfly.JCal.offsetDateTime = function (datetime, offset)
{
    var JCal = Dragonfly.JCal;
    if (JCal.isUntimed (datetime)) {
        var newday = JCal.parseDay ($V(datetime)).offsetBy (offset);
        datetime.value = JCal.serializeDay (newday);
    } else {
        var newdate = new Date (JCal.parseDateTime ($V(datetime)).valueOf() + offset);
        datetime.value = JCal.serializeDateTime (newdate);
    }
}

Dragonfly.JCal.parseDuration = function (s, asUntimed)
{
    if (!s) {
        return null;
    }
    // >>> '+P1DT2H3M4S'.match (...)
    // ["+P1DT2H3M4S", "+", undefined, "1", "2", "3", "4"]
    // >>> '-P1W'.match (...)
    // ["-P1W", "-", "1", undefined, undefined, undefined, undefined]
    // >>> 'P1D'.match (...)
    // ["P1D", "", undefined, "1", undefined, undefined, undefined]
    var matches = s.match (/^([+-]?)P(?:(\d*)W|(?:(\d*)D)?(?:T(?:(\d*)H)?(?:(\d*)M)?(?:(\d*)S)?)?)$/);
    if (!matches) {
        return null;
    }
    var duration = 0;
    if (matches[2] || matches[2] == '0') {
        duration += parseInt (matches[2]) * (60 * 60 * 24 * 7);
    } else {
        [ 24, 60, 60, 1 ].each (
            function (mult, idx) {
                if (matches[3 + idx]) {
                    duration += parseInt (matches[3 + idx]);
                }
                duration *= mult;
            });
    }
    duration = (matches[0] && matches[0] == '-') ? -duration : duration;
    return (asUntimed)
        ? Math.max (Math.round (duration / (24 * 60 * 60)) - 1, 0)
        : duration * 1000;
};

Dragonfly.JCal.serializeDuration = function (duration, asUntimed)
{
    duration = (asUntimed)
            ? (duration + 1) * (24 * 60 * 60)
            : duration / 1000;
    var s = 'P';
    var days = Math.floor (duration / (24 * 60 * 60));
    if (days) {
        s += days + 'D';
        duration -= days * (24 * 60 * 60);
    }
    if (duration) {
        s += 'T';
    }
    var hours = Math.floor (duration / (60 * 60));
    if (hours) {
        s += hours + 'H';
        duration -= hours * (60 * 60);
    }
    var minutes = Math.floor (duration / 60);
    if (minutes) {
        s += minutes + 'M';
        duration -= minutes * 60;
    }
    if (duration) {
        s += duration + 'S';
    }
    return s;
};

Dragonfly.JCal.durationToDtend = function (dtstart, duration) { // Parameters are JSON-objects
    var JCal = Dragonfly.JCal;
    
    var isUntimed = JCal.isUntimed (dtstart);
    var dtend = deepclone (dtstart);
    var duration = JCal.parseDuration ($V(duration), isUntimed);
    JCal.offsetDateTime (dtend, isUntimed ? duration + 1 : duration);
    return dtend;
};

Dragonfly.JCal.createUID = function ()
{
    var d = Dragonfly;
    var j = d.JCal;

    var date = new Date;
    function padmilli (m) {
        return m < 10 ? '00' + m :
            m < 100 ? '0' + m :
            m;
    }
    return j.serializeDateTime (date) +
    '.' + padmilli (date.getMilliseconds()) +
    '-' + padmilli (Math.floor (Math.random () * 1000)) +
    padmilli (Math.floor (Math.random () * 1000)) +
    padmilli (Math.floor (Math.random () * 1000)) +
    '@' + window.location.hostname;
};

// Creates a new JCalendar, JEvent, JOccurrence
Dragonfly.JCal.buildNewEvent = function (summary, dayOrDate, tzid)
{
    var d = Dragonfly;
    var c = d.Calendar;

    summary = summary || _('eventDefaultTitle');
    var params = { };
    
    var start, end;
    if (!dayOrDate) {
        dayOrDate = Day.getUTCToday();
        dayOrDate.setDate (dayOrDate.getDate() + 1);
        dayOrDate.setUTCHours (12);
    }
    
    if (dayOrDate instanceof Date) { // this is a timed occurrance
        var time = dayOrDate;
        start = this.serializeDateTime (time);
        time.setUTCHours (time.getUTCHours() + 1);
        end = this.serializeDateTime (time);
        params.tzid = tzid || d.getCurrentTzid();
    } else {
        start = this.serializeDay (dayOrDate);
        end = this.serializeDay (dayOrDate.offsetBy (1));
        params.type = 'DATE';
    }

    var cal = c.getSelectedCalendar ();
    if (cal.type != 'personal') {
        cal = c.calendars.getByName ('Personal');
    }
    var jsob = {
        jcal: {
            version: { value: '2.0' },
            components: [{
                type: 'event',
                uid: { value: this.createUID() },
                dtstart: { value: start, params: deepclone(params)},
                dtend: { value: end, params: deepclone(params)},
                summary: { value: summary }
            }]            
        },
        occurrences: [{
            dtstart: { value: start, params: deepclone(params) },
            dtend: { value: end, params: deepclone(params) }
        }],
        calendarIds: [ cal.bongoId ]
    };
    return new this.VCalendar (jsob);
};

Dragonfly.JCal.saveOccurrence = function (occurrence, changes, scope)
{
    var vcal = occurrence.vcalendar;
    
    if (scope != Dragonfly.JCal.followingScope) {
        vcal.update (occurrence, changes, scope);
        return vcal.save();    
    }

    var sibling = vcal.split (occurrence);
    sibling.update (occurrence, changes, scope);
    return new Dragonfly.MultiDeferred (vcal.save(), sibling.save());    
};

// returns a deferred
Dragonfly.JCal.delOccurrence = function (occurrence, scope)
{
    var vcal = occurrence.vcalendar;

    if (!vcal.isRecurring || (scope == this.allScope)) { // delete the base event
        return Dragonfly.request ('DELETE', vcal.getLocation());

    } else if (scope == this.instanceScope) { // add occurrence to exdate list
        var event = vcal.event;
        event.jcal.exdates = (event.jcal.exdates) ? event.jcal.exdates : [ ];
        event.jcal.exdates.push (occurrence.jcal.recurid);
        if (occurrence.exception) { // remove exception object from jcal
            var cmps = vcal.jcal.components;
            cmps.splice (findIdentical (cmps, occurrence.exception.jcal), 1);
        }
        return vcal.save();

    } else if (scope == this.followingScope) { // edit the rrule termination condition
        vcal.updateRrulesEnd (this.serializeDay (occurrence.startDay.offsetBy (-1)));
        return vcal.save();
    }
};

Dragonfly.JCal.VCalendar = function (jsob)
{
    this.parse (jsob);
};

Dragonfly.JCal.VCalendar.SpecialFields = {
    startDay: true, startTime: true, startTzid: true,
    endDay: true, endTime: true, endTzid: true, 
    isUntimed: true, duration: true, rrules: true, calendarIds: true, exdates: true
};

Dragonfly.JCal.VCalendar.prototype = { };

Dragonfly.JCal.VCalendar.prototype.parse = function (jsob)
{
    var d = Dragonfly;
    var JCal = d.JCal;

    if (jsob) {
        this.jcal = jsob.jcal;
        if ($V(this.jcal.version) != '2.0') {
            logWarning ('Unsupported iCalendar version: ' + $V(this.jcal.version));
        }

        this.bongoId = jsob.bongoId;
        this.timezones = { };
        this.exceptions = { };
        this.calendarIds = jsob.calendarIds;
    }

    for (var i = 0; i < this.jcal.components.length; i++) {
        var comp = this.jcal.components[i];
        switch (comp.type) {
        case 'event':
            var recurid = $V(comp.recurid);
            if (recurid && this.exceptions[recurid]) {
                this.exceptions[recurid].parse (comp);
            } else if (recurid) {
                this.exceptions[recurid] = new JCal.Event (this, comp);
            } else if (this.event) {
                this.event.parse (comp);
            } else {
                this.event = new JCal.Event (this, comp);
            }
            break;
        case 'timezone':
            var timezone = new JCal.Timezone (this, comp);
            this.timezones[timezone.tzid] = timezone;
            break;
        default:
            logWarning ('Ignoring unknown iCalendar type: ' + comp.type);
        }
    }
    this.isRecurring = !!(this.event && this.event.jcal.rrules);

    this.occurrences = [ ];
    var numOccurrences = condGetProp(jsob.occurrences, 'length', -1);
    for (i = 0; i < numOccurrences; i++) {
        this.occurrences.push (new JCal.Occurrence (this, jsob.occurrences[i]));
    }
};

Dragonfly.JCal.VCalendar.prototype.update = function (occurrence, changes, scope)
{
    var JCal = Dragonfly.JCal;
    var event = this.event;
    var jc = event.jcal;

    if (this.isRecurring) {
        if (scope == JCal.instanceScope || occurrence.exception) {
            if (!occurrence.exception) {
                this.makeException (occurrence);
            }
            event = occurrence.exception;
            jc = event.jcal;
        } else if (JCal.allScope) { // make day changes apply to the first event
            this.updateRrules (occurrence, changes);
        }
    } else if (changes.rrules) {
        jc.rrules = changes.rrules;
        this.isRecurring = true;    
    }

    // update start and end times
    this.updateTimes (occurrence, changes, scope);

    // update calendar ids --- these changes apply to all regardless of scope
    if (changes.calendarIds) {
        this.calendarIds = changes.calendarIds;
    }

    // update remaining keys
    for (var key in changes) {
        if (!JCal.VCalendar.SpecialFields[key]) {
            jc[key] = { value: changes[key] };
        }
    }
    
    // update sequence and timestamp
    if (!jc.sequence) {
        jc.sequence = {value: 0};
    } else {
        jc.sequence.value = $V(jc.sequence) + 1;
    }
    jc.dtstamp = { value: JCal.serializeDateTime (new Date()) + 'Z'};

    // update Occurrence
    occurrence.update (changes);
};

Dragonfly.JCal.VCalendar.prototype.updateTimes = function (occurrence, changes, scope)
{
    var JCal = Dragonfly.JCal;
    
    var event = occurrence.exception || this.event;
    var jc = event.jcal;
    
    var wasUntimed = JCal.isUntimed (jc.dtstart);
    var isUntimed = (changes.isUntimed == undefined) ? wasUntimed : changes.isUntimed;

    var startChanged = (changes.startDay || changes.startTime || changes.startTzid || isUntimed != wasUntimed);
    var endChanged = (changes.endDay || changes.endTime || changes.endTzid || isUntimed != wasUntimed);

    if (startChanged || endChanged) {
        if (jc.duration) {
            jc.dtend = JCal.durationToDtend (jc.dtstart, jc.duration);
            delete jc.duration;
        }   
    
        if (startChanged) {
            JCal.updateDateTime (jc.dtstart, changes.startDay, changes.startTime, 
                                changes.isUntimed, changes.startTzid);
        }
        
        // Update exdates
        if (jc.exdates) {
            forEach (jc.exdates, function (date) {
                JCal.updateDateTime (date, null, changes.startTime, changes.isUntimed);
            });
        }
        
        // Update exceptions
        if (event == this.event && scope == JCal.allScope) {
            forEach (this.jcal.components, function (evt) {
                if (evt.recurid) {
                    JCal.updateDateTime (evt.recurid, changes.startDay, changes.startTime, changes.isUntimed);
                }
            });
        }
    
        // Update event end, changing duration-specified events to dtend-specified events
        if (endChanged) {
            var endDay = changes.endDay;
            if (!endDay && isUntimed != wasUntimed) {
                endDay = (wasUntimed)
                    ? JCal.parseDay ($V(jc.dtend)).offsetBy (-1)
                    : JCal.parseDay ($V(jc.dtend));
            } 
            if (endDay && isUntimed) {
                endDay = endDay.offsetBy (1);
            }
            JCal.updateDateTime (jc.dtend, endDay, changes.endTime, isUntimed, changes.endTzid);
        }
    }
}

Dragonfly.JCal.VCalendar.prototype.updateRrules = function (occurrence, changes)
{
    var JCal = Dragonfly.JCal;
    var jc = this.event.jcal;
    if (changes.startDay) {
        var eventStart = JCal.parseDay ($V(jc.dtstart));
        changes.startDay = eventStart.offsetBy (changes.startDay.minus(occurrence.startDay));
    }
    if (changes.endDay) {
        var eventEnd = JCal.parseDay ($V(jc.dtend));
        if (occurrence.isUntimed) {
            eventEnd = eventEnd.offsetBy (-1);
        }
        changes.endDay = eventEnd.offsetBy (changes.endDay.minus(occurrence.endDay));
    }
    
//   } else if (jc.rrules) {
//        forEach (jc.rrules, function (rule) {
//            condDelProp (rule.params, 'x-bongo-until');
//        });
//    }
};

Dragonfly.JCal.VCalendar.prototype.updateRrulesEnd = function (day)
{
    var rrules = this.event.jcal.rrules;
    for (var i = 0; i < rrules.length; i++) {
        var rule = $V(rrules[i]);
        rule.until = day;
        delete rule.count; // we hates these rules anyway
    }
};

Dragonfly.JCal.VCalendar.prototype.makeException = function (occurrence)
{
    var recurid = occurrence.jcal.dtstart;
    var jcal = {
        type: 'event',
        uid: deepclone(this.event.jcal.uid),
        recurid: deepclone (recurid),
        dtstart: deepclone (recurid)
    };
    condSetProp (jcal, 'duration', deepclone (occurrence.jcal.duration));
    condSetProp (jcal, 'dtend', deepclone (occurrence.jcal.dtend));
    condDelProp (jcal.dtend, 'valueLocal');
    
    var exception = new Dragonfly.JCal.Event (this, jcal);
    
    this.jcal.components.push (exception.jcal);
    this.exceptions[$V(recurid)] = exception;
    if (!this.event.jcal.exdates) {
            this.event.jcal.exdates = [ ];
    }
    this.event.jcal.exdates.push (deepclone (recurid));
    occurrence.exception = exception;
};

Dragonfly.JCal.VCalendar.prototype.split = function (occurrence)
{
    var JCal = Dragonfly.JCal;
    
    var sibling = new JCal.VCalendar ({ jcal: deepclone (this.jcal) });
    var changes = { startDay: occurrence.startDay, endDay: occurrence.endDay };
    if (!occurrence.isUntimed) {
        changes.startTime = occurrence.startTime;
        changes.endTime = occurrence.endTime;
    }
    sibling.update (occurrence, changes, JCal.allScope);

    logDebug (serializeJSON (sibling.jcal));
    this.updateRrulesEnd (JCal.serializeDay (occurrence.startDay.offsetBy (-1)));
    return sibling;
};

Dragonfly.JCal.VCalendar.prototype.getLocation = function ()
{
    return (this.bongoId) ? '/user/' + Dragonfly.userName + '/calendar/all/events/-/' + this.bongoId : null;
};

Dragonfly.JCal.VCalendar.prototype.save = function ()
{
    var d = Dragonfly;
    var JCal = d.JCal;
    
    // Check to see if we need to do an RPC to update the rrule until condition
    var dtstart = this.event.jcal.dtstart;
    if (this.isRecurring && !JCal.isUntimed (dtstart) && !JCal.isFloating (dtstart)) {
        var rrules = this.event.jcal.rrules;
        for (var i = 0; i < rrules.length; i++) {
            var rule = $V(rrules[i]);
            if (rule.until && !endsWith(rule.until, 'Z')) { // make the RPC call            
                var updateRuleEnd = function (untilDate) {
                    rule.until = untilDate;
                    return this.save();
                };

                var tzid = dtstart.params.tzid;
                var cmps = (startsWith (tzid, '/bongo')) ? [ ] : [ vcal.timezones[tzid].jcal ];
                var loc = '/user/' + d.userName + '/calendar/all/events';
                return d.requestJSONRPC (
                    'convertTzid', loc, { "components" : cmps }, rule.until + 'T235959',
                    tzid, '/bongo/UTC').addCallback (bind (updateRuleEnd, this));                
            }
        }
    }

    // If all is good, save the event
    if (this.bongoId) {
        return d.requestJSONRPC ('update', this.getLocation(), this.jcal, this.calendarIds);
    } else {
        var loc = '/user/' + d.userName + '/calendar/all/events';
        return d.requestJSONRPC ('createEvent', loc, this.jcal, this.calendarIds).addCallback (
            bind (function (res) {
                      this.bongoId = res.bongoId;
                  }, this));
    }
};

Dragonfly.JCal.VCalendar.prototype.getColor = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    var color;
    var calendar;
    if (c.calendars) {
        calendar = (this.calendarIds)
            ? c.calendars.getByBongoId (this.calendarIds[0])
            : c.calendars.getByIndex (0);
    }
    if (calendar) {
        color = calendar.cal.color;
    }
    return color || d.getRandomColor ();
};

Dragonfly.JCal.Timezone = function (vcalendar, jsob)
{
    this.vcalendar = vcalendar;
    this.tzid = $V(jsob.tzid);
    this.jcal = jsob;
};

Dragonfly.JCal.Event = function (vcalendar, jsob)
{
    this.vcalendar = vcalendar;
    this.parse (jsob);
};

Dragonfly.JCal.Event.prototype = { };

Dragonfly.JCal.Event.prototype.parse = function (jsob)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var j = d.JCal;
    var e = d.JCal.Event;

    this.jcal = jsob;

    if (!$LV(this.jcal.dtstart)) {
        throw new Error ('Event missing start');
    }

    this.summary = $V(this.jcal.summary);
    this.location = $V(this.jcal.location);
    this.isFloating = j.isFloating (this.jcal.dtstart); // Workaround for bug 203351
    this.note = $V(this.jcal.note);
    this.description = $V(this.jcal.description);
    this.color = this.getColor ();
};

Dragonfly.JCal.Event.prototype.getColor = function ()
{
    return this.color || this.vcalendar.getColor ();
};

Dragonfly.JCal.Occurrence = function (vcalendar, jsob)
{
    this.vcalendar = vcalendar;
    this.event = vcalendar.event;
    this.parse (jsob);
};

Dragonfly.JCal.Occurrence.prototype = { };

Dragonfly.JCal.Occurrence.prototype.parse = function (jsob)
{
    var d = Dragonfly;
    var j = d.JCal;

    this.jcal = jsob;

    if (!this.jcal.dtstart) {
        logWarning ('Occurrence missing dtstart');
    }

    this.recurid = $V(this.jcal.recurid) || $LV(this.jcal.dtstart);
    if (this.recurid) {
        this.exception = this.vcalendar.exceptions[this.recurid];
    }

    this.summary = $V(this.getAttr ('summary'));
    this.location = $V(this.getAttr ('location'));
    this.note = $V(this.getAttr ('note'));
    this.description = $V(this.getAttr ('description'));

    this.isUntimed = j.isUntimed (this.jcal.dtstart);
    var duration = j.parseDuration ($V(this.getAttr ('duration')), this.isUntimed);
    this.hasDuration = !!duration;

    if (this.isUntimed) {
        this.startDay = j.parseDay ($LV(this.jcal.dtstart));
        if (this.hasDuration) {
            this.duration = duration;
            this.endDay = this.startDay.offsetBy (this.duration);
        } else {
            this.endDay = j.parseDay ($LV(this.jcal.dtend)).offsetBy (-1);
            this.duration = this.endDay.minus (this.startDay);
            if (this.duration < 0) {
                this.duration = 0;
                this.endDay = this.startDay;
            }
        }
    } else {
        // Workaround for bug 203351
        var localValue = (this.event.isFloating) ? $V : $LV;
        this.startTime = j.parseDateTime (localValue(this.jcal.dtstart));
        this.startTzid = (this.event.isFloating) ?
            '/bongo/floating' :
            condGetProp (this.jcal.dtstart.params, 'tzid', '/bongo/UTC');
        this.startDay = Day.get (this.startTime);
        if (this.hasDuration) {
            this.duration = duration;
            this.endTime = new Date (this.startTime.valueOf() + this.duration);
            this.endTzid = this.startTzid;
        } else {
            this.endTime = j.parseDateTime (localValue(this.jcal.dtend));
            this.duration = this.endTime - this.startTime;
            this.endTzid = (this.event.isFloating) ?
                '/bongo/floating' :
                condGetProp (this.jcal.dtend.params, 'tzid', '/bongo/UTC');
        }
        this.endDay = Day.get (this.endTime);
    }
};

Dragonfly.JCal.Occurrence.prototype.update = function (changes)
{
    for (var key in changes) {
        this[key] = changes[key];
    }
}

Dragonfly.JCal.Occurrence.prototype.getAttr = function (attr)
{
    if (this.jcal[attr]) {
        return this.jcal[attr];
    } else if (this.exception && this.exception.jcal[attr]) {
        return this.exception.jcal[attr];
    } else if (this.event) {
        return this.event.jcal[attr];
    }

};

Dragonfly.JCal.Occurrence.prototype.setAttr = function (attr, value)
{
    if (this.exception && this.exception.jcal[attr]) {
        //logDebug ('exception[' + attr + '] <= ' + serializeJSON (value));
        this.exception.jcal[attr] = value;
    } else if (this.event) {
        //logDebug ('event[' + attr + '] <= ' + serializeJSON (value));
        this.event.jcal[attr] = value;
    }
    if (this.jcal[attr]) {
        this.jcal[attr] = value;
    }
};

Dragonfly.JCal.Occurrence.prototype.getValue = function (attr)
{
    return $V(this.getAttr (attr));
};

Dragonfly.JCal.Occurrence.prototype.setValue = function (attr, value)
{
    this.setAttr (attr, { value: value });
};

Dragonfly.JCal.Occurrence.prototype.setTimezone = function (attr, tzid)
{
    var d = Dragonfly;
    var j = d.JCal;

    var value = this.getAttr (attr);
    if (!value) {
       return;
    }

    value.params = condSetProp (value.params, 'tzid', tzid);
    this.setAttr (attr, value);
};

Dragonfly.JCal.Occurrence.prototype.getColor = function ()
{
    return this.event.getColor ();
};

Dragonfly.JCal.Occurrence.prototype.formatRange = function (timedFormat, untimedFormat)
{
    return (this.isUntimed)
        ? Day.formatRange (untimedFormat || 'd', this.startDay, this.endDay)
        : Dragonfly.formatDateRange (timedFormat || 'g', this.startTime, this.endTime);
};
