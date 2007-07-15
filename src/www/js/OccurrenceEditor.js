// The OccurrencePopup manages the interaction for creating and editing an
// event, including confirming the scope of changes
Dragonfly.Calendar.OccurrencePopup = function ()
{
    Dragonfly.PopupBuble.apply (this, arguments);
    addElementClass (this.frameId, 'calendar-popup');
};

Dragonfly.Calendar.OccurrencePopup.prototype = clone (Dragonfly.PopupBuble.prototype);

Dragonfly.Calendar.OccurrencePopup.prototype.dispose =
Dragonfly.Calendar.OccurrencePopup.prototype.disposeZone = function ()
{
    this.hide();
    this.htmlZone.disposeZone();
    this.setElem (null);
    if (this.occurrence && this.occurrence.bubles) {
        forEach (this.occurrence.bubles, function (buble) {
            removeElementClass (buble, 'selected');
            removeElementClass (buble, 'saving');
            removeElementClass (buble, 'deleting');
        });
    }
    delete this.occurrence;
    delete this.changes;
    delete this.changeScope;
    delete this.summary;
    delete this.editor;
    Element.setHTML (this.contentId, ''); // sidestep a Mac firefox rendering glitch
};

Dragonfly.Calendar.OccurrencePopup.prototype.quickEventEdit = function (elem)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.setClosable (true);

    var qeventEntryId = d.nextId ('occurrence-qevent-entry');
    var qeventPostId = d.nextId ('occurrence-qevent-post');
    var editId = d.nextId ('occurrence-edit');

    new d.HtmlBuilder (
        '<p><input autocomplete="off" size="30" class="occurrence-entry" id="', qeventEntryId, '">',
        '<p>', _('Enter free-form text for example:'), '<br>',
        '<i>', _('lunch with Joe tomorrow at noon'), '</i><div class="actions">',
        '<span class="secondary">', _('or,'), ' <a id="', editId, '" >', _('enter details instead'), '</a></span>',
        '<input type="button" value="', _('Add Event'), '" id="', qeventPostId, '" disabled></div>'
    ).set (this.contentId);

    var editDetails = function (evt) {
        this.edit (evt, $(qeventEntryId).value);
    }.bindAsEventListener (this);

    var saveQuickEvent = bind (function (jsob) {
        var JCal = Dragonfly.JCal;        
        var j = jsob.jcal.components[0];
        var summary = $V(j.summary), dtstart = $V(j.dtstart), dtend = $V(j.dtend);

        if (dtstart.slice(9) == '000000' && dtend.slice(9) == '235900') { // Work around bug 185097
            var vcal = JCal.buildNewEvent (summary, JCal.parseDay (dtstart));
            this.changes = { endDay: JCal.parseDay (dtend) };
        } else {
            var vcal = JCal.buildNewEvent (summary, JCal.parseDateTime (dtstart));
            this.changes = { endDay: JCal.parseDay (dtend), endTime: JCal.parseDateTime (dtend) };
        }
        this.occurrence = vcal.occurrences[0];
        this.save();
    }, this);

    var postQuickEvent = function (evt) {
        var qeventPost = Dragonfly.Calendar.postQuickEvent ($(qeventEntryId).value);
        qeventPost.addCallbacks (saveQuickEvent, editDetails);
    }.bindAsEventListener (this);

    var handleKeypress = function (evt) {
        var postText = $(qeventEntryId).value;
        $(qeventPostId).disabled = !postText;
        if (!postText && evt.keyCode == Event.KEY_RETURN) {
            editDetails (evt);
        } else if (evt.keyCode == Event.KEY_RETURN) {
            postQuickEvent (evt);
        }
    }.bindAsEventListener (this);

    Event.observe (qeventEntryId, 'keyup', handleKeypress);
    Event.observe (editId, 'click', editDetails);
    Event.observe (qeventPostId, 'click', postQuickEvent);

    this.showVertical (elem);
    $(qeventEntryId).focus ();
};

