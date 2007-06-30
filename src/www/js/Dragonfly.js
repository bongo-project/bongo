Dragonfly = { 
    title: 'Bongo', 
    version: 'SVN r2483+', 
    userName: '', 
    tabs: { }, 
    NAME: 'Dragonfly'
};

Dragonfly.updateTabs = function (loc)
{
    var d = Dragonfly;

    var tabs = keys (d.tabs);
    for (var i = 0; i < tabs.length; i++) {
        tab = tabs[i];
        if (!$(tab + '-tab')) {
            continue;
        }
        if (tab != loc.tab) {
            Element.removeClassName (tab + '-tab', 'selected');
        }
    }

    Element[loc.tab == 'search' ? 'show' : 'hide']('sidebar-select-search');
    $('sidebar-select').value = loc.tab;

    if (!loc.tab) {
        return;
    }

    d.tab = loc.tab + '-tab';
    if (!$(d.tab)) {
        return;
    }

    $(d.tab + '-href').href = '#' + loc.getClientUrl ();
    Element.addClassName (d.tab, 'selected');
};

Dragonfly.sidebarSelectChanged = function (evt)
{
    var d = Dragonfly;

    var elem = Event.element (evt);

    var tab = $(elem.value + '-tab-href');
    if (!tab) {
        return;
    }
    
    d.go (tab.href);
};

Dragonfly.setCurrentTzid = function (tzid)
{
    var d = Dragonfly;
    
    if (tzid != d.curTzid) {
        d.curTzid = tzid;
        if (d.curLoc) {
            d.go (d.curLoc);
        }
    }
}

Dragonfly.setLoc = function (loc)
{
    var d = Dragonfly;
    var c = d.Calendar;

    if (typeof loc == 'string') {
        loc = new d.Location (loc);
    }

    d.prevLoc = d.curLoc;
    d.curLoc = loc;

    d.stopCheckingForUrlChanges ();

    d.updateTabs (loc);
    //d.updateSourcebar (loc);
    if ($('source')) {
        $('source').href = loc.getServerUrl ();
    }
    $('search-entry').value = loc.query || '';

    if (loc.tab != 'calendar' && d.prevLoc && d.prevLoc.tab == 'calendar') {
        d.sideCal.relabel ();
    }
};

Dragonfly.checkForUrlChanges = function ()
{
    var d = Dragonfly;

    if (d.locChangeId) {
        return;
    }
    if (d.isIE) {
        logDebug ('adding history event listener...');
        Event.observe ('history-iframe', 'load',
                       d.checkLocationChanged);
        d.locChangeId = true;
    } else if (!d.isWebkit || d.webkitVersion >= 420) {
        logDebug ('Checking for url changes...');
        d.locChangeId = window.setInterval (d.checkLocationChanged, 250, null);
    }
};

Dragonfly.stopCheckingForUrlChanges = function ()
{
    var d = Dragonfly;

    if (!d.locChangeId) {
        return;
    }

    if (d.isIE) {
        logDebug ('removing history event listener...');
        Event.stopObserving ('history-iframe', 'load',
                             d.checkLocationChanged);
    } else {
        logDebug ('No longer checking for url changes.');
        window.clearInterval (d.locChangeId);
    }
    delete d.locChangeId;
};

Dragonfly.updateLocation = function (loc)
{
    var d = Dragonfly;

    loc = loc || d.curLoc;

    /* add a mechanism for locations to have temporary (not in
     * history) urls */
    var url = loc.getClientUrl ();
    if (url) {
        if (d.webkitVersion < 420) { // setting the hash makes Safari 2.0.x unhappy
            document.webkitKludge.action = '#' + url;
            document.webkitKludge.submit();
        } else if (d.isIE) {
            location.hash = url;
            // add a .txt so ie doesn't think this is a malicious download url
            $('history-iframe').setAttribute ('src', 'history?' + url + '.txt');
            logDebug ('history iframe src:', $('history-iframe').getAttribute ('src'));
        } else {
            location.hash = url;
        }
        d.currentView = '#' + url;
        d.checkForUrlChanges ();
        // Click on the logo div to reload the current client data
        $('logo').setAttribute ('href', d.currentView);
    } else {
        d.stopCheckingForUrlChanges ();
    }
};

