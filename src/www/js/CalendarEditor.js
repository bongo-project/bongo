Dragonfly.Calendar.PersonalProperties = function (parent, calendar, showImport)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.id = d.nextId ('calendar-properties');

    this.nameId = d.nextId ('calendar-name');

    this.color = new d.ColorPicker;
    this.colorId = this.color.id;

    // sharing bits
    this.disclosure = new d.DisclosingLink (null, _('shareCalendar'));
    this.publishId = this.disclosure.checkId;

    this.nb = new d.Notebook();
    
    this.searchableId = d.nextId ('calendar-searchable');
    this.protectId = d.nextId ('calendar-protect');

    this.userId = d.nextId ('calendar-username');
    this.passId = d.nextId ('calendar-password');
    this.passId2 = d.nextId ('calendar-password');

    this.oldUserId = d.nextId ('calendar-username');
    this.oldPassId = d.nextId ('calendar-password');

    this.passLabelId = d.nextId ('calendar-password-message');
    this.pwState = 0;
    this.basicInviteId = d.nextId ('calendar-invite');

    // this.rwId = d.nextId ('calendar-readwrite');

    // import file bits
    if (showImport) {
        this.importDivId = d.nextId ('calendar-import-div');
        this.importFileId = d.nextId ('calendar-import');
    }

    this.addUserId = d.nextId ('calendar-email');
    this.userListId = d.nextId ('calendar-users');

    this.addButtonId = d.nextId ('calendar-add');
    this.removeButtonId = d.nextId ('calendar-remove');

    this.selectAllId = d.nextId ('calendar-select-all');
    this.selectNoneId = d.nextId ('calendar-select-none');

    this.searchId = d.nextId ('calendar-search');

    this.statusLabelId = d.nextId ('calendar-status');
    this.detailedInviteId = d.nextId ('calendar-invite');

    if (calendar) {
        this.loadFrom (calendar);
    }
    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Calendar.PersonalProperties.prototype = { };

