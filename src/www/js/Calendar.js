Dragonfly.Calendar = { defaultSet: 'all', defaultHandler: 'events' };

/* -------- Controls -------- */
Dragonfly.tabs['calendar'] = Dragonfly.Calendar;

Dragonfly.Calendar.handlers = { };
Dragonfly.Calendar.views = { };
Dragonfly.Calendar.sets = [ 'all', 'personal', 'shared', 'published', 'subscribed' ];
Dragonfly.Calendar.setLabels = {
    'all':        'All',
    'personal':   'Personal',
    'shared':     'Shared',
    'published':  'Published',
    'subscribed': 'Subscribed'
};

Dragonfly.Calendar.handleResize = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    c.resetTableMetrics ();
    if (c.popup.isVisible()) {
        if (c.popup.showAppropriate) {
            c.popup.showAppropriate();
        } else {
            c.popup.showVertical ();
        }
    }
    
    if (c.viewType == 'column') {
        c.resizeColumns();
    }
};

Dragonfly.Calendar.resizeColumns = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    var sheet = c.getColumnSheet();
    if (!sheet) {
        return;
    }
        
    var selectors = [ '.timestrip',  '.calstripes', 'table.columnview ul.timed-events' ];
    var footerSize = Element.getDimensions ('calendar-footer');
    var declaration = 'height: ' + (d.getScrolledViewSize ('calendar-wrapper') - footerSize.height) + 'px';

    if (sheet.deleteRule) {
        if (sheet.cssRules.length) {
            sheet.deleteRule (0);
        }
        sheet.insertRule (selectors.join (', ') + '{' + declaration + ';}', 0);
    } else {
        while (sheet.rules.length) {
            sheet.removeRule(0);
        }
        for (var i = 0; i < selectors.length; i++) {
            sheet.addRule (selectors[i], declaration, -1);
        }
    }
};

Dragonfly.Calendar.getColumnSheet = function ()
{
    var d = Dragonfly;
    var id = 'calendar-column-sheet';

    if (!$(id)) {
        var sheet = createDOM ('style', { id: id, title: id, type: 'text/css' });
        document.documentElement.getElementsByTagName ('HEAD')[0].appendChild (sheet);
    }
    for (var i = document.styleSheets.length - 1; i >= 0; --i) {
        var sheet = document.styleSheets[i];
        if (sheet.title == id) {
            return sheet;
        }
    }
};

Dragonfly.Calendar.niceDate = function (day, format)
{
    if (day instanceof Date) {
        day = Day.get (day);
    }
    var today = Day.get();

    switch (today.minus (day)) {
    case 1:
        return _('Yesterday');
    case 0:
        return _('Today');
    case -1:
        return _('Tomorrow');
    }

    if (!format) {
        format = 'MMMM d';
    }
    if (day.year != today.year) {
        format += ', yyyy';
    }
    return day.format (format);
};

Dragonfly.Calendar.niceTime = function (time, hideMinutes)
{
    return Dragonfly.formatDate (time, 't');
};

Dragonfly.Calendar.postQuickEvent = function (text)
{
    var d = Dragonfly;
    var loc = new d.Location ({ tab: 'calendar', set: 'personal', view: 'week' });
    return d.requestJSON ('POST', loc, { method: 'parseCommand', command: text });
}

Dragonfly.Calendar.newEvent = function (evt)
{
    var smallMode = document.body.className.indexOf ('low-res') != -1;
    Dragonfly.Calendar.popup.quickEventEdit (smallMode ? 'new-event-href-alt' : 'new-event-href');
};

Dragonfly.Calendar.newCalendar = function (subscription)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var name = 'New Calendar';

    if (subscription) {
        var pop = new c.NewSubscriptionPopup (name);
        pop.showVertical ('sub-calendar-href');
    } else {
        d.notify ('Creating "' + d.escapeHTML (name) + '"...', true);
        var calendar = new c.BongoCalendar (null, { name: name, color: d.getRandomColor() });
        calendar.create().addCallbacks (
            function (res) {
                d.clearNotify ();
                c.setSelectedCalendarId (calendar.bongoId);
                (new c.CalendarPropsPopup (calendar)).edit();
                return res;
            },
            function (err) {
                if (!(err instanceof CancelledError)) {
                    d.notifyError ('Could not create a new calendar', err);
                    logDebug ('error creating calendar:', d.reprRequestError (err));
                }
                return err;
            });
    }
};

Dragonfly.Calendar.loadCalendars = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (c.calendarsDef) {
        return c.calendarsDef;
    } else if (c.calendars) {
        return succeed();
    }

    var calLoc = new d.Location ({ tab: 'calendar', handler: 'calendars' });
    c.calendarsDef = d.requestJSON ('GET', calLoc).addCallback (
        function (jsob) {
            delete c.calendarsDef;
            if (c.calendars) {
                c.calendars.load (jsob);
            } else {
                c.calendars = new c.Collection (jsob);
            }
            Element.setHTML ('calendar-list', '');
            forEach (c.calendars, c.calendarAdded);
            Element.addClassName ($('calendar-list').getElementsByTagName ('LI')[0], 'selected');
        });

    return c.calendarsDef
};