Dragonfly.getCurrentTab = function ()
{
    return Dragonfly.curLoc ? Dragonfly.curLoc.getTab () : null;
};

Dragonfly.getCurrentHandler = function ()
{
    return Dragonfly.curLoc ? Dragonfly.curLoc.getHandler () : null;
};

Dragonfly.getCurrentTzid = function ()
{
    return Dragonfly.curTzid;
};

Dragonfly.clearNotify = function ()
{
    hideElement ('notification');
    Element.setHTML ('notification', '');
};

Dragonfly._startNotifyTimer = function ()
{
    var d = Dragonfly;
    var self = arguments.callee;

    if (self.def) {
        self.def.cancel ();
    }
    self.def = callLater (5,
                          function (res) {
                              delete self.def;
                              Dragonfly.clearNotify();
                              return res;
                          });
};

Dragonfly.notify = function (msg, indefinite)
{
    var d = Dragonfly;

    Element.setHTML ('notification', msg);
    Element.removeClassName ('notification', 'error');
    showElement ('notification');
    if (!indefinite) {
        d._startNotifyTimer ();
    }
};

Dragonfly.notifyError = function (msg, err, indefinite)
{
    var d = Dragonfly;
    if (err) {
        if (msg) {
            msg += ': ';
        }
        msg += d.escapeHTML (d.reprError (err));
    }
    Element.setHTML ('notification', msg);
    Element.addClassName ('notification', 'error');
    showElement ('notification');
    if (!indefinite) {
        d._startNotifyTimer ();
    }
};

Dragonfly.updateSourcebar = function (loc, force)
{
    var d = Dragonfly;

    var sets = loc.getTab().sets;

    var selected = loc.set ? (loc.tab + '-source-' + loc.set) : null;
    var foundSelected = false;

    if (selected && !$(selected)) {
        return;
    }

    var tabs = document.getElementsByClassName ('selected', 'sourcebar');
    for (var i = 0; i < tabs.length; i++) {
        if (selected && tabs[i].id == selected) {
            foundSelected = true;
        } else {
            Element.removeClassName (tabs[i], 'selected');
        }
    }

    if (selected && !foundSelected) {
        Element.addClassName (selected, 'selected');
    }

    if (!force && d.prevLoc && d.prevLoc.tab == loc.tab && d.prevLoc.handler == loc.handler) {
        return;
    }

    if (loc.tab == 'calendar') {
        var tmpLoc = new d.Location (loc);
    } else {
        var tmpLoc = new d.Location ({ tab: loc.tab, set: loc.set, handler: loc.handler, query: loc.query });    
    }
    sets.each (
        function (set) {
            tmpLoc.set = set;
            var href = '#' + tmpLoc.getClientUrl ();
            $(loc.tab + '-source-' + set).setAttribute ('href', href);
            $(loc.tab + '-source-' + set + '-href').href = href;
        });
    Element.show ('sourcebar');
};

Dragonfly.buildSourcebar = function (loc)
{
    var d = Dragonfly;
    var v = loc.getTab();

    if (loc.tab != 'mail') {
        Element.hide ('sourcebar');
        return;
    }

    if (!$(loc.tab + '-source-' + loc.set)) {
        var html = new d.HtmlBuilder('<ul>');
        for (var i = 0; i < v.sets.length; i++) {
            var col = v.sets[i];
            var id = loc.tab + '-source-' + col;
            html.push ('<li id="', id, '"><a id="', id, '-href" href="#', loc.getClientUrl(), '">');
            html.push (_(v.setLabels[col]), '</a></li>');
        }
        html.push ('</ul>');
        html.set ('sourcebar');
    }
    
    d.updateSourcebar (loc, true);
};

