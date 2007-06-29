/* ------------  CalendarView: builds calendar views ------------------------ */

Dragonfly.Calendar.buildCellView = function (parent, id, day, startDay, endDay, numCols, dateBaseUrl, uids)
{
    var view = SPAN();
    var html = new Dragonfly.HtmlBuilder ('<table id="', id, '" class="calendar monthview cellview">');
    var cal = Dragonfly.Calendar;
    
    forEach (Day.iter (startDay, endDay, numCols), function (weekday) {
         html.push ('<tr>');
         forEach (Day.iter (weekday, numCols), function (cday) {
              html.push ('<td  id="', cal.dayID(id, cday, 'd', uids), '"');
              html.push ((cday.month != day.month) ? ' class="offmonth">' : '>');
              html.push ('<div class="date"><a href="', dateBaseUrl, cday, '">',
                         cal.niceDate (cday, 'dddd, MMMM dd'), '</a></div>',
                         '<ul id="', cal.dayID(id, cday, 'u', uids), '" class="untimed-events"><li></li></ul>',
                         '<ul id="', cal.dayID(id, cday, 't', uids), '" class="timed-events"><li></li></ul></td>');
          });
         html.push ('</tr>');
    });
    html.push ('</table>');
    html.set (view);
    view = view.firstChild;
    appendChildNodes(parent, view);

    var todaytd = $(cal.dayID(id, Day.get(), 'd'));
    if (todaytd) {
        todaytd.className = 'today';
    }
    this.viewStartDay = startDay;
    this.viewEndDay = endDay;
    this.viewNumCols = numCols;
    this.viewElementID = id;
    
    Event.observe (parent, 'mousedown', this.handleMousedown);    

    // Event.observe doesn't seem to work for double click
    $(parent).ondblclick = this.handleDoubleClick.bindAsEventListener (null);
    this.resetTableMetrics ();
    this.viewType = 'cell';
    return view;
};

// Each day on a calendar view (along with timed and untimed event lists) has a 
// unique id composed of the view's container id and the integer form of the day.  
// Suitable codes are 'd', 't', and 'u', respectively.
Dragonfly.Calendar.dayID = function (viewID, day, code, uids)
{
    return (uids) ? [viewID, '-', code, day.intform].join('') : code + day.intform;
};


Dragonfly.Calendar.setToMonthView = function (loc, dateBaseUrl)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var day = loc.day, today = Day.get();
    var startday = this.viewStartDay = day.getMonthViewStart ();
    var endday = this.viewEndDay = day.getMonthViewEnd ();
    this.viewNumCols = 7;

    var title = day.format ('y');

    document.title = title + ': ' + d.title;

    var reldays = c.getRelativeDays (loc);
    var tmpLoc = new d.Location (loc);
    delete tmpLoc.today;

    tmpLoc.day = reldays.prev;
    reldays.prev = '#' + tmpLoc.getClientUrl();
    
    tmpLoc.day = reldays.next;
    reldays.next = '#' + tmpLoc.getClientUrl();

    var html = [
        '<table cellspacing="0" cellpadding="0" class="month-header"><tr>',
        '<td class="month-prev"><a href="', d.escapeHTML (reldays.prev), '"><img src="img/blank.gif" alt="">', _('genericPreviousShort'), '</a></td>',
        '<td class="month-header-title"><h2 class="month-header">', d.escapeHTML (title), '</h2></td>',
        '<td class="month-next"><a href="', d.escapeHTML (reldays.next), '">', _('genericNext'), '<img src="img/blank.gif" alt=""></a></td>',
        '</tr></table>'
        ];
    html.push ('<div id="calendar-wrapper" class="scroll">',
               '<table class="calendar monthview" cellpadding="0" cellspacing="0"><tr>');
    for (var i = 0; i < 7; i++) {
        html.push ('<th>', d.getDayName (i), '</th>');
    }
    html.push ('</tr>');
    forEach (Day.iter (startday, endday, 7), function (weekday) {
                 if (endday.minus (weekday) == 6) {
                     html.push ('<tr class="last">');
                 } else {
                     html.push ('<tr>');
                 }
                 forEach (Day.iter (weekday, 7), function (cday) {
                              var dayClass = [ ];
                              if (cday.month != day.month) {
                                  dayClass.push ('offmonth');
                              }
                              if (cday.day == 6) {
                                  dayClass.push ('last');
                              }
                              if (cday.intform == today.intform) {
                                  dayClass.push ('today');
                              }
                              dayClass = dayClass.length ? ' class="' + dayClass.join (' ') + '"' : '';
                              html.push ('<td', dayClass, ' id="d', cday.intform, '">',
                                         '<div class="date"><a href="', dateBaseUrl, cday.toString(), '">',
                                         cday.date, '</a></div>',
                                         '<ul id="u', cday.intform, '" class="untimed-events"></ul>',
                                         '<ul id="t', cday.intform, '" class = "timed-events"></ul></td>');
                          });
                 html.push ('</tr>');
             });
    html.push ('</table></div>');
    Element.setHTML (this.viewElementID, html);
    this.viewType = 'cell';
};