Dragonfly.Calendar.PersonalProperties.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    html.push ('<div id="', this.id, '">',
               '<input type="hidden" name="calendar-type" value="personal">',
               '<table cellspacing="0" cellpadding="0">',

               '<tr><td><label for="', this.nameId, '">Name: </label></td>',
               '<td><input id="', this.nameId, '" name="calendar-name"></td></tr>',

               '<tr><td><label for="', this.colorId, '">Color: </label></td>',
               '<td>');

    this.color.buildHtml (html);

    html.push ('</td></tr>');
    if (this.importDivId) {
        html.push ('<tr><td><label for="', this.importFileId, '">Import .ics File: </label></td>',
                   '<td><input id="', this.importFileId, '" name="import-file" type="file"></td></tr>');
    }

    html.push ('</table>');

    // sharing tab
    var html2 = new d.HtmlBuilder (
        '<div><label><input id="', this.searchableId, '" name="sharing-searchable" type="checkbox"> ',
        'Allow other users to search for this calendar</label></div>',

        '<div><label><input id="', this.protectId, '" name="sharing-protect" type="checkbox"> ',
        'Password protect access:</label></div>',

        '<div class="indented">',
        '<table cellspacing="0" cellpadding="0">',

        '<tr><td><label for="', this.userId, '">Name: </label></td>',
        '<td><input id="', this.userId, '" name="sharing-name"></td></tr>',

        '<tr style="display: none"><td><label for="', this.oldPassId, '">Current password: </label></td>',
        '<td><input id="', this.oldPassId, '" type="password" name="sharing-old-password"></td></tr>',

        '<tr><td><label for="', this.passId, '">Password: </label></td>',
        '<td><input id="', this.passId, '" name="sharing-password" type="password"></td></tr>',

        '<tr><td><label for="', this.passId2, '">Password (again): </label></td>',
        '<td><input id="', this.passId2, '" name="sharing-password-validate" type="password"></td></tr>',

        '<tr><td></td><td><span id="', this.passLabelId, '" style="visibility: hidden">&nbsp;</span></td></tr>',
        
        '</table>',

        //'<div><label><input id="', this.rwId, '" name="sharing-canwrite" type="checkbox"> ',
        //'People with this password can modify this calendar</label></div>',

        '</div>', // password group

        '<a id="', this.basicInviteId, '">Send out invitation...</a>'
        );
    html2.set (this.nb.insertPage ('Basic', 0));

    // specific users
    html2 = new d.HtmlBuilder (
        '<div><label>Email: <input id="', this.addUserId, '"><input id="', this.addButtonId, '" type="button" value="Add"></label></div>',

        '<div class="scrollv"><ul style="height: 8em; max-width: 25em;" id="', this.userListId, '"></ul></div>',

        '<div class="actions"><span class="secondary">',
        //'<input type="button" value="+" id="', this.addButtonId, '">',
        '<input type="button" value="Remove" id="', this.removeButtonId, '">',
        '</span>',

        '<span style="display: none">Select: <a id="', this.selectAllId, '">All</a> <a id="', this.selectNoneId, '">None</a></span>',

        '</div><div>',
        '<a id="', this.detailedInviteId, '">Send out invitation...</a>',
        '</div>'
        );
    html2.set (this.nb.insertPage ('Detailed', 1));

    this.disclosure.buildHtml (html, this.nb);

    html.push ('<p id="', this.statusLabelId, '"></p></div>');

    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.Calendar.PersonalProperties.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;
    var c = d.Calendar;

    d.LabeledEntry (this.passId, '');
    d.LabeledEntry (this.passId2, '');

    if (this.loadDef) {
        Element.setHTML (this.statusLabelId, 'Loading calendar data...');
    } else if (this._calendar) {
        this._loadFrom (this._calendar);
        delete this._calendar;
    } else {
        $(this.nameId).value = 'New Calendar';
        this.color.setColor (d.getRandomColor ());
        $(this.colorId).name = 'calendar-color';
        this.publishClicked ();
    }

    $(this.publishId).name = 'sharing-publish';
    
    // basic...
    Event.observe (this.userId, 'keyup',
                   this.checkPasswords.bindAsEventListener (this));
    
    Event.observe (this.passId, 'keyup',
                   this.checkPasswords.bindAsEventListener (this));
    
    Event.observe (this.passId2, 'keyup',
                   this.checkPasswords.bindAsEventListener (this));
    
    Event.observe (this.publishId, 'change',
                   this.publishClicked.bindAsEventListener (this));
    
    Event.observe (this.protectId, 'change',
                   this.protectClicked.bindAsEventListener (this));

    Event.observe (this.basicInviteId, 'click',
                   this.inviteBasic.bindAsEventListener (this));

    // detailed...
    Event.observe (this.addUserId, 'keyup',
                   this.enableAdd.bindAsEventListener (this));
    this.enableAdd ();

    Event.observe (this.addButtonId, 'click',
                   this.addClicked.bindAsEventListener (this));

    Event.observe (this.userListId, 'click',
                   this.userListClicked.bindAsEventListener (this),
                   true);
    this.userListClicked ();

    Event.observe (this.removeButtonId, 'click',
                   this.removeClicked.bindAsEventListener (this));
    
    $(this.selectAllId).ignoreClick = true;
    Event.observe (this.selectAllId, 'click',
                   this.selectAll.bindAsEventListener (this));

    $(this.selectNoneId).ignoreClick = true;
    Event.observe (this.selectNoneId, 'click',
                   this.selectNone.bindAsEventListener (this));

    Event.observe (this.detailedInviteId, 'click',
                   this.inviteDetailed.bindAsEventListener (this));

    return elem;
};