Dragonfly.go = function (path)
{
    var d = Dragonfly;
    var loc;

    var self = arguments.callee;

    function emptyTab () {
        Element.setHTML ('content-iframe', '');
        return fail ();
    }

    var profile = false;

    var prof;
    var fprof;
    var dprof;
    var cprof;
    
    prof = new d.Profiler ('go', true);
    if (profile) {
        fprof = new d.Profiler ('Format', true);
        dprof = new d.Profiler ('Day', true);
        cprof = new d.Profiler ('Calendar', true);
        Day.resetCacheStats ();
    }

    var startDate = new Date();

    path = path || d.curLoc;
    if (path instanceof d.Location) {
        loc = path;
        path = loc.getClientUrl();
    } else {
        loc = new d.Location (path);
    }

    if (self.def) {
        logDebug ('Trying to cancel def: ' + self.def.loc.getClientUrl ());
        self.def.cancel ();
    }

    var def = d.contentIFrameZone.canDisposeZone ();
    if (def instanceof Deferred) {
        return def.addCallback (partial (d.go, path));
    } else if (!def) {
        return;
    }
    d.contentIFrameZone.disposeZone();

    //logDebug (startDate + ': Loading ', path, ':', serializeJSON (loc));
    //logDebug ('    from: ', d.currentView);
    //logDebug ('    location: ', window.location);
    //logDebug ('    hash: ', window.location.hash);
    //logDebug ('    loc: ', serializeJSON(loc));

    if (!loc || !loc.valid) {
        logError ('invalid path: ' + path);
        return emptyTab ();
    }

    d.setLoc (loc);

    var v = d.tabs[loc.tab];
    if (!v) {
        logError ('no tab for ' + loc.tab);
        return emptyTab ();
    }

    var h = v.handlers[loc.handler];
    if (!h) {
        logError ('no handler for ' + loc.handler);
        return emptyTab ();
    }

    prof.toggle ('request');
    if (h.getData) {
        self.def = h.getData (loc);
    } else {
        self.def = d.requestJSON ('GET', loc);
    }
    
    self.def.loc = loc;
    return self.def.addCallbacks (
        function (jsob) {
            var netFinish = new Date;
            prof.toggle ('request');
            
            try {
                prof.toggle ('buildSourceBar');
                d.buildSourcebar (loc);
                prof.toggle ('buildSourceBar');
                
                prof.toggle ('build');
                h.build (loc);
                prof.toggle ('build');
                
                prof.toggle ('load');
                h.load (loc, jsob);
                prof.toggle ('load');
                
                prof.toggle ('resizeScrolledView');
                var elem = d.resizeScrolledView (loc);
                // callLater (0.001, bind ('focus', $(elem)));
                prof.toggle ('resizeScrolledView');
            } catch (e) {
                logError ('Exception loading ' + path + ': ' + d.reprError (e));
                delete self.def;
                return e;
            }
            
            prof.toggle ('updateLocation');
            d.updateLocation();
            prof.toggle ('updateLocation');
            
            var finishDate = new Date;
            
            if (profile) {
                dprof.dump (false, netFinish, finishDate);
                Day.printCacheStats();
                
                fprof.dump (false, netFinish, finishDate);
                cprof.dump (false, netFinish, finishDate);
                
                prof.dump (true, startDate, finishDate);
            }
            
            logDebug ('Finished loading', path,
                      '(' + (finishDate - startDate) / 1000 + 's; local:',
                      (finishDate - netFinish) / 1000 + 's =',
                      Math.round (100 * (finishDate - netFinish) / (finishDate - startDate)),
                      'percent)');
            
            delete self.def;
            return jsob;
        },
        function (err) {
            if (!(err instanceof CancelledError)) {
                d.notifyError ('Could not load: ' + d.displayError (err));
            }
            delete self.def;
            return err;
        });
};

Dragonfly.search = function (queryString)
{
    var d = Dragonfly;
    var loc;

    if (queryString == "" || queryString == null) {
        if (!d.curLoc || d.curLoc.tab == 'search') {
            loc = new d.Location ({ valid: true, tab: 'summary' });
        } else {
            loc = new d.Location (d.curLoc);
            delete loc.query;
        }
    } else {
        loc = new d.Location ({ valid: true, tab: 'search', query: queryString });
    }
    return d.go(loc);
};

Dragonfly.checkLocationChanged = function ()
{
    var d = Dragonfly;
    var winloc;
    var doc;
    if (d.isIE) {
        doc = d.getIFrameDocument ('history-iframe');
        if (!doc || !doc.body) {
            return;
        }
        winloc = '#' + Element.getText (doc.body).replace (/\.txt$/, '');
        if (window.event) {
            logDebug ('got winloc:', winloc);
        }
    } else {
        winloc = window.location.toString ();
    }
    var hashIdx = winloc.indexOf ('#');

    if (hashIdx != -1 && winloc.substr (hashIdx) != d.currentView) {
        logWarning ('location has changed: ' + winloc.substr (hashIdx) + ' != ' + d.currentView);
        d.go (winloc.substr(hashIdx));
    } else if (d.isIE) {
        // update the history's title
        doc.title = document.title;
    }
};