Dragonfly.Calendar.resetCalendars = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (c.calendarsDef) {
        c.calendarsDef.cancel ();
    }
    delete c.calendarsDef;
    if (c.calendars) {
        c.calendars.clear();
    }
    Element.setHTML ('calendar-list', '');
    delete c.selectedCalendarId;
};

Dragonfly.Calendar.setSelectedCalendarId = function (bongoId)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var lis = $('calendar-list').getElementsByTagName ('LI');
    for (var i = 0; i < lis.length; i++) {
        if (lis[i].getAttribute ('bongoid') == bongoId) {
            Element.addClassName (lis[i], 'selected');
        } else {
            Element.removeClassName (lis[i], 'selected');
        }
    }
    c.selectedCalendarId = bongoId;
};

Dragonfly.Calendar.setSelectedCalendar = function (calendar)
{
    return Dragonfly.Calendar.setSelectedCalendarId (calendar.bongoId);
};

Dragonfly.Calendar.getSelectedCalendarId = function ()
{
    return Dragonfly.Calendar.selectedCalendarId;
};

Dragonfly.Calendar.getSelectedCalendar = function ()
{
    var c = Dragonfly.Calendar;
    var bongoId = c.getSelectedCalendarId ();
    return bongoId && c.calendars.getByBongoId (bongoId);
};

Dragonfly.Calendar.Events = { };
Dragonfly.Calendar.handlers['events'] = Dragonfly.Calendar.Events;

Dragonfly.Calendar.Events.parseArgs = function (loc, args)
{
    if (!args) {
        return;
    }

    var arg = args[0];
    if (arg[0] == '-') {
        return;
    }
    var matches = arg.match (/^page(\d*)$/);
    if (matches) {
        loc.page = parseInt (matches[1]);
    }
};

Dragonfly.Calendar.Events.getArgs = function (loc)
{
    return [ loc.page ? 'page' + loc.page : '-' ];
};

Dragonfly.Calendar.Events.parsePath = function (loc, path)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var today = Day.get();

    var tz = path.shift ();

    if (!tz || tz == '-') {
        loc.timezone = null;
        loc.view = c.Preferences.getDefaultView ();
    } else if ({ 'day': true, 'upcoming': true, 'week': true, 'month': true }[tz]) {
        loc.timezone = null;
        loc.view = tz;
    } else {
        loc.timezone = decodeURIComponent(tz);
        loc.view = path.shift();
        if (loc.view == '-') {
            loc.view = c.Preferences.getDefaultView ();
        }
    }

    if (!loc.page) {
        var year = path.shift();
        var month;
        var day;

        if (!year || year == 'today') {
            loc.today = true;
            loc.day = today;
        } else {
            loc.today = false;
            year = parseInt (year);
            month = parseInt (path.shift() || (year == today.year ? today.month + 1 : 1));
            day = parseInt (path.shift() || (year == today.year &&
                                             month == today.month ? today.date : 1));
            loc.day = Day.get (year, month - 1, day);
        }
    }

    loc.event = path.shift();

    /* bless this mess */
    loc.valid = true;
    return loc;
};

Dragonfly.Calendar.Events.getServerUrl = function (loc, path)
{
    var c = Dragonfly.Calendar;
    
    if (loc.event) {
        path.push (loc.event);
    } else if (loc.day) {
        path.push (loc.view || '-', loc.day.year, loc.day.month + 1, loc.day.date);
    }
    return false;
};

Dragonfly.Calendar.Events.getClientUrl = function (loc, path)
{
    if (loc.timezone) {
        path.push (encodeURIComponent(loc.timezone));
    }
    
    if (loc.view) {
        path.push (loc.view);
    }
    
    if (loc.today) {
        path.push ('today');
    } else if (loc.day) {
        path.push (loc.day.year, loc.day.month + 1, loc.day.date);
    }
    if (loc.event) {
        path.push (loc.event);
    }

    return loc;
};

