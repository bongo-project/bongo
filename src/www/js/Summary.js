Dragonfly.Summary = {
    defaultSet: 'inbox',
    defaultHandler: 'summary'
};
Dragonfly.tabs['summary'] = Dragonfly.Summary;

Dragonfly.Summary.handlers = { };
Dragonfly.Summary.sets = [ 'inbox' ];
Dragonfly.Summary.setLabels = {
    'inbox': 'Inbox'
};

Dragonfly.Summary.build = function (loc)
{
    var d = Dragonfly;
    var s = d.Summary;

    if (!$('summary-view')) {
        Element.setHTML ('toolbar', '');
        Element.hide ('toolbar');

        var loc = new d.Location ({ tab: 'mail' });

        loc.set = 'starred';
        var starredUrl = d.escapeHTML (loc.getClientUrl());

        loc.set = 'inbox';
        loc.handler = 'tome';
        var tomeUrl = d.escapeHTML (loc.getClientUrl());

        loc.handler = 'contacts';
        var contactsUrl = d.escapeHTML (loc.getClientUrl());

        loc.handler = 'subscriptions';
        var subsUrl = d.escapeHTML (loc.getClientUrl());

        Element.setHTML ('content-iframe', 
                         ['<div class="scrollv" id="summary-view">',
                          '<table id="summary-table"> ',
                          '<tbody> <tr>',
                          '<td class="summary-column"> ',
                          '<div id="starred-summary" class="summary-category mail-summary"> ',
                          '<h3><a href="#', starredUrl, '">Starred Mail</a></h3> ',
                          '<ul id="starred-list"></ul>',
                          '</div> ',
                          '<div id="tome-summary" class="summary-category mail-summary"> ',
                          '<h3><a href="#', tomeUrl, '">Mail To Me</a></h3> ',
                          '<ul id="tome-list"></ul>',
                          '</div> ',
                          '<div id="frommycontacts-summary" class="summary-category mail-summary"> ',
                          '<h3><a href="#', contactsUrl, '">Mail From My Contacts</a></h3> ',
                          '<ul id="frommycontacts-list"></ul>',
                          '</div> ',
                          '<div id="subscriptions-summary" class="summary-category mail-summary"> ',
                          '<h3><a href="#', subsUrl, '">Mail To Mailing lists</a></h3> ',
                          '<ul id="subscriptions-list"></ul>',
                          '</div> ',
                          '<div id="nomail-summary" style="display: none;">You have no new mail.</div>',
                          '</td> ',
                          '<td class="summary-column"> ',
                          '<div id="invites-summary" class="summary-category cal-summary">',
                          '<h3>Invitations</h3>',
                          '<div id="summary-cal-invites"></div>',
                          '<div id="summary-event-invites"></div>',
                          '</div>',
                          '<div class="summary-category cal-summary">',
                          '<h3>Upcoming Events</h3>',
                          '<div id="summary-cal"></div>',
                          '</div>',
                          '</td> ',
                          '</tr> </tbody> ',
                          '</table> ',
                          '</div>']);

        var today = Day.get();
        Dragonfly.Calendar.buildCellView ('summary-cal', 'sumcal', today,
            today, today.offsetBy(5), 2, '#~' + d.userName + '/calendar/day/-/');

        Event.observe ('summary-cal-invites', 'click',
                       s.calInvitesClicked.bindAsEventListener (null),
                       true);

        Event.observe ('summary-event-invites', 'click',
                       s.eventInvitesClicked.bindAsEventListener (null),
                       true);
    }
    document.title = 'Summary: ' + d.title;
};

Dragonfly.Summary.getScrolledElement = function (loc)
{
    return 'summary-view';
};

Dragonfly.Summary.calInvitesClicked = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var s = d.Summary;

    var elem = Event.element (evt);
    if (elem.nodeName != 'A') {
        return;
    }

    Event.stop (evt);

    var bongoId = elem.getAttribute ('bongoid');
    if (!bongoId) {
        return;
    }
    var invite = s.calInvites[bongoId];
    if (!invite) {
        return;
    }
    var pop = new c.CalendarInvitationPopup (invite, elem);
    pop.showBelow (elem);
};

