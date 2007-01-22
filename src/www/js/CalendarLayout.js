// ----- Global Layout Data Structures ----- 
Dragonfly.Calendar.clear = function ()
{
	this.viewElementID = 'calendar';
    this.allEvents = [ ];
};

Dragonfly.Calendar.insertInTable = function (table, buble, day) 
{
    var index = (day || buble.day).intform;
    if (!table[index]) {
        table[index] = [];
    }
    table[index].push (buble);
};

Dragonfly.Calendar.removeFromTable = function (table, buble, day)
{
    var index = (day || buble.day).intform;
    if (!table[index]) {
        return;
    }

    for (var i = 0, bubles = table[index]; i < bubles.length; i++) {
        if (bubles[i] == buble) {
            bubles.splice (i, 1);
            return;
        }
    }
};

/* ---------- CalendarLayout: Lays out event bubles on views ---------------- */

Dragonfly.Calendar.layoutEvents = function (events)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var JCal = d.JCal;

    var id = this.viewElementID;
    var dayID = bind ('dayID', this);

    var prof = new d.Profiler ('Calendar');
    prof.toggle ('layoutEvents');
	// 1: Add events to event list
    if (events && events.length > 0) {
    	events = events[0] instanceof JCal.VCalendar ? events : JCal.parse (events);
		for (var i = 0; i < events.length; i++) {
			this.allEvents.push (events[i]);
		}
	}

	// 2: clear layout tables and insert bubles into tables
    this.untimedConflictTable = [ ];
    this.untimedLayoutTable = [ ];
    this.timedEventTable = [ ];
	for (var i = 0; i < this.allEvents.length; i++) {
		var jcal = this.allEvents[i];
		if (!c.Preferences.isEventVisible (jcal)) {
			continue;
		}
		for (var j = 0, len = jcal.occurrences.length; j < len; j++) {
			 var occurrence = jcal.occurrences[j];
			 if (occurrence.event) { // skip todos etc.
				 this.updateBubles(occurrence);
			 }
		}
	}

	// 3: resolve collisions and insert bubles into DOM
    var bubles, ul, i, n = 0;
    forEach (Day.iter (this.viewStartDay, this.viewEndDay), bind (function (day) {
                 // resolve untimed collisions for each 'week'
                 if (n % this.viewNumCols == 0) {
                     if (this.untimedConflictTable[day.intform]) {
                         this.resolveUntimedCollisions (day);
                     }
                 }

                 // clear old untimed events for this day
                 ul = $(dayID (id, day, 'u'));
                 replaceChildNodes (ul);

                 bubles = this.untimedLayoutTable[day.intform];
                 if (bubles) {
                     for (i = 0; i < bubles.length; i++) {
                         ul.appendChild (bubles[i]);
                     }
                 }
                 ul.appendChild (this.makeSpacer(true));

                 // clear old timed events for this day
                 ul = $(dayID (id, day, 't'));
                 replaceChildNodes (ul);

                 // resolve timed collisions for this day
                 bubles = this.timedEventTable[day.intform];
                 if (bubles) {
                     this.resolveTimedCollisions (day);

                     // resolving timed conflicts reorders the table, so we have to re-get it
                     bubles = this.timedEventTable[day.intform];
                     for (i = 0; i < bubles.length; i++) {
                         ul.appendChild (bubles[i]);
                     }            
                 }

                 n++;
             }, this));

    this.resetTableMetrics ();
    prof.toggle ('layoutEvents');
};

Dragonfly.Calendar.remove = function (event)
{
	this.allEvents.splice (findIdentical (this.allEvents, event), 1);
	this.layoutEvents();
};

Dragonfly.Calendar.resolveTimedCollisions = function (day)
{
    var bubles = this.timedEventTable[day.intform];
    bubles.sort (this.timedComparator);

    for (var i = 0, slots = [ ], conflicts = [ ]; i < bubles.length; i++) {
        var buble = bubles[i];
        for (var si = 0, mi = slots.length, start = true; si < slots.length; si++) {
            if (this.timedOverlap (slots[si], buble)) {
                start = false;
            } else if (si < mi) {
                mi = si;
            }
        }
        if (start) { // clean out the exiting conflict group
            this.layoutTimedBubles (conflicts, slots.length);
            slots = [ ];
            conflicts = [ ];
        }
        slots[mi] = buble;
        buble.slot = mi;
        conflicts.push (buble);
    }    
    this.layoutTimedBubles (conflicts, slots.length); // lay out last group
};

