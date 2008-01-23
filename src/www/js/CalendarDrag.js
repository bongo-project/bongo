/* -------- CalendarDrag: handle mouse events on calendar views ------------- */

Dragonfly.ThrottledMoveHandler = function (func, origEvent, elem, useCapture)
{
    var d = Dragonfly;
    var t = d.ThrottledMoveHandler;

    var isMoving;
    var origPos;

    if (origEvent) {
        isMoving = false;
        origPos = d.getMousePosition (origEvent);
    } else {
        isMoving = true;
    }

    var eventsIn = 0;
    var eventsOut = 0;
    var timestamp = 0;
    var timeout = 0;
    var pos;

    this.elem = elem || document;
    this.useCapture = elem ? useCapture : true;
    this.handler = (function (evt) {
                        Event.stop (evt);
                        eventsIn++;
                        pos = d.getMousePosition (evt);
                        if (!isMoving) {
                            if (Math.abs (pos.x - origPos.x) < 8 &&
                                Math.abs (pos.y - origPos.y) < 8) {
                                return;
                            }
                            isMoving = true;
                        }

                        var newtime = new Date;
                        if (newtime > timestamp + t.MsecsBetweenUpdates) {
                            if (timeout) {
                                window.clearInterval (timeout);
                                timeout = 0;
                            }
                            eventsOut++;
                            func (pos);
                        } else {
                            if (!timeout) {
                                var lastTimeout = newtime;
                                timeout = window.setInterval (
                                    function () {
                                        eventsOut++;
                                        func (pos);
                                        window.clearInterval (timeout);
                                        timeout = 0;
                                    }, t.MsecsBetweenUpdates);
                            }
                        }
                        timestamp = newtime;
                        eventsIn++;
                    }).bindAsEventListener (null);

    Event.observe (this.elem, 'mousemove', this.handler, this.useCapture);
    //return;

    this.timeout = window.setInterval (
        function () {
            if (eventsIn) {
                console.debug ('events in:', eventsIn, 'events out:', eventsOut);
                eventsIn = eventsOut = 0;
            }
        }, 1000);                      
};

Dragonfly.ThrottledMoveHandler.FramesPerSecond = 30;
Dragonfly.ThrottledMoveHandler.MsecsBetweenUpdates = 1000 / 30;

Dragonfly.ThrottledMoveHandler.prototype = { };
Dragonfly.ThrottledMoveHandler.prototype.stop = function ()
{
    Event.stopObserving (this.elem, 'mousemove', this.handler, this.useCapture);
    if (this.timeout) {
        window.clearInterval (this.timeout);
    }
};

Dragonfly.Calendar.getTimedTableMetrics = function ()
{
    var c = Dragonfly.Calendar;

    if (!c._timedTableMetrics) {
        c._timedTableMetrics = Element.getMetrics ('timed-table');
    }
    return c._timedTableMetrics;
};

Dragonfly.Calendar.getUntimedTableMetrics = function ()
{
    var c = Dragonfly.Calendar;
    
    if (!c._untimedTableMetrics) {
        c._untimedTableMetrics = Element.getMetrics ('untimed-table');
    }
    return c._untimedTableMetrics;
};

Dragonfly.Calendar.isPosUntimed = function (pos)
{
    return pos.y < Dragonfly.Calendar.getTimedTableMetrics().y;
};

Dragonfly.Calendar.getTableMetricsAt = function (pos)
{
    var d = Dragonfly;
    var c = d.Calendar;

    return c.isPosUntimed (pos) ? c.getUntimedTableMetrics() : c._timedTableMetrics;
};

Dragonfly.Calendar.resetTableMetrics = function ()
{
    delete Dragonfly.Calendar._timedTableMetrics;
    delete Dragonfly.Calendar._untimedTableMetrics;
};