Dragonfly.getEventHref = function (evt)
{
    var d = Dragonfly;

    var elem = Event.element (evt);
    var href;
    
    switch (elem.nodeName.toLowerCase ()) {
    case 'input':
        return null;
    }

    do {
        if (elem.getAttribute) {
            href = elem.getAttribute ('href');
            if (href) {
                break;
            }
        } else {
            elem = null;
            break;
        }
    } while ((elem = elem.parentNode));

    return elem;
};

Dragonfly.isMailtoUrl = function (href)
{
    return href.substr (0, 7) == 'mailto:';
};

Dragonfly.isLocalUrl = function (href)
{
    return href.charAt (0) == '/' || href.charAt (0) == '#' || href.indexOf (location.protocol + '//' + location.host + '/') == 0;
};

Dragonfly.isDragonflyUrl = function (href)
{
    var d = Dragonfly;

    var hashIdx = href.indexOf ('#');
    switch (hashIdx) {
    case -1:
        return false;
    case 0:
        return true;
    default:
        return location.toString ().substr (0, hashIdx) == href.substr (0, hashIdx);
    }
};

Dragonfly.UnknownUrl   = 0;
Dragonfly.MailUrl      = 1;
Dragonfly.LocalUrl     = 2;
Dragonfly.DragonflyUrl = 3;

Dragonfly.getUrlType = function (href)
{
    var d = Dragonfly;
    if (d.isMailtoUrl (href)) {
        return d.MailUrl;
    }
    if (d.isLocalUrl (href)) {
        if (d.isDragonflyUrl (href)) {
            return d.DragonflyUrl;
        }
        return d.LocalUrl;
    }
    return d.UnknownUrl;
};

Dragonfly.handleHashClick = function (evt)
{
    var d = Dragonfly;
    
    var href = d.getEventHref (evt);
    if (!href || href.ignoreClick) {
        return;
    }
    //logDebug ('got href: ' + href);

    var elem = Event.element (evt);
    if (elem.ignoreClick) {
        return;
    }

    var url = href.getAttribute ('href');
    switch (d.getUrlType (url)) {
    case d.MailUrl:
        elem.blur && elem.blur ();
        Event.stop (evt);
        d.Mail.Composer.mailtoClicked (elem, url);
        break;
    case d.LocalUrl:
        d.setCurrentUser ();
        break;
    case d.DragonflyUrl:
        d.setCurrentUser ();
        elem.blur && elem.blur ();
        Event.stop (evt);
        d.go (url);
        break;
    }
};

Dragonfly.handleContextClick = function (evt)
{
    var d = Dragonfly;

    logDebug ('context: ' + (Event.element (evt) ? Event.element(evt).tagName : '???'));

    var href = d.getEventHref (evt);
    if (!href) {
        return;
    }
    //logDebug ('got href: ' + href);
    switch (d.getUrlType (href.getAttribute ('href'))) {
    case d.LocalUrl:
    case d.DragonflyUrl:
        d.setCurrentUser ();
        break;
    }
};

Dragonfly.getScrolledViewSize = function (elem, parentNode)
{
    var d = Dragonfly;

    var elemPos;
    var winSize = d.getWindowDimensions ();

    if (parentNode) {
        var parentPos = Element.getMetrics (parentNode);
        elemPos = Element.getMetrics (elem);

        //logDebug ('winSize:', repr(winSize), 'parentPos:', repr(parentPos), 'elemPos:', repr(elemPos));
        return Math.max (0, winSize.h - parentPos.y - parentPos.height + elemPos.height);
    } else {
        elemPos = elementPosition (elem);
        return winSize.h - elemPos.y;
    }
};

Dragonfly.setScrolledViewMaxSize = function (elem)
{
    var d = Dragonfly;
    elem = $(elem);
    if (elem) {
        elem.style.maxHeight = d.getScrolledViewSize (elem) + 'px';
    }
};

