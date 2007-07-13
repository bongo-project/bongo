//
// ValidatingEntry
//
Dragonfly.ValidatingEntry = function ()
{
    if (arguments.length) {
        this.connect.apply (this, arguments);
    }
};

Dragonfly.ValidatingEntry.prototype = { };
Dragonfly.ValidatingEntry.prototype.select = function ()
{
    this.elem.select ();
};

Dragonfly.ValidatingEntry.prototype._focus = function (evt)
{
    this.select ();
};

Dragonfly.ValidatingEntry.prototype._mouseup = function (evt)
{
    Event.stop (evt);
    this.select ();
};

Dragonfly.ValidatingEntry.prototype._keypress = function (evt)
{
    // this indirection lets us easily override the keypress handler
    // on a per-instance basis, which is helpful for the month entry
    this.handleKeyPress (evt);
    if (this.changeListener) {
        this.changeListener();
    }
};

Dragonfly.ValidatingEntry.prototype.connect = function (elem, value)
{
    this.elem = $(elem);
    this.elem.setAttribute ('autocomplete', 'off');

    Event.observe (this.elem, 'focus', this._focus.bindAsEventListener (this));
    Event.observe (this.elem, 'mouseup', this._mouseup.bindAsEventListener (this));
    // Prototype doesn't actually set the keypress event on IE
    this.elem.onkeypress = this._keypress.bindAsEventListener (this);

    this.setValue (value);
};

Dragonfly.ValidatingEntry.prototype.setChangeListener = function (changeListener)
{
    this.changeListener = changeListener;
};

Dragonfly.ValidatingEntry.prototype.getValue = function ()
{
    return this.elem.value;
};

Dragonfly.ValidatingEntry.prototype.setValue = function (value)
{
    this.elem.value = value;
};

//
// AmPmEntry
//
Dragonfly.AmPmEntry = function (elem, isAm)
{
    this.connect (elem, isAm ? 'AM' : 'PM');
    this.elem.size = '2';
};

Dragonfly.AmPmEntry.prototype = clone (Dragonfly.ValidatingEntry.prototype);
Dragonfly.AmPmEntry.prototype.handleKeyPress = function (evt)
{
    var charcode = evt.charCode || evt.keyCode;
    if (charcode == 8 || charcode == 127) { // gobble backspace/delete
        Event.stop (evt);
    }
    
   // Pass through non-character-generating keyevents on ffx at least
    if (evt.charCode === 0 || evt.ctrlKey || evt.metaKey || evt.altKey || charcode == 9) {
        return;
    }

    Event.stop (evt);
    var s = String.fromCharCode (charcode);
    if (s == 'a' || s == 'A') {
        s = 'AM';
    } else if (s == 'p' || s == 'P') {
        s = 'PM';
    } else {
        return;
    }

    this.setValue (s);
    this.select ();
};

//
// Number Entry
//
Dragonfly.NumberEntry = function (elem, value, min, max, format)
{
    var d = Dragonfly;

    this.format = format || '0';
    this.connect (elem, d.formatNumber (value, this.format));

    this.elem.size = max.toString().length;
    this.modval = Math.pow (10, this.elem.size);
    this.setBounds (min, max);
};

Dragonfly.NumberEntry.prototype = clone (Dragonfly.ValidatingEntry.prototype);
Dragonfly.NumberEntry.prototype._focus = function (evt)
{
    this.newlyFocused = true;
    this.select ();
};

Dragonfly.NumberEntry.prototype.getValue = function ()
{
    return parseInt (this.elem.value, 10);
};

Dragonfly.NumberEntry.prototype.setValue = function (value)
{
    this.elem.value = Dragonfly.formatNumber (value, this.format);
};

Dragonfly.NumberEntry.prototype.handleKeyPress = function (evt)
{
    var charcode = evt.charCode || evt.keyCode;
    if (charcode == 8 || charcode == 127) { // gobble backspace/delete
        Event.stop (evt);
    }
    
   // Pass through non-character-generating keyevents on ffx at least
    if (evt.charCode === 0 || evt.ctrlKey || evt.metaKey || evt.altKey || charcode == 9) {
        return;
    }

    Event.stop (evt);
    var n = parseInt (String.fromCharCode (charcode), 10);
    if (isNaN (n)) {
        //logDebug ('is NaN:', String.fromCharCode (evt.charCode));
        return;
    }

    if (this.newlyFocused) {
        this.newlyFocused = false;
    } else {
        var v = (this.getValue() * 10 + n) % this.modval;
        if (v <= this.max) {
            n = v;
        }
    }
    if (n < this.min) {
        return;
    }
    this.setValue (n);
    this.select ();
};