Dragonfly.Calendar.OccurrencePopup.prototype.summarize = function (occurrence, elem)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.hide ();
    this.setClosable (true);
    this.occurrence = (occurrence) ? occurrence : this.occurrence;

    var deleteId = d.nextId ('occurrence-delete');
    var editId = d.nextId ('occurrence-edit');

    var html = new d.HtmlBuilder();
    this.summary = new c.OccurrenceSummary (this.occurrence, html);
    html.push ('<div class="actions">',
               '<a id="', deleteId, '" class="secondary">', _('Delete'), '</a>',
               '<a id="', editId, '">', _('Edit'), '</a></div>');
    html.set (this.contentId);

    Event.observe (deleteId, 'click', this.delConfirm.bindAsEventListener (this));
    Event.observe (editId, 'click', this.edit.bindAsEventListener (this));
    this.showAppropriate (elem || this.elem);
};

Dragonfly.Calendar.OccurrencePopup.prototype.edit = function (evt, occurrence, elem)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.hide ();
    this.setClosable (false);
    delete this.changes; // may be set if we're editing after a save but before dispose
    if (occurrence) {
        if (!occurrence.pageX)
        {
            this.occurrence = (typeof occurrence == 'string')
                ? d.JCal.buildNewEvent (occurrence).occurrences[0]
                : occurrence;
        }
        else
        {
            // Rebuild the event?
        }
    } else if (!this.occurrence) {
        this.occurrence = d.JCal.buildNewEvent ().occurrences[0];
    }
    
    this.editor = new c.OccurrenceEditor (this.occurrence);
    if (!this.occurrence.vcalendar.bongoId) { // use new event actions
        this.setForm (this.editor, [    
            { value: _('Cancel'), onclick: 'cancel' },
            { value: _('Save'), onclick: 'save' }
        ]);
    } else { // use existing event actions
        this.setForm (this.editor, [
            { value: _('Delete'), onclick: 'del', secondary: true },       
            { value: _('Cancel'), onclick: 'dispose' },
            { value: _('Save'), onclick: 'save' }
        ]);
    }

    this.showAppropriate (elem || this.elem);
    this.editor.focus ();
    if (evt) {
        Event.stop (evt);
    }
};

Dragonfly.Calendar.OccurrencePopup.prototype.cancel = function ()
{
    var event = this.occurrence.event;
    this.dispose();
    Dragonfly.Calendar.remove (event);
}

Dragonfly.Calendar.OccurrencePopup.prototype.del = function ()
{
    var d = Dragonfly;
    var JCal = d.JCal;
    var occurrence = this.occurrence;
    var changeScope = this.changeScope;

    if (!occurrence.vcalendar.isRecurring) {
        changeScope = JCal.instanceScope;
    } else if (!changeScope) {
        this.getScope (false);
        return;
    }

    d.notify (_('Saving changes...'), true);
    forEach (occurrence.bubles, function (b) { addElementClass (b, 'deleting') });

    JCal.delOccurrence (occurrence, changeScope).addCallbacks (
        bind (function (result) {
            this.dispose(); // deletes this's reference to occurrence
            if (occurrence.vcalendar.isRecurring && changeScope != JCal.allScope) {
                d.go();
            } else {
                d.Calendar.remove (occurrence.event);
            }
            d.notify (_('Changes saved.'));
            return result;
        }, this), bind ('showError', this));
};

Dragonfly.Calendar.OccurrencePopup.prototype.save = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;
    var JCal = Dragonfly.JCal;
    var occurrence = this.occurrence;

    if (!this.changes) {
        this.changes = this.editor ? this.editor.getChanges() : { };
    }

    // Don't bother saving if there are no changes to an existing occurrence
    if (occurrence.vcalendar.bongoId && keys(this.changes).length == 0) {
        this.summarize ();
        this.shouldAutohide = true;
        callLater (2, bind ('autohide', this));
        return;        
    }

    if (!occurrence.vcalendar.isRecurring) {
        this.changeScope = JCal.instanceScope;
    } else if (!this.changeScope) {
        this.getScope (true);
        return;
    }

    d.notify (_('Saving changes...'), true);
    if (occurrence.bubles) {
        forEach (occurrence.bubles, function (b) { 
            addElementClass (b, 'saving') 
        });
    }

    JCal.saveOccurrence (occurrence, this.changes, this.changeScope).addCallbacks (
        bind (function (result) {
            this.hide ();
            d.notify (_('Changes saved.'));
            var tab = condGetProp (d.curLoc, 'tab');
            if (tab && (tab == 'calendar' || tab == 'summary')) {
                if (occurrence.vcalendar.isRecurring) {
                    d.go ();
                } else {
                    c.updateBubles (occurrence);
                    c.layoutEvents ();
                }
            } else {
                // show in sidecal!
                logDebug('Show in sidecal!');
            }
            return result;
        }, this), bind ('showError', this));
        
    if (!occurrence.vcalendar.isRecurring) {
       this.summarize ();
       this.shouldAutohide = true;
       callLater (2, bind ('autohide', this));
    }
};