Dragonfly.Calendar.PersonalProperties.prototype._loadFrom = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    delete this.loadDef;

    if (!$(this.id)) {
        this._calendar = calendar;
        return;
    }
    
    Element.setHTML (this.statusLabelId, '');

    var cal = calendar.cal;

    this.bongoId = calendar.bongoId;

    $(this.nameId).value = cal.name || 'New Calendar';
    $(this.colorId).value = cal.color || d.getRandomColor ();

    $(this.publishId).checked = !!cal.publish;
    $(this.searchableId).checked = !!cal.searchable;
    $(this.protectId).checked = !!cal.protect;
    $(this.userId).value = cal.username || '';
    this.origUserName = cal.username || '';
    $(this.oldPassId).value = '';

    if (cal.username) {
        // fake a password
        $(this.passId).emptyLabel = 'password';
        $(this.passId).setValue('');

        $(this.passId2).emptyLabel = 'password';
        $(this.passId2).setValue('');
    }

    //var tr = d.getParentByTagName (this.oldPassId, 'tr');
    //Element.show (tr);
    //$(this.passId).setvalue ('';
    //$(this.passId2).value = '';
    if (cal.publish) {
        this.disclosure.disclose ();
    } else {
        this.disclosure.enclose ();
    }
    if ($(this.publishId).checked) {
        this.showSharingProperties ();
    } else {
        this.hideSharingProperties ();
    }
    if (cal.acl) {
        for (var i = 0; i < cal.acl.length; i++) {
            this.addUser.apply (this, cal.acl[i]);
        }
    }
    this.publishClicked ();
};

Dragonfly.Calendar.PersonalProperties.prototype.loadFrom = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (this.loadDef) {
        this.loadDef.cancel();
    }
    // set loadDef before adding the callback so that we don't set
    // loadDef after _loadFrom is called
    this.loadDef = calendar.refresh();
    this.loadDef.addCallback (bind ('_loadFrom', this));
};

Dragonfly.Calendar.PersonalProperties.prototype.saveTo = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    calendar.setType ('personal');

    var cal = calendar.cal;

    cal.name = $(this.nameId).value;
    cal.color = $(this.colorId).value;

    calendar.setShared ($(this.publishId).checked);
    cal.searchable = $(this.searchableId).checked;
    if ($(this.protectId).checked) {
        calendar.setPassword ($(this.userId).value,
                              $(this.passId).getValue());
    }
    cal.acl = [ ];
    var lis = $(this.userListId).getElementsByTagName ('LI');
    for (var i = lis.length - 1; i >= 0; --i) {
        var span = lis[i].getElementsByTagName ('SPAN')[1];
        var img = lis[i].getElementsByTagName ('IMG')[0];
        cal.acl.unshift ([ Element.getText (span),
                           img.className.indexOf ('readwrite') != -1 ]);
    }

};

Dragonfly.Calendar.PersonalProperties.prototype.checkPasswords = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    var user = $(this.userId).value;
    var p1 = $(this.passId).getValue();
    var p2 = $(this.passId2).getValue();

    var pwState = 0;
    var msg = '';

    if (!$(this.publishId).checked || !$(this.protectId).checked) {
        pwState = 0;
    } else if (!user) {
        pwState = 1;
        msg = 'a username is required';
    } else if (p1 != p2) {
        pwState = 2;
        msg = 'the passwords do not match';
    } else if (!p1 && user != this.origUserName) {
        pwState = 3;
        msg = 'a password is required';
    }

    if (pwState != this.pwState) {
        if (pwState) {
            Element.setHTML (this.passLabelId, '<i>' + msg + '</i>');
            $(this.passLabelId).style.visibility = 'visible';
        } else {
            $(this.passLabelId).style.visibility = 'hidden';
            Element.setHTML (this.passLabelId, '&nbsp;');
        }
    }

    this.pwState = pwState;
};

Dragonfly.Calendar.PersonalProperties.prototype.hideSharingProperties = function ()
{
    this.disclosure.enclose ();
    this.publishClicked ();
};

Dragonfly.Calendar.PersonalProperties.prototype.showSharingProperties = function ()
{
    this.disclosure.disclose ();
    this.publishClicked ();
};

Dragonfly.Calendar.PersonalProperties.prototype.publishClicked = function ()
{
    var d = Dragonfly;
    
    var disabled = !$(this.publishId).checked;

    $(this.searchableId).disabled = disabled;
    $(this.protectId).disabled = disabled;

    this.protectClicked ();
};

Dragonfly.Calendar.PersonalProperties.prototype.protectClicked = function ()
{
    var d = Dragonfly;

    var disabled = !$(this.publishId).checked || !$(this.protectId).checked;

    this.checkPasswords ();

    $(this.userId).disabled = disabled;
    $(this.oldPassId).disabled = disabled;
    $(this.passId).disabled = disabled;
    $(this.passId2).disabled = disabled;

    //$(this.rwId).disabled = disabled;
};

Dragonfly.Calendar.PersonalProperties.prototype.enableAdd = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;

    // verify that this is a valid email address or user...
    $(this.addButtonId).disabled = $(this.addUserId).value.length == 0;
};

Dragonfly.Calendar.PersonalProperties.prototype.addUser = function (email, readwrite)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var html = [
        '<span style="float: right;">',
        '<img src="/img/blank.gif" height="16" width="16" class="icon read', 
        readwrite ? 'write' : 'only', '">',
        '</span>',
        '<input type="checkbox"> ',
        '<span>',
        d.escapeHTML (email),
        '</span>'
        ];
                 
                 
    var li = LI();
    Element.setHTML (li, html);
    $(this.userListId).appendChild (li);
};

Dragonfly.Calendar.PersonalProperties.prototype.addClicked = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.addUser ($(this.addUserId).value, false);
    $(this.addUserId).value = '';
    $(this.addButtonId).disabled = true;
};

Dragonfly.Calendar.PersonalProperties.prototype.userListClicked = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var elem = evt && Event.element (evt);

    if (elem && elem.nodeName == 'IMG') {
        if (elem.className.indexOf ('readonly') != -1) {
            elem.className = 'icon readwrite';
        } else {
            elem.className = 'icon readonly';
        }
    } else {
        var inputs = $(this.userListId).getElementsByTagName ('INPUT');
        for (var i = inputs.length - 1; i >= 0; --i) {
            if (inputs[i].checked) {
                $(this.removeButtonId).disabled = false;
                return;
            }
        }
        $(this.removeButtonId).disabled = true;
    }
};

Dragonfly.Calendar.PersonalProperties.prototype.removeClicked = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var inputs = $(this.userListId).getElementsByTagName ('INPUT');
    for (var i = inputs.length - 1; i >= 0; --i) {
        if (inputs[i].checked) {
            removeElement (inputs[i].parentNode);
        }
    }
    $(this.removeButtonId).disabled = true;
};

Dragonfly.Calendar.PersonalProperties.prototype.selectAll = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var inputs = $(this.userListId).getElementsByTagName ('INPUT');
    for (var i = inputs.length - 1; i >= 0; --i) {
        if (inputs[i].type == 'checkbox') {
            inputs[i].checked = true;
        }
    }
    $(this.removeButtonId).disabled = inputs.length == 0;
};

Dragonfly.Calendar.PersonalProperties.prototype.selectNone = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var inputs = $(this.userListId).getElementsByTagName ('INPUT');
    for (var i = inputs.length - 1; i >= 0; --i) {
        if (inputs[i].type == 'checkbox') {
            inputs[i].checked = false;
        }
    }
    $(this.removeButtonId).disabled = true;
};

Dragonfly.Calendar.PersonalProperties.prototype.verifies = function ()
{
    if (!$(this.nameId).value) {
        logDebug ('no name.');
        return false;
    }
    if (this.pwState != 0) {
        logDebug ('pwState:', this.pwState);
        return false;
    }
    return true;
};

Dragonfly.Calendar.PersonalProperties.prototype.getInvitationMail = function ()
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;
    var p = m.Preferences;

    var msg = {
        from: m.getFromAddress(),
        to: '',
        cc: '',
        bcc: p.getAutoBcc() || '',
        subject: 'Invitation to ' + d.userName + '\'s calendar'
    };
    msg.body = [ 'Someone wants to share their calendar with you.\n\n',
                 'You can view it in a few different ways:\n\n',
                 '        ics: [ICSURL]\n',
                 '     caldav: [CALDAVURL]\n',
                 'web browser: [HTMLURL]\n\n',
                 'If a password is needed, please ask me for it.' ].join ('');
    return msg;
};

Dragonfly.Calendar.PersonalProperties.prototype.inviteBasic = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    var msg = this.getInvitationMail();
    msg.calendarInvitationFor = this.bongoId;
    msg.useAuthUrls = false;
    c.composeNew (msg);
};

Dragonfly.Calendar.PersonalProperties.prototype.inviteDetailed = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    var msg = this.getInvitationMail();

    var lis = $(this.userListId).getElementsByTagName ('LI');
    var bccs = [ ];
    for (var i = lis.length - 1; i >= 0; --i) {
        var input = lis[i].getElementsByTagName ('INPUT')[0];
        if (input.checked) {
            var span = lis[i].getElementsByTagName ('SPAN')[1];
            bccs.unshift (Element.getText (span));
        }
    }
    bccs.unshift (msg.bcc);
    msg.bcc = bccs.join (', ');

    msg.calendarInvitationFor = this.bongoId;
    msgr.useAuthUrls = true;
    c.composeNew (msg);
};

// // //
Dragonfly.Calendar.SubscribedProperties = function (parent, calendar, showImport)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.id = d.nextId ('calendar-properties');

    this.nameId = d.nextId ('calendar-name');

    this.color = new d.ColorPicker;
    this.colorId = this.color.id;

    // web bits
    this.urlId = d.nextId ('calendar-url');
    this.webSearchableId = d.nextId ('calendar-searchable');

    this.webProtectId = d.nextId ('calendar-protect');
    this.webUserId = d.nextId ('calendar-username');
    this.webPassId = d.nextId ('calendar-password');

    this.statusLabelId = d.nextId ('calendar-status');

    if (calendar) {
        this.loadFrom (calendar);
    }
    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Calendar.SubscribedProperties.prototype = { };

Dragonfly.Calendar.SubscribedProperties.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    html.push ('<div id="', this.id, '">',
               '<input type="hidden" name="calendar-type" value="web">',
               '<table cellspacing="0" cellpadding="0">',

               '<tr><td><label for="', this.nameId, '">Name: </label></td>',
               '<td><input id="', this.nameId, '" name="calendar-name"></td></tr>',

               '<tr><td><label for="', this.colorId, '">Color: </label></td>',
               '<td>');

    this.color.buildHtml (html);

    html.push ('</td></tr>',

               '<tr><td><label for="', this.urlId, '">Url:</label></td>',
               '<td><input id="', this.urlId, '" name="web-url"></td></tr>',
        
               '<tr><td colspan="2"><label><input id="', this.webSearchableId, '" name="web-searchable" type="checkbox"> ',
               'Allow other users to search for this calendar</label></td></tr>',

               '<tr><td colspan="2"><label><input id="', this.webProtectId, '" name="web-protect" type="checkbox"> ',
               'This URL requires a password</label></td></tr>',

               '<tr><td><label for="', this.webUserId, '">Name: </td>',
               '<td><input id="', this.webUserId, '" name="web-username"></label></td></tr>',

               '<tr><td><label for="', this.webPassId, '">Password: </td>',
               '<td><input id="', this.webPassId, '" type="password" name="web-password"></label></td></tr>',

               '</table>');

    html.push ('<p id="', this.statusLabelId, '"></p></div>');

    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.Calendar.SubscribedProperties.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (this.loadDef) {
        Element.setHTML (this.statusLabelId, 'Loading calendar data...');
    } else if (this._calendar) {
        this._loadFrom (this._calendar);
        delete this._calendar;
    } else {
        $(this.nameId).value = 'New Calendar';
        this.color.setColor (d.getRandomColor ());
        $(this.colorId).name = 'calendar-color';
    }

    //Event.observe (this.whichId, 'change', this.updateNb.bindAsEventListener (this))
    
    // web bits...
    Event.observe (this.webProtectId, 'change',
                   this.webProtectClicked.bindAsEventListener (this));

    return elem;
};

Dragonfly.Calendar.SubscribedProperties.prototype._loadFrom = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    delete this.loadDef;

    if (!$(this.id)) {
        this._calendar = calendar;
        return;
    }
    
    Element.setHTML (this.statusLabelId, '');

    var cal = calendar.cal;

    $(this.nameId).value = cal.name || 'New Calendar';
    $(this.colorId).value = cal.color || d.getRandomColor ();

    $(this.urlId).value = cal.url || '';
    $(this.webSearchableId).checked = !!cal.searchable;
    $(this.webProtectId).checked = !!cal.protect;
    $(this.webUserId).value = cal.username || '';
    this.origUserName = cal.username || '';
    $(this.webPassId).value = '';
    this.webProtectClicked ();
};

Dragonfly.Calendar.SubscribedProperties.prototype.loadFrom = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (this.loadDef) {
        this.loadDef.cancel();
    }
    // set loadDef before adding the callback so that we don't set
    // loadDef after _loadFrom is called
    this.loadDef = calendar.refresh();
    this.loadDef.addCallback (bind ('_loadFrom', this));
};

Dragonfly.Calendar.SubscribedProperties.prototype.saveTo = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    calendar.setType ('web');

    var cal = calendar.cal;

    cal.name = $(this.nameId).value;
    cal.color = $(this.colorId).value;

    cal.url = $(this.urlId).value;
    cal.searchable = $(this.webSearchableId).checked;
    if ($(this.webProtectId).checked) {
        calendar.setPassword ($(this.webUserId).value,
                              $(this.webPassId).value);
    }
};

Dragonfly.Calendar.SubscribedProperties.prototype.webProtectClicked = function ()
{
    var d = Dragonfly;

    var disabled = !$(this.webProtectId).checked;

    $(this.webUserId).disabled = disabled;
    $(this.webPassId).disabled = disabled;
};

Dragonfly.Calendar.SubscribedProperties.prototype.verifies = function ()
{
    return true;
};

Dragonfly.Calendar.CalendarSummary = function (parent, calendar)
{
    var d = Dragonfly;

    this.calendar = calendar;

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Calendar.CalendarSummary.prototype = { };

Dragonfly.Calendar.CalendarSummary.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    html.push ('<h3>', d.escapeHTML (this.calendar.cal.name), '</h3>',
               '<p>Url: <a href="', d.escapeHTML (this.calendar.subscriptionUrl), '.ics">', d.escapeHTML(this.calendar.subscriptionUrl), '.ics</a></p>');
    html.addCallback (bind ('connectHtml', this));
    return html;
};

Dragonfly.Calendar.CalendarSummary.prototype.connectHtml = function (elem)
{
    return elem;
};

Dragonfly.Calendar.NewSubscriptionPopup = function (calName)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.calName = calName;

    this.formId = d.nextId ('calendar-form');
    this.urlId = d.nextId ('calendar-url');
    this.cancelId = d.nextId ('calendar-cancel');
    this.subscribeId = d.nextId ('calendar-subscribe');

    // this call results in the popup being built
    Dragonfly.PopupBuble.apply (this);
};

Dragonfly.Calendar.NewSubscriptionPopup.prototype = clone (Dragonfly.PopupBuble.prototype);

Dragonfly.Calendar.NewSubscriptionPopup.prototype.buildHtml = function (html)
{
    Dragonfly.PopupBuble.prototype.buildHtml.call (
        this, html, '<form id="', this.formId, '">Url: <textarea rows="2" id="', this.urlId, '"></textarea>',
        '<div class="actions">',
        '<input id="', this.cancelId, '" type="button" value="', _('genericCancel'), '">',
        '<input id="', this.subscribeId, '" type="submit" value="Subscribe"></div>',
        '</form>');
        
    // No need to connect html because the superclass does it for us 
};

Dragonfly.Calendar.NewSubscriptionPopup.prototype.connectHtml = function (elem)
{
    Dragonfly.PopupBuble.prototype.connectHtml.call (this, elem);

    var d = Dragonfly;

    Event.observe (this.urlId, 'change', this.updateSubscribe.bindAsEventListener (this));
    Event.observe (this.urlId, 'input', this.updateSubscribe.bindAsEventListener (this));
    Event.observe (this.urlId, 'mouseup', this.updateSubscribe.bindAsEventListener (this));
    Event.observe (this.urlId, 'keyup', this.updateSubscribe.bindAsEventListener (this));

    this.updateSubscribe ();

    Event.observe (this.formId, 'submit', this.submitClicked.bindAsEventListener (this));    
    Event.observe (this.cancelId, 'click', this.dispose.bindAsEventListener (this));
    return elem;
};