Dragonfly.NumberEntry.prototype.setBounds = function (min, max)
{
    this.min = min;
    this.max = max;
};

//
// TimeEntry
//
Dragonfly.TimeEntry = function (date, changeListener, parent)
{
    var d = Dragonfly;

    this.date = date instanceof Date ? date : new Date();

    this.id = d.nextId ('time-entry');
    this.hourId = d.nextId ('hour');
    this.minuteId = d.nextId ('minute');
    this.ampmId = d.nextId ('ampm');
    this.changeListener = changeListener;
    

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.TimeEntry.prototype = { };
Dragonfly.TimeEntry.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    html.push (d.format ('<span id="{0}" class="time-entry"><input id="{1}" size="2"> : ' +
                         '<input id="{2}" size="2"> <input id="{3}" size="2"></span>',
                         this.id, this.hourId, this.minuteId, this.ampmId));

    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.TimeEntry.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;
    this.elem = $(this.id);
    
    this.hoursEntry = new d.NumberEntry (this.hourId, (this.date.getUTCHours() + 11) % 12 + 1, 1, 12, '0');
    this.minutesEntry = new d.NumberEntry (this.minuteId, this.date.getUTCMinutes (), 0, 60, '00');
    this.ampmEntry = new d.AmPmEntry (this.ampmId, this.date.getUTCHours() < 12);
    
    if (this.changeListener) {
        this.hoursEntry.setChangeListener (this.changeListener);
        this.minutesEntry.setChangeListener (this.changeListener);
        this.ampmEntry.setChangeListener (this.changeListener);
        delete this.changeListener;
    }
    delete this.date;

    return elem;
};

Dragonfly.TimeEntry.prototype.getTime = function (day)
{
    date = (day) ? day.asDate() : Day.getUTCToday ();

    var hours = this.hoursEntry.getValue() % 12;
    if (this.ampmEntry.getValue().charAt(0) == 'P') {
        hours += 12;
    }
    date.setUTCHours (hours, this.minutesEntry.getValue (), 0, 0);
    return date;
};

Dragonfly.TimeEntry.prototype.setTime = function (date)
{
    if ($(this.hourId)) {
        this.hoursEntry.setValue ((date.getUTCHours() + 11) % 12 + 1);
        this.minutesEntry.setValue (date.getUTCMinutes());
        this.ampmEntry.setValue (date.getUTCHours() < 12 ? 'AM' : 'PM');
    } else {
        this.date = date;
    }
};

//
// DayEntry
//
Dragonfly.DayEntry = function (dayOrDate, changeListener, parent)
{
    var d = Dragonfly;

    this.date = (dayOrDate instanceof Day) ? dayOrDate.asDate() : new Date (dayOrDate);
    this.fixedDate = new Date (this.date);

    this.id = d.nextId ('day-entry');
    this.monthId = d.nextId ('month');
    this.dayId = d.nextId ('day');
    this.yearId = d.nextId ('year');
    this.changeListener = changeListener;

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.DayEntry.prototype = { };
Dragonfly.DayEntry.prototype.handleNumberKeyPress = Dragonfly.NumberEntry.prototype.handleKeyPress;

Dragonfly.DayEntry.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    html.push (d.format ('<span id="{0}" class="day-entry"><input id="{1}" size="2"> / ' +
                         '<input id="{2}" size="2"> / <input id="{3}" size="2"></span>',
                         this.id, this.monthId, this.dayId, this.yearId));

    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.DayEntry.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;
 
    this.elem = $(this.id);
    
    this.monthEntry = new d.NumberEntry (this.monthId, 1, 1, 12, '00');
    this.dayEntry = new d.NumberEntry (this.dayId, 1, 1, 31, '00');
    this.yearEntry = new d.NumberEntry (this.yearId, 0, 0, 99, '00');
    
    this.monthEntry.handleKeyPress = bind ('handleMonthKeyPress', this);
    this.yearEntry.handleKeyPress = bind ('handleYearKeyPress', this);
    this.updateEntries ();
    
    if (this.changeListener) {
        this.monthEntry.setChangeListener (this.changeListener);
        this.dayEntry.setChangeListener (this.changeListener);
        this.yearEntry.setChangeListener (this.changeListener);
        delete this.changeListener;
    }
    return elem;
};