Dragonfly.Calendar.OccurrencePopup.prototype.delConfirm = function ()
{
    if (this.occurrence.vcalendar.isRecurring) {
        this.getScope (false);
        return;
    }

    this.hide ();
    var text = ['<p>', _('Are you sure you want to delete the event'),
                '&ldquo;', Dragonfly.escapeHTML (this.occurrence.summary), '&rdquo;',
                _('? This cannot be undone.'), '</p>'];
    var actions = [{ value: _('Cancel'), onclick: 'dispose'}, { value: _('Delete'), onclick: 'del'}];
    this.setForm (text, actions);
    this.show();
};

Dragonfly.Calendar.OccurrencePopup.prototype.getScope = function (forSave)
{
    this.hide ();
    this.scopeForm = Dragonfly.nextId ('occurrence-scope');
    var form = [
        '<p>', _('This event is part of a series. Would you like'), ' ',  
        (forSave) ? _('changes to apply to') : _('to delete'), ':</p>',
        '<form id="', this.scopeForm, '">',
        '<label><input type="radio" name="scope" checked>', _('only this event'), '</label><br>',
        '<label><input type="radio" name="scope" ', (forSave) ? 'disabled' : '', '>', _('this and following events'), '</label><br>',
        '<label><input type="radio" name="scope">', _('all events in series'), '</label>',
        '</form>'];
    var actions = [
        { value: _('Cancel'), onclick: 'dispose'},
        { value: forSave ? _('Save') : _('Delete'), onclick: partial (this.dispatchWithScope, forSave) }];
    this.setForm (form, actions);
    this.show();
};

Dragonfly.Calendar.OccurrencePopup.prototype.dispatchWithScope = function (forSave)
{
    var scope = $(this.scopeForm).scope;
    for (var i = 0; i < 3; i++) {
        if (scope[i].checked) {
            this.changeScope = i + 1;
        }
    }
    (forSave ? this.save : this.del)();
}    

Dragonfly.Calendar.OccurrencePopup.prototype.showAppropriate = function (elem)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (!this.occurrence.isUntimed && c.viewType == 'column') {
        elem = $(elem) || this.elem;
        if (parseInt (elem.style.height) > 25) {
            this.showHorizontal (elem);
            return;
        }
    }
    this.showVertical (elem);
};

Dragonfly.Calendar.OccurrencePopup.prototype.addPoppedOut = function ()
{
    if (this.occurrence && this.occurrence.bubles) {
        for (var i = 0; i < this.occurrence.bubles.length; i++) {
            addElementClass (this.occurrence.bubles[i], 'popped-out');
        }
    }
};

Dragonfly.Calendar.OccurrencePopup.prototype.removePoppedOut = function ()
{
    if (this.occurrence && this.occurrence.bubles) {
        for (var i = 0; i < this.occurrence.bubles.length; i++) {
            removeElementClass (this.occurrence.bubles[i], 'popped-out');
        }
    }
};

// OccurrenceEditors typically live within an OccurrencePopup, but they are
// stand-alone components as well.
Dragonfly.Calendar.OccurrenceEditor = function (occurrence, parent)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.id = d.nextId ('occurrence-editor');
    this.summaryId = d.nextId ('occurrence-editor-summary');
    this.locationId = d.nextId ('occurrence-editor-location');
    this.alldayId = d.nextId ('occurrence-editor-allday');
    this.noteId = d.nextId ('occurrence-editor-note');
    this.calendarsId = d.nextId ('occurrence-editor-calendars');

    this.notebook = new d.Notebook();

    this.occurrence = occurrence;

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Calendar.OccurrenceEditor.EventTab = 1;
Dragonfly.Calendar.OccurrenceEditor.SharingTab = 2;
Dragonfly.Calendar.OccurrenceEditor.RepeatTab = 3;

