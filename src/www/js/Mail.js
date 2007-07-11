Dragonfly.Mail = {
    defaultSet: 'inbox',
    defaultHandler: 'conversations'
};

Dragonfly.tabs['mail'] = Dragonfly.Mail;

Dragonfly.Mail.handlers = { };
Dragonfly.Mail.sets = [ 'all', 'inbox', 'starred', 'sent', 'drafts', 'trash', 'junk' ];
Dragonfly.Mail.setLabels = {
    'all':     'mailSetAllLabel',
    'inbox':   'mailSetInboxLabel',
    'starred': 'mailSetStarredLabel',
    'sent':    'mailSetSentLabel',
    'drafts':  'mailSetDraftsLabel',
    'trash':   'mailSetTrashLabel',
    'junk':    'mailSetJunkLabel'
};

Dragonfly.Mail.getView = function (loc)
{
    logDebug('Dragonfly.Mail.getView(loc) called.');
    
    var d = Dragonfly;
    var m = d.Mail;

    loc = loc || d.curLoc;
    
    var ret;
    if (loc.conversation)
    {
        ret = m.ConversationView;
    }
    else if (loc.handler == 'contacts')
    {
        if (loc.contact)
        {
            ret = m.ListView;
        }
        else
        {
            ret = m.MultiListView;
        }
    }
    else if (loc.handler == 'subscriptions')
    {
        if (loc.listId)
        {
            ret = m.ListView;
        }
        else
        {
            ret = m.MultiListView;
        }
    }
    else 
    {
        ret = m.ListView;
    }
    
    return ret;
};

Dragonfly.Mail.Preferences = { };

Dragonfly.Mail.Preferences.update = function (prefs)
{
    if (!prefs.mail) {
        prefs.mail = { };
    }
    if (!prefs.mail.pageSize) {
        prefs.mail.pageSize = 30;
    }
};

Dragonfly.Mail.Preferences.getFromAddress = function ()
{
    var d = Dragonfly;
    var p = d.Preferences;

    return p.prefs.mail.from;
};

Dragonfly.Mail.Preferences.setFromAddress = function (address)
{
    var d = Dragonfly;
    var p = d.Preferences;

    p.prefs.mail.from = address;
    p.save ();
};

Dragonfly.Mail.Preferences.getAutoBcc = function ()
{
    var d = Dragonfly;
    var p = d.Preferences;

    return p.prefs.mail.autoBcc;
};

Dragonfly.Mail.Preferences.setAutoBcc = function (bcc)
{
    var d = Dragonfly;
    var p = d.Preferences;

    p.prefs.mail.autoBcc = bcc;
    p.save ();
};

Dragonfly.Mail.Preferences.getPageSize = function ()
{
    var d = Dragonfly;
    var p = d.Preferences;

    return p.prefs.mail.pageSize || 30;
};

Dragonfly.Mail.Preferences.getSignatureAvailable = function ()
{
    var p = Dragonfly.Preferences;
    
    return p.prefs.mail.usesig || false;
}

Dragonfly.Mail.Preferences.getSignature = function ()
{
    var p = Dragonfly.Preferences;
    
    return p.prefs.mail.signature || '';
}

Dragonfly.Mail.getFromAddress = function ()
{
    var d = Dragonfly;
    var m = d.Mail;
    var p = m.Preferences;

    return p.getFromAddress() || d.defaultMailAddress || (d.userName + '@' + window.location.hostname);
};

Dragonfly.Mail.updateToolbar = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView (loc);

    /* these aren't *exactly* right... */
    $('mail-toolbar-archive').value = (loc.set == 'all')     ? _('buttonUnarchiveLabel') : _('buttonArchiveLabel');
    $('mail-toolbar-delete').value  = (loc.set == 'trash')   ? _('buttonUndeleteLabel')  : _('genericDelete');
    $('mail-toolbar-junk').value    = (loc.set == 'junk')    ? _('buttonNotJunkLabel')   : _('buttonJunkLabel');
    $('mail-toolbar-star').value    = (loc.set == 'starred') ? _('buttonUnstarLabel')    : _('buttonStarLabel');

    Element[v.selectAll   ? 'show' : 'hide' ] ('mail-toolbar-select-all');
    Element[v.deselectAll ? 'show' : 'hide' ] ('mail-toolbar-deselect-all');

    Element.setHTML ('conv-prev-href', '');
    Element.setHTML ('conv-next-href', '');
};

