Dragonfly.Location = function (path)
{
    var d = Dragonfly;

    if (typeof path != 'string') {
        update (this, path);
        this.user = this.user || d.userName;
        this.tab  = this.tab  || 'summary';

        tab = d.tabs[this.tab];
        if (!tab) {
            this.valid = true;
            return;
        }

        this.set = this.set || tab.defaultSet;

        this.handler = this.handler || tab.defaultHandler;

        return;
    }

    var h = path.indexOf ('#');
    if (h != -1) {
        path = path.substr (h + 1);
    }

    path = path.split ('/');

    var arg = path.shift ();
    if (arg && arg.charAt (0) == '~') {
        this.user = arg.substr (1);
        arg = path.shift ();
    } else {
        this.user = d.userName;
    }
    
    this.tab = arg || 'summary';

    var tab = d.tabs[this.tab];
    if (!tab) {
        this.valid = true; /* for now */
        return;
    }

    this.set = path.shift ();
    if (!this.set || !tab.setLabels[this.set]) {
        this.set && path.unshift (this.set);
        this.set = tab.defaultSet;
    }

    (tab.parseSubSet || Prototype.emptyFunction) (this, path);

    this.handler = path.shift ();
    if (!this.handler) {
        this.handler = tab.defaultHandler;
    } else if (this.handler == '-') {
        path.unshift (this.handler);
        this.handler = tab.defaultHandler;
    } else {
        var matches = this.handler.match (/^([^\(]*)(?:\((.*)\))?$/);
        if (matches && (!matches[1] || tab.handlers[matches[1]])) {
            this.handler = matches[1] || tab.defaultHandler;
            this.query = decodeURIComponent (matches[2] || '');
        } else {
            path.unshift (this.handler);
            this.handler = tab.defaultHandler;
        }
    }

    var handler = tab.handlers[this.handler];
    if (!handler) {
        console.error ('got invalid handler "' + this.handler + '" for tab "' + this.tab + '"');
        return;
    }

    if (handler.parseArgs) {
        var args = path.shift();
        if (args && args != '-') {
            args = args.split (',').map (decodeURIComponent);
        } else {
            args = null;
        }
        handler.parseArgs(this, args);
    }

    (handler.parsePath || Prototype.emptyFunction) (this, path);
};

Dragonfly.Location.getServerUrl = function (url /*, skipArgs, ext */)
{
    var d = Dragonfly;

    if (typeof url == 'string') {
        if (url.charAt ('0') == '#') {
            url = new d.Location (url);
        } else {
            return url;
        }
    } else if (!url.getServerUrl) {
        url = new d.Location (url);
    }
    return url.getServerUrl.apply (url, $A(arguments).slice (1));
};

Dragonfly.Location.prototype = new Object;

Dragonfly.Location.prototype.toString = function ()
{
    return this.getClientUrl();
};

Dragonfly.Location.prototype.getServerUrl = function (skipArgs, ext, stopAtComponent)
{
    var d = Dragonfly;
    var v = d.tabs[this.tab];
    var h = v && v.handlers[this.handler];

    if (!h) {
        return null;
    }

    var path = [ '/user', (this.user || d.userName), this.tab, this.set ];
    if (arguments.length < 2) {
        ext = '.json';
    }

    /* subsets */

    var handler = this.handler;
    if (this.query) {
        handler += '(' + encodeURIComponent (this.query) + ')';
    }
    path.push (handler);

    if (h.getArgs) {
        path.push (!skipArgs ? (h.getArgs (this).map(encodeURIComponent).join (',')) : '-');
    }
    
    if ((h.getServerUrl || Prototype.emptyFunction) (this, path, $A(arguments).slice (1))) {
        ext = '';
    }

    if (path[path.length - 1] == '-') {
        path.pop ();
    }

    return path.join ('/') + (ext || '');
};

Dragonfly.Location.prototype.getClientUrl = function (stopAtComponent)
{
    var d = Dragonfly;

    // push username and tab
    var path = [ '~' + (this.user || d.userName), this.tab ];
    if (stopAtComponent == 'tab') {
        return path.join ('/');
    }

    // push set if not default
    var tab = d.tabs[this.tab];
    if (this.set && (!tab || this.set != tab.defaultSet)) {
        path.push (this.set);
    }
    if (stopAtComponent == 'set') {
        return path.join ('/');
    }

    // push handler and query if not default/empty
    var hpath = '';
    if (this.handler && (!tab || this.handler != tab.defaultHandler)) {
        hpath += this.handler;
    }
    if (this.query) {
        hpath += '(' + encodeURIComponent (this.query) + ')';
    }
    if (hpath) {
        path.push (hpath);
    }
    if (stopAtComponent == 'handler') {
        return path.join ('/');
    }

    // push handler arguments
    var handler = tab && tab.handlers[this.handler];
    if (handler && handler.getArgs) {
        path.push (handler.getArgs (this).map(encodeURIComponent).join (','));
    }
    if (handler && handler.getClientUrl) { 
        handler.getClientUrl (this, path, stopAtComponent);
    }

    return path.join ('/');
};

Dragonfly.Location.prototype.getTab = function ()
{
    return (this.tab) ? Dragonfly.tabs[this.tab] : null;
};

Dragonfly.Location.prototype.getHandler = function ()
{
    var tab = this.getTab ();
    return (tab) ? tab.handlers[this.handler] : null;
};

Dragonfly.Location.prototype.getBreadCrumbs = function ()
{
    var d = Dragonfly;

    var tab = d.tabs[this.tab];
    var handler = tab && this.handler && tab.handlers[this.handler];

    var res = [ d.format ('<a href="#{0}">{1}</a>',
                          d.escapeHTML (this.getClientUrl ('set')),
                          d.escapeHTML (_(tab.setLabels [this.set]))) ];


    if (this.handler && this.handler != tab.defaultHandler) {
        res.push (d.format (' &gt; <a href="#{0}">{1}</a>',
                            d.escapeHTML (this.getClientUrl ('handler')),
                            d.escapeHTML (_(handler.label))));
    }

    var crumb = handler && handler.getBreadCrumbs && handler.getBreadCrumbs (this);
    if (crumb) {
        res.push (' &gt; ', crumb);
    }

    return res.join ('');
};