Dragonfly.Calendar.layoutTimedBubles = function (bubles, numSlots)
{
    for (var i = 0; i < bubles.length; i++) {
        var buble = bubles[i];
        buble.numSlots = numSlots;
        buble.style.left = (buble.slot / numSlots * 100) + '%'; 
        buble.style.width = (1 / numSlots * 100) + '%'; 
    }
};

Dragonfly.Calendar.resolveUntimedCollisions = function (rowday)
{
    bubles = this.untimedConflictTable[rowday.intform];
    bubles.sort (this.untimedComparator);
    var slots = [ ];
    var bindex = sindex = 0;

    forEach (Day.iter (rowday, this.viewNumCols), bind (function (day) {
                 delete this.untimedLayoutTable[day.intform];
                 var si = 0; // next free index in slots for current day
                 while (bindex < bubles.length && compare (bubles[bindex].startDay, day) == 0) {
                     var buble = bubles[bindex++];
                     while (si < slots.length && this.untimedOverlap (slots[si], buble)) {
                         this.insertInTable (this.untimedLayoutTable, this.makeSpacer(), day);
                         si++;
                     }
                     slots[si] = buble;
                     buble.slot = si++;
                     this.insertInTable (this.untimedLayoutTable, buble, day);
                 }
                 // pad the day to account for any events coming over from previous days
                 for (var gaps = 0; si < slots.length; si++) {
                     if (compare (slots[si].endDay, day) >= 0) {
                         this.insertInTable (this.untimedLayoutTable, this.makeSpacer(), day);
                         for (; gaps > 0; gaps--) {
                             this.insertInTable (this.untimedLayoutTable, this.makeSpacer(), day);
                         }
                     } else {
                         gaps++;
                     }
                 }
             }, this));
};

Dragonfly.Calendar.makeSpacer = function (dragHolder)
{
    var spacer = document.createElement ('li');
    Element.setHTML (spacer, '&nbsp;');
    addElementClass (spacer, 'spacer');
    if (dragHolder) {
        addElementClass (spacer, 'dragholder');
    }
    return spacer;
};

Dragonfly.Calendar.mixedComparator = function (a, b)
{
    var c = Dragonfly.Calendar;
    if (a.isUntimed == b.isUntimed) {
        return a.isUntimed ? c.untimedComparator (a, b) : c.timedComparator (a, b);
    } else if (a.startDay.intform == b.startDay.intform) {
        return a.isUntimed ? -1 : 1;
    } else {
        return a.startDay.intform < b.startDay.intform ? -1 : 1;
    }
};

Dragonfly.Calendar.timedComparator = function (a, b) 
{
    if (compare (a.startTime, b.startTime)) {
        return (a.startTime < b.startTime) ? -1 : 1;
    } else if (a.endTime != b.endTime) {
        return (a.endTime > b.endTime) ? -1 : 1;
    } else {
        return 0;
    }
};

Dragonfly.Calendar.untimedComparator = function (a, b)
{
    if (a.startDay.intform != b.startDay.intform) {
        return (a.startDay.intform < b.startDay.intform) ? -1 : 1;
    } else if (a.endDay.intform != b.endDay.intform) {
        return (a.endDay.intform > b.endDay.intform) ? -1 : 1;
    }
    return 0;
};

Dragonfly.Calendar.timedOverlap = function (a, b)
{
    var x = compare (a.endTime, b.startTime), y = compare (b.endTime, a.startTime);
    if (x == 0 || y == 0) {
        return compare (a.startTime, b.startTime) == 0;
    } else {
        return (x >= 0 && y >= 0);
    }
};

Dragonfly.Calendar.untimedOverlap = function (a, b)
{
    return ((compare (a.startDay, b.endDay) <= 0) &&
            (compare (a.endDay, b.startDay) >= 0));
};