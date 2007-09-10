Dragonfly.Mail = {
    defaultSet: 'inbox',
    defaultHandler: 'conversations'
};

Dragonfly.tabs['mail'] = Dragonfly.Mail;

Dragonfly.Mail.handlers = { };
Dragonfly.Mail.sets = [ 'all', 'inbox', 'starred', 'sent', 'drafts', 'trash', 'junk' ];
Dragonfly.Mail.setLabels = {
    'all':     N_('All'),
    'inbox':   N_('Inbox'),
    'starred': N_('Starred'),
    'sent':    N_('Sent'),
    'drafts':  N_('Drafts'),
    'trash':   N_('Trash'),
    'junk':    N_('Junk')
};

Dragonfly.Mail.getData = function (loc)
{
    logDebug('Dragonfly.Mail.getData(loc) called.');
    var d = Dragonfly;
    var m = d.Mail;
    
    var view = m.getView(loc);
    if (view.getData)
    {
        logDebug('loc ', loc, ' has a getView function.');
        return view.getData(loc);
    }
    else
    {
        logDebug('No getView function - falling back to requestJSON.');
        return d.requestJSON ('GET', loc);
    }
}

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
    else if (loc.composing == true)
    {
        // We're faked into a conversation view if we're composing!
        ret = m.ConversationView;
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
    
    var skippy = false;
    
    var emailfrom = '';
    if (p.prefs.mail && p.prefs.mail.from)
    {
        emailfrom = p.prefs.mail.from;
    }

    if (emailfrom == '')
    {
        if (d.defaultMailAddress)
        {
            return d.defaultMailAddress;
        }
    }
    
    var namefrom = d.userName;
    if (p.prefs.mail && p.prefs.mail.sender)
    {
        namefrom = p.prefs.mail.sender;
    }
    
    return d.format("{0} <{1}>", namefrom, emailfrom || (d.userName + '@' + window.location.hostname));
};

Dragonfly.Mail.Preferences.setFromAddress = function (address)
{
    var d = Dragonfly;
    var p = d.Preferences;

    p.prefs.mail.from = address;
    p.save ();
};

Dragonfly.Mail.Preferences.wantsHtmlComposer = function()
{
    var p = Dragonfly.Preferences;

    // Make sure the user has some sort of composer prefs setup.
    if (p.prefs.composer)
    {
        return (p.prefs.composer.messageType || "html") == "html";
    }
    else
    {
        return true;
    }
};

Dragonfly.Mail.Preferences.getComposerWidth = function()
{
    var p = Dragonfly.Preferences;

    // Same as above :)
    if (p.prefs.composer)
    {
    	return p.prefs.composer.lineWidth || 80;
    }
    else
    {
        return 80;
    }
};

Dragonfly.Mail.Preferences.getAutoBcc = function ()
{
    var d = Dragonfly;
    var p = d.Preferences;

    if (p.prefs.mail)
    {
        return p.prefs.mail.autoBcc || '';
    }
    else
    {
        return '';
    }
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

    if (p.prefs.mail)
    {
        return p.prefs.mail.pageSize || 30;
    }
    else
    {
        return 30;
    }
};

Dragonfly.Mail.Preferences.getSignatureAvailable = function ()
{
    var p = Dragonfly.Preferences;
    
    if (p.prefs.mail)
    {
        return p.prefs.mail.usesig || false;
    }
    else
    {
        return false;
    }
}

Dragonfly.Mail.Preferences.getSignature = function ()
{
    var p = Dragonfly.Preferences;
    
    if (p.prefs.mail)
    {
        return p.prefs.mail.signature || '';
    }
    else
    {
        return '';
    }
}

Dragonfly.Mail.getFromAddress = function ()
{
    var d = Dragonfly;
    var m = d.Mail;
    var p = m.Preferences;

    return p.getFromAddress();
};

Dragonfly.Mail.updateToolbar = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.getView (loc);

    /* these aren't *exactly* right... */
    $('mail-toolbar-archive').value = (loc.set == 'all')     ? _('Unarchive') : _('Archive');
    $('mail-toolbar-delete').value  = (loc.set == 'trash')   ? _('Undelete')  : _('Delete');
    $('mail-toolbar-junk').value    = (loc.set == 'junk')    ? _('Not Junk')   : _('Junk');
    $('mail-toolbar-star').value    = (loc.set == 'starred') ? _('Unstar')    : _('Star');

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
                         '<option value="conversations">', _('All'), '</option>',
                         '<option value="tome">', _('To Me'), '</option>',
                         '<option value="contacts">', _('From My Contacts'), '</option>',
                         '<option value="subscriptions">', _('Mailing Lists'), '</option>',
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
                    
                             '<input type="button" id="mail-toolbar-archive" value="', _('Archive'), '">',
                             '<input type="button" id="mail-toolbar-junk" value="', _('Junk'), '">',
                             '<input type="button" id="mail-toolbar-delete" value="', _('Delete'), '">',
                             '<input type="button" id="mail-toolbar-star" value="', _('Star'), '">',

                             '<input type="button" id="mail-toolbar-select-all" value="', _('Select All'), '">',
                             '<input type="button" id="mail-toolbar-deselect-all" value="', _('Deselect All'), '">',

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

Dragonfly.Mail.Conversations = { label: N_('Conversations') };
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

    return m['build' + (loc.message ? _('Conversation') : _('List')) + _('View')] (loc);
};

Dragonfly.Mail.Messages.load = function (loc, jsob)
{
    var c = Dragonfly.Mail.Messages;
    var m = Dragonfly.Mail;

    return m['load' + (loc.message ? _('Item') : _('List'))] (loc, jsob);
};
*/

Dragonfly.Mail.Contacts = { label: N_('Contacts') };
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
                         loc.set == 'sent' ? _('To') + " " : _('From') + " ",
                         d.escapeHTML (ab.getNameForContact (loc.contact)));
    }
};

Dragonfly.Mail.Subscriptions = { label: N_('Mailing Lists') };
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

Dragonfly.Mail.ToMe = { label: N_('To Me') };
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

Dragonfly.Mail.yeahBaby = function (msg)
{
    var d = Dragonfly;
    
    if (!d.Preferences.prefs.mail.colorQuotes)
    {
        return d.htmlizeText (msg);
    }
    
    // Split the message up by newlines.
    msg = msg.split("\n");
    
    // Loop through and find quotes.
    for (i=0;i<msg.length;i++)
    {
        // Check if it starts with >
        if (msg[i].substring(0, 1) == ">")
        {
            // Loop and find out how deep we are.
            var depth = 0;
            for (x=0;x<msg[i].length;x++)
            {
                if (msg[i][x] == '>')
                {
                    depth++;
                }
            }
            
            // Index of d.Colors to use.
            var cdepth = depth;
            
            // Shrink the index if depth is larger than colour list.
            if (depth > d.Colors.length))
            {
                // Calculate the index to subsitute in for the same depth listing using the mod of the
                // depth index (not zero-based) and the length of colours array (number of colors, also
                // not zero-based).
                //
                // If that makes sense, you need a holiday :)
                
                cdepth = depth % d.Colors.length;
            }
            
            // Convert the index to a zero-based one, and use it to find the colour we want.
            var color = d.Colors[cdepth - 1];
            
            // Provide a class="" value, so that browser can override reply depth colour if they want.
            msg[i] = '<span class="mailreply-' + cdepth + '" style="color: ' + color + ';">' + d.htmlizeText (msg[i]) + '</span>';
        }
        else
        {
            msg[i] = d.htmlizeText (msg[i]);
        }
    }
    
    return msg.join("<br />");
}