Dragonfly.Mail.toolbarAction = function (method)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView ();

    var guids = v.getSelectedIds ();
    if (guids.length == 0) {
        return;
    }
    var tmpLoc = new d.Location (d.curLoc);

    delete tmpLoc.message;
    delete tmpLoc.conversation;

    d.disableToolbar ();
    d.notify ('Marking messages as ' + method, true);

    return d.request ('POST', tmpLoc, { method: method, bongoIds: guids }).addCallbacks (
        function (res) {
            d.notify ('Messages marked as ' + method + '.');
            d.enableToolbar ();
            d.go (tmpLoc);
            return res;
        },
        function (err) {
            if (!(err instanceof CancelledError)) {
                d.notifyError ('Error marking messages as ' + method);
                logError ('error is:', d.reprError (err));
            }
            d.enableToolbar ();
            return err;
        });
};

Dragonfly.Mail.toolbarDeleteClicked = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;

    return m.toolbarAction ((d.curLoc.set == 'trash' ? 'un' : '') + 'delete');
};

Dragonfly.Mail.toolbarJunkClicked = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;

    return m.toolbarAction ((d.curLoc.set == 'junk' ? 'un' : '') + 'junk');
};

Dragonfly.Mail.toolbarArchiveClicked = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;

    return m.toolbarAction ((d.curLoc.set == 'all' ? 'un' : '') + 'archive');
};

Dragonfly.Mail.toolbarStarClicked = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;

    return m.toolbarAction ((d.curLoc.set == 'starred' ? 'un' : '') + 'star');
};

Dragonfly.Mail.buildSummarySelect = function ()
{
    var d = Dragonfly;
    var m = d.Mail;
    
    Element.setHTML ('mail-summary-div', [
                         '<select name="mail-summary-select" id="mail-summary-select">',
                         '<option value="conversations">', _('mailAllLabel'), '</option>',
                         '<option value="tome">', _('mailToMeSelectionLabel'), '</option>',
                         '<option value="contacts">', _('mailFromContactsLabel'), '</option>',
                         '<option value="subscriptions">', _('mailSubscriptionsLabel'), '</option>',
                         '</select>']);

    Event.observe ('mail-summary-select', 'change', 
                   (function (evt) {
                       m.setHandler ($('mail-summary-select').value);
                   }).bindAsEventListener (null));
};

Dragonfly.Mail.setHandler = function (handler)
{
    var d = Dragonfly;
    
    var loc = { tab: d.curLoc.tab,
                set: d.curLoc.set,
                handler: handler,
                page: 1,
                valid: true };

    d.go (new d.Location (loc));
};      

Dragonfly.Mail.updateSummarySelect = function (loc) 
{
    var d = Dragonfly;
    
    $('mail-summary-select').value = loc.handler;
};

Dragonfly.Mail.getScrolledElement = function (loc)
{ 
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView (loc);

    return ((v && v.getScrolledElement) || Prototype.emptyFunction) (loc);
};

Dragonfly.Mail.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;

    function connectToolbarButton (id, func)
    {
        Event.observe (id, 'click', m[func].bindAsEventListener (null));
    }

/*
    if (!d.prevLoc || d.prevLoc.tab != 'mail') {
        d.setActions ([ 'New Conversation', 'mailto:' ]);
    }
*/
    if (!$('mail-toolbar-delete')) {
        Element.setHTML ('toolbar', [
                             '<table class="conv-nav"><tr>',
                             '<td width="1%" rowspan="2">',
                    
                             '<span id="mail-summary-div"></span>',
                    
                             '<input type="button" id="mail-toolbar-archive" value="', _('buttonArchiveLabel'), '">',
                             '<input type="button" id="mail-toolbar-junk" value="', _('buttonJunkLabel'), '">',
                             '<input type="button" id="mail-toolbar-delete" value="', _('genericDelete'), '">',
                             '<input type="button" id="mail-toolbar-star" value="', _('buttonStarLabel'), '">',

                             '<input type="button" id="mail-toolbar-select-all" value="', _('toolbarSelectAll'), '">',
                             '<input type="button" id="mail-toolbar-deselect-all" value="', _('toolbarDeselectAll'), '">',

                             '</td>',
                             '<td class="prev"><a id="conv-prev-href" href="#"></a></td></tr>',
                             '<tr><td class="next"><a id="conv-next-href" href="#"></a></td></tr>',
                             '</table>'
                             ]);

        Event.observe ('mail-toolbar-delete', 'click',
                       m.toolbarDeleteClicked.bindAsEventListener (null));
                        
        Event.observe ('mail-toolbar-junk', 'click',
                       m.toolbarJunkClicked.bindAsEventListener (null));
                        
        Event.observe ('mail-toolbar-archive', 'click',
                       m.toolbarArchiveClicked.bindAsEventListener (null));

        Event.observe ('mail-toolbar-star', 'click',
                       m.toolbarStarClicked.bindAsEventListener (null));

        Event.observe ('mail-toolbar-select-all', 'click',
                       (function (evt) {
                           m.getView (d.curLoc).selectAll ();
                       }).bindAsEventListener (null));
                        
        Event.observe ('mail-toolbar-deselect-all', 'click',
                       (function (evt) {
                           m.getView (d.curLoc).deselectAll ();
                       }).bindAsEventListener (null));

        m.buildSummarySelect ();
        Element.show ('mail-summary-div');
        Element.show ('toolbar');
    }

    m.updateToolbar (loc);
    m.updateSummarySelect (loc);
};

