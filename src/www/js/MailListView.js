Dragonfly.Mail.ListView = { };

Dragonfly.Mail.ListView.getSelectedIds = function ()
{
    var d = Dragonfly;
    var m = d.Mail;

    var guids = [ ];
    var rows = $('content-iframe').getElementsByTagName ('input');
    for (var i = 0; i < rows.length; i++) {
        if (!rows[i].checked) {
            continue;
        }
        for (var tr = rows[i].parentNode; tr && tr.nodeName.toLowerCase() != 'tr'; tr = tr.parentNode) {
            ;
        }
        if (!tr) {
            continue;
        }
        var bongoId = tr.getAttribute ('bongoId');
        if (bongoId) {
            guids.push (bongoId);
        }
    }
    return guids;
};

Dragonfly.Mail.ListView.checkAll = function (checked)
{
    var d = Dragonfly;
    var m = d.Mail;

    forEach ($('content-iframe').getElementsByTagName ('input'),
             function (row) {
                 row.checked = checked;
             });
};

Dragonfly.Mail.ListView.selectAll   = partial (Dragonfly.Mail.ListView.checkAll, true);
Dragonfly.Mail.ListView.deselectAll = partial (Dragonfly.Mail.ListView.checkAll, false);

Dragonfly.Mail.ListView.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;

    // Element.setHTML ('content-iframe', '');
};

Dragonfly.Mail.ListView.getData = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;

    var pageSize = m.Preferences.getPageSize ();

    var propsStart = ((loc.page || 1) - 1) * pageSize;

    return d.requestJSONRPC ('getConversationList', loc,
                             propsStart,
                             propsStart + pageSize - 1,
                             propsStart,
                             propsStart + pageSize - 1,
                             true);
};

Dragonfly.Mail.ListView.load = function (loc, jsob)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = d.Calendar;

    var force = false;

    var html = new d.HtmlBuilder ('<div class="scrollv" id="mail-list-wrapper">');
    var pageSize = m.Preferences.getPageSize ();
            
    document.title = _(loc.set) + ' - ' + _(loc.tab) + ': ' + Dragonfly.title;

    var list = new m.ConversationList;
    list.buildStart (html);

    var pageFmt;
    if (loc.contact) {
        list.addGroupHeader (html, loc.getBreadCrumbs());
        list.urlFmt = loc.getClientUrl ('handler') + '/-/' + loc.contact + '/{0}';
        pageFmt = loc.getClientUrl ('handler') + '/page{0}/' + loc.contact;
    } else if (loc.listId) {
        list.addGroupHeader (html, loc.getBreadCrumbs());
        list.urlFmt = loc.getClientUrl ('handler') + '/-/' + loc.listId + '/{0}';
        pageFmt = loc.getClientUrl ('handler') + '/page{0}/' + loc.listId;
    } else {
        list.addGroupHeader (html, loc.getBreadCrumbs());
        list.urlFmt = loc.getClientUrl ('handler') + '/-/{0}';
        pageFmt = loc.getClientUrl ('handler') + '/page{0}';
    }
    
    if (jsob.conversations && jsob.conversations.length) {
        list.addConversations (html, jsob.conversations);
    } else {
        list.addGroupFooter (html,
                             '<span class="empty">' + _('No messages here.') + '</span>');
    }

    list.buildEnd (html);

    new d.Pager (html, pageFmt,
                 loc.page, Math.ceil (jsob.total / pageSize));

    html.push ('</div>');
    html.set ('content-iframe');
};

Dragonfly.Mail.ListView.getScrolledElement = function (loc)
{
    return 'mail-list-wrapper';
};

/* ******************************** */

Dragonfly.Mail.MultiListView = { };
Dragonfly.Mail.MultiListView.getSelectedIds = Dragonfly.Mail.ListView.getSelectedIds;

Dragonfly.Mail.MultiListView.selectAll   = Dragonfly.Mail.ListView.selectAll;
Dragonfly.Mail.MultiListView.deselectAll = Dragonfly.Mail.ListView.deselectAll;

Dragonfly.Mail.MultiListView.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;

    // Element.setHTML ('content-iframe', '');
};

Dragonfly.Mail.MultiListView.getData = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;

    var pageSize = m.Preferences.getPageSize ();
    
    var propsStart = ((loc.page || 1) - 1) * pageSize;

    return d.requestJSONRPC ('getConversationList', loc,
                             propsStart,
                             propsStart + pageSize - 1,
                             propsStart,
                             propsStart + pageSize - 1,
                             true);
};

Dragonfly.Mail.MultiListView.load = function (loc, jsob)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = d.Calendar;
    var ab = d.AddressBook;

    var force = false;

    loc = new d.Location (loc);

    var html = new d.HtmlBuilder ('<div class="scrollv" id="mail-list-wrapper">');
    var pageSize = m.Preferences.getPageSize ();
    
    document.title = _(loc.set) + ' - ' + _(loc.tab) + ': ' + Dragonfly.title;

    var list = new m.ConversationList;
    list.buildStart (html);
    list.addGroupHeader (html, loc.getBreadCrumbs());

    var subgroup = loc.handler == 'contacts' ? 'contact' : 'listId';
    var handler = m.handlers[loc.handler];

    var urlBase = loc.getClientUrl ('handler');
    for (var i = 0; i < jsob[loc.handler].length; i++) {
        var group = jsob[loc.handler][i];

        loc[subgroup] = group[subgroup];
        list.addGroupHeader (html, handler.getBreadCrumbs (loc));

        if (!group.total) {
            list.addGroupFooter (html,
                                 '<span class="empty">' + _('No messages here.') + '</span>');
            continue;
        }

        list.urlFmt = urlBase + '/-/' + encodeURIComponent(group[subgroup]) + '/{0}';
        list.addConversations (html, group.conversations);
        if (group.total > group.conversations.length) {
            list.addGroupFooter (html,
                                 d.format ('<a href="#{0}/page1/{1}">{2} {3}</a>', 
                                           urlBase, encodeURIComponent (group[subgroup]),
                                           group.total - group.conversations.length),
                                           _('more...'));
        }

    }

    list.buildEnd (html);

    pageFmt = loc.getClientUrl ('handler') + '/page{0}';

    new d.Pager (html, pageFmt, loc.page, Math.ceil (jsob.total / pageSize));

    html.push ('</div>');
    html.set ('content-iframe');
};

Dragonfly.Mail.MultiListView.getScrolledElement = function (loc)
{
    return 'mail-list-wrapper';
};
