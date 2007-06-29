Dragonfly.Calendar.BongoCalendar = function (bongoId, jsob)
{
    var d = Dragonfly;

    this.cal = { };

    if (bongoId) {
        this.setId (bongoId);
    }
    if (jsob) {
        this.parseJSON (jsob);
    }
};

Dragonfly.Calendar.BongoCalendar.prototype = { };

Dragonfly.Calendar.BongoCalendar.prototype.setId = function (bongoId)
{
    var d = Dragonfly;

    this.bongoId = bongoId;

    var loc = new d.Location ({ tab: 'calendar', handler: 'calendars' });
    this.serverUrl = loc.getServerUrl (false, '') + '/-/' + this.bongoId;
};

Dragonfly.Calendar.BongoCalendar.prototype.shrink = function ()
{
    this.cal = { name: this.cal.name, color: this.cal.color };
};

Dragonfly.Calendar.BongoCalendar.prototype.setType = function (type)
{
    this.shrink ();
    this.type = type;
};

Dragonfly.Calendar.BongoCalendar.prototype.setPassword = function (username, password)
{
    this.cal.protect = true;
    this.cal.username = username;
    this.cal.password = password;
};

Dragonfly.Calendar.BongoCalendar.prototype.clearPassword = function ()
{
    this.cal.protect = false;
    delete this.cal.username;
    delete this.cal.password;
};

Dragonfly.Calendar.BongoCalendar.prototype.setShared = function (isPublic)
{
    this.cal.publish = isPublic;
    if (isPublic) {
        this.clearPassword ();
    } else {
        delete this.cal.protect;
        delete this.cal.username;
        delete this.cal.password;
    }
};

Dragonfly.Calendar.BongoCalendar.prototype.parseJSON = function (jsob)
{
    var d = Dragonfly;

    function calColor (calname)
    {
        return { 
            'Personal': 'blue', 
            'Red Sox': 'red',
            'US Holidays': 'orange',
            'Holidays': 'orange',
            'The Moon': 'gray',
            'Novell': 'red',
            'openSUSE': 'green',
            'Meetings': 'brown'
        }[calname] || d.getRandomColor();
    }

    this.cal = jsob;

    this.cal.name = jsob.name || '';
    this.cal.color = jsob.color || calColor (jsob.name);

    if (jsob.url) {
        this.type = 'web';
    } else if (jsob.owner) {
        this.type = 'local';
    } else {
        this.type = 'personal';
    }

	this.subscriptionUrl = "/user/ " + d.userName + "/calendar/" + d.escapeHTML(this.cal.name) + "/events";

    /*
    // this should probably trust the server...
    switch (this.type) {
    case 'personal':
        this.cal.publish = jsob.publish;
        if (this.cal.publish) {
            this.cal.protect = jsob.protect;
            this.cal.searchable = jsob.searchable;
            this.cal.username = jsob.username;
        }
        break;
    case 'web':
        this.cal.url = jsob.url;
        this.cal.searchable = jsob.searchable;
        this.cal.username = jsob.username;
        break;
    }
    */
};

Dragonfly.Calendar.BongoCalendar.prototype.refresh = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (this.serverUrl) {
        return d.requestJSON ('GET', this.serverUrl).addCallbacks (
            bind (function (res) {
                      this.parseJSON (res);
                      c.calendarUpdated (this);
                      return this;
                  }, this),
            bind (function (err) {
                      return err;
                  }, this));
    } else {
        return succeed();
    }
};

Dragonfly.Calendar.BongoCalendar.prototype.del = function ()
{
    return Dragonfly.request ('DELETE', this.serverUrl);
};

Dragonfly.Calendar.BongoCalendar.prototype.save = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    return d.request ('PUT', this.serverUrl, this.cal).addCallbacks (
        bind (function (res) {
                  c.calendarUpdated (this);
                  return this;
              }, this),
        bind (function (err) {
                  return err;
              }, this));
};

Dragonfly.Calendar.BongoCalendar.prototype.create = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    var loc = new d.Location ({ tab: 'calendar', handler: 'calendars' });
    return d.requestJSONRPC ('createCalendar', loc, this.cal).addCallbacks (
        bind (function (res) {
                  this.setId (res[0]);
                  this.cal.name = res[1];                         
                  c.calendarAdded (this);
                  return res;
              }, this),
        bind (function (err) {
                  return err;
              }, this));
};

Dragonfly.Calendar.Collection = function (jsob)
{
    if (jsob) {
        this.load (jsob);
    } else {
        this.clear ();
    }
};

Dragonfly.Calendar.Collection.prototype = { };
Dragonfly.Calendar.Collection.prototype.getByBongoId = function (bongoId)
{
    return this._byId[bongoId];
};

Dragonfly.Calendar.Collection.prototype.getByIndex = function (idx)
{
    return this._byIdx[idx];
};

Dragonfly.Calendar.Collection.prototype.getByName = function (name)
{
    return this._byName[name.toLowerCase()];
};

