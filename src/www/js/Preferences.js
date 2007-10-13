/*
    Setup the Preferences handler
*/
Dragonfly.Preferences = {
    defaultSet: 'all',
    defaultHandler: 'dragonfly'
};

Dragonfly.tabs['preferences'] = Dragonfly.Preferences;

Dragonfly.Preferences.handlers = { };
Dragonfly.Preferences.sets = [ 'all' ];
Dragonfly.Preferences.setLabels = { all: 'All' };

/*
    Dragonfly.Preferences.prefs
    namespace
    
    Summary:    The core preferences code.
*/

Dragonfly.Preferences.prefs = { };

/*
    Dragonfly.Preferences.defaultPrefs
    array
    
    Summary:    Stores the default preferences
                that a user should have.
*/
Dragonfly.Preferences.defaultPrefs = {
    addressbook: {
        me: null
    },
    mail: {
        colorQuotes: true
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

/*
    Dragonfly.Preferences.load ()
    function
    
    Summary:    Loads the preferences from the server
                into the preferences handler for each
                service (ie. mail, calendar etc)
*/
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

/*
    Dragonfly.Preferences.save ()
    function
    
    Summary:    Saves the preferences from all
                the preference handlers back onto 
                the server, via JSON.
             
    Throws:     an error if the preferences object is null
                or undefined.
*/
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

/*
    Dragonfly.Preferences.reset ()
    function
    
    Summary:    Resets the current preferences to their defaults
                and saves the default vales back onto the server.
*/
Dragonfly.Preferences.reset = function()
{
    var p = Dragonfly.Preferences;
    p.prefs = deepclone (p.defaultPrefs);
    p.save();
};

/*
    Dragonfly.Preferences._lookup (obj, path)
    function
    
    Summary:    Looks up an preference value from within the
                path.
    
    Argument:   obj         The object to lookup
                path        Path to lookup from
*/
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
};

/*
    Dragonfly.Preferences.get (key)
    function
    
    Summary:    Gets the preference value for the specified
                key.
                
    Argument:   key         The key that is associated with
                            preference value you want to find.
    
    Throws:     an error if the key cannot be found in both
                the user's current preferences and the defaults.
                
                a warning if the key cannot be found in the
                user's current preferences.
*/
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
};

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
};

/*
    The Preferences editor (the UI component)
*/

// Setup the editor handler.
Dragonfly.Preferences.Editor = { };
Dragonfly.Preferences.handlers['dragonfly'] = Dragonfly.Preferences.Editor;

Dragonfly.Preferences.Editor.parsePath = function (loc, path)
{
    loc.valid = true;
    return loc;
};

/*
    Dragonfly.Preferences.Editor.build (loc)
    function
    
    Summary:    Builds the HTML elements for the preferences
                editor.
                
    Notes:      This function is called automatically by the
                location hash handler.
    
    Argument:   loc         The current Dragonfly location.
*/  
Dragonfly.Preferences.Editor.build = function (loc)
{
    var d = Dragonfly;
    
    Element.setHTML ('toolbar', '');
    Element.hide ('toolbar');
    
    var html = new d.HtmlBuilder ();
    // First, build the container.
    html.push ('<div id="preferences-container">', '</div>');
    html.set ('content-iframe');
    
    // Come up with some magic tabs!
    var notebook = new d.Notebook();
    var userpage = notebook.appendPage('General');
    var mailpage = notebook.appendPage('Mail');
    var calendarpage = notebook.appendPage('Calendar');
    var composerpage = notebook.appendPage('Composer');
    
    var userhtml = new d.HtmlBuilder ();
    userhtml.push ('<table border="0" cellspacing="0">',
        '<tr><td><label>', _('Profile:'), '</td><td><form id="userprofile">', '</form></td></tr>',
        '<tr><td><label>', _('Timezone:'), '</label></td>',
        '<td><span id="tzpicker"></span></td></tr>',
        '</table>');
    userhtml.set (userpage);
    
    var mailhtml = new d.HtmlBuilder ();
    mailhtml.push ('<table border="0" cellspacing="0">', 
        '<tr><td><label for="from">', _('From address:'), '</label></td>', 
        '<td><input type="text" id="from" name="from" /></td></tr>', 
        '<tr><td colspan="2"><span style="font-size: 80%;">Please don\'t abuse the above feature for now.</span></td></tr>',
        '<tr><td><label for="autobcc">', _('Auto BCC:'), '</label></td>',
        '<td><input type="text" name="autobcc" id="autobcc" /></td></tr>',
        '<tr><td><label for="signature">', _('Signature:'), '</label></td>',
        '<td><textarea rows="3" cols="30" name="signature" id="signature"></textarea><br />',
        '<input type="checkbox" id="usesig" name="usesig" /><label for="usesig">', _('Apply signature to outgoing mail'), '</label>',
        '</td></tr>',
        '<tr><td><label for="htmlmsg">', _('Show HTML messages:'), '</label></td>',
        '<td><select id="htmlmsg" name="htmlmsg"><option value="show">', _('Yes'), '</option><option value="ptext">', _('Prefer plain-text'), '</option><option value="hide">', _('No'), '</option></select></td></tr>',
        '<tr><td><label for="mailpagesize">', _('Page size:'), '</label></td>',
        '<td><input type="text" style="width: 20px;" name="mailpagesize" id="mailpagesize"> ', _('items'), '</td></tr>',
        '<tr><td colspan="2"><input type="checkbox" id="colorquotes" name="colorquotes" checked /><label for="colorquotes">', _('Colorize quotes in plain-text messages.'), '</label></td>',
        '</table>');
    mailhtml.set (mailpage);
    
    var calendarhtml = new d.HtmlBuilder ();
    calendarhtml.push ('<table border="0" cellspacing="0">',
        '<tr><td><label for="defaultcalview">', _('Default view:'), '</label></td>',
        '<td><select name="defaultcalview" id="defaultcalview">',
        '<option value="month">', _('Month'), '</option>',
        '<option value="week">', _('Week'), '</option>',
        '<option value="upcoming">', _('Upcoming'), '</option>',
        '<option value="day">', _('Day'), '</option>',
        '</select></td></tr>',
        '<tr><td><label>', _('Share with others by default?'), '</label></td>',
        '<input type="radio" name="calshare" value="public" id="calshareyes"><label for="calshareyes">Yes</label>&nbsp;',
        '<input type="radio" name="calshare" value="private" id="calshareno"><label for="calshareno">No</label>',
        '</td></tr>',
//            '<tr><td><label>Day start:</label></td>',
//            '<td><input type="text" style="width: 20px;" id="daystart" value="7"></td></tr>',
        '</table>');
    calendarhtml.set (calendarpage);
    
    var composerhtml = new d.HtmlBuilder();
    composerhtml.push('<table border="0" cellspacing="0">',
        '<tr><td><label for="defaultcompose">', _('Default message format:'), '</label></td>',
        '<td><select name="defaultcompose" id="defaultcompose">',
        '<option value="html">', _('HTML'), '</option>',
        '<option value="text">', _('Plain-text'), '</option>',
        '</select></td></tr>',
        '<tr><td><label for="linewidth">', _('Line width:'), '</label></td>',
        '<td><input type="text" id="linewidth" name="linewidth" style="width: 20px;"></td></tr>',
        '</table>');
    composerhtml.set(composerpage);
    
    var form = [
    '<form id="someid">', notebook, '</form>'];
    
    var actions = [{ value: _('Cancel'), onclick: 'dispose'}, { value: _('Save'), onclick: 'save'}];        
    
    // Now, fill in the content with tabs & buttons.
    this.buildInterface (form, actions, 'preferences-container');
    logDebug('Created UI OK - setting up TzSelector widget...');
    
    // Setup timezone widget
    d.tzselector = new d.TzSelector (d.curTzid, 'tzpicker', true);
    
    logDebug('We just got built.');
};


Dragonfly.Preferences.Editor.save = function ()
{
    var d = Dragonfly;
    var p = d.Preferences;
    
    // User profile
    Dragonfly.notify (_('Saving changes...'), true);
    var result = this.profileEditor.save().addCallbacks(bind(
        function (result) {
            // User prefs
            p.prefs.addressbook.me = result.bongoId || this.oldId || 0;

            // Mail prefs
            p.prefs.mail.from = $('from').value;
            p.prefs.mail.sender = this.profileEditor.fname || 'BUG!';
            p.prefs.mail.autoBcc = $('autobcc').value;
            p.prefs.mail.pageSize = $('mailpagesize').value;
            p.prefs.mail.signature = $('signature').value;
            p.prefs.mail.usesig = $('usesig').checked;
            p.prefs.mail.colorQuotes = $('colorquotes').checked;
            
            // Calendar prefs
            p.prefs.calendar.defaultView = $('defaultcalview').value;
            p.prefs.calendar.dayStart = 7;  // Yes, hardwire this one (for now)!
            p.prefs.calendar.sharePublicDefault = $('calshareyes').checked;
            
            // Composer prefs
            p.prefs.composer = {};
            p.prefs.composer.messageType = $('defaultcompose').value;
            p.prefs.composer.lineWidth = $('linewidth').value;
            
            // Finish up
            p.save();
            
            // Timezone
            d.setCurrentTzid (d.tzselector.getTzid());
            
            Dragonfly.notify (_('Changes saved.'));
            
            this.dispose();
        }, this), bind('showError', this));
};