Dragonfly.DayEntry.prototype.updateEntries = function ()
{
    this.monthEntry.setValue (this.date.getUTCMonth() + 1);
    this.dayEntry.setValue (this.date.getUTCDate());
    this.yearEntry.setValue (this.date.getUTCFullYear () % 100);
    this.updateDayBounds ();
};

Dragonfly.DayEntry.prototype.getDay = function ()
{
    return Day.get (this.getDate());
};

Dragonfly.DayEntry.prototype.setDay = function (day)
{
    this.setDate (day.asDate());
};

Dragonfly.DayEntry.prototype.getDate = function ()
{
    this.date.setUTCDate (this.dayEntry.getValue());
    return new Date (this.date);
};

Dragonfly.DayEntry.prototype.setDate = function (date)
{
    this.date = new Date (date);
    this.fixedDate = new Date (this.date);
    this.updateEntries ();
};

Dragonfly.DayEntry.prototype.updateDayBounds = function ()
{
    var date = new Date (this.date);

    date.setUTCMonth (this.date.getUTCMonth() + 1);
    date.setUTCDate (0);
    
    this.dayEntry.setBounds (1, date.getUTCDate ());
};

Dragonfly.DayEntry.prototype.handleMonthKeyPress = function (evt)
{
    this.handleNumberKeyPress.call (this.monthEntry, evt);
    if (this.monthEntry.getValue() - 1 == this.date.getUTCMonth ()) {
        return;
    }

    this.date.setUTCDate (this.dayEntry.getValue());
    this.date.setUTCMonth (this.monthEntry.getValue() - 1);

    this.updateEntries ();

    this.monthEntry.select ();
};

Dragonfly.DayEntry.prototype.handleYearKeyPress = function (evt)
{
    this.handleNumberKeyPress.call (this.yearEntry, evt);
    if (this.yearEntry.getValue() == this.date.getFullYear () % 100) {
        return;
    }
    this.date.setUTCDate (this.dayEntry.getValue());

    var year = this.fixedDate.getUTCFullYear();
    var decade = this.yearEntry.getValue();
    var lower = (year % 100 < decade) ?
        Math.floor (year/100) * 100 + decade - 100 :
        Math.floor (year/100) * 100 + decade;
    var upper = lower + 100;
    year = (year - lower < upper - year) ? lower : upper;

    this.date.setUTCFullYear (year);

    this.updateEntries ();
    
    this.yearEntry.select ();
};