Dragonfly.Mail.parseParticipant = function (name)
{
    function cleanup (s) {
        var start, end, c;
        for (start = 0; start < s.length; start++) {
            c = s.charAt (start);
            if (c != ' ' && c != '"') {
                break;
            }
        }
        for (end = s.length - 1; end > start; --end) {
            c = s.charAt (end);
            if (c != ' ' && c != '"') {
                break;
            }
        }
        return s.substring (start, end + 1);
    }
    var matches = name.match (/^([^<]*)<([^>]*)>/);    
    var part = { 
        name: cleanup (matches ? matches[1] || matches[2] : name),
        mail: matches ? matches[2] : name
    };
    //logDebug ('person: |' + name + '|  -=>  |' + ret.name + '|');
    if (part.name == '' && part.mail == '') {
        return null;
    }
    return part;
};

Dragonfly.Mail.markupParticipants = function (parts)
{
    var d = Dragonfly;
    var m = d.Mail;

    var res = '';
    var need_comma = false;

    forEach (parts, function (part) {
                 var name = m.parseParticipant (part);
                 if (!name) {
                     return;
                 }
                 if (need_comma) {
                     res += '<span class="comma">,</span></span>';
                 } else {
                     need_comma = true;
                 }
                 
                 res += '<span class="people">' + d.escapeHTML (name.name);
             });
    if (need_comma) {
        res += '</span>';
    }
    return res;
};

Dragonfly.Mail.joinParticipants = function (parts)
{
    var d = Dragonfly;
    var m = d.Mail;
    return map (function (part) {
                    var name = m.parseParticipant (part);
                    return name && name.name;
                }, parts).join (', ');
};

Dragonfly.Mail.Conversations = { label: 'mailConversationsLabel' };
Dragonfly.Mail.handlers['conversations'] = Dragonfly.Mail.Conversations;

Dragonfly.Mail.Conversations.parseArgs = function (loc, args)
{
    if (args) {
        var matches = args[0].match (/^page(\d*)$/);
        if (matches) {
            loc.page = parseInt (matches[1]);
        }
    }
    if (!loc.page) {
        loc.page = 1;
    }
};

Dragonfly.Mail.Conversations.getArgs = function (loc)
{
    return [ loc.conversation ? '-' : 'page' + (loc.page || 1) ];
};

Dragonfly.Mail.Conversations.parsePath = function (loc, path)
{
    var c = Dragonfly.Mail.Conversations;
    
    loc.conversation = path.shift ();
    loc.valid = true;
    
    return loc;
};

Dragonfly.Mail.Conversations.getServerUrl = function (loc, path)
{
    if (loc.conversation) {
        path.push (loc.conversation);
    }

    if (loc.message) {
        path.push (loc.message);
    }

    if (loc.attachment) {
        path.push (loc.attachment);
        return true;
    }

    return false;
};

Dragonfly.Mail.Conversations.getClientUrl = function (loc, path)
{
    if (loc.conversation) {
        path.push (loc.conversation);
    }

    return loc;
};

Dragonfly.Mail.Conversations.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView (loc);

    m.build (loc);
    v.build (loc);
};