Dragonfly.Calendar.NewSubscriptionPopup.prototype.submitClicked = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var calendar = new c.BongoCalendar (null, { name: this.calName, url: $(this.urlId).value, color: d.getRandomColor() });
    d.notify ('Subscribing to "' + d.escapeHTML (calendar.cal.name) + '"...', true);
    calendar.create().addCallbacks (
        bind (function (res) {
                  d.clearNotify();
                  (new c.CalendarPropsPopup (calendar)).summarize();
                  return res;
              }, this),
        bind (function (err) {
                  if (!(err instanceof CancelledError)) {
                      d.notifyError ('Could not subscribe to "' + d.escapeHTML (calendar.cal.name) + '"', err);
                      logError ('error was:', d.reprError (err));
                  }
                  return err;
              }, this));
    this.dispose();
    Event.stop (evt);
};

Dragonfly.Calendar.NewSubscriptionPopup.prototype.updateSubscribe = function (evt)
{
    $(this.subscribeId).disabled = !$(this.urlId).value;
};

Dragonfly.Calendar.CalendarPropsPopup = function (calendar)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (!calendar) {
        throw new Error ('No calendar specified');
    }
    
    this.calendar = calendar;

    var currentPopup = c.CalendarPropsPopup.currentPopup;
    if (currentPopup && $(currentPopup.id)) {
        currentPopup.dispose();
    }
    c.CalendarPropsPopup.currentPopup = this;
 
    // this call results in the popup being built
    Dragonfly.PopupBuble.apply (this);
};

Dragonfly.Calendar.CalendarPropsPopup.prototype = clone (Dragonfly.PopupBuble.prototype);

Dragonfly.Calendar.CalendarPropsPopup.prototype.show = function ()
{
    var d = Dragonfly;

    d.sideBook.setCurrentPage (1);
    $('bongo-calendar-' + this.calendar.bongoId).scrollIntoView (false);
    d.PopupBuble.prototype.show.call (this, 'bongo-calendar-' + this.calendar.bongoId, this.vertical);    
};

Dragonfly.Calendar.CalendarPropsPopup.prototype.edit = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    var html = new d.HtmlBuilder();
    if (this.calendar.type == 'personal') {
        this.editor = new c.PersonalProperties (html, this.calendar);
    } else if (this.calendar.type == 'web') {
        this.editor = new c.SubscribedProperties (html, this.calendar);
    } else {
        throw new Error ('Unsupported calendar type: ' + this.calendar.type);
    }

    var deleteId = d.nextId ('calendar-delete');
    var cancelId = d.nextId ('calendar-cancel');
    this.saveId = d.nextId ('calendar-save');

    html.push ('<div class="actions">',
               '<input id="', deleteId, '" type="button" value="', _('genericDelete'), '" class="secondary">',
               '<input id="', cancelId, '" type="button" value="', _('genericCancel'), '"> ',
               '<input id="', this.saveId, '" type="button" value="', _('genericSave'), '">',
               '</div>');
    html.set (this.contentId);

    Event.observe (this.editor.id, 'change', this.updateUpdate.bindAsEventListener (this), true);
    Event.observe (this.editor.id, 'input', this.updateUpdate.bindAsEventListener (this), true);
    Event.observe (this.editor.id, 'keyup', this.updateUpdate.bindAsEventListener (this), true);
    Event.observe (this.editor.id, 'mouseup', this.updateUpdate.bindAsEventListener (this), true);

    Event.observe (deleteId, 'click', this.deleteClicked.bindAsEventListener (this));
    Event.observe (cancelId, 'click', this.dispose.bindAsEventListener (this));
    Event.observe (this.saveId, 'click', this.saveClicked.bindAsEventListener (this));

    this.updateUpdate ();
    this.setClosable (false);

    this.show();

    $(this.editor.nameId).focus ();
    $(this.editor.nameId).select ();
};

Dragonfly.Calendar.CalendarPropsPopup.prototype.summarize = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    var html = new d.HtmlBuilder();

    this.summary = new c.CalendarSummary (html, this.calendar);

    var deleteId = d.nextId ('calendar-delete');
    var inviteId = d.nextId ('calendar-invite');
    var editId = d.nextId ('calendar-edit');

    html.push ('<div class="actions">',
               '<a id="', deleteId, '" class="secondary">', _('genericDelete'), '</a>',
               '<a id="', inviteId, '">Send Invitation...</a>',
               '<a id="', editId, '">Edit</a>',
               '</div>');
    html.set (this.contentId);

    Event.observe (deleteId, 'click', this.deleteClicked.bindAsEventListener (this));
    Event.observe (editId, 'click', this.edit.bindAsEventListener (this));

    this.setClosable (true);

    this.show();
};