Dragonfly.Summary.eventInvitesClicked = function (evt)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var j = d.JCal;
    var s = d.Summary;

    var elem = Event.findElement (evt, 'A');
    if (!elem) {
        return;
    }

    Event.stop (evt);
    if (!s.eventInvites || !s.eventInvites.length) {
        return;
    }

    var bongoId = elem.getAttribute ('bongoid');
    if (!bongoId) {
        return;
    }

    var jcal;
    for (var i = 0; i < s.eventInvites.length; i++) {
        jcal = s.eventInvites[i];
        if (jcal.bongoId == bongoId) {
            break;
        }
    }
    if (i == s.eventInvites.length) {
        return;
    }

    if (!jcal.event) {
        return;
    }
    var pop = new c.EventInvitationPopup (jcal, elem);
    pop.showBelow (elem);
};


Dragonfly.Summary.addConversationList = function (loc, jsob, set, name)
{
    var d = Dragonfly;
    var html = [];
    var i = 0;

    if (!jsob[name] || !jsob[name].conversations || !jsob[name].conversations.length) {
        Element.hide (name + '-summary');
        return false;
    }

    var conversationLoc = new d.Location (loc);
    conversationLoc.tab = 'mail';
    conversationLoc.set = set;
    conversationLoc.handler = 'conversations';

    jsob[name].conversations.sort (
        function (a, b) {
            return a.props.date > b.props.date ? -1 : a.props.date < b.props.date ? 1 : 0;
        });

    jsob[name].conversations.each (
        function (item) {
            var className = i++ % 2 ? 'even' : 'odd';
            if (!item.props.flags.read) {
                className = className + ' unread';
            }

            html.push ('<li class="', className, '">');

            conversationLoc.conversation = item.bongoId;
            
            html.push ('<a href="#', escapeHTML (conversationLoc.getClientUrl ()), '">');
            
            if (item.props.flags.starred) {
                html.push ('<img src="img/star.png">');
            }                
            html.push ('<span class="summary-from">');
            html.push (d.escapeHTML (item.props.from));
            html.push ('</span>');
            html.push ('<span class="summary-subject">');
            html.push (d.escapeHTML (item.props.subject));
            html.push ('</span></a></li>');
        });

    /*
    if (jsob[name].conversations.length < jsob[name].total) {
        delete conversationLoc.conversation;
        html.push ('<span class="summary-more"><a href="#', escapeHTML (conversationLoc.getClientUrl ()), '">');
        html.push ('... and ' + (jsob[name].total - jsob[name].conversations.length) + ' More</a></span>');
    }   
    */
    Element.setHTML (name + '-list', html);
    Element.show (name + '-summary');
    return true;
};

Dragonfly.Summary.addFromMyContacts = function (loc, jsob)
{
    var d = Dragonfly;
    var a = d.AddressBook;
    var html = [];

    // we have to iterate through contacts twice to sort
    var contacts = [];
    for (var bongoId in jsob.frommycontacts) {
        var item = jsob.frommycontacts[bongoId];
        item.bongoId = bongoId;
        contacts.push(item);
    }

    if (!contacts.length) {
        Element.hide ('frommycontacts-summary');
        return false;
    }

    var contactLoc = new d.Location (loc);
    contactLoc.tab = 'mail';
    contactLoc.set = 'inbox';
    contactLoc.handler = 'contacts';

    contacts.sort(a.compareContacts);
    
    for (var i = 0; i < contacts.length; i++) {
        var item = contacts[i];
        
        var className = i % 2 ? 'even unread' : 'odd unread';
        
        contactLoc.contact = item.bongoId;

        html.push ('<li class="' + className + '">');
        html.push ('<a href="#', escapeHTML (contactLoc.getClientUrl ()), '">');
        if (item.flags.starred) {
            html.push ('<img src="img/star.png">');
        }

        html.push (escapeHTML(item.fn));
        html.push (' (' + item.count + ')');
        html.push ('</a></li>');
    }

    Element.setHTML ('frommycontacts-list', html);
    Element.show ('frommycontacts-summary');

    return true;
};  

Dragonfly.Summary.addSubscriptions = function (loc, jsob)
{
    var d = Dragonfly;
    var html = [];

    var listLoc = new d.Location (loc);
    listLoc.tab = 'mail';
    listLoc.set = 'inbox';
    listLoc.handler = 'subscriptions';
    
    for (var list in jsob.lists) {
        var item = jsob.lists[list];
        var count = item.count;
        var className = i % 2 ? 'even unread' : 'odd unread';

        listLoc.listId = item.listId;
        
        html.push ('<li class="', className, '">');
        html.push ('<a href="#', escapeHTML (listLoc.getClientUrl ()), '">');
        html.push (escapeHTML (item.displayName), ' (' + count + ')');
        html.push ('</a></li>');
    }

    if (!html.length) {
        Element.hide ('subscriptions-summary');
        return false;
    }

    Element.setHTML ('subscriptions-list', html);
    Element.show ('subscriptions-summary');
    return true;
};  