Dragonfly.Mail.Conversations.load = function (loc, jsob)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView (loc);

    return v.load (loc, jsob);
};

/*
Dragonfly.Mail.Messages = { };
Dragonfly.Mail.handlers['messages'] = Dragonfly.Mail.Messages;

Dragonfly.Mail.Messages.parseArgs = function (loc, args)
{
    if (args) {
        var matches = args[0].match (/^page(\d*)$/);
        if (matches) {
            loc.page = parseInt (matches[1]);
        }
    }
    if (!loc.page) {
        loc.page = 1;
    }
};

Dragonfly.Mail.Messages.getArgs = function (loc)
{
    return [ 'page' + (loc.page || 1) ];
};

Dragonfly.Mail.Messages.parsePath = function (loc, path)
{
    var c = Dragonfly.Mail.Messages;

    loc.message = path.shift ();

    loc.valid = true;
    
    return loc;
};

Dragonfly.Mail.Messages.getServerUrl = function (loc, path)
{
    if (loc.message) {
        path.push (loc.message);
    }

    if (loc.attachment) {
        path.push (loc.attachment);
        return true;
    }

    return false;
};

Dragonfly.Mail.Messages.getClientUrl = function (loc, path)
{
    if (loc.message) {
        path.push (loc.message);
    }

    return loc;
};

Dragonfly.Mail.Messages.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;

    return m['build' + (loc.message ? _('mailConversation') : _('mailList')) + _('mailView')] (loc);
};

Dragonfly.Mail.Messages.load = function (loc, jsob)
{
    var c = Dragonfly.Mail.Messages;
    var m = Dragonfly.Mail;

    return m['load' + (loc.message ? _('mailItem') : _('mailList'))] (loc, jsob);
};
*/

Dragonfly.Mail.Contacts = { label: 'mailContactsLabel' };
Dragonfly.Mail.handlers['contacts'] = Dragonfly.Mail.Contacts;

Dragonfly.Mail.Contacts.parseArgs = function (loc, args)
{
    if (args) {
        var matches = args[0].match (/^page(\d*)$/);
        if (matches) {
            loc.page = parseInt (matches[1]);
        }
    }
    if (!loc.page) {
        loc.page = 1;
    }
};

Dragonfly.Mail.Contacts.getArgs = function (loc)
{
    return [ loc.conversation ? '-' : 'page' + (loc.page || 1) ];
};

Dragonfly.Mail.Contacts.parsePath = function (loc, path)
{
    var c = Dragonfly.Mail.Contacts;
    
    loc.contact = path.shift ();
    loc.conversation = path.shift ();
    loc.valid = true;
    
    return loc;
};

Dragonfly.Mail.Contacts.getServerUrl = function (loc, path)
{
    if (loc.contact) {
        path.push (loc.contact);
    }

    if (loc.conversation) {
        path.push (loc.conversation);
    }

    if (loc.message) {
        path.push (loc.message);
    }

    if (loc.attachment) {
        path.push (loc.attachment);
        return true;
    }

    return false;
};

Dragonfly.Mail.Contacts.getClientUrl = function (loc, path, stopAtComponent)
{
    if (loc.contact) {
        path.push (loc.contact);
    }
    if (stopAtComponent == 'contact') {
        return loc;
    }
    
    if (loc.conversation) {
        path.push (loc.conversation);
    }

    return loc;
};

Dragonfly.Mail.Contacts.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView (loc);

    m.build (loc);
    v.build (loc);
};

Dragonfly.Mail.Contacts.load = function (loc, jsob)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Contacts;
    
    var v = m.getView (loc);
    return v.load (loc, jsob);
};

Dragonfly.Mail.Contacts.getBreadCrumbs = function (loc)
{
    var d = Dragonfly;
    var ab = d.AddressBook;

    if (loc.contact) {
        return d.format ('<a href="#{0}">{1} <strong>{2}</strong></a>',
                         d.escapeHTML (loc.getClientUrl ('contact')),
                         loc.set == 'sent' ? _('mailTo') + " " : _('mailFrom') + " ",
                         d.escapeHTML (ab.getNameForContact (loc.contact)));
    }
};

Dragonfly.Mail.Subscriptions = { label: 'mailMailingListsLabel' };
Dragonfly.Mail.handlers['subscriptions'] = Dragonfly.Mail.Subscriptions;