Dragonfly.Calendar.CalendarPropsPopup.prototype.deleteClicked = function (evt)
{
    var d = Dragonfly;

    d.notify ('Deleting "' + this.calendar.cal.name + '"...', true);

    this.calendar.del().addCallbacks (
        bind (function (res) {
                  this.dispose();
                  d.Calendar.calendarRemoved (this.calendar);
                  d.notify ('DELETED!!');
                  return res;
              }, this),
        bind ('showError', this));
};

Dragonfly.Calendar.CalendarPropsPopup.prototype.updateUpdate = function (evt)
{
    $(this.saveId).disabled = !this.editor.verifies();
};

Dragonfly.Calendar.CalendarPropsPopup.prototype.saveClicked = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;

    this.editor.saveTo (this.calendar);
    
    d.notify ('Saving "' + this.calendar.cal.name + '"...', true);

    this.calendar.save().addCallbacks (
        bind ('disposeAndReload', this),
        bind ('showError', this));
};

Dragonfly.Calendar.CalendarInvitationPopup = function (invite, elem)
{
    this.invite = invite;
    Dragonfly.PopupBuble.apply (this, [elem]); // this call results in the popup being built
};

Dragonfly.Calendar.CalendarInvitationPopup.prototype = clone (Dragonfly.PopupBuble.prototype);

Dragonfly.Calendar.CalendarInvitationPopup.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;
 
    d.PopupBuble.prototype.connectHtml.call (this, elem);
    var form = [
        '<p>', d.escapeHTML (this.invite.from[0][0]), ' has invited you to a calendar called ',
        d.escapeHTML (this.invite.calendarName), '.</p><p>You should only subscribe if you trust ',
        'this person and the server ', d.escapeHTML (this.invite.server), '.</p>'];

    var actions = [
        { value: "Delete", onclick: 'deleteClicked', secondary: true },
        { value: "Junk", onclick: 'junkClicked', secondary: true },
        { value: "Ignore", onclick: 'ignoreClicked'},
        { value: "Subscribe", onclick: 'subscribedClicked'}];

    this.setForm (form, actions);
    this.setClosable (true);
    return elem;
};

Dragonfly.Calendar.CalendarInvitationPopup.prototype.deleteClicked = function (evt)
{
    var d = Dragonfly;

    var loc = new d.Location ({ tab: 'mail', conversation: this.invite.convId, message: this.invite.bongoId });
    d.notify ('Deleting invitation...', true);
    d.request ('POST', loc, { method: 'delete' }).addCallbacks (
        bind ('disposeAndReload', this),
        bind ('showError', this));
};

Dragonfly.Calendar.CalendarInvitationPopup.prototype.junkClicked = function (evt)
{
    var d = Dragonfly;

    var loc = new d.Location ({ tab: 'mail', conversation: this.invite.convId, message: this.invite.bongoId });
    d.notify ('Junking invitation...', true);
    d.request ('POST', loc, { method: 'junk' }).addCallbacks (
        bind ('disposeAndReload', this),
        bind ('showError', this));
};

Dragonfly.Calendar.CalendarInvitationPopup.prototype.ignoreClicked = function (evt)
{
    var d = Dragonfly;
    var loc = new d.Location ({ tab: 'mail', conversation: this.invite.convId, message: this.invite.bongoId });
    d.notify ('Archiving invitation...', true);
    var req = d.request ('POST', loc, { method: 'archive' }).addCallbacks (
        bind ('disposeAndReload', this),
        bind ('showError', this));
    if (this.subCal) {
        var cal = this.subCal;
        req.addCallback (function () {
            (new d.Calendar.CalendarPropsPopup (cal)).summarize();
        });
    }
};

Dragonfly.Calendar.CalendarInvitationPopup.prototype.subscribeClicked = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var name = this.invite.calendarName;
    this.subCal = new c.BongoCalendar (null, { name: name, url: this.invite.ics });
    d.notify ('Subscribing to "' + d.escapeHTML (name) + '"...', true);
    this.subCal.create().addCallbacks ( bind ('ignoreClicked', this), bind ('showError', this));
};