Dragonfly.Calendar.OccurrenceEditor.prototype = { };

Dragonfly.Calendar.OccurrenceEditor.prototype.buildEventTab = function (page)
{
    var d = Dragonfly;
    var o = this.occurrence;

    var html = new d.HtmlBuilder (
        '<div><input id="', this.locationId, '" size="40"></div>',
        '<div><label><input id="', this.alldayId, '" type="checkbox"> ', _('all day'), '</label></div>');
    this.dateEntry = new d.DateRangeEntry (o, html);
    html.push ('<textarea id="', this.noteId, '"></textarea>');

    html.set (page);
    return page;
};

Dragonfly.Calendar.OccurrenceEditor.prototype.buildSharingTab = function (page)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var html = new d.HtmlBuilder (
        _('Include this event in:'),
        '<ul id="', this.calendarsId, '" class="scrollv" style="max-height: 8em">');

    var calIds = this.occurrence.vcalendar.calendarIds;
    var cal = c.calendars.getByBongoId (calIds[0]);

    forEach (c.calendars, function (calendar) {
                 logDebug (cal.name, '(' + cal.bongoId + '):', cal.type,
                           calendar.name, '(' + calendar.bongoId + '):', calendar.type);

                 if ((cal.type == 'personal' && calendar.type == 'personal') ||
                     (cal.bongoId == calendar.bongoId)) {
                     html.push ('<li><label><input type="checkbox" bongoid="', calendar.bongoId, '"');
                     for (var i = 0; i < calIds.length; i++) {
                         if (calIds[i] == calendar.bongoId) {
                             html.push (' checked');
                             if (cal.type != 'personal') {
                                 html.push (' disabled');
                             }
                             break;
                         }
                     }
                     html.push ('> ', escapeHTML (calendar.cal.name), '</label></li>');
                 }
             });

    html.push ('</ul>',
               '<div><label><input type="checkbox"> ', _('Publish this event on its own'), '</label></div>',
               '<table cellspacing="0" cellpadding="0">',
               '<tr><td><input type="checkbox"></td><td><input></td></tr>',
               '<tr><td></td><td><input></td></tr>',
               '</table>');

    html.set (page);
    return page;
};

Dragonfly.Calendar.OccurrenceEditor.prototype.buildRepeatTab = function (page)
{
    var rrules = this.occurrence.event.jcal.rrules;
    var day = this.occurrence.startDay;
    this.rruleEditor = new Dragonfly.Calendar.RRuleEditor (page, rrules, day);
    return page;
};

Dragonfly.Calendar.OccurrenceEditor.prototype.buildHtml = function (html)
{
    var d = Dragonfly;
    var c = d.Calendar;

    html.push ('<div><input id="', this.summaryId, '" size="40"></div>');

    this.buildEventTab(this.notebook.insertPage (_('Event'), c.OccurrenceEditor.EventTab - 1));
    this.buildSharingTab(this.notebook.insertPage (_('Sharing'), c.OccurrenceEditor.SharingTab - 1));
    this.buildRepeatTab(this.notebook.insertPage (_('Repeat'), c.OccurrenceEditor.RepeatTab - 1));

    this.notebook.buildHtml (html);

    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.Calendar.OccurrenceEditor.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;
    $(this.alldayId).checked = this.occurrence.isUntimed;
    this.dateEntry.setUntimed (this.occurrence.isUntimed);
    Event.observe (this.alldayId, 'click', function (evt) {
        this.dateEntry.setUntimed ($(this.alldayId).checked);
    }.bindAsEventListener (this));
    d.LabeledEntry (this.summaryId, _('Summary'), this.occurrence.summary);
    d.LabeledEntry (this.locationId, _('Location'), this.occurrence.location);
    d.LabeledEntry (this.noteId, _('Notes'), this.occurrence.note);

    return elem;
};