Dragonfly.Calendar.getDayBeforePos = function (pos)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var table = c.getTableMetricsAt (pos);

    var percent = (pos.x - table.x) / table.width;
    var dateOffset = c.viewEndDay.minus (c.viewStartDay) + 1;
    return c.viewStartDay.offsetBy (Math.floor (dateOffset * percent));
};

Dragonfly.Calendar.getDayAfterPos = function (pos)
{
    var d = Dragonfly;
    var c = d.Calendar;

    return c.getDayBeforePos (pos).offsetBy (1);
};

Dragonfly.Calendar.getTimeBeforePos = function (pos)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var table = c.getTableMetricsAt (pos);

    var day = c.getDayBeforePos (pos);

    var percent = (pos.y - table.y) / table.height;
    // round to 15 minutes
    var hours = percent * 24;
    var minutes = 15 * (Math.floor (percent * 96) % 4);
    //console.debug ('hours:', Math.floor(hours), 'minutes:', minutes);
    return day.asDateWithTime (hours, minutes, 0);
};

Dragonfly.Calendar.getTimeAfterPos = function (pos)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var date = c.getTimeBeforePos (pos);
    date.setUTCMinutes (date.getUTCMinutes() + 15);
    return date;
};

Dragonfly.Calendar.handleDoubleClick = function (evt) {
    var d = Dragonfly;
    var c = d.Calendar;
    var j = d.JCal;

    if (!c.popup.canHideZone()) {
        return;
    }
    
    var elem = d.findElementWithClass (Event.element (evt), 'buble');
    if (elem) {
        c.popup.edit (evt, elem.occurrence, elem);
        return;
    }

    var pos = d.getMousePosition (evt);
    var daymatch = Event.element(evt).id.match (RegExp('\\d{8}'));
    if (c.viewType == 'column' && c.isPosUntimed (pos)) {
        var vcal = j.buildNewEvent (null, c.getDayBeforePos (pos));
    } else if (daymatch && c.viewType == 'cell') {
        var cellDay = Day.fromIntform (Number(daymatch[0]));
        var vcal = j.buildNewEvent (null, cellDay.asDateWithTime (12));
    } else {
        var dt = c.getTimeBeforePos (pos);
        var vcal = j.buildNewEvent (null, dt);
    }

    c.layoutEvents ([vcal]);
    // TODO: somehow select the first occurrences...
    c.popup.edit (evt, vcal.occurrences[0], vcal.occurrences[0].bubles[0]);
};


Dragonfly.Calendar.handleMousedown = function (evt)
{    
    var c = Dragonfly.Calendar;

    if (!Event.isLeftClick (evt) ||  !c.popup.canHideZone()) {
        return;
    }
    c.popup.hide();
    
    var target = Event.element (evt);
    var buble = Dragonfly.findElementWithClass (target, 'buble');
    if (buble) {
        c.dragstate = {
            buble: buble,
            isResize: Element.hasClassName (target, 'resize'),
            origin: Dragonfly.getMousePosition (evt) 
        };
        Event.observe (document, 'mousemove', c.detectdragHandler);
        Event.observe (document, 'mouseup', c.handleClick);
    }
    Event.stop (evt); // prevents text selection and other default behaviors
};

Dragonfly.Calendar.handleClick = function (evt)
{
    var c = Dragonfly.Calendar;
    var buble = c.dragstate.buble;
    
    Event.stopObserving (document, 'mousemove', c.detectdragHandler);
    Event.stopObserving (document, 'mouseup', c.handleClick);
    c.popup.summarize (buble.occurrence, buble);
    delete c.dragstate; 
};

Dragonfly.Calendar.detectdragHandler = function (evt) 
{
    var d = Dragonfly;
    var c = d.Calendar;
    
    Event.stop (evt);

    var state = c.dragstate;
    var pos = d.getMousePosition (evt);
    if (Math.abs (pos.x - state.origin.x) < 8 && Math.abs (pos.y - state.origin.y) < 8) {
        return;
    }
    // We're dragging - clean up handleMousedown events
    Event.stopObserving (document, 'mousemove', c.detectdragHandler);
    Event.stopObserving (document, 'mouseup', c.handleClick);
    c.popup.hide();

    // initialize drag state
    var occurrence = state.occurrence = state.buble.occurrence;
    state.origin.occurrence = merge (occurrence);
    occurrence.isMoving = true;
    
    if (state.isResize) {
        state.ondrag = c.columnDragResize;
    } else {
        state.ondrag = (d.curLoc.view == 'month') ? c.monthDragMove : c.columnDragMove;
    }
    
    forEach (occurrence.bubles, function (b) { addElementClass (b, 'moving') });
    c.dragHandler (evt);
    Event.observe (document, 'mousemove', c.dragHandler);
    Event.observe (document, 'mouseup', c.dragendHandler);
    Event.observe (document, 'keypress', c.dragendHandler);
};

Dragonfly.Calendar.dragHandler = function (evt) 
{
    var c = Dragonfly.Calendar;
    var state = c.dragstate;
    var pos = Dragonfly.getMousePosition (evt);
    Event.stop (evt);
    if (Dragonfly.curLoc.tab == 'calendar' && 
        Dragonfly.curLoc.view != 'month') {
        var timedTable = c.getTimedTableMetrics ();
        var untimedTable = c.getUntimedTableMetrics ();
        var pos = state.pos = Dragonfly.getMousePosition (evt);
        if (pos.x < untimedTable.x || pos.x > timedTable.x + timedTable.width ||
            pos.y < untimedTable.y || pos.y > timedTable.y + timedTable.height) {
            return;
        }
    }
    state.ondrag (state.occurrence, pos, state.origin);
};

Dragonfly.Calendar.dragendHandler = function (evt) 
{
    var c = Dragonfly.Calendar;
    var JCal = Dragonfly.JCal;
    
    var saveChanges = true;
    if (evt.keyCode == Event.KEY_ESC) {
        saveChanges = false;
    } else if (Dragonfly.curLoc.view == 'month') {
        saveChanges = false; // don't save in month view, for the moment
    } else if (evt.type != 'mouseup') {
        return;
    }
    var state = c.dragstate;
    delete c.dragstate;

    Event.stopObserving (document, 'mousemove', c.dragHandler);
    Event.stopObserving (document, 'mouseup', c.dragendHandler);
    Event.stopObserving (document, 'keypress', c.dragendHandler);

    var occurrence = state.occurrence, origin = state.origin;
    delete occurrence.isMoving;
    forEach (occurrence.bubles, function (b) { removeElementClass (b, 'moving') });    

    if (saveChanges) {
        var changes = {
            startTime: occurrence.startTime, startDay: occurrence.startDay,
            endTime: occurrence.endTime, endDay: occurrence.endDay };

        for (key in changes) { // remove unchanged values
            if (!compare (changes[key], origin.occurrence[key])) {
                delete changes[key];
            }
        }
        
        JCal.saveOccurrence (occurrence, changes, JCal.instanceScope);
    } else {
        occurrence.startTime = origin.occurrence.startTime;
        occurrence.startDay = origin.occurrence.startDay;
        
        occurrence.endTime = origin.occurrence.endTime;
        occurrence.endDay = origin.occurrence.endDay;
        
        occurrence.duration = origin.occurrence.duration;
        occurrence.isUntimed = origin.occurrence.isUntimed;
        c.updateBubles (occurrence);        
    }
    c.layoutEvents();
};

