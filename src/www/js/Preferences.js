Dragonfly.Preferences = {
    defaultSet: 'all',
    defaultHandler: 'dragonfly'
};

Dragonfly.tabs['preferences'] = Dragonfly.Preferences;

Dragonfly.Preferences.handlers = { };
Dragonfly.Preferences.sets = [ 'all' ];
Dragonfly.Preferences.setLabels = { all: 'All' };

Dragonfly.Preferences.prefs = { };

Dragonfly.Preferences.defaultPrefs = {
    addressbook: {
        me: null
    },
    calendar: {
        dayStart: 7,
        defaultView: 'month',
        defaultTimezone: '/bongo/Europe/London',
        disabledCalendars: { },
        timezones: [ 
            {tzid: '/bongo/America/Los_Angeles', name: 'San Francisco'},
            {tzid: '/bongo/America/Boise', name: 'Provo'},
            {tzid: '/bongo/America/New_York', name: 'Boston'},
            {tzid: '/bongo/Europe/London', name: 'London'},
            {tzid: '/bongo/Europe/Paris', name: 'Paris'}
        ]
    }
};

Dragonfly.Preferences.load = function ()
{
    var d = Dragonfly;
    var p = d.Preferences;

    var a = d.AddressBook.Preferences;
    var c = d.Calendar.Preferences;
    var m = d.Mail.Preferences;

    function applyPrefs (prefs, fromError) {
        try {
            [ a, c, m ].each (
                function (o) {
                    ((o && o.update) || Prototype.emptyFunction) (prefs);
                });
            return p.prefs = prefs;
        } catch (e) {
            logError ('Error applying preferences: ' + d.reprError (e));
            if (fromError) {
                throw new Error ('Error applying default preferences');
            }
            return p.prefs = applyPrefs (deepclone (p.defaultPrefs), true);
        }
    }

    return d.requestJSON ('GET', '/user/' + d.userName + '/preferences/all/dragonfly.json').addCallbacks (
        function (jsob) {
            if (!jsob.addressbook) {
                logDebug ('Empty preferences, loading defaults.');
                jsob = p.defaultPrefs;
            }
            return applyPrefs (jsob);
        },
        function (e) {
            if (e instanceof d.InvalidJsonTextError || e instanceof SyntaxError) {
                logDebug ('Could not parse preferences, loading default.');
                return succeed (applyPrefs (deepclone (p.defaultPrefs), true));
            }
            logError ('Error loading preferences: ' + d.reprError (e));
            return e;
        });
};

Dragonfly.Preferences.save = function ()
{
    var d = Dragonfly;
    try {
        if (!d.Preferences.prefs) {
            throw new Error ('preferences object is invalid');
        }
        return d.request ('PUT', '/user/' + d.userName + '/preferences/all/dragonfly.json',
                          d.Preferences.prefs);
    } catch (e) {
        logError ('Error serializing preferences: ' + d.reprError (e));
        return d.Preferences.load();
    }
};

Dragonfly.Preferences.reset = function()
{
    var p = Dragonfly.Preferences;
    p.prefs = deepclone (p.defaultPrefs);
    p.save();
}

Dragonfly.Preferences._lookup = function (obj, path)
{
    try {
        var value = obj[path[0]];
        for (var i = 1; i < path.length; i++) {
            value = value[path[i]];
         }
         return value;
    } catch (e) {
        return undefined;
    }
}

Dragonfly.Preferences.get = function (key)
{
    var p = Dragonfly.Preferences;

    var path = key.split ('.');
    var value = p._lookup (p.prefs, path);
    if (value !== undefined) {
        return value;
    }

    logWarning ('No preference value found for key', key, ', checking defaults');
    value = p._lookup (p.defaultPrefs, path);
    if (value !== undefined) {
        return deepclone (value);
    }
    
    throw new Error ('Invalid Preference Key ' + key);
}

Dragonfly.Preferences.set = function (key, value)
{
    var p = Dragonfly.Preferences;

    var path = key.split ('.');
    var container = p._lookup (p.prefs, path.slice (0, -1));
    if (container !== undefined) {
        container[path.last()] = deepclone (value);
        return p.save();
    }
    throw new Error ('Invalid Preference Key ' + key);
}

Dragonfly.Preferences.Editor = { };
Dragonfly.Preferences.handlers['dragonfly'] = Dragonfly.Preferences.Editor;

Dragonfly.Preferences.Editor.parsePath = function (loc, path)
{
    loc.valid = true;
    return loc;
};

Dragonfly.Preferences.Editor.build = function (loc)
{
    var d = Dragonfly;
    
    Element.setHTML ('toolbar', '');
    Element.hide ('toolbar');
    var html = new d.HtmlBuilder ('<div id="search-no-results" class="scrollv">');

    html.push ('<table style="margin: auto;"><tr>',
               '<td><img src="img/big-info.png" style="float: left;" /></td>',
               '<td style="vertical-align:middle;"><h3>The Preferences editor goes here! <code>:)</code></h3></td>',
               '</tr></table></div>');
               
    html.set ('content-iframe');
}