Dragonfly.Calendar.getRelativeDays = function (loc)
{
    var prevDay, nextDay, stride;
    
    switch (loc.view) {
    case 'month':
        prevDay = loc.day.getPrevMonthStart ();
        nextDay = loc.day.getNextMonthStart ();
        break;
    case 'week':
        stride = 7;
        break;
    case 'upcoming':
    case 'day':
        stride = 1;
        break;
    }

    if (stride) {
        prevDay = loc.day.offsetBy (-stride);
        nextDay = loc.day.offsetBy (stride);
    }

    return { prev: prevDay, next: nextDay };
};

Dragonfly.Calendar.buildColumnLabels = function (html, startday, endday, dateBaseUrl)
{
    var today = Day.get();

    html.push ('<table class="calendar columnview view', endday.minus (startday) + 1, 'days" cellpadding="0" cellspacing="0"><tr>');
    forEach (Day.iter (startday, endday), function (cday) { //column headers
                 var dayClass = [ ];
                 if (cday.intform == endday.intform) {
                     dayClass.push ('last');
                 }
                 if (cday.intform == today.intform) {
                     dayClass.push ('today');
                 }
                 html.push ('<th class="', dayClass.join (' '), '">',
                            '<a href="', dateBaseUrl, cday.toString(), '">',
                            cday.format ('ddd, d'), '</a></th>');
             });
    html.push ('</tr></table>');
};

Dragonfly.Calendar.buildUntimedColumns = function (html, startday, endday)
{
    var today = Day.get();

    html.push ('<table id="untimed-table" class="calendar columnview view', endday.minus (startday) + 1, 'days top" cellpadding="0" cellspacing="0"><tr>');

    forEach (Day.iter (startday, endday), function (cday) { // untimed events
                 var dayClass = [ 'untimed' ];
                 if (cday.intform == endday.intform) {
                     dayClass.push ('last');
                 }
                 if (cday.intform == today.intform) {
                     dayClass.push ('today');
                 }
                 html.push ('<td class="', dayClass.join (' '), '">',
                            '<ul id="u', cday.intform, '" class="untimed-events"><li></li></ul></td>');

             });

    html.push ('</tr></table>');
};

Dragonfly.Calendar.buildTimedColumns = function (html, startday, endday)
{
    var today = Day.get();

    html.push ('<table id="timed-table" class="calendar columnview view', endday.minus (startday) + 1, 'days bottom" cellpadding="0" cellspacing="0"><tr>');

    forEach (Day.iter (startday, endday), function (cday) { // timed events
                 var dayClass = [ ];
                 if (cday.intform == endday.intform) {
                     dayClass.push ('last');
                 }
                 if (cday.intform == today.intform) {
                     dayClass.push ('today');
                 }
                 html.push ('<td id="d', cday.intform, '" class="', dayClass.join (' '), '">',
                            '<ul id="t', cday.intform, '" class="timed-events"></ul></td>');
             });

    html.push ('</tr></table>');
};

Dragonfly.Calendar.buildTimestrip = function (html)
{
    html.push ('<div class="timestrip">',
               '<div class="spacer">&nbsp;</div>',
               '<div class="even first"><span class="timeval">1<span> am</span></span></div>');
    for (var i = 2; i < 12; i += 2) {
        html.push ('<div class="odd"><span class="timeval">', i, '<span> am</span></span></div>',
                   '<div class="even"><span class="timeval">', (i + 1), '<span> am</span></span></div>');
    }
    html.push ('<div class="odd"><span class="timeval">noon</span></div>',
               '<div class="even first"><span class="timeval">1<span> pm</span></span></div>');
    for (var i = 2; i < 12; i += 2) {
        html.push ('<div class="odd"><span class="timeval">', i, '<span> pm</span></span></div>',
                   '<div class="even"><span class="timeval">', (i + 1), '<span> pm</span></span></div>');
    }
    html.push ('<div class="spacer">&nbsp;</div></div>');
};