Dragonfly.Calendar.columnDragMove = function (occurrence, pos, origin)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (origin.dayOffset == undefined) {
        if (origin.occurrence.isUntimed) {
            origin.lastDay = c.getDayBeforePos (origin);
            origin.dayOffset = origin.lastDay.minus (origin.occurrence.startDay);
            origin.dayDuration = origin.occurrence.duration;
            
            origin.dateOffset = 60 * 60 * 1000;
            origin.dateDuration = 2 * origin.dateOffset;
            c.updateBubles (occurrence, true);
        } else {
            origin.dateOffset = c.getTimeBeforePos (origin) - origin.occurrence.startTime;
            origin.dateDuration = origin.occurrence.duration;
            
            origin.dayOffset = 0;
            origin.dayDuration = 1;
        }
        //console.debug ('orig.dayOffset:', orig.dayOffset, 'orig.dayDuration:', orig.dayDuration);
        //console.debug ('orig.dateOffset:', orig.dateOffset, 'orig.dateDuration:', orig.dateDuration);
    }
            
    occurrence.isUntimed = c.isPosUntimed (pos);
            
    if (occurrence.isUntimed) {
        var day = c.getDayBeforePos (pos);
        if (!compare (day, origin.lastDay)) {
            return;
        }
        origin.lastDay = day;
        origin.lastDate = null;
        occurrence.startDay = day.offsetBy (-origin.dayOffset);
        occurrence.endDay = occurrence.startDay.offsetBy (origin.dayDuration);
        occurrence.duration = origin.dayDuration;
        delete occurrence.startDate;
        delete occurrence.endDate;
    } else {
        var date = c.getTimeBeforePos (pos);
        if (origin.lastDate && !compare (date, origin.lastDate)) {
            return;
        }
        origin.lastDate = date;
        origin.lastDay = null;
        occurrence.startTime = new Date (date - origin.dateOffset);
        occurrence.startDay  = Day.get (occurrence.startTime);
        
        occurrence.endTime = new Date (occurrence.startTime.valueOf() + origin.dateDuration);
        occurrence.endDay = Day.get (occurrence.endTime);
        occurrence.duration = origin.dateDuration;
    }
    // this should really be occurrence.setRange (start, end)
    c.updateBubles (occurrence, true);
};

Dragonfly.Calendar.columnDragResize = function (occurrence, pos, origin)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (!origin.fixedDay) {
        update (origin, elementPosition (occurrence.bubles[0]));
        origin.fixedDay = origin.occurrence.startDay;
        origin.fixedDate = origin.occurrence.startTime;
        //console.debug ('orig.dayOffset:', orig.dayOffset, 'orig.dayDuration:', orig.dayDuration);
        //console.debug ('orig.dateOffset:', orig.dateOffset, 'orig.dateDuration:', orig.dateDuration);
    }
            
    if (occurrence.isUntimed != c.isPosUntimed (pos)) {
        return;
    }
            
    var day = c.getDayBeforePos (pos);
    var cmp = compare (origin.fixedDay, day);
    var resizeAfter = (cmp < 0) || (cmp == 0 && (occurrence.isUntimed 
                                                 ? (origin.x < pos.x) 
                                                 : (origin.y < pos.y)));
    //console.debug ('day:', day, 'cmp:', cmp, 'resizeAfter:', resizeAfter);
    
    if (occurrence.isUntimed) {
        occurrence.startDay = resizeAfter ? origin.fixedDay : day;
        occurrence.endDay   = resizeAfter ? c.getDayAfterPos(pos) : origin.fixedDay;
        
        occurrence.duration = occurrence.endDay.minus (occurrence.startDay);
    } else {
        occurrence.startTime = resizeAfter ? origin.fixedDate : c.getTimeBeforePos (pos);
        occurrence.endTime   = resizeAfter ? c.getTimeAfterPos (pos) : origin.fixedDate;
        
        occurrence.duration = occurrence.endTime - occurrence.startTime;
        
        occurrence.startDay  = Day.get (occurrence.startTime);
        occurrence.endDay = Day.get (occurrence.endTime);
    }
    // this should really be occurrence.setRange (start, end)
    c.updateBubles (occurrence, true);
};

Dragonfly.Calendar.monthDragMove = function (occurrence, pos, origin)
{
    var state = Dragonfly.Calendar.dragstate;
    state.buble.style.left = (pos.x - origin.x) + 'px';
    state.buble.style.top = (pos.y - origin.y) + 'px';
};
