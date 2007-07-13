Dragonfly.Search = {
    defaultSet: 'all',
    defaultHandler: 'results'
};

Dragonfly.tabs['search'] = Dragonfly.Search;

Dragonfly.Search.handlers = { };
Dragonfly.Search.sets = [ 'all' ];
Dragonfly.Search.setLabels = { all: 'All' };

Dragonfly.Search.groups = [ ];
Dragonfly.Search.Group = function (label, name, baseLoc)
{
    if (!arguments.length) {
        return;
    }

    Dragonfly.Search.groups.push (this);

    this.label = label;
    this.name = name;
    this.baseLoc = baseLoc;
};

Dragonfly.Search.Group.prototype = { };

Dragonfly.Search.Group.prototype.reset = function ()
{
    delete this.defData;
};

Dragonfly.Search.Group.prototype.getLoc = function (loc)
{
    var d = Dragonfly;

    return new d.Location (merge (this.baseLoc, { query: loc.query, page: 1 }));
};

Dragonfly.Search.Group.prototype.build = function (html)
{
    var d = Dragonfly;

    html.push ('<table id="', this.name, '-search" class="conversation-list" cellspacing="0" cellpadding="0" style="display: none">',
               '<tr class="group-header"><td colspan="5">', d.escapeHTML (this.label), '</td></tr>',
               '<div id="', this.name, '-search-results"></div>',
               '<tr class="group-footer"><td id="', this.name, '-search-footer"></td></tr>',
               '</table>');
};

Dragonfly.Search.Group.prototype.getData = function (loc)
{
    var d = Dragonfly;

    if (this.dataDef) {
        this.dataDef.cancel();
    }

    loc = this.getLoc (loc);
    this.dataUrl = loc.getServerUrl();
    this.dataDef = d.requestJSON ('GET', this.dataUrl);
    return this.dataDef.addCallbacks (
        bind (function (res) {
                  this.data = res;
                  delete this.dataDef;
                  return res;
              }, this),
        bind (function (err) {
                  delete this.dataDef;
                  delete this.data;
                  delete this.dataUrl;
              }, this));
};

Dragonfly.Search.Group.prototype.load = function (loc, jsob)
{
    throw new Error ('Dragonfly.Search.Group.load() not implemented');
};

Dragonfly.Search.Group.prototype.showPage = function (loc)
{
    /*
     * if we are showing half-pages, this should check if the correct
     * data is loaded, and then simply load the right data if so.
     * otherwise get the data
     */
    return this.getData (loc).addCallback (
        bind (function (res) {
                  this.load (loc, res);
                  return res;
              }, this));
};

Dragonfly.Search.Contacts = new Dragonfly.Search.Group (
    'Contacts', 'contacts', 
    { tab: 'addressbook', set: 'all', handler: 'contacts' });

Dragonfly.Search.Contacts.build = function (html)
{
    var d = Dragonfly;
    var s = d.Search;

    if (!this.popup) {
        var popupholder = DIV();
        $('content').appendChild (popupholder);
        this.popup = new Dragonfly.AddressBook.ContactPopup (popupholder);
    }
    s.Group.prototype.build.apply (this, arguments);
};

Dragonfly.Search.Contacts.connectHtml = function (elem)
{
    var d = Dragonfly;

    //d.Search.Contacts.prototype.connectHtml.apply (this, arguments);
    //$('contacts-search-results').style.overflow = 'auto';
    logDebug('Observing contacts-search-results.');
    Event.observe ('contacts-search-results', 'click',
                   (function (evt) {
                       var elem = Event.element (evt);
                       logDebug('Element: ' + elem);
                       var card = d.findElement (elem, 'TR', $('contacts-search'));
                       logDebug('Card (element): ' + card);
                       var bongoId = card && card.getAttribute ('bongoid');
                       if (!bongoId) {
                           return;
                       }
                       this.popup.elem = card;
                       d.AddressBook.loadContact(bongoId).addCallback(bind ('summarize', this.popup));   
                   }).bindAsEventListener (this));

    return elem;
};


Dragonfly.Search.Contacts.load = function (loc, jsob)
{
    var d = Dragonfly;

    var html = new d.HtmlBuilder;
    var ret = false;
    if (jsob && jsob.contacts && jsob.contacts.length) {
        ret = true;
        var rowtype = 'even';
        
        forEach (jsob.contacts, function (card) {
                     html.push ('<tr id bongoid="', card.bongoId, '" class="', rowtype, '">');
                     html.push ('<td class="checkbox"><input type="checkbox"></td>');
                     
                     html.push ('<td class="star"><img src="img/contact-16.png" /></td>');
                     
                     html.push ('<td class="from">');
                     html.push ('<a>', d.escapeHTML (card.fn), '</a>');
                     html.push ('</td>');
                     
                     if (card.email && card.email.length) {
                        html.push ('<td class="subject" >');
                        html.push ('<a>', d.escapeHTML (card.email[0]), '</a>');
                        html.push ('</td>');
                     }
                     
                     html.push ('</td></tr>');
                     
                     if (rowtype == 'even')
                     {
                        rowtype = 'odd';
                     }
                     else
                     {
                        rowtype = 'even';
                     }
                 });
        if (jsob.total > jsob.contacts.length) {
            var page2 = this.getLoc (loc);
            page2.page = 2;
            Element.setHTML ('contacts-search-footer',
                             [ '<a href=#"', d.escapeHTML (page2.getClientUrl ()), '">',
                               jsob.total - jsob.contacts.length, ' More...</a>' ]);
        }
        
    }    
    html.set ('contacts-search-results');
    d.Search.Contacts.connectHtml();    
    (ret ? Element.show : Element.hide) ('contacts-search');
    return ret;
};