Dragonfly.Preferences.Editor.showError = function (req)
{
    var d = Dragonfly;
    d.notifyError("We had a woopsie when we tried to save your prefs.", null, false);
    this.dispose();
};

Dragonfly.Preferences.Editor.dispose = function ()
{
    var d = Dragonfly;    
    
    // Send user back to previous location.
    d.go('#' + d.prevLoc);
};

Dragonfly.Preferences.Editor.load = function (loc, jsob)
{
    var d = Dragonfly;

    document.title = 'Preferences: ' + Dragonfly.title;

    // Mail prefs
    if (jsob.mail)
    {
        $('from').value = jsob.mail.from;
    }
    else
    {
        // I would *love* to use d.defaultMailAddress here, but we get the whole "name <address>" jazz.
        // Maybe use/write some function in d.Mail to convert that?
        $('from').value = (d.userName + '@' + window.location.hostname);
        
        if (!jsob.mail)
        {
            logWarning('You have no prefs. I should probably warn you, or.. something.');
        }
    }
    
    if (jsob.mail && jsob.mail.autoBcc)         { $('autobcc').value        = jsob.mail.autoBcc;         }
    if (jsob.mail && jsob.mail.pageSize)        { $('mailpagesize').value   = jsob.mail.pageSize;        }
    if (jsob.mail && jsob.mail.signature)       { $('signature').value      = jsob.mail.signature;       }
    if (jsob.mail && jsob.mail.usesig)          { $('usesig').checked       = jsob.mail.usesig;          }
    if (jsob.mail && jsob.mail.colorQuotes)     { $('colorquotes').checked  = jsob.mail.colorQuotes;     }
    
    // Calendar prefs
    if (jsob.calendar && jsob.calendar.defaultView)    { $('defaultcalview').value = jsob.calendar.defaultView; }
    if (jsob.calendar && jsob.calendar.sharePublicDefault) { $('calshareyes').checked = jsob.calendar.sharePublicDefault; $('calshareno').checked = !jsob.calendar.sharePublicDefault  } else { $('calshareno').checked = true; }
    //if (jsob.calendar.dayStart)    { $('daystart').value       = jsob.calendar.dayStart;    }
    
    // Composer prefs
    if (jsob.composer && jsob.composer.messageType) { $('defaultcompose').value = jsob.composer.messageType; }
    if (jsob.composer && jsob.composer.lineWidth)   { $('linewidth').value      = jsob.composer.lineWidth  ; } else { $('linewidth').value = '80'; }
       
    // Now, load up the profile editor!
    var pe = new d.AddressBook.UserProfile('userprofile');
    if (jsob.addressbook && jsob.addressbook.me)
    {
        logDebug('Awesome, we have a contact ID!');
        // We've already setup our profile - load it!
        this.oldId = jsob.addressbook.me;
        d.AddressBook.loadContact(jsob.addressbook.me).addCallback(bind ('build', pe));
    }
    else
    {
        logDebug('me == null. Buid empty contact UI thingo.');
        // Build empty profile editor UI.
        pe.build(Dragonfly.AddressBook.buildNewContact());
    }
    
    this.profileEditor = pe;
    
    logDebug('We just loaded.');
};

Dragonfly.Preferences.Editor.buildInterface = function (children, actions, element)
{
    var d = Dragonfly;    
    
    var html = new d.HtmlBuilder ();
    d.HtmlBuilder.buildChild (html, children);
    html.push ('<div class="actions">');
    for (var i = 0, ids = [ ]; i < actions.length; i++){
        var action = actions[i];
        ids.push (d.nextId ('button'));
        html.push (' <input id="', ids[i], '" type="button" value="', action.value, '"');
        html.push (action.secondary ? ' class="secondary">' : '>');
    }
    html.push ('</div>');
    
    html.set (element);    
    for (var i = 0; i < actions.length; i++){
        logDebug('Setting up actions for ' + actions[i].value);
        var clickHandler = actions[i].onclick;
        Event.observe (ids[i], 'click', bind (clickHandler, this));
        logDebug('Done!');
    }
};