Dragonfly.Calendar.Events.build = function (loc)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (!$('calendar-view-day')) {
        Element.setHTML (
            'toolbar', [
                '<label>', _('View:'), ' </label>',
                '<a id="calendar-view-day" href="#">', _('Day'), '</a>',
                '<span class="bar"> | </span>',
                '<a id="calendar-view-upcoming" href="#">', _('Upcoming'), '</a>',
                '<span class="bar"> | </span>',
                '<a id="calendar-view-week" href="#">', _('Week'), '</a>',
                '<span class="bar"> | </span>',
                '<a id="calendar-view-month" href="#">', _('Month'), '</a>'
                ]);        
        Element.show ('toolbar');
    } else {
        c.popup.hide ();
    }
    
	var views = [ 'day', 'upcoming', 'week', 'month' ];
	for (var i = 0, tmploc = new d.Location (loc); i < 4; i++) {
		tmploc.view = views[i];
		var elem = $('calendar-view-' + tmploc.view);
		elem.href = '#' + tmploc.getClientUrl();
		if (tmploc.view == loc.view) {
			Element.addClassName (elem, 'selected');
		} else {
			Element.removeClassName (elem, 'selected');
		}
	}

    if (!$('calendar')) {
        Element.setHTML ('content-iframe','<div id="calendar"></div>');
                           
        // this handles 'click' as well
        Event.observe ('calendar', 'mousedown', c.handleMousedown);    

        // Event.observe doesn't seem to work for double click
        $('calendar').ondblclick = c.handleDoubleClick.bindAsEventListener (null);
        c.resetTableMetrics ();
    }

    c.clear();
    var dateBaseUrl =  '#' + loc.getClientUrl('handler') + '/-/day/';

    if (loc.view == 'month') {
        c.setToMonthView (loc, dateBaseUrl);
	} else if (loc.view == 'week') {
        c.setToColumnView (loc, 7, false, dateBaseUrl);
	} else if (loc.view == 'upcoming') {
        c.setToColumnView (loc, 4, true, dateBaseUrl);
	} else if (loc.view == 'day') {
        c.setToColumnView (loc, 1, true, dateBaseUrl);
	} else {
        logError ('Unknown Calendar View: ' + loc.view);
        return fail();
    }

    d.sideCal.showMonth (loc.day);
};

Dragonfly.Calendar.Events.load = function (loc, jsob)
{
    Dragonfly.Calendar.layoutEvents (jsob);
};

Dragonfly.Calendar.Calendars = { };
Dragonfly.Calendar.handlers['calendars'] = Dragonfly.Calendar.Calendars;

Dragonfly.Calendar.Preferences = { };

Dragonfly.Calendar.Preferences.addTimezone = function (name, tzid)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var p = d.Preferences;
    
    c.Preferences.removeTimezone(name);
    p.prefs.calendar.timezones.push ({ "name" : name, "tzid" : tzid});

    p.save();
};

Dragonfly.Calendar.Preferences.removeTimezone = function (name)
{
    var d = Dragonfly;
    var p = d.Preferences;
    var timezones = p.prefs.calendar.timezones;

    for (var i = 0; i < timezones.length; i++) {
        if (timezones[i].name == name) {
            timezones.splice (i, 1);
            break;
        }
    }

    if (d.Calendar.Preferences.getDefaultTimezone () == name) {
        if (timezones.length > 0) {
            d.Calendar.Preferences.setDefaultTimezone (timezones[0].name);
        } else {
            d.Calendar.Preferences.setDefaultTimezone (null);
        }
    }

    p.save();
};

Dragonfly.Calendar.Preferences.getTimezones = function ()
{
    return Dragonfly.Preferences.get ('calendar.timezones');
};

Dragonfly.Calendar.Preferences.setTimezones = function (timezones)
{
    return Dragonfly.Preferences.set ('calendar.timezones', timezones);
};

Dragonfly.Calendar.Preferences.setDefaultTimezone = function (tzid)
{
    return Dragonfly.Preferences.set ('calendar.defaultTimezone', tzid);
};

Dragonfly.Calendar.Preferences.getDefaultTimezone = function ()
{
    return Dragonfly.Preferences.get ('calendar.defaultTimezone');
};

Dragonfly.Calendar.Preferences.setDefaultView = function (view)
{
    return Dragonfly.Preferences.set ('calendar.defaultView', view);
};

Dragonfly.Calendar.Preferences.getDefaultView = function ()
{
    return Dragonfly.Preferences.get ('calendar.defaultView');
};

Dragonfly.Calendar.Preferences.getCalendarVisible = function (calendar)
{
    var disabledCals = Dragonfly.Preferences.get ('calendar.disabledCalendars');
    return !disabledCals [calendar.bongoId || calendar];
};

Dragonfly.Calendar.Preferences.setCalendarVisible = function (calendar, visible)
{
    var d = Dragonfly;
    var p = d.Preferences;

    var bongoId = calendar.bongoId || calendar;
    var cals = p.get ('calendar.disabledCalendars');

    if (visible) {
        delete cals[bongoId];
    } else {
        cals[bongoId] = true;
    }

    p.set ('calendar.disabledCalendars', cals);
};

Dragonfly.Calendar.Preferences.isEventVisible = function (jcal)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var p = c.Preferences;

    var cals = jcal.calendarIds;
    for (var i = 0; i < cals.length; i++) {
        if (p.getCalendarVisible (cals[i])) {
                return true;
        }
    }
    return false;
};