Dragonfly.Calendar.setToColumnView = function (loc, numDays, isupcoming, dateBaseUrl)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var day = loc.day;
    var startday = this.viewStartDay = (isupcoming) ? day : day.getWeekStart();
    var endday = this.viewEndDay = startday.offsetBy (numDays - 1);
    var today = Day.get();

    this.viewNumCols = numDays;

    function shortDate (day) {
        return c.getMonthName (day.month) + ' ' + day.date;
    }

    var title = Day.formatRange ('D', startday, endday);
    document.title = title + ': ' + d.title;

    var tmpLoc = new d.Location (loc);
    delete tmpLoc.today;
    var reldays = c.getRelativeDays (loc);

    tmpLoc.day = reldays.prev;
    var prev = '<a href="#' + tmpLoc.getClientUrl() + '" class="month-prev"><img src="img/blank.gif" alt="">' + _('genericPreviousShort') + '</a>';

    tmpLoc.day = reldays.next;
    var next = '<a href="#' + tmpLoc.getClientUrl() + '" class="month-next">' + _('genericNext') + '<img src="img/blank.gif" alt=""></a>';

    var html = new d.HtmlBuilder ('<h2 id="calendar-header">', escapeHTML (title), '</h2>',
                                  '<table class="calendarwrapper" cellpadding="0" cellspacing="0">',
                                  '<tr><td class="timestrip-wrapper">', prev, '</td>',
                                  '<td>');

    c.buildColumnLabels (html, startday, endday, dateBaseUrl);

    html.push ('</td><td class="timestrip-wrapper">', next, '</td></tr>',
               '<tr><td>&nbsp;</td><td>');


    c.buildUntimedColumns (html, startday, endday);

    html.push ('</td><td>&nbsp;</td></tr></table>',
               '<div id="calendar-wrapper">',
               '<table cellpadding="0" cellspacing="0" class="calendarwrapper">',
               '<tr><td class="timestrip-wrapper left">');

    c.buildTimestrip (html);

    html.push ('</td><td>',
               '<div class="timedwrapper">',
               '<div class="calstripes">');

    for (var i = 0; i < 12; i++) {
        html.push ('<div id="calstripe' + (i * 2) + '" class="even">&nbsp;</div>',
                   '<div id="calstripe' + (i * 2 + 1) + '" class="odd">&nbsp;</div>');
    }

    html.push ('</div>'); // calstripes

    c.buildTimedColumns (html, startday, endday);

    html.push ('</div>', // timedwrapper
               '</td><td class="timestrip-wrapper right">');

    c.buildTimestrip (html);

    html.push ('</td></tr>');

    html.push ('<tr id="calendar-footer"><td class="timestrip-wrapper">', prev, '</td><td>');
    c.buildColumnLabels (html, startday, endday, dateBaseUrl);
    html.push ('</td><td class="timestrip-wrapper">', next, '</td></tr>');
    
    html.push ('</table></div>');

    html.set (this.viewElementID);
    c.viewType = 'column';
};

Dragonfly.Calendar.getScrolledElement = function (loc)
{
    return 'calendar-wrapper';
};

//
// Bubles represent fractions of occurrences on calendar views
// 
Dragonfly.Calendar.Buble = function (occurrence, day, endDay)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var b = c.Buble;

    var prof = new d.Profiler ('Calendar');
    prof.toggle ('Buble');

    var buble = document.createElement ('li');
    buble.id = d.nextId ('buble');
    
    // this could be some sort of merge() or something
    buble.setDay = b._setDay;
    buble.occurrence = occurrence;

    buble.summary = d.nextId ('summary');
    buble.time = d.nextId ('time');
    buble.note = d.nextId ('note');
    
    var time = occurrence.isUntimed ? '' : d.formatDate (occurrence.startTime, 't');
    Element.setHTML (buble, [
                         '<span class="item">',
                         '<span id="', buble.time, '" class="time">', time, '</span>',
                         '<strong id="', buble.summary, '" class="title">', 
                         d.escapeHTML (occurrence.summary),
                         '</strong>',
                         '<span id="', buble.note, '" class="note">', d.escapeHTML (occurrence.note), '</span>',
                         '<span class="resize"></span>',
                         '</span>'
                         ]);

    prof.toggle ('Buble');
    return buble;
};