Dragonfly.setScrolledViewSize = function (elem, parentNode)
{
    var d = Dragonfly;
    elem = $(elem);
    if (elem) {
        elem.style.height = d.getScrolledViewSize (elem, parentNode) + 'px';
        logDebug ('scrolled height:', elem.style.height);
    }
};

Dragonfly.getScrolledSidebookElement = function ()
{
    var d = Dragonfly;

    switch (d.sideBook.getCurrentPage ()) {
    case 0:
        return d.AddressBook.sideboxPicker.unselectedId;
    case 1:
        return 'calendar-list';
    }
};

Dragonfly.resizeSideBook = function ()
{
    var d = Dragonfly;
    var elem = d.getScrolledSidebookElement ();
    if (elem) { 
        var calHeight = Element.getDimensions ('sidebox-calendar').height;

        var height = d.getScrolledViewSize (elem, 'sidebox-sidebook');
        var smallMode = document.body.className.indexOf ('low-res') != -1;

        function switchMode () {
            Element[(smallMode ? 'remove' : 'add') + 'ClassName'](document.body, 'low-res');
            height = d.getScrolledViewSize (elem, 'sidebox-sidebook');
            smallMode = !smallMode;
        }

        function maybeShrink() {
            //logDebug ('height:', height, 'calHeight:', calHeight);
            if (height < calHeight / 3) {
                switchMode();
            }
        }

        /*
         * make sure the height of the sidebook content is at least
         * some portion of the calendar height when in large mode.
         */
        if (smallMode) {
            switchMode();
            maybeShrink();
        } else if (height < 2 * calHeight) {
            maybeShrink();
        }

        // the 3 here is for the 1 px border i guess; who knows?
        $(elem).style.height = (height - 3) + 'px';
    }
};

Dragonfly.resizeScrolledView = function (loc)
{
    var d = Dragonfly;

    loc = loc || d.curLoc;
    if (!loc) {
        return;
    }

    var tab = loc.getTab();
    var handler = loc.getHandler();

    var elem = ((tab && tab.getScrolledElement) || 
                (handler && handler.getScrolledElement) || 
                Prototype.emptyFunction) (loc);

    if (Dragonfly.ieVersion == 6) {
        d.setScrolledViewSize (elem);
    } else {
        d.setScrolledViewMaxSize (elem);
    }

    ((tab && tab.handleResize) || 
     (handler && handler.handleResize) || 
     Prototype.emptyFunction) ();

    return elem;
};

Dragonfly._handleResize = function ()
{
    var d = Dragonfly;

    if (d.resizeDef) {
        window.clearInterval (d.resizeDef);
        d.resizeDef = 0;
    }
    
    d.resizeSideBook ();
    d.resizeScrolledView ();
};

Dragonfly.handleResize = function (evt)
{
    var d = Dragonfly;

    if (d.resizeDef) {
        //logDebug ('resize already defered...');
        return;
    }
    //logDebug ('defering resize...');
    d.resizeDef = window.setInterval (d._handleResize, 150);
};

Dragonfly.toggleConsole = function ()
{
    Element.toggle('interpreter_form');
    if (Element.visible ('interpreter_form')) {
        $('interpreter_text').focus();
    }
    Dragonfly.handleResize();
};

Dragonfly.sendFeedback = function ()
{
    var Mail = Dragonfly.Mail;
    var Composer = Mail.Composer;

    var msg = {
        from: Mail.getFromAddress(),
        to: 'bongo-feedback',
        subject: 'Feedback on Bongo version: ' + Dragonfly.version,
        body: [
        'Thanks for sending feedback!  If you are reporting a problem, ',
        'try to be as specific as possible: say what you did, what happened, ',
        'and what you expected. For example, "When I tried to create a new ',
        'event by double-clicking on the calendar in the Summary tab..."\n\n',
        '-------------------------------------------------------------------\n',
        'This log may help us reproduce bugs. It contains a history of your ',
        'current web session, which may include the names of mailing lists ',
        'you are on and searches you have done.',
        '\n-------------------------------------------------------------------\n',
        navigator.userAgent + '\n',
        logger.getMessageText()].join('')
    };

    Composer.composeNew (msg);
};