Dragonfly.Calendar.Collection.prototype.load = function (jsob)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.clear ();
    for (var i = 0; i < jsob.length; i++) {
        this.add (new c.BongoCalendar (jsob[i][0], jsob[i][1]));
    }
};

Dragonfly.Calendar.Collection.prototype.add = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (!(calendar instanceof c.BongoCalendar)) {
        throw new Error ('Trying to add non-calendar to Collection');
    }
    var oldCal = this._byId[calendar.bongoId];
    if (oldCal) {
        if (oldCal != calendar) {
            throw new Error ('Adding calendar with duplicate bongoId');
        }
        return;
    }
    if (this._byName[calendar.cal.name.toLowerCase()]) {
        throw new Error ('Adding calendar with duplicate name');
    }
    this._byIdx.push (calendar);
    this._byId[calendar.bongoId] = calendar;
    calendar._nameKey = calendar.cal.name.toLowerCase();
    this._byName[calendar._nameKey] = calendar;
};

Dragonfly.Calendar.Collection.prototype.update = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var newKey = calendar.cal.name.toLowerCase();
    if (newKey != calendar._nameKey) {
        delete this._byName[calendar._nameKey];
        calendar._nameKey = newKey;
        this._byName[newKey] = calendar;
    }
};

Dragonfly.Calendar.Collection.prototype.remove = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (!(calendar instanceof c.BongoCalendar)) {
        throw new Error ('Trying to remove a non-calendar');
    }
    if (!this._byId[calendar.bongoId]) {
        return;
    }
    delete this._byId[calendar.bongoId];
    delete this._byName[calendar._nameKey];
    for (var i = 0; i < this._byIdx.length; i++) {
        if (this._byIdx[i] == calendar) {
            this._byIdx.splice (i, 1);
            break;
        }
    }
};

Dragonfly.Calendar.Collection.prototype.iter = function ()
{
    var i = 0;
    var cals = this._byIdx;
    return { next: function () { if (i >= cals.length) throw StopIteration; return cals[i++]; } };
};

Dragonfly.Calendar.Collection.prototype.clear = function ()
{
    this._byIdx = [ ];
    this._byId = { };
    this._byName = { };
};

Dragonfly.Calendar.getLegend = function (cal)
{
    var legend = '';
    if (cal.url) {
        legend += 'W';
    }
    if (cal.owner) {
        legend += 'H';
    }
    if (cal.publish) {
        legend += 'P';
    }
    if (cal.protect) {
        legend += 'L';
    }
    if (cal.searchable) {
        legend += 'F';
    }
    return legend;
};

Dragonfly.Calendar.buildIcons = function (cal, html)
{
    var className;
    if (cal.url) {
        className = 'web';
    } else if (cal.publish) {
        if (cal.protect) {
            className = 'protect';
        } else {
            className = 'publish';
        }
    } else {
        className = 'info';
    }
    html.push ('<img src="/img/blank.gif" height="16" width="16" class="icon cal-info-button ', className, '">');
};

Dragonfly.Calendar.calendarAdded = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var p = c.Preferences;

    var cal = calendar.cal;

    c.calendars.add (calendar);

    var selected = c.getSelectedCalendarId();
    if (!selected) {
        selected = ' selected';
        c.selectedCalendarId = calendar.bongoId;
    } else {
        selected = '';
    }

    var li = LI({ id: 'bongo-calendar-' + calendar.bongoId,
                  'class': cal.color + selected, 
                  bongoId: calendar.bongoId });

    
    var html = new d.HtmlBuilder ('<span class="cal-icons" style="float: right;">');
    c.buildIcons (cal, html);
    html.push ('</span>',
               '<input type="checkbox" id="calendar-', calendar.bongoId, '"',
               p.getCalendarVisible (calendar.bongoId) ? ' checked' : '', '>',
               '<span class="cal-name">', d.escapeHTML (cal.name), '</span>');
    html.set (li);

    $('calendar-list').appendChild (li);
};

Dragonfly.Calendar.calendarRemoved = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    c.calendars.remove (calendar);

    var li = $('bongo-calendar-' + calendar.bongoId);
    if (li) {
        removeElement (li);
    }
    if (c.getSelectedCalendarId() == calendar.bongoId) {
        c.setSelectedCalendar (c.calendars.getByIndex (0));
    }
};

Dragonfly.Calendar.calendarUpdated = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    c.calendars.update (calendar);

    var li = $('bongo-calendar-' + calendar.bongoId);
    if (li) {
        // logDebug ('got the li!');
        var className = calendar.cal.color;
        if (li.className.indexOf ('selected') != -1) {
            className += ' selected';
        }
        if (li.className.indexOf ('popped-out')) {
            className += ' popped-out';
        }
        li.className = className;
        var span = li.getElementsByTagName ('span');

        var html = new d.HtmlBuilder;
        c.buildIcons (calendar.cal, html);
        html.set (span[0]);

        Element.setText (span[1], calendar.cal.name);
    } else {
        logWarning ('no calendar for:', calendar.bongoId);
    }
};