Dragonfly.Calendar.OccurrenceEditor.prototype.getChanges = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    var changes = new Object();

    changes.isUntimed = this.dateEntry.getUntimed ();
    changes.startDay = this.dateEntry.getStartDay ();
    changes.endDay = this.dateEntry.getEndDay ();
    if (!changes.isUntimed) {
        changes.startTime = this.dateEntry.getStartDate ();
        changes.endTime = this.dateEntry.getEndDate ();
        changes.startTzid = this.dateEntry.getStartTzid ();
        changes.endTzid = this.dateEntry.getEndTzid ();
    }
    
    if (compare (changes.startDay, changes.endDay) == 1 
        || compare (changes.startTime, changes.endTime) == 1) {
            var tmp = changes.startDay;
            changes.startDay = changes.endDay;
            changes.endDay = tmp;
            tmp = changes.startTime;
            changes.startTime = changes.endTime;
            changes.endTime = tmp;
    }

    changes.summary = $(this.summaryId).getValue();
    changes.location = $(this.locationId).getValue();
    changes.note = $(this.noteId).getValue();
    changes.rrules = this.rruleEditor.getValue();
 
    for (key in changes) { // remove unchanged values
        if (!compare (changes[key], this.occurrence[key]) ||
            (changes[key] == '' && !this.occurrence[key])) {
            delete changes[key];
        }
    }
    
    changes.calendarIds = this.getCalendarIds();    
    if (!compare (changes.calendarIds, this.occurrence.vcalendar.calendarIds)) {
        delete changes.calendarIds;
    }

    return changes;
};

Dragonfly.Calendar.OccurrenceEditor.prototype.getCalendarIds = function ()
{
    var inputs = $(this.calendarsId).getElementsByTagName ('INPUT');
    var calIds = [ ];
    for (var i = 0; i < inputs.length; i++) {
        if (inputs[i].checked) {
            calIds.push (inputs[i].getAttribute ('bongoid'));
        }
    }
    if (!calIds.length) {
        calIds.push (Dragonfly.Calendar.calendars.getByIndex(0).bongoId);
    }
    return calIds;
};

Dragonfly.Calendar.OccurrenceEditor.prototype.focus = function ()
{
    $(this.summaryId).blur ();
    $(this.summaryId).focus ();
};

Dragonfly.Calendar.OccurrenceSummary = function (occurrence, parent)
{
    var d = Dragonfly;
    this.occurrence = occurrence;
    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Calendar.OccurrenceSummary.prototype = { };
Dragonfly.Calendar.OccurrenceSummary.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    if (this.occurrence.summary) {
        html.push ('<h3>', d.escapeHTML (this.occurrence.summary), '</h3>');
    }
    if (this.occurrence.location) {
        html.push ('<div class="location">', d.htmlizeText (this.occurrence.location), '</div>');
    }
    html.push ('<div class="date">',
               d.escapeHTML (this.occurrence.formatRange()),
               '</div>');
    if (this.occurrence.description) {
        html.push ('<div class="description">', d.htmlizeText (this.occurrence.description), '</div>');
    }
    if (this.occurrence.note) {
        html.push ('<div class="note">', d.htmlizeText (this.occurrence.note), '</div>');
    }
};