Dragonfly.Mail.Subscriptions.parseArgs = function (loc, args)
{
    if (args) {
        var matches = args[0].match (/^page(\d*)$/);
        if (matches) {
            loc.page = parseInt (matches[1]);
        }
    }
    if (!loc.page) {
        loc.page = 1;
    }
};

Dragonfly.Mail.Subscriptions.getArgs = function (loc)
{
    return [ loc.conversation ? '-' : 'page' + (loc.page || 1) ];
};

Dragonfly.Mail.Subscriptions.parsePath = function (loc, path)
{
    var c = Dragonfly.Mail.Subscriptions;
    
    loc.listId = decodeURIComponent(path.shift () || '');
    loc.conversation = path.shift ();
    loc.valid = true;
    
    return loc;
};

Dragonfly.Mail.Subscriptions.getServerUrl = function (loc, path)
{
    if (loc.listId) {
        path.push (encodeURIComponent(loc.listId))
    }

    if (loc.conversation) {
        path.push (loc.conversation);
    }

    if (loc.message) {
        path.push (loc.message);
    }

    if (loc.attachment) {
        path.push (loc.attachment);
        return true;
    }

    return false;
};

Dragonfly.Mail.Subscriptions.getClientUrl = function (loc, path, stopAtComponent)
{
    if (loc.listId) {
        path.push (encodeURIComponent(loc.listId));
    }
    if (stopAtComponent == 'listId') {
        return loc;
    }
    
    if (loc.conversation) {
        path.push (loc.conversation);
    }

    return loc;
};

Dragonfly.Mail.Subscriptions.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView (loc);

    m.build (loc);
    v.build (loc);
};

Dragonfly.Mail.Subscriptions.load = function (loc, jsob)
{
    var d = Dragonfly;
    var m = d.Mail;
    var s = m.Subscriptions;
    
    var v = m.getView (loc);
    return v.load (loc, jsob);
};

Dragonfly.Mail.Subscriptions.getBreadCrumbs = function (loc)
{
    var d = Dragonfly;

    if (loc.listId) {
        var splitChar = loc.listId.indexOf ('@');
        if (splitChar == -1) {
            splitChar = loc.listId.indexOf ('.');
        }

        if (splitChar == -1) {
            return d.format ('<a href="#{0}"><strong>{1}</strong></a>',
                             d.escapeHTML (loc.getClientUrl ('listId')),
                             d.escapeHTML (loc.listId));
        } else {
            return d.format ('<a href="#{0}"><strong>{1}</strong> ({2})</a>',
                             d.escapeHTML (loc.getClientUrl ('listId')),
                             d.escapeHTML (loc.listId.substring (0, splitChar)),
                             d.escapeHTML (loc.listId.substring (splitChar + 1)));
        }
    }
};

Dragonfly.Mail.ToMe = { label: 'mailToMeLabel' };
Dragonfly.Mail.handlers['tome'] = Dragonfly.Mail.ToMe;

Dragonfly.Mail.ToMe.parseArgs = function (loc, args)
{
    if (args) {
        var matches = args[0].match (/^page(\d*)$/);
        if (matches) {
            loc.page = parseInt (matches[1]);
        }
    }
    if (!loc.page) {
        loc.page = 1;
    }
};

Dragonfly.Mail.ToMe.getArgs = function (loc)
{
    return [ loc.conversation ? '-' : 'page' + (loc.page || 1) ];
};

Dragonfly.Mail.ToMe.parsePath = function (loc, path)
{
    var c = Dragonfly.Mail.ToMe;
    
    loc.conversation = path.shift ();
    loc.valid = true;
    
    return loc;
};

Dragonfly.Mail.ToMe.getServerUrl = function (loc, path)
{
    if (loc.conversation) {
        path.push (loc.conversation);
    }

    if (loc.message) {
        path.push (loc.message);
    }

    if (loc.attachment) {
        path.push (loc.attachment);
        return true;
    }

    return false;
};

Dragonfly.Mail.ToMe.getClientUrl = function (loc, path)
{
    if (loc.conversation) {
        path.push (loc.conversation);
    }

    return loc;
};

Dragonfly.Mail.ToMe.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView (loc);

    m.build (loc);
    v.build (loc);
};

Dragonfly.Mail.ToMe.load = function (loc, jsob)
{
    var d = Dragonfly;
    var m = d.Mail;
    var s = m.ToMe;
    
    var v = m.getView (loc);

    return v.load (loc, jsob);
};