Dragonfly.Summary.addCalInvites = function (loc, jsob)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var s = d.Summary;

    s.calInvites = jsob.calInvites;
    var calKeys = s.calInvites && keys (s.calInvites);

    if (!calKeys || !calKeys.length) {
        s.calInvites = null;
        Element.hide ('summary-cal-invites');
        return false;
    }

    var html = new d.HtmlBuilder ('<ul>');
    for (var i = 0; i < calKeys.length; i++) {
        var bongoId = calKeys[i];
        if (typeof s.calInvites[bongoId] == 'function') {
            return;
        }
        var cal = s.calInvites[bongoId];
        html.push ('<li><a bongoid="', bongoId, '">',
                   d.escapeHTML (cal.calendarName),
                   ' from ',
                   d.escapeHTML (cal.from[0][0]),
                   '</a></li>');
    }
    html.push ('</ul>');
    html.set ('summary-cal-invites');
    Element.show ('summary-cal-invites');
    return true;
};

Dragonfly.Summary.addEventInvites = function (loc, jsob)
{
    var d = Dragonfly;
    var c = d.Calendar;
    var j = d.JCal;
    var s = d.Summary;

    if (!jsob.eventInvites || !jsob.eventInvites.length) {
        s.eventInvites = null;
        Element.hide ('summary-event-invites');
        return false;
    }
    var html = new d.HtmlBuilder ('<ul>');
    s.eventInvites = [ ];
    for (var i = 0; i < jsob.eventInvites.length; i++) {
        var invite = jsob.eventInvites[i];
        var jcal = new j.VCalendar (jsob.eventInvites[i]);
        s.eventInvites.push (jcal);

        jcal.from = invite.from;
        jcal.subject = invite.subject;
        jcal.convId = invite.convId;

        if (jcal.event) {
            html.push ('<li><a bongoid="', jcal.bongoId, '">',
                       '<span class="invite-from">',
                       d.escapeHTML (jcal.from),
                       '</span>',
                       '<span class="invite-subject">',
                       d.escapeHTML (jcal.event.summary),
                       '</span>',
                       '<span class="invite-date">',
                       d.escapeHTML (jcal.occurrences[0].formatRange()),
                       '</span>',
                       '</a></li>');
        }
    }
    html.push ('</ul>');
    html.set ('summary-event-invites');
    Element.show ('summary-event-invites');
    return true;
};

Dragonfly.Summary.load = function (loc, jsob)
{
    var d = Dragonfly;
    var s = d.Summary;
    
    var hasMail = false;
    var hasInvites = false;

    hasMail = s.addConversationList (loc, jsob, "starred", "starred");
    hasMail = s.addConversationList (loc, jsob, "tome", "tome") || hasMail;
    hasMail = s.addFromMyContacts (loc, jsob) || hasMail;
    hasMail = s.addSubscriptions (loc, jsob) || hasMail;

    $('nomail-summary').style.display = hasMail ? 'none' : '';

    hasInvites = s.addCalInvites (loc, jsob);
    hasInvites = s.addEventInvites (loc, jsob) || hasInvites;

    $('invites-summary').style.display = hasInvites ? '' : 'none';

	d.Calendar.clear();
	d.Calendar.layoutEvents (jsob.events);
};

Dragonfly.Summary.Summary = { };

Dragonfly.Summary.handlers['summary'] = Dragonfly.Summary.Summary;

Dragonfly.Summary.Summary.parsePath = function (loc, path)
{
    loc.valid = true;    
    return loc;
};

Dragonfly.Summary.Summary.getServerUrl = function (loc, path)
{
    return false;
};

Dragonfly.Summary.Summary.getClientUrl = function (loc, path)
{
    return loc;
};

Dragonfly.Summary.Summary.build = function (loc)
{
    return Dragonfly.Summary.build (loc);
};

Dragonfly.Summary.Summary.load = function (loc, jsob)
{
    return Dragonfly.Summary.load (loc, jsob);
};