Dragonfly.Search.Events = new Dragonfly.Search.Group (
    'Events', 'events', 
    { tab: 'calendar', set: 'all', handler: 'events' });

Dragonfly.Search.Events.load = function (loc, jsob)
{
    var d = Dragonfly;
    var html = new d.HtmlBuilder;
    var ret = false;
    
    if (jsob && jsob.length)
    {
        ret = true;
        var rowtype = 'even';
        
        forEach (jsob, function (res) {
            html.push('<tr bongoId="', res.bongoId, '" class="', rowtype, '">');
            
            html.push ('<td class="checkbox"><input type="checkbox"></td>');
                     
            html.push ('<td class="star"><img src="img/calendar-16.png" /></td>');
            
            html.push ('<td class="from"></td>');
            
            html.push ('<td class="subject">',
                d.escapeHTML (res.occurrences[0].summary.value),
                '</td>');
            
            html.push('</tr>');
            
            if (rowtype == 'even')
            {
                rowtype = 'odd';
            }
            else
            {
                rowtype = 'even';
            }
        });
    }
    
    html.set ('events-search-results');
    (ret ? Element.show : Element.hide) ('events-search');
    return ret;
};

/*
Dragonfly.Search.Calendars = new Dragonfly.Search.Group (
    'Calendars', 'calendars',
    { tab: 'calendar', set: 'all', handler: 'calendars' });
Dragonfly.Search.Calendars.load = function (loc, jsob)
{
    var d = Dragonfly;
    var html = new d.HtmlBuilder;
    var ret = false;
    html.set ('calendars-search-results');
    (ret ? Element.show : Element.hide) ('calendars-search');
    return ret;
};
*/

Dragonfly.Search.Mail = new Dragonfly.Search.Group (
    'Mail', 'mail',
    { tab: 'mail', set: 'all', handler: 'conversations' });
Dragonfly.Search.Mail.load = function (loc, jsob)
{
    var d = Dragonfly;
    var m = d.Mail;
    var html = new d.HtmlBuilder;
    var ret = false;
    if (jsob && jsob.conversations && jsob.conversations.length) {
        ret = true;
        var subLoc = this.getLoc (loc);
        new d.Mail.ConversationList (html, jsob.conversations, subLoc.getClientUrl ('handler') + '/-/{0}');
        if (jsob.total > jsob.conversations.length) {
            var page2 = this.getLoc (loc);
            page2.page = 2;
            Element.setHTML ('mail-search-footer',
                             [ '<a href="#', d.escapeHTML (page2.getClientUrl ()), '">',
                               jsob.total - jsob.conversations.length, ' More...</a>' ]);
        }

    }
    html.set ('mail-search-results');
    (ret ? Element.show : Element.hide) ('mail-search');
    return ret;
};

Dragonfly.Search.Results = { };
Dragonfly.Search.handlers['results'] = Dragonfly.Search.Results;

Dragonfly.Search.Results.parsePath = function (loc, path)
{
    loc.valid = true;
    return loc;
};

Dragonfly.Search.Results.build = function (loc)
{
    var d = Dragonfly;
    var s = d.Search;

    if (!$('search-wrapper')) {
        Element.setHTML ('toolbar', '');
        Element.hide ('toolbar');

        var html = new d.HtmlBuilder ('<div id="search-wrapper" class="scrollv">');

        for (var i = 0; i < s.groups.length; i++) {
            s.groups[i].build (html);
        }


        html.push ('<div id="search-no-results"><table style="margin: auto;"><tr>',
                   '<td><img src="img/sad.png" style="float: left;" /></td>',
                   '<td style="vertical-align:middle;"><h3>', _('Sorry, but your query returned no results.'), '</h3>',
                   _('Here are some tips for getting better results:'),
                   '<ul style="font-size: 80%; margin-left: 3em;"><li style="list-style-type: disc;">',
                   _('Make sure all words are spelt correctly.'), '</li><li style="list-style-type: disc;">',
                   _('Try different keywords.'), '</li><li style="list-style-type: disc;">',
                   _('Be less specific in your search.'), '</li></ul></td>',
                   '</tr></table></div>');

        html.set ('content-iframe');
    }
};

Dragonfly.Search.Results.getScrolledElement = function (loc)
{
    return 'search-wrapper';
};

Dragonfly.Search.Results.getData = function (loc)
{
    var d = Dragonfly;
    var s = d.Search;

    var def = new d.MultiDeferred();

    for (var i = 0; i < s.groups.length; i++) {
        def.addDeferred (s.groups[i].getData (loc));
    }
    return def;
};

Dragonfly.Search.Results.load = function (loc, jsob)
{
    var d = Dragonfly;
    var s = d.Search;

    document.title = d.format(_('Search results for {0}'), d.escapeHTML (loc.query)) + ': ' + Dragonfly.title;
    var results = false;

    var html = new d.HtmlBuilder;
    for (var i = 0; i < s.groups.length; i++) {
        results = s.groups[i].load (loc, s.groups[i].data) || results;
    }
    (results ? Element.hide : Element.show) ('search-no-results');
};