Dragonfly.Calendar.Buble._setDay = function (day, endDay, layoutManager)
{
    var d = Dragonfly;
    var c = layoutManager;

    var prof = new d.Profiler ('Calendar');
    prof.toggle ('setDay');

    var isStart = compare (day, this.occurrence.startDay) <= 0;
    var isEnd = this.occurrence.isUntimed ?
        (compare (this.occurrence.endDay, endDay) < 1) :
        !compare (this.occurrence.endDay, day);

    var className = ['buble', this.occurrence.getColor()];
    if (isStart) {
        className.push ('start');
    }
    if (isEnd) {
        className.push ('end');
    }
    if (this.occurrence.isMoving) {
        className.push ('moving');
    }
    this.className = className.join(' ');

    if (this.occurrence.isUntimed) {
        this.startDay = isStart ? this.occurrence.startDay : day;
        this.endDay = isEnd ? this.occurrence.endDay : day.offsetBy (c.viewNumCols-1);
        this.style.top = '';
        this.style.bottom = (this.occurrence.isMoving) ? '0' : '';
        this.style.height = '2em';
        this.style.width = (this.endDay.minus (this.startDay) + 1) + '00%';
        this.style.paddingRight = '0px';
        // delete does not work on nodes in IE
        this.startTime = this.endTime = undefined;
    } else {
        this.startDay = this.endDay = day;
        this.startTime = isStart ? this.occurrence.startTime : day.asDateWithTime (0, 0, 0);
        this.endTime = isEnd ? this.occurrence.endTime : day.asDateWithTime (23, 59, 59);
        this.style.top = Day.secondOfDay (this.startTime) / 864 + '%';
        this.style.height = (this.endTime - this.startTime) / 864000  + '%';
        this.style.width = '100%';
        this.style.left = '';
    }

    var eventTable = this.occurrence.isUntimed ? c.untimedConflictTable : c.timedEventTable;
    if (eventTable != this.eventTable || compare (this.day, this.startDay)) {
        if (this.eventTable) {
            c.removeFromTable (this.eventTable, this, this.day);
        }
        this.eventTable = eventTable;
        c.insertInTable (this.eventTable, this, day);
    }
    this.day = day;
    prof.toggle ('setDay');
};

Dragonfly.Calendar.updateBubles = function (occurrence, reparent)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var prof = new d.Profiler ('Calendar');
    prof.toggle ('updateBubles');

    var iter = occurrence.isUntimed ?
        Day.iter (this.viewStartDay, this.viewEndDay, this.viewNumCols) :
        Day.iter (occurrence.startDay, occurrence.endDay);

    var i = 0;
    var bubles = occurrence.bubles || [ ];
    forEach (iter, bind (function (day) {
                 var endDay;
                 if (occurrence.isUntimed) {
                     endDay = day.offsetBy (this.viewNumCols - 1);
                     if (!this.untimedOverlap (occurrence, { startDay: day, endDay: endDay})) {
                         return; // i.e. continue through iteration
                     }
                 }
                 if (bubles[i]) {
                     Element.setText (bubles[i].summary, occurrence.summary);
                     Element.setText (bubles[i].time,
                                      occurrence.isUntimed ? '' : Dragonfly.formatDate (occurrence.startTime, 't'));
                     Element.setText (bubles[i].note, occurrence.note);
                 } else {
                     bubles[i] = c.Buble (occurrence, day, endDay);
                 }
                 bubles[i].setDay (day, endDay, this);

                 if (reparent && !occurrence.isUntimed) {
                     var p = 't' + bubles[i].day.intform;
                     if ($(p)) {
                         $(p).appendChild (bubles[i]);
                     } else if (bubles[i].parentNode) {
                         removeElement (bubles[i]);
                     }
                 } else if (reparent && occurrence.isUntimed) {
                     var oldParent = bubles[i].parentNode;
                     var newParent = $('u' + bubles[i].startDay.intform);
                     if (oldParent && newParent && oldParent != newParent) {
                        oldParent.replaceChild (this.makeSpacer(true), bubles[i]);
                        var newChildren = newParent.childNodes;
                        var si = newChildren.length;
                        while (si > 0 && hasElementClass (newChildren[si-1], 'dragholder')) {
                            si--;
                        }
                        newParent.replaceChild (bubles[i], newParent.childNodes[si]);
                     }
                 }
                 i++;
             }, this));

    // remove old bubles
    for (var j = i; j < bubles.length; j++) {
        var buble = bubles[j];
        this.removeFromTable (buble.eventTable, buble);
        if (buble.parentNode) {
            removeElement (buble);
        }
    }
    bubles.length = i;
    occurrence.bubles = bubles;
    prof.toggle ('updateBubles');
};
