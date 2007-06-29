// Public CalendarWidget API
Dragonfly.CalendarWidget = function (parent, style, day)
{
    this.style = style;
    this.day = day;
    this.id = Dragonfly.nextId('calendar-widget');
    this.clear();
    
    if (parent) {
        Dragonfly.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.CalendarWidget.prototype = { };

Dragonfly.CalendarWidget.prototype.buildHtml = function (html)
{
    var day = this.day;
    if (this.style == 'popup') {
        this.buildCellView (html, day, day, 1, '');
    } else if (this.style == 'summary') {
        this.buildCellView (html, day, day.offsetBy(6), 2, 'dddd, MMMM');
    } else if (this.style == 'month') {
        this.buildCellView (html, day.getMonthViewStart(), day.getMonthViewEnd(), 7, 'd');
    } else if (this.style == 'week') {
        this.buildColumnView (html, day.getWeekStart(), 7);
    } else if (this.style == 'upcoming') {
        this.buildColumnView (html, day, 4);
    } else if (this.style == 'day') {
        this.buildColumnView (html, day, 1);
    }
    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.CalendarWidget.prototype.connectHtml = function (html)
{
    var c = Dragonfly.Calendar;
    // this handles 'click' as well
    Event.observe (this.id, 'mousedown', c.handleMousedown.bindAsEventListener(null));    

    // Event.observe doesn't seem to work for double click
    $(this.id).ondblclick = c.handleDoubleClick.bindAsEventListener (null);
};

Dragonfly.CalendarWidget.prototype.clear = Dragonfly.Calendar.clear;
Dragonfly.CalendarWidget.prototype.layout = Dragonfly.Calendar.layoutEvents;
Dragonfly.CalendarWidget.prototype.remove = Dragonfly.Calendar.remove;

// Private CalendarWidget API
Dragonfly.CalendarWidget.prototype.buildCellView = function (html, startDay, endDay, numCols, dateformat)
{
    var Loc = Dragonfly.Location;
    var Calendar = Dragonfly.Calendar;
    
    var day = this.day;
    var getDayID = bind ('getDayId', this);
    var urlBase = '#' + new Loc({tab:'calendar', view:'day'}).getClientUrl() + '/';
    
    html.push('<table id="', this.id, '" class="calendar monthview cellview">');
    forEach (Day.iter (startDay, endDay, numCols), function (weekday) {
         html.push ('<tr>');
         forEach (Day.iter (weekday, numCols), function (cday) {
              html.push ('<td  id="', getDayID(cday, 'd'), '"');
              html.push ((cday.month != day.month) ? ' class="offmonth">' : '>');
              html.push ('<div class="date"><a href="', urlBase, cday, '">',
                         Calendar.niceDate (cday, dateformat), '</a></div>',
                         '<ul id="', getDayID(cday, 'u'), '" class="untimed-events"><li></li></ul>',
                         '<ul id="', getDayID(cday, 't'), '" class="timed-events"><li></li></ul></td>');
          });
         html.push ('</tr>');
    });
    html.push ('</table>');
    this.viewStartDay = startDay;
    this.viewEndDay = endDay;
    this.viewNumCols = numCols;
    this.viewElementID = this.id;
    this.viewType = 'cell';
};

Dragonfly.CalendarWidget.prototype.buildColumnView = function (html, startDay, numCols, dateformat, dateBaseUrl)
{
    this.viewStartDay = startDay;
    this.viewEndDay = startDay.offsetBy(numCols);
    this.viewNumCols = numCols;
    this.viewElementID = this.id;
    this.viewType = 'column';
};

// Todo: remove this function once all calendars are switched to CalendarWidgets
Dragonfly.CalendarWidget.prototype.dayID = function (unused, day, code)
{
    return this.getDayId (day, code);
};

// Each day on a calendar Widget (along with timed and untimed event lists) has a 
// unique id composed of the view's id and the integer form of the day.  
// Suitable codes are 'd', 't', and 'u', respectively.
Dragonfly.CalendarWidget.prototype.getDayId = function (day, code)
{
    return [this.id, '-', code, day.intform].join('');
};

Dragonfly.CalendarWidget.prototype.updateBubles = Dragonfly.Calendar.updateBubles;
Dragonfly.CalendarWidget.prototype.deleteBubles = Dragonfly.Calendar.deleteBubles;
Dragonfly.CalendarWidget.prototype.resetTableMetrics = Dragonfly.Calendar.resetTableMetrics;
Dragonfly.CalendarWidget.prototype.insertInTable = Dragonfly.Calendar.insertInTable;
Dragonfly.CalendarWidget.prototype.removeFromTable = Dragonfly.Calendar.removeFromTable;
Dragonfly.CalendarWidget.prototype.resolveTimedCollisions = Dragonfly.Calendar.resolveTimedCollisions;
Dragonfly.CalendarWidget.prototype.layoutTimedBubles = Dragonfly.Calendar.layoutTimedBubles;
Dragonfly.CalendarWidget.prototype.resolveUntimedCollisions = Dragonfly.Calendar.resolveUntimedCollisions;
Dragonfly.CalendarWidget.prototype.makeSpacer = Dragonfly.Calendar.makeSpacer;
Dragonfly.CalendarWidget.prototype.mixedComparator = Dragonfly.Calendar.mixedComparator;
Dragonfly.CalendarWidget.prototype.timedComparator = Dragonfly.Calendar.timedComparator;
Dragonfly.CalendarWidget.prototype.untimedComparator = Dragonfly.Calendar.untimedComparator;
Dragonfly.CalendarWidget.prototype.timedOverlap = Dragonfly.Calendar.timedOverlap;
Dragonfly.CalendarWidget.prototype.untimedOverlap = Dragonfly.Calendar.untimedOverlap;

Dragonfly.Calendar.MonthWidget = function (parent, day)
{
    var d = Dragonfly;

    this.day = day.getMonthStart ();
    this.id = d.nextId ('month-widget');

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Calendar.MonthWidget.prototype = { };
Dragonfly.Calendar.MonthWidget.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    var startTime = new Date();
    var tmpLoc = new d.Location ({ tab: 'calendar', set: 'all', day: this.day });
    if (d.curLoc && d.curLoc.tab == 'calendar') {
        tmpLoc.view = d.curLoc.view;
    } else {
        tmpLoc.view = 'month';
    }

    tmpLoc.day = Day.get();
    var thisMonth = d.escapeHTML (tmpLoc.getClientUrl ());

    tmpLoc.day = this.day.getPrevMonthStart ();
    var prevUrl = d.escapeHTML (tmpLoc.getClientUrl ());

    tmpLoc.day = this.day.getNextMonthStart ();
    var nextUrl = d.escapeHTML (tmpLoc.getClientUrl ());

    html.push ('<div id="', this.id, '">',
               '<table cellspacing="0" cellpadding="0" class="month-widget">',
               '<tr><td id="kludgysidebarleft" class="prev"><a href="#', prevUrl, '">&larr;</a></td>',
               '<th id="kludgysidebartitle" class="header" colspan="5">', this.day.format ('y'), '</th>',
               '<td id="kludgysidebarright" class="next"><a href="#', nextUrl, '">&rarr;</a></td></tr>',
               '<tr></tr>',
               '<tr class="daylabels"><td>', _('sunday-s'),
               '</td><td>', _('monday-s'),
               '</td><td>', _('tuesday-s'),
               '</td><td>', _('wednesday-s'),
               '</td><td>', _('thursday-s'),
               '</td><td>', _('friday-s'),
               '</td><td>', _('saturday-s'),
               '</td></tr>');

    this.headerRows = 3;
    var rows = 0;
    forEach (Day.iter (this.day.getMonthViewStart (),
                       this.day.getMonthViewEnd (), 7),
             function (weekstart) {
                 rows++;
                 html.push ('<tr>');
                 forEach (Day.iter (weekstart, 7),
                          function (weekday) {
                              var text;
                              var visibility;

                              tmpLoc.day = weekday;
                              if (weekday.month == this.day.month) {
                                  text = weekday.date;
                                  visibility = 'visible';
                              } else {
                                  text = '';
                                  visibility = 'hidden';
                              }

                              html.push ('<td><a style="visibility: ', visibility,
                                         '" href="#', d.escapeHTML (tmpLoc.getClientUrl()), '">',
                                         text, '</a></td>');
                          }, this);
                 html.push ('</tr>');
             }, this);

    for (var i = rows + this.headerRows; i < this.headerRows + 6; i++) {
        html.push ('<tr>');
        for (var j = 0; j < 7; j++) {
            html.push ('<td><a href="#">&nbsp;</a></td>');
        }
        html.push ('</tr>');
    }
    html.push ('<tr><td id="kludgysidebartoday" colspan="7" valign="middle" class="todaylink"><p><a href="#', thisMonth, '">', _('showToday'), '</a></td></tr>');
    html.push ('</table>');

    html.push ('</div>');
    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.Calendar.MonthWidget.prototype.connectHtml = function (elem) {
    var d = Dragonfly;
    
    startTime = new Date();
    if (!this.elem) {
        this.elem = $(this.id);
    }
    this.table = this.elem.getElementsByTagName ('TABLE')[0];
    this.rows = this.table.getElementsByTagName ('TR');
    
    this.title = $('kludgysidebartitle');
    
    this.prev = $('kludgysidebarleft').getElementsByTagName ('A')[0];
    this.today = $('kludgysidebartoday').getElementsByTagName ('A')[0];
    this.next = $('kludgysidebarright').getElementsByTagName ('A')[0];
    
    this.today.ignoreClick = true;
    Event.observe (this.today, 'click',
                 (function (evt) {
                     if (d.curLoc && d.curLoc.tab == 'calendar') {
                         d.go (Event.element (evt).href).addCallback (
                             function (res) {
                                 if (d.curLoc.view == 'month' &&
                                     $('d' + d.curLoc.day.intform)) {
                                     $('d' + d.curLoc.day.intform).scrollIntoView (false);
                                 }
                                 return res;
                             });
                     } else {
                         this.showMonth (Day.getUTCToday ());
                     }
                     Event.stop (evt);
                 }).bindAsEventListener (this));
    
    this.prev.ignoreClick = true;
    Event.observe (this.prev, 'click',
                 (function (evt) {
                     this.prevMonth ();
                     Event.stop (evt);
                 }).bindAsEventListener (this));
    
    this.next.ignoreClick = true;
    Event.observe (this.next, 'click',
                 (function (evt) {
                     this.nextMonth ();
                     Event.stop (evt);
                 }).bindAsEventListener (this));
    
    this.as = [ ];
    for (var i = this.headerRows; i < this.rows.length; i++) {
        var week = this.rows[i].getElementsByTagName ('A');
        this.as.push (week);
    }
    
    this._relabel (elem);
    return elem;
};

Dragonfly.Calendar.MonthWidget.prototype._relabel = function (elem)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var i, j;

    var startTime = new Date();
    var today = Day.get();

    var day = this.day.getMonthViewStart ();
    var end = this.day.getMonthViewEnd ();

    delete this.def;

    var tmpLoc = new d.Location ({ tab: 'calendar', set: 'all', day: this.day });
    if (d.curLoc && d.curLoc.tab == 'calendar') {
        tmpLoc.view = d.curLoc.view;
    } else {
        tmpLoc.view = 'month';
    }

    tmpLoc.day = Day.get();
    this.today.href = '#' + tmpLoc.getClientUrl ();

    tmpLoc.day = this.day.getPrevMonthStart ();
    this.prev.href = '#' + tmpLoc.getClientUrl ();

    tmpLoc.day = this.day.getNextMonthStart ();
    this.next.href = '#' + tmpLoc.getClientUrl ();

    for (i = 0; day.intform <= end.intform; i++) {
        for (j = 0; j < 7; j++) {
            var daylink = this.as[i][j];
            if (day.month == this.day.month) {
                Element.setText (daylink, day.date);
                tmpLoc.day = day;
                daylink.href = '#' + tmpLoc.getClientUrl ();
                daylink.day = day;
                daylink.style.visibility = 'visible';
                daylink.ignoreClick = true;
                daylink.popupbuble = $(this.popupbuble);
                daylink.onclick = Dragonfly.Calendar.MonthWidget.showPopup.bindAsEventListener (daylink);
                if (day.intform == today.intform) {
                    daylink.className = 'today';
                } else if (d.curLoc && d.curLoc.tab == 'calendar' &&
                    day.intform >= c.viewStartDay.intform &&
                    day.intform <= c.viewEndDay.intform) {
                    daylink.className = 'visible';
                } else {
                    daylink.className = '';
                }
            } else {
                daylink.style.visibility = 'hidden';
                daylink.className = '';
                Element.setText (daylink, '');
            }
            day = day.offsetBy (1);
        }
        //logDebug ('showing row', i);
        this.rows[i + this.headerRows].style.visibility = 'visible';
    }
    for ( ; i < this.headerRows + 3; i++) {
        //logDebug ('hiding row', i);
        for (j = 0; j < 7; j++) {
            Element.setHTML (this.as[i][j], '&nbsp;');
            this.as[i][j].className = '';
        }
        this.rows[i + this.headerRows].style.visibility = 'hidden';
    }

    return elem;
    //logDebug (d.format ('MonthWidget.relabel: {0}s', (new Date - startTime) / 1000));
};

Dragonfly.Calendar.MonthWidget.showPopup = function (evt) {
    var d = Dragonfly;
    var c = d.Calendar;
    var w = c.MonthWidget;

    if (d.curLoc.tab == 'calendar') {
        d.go (this.href);
    } else if (!w.popup || w.popup.isDisposed() || w.popup.canHideZone()){
        if (w.popup && !w.popup.isDisposed()) {
            w.popup.dispose();
        }
        var loc = new d.Location ({ tab: 'calendar', view: 'day', day: this.day});
        var calwidget = new d.CalendarWidget (null, 'popup', this.day);
        w.popup = new d.PopupBuble (
            Event.element (evt), 
            '<h3>', this.day.format ('dddd, MMMM d'), '</h3>',
            '<div style="width:150px;">', calwidget, '</div>',
            '<p style="text-align:center;">',
            '<a href="#' + loc.getClientUrl() + '">', _('viewInCal'), '</a>'
        );
        w.popup.showBelow ();        
        d.requestJSON ('GET', loc).addCallback (function (jsob) { calwidget.layout (jsob); });
    }
    Event.stop (evt);
};

Dragonfly.Calendar.MonthWidget.prototype.relabel = function ()
{
    Element.setText (this.title, this.day.format ('y'));
    if (this.def) {
        this.def.cancel();
    }
    this.def = callLater (0.200, bind ('_relabel', this));
};

Dragonfly.Calendar.MonthWidget.prototype.showMonth = function (date)
{
    this.day = Day.get (date);
    this.relabel ();
};

Dragonfly.Calendar.MonthWidget.prototype.nextMonth = function ()
{
    this.day = this.day.getNextMonthStart ();
    this.relabel ();
};

Dragonfly.Calendar.MonthWidget.prototype.prevMonth = function ()
{
    this.day = this.day.getPrevMonthStart ();
    this.relabel ();
};