//
// DateRangeEntry
//
Dragonfly.DateRangeEntry = function (occurrence, parent)
{
    var d = Dragonfly;
    var JCal = d.JCal;
    var o = occurrence;

    this.id = d.nextId ('date-range-entry');
    this.startTimeRowId = d.nextId ('date-range-startTime');
    this.endTimeRowId = d.nextId ('date-range-endTime');

    var startDay, startTime, startTzid, endDay, endTime, endTzid;
    var startDay = JCal.parseDay ($V(o.jcal.dtstart));
    var endDay = JCal.parseDay ($V(o.jcal.dtend));

    this.isUntimed = (o.startTime == undefined);
    if (this.isUntimed) {
        startTime = Day.getUTCToday (12);
        endTime = Day.getUTCToday (13);
        endDay = endDay.offsetBy (-1); // because we pulled endDay from jcal
    } else {
        startTime = JCal.parseDateTime ($V(o.jcal.dtstart)); 
        endTime = JCal.parseDateTime ($V(o.jcal.dtend));
    }
    var updateDuration = bind ('updateDuration', this);
    var maintainDuration = bind ('maintainDuration', this);

    this.startDayEntry = new d.DayEntry (startDay, maintainDuration);
    this.startTimeEntry = new d.TimeEntry (startTime, maintainDuration);
    this.startTzidEntry = new d.TzSelector (this.isUntimed ? 
        d.getCurrentTzid() : 
        o.startTzid);
    this.endDayEntry = new d.DayEntry (endDay, updateDuration);
    this.endTimeEntry = new d.TimeEntry (endTime, updateDuration);
    this.endTzidEntry = new d.TzSelector (this.isUntimed ? 
        d.getCurrentTzid() : 
        o.endTzid);

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.DateRangeEntry.prototype = { };

Dragonfly.DateRangeEntry.prototype.buildHtml = function (html)
{
    html.push ('<table id="', this.id, '" class="date-range-entry">');

    html.push ('<tr><td class="form-label"><label>', _('Start on:'), '</label></td><td>');
    this.startDayEntry.buildHtml (html);
    html.push ('</td></tr><tr class="time-entry-fields"><td class="form-label"><label>', _('at:'), '</label></td><td>');
    this.startTimeEntry.buildHtml (html);
    html.push (' ');
    this.startTzidEntry.buildHtml (html);
    html.push ('</td></tr>');

    html.push ('<tr><td class="form-label"><label>', _('dateEndOn'), '</label></td><td>');
    this.endDayEntry.buildHtml (html);
    html.push ('</td></tr><tr class="time-entry-fields"><td class="form-label"><label>', _('at:'), '</label></td><td>');
    this.endTimeEntry.buildHtml (html);
    html.push (' ');
    this.endTzidEntry.buildHtml (html);
    html.push ('</td></tr>');

    html.push ('</table>');
    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.DateRangeEntry.prototype.connectHtml = function (elem)
{
    this.startTzidEntry.setChangeListener (bind ('onStartTzidChange', this));
    this.endTzidEntry.setChangeListener (bind ('onEndTzidChange', this));
    this.setUntimed (this.isUntimed);
    this.updateDuration ();
    this.onEndTzidChange ();
    return elem;
};

Dragonfly.DateRangeEntry.prototype.onStartTzidChange = function ()
{
    var timezone = this.startTzidEntry.getTimezone();
    this.endTzidEntry.setTimezone (timezone);
    this.endTzidEntry.setEnabled (timezone.tzid != '/bongo/floating');
}

Dragonfly.DateRangeEntry.prototype.onEndTzidChange = function ()
{
    if (this.isValidatingEnd) {
        return;
    }
    this.isValidatingEnd = true;
    if (this.endTzidEntry.getTzid() == '/bongo/floating') {
        this.startTzidEntry.setTzid ('/bongo/floating');
        this.endTzidEntry.setEnabled (false);
    }
    delete this.isValidatingEnd;
};

Dragonfly.DateRangeEntry.prototype.setUntimed = function (isUntimed)
{
    if (isUntimed) {
        addElementClass (this.id, 'untimed');
    } else {
        removeElementClass (this.id, 'untimed');
    }
    this.isUntimed = isUntimed;
};

Dragonfly.DateRangeEntry.prototype.getUntimed = function ()
{
    return this.isUntimed;
};

Dragonfly.DateRangeEntry.prototype.getStartDay = function ()
{
    return this.startDayEntry.getDay ();
};

Dragonfly.DateRangeEntry.prototype.getStartDate = function ()
{
    return this.startTimeEntry.getTime (this.getStartDay());
};

Dragonfly.DateRangeEntry.prototype.getStartTzid = function ()
{
    return this.startTzidEntry.getTzid();
};

Dragonfly.DateRangeEntry.prototype.getEndTzid = function ()
{
    return this.endTzidEntry.getTzid();
};

Dragonfly.DateRangeEntry.prototype.getEndDay = function ()
{
    return this.endDayEntry.getDay();
};

Dragonfly.DateRangeEntry.prototype.getEndDate = function ()
{
    return this.endTimeEntry.getTime (this.endDayEntry.getDay());
};

Dragonfly.DateRangeEntry.prototype.updateDuration = function ()
{
    this.duration = 
        this.endTimeEntry.getTime (this.endDayEntry.getDay()) -
        this.startTimeEntry.getTime (this.startDayEntry.getDay());
};

Dragonfly.DateRangeEntry.prototype.maintainDuration = function ()
{
    var start = this.startTimeEntry.getTime (this.startDayEntry.getDay());
    var end = new Date (start.valueOf() + this.duration);
    this.endDayEntry.setDate (end);
    this.endTimeEntry.setTime (end);
};