Dragonfly.buildSidebar = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    d.sideCal = new d.Calendar.MonthWidget ('sidebox-calendar', Day.get());
    c.popup = new c.OccurrencePopup ();

    // Build sidebook
    d.sideBook = new d.Notebook();
    d.sideBook.addPageChangeCallback (d.resizeSideBook);

    // addressbook tab
    var html = new d.HtmlBuilder();
    d.AddressBook.sideboxPicker = new d.AddressBook.ContactPicker (html);
    html.set (d.sideBook.appendPage (IMG({ 'class': 'icon addressbook', src: '/img/blank.gif' })));

    // calendar tab of sidebook
    html = new d.HtmlBuilder (
        '<div class="list-selector">',
        '<ul id="calendar-list" class="unselected-items"></ul>',
        '<div id="new-calendar" class="action"><a id="new-calendar-href" href="javascript:void(Dragonfly.Calendar.newCalendar())">', _('createNewCalendarLabel'), '</a></div>',
        '<div id="sub-calendar" class="action"><a id="sub-calendar-href" href="javascript:void(Dragonfly.Calendar.newCalendar(true))">', _('subscribeCalendarLabel'), '</a></div>',
        '</div>');
    html.set (d.sideBook.appendPage (IMG({ 'class': 'icon calendars', src: '/img/blank.gif' })));
    
    //  todo tab of sidebook
    //d.sideBook.appendPage (DIV(), IMG({ 'class': 'icon todos', src: '/img/blank.gif' }));
    d.HtmlBuilder.buildChild ('sidebox-sidebook', d.sideBook);
};

Dragonfly.observeEvents = function ()
{
    var d = Dragonfly;
    var c = d.Calendar;

    Event.observe (document.documentElement, 'click', d.handleHashClick.bindAsEventListener (null), true);
    Event.observe (document.documentElement, 'contextmenu', d.handleContextClick.bindAsEventListener (null), true);
    Event.observe ('search-entry', 'keypress', 
                   (function (evt) {
                       if (evt.keyCode == Event.KEY_RETURN) {
                           Event.element(evt).blur();
                       }
                   }).bindAsEventListener (null));
    Event.observe ('search-entry', 'change', 
                   (function (evt) {
                       d.search ($('search-entry').value);
                   }).bindAsEventListener (null));
    Event.observe (window, 'resize', d.handleResize.bindAsEventListener (null));
    Event.observe ('calendar-list', 'click',
                   (function (evt) {
                       var elem = Event.element (evt);

                       if (elem.tagName == 'INPUT') {
                           return;
                       }

                       var li = Event.findElement (evt, 'LI');
                       if (!li) {
                           return;
                       }
                       
                       var bongoId = li.getAttribute ('bongoId');
                       if (!bongoId) {
                           return;
                       }

                       c.setSelectedCalendarId (bongoId);
                       
                       if (elem.className.indexOf ('cal-info-button') != -1) {
                           var cal = c.calendars.getByBongoId (bongoId);
                           (new c.CalendarPropsPopup (cal)).summarize();
                       }
                   }).bindAsEventListener (null),
                   true);
    Event.observe ('calendar-list', 'change',
                   (function (evt) {
                       var elem = Event.element (evt);
                       var li = Event.findElement (evt, 'LI');
                       if (!li) {
                           return;
                       }

                       var bongoId = li.getAttribute ('bongoId');
                       if (!bongoId) {
                           return;
                       }

                       c.Preferences.setCalendarVisible (bongoId, elem.checked);
                       if (d.curLoc &&
                           d.curLoc.tab == 'calendar' ||
                           d.curLoc.tab == 'summary') {
                           c.layoutEvents();
                       }
                   }).bindAsEventListener (null),
                   true);

    Event.observe ('sidebar-select', 'change',
                   d.sidebarSelectChanged.bindAsEventListener (null));
};