Dragonfly.Calendar.RRuleEditor = function (parent, rrules, day)
{
    this.rrules = rrules; // the rrules we are editing (if any)
    this.day = (day) ? day : Day.get(); // the day to determine defaults
    if (parent) {
        Dragonfly.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Calendar.RRuleEditor.prototype = { };

Dragonfly.Calendar.RRuleEditor.prototype.constants = {
    modes: ['never', 'day', 'week', 'month-by-date', 'month-by-day', 'year'],
    freqLabels: ['Never', 'Daily', 'Weekly', 'Monthly by Date', 'Monthly by Weekday', 'Yearly'],
    freqCodes: [null, 'daily', 'weekly', 'monthly', 'monthly', 'yearly'],
    periodLabels: [null, 'day', 'week', 'month', 'month', 'year'],
    wkdyLabelsS: ['S', 'M', 'T', 'W', 'T', 'F', 'S'],
    wkdyLabelsL: ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'],
    wkdyCodes: ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA'],
    modyLabels: list (range (1, 32)),
    wkLabels: ['The first', 'The second', 'The third', 'The fourth', 'The last'],
    wkCodes: ['1', '2', '3', '4', '-1']
};

Dragonfly.Calendar.RRuleEditor.prototype.buildHtml = function (html)
{
    // Overall layout is a ordered list of controls
    this.id = Dragonfly.nextId ('rrule');    
    this.freqSelectId = Dragonfly.nextId ('rrule-freq');
    html.push ('<ol id="', this.id, '" class="rrule-editor"><li><label>', _('Repeat:'), '</label>');
    html.push ('<select id="', this.freqSelectId, '">');
    Dragonfly.Widgets.addOptions (html, this.constants.freqLabels);
    html.push ('</select></li>');

    // Interval count controls
    this.intervalCountId = Dragonfly.nextId ('rrule-interval-count');
    this.intervalLabelId = Dragonfly.nextId ('rrule-interval-label');
    html.push (
        '<li class="controls day week month-by-day month-by-date year">',
        '<label>', _('Every:'), '</label>',
        '<input size="2" autocomplete="off" id="', this.intervalCountId, '">',
        '<span id="', this.intervalLabelId, '"></span></li>'
    );
    
    // repeat weekly
    html.push ('<li class="controls week"><label>', _('On:'), '</label>');
    html.push ('<div style="display:inline-block; display:-moz-inline-box;">');
    this.wkdyButtons = new Dragonfly.ToggleButtons (html, this.constants.wkdyLabelsS);
    html.push ('</div>');
    
    // repeat monthly by date
    html.push ('</li><li class="controls month-by-date"><label>', _('On:'), '</label>');
    html.push ('<div style="display:inline-block; display:-moz-inline-box;">');
    this.modyButtons = new Dragonfly.ToggleButtons (html, this.constants.modyLabels, 5);
    html.push ('</div>');
    
    // repeat monthly by day
    html.push ('</li><li class="controls month-by-day"><label>', _('On:'), '</label>');
    this.wkSelectId = Dragonfly.nextId ('rrule-week-select');
    this.wkdySelectId = Dragonfly.nextId ('rrule-weekday-select');
    html.push ('<select id="', this.wkSelectId, '">');
    Dragonfly.Widgets.addOptions (html, this.constants.wkLabels);
    html.push ('</select><select id="', this.wkdySelectId, '">');
    Dragonfly.Widgets.addOptions (html, this.constants.wkdyLabelsL);
    html.push ('</select></li>');
       
    // Ending select controls
    this.endSelectId = Dragonfly.nextId ('rrule-end');
    this.endCountId = Dragonfly.nextId ('rrule-end-count');
    this.endLabelId = Dragonfly.nextId ('rrule-end-label');
    html.push (
        '<li class="controls day week month-by-day month-by-date year">',
        '<label>', _('Ending:'), '</label><select id="', this.endSelectId,'">',
        '<option>', _('Never'), '</option><option>', _('On date'), '</option>',
        '<option>', _('After repeating'), '</option></select></li>',

        '<li class="controls end-by-count"><label></label>',
        '<input size="2" autocomplete="off" id="', this.endCountId,'">',
        '<span id="rrule-end-label1"></span></li>',

        '<li class="controls end-by-date"><label></label>'
    );
    this.endDay = new Dragonfly.DayEntry (Day.get(), undefined, html);
    html.push ('</li></ol>');
        
    // Warning for when we can't edit an event's rrules
    this.warnId = Dragonfly.nextId ('rrule-warn');
    this.editAnywayId = Dragonfly.nextId ('rrule-edit-anyway');
    html.push (
        '<div  id="', this.warnId, '" style="display:none;"><p><strong>', _('Caution'), '</strong>',
        '<br>', _('This event has a schedule that cannot be edited with Bongo; choosing to edit will replace the existing schedule.'), '<div class="actions">',
        '<input type="button" id="', this.editAnywayId, '" value="', _('Edit Anyway'), '" disabled></div></div>'
     );
    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.Calendar.RRuleEditor.prototype.connectHtml = function (elem)
{
    this.intervalCount = new Dragonfly.NumberEntry ($(this.intervalCountId), 1, 1, 99);
    Event.observe (this.freqSelectId, 'change', bind ('updateFreq', this));
    Event.observe (this.intervalCountId, 'change', bind ('updateFreq', this));
    this.endCount = new Dragonfly.NumberEntry ($(this.endCountId), 1, 1, 99);
    Event.observe (this.endSelectId, 'change', bind ('updateEnd', this));
    Event.observe (this.endCountId, 'change', bind ('updateEnd', this));

    // when we can't edit existing rules, we bail().  Clicking the edit button
    // will editAnyway(), revealing the editor
    var id = this.id, warnId = this.warnId;
    function bail () {
        delete this.rrules;
        Element.hide (id);
        Element.show (warnId);
        return elem;
    }
    function editAnyway () {
        Element.hide (warnId);
        Element.show (id);
    }   
    Event.observe (this.editAnywayId, 'click', editAnyway);

    if (!this.rrules) {
        return elem;
    } else if (this.rrules) {
        return bail();
    }    
    var rrule = $V (this.rrules[0]);
    var params = this.rrules[0].params;

    // set frequency and interval controls
    var freq = findValue (this.constants.freqCodes, rrule.freq.toLowerCase());
    if (freq == -1) { return bail(); }
    $(this.freqSelectId).options[freq].selected = true;
    this.intervalCount.setValue ((rrule.interval) ? rrule.interval : 1);
    this.updateFreq();
    delete rrule.freq;
    delete rrule.interval;

    if (rrule.until) {
        delete rrule.until;
        this.endDay.setDay (this.day); // fixme
        $(this.endSelectId).options[1].selected = true;
    } else if (rrule.count) {
        this.endCount.setValue (rrule.count);
        $(this.endSelectId).options[2].selected = true;         
        delete rrule.count;
    }
    this.updateEnd();

    var FREQ = this.constants.freqCodes[freq];
    if (FREQ == 'DAILY' && (keys(rrule).length != 0)) {
        return bail();
    } else if (FREQ == 'WEEKLY') {
        return bail();
    } else if (FREQ == 'MONTHLY' && rrule.BYDAY) {
        return bail();
    } else if (FREQ == 'MONTHLY') {
        return bail();
    } else if (FREQ == 'YEARLY') {
        return bail();
    }
    delete this.rrules;
    return elem;
};

Dragonfly.Calendar.RRuleEditor.prototype.updateFreq = function ()
{
    var mode = $(this.freqSelectId).selectedIndex;
    //? findValue (this.constants.freqCodes, freq)
    setElementClass (this.id, 'rrule-editor ' + this.constants.modes[mode]);
    var label = (this.intervalCount.getValue() > 1)
        ? ' ' + this.constants.periodLabels[mode] + 's'
        : ' ' + this.constants.periodLabels[mode];
    Element.setText (this.intervalLabelId, label);
    
    if (!this.rrules) { // don't run when we're editing an existing rule
        var day = this.day.day, date = this.day.date;
        this.wkdyButtons.select (day);
        this.modyButtons.select (date - 1);
        $(this.wkSelectId).options[Math.floor (date/7)].selected = true;
        $(this.wkdySelectId).options[day].selected = true;
    }
    this.updateEnd();
};

// Updates the controls for chosing an ending position
Dragonfly.Calendar.RRuleEditor.prototype.updateEnd = function ()
{
    var mode = $(this.endSelectId).selectedIndex;
    removeElementClass (this.id, 'end-by-date');
    removeElementClass (this.id, 'end-by-count');
    if (mode == 0 || hasElementClass (this.id, 'never')) {
        return;
    }
    addElementClass (this.id, (mode == 1) ? 'end-by-date' : 'end-by-count');
    var label = (this.endCount.getValue() > 1) ? ' times' : ' time';
    Element.setText (this.endLabelId, label);
};

// Generate the rrule from the current controls
Dragonfly.Calendar.RRuleEditor.prototype.getValue = function (isUntimed)
{
    var labels = this.constants;
    var modeIdx = $(this.freqSelectId).selectedIndex;
    var mode = labels.modes[modeIdx];
    if (mode == 'never') {
        return null;
    }
    var rrule = { value: { 
        freq: labels.freqCodes[modeIdx], 
        interval: this.intervalCount.getValue()
    }};

    var endMode = $(this.endSelectId).selectedIndex;
    if (endMode == 1) {
        rrule.value.until = Dragonfly.JCal.serializeDay (this.endDay.getDay());    
    } else if (endMode == 2) {
        rrule.value.count = this.endCount.getValue();    
    }
    
    if (mode == 'week') {
        var daylist = this.wkdyButtons.getSelected();
        for (var i=0; i < daylist.length; i++) {
            daylist[i] = labels.wkdyCodes[daylist[i]];
        }
        rrule.value.byday = daylist;
    } else if (mode == 'month-by-date') {
        var daylist = this.modyButtons.getSelected();
        for (var i=0; i < daylist.length; i++) {
            daylist[i]++; // account for month dates not being zero-indexed
        }
        rrule.value.bymonthday = daylist;
    } else if (mode == 'month-by-day') {
        rrule.value.byday =
            [ labels.wkCodes[$(this.wkSelectId).selectedIndex]
            + labels.wkdyCodes[$(this.wkdySelectId).selectedIndex] ];
    }
    return [rrule];
};

Dragonfly.Calendar.EventInvitationPopup = function (invite, elem)
{
    this.invite = invite;
    Dragonfly.PopupBuble.apply (this, [elem]); // this call results in the popup being built
};

Dragonfly.Calendar.EventInvitationPopup.prototype = clone (Dragonfly.PopupBuble.prototype);

Dragonfly.Calendar.EventInvitationPopup.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;
    var c = d.Calendar;

    d.PopupBuble.prototype.connectHtml.call (this, elem);
    this.calendarId = d.nextId ('invite-calendar');
    var form = [
        '<div id="from">', d.format(_('{0} has invited you to:'), d.escapeHTML (this.invite.from)), '</div><hr>',
        new c.OccurrenceSummary (this.invite.occurrences[0]), '<hr><div>', 
        _('Calendar:'), ' <select id="', this.calendarId, '">'];

    forEach (c.calendars, function (hcal) {
        if (hcal.type == 'personal') {
            form.push ('<option value="', hcal.bongoId, '">');
            form.push (d.escapeHTML (hcal.cal.name), '</option>')
        }
    });
    form.push ('</select></div>');
    
    var actions = [
        { value: _('Delete'), onclick: 'deleteClicked', secondary: true },
        { value: _('Junk'), onclick: 'junkClicked', secondary: true },
        { value: _('Ignore'), onclick: 'ignoreClicked'},
        { value: _('Add'), onclick: 'addClicked'}];
        
    this.setForm (form, actions);
    this.setClosable (true);
    return elem;
};


Dragonfly.Calendar.EventInvitationPopup.prototype.getMessageUrl = function ()
{
    var d = Dragonfly;
    return new d.Location ({ tab: 'mail', set: 'all', conversation: this.invite.convId, message: this.invite.bongoId });
};

Dragonfly.Calendar.EventInvitationPopup.prototype.deleteClicked = function (evt)
{
    Dragonfly.request ('POST', this.getMessageUrl(), { method: 'delete' }).addCallbacks (
        bind ('disposeAndReload', this),
        bind ('showError', this));
};

Dragonfly.Calendar.EventInvitationPopup.prototype.junkClicked = function (evt)
{
    Dragonfly.request ('POST', this.getMessageUrl(), { method: 'junk' }).addCallbacks (
        bind ('disposeAndReload', this),
        bind ('showError', this));
};

Dragonfly.Calendar.EventInvitationPopup.prototype.ignoreClicked = function (evt)
{
    var loc = this.messageLoc || this.getMessageUrl();
    Dragonfly.request ('POST', loc, { method: 'archive' }).addCallbacks (
        bind ('disposeAndReload', this),
        bind ('showError', this));
};

Dragonfly.Calendar.EventInvitationPopup.prototype.addClicked = function (evt)
{
    this.messageLoc = this.getMessageUrl(); // save this so we can archive the message
    delete this.invite.bongoId; // delete this so that the event gets its own id
    this.invite.calendarIds = [ $(this.calendarId).value ];
    this.invite.save().addCallbacks (bind ('ignoreClicked', this), bind ('showError', this));
};