Dragonfly.translateElements = function ()
{
    // Top bar
    Element.setHTML ('admin-text', _('administrationLabel'));
    Element.setHTML ('logout-text', _('logoutLabel'));
    Element.setHTML ('settings-text', _('preferencesLabel'));
    $('bongosearch').value = _('searchButtonLabel');
    Element.setHTML ('sidebar-select-search', _('searchButtonLabel'));
    
    // Sidebar
    Element.setHTML ('summary-tab-label', _('summaryTabLabel'));
    Element.setHTML ('summary-tab-label-alt', _('summaryTabLabel'));
    document.getElementById('summary-tab-href').title = _('summaryTabHint');
    
    Element.setHTML ('mail-tab-label', _('mailTabLabel'));
    Element.setHTML ('mail-tab-label-alt', _('mailTabLabel'));
    document.getElementById('mail-tab-href').title = _('mailTabHint');
    Element.setHTML ('compose-tab', _('composeMailLabel'));
    
    Element.setHTML ('calendar-tab-label', _('calendarTabLabel'));
    Element.setHTML ('calendar-tab-label-alt', _('calendarTabLabel'));
    document.getElementById('calendar-tab-href').title = _('calendarTabHint');
    Element.setHTML ('new-event-href', _('composeEventLabel'));
}

Dragonfly.start = function ()
{
    var d = Dragonfly;
    var p = d.Preferences;
    var c = d.Calendar;
    
    d.buildSidebar();
    d.translateElements();

    d.setLoginMessage (_('loadingData'));

    // set current timezone to default and build timezone selector
    d.curTzid = c.Preferences.getDefaultTimezone();
    //d.tzselector = new d.TzSelector (d.curTzid, 'tzselect', true);
    //d.tzselector.setChangeListener (function () { d.setCurrentTzid (d.tzselector.getTzid()); });

    var def = new d.MultiDeferred ([ c.loadCalendars(), 
                                     d.AddressBook.sideboxPicker.load() ]);
    return def.addCallbacks (
        function (res) {
            var loc = new d.Location (location.hash);
            d.setLoginMessage (_('loadingItemPre') + " " + d.escapeHTML (_(loc.tab)) + _('loadingItemPost'));
            return d.go (loc.user == d.userName ? loc : '#').addErrback (
                function (err) {
                    logDebug ('got error on first try:', d.reprError (err), '; trying summary...');
                    return d.go ('#');
                }).addCallbacks (
                    function (res) {
                        hideElement ('loading');
                        hideElement ('login-pane');
                        hideElement ('admin-link');
                        $('login-user').value = ''; 
                        $('login-password').value = '';
                        Element.setText ('user-name', d.userName);
                        showElement ('content');
                        
                        // Add administration link if we're admin
                        if (d.userName == 'admin')
                        {
                            // Don't use showElement - not nice to non-divs
                            $('admin-link').style.display = 'inline';
                        }
                        
                        removeElementClass (document.body, 'login');
                        
                        d._handleResize();
                        d.observeEvents();
                        
                        return res;
                    },
                    function (err) {
                        logError ('error loading', loc + ':', d.reprError (err));
                        d.setLoginMessage ('Could not load ' + d.escapeHTML (loc.tab) + ' (check Logs)');
                        d.showLoginPane();
                        return err;
                    });
        },
        function (err) {
            logError ('error loading contacts/calendars:', d.reprError (err));
            d.setLoginMessage ('Could not load contacts or calendars (check Logs)');
            d.showLoginPane();
            return err;
        });
};

Dragonfly.stop = function ()
{
    return true;
};

Dragonfly.beforeUnload = function ()
{
    var d = Dragonfly;

    var def = d.contentZone.canDisposeZone();
    if (def instanceof Deferred) {
        def.addCallback (
            function (res) {
                alert ('Bongo has finished saving changes.  It is OK to browse to another location.');
                return res;
            });
        return 'Bongo is still busy with a few things, but we ' +
               'should be done shortly.\n\n' +
               'Please click Cancel and wait until we are finished, ' +
               'otherwise your changes may be lost.';
    } else if (!def) {
        return 'Bongo needs some help from you before it\'s safe to leave.\n\n' +
               'Please click Cancel and tie up any loose ends ' +
               'otherwise your changes may be lost.';
    }
};

Dragonfly.initHtmlZones = function ()
{
    var d = Dragonfly;
    d.contentZone = new d.HtmlZone ('content');
    d.contentIFrameZone = new d.HtmlZone ('content-iframe');
    window.onbeforeunload = d.beforeUnload;
    // disposeZone is not an event handler
    Event.observe (window, 'unload', bind ('disposeZone', d.contentZone));
};

Dragonfly.main = function ()
{
    Dragonfly.browserDetect();
    Dragonfly.initHtmlZones();
    //Dragonfly.buildSidebar();
    Dragonfly.observeLoginEvents ();
    Dragonfly.login();
};
