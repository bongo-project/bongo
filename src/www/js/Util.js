// Object Utilities
deepclone = function (x) {
    if (x == null || typeof x != 'object') {
        return x;
    }
    var clone = new x.constructor();
    for (var property in x) {
        // don't copy prototypical properties
        if (clone[property] != x[property]) {
            clone[property] = deepclone (x[property]);
        }
    }
  return clone;
};

condSetProp = function (object, property, value)
{
    if (value) {
        object = (object) ? object : { };
        object[property] = value;
    }
    return object;
};

condGetProp = function (object, property, defaultVal)
{
    return (object && object[property]) ? object[property] : defaultVal;
};

condDelProp = function (object, property)
{
    if (object) {
        delete object[property];
    }
};

// String Utilities
startsWith = function (value, target) 
{
    return (value.substr(0, target.length) == target);
};

endsWith = function (value, target) 
{
    if (value.length < target.length) {
        return false;
    }
    return (value.substr(value.length - target.length) == target);
};

// Ideally, we'd only like to set these classes when we're sure they are 
// correct.  We don't live in an ideal world, however.
Dragonfly.browserDetect = function () 
{
    var os, osv, eng, engv, agent = navigator.userAgent;
    var components = [ ];
    if (document.body.className) {
        components.push (document.body.className);
    }

    // os
    os = navigator.platform.slice(0,3).toLowerCase();
    if (os == 'win' || os == 'lin' || os == 'mac') {
        components.push ('os-' + os);
    }
    if (agent.indexOf ('X11') + 1) {
        components.push ('os-x11');
    }

    // engine
    var match = agent.match (/MSIE (\d+)/);
    if (match) {
        components.push ('eng-msie');
        components.push ('engv-' + match[1]);
        Dragonfly.isIE = true;
        Dragonfly.ieVersion = Number (match[1]);
    } else if (match = agent.match(/AppleWebKit\/(\d+)/)) {
        components.push ('eng-webkit');
        components.push ('engv-' + match[1]);
        Dragonfly.isWebkit = true;
        Dragonfly.webkitVersion = Number (match[1]);
    } else if (match = agent.match(/rv:(\d+)\.(\d+).*Gecko/)) {
        components.push ('eng-gecko');
        components.push ('engv-' + match[1] + match[2]);
    }
        
    document.body.className = components.join(' ');
};

Dragonfly.Profiler = function (label, resetExisting)
{
    var self = arguments.callee;

    this.label = label || 'default';

    var that = self.profilers[this.label];
    if (that) {
        if (resetExisting) {
            that.reset();
        }
        return that;
    }
    self.profilers[this.label] = this;

    this.reset();
};

Dragonfly.Profiler.profilers = { };
Dragonfly.Profiler.prototype = { };

Dragonfly.Profiler.prototype.reset = function ()
{
    this.startTime = (new Date).getTime();
    this.pending = $H();
    this.totals = $H();
    delete this.endTime;
};

Dragonfly.Profiler.prototype.toggle = function (label) 
{
    if (!this.pending[label]) {
        this.pending[label] = [ ];
    }
    this.pending[label].push ((new Date).getTime());
};

Dragonfly.Profiler.prototype.pause = function ()
{
    this.endTime = (new Date).getTime();
};

Dragonfly.Profiler.prototype.dump = function (finished, startTime, endTime)
{
    startTime = startTime || this.startTime;
    endTime = endTime || this.endTime || (new Date).getTime();

    var total = endTime - startTime;
    var totals = this.totals;
    var pending = this.pending;

    this.pending.each (
        function (pair) {
            if (!totals[pair.key]) {
                totals[pair.key] = 0;
            }
            for (var i = 0; i < pair.value.length; i += 2) {
                totals[pair.key] += pair.value[i + 1] - pair.value[i];
            }
        });

    console.debug ('Dumping Profiler Info for ' + this.label);
    this.totals.sortBy (
        function (value, index) {
            return -value.value;
        }).each (
            function (pair) {
                var key = pair.key;
                if (key.charAt (key.length - 1) == '*') {
                    return;
                }
                console.debug (
                    Dragonfly.format (
                        '    {0}: {2:#}pct, {3} new: {1:#.00}ms tot, {4:#.00}ms avg', 
                        key, pair.value, 100 * pair.value / total,
                        pending[key].length / 2, 
                        2 * pair.value / pending[key].length));
            });
    console.debug ('    ----------------------');
    console.debug ('    Total: ' + total + 'ms');
    if (finished) {
        this.finished();
    } else {
        this.pending = $H();
    }
};

Dragonfly.Profiler.prototype.finished = function (label)
{
    if (Dragonfly.Profiler.profilers) {
        delete Dragonfly.Profiler.profilers[label || this.label];
    }
};

Element.setText = function (elem, text)
{
    elem = $(elem);
    if (elem) {
        elem['innerText' in elem ? 'innerText' : 'textContent'] = text || '';
    }
};

Element.getText = function (elem)
{
    elem = $(elem);
    return elem ? elem['innerText' in elem ? 'innerText' : 'textContent'] || '' : '';
};

Element.repr = function (elem, origElem)
{
    var s = (typeof origElem == 'string') ?
        ['$("', origElem, '")'] :
        ['$(', (elem && ('"' + (elem.id || '???') + '"')) || 'null', ')'];
    if (elem) {
        s.push ('<', elem.nodeName, (elem.className) ? ' class="' + elem.className + '">' : '>');
    }
    return s.join('');
};

Element.setHTML = function (elem, html)
{
    var start = new Date();

    var origElem = elem;
    elem = $(elem);

    if (!elem) {
        console.error ('Could not find element ' + Element.repr(elem, origElem));
    }
    
    try {
        html = html.join ? html.join ('') : html;
        elem.innerHTML = html;
    } catch (e) {
        console.error ('could not set html on ' + Element.repr(elem, origElem) + ': ' + repr (e) + ': |' + html + '|');
        return;
    }

    if (!html || elem.innerHTML) {
        var finish = new Date();
        if (finish - start > 100) {
            console.warn ('sync setHTML on', Element.repr(elem, origElem), 'took', (finish - start) / 1000, 'seconds.');
        }
        return;
    }

    console.error ('setting html had no effect on ' + Element.repr(elem, origElem) + ': |' + html + '|');
    return;
};

/* HtmlBuildable interface
 *
 * Dragonfly.HtmlBuildable = function () { };
 * Dragonfly.HtmlBuildable.prototype = { };
 * Dragonfly.HtmlBuildable.prototype.buildHtml = function (html) { };
 *
 * note, connectHtml() is currently not part of this interface; HtmlBuildables are 
 * responsible for setting up their own connection callback.
 */
Dragonfly.HtmlBuilder = function ()
{
    this._html = [ ];

    if (arguments.length) {
        this._html.push.apply (this._html, arguments);
    }

    this.push = bind (this._html.push, this._html);

    this._def = new Deferred;
    
    forEach ([ 'addBoth', 'addCallback', 'addErrback', 'addCallbacks' ],
             function (func) {
                 this[func] = bind (func, this._def);
             }, this);
};

Dragonfly.HtmlBuilder.buildChild = function (parent, child)
{
    var d = Dragonfly;

    if (parent instanceof d.HtmlBuilder) {
        if (child.buildHtml) {
            child.buildHtml (parent);
        } else if (typeof child == 'string') {
            parent.push (child);
        } else if (child.length) {
            for (var i = 0; i < child.length; i++) {
                Dragonfly.HtmlBuilder.buildChild (parent, child[i]);
            }
        } else {
           throw new Error ("HtmlBuilder can't build child: " + child);
        }
    } else {
        var html = new d.HtmlBuilder();
        child.buildHtml (html);
        html.set (parent);
    }
};

Dragonfly.HtmlBuilder.prototype = { };

Dragonfly.HtmlBuilder.prototype.handleCallbackError = function (err)
{
    console.error ('HtmlBuilder callback error: ', Dragonfly.reprError (err));
    return err;
};

Dragonfly.HtmlBuilder.prototype.set = function (elem)
{
    var oldElem = elem;
    var inDOM = false;
    var holder;
    var node;

    elem = $(elem);
    Element.setHTML (elem, this._html);

    for (node = elem; node.parentNode; node = node.parentNode) {
        if (node.parentNode == node.ownerDocument) {
            inDOM = true;
            break;
        }
    }
    if (!inDOM) {
        // node here is the highest level parentless node. we could
        // just remember elem's old parent, instead of adding all of
        // node to our holder, except that could produce invalid html
        // (if elem is a td, for example)
        holder = DIV ({style:'display:none;'});
        holder.appendChild (node);
        document.body.appendChild (holder);
    }

    this._def.addErrback (this.handleCallbackError);
    this._def.callback (elem);

    if (!inDOM) {
        // on IE, node actually can refer to a document fragment,
        // whose child was added to holder (instead of it), so remove
        // holder's first child instead of node
        holder.removeChild (holder.firstChild);
        document.body.removeChild (holder);
    }
    return elem;
};

Dragonfly.HtmlBuilder.prototype.buildChild = function (child)
{
    Dragonfly.HtmlBuilder.buildChild (this, child);
};

/* HtmlZoneable interface:
 *
 * Dragonfly.HtmlZoneable = function () { };
 * Dragonfly.HtmlZoneable.prototype = { };
 *
 * Dragonfly.HtmlZoneable.prototype.canHideZone = function () { return true || false || new Deferred() };
 * Dragonfly.HtmlZoneable.prototype.canDisposeZone = function () { return true || false || new Deferred() };
 *
 * Dragonfly.HtmlZoneable.prototype.hideZone = function () { };
 * Dragonfly.HtmlZoneable.prototype.disposeZone = function ()  { };
 */
Dragonfly.HtmlZone = function (elem)
{
    var elemId = elem.id || elem;
    this.elem = $(elem);

    if (!this.elem) {
        throw new Error ('Could not find element "' + elemId + '" for HtmlZone');
    }

    if (this.elem.htmlZone) {
        throw new Error ('Element ' + Element.repr (this.elem) + ' already as an HtmlZone');
    }

    this.elem.htmlZone = this;
    this.zoneables = [ ];

    this.setParent (this.elem.parentNode);
};

Dragonfly.HtmlZone.findZone = function (elem)
{
    elem = $(elem);
    while (elem && !elem.htmlZone) {
        elem = elem.parentNode;
    }
    return elem && elem.htmlZone;
};

Dragonfly.HtmlZone.getDepth = function (elem)
{
    var depth = 0;
    for (var zone = this.findZone(elem); zone; zone = this.findZone(elem)) {
        depth++;
        elem = (zone.parentZone) ? zone.parentZone.elem : zone.elem.parentNode;
    }
    return depth;
};

Dragonfly.HtmlZone.add = function (elem, zoneable)
{
    var zone = Dragonfly.HtmlZone.findZone (elem);
    if (!zone) {
        throw new Error ('Could not find a zone for', Element.repr (elem));
    }
    zone.add (zoneable);
    return zone;
};

Dragonfly.HtmlZone.remove = function (elem, zoneable)
{
    var zone = Dragonfly.HtmlZone.findZone (elem);
    if (!zone) {
        throw new Error ('Could not find a zone for', Element.repr (elem));
    }
    zone.remove (zoneable);
};

Dragonfly.HtmlZone.prototype = { };

Dragonfly.HtmlZone.prototype.setParent = function (parentElem)
{
    this.unsetParent();
    this.parentZone = Dragonfly.HtmlZone.findZone (parentElem);
    if (this.parentZone) {
        console.debug ('Adding zone for:', Element.repr (this.elem),
                  'to the zone for:', Element.repr (this.parentZone.elem));
        this.parentZone.add (this);
    }
};

Dragonfly.HtmlZone.prototype.unsetParent = function ()
{
    if (this.parentZone) {
        this.parentZone.remove (this);
    }
    delete this.parentZone;
}

Dragonfly.HtmlZone.prototype.destroy = function ()
{
    this.unsetParent();
    delete this.elem.htmlZone;
    delete this.elem;
}

Dragonfly.HtmlZone.prototype.add = function (zoneable)
{
    this.zoneables.push (zoneable);
};

Dragonfly.HtmlZone.prototype.remove = function (zoneable)
{
    if (this._disposing) {
        return;
    }
    for (var i = this.zoneables.length - 1; i >= 0; --i) {
        if (this.zoneables[i] == zoneable) {
            this.zoneables.splice (i, 1);
            return;
        }
    }
};

Dragonfly.HtmlZone.canSomething = function (what)
{
    console.debug ('Checking if zone for', Element.repr (this.elem), what);

    if (this._def) {
        console.debug ('Cancelling the currently pending deferred');
        this._def.cancel();
        if (this._def) {
            console.error ('Our deferred persists after it was canceled');
        }
    }

    var defs = [ ];

    for (var i = this.zoneables.length - 1; i >= 0; --i) {
        var zoneable = this.zoneables[i];
        if (zoneable[what]) {
            var res = zoneable[what]();
            if (res instanceof Deferred) {
                defs.push (res);
            } else if (!res) {
                return res;
            }
        }
    }

    if (!defs.length) {
        return true;
    }

    this._def = new Dragonfly.MultiDeferred (defs);
    return this._def.addBoth (bind (function (res) {
                                        delete this._def;
                                        return res;
                                    }, this));
};

Dragonfly.HtmlZone.prototype.canHideZone = partial (Dragonfly.HtmlZone.canSomething, 'canHideZone');
Dragonfly.HtmlZone.prototype.canDisposeZone = partial (Dragonfly.HtmlZone.canSomething, 'canDisposeZone');

Dragonfly.HtmlZone.prototype.doSomething = function (what)
{
    console.debug ('Doing', what, 'in zone', Element.repr (this.elem));
    if (this._def) {
        console.error ('We already have a deferred');
    }

    for (var i = this.zoneables.length - 1; i >= 0; --i) {
        var zoneable = this.zoneables[i];
        if (zoneable[what]) {
            zoneable[what]();
        }
    }
};

Dragonfly.HtmlZone.prototype.hideZone = partial (Dragonfly.HtmlZone.prototype.doSomething, 'hideZone');
Dragonfly.HtmlZone.prototype.disposeZone = function ()
{
    this._disposing = true;
    this.doSomething ('disposeZone');
    this._disposing = false;
    this.zoneables = [ ];
};

Element.getMetrics = function (elem, relativeTo)
{
    return merge (Element.getDimensions (elem),
                  elementPosition (elem, relativeTo),
                  {
                      elem: elem,
                      toString: function () { 
                          return '(' +  this.x + ', ' + this.y + ') [' + this.width + 'x' + this.height + ']';
                      },
                      __repr__: function () { return this.toString(); }
                  });
};

Element.setSelectedOption = function (elem, value)
{
    var opts = elem.options;
    for (var i = 0; i < opts.length; i++) {
        var opt = opts[i];
        opt.selected = (opt.value == value);
    }                           
};

Dragonfly.getWindowDimensions = function ()
{
    var d = Dragonfly;
    var w, h;
    
    // http://www.howtocreate.co.uk/tutorials/javascript/browserwindow
    if (window.innerWidth) {
        w = window.innerWidth;
        h = window.innerHeight;
    } else if (document.documentElement.clientWidth) {
        w = document.documentElement.clientWidth;
        h = document.documentElement.clientHeight;
    } else {
        w = document.body.clientWidth;
        h = document.body.clientHeight;
    }

    return new MochiKit.DOM.Dimensions (w, h);
};

Dragonfly.nextId = function (base)
{
    var self = arguments.callee;
    return base + (self[base] = (self[base] || 0) + 1);
};

Dragonfly.clone = function (elem)
{
    var d = Dragonfly;

    function fixupId (e)
    {
        if (e.id) {
            elem[e.id] = e;
            e.id = d.nextId (e.id);
            console.debug ('created', e.id);
            
        }
        forEach (e.childNodes, fixupId);
    }

    var origElem = elem;
    elem = $(elem);
    if (!elem) {
        console.error ('Could not locate elem "' + origElem + '"');
        return null;
    }

    var start = new Date;
    elem = elem.cloneNode (true);
    var cloneFinish = new Date;
    fixupId (elem);
    var finish = new Date;

    console.debug ('clone(' + elem.id + '):',
              'total:', (finish - start) / 1000,
              'clone:', (cloneFinish - start) / 1000,
              'fixup:', (finish - cloneFinish) / 1000);

    return elem;
};


Dragonfly.getMousePosition = function (evt)
{
    return new MochiKit.DOM.Coordinates (Event.pointerX (evt),
                                         Event.pointerY (evt));
};

Dragonfly.findElementWithClass = function (element, classname)
{
    while (element != document) {
        if (hasElementClass(element, classname)) {
            return element;
        }
        element = element.parentNode;
    }
    return null;
};

Dragonfly.findElement = function (element, tagName, stopAt)
{
    while (element && element != stopAt) {
        if (element.tagName == tagName) {
            return element;
        }
        element = element.parentNode;
    }
    return null;
};

Dragonfly.escapeHTML = function (s)
{
    return (s == null) ? '' : escapeHTML (s.toString());
};

Dragonfly._linkifyText = function (s)
{
    var urlRegex = /\b(((https?|ftp|irc|telnet|nntp|gopher|file):\/\/|(mailto|news|data):)[^\s\"<>\{\}\'\(\)]*)/g;
    return (s == null) ? '' : s.replace (urlRegex, '<a href="$&">$&</a>');
};

Dragonfly.linkifyText = function (s)
{
    var d = Dragonfly;
    return d._linkifyText (d.escapeHTML (s));
};

Dragonfly.htmlizeText = function (s)
{
    var d = Dragonfly;
    return d.linkifyText (s).replace (/\r?\n/g, '<br>');
};

Dragonfly.linkifyNode = function (node)
{
    var d = Dragonfly;
    /* i wonder if we need to go up more than a single node when
     * checking for anchors */
    if (node.nodeType == 3 /* TEXT_NODE */ && node.parentNode.nodeName != 'A') {
        var dingus = d._linkifyText (node.nodeValue);
        if (dingus.length != node.nodeValue.length) {
            var span = SPAN();
            Element.setHTML (span, dingus);
            swapDOM (node, span);
        }
    } else if (node.nodeName != 'A' && node.hasChildNodes) {
        forEach (node.childNodes, d.linkifyNode);
    }
};

/* this is potentially slow */
Dragonfly.linkifyHtml = function (s)
{
    var span = SPAN();
    Element.setHTML (span, s);
    Dragonfly.linkifyNode (span);
    return span.innerHTML;
};

MochiKit.Base._newNamedError (Dragonfly, 'InvalidJsonTextError', function (text) { this.message = text });
Dragonfly.eval = function (text)
{
    var d = Dragonfly;
    var checkStart = new Date;

    // sometimes this crashes webkit, so for now let's skip this check
    if (!Dragonfly.isWebkit) {
        if (/[^,:{}\[\]0-9.\-+Eaeflnr-u \n\r\t]/.test(
                text.replace(/\"(\\.|[^\"\\])*\"/g, ''))) {
            throw new d.InvalidJsonTextError (text);
        }
        
    }
    var evalStart = new Date;
    var r = eval('(' + text + ')');
    var done = new Date;
    //console.debug ('check:', (evalStart - checkStart) / 1000, 'eval:', (done - evalStart) / 1000);
    return r;
};

MochiKit.Base._newNamedError (Dragonfly, 'InvalidJsonContentTypeError', function (contentType) { this.message = contentType; });
Dragonfly.jsonMimeTypes = {
    'text/javascript': true,
    'text/x-js': true,
    'application/x-javascript': true,
    'text/x-json': true
};
Dragonfly.evalJSONRequest = function (req)
{
    var d = Dragonfly;
    var contentType = req.getResponseHeader ('Content-Type');
    var extra = contentType.indexOf(';');
    if (extra >= 0)
        contentType = contentType.substr(0, extra);
    if (!contentType) {
        throw new d.InvalidJsonContentTypeError ('Missing Content-Type header');
    }
    if (!d.jsonMimeTypes[contentType.toLowerCase()]) {
        throw new d.InvalidJsonContentTypeError ('Invalid JSON Content-Type: ' + contentType);
    }
    // this is a hack until we have a better way to get server
    // settings at login time
    var mailAddress = req.getResponseHeader('X-Bongo-Address');
    if (mailAddress) {
        d.defaultMailAddress = mailAddress;
    }
    return d.eval (req.responseText);
};

Dragonfly.reprRequestError = function (err)
{
    var number;
    var message;

    var req = err.req || err;

    if (err.number) {
        number = err.number;
        message = "";
    } else try {
        number  = req.status;
        message = req.statusText;
    } catch (e) { }
    return number ? 'HTTP Error: ' + number + '/' + message : 'No status: Server down or not responding';
};

Dragonfly.displayError = function (err)
{
    return err.req ? Dragonfly.reprRequestError (err) : err.message;
};

Dragonfly.reprError = function (err)
{
    var d = Dragonfly;
    var s = '';
    
    if (err.req) {
        s += ': ' +  d.reprRequestError (err);
    }

    var file = err.fileName   || err.sourceURL;
    var line = err.lineNumber || err.line;
    if (file && line) {
        s += ' (' + file + ':' + line + ')';
    }

    if (!s) {
        try {
            s += ': ' + serializeJSON (err);
        } catch (e) { 
            s += ': (error serializing error)';
        }
    }
    return err.name + ': ' + err.message + s;
};

registerRepr ('error',
              function () {
                  for (var i = 0; i < arguments.length; i++) {
                      if (!(arguments[i] instanceof Error)) {
                          return false;
                      }
                  }
                  return true;
              },
              Dragonfly.reprError);

Dragonfly.request = function (method, url, body, headers)
{
    var d = Dragonfly;
    var c = d.Calendar;

    var origMethod = method;

    url = d.Location.getServerUrl (url);

    if (!url) {
        return succeed ({ responseText: 'undefined' });
    }

    // webkit can't put; only post
    if (method.toLowerCase() != 'get' && method.toLowerCase() != 'post') {
        method = 'POST';
    }
  
    var req = getXMLHttpRequest ();

    req.open (method, url, true);

    if (method != origMethod) {
        req.setRequestHeader ('X-Bongo-HttpMethod', origMethod);
    }

    req.setRequestHeader ('X-Bongo-Agent', 'Dragonfly/0.1');
    // req.setRequestHeader ('X-Bongo-User', d.userName);

    if (d.authToken) {
        req.setRequestHeader ('X-Bongo-Authorization', 'Basic ' + d.authToken);
    }
    var tzid = d.getCurrentTzid();
    if (tzid) {
        req.setRequestHeader ('X-Bongo-Timezone', tzid);
    }
    if (startsWith (url, '/user/')) {
        // work around IE6's stubborn refusal to take "no-cache" for an answer  
        req.setRequestHeader ('If-Modified-Since', 'Tue, 22 Feb 1972 22:22:22 GMT');
    }

    // from Prototype
    if (method.toLowerCase() == 'post' && body) {
        if (typeof body == 'string') {
            req.setRequestHeader ('Content-type', 'application/x-www-form-urlencoded');
        } else {
            try {
                body = serializeJSON (body);
                req.setRequestHeader ('Content-type', 'text/javascript');
            } catch (e) {
                console.error ('failed to serialize request body: ' + d.reprError (e));
                return fail(e);
            }
        }

        /* Force "Connection: close" for Mozilla browsers to work around
         * a bug where XMLHttpReqeuest sends an incorrect Content-length
         * header. See Mozilla Bugzilla #246651.
         */
        if (req.overrideMimeType) {
            req.setRequestHeader ('Connection', 'close');
        }
    } else {
        body = null;
    }

    if (headers) {
        for (var i = 0; i < headers.length; i += 2) {
            req.setRequestHeader (headers[i], headers[i + 1]);
        }
    }

    console.debug (method, url, 'started...');
    var start = new Date();
    return sendXMLHttpRequest (req, body).addCallbacks (
        function (res) {
            var finish = new Date();
            console.debug (method, url, 'finished: (' + (finish - start) / 1000, 'seconds)');
            return res;
        },
        function (e) {
            console.error ('Exception', method, url + ':', d.reprError (e));
            return e;
        });o
};

Dragonfly.requestJSON = function (method, url, body, headers)
{
    var d = Dragonfly;
    url = d.Location.getServerUrl (url, false, '.json');
    return d.request.apply (d, arguments).addCallback (d.evalJSONRequest);
};

Dragonfly.getJsonRpcId = function ()
{
    return 0;
};

MochiKit.Base._newNamedError (Dragonfly, 'JsonRpcError', function (jsob) { this.message = jsob.error.message; });

Dragonfly.getJsonRpcResult = function (jsob)
{
    var d = Dragonfly;
    // if (typeof jsob == 'string') {
    //     jsob = d.eval (jsob);
    // }
    console.debug ('RPC id', jsob.id, 'returned with', jsob.error ? 'Error' : jsob.result ? 'Success' : 'Nothing');
    if (jsob.error) {
        throw new d.JsonRpcError (jsob);
    }
    return jsob.result;
};

Dragonfly.requestJSONRPC = function (rpcMethod, url  /*, ... */)
{
    var d = Dragonfly;
    url = d.Location.getServerUrl (url, true, '');
    var jsob = { id: d.getJsonRpcId(), method: rpcMethod, params: $A(arguments).slice (2) };
    return d.requestJSON ('POST', url, jsob).addCallback (d.getJsonRpcResult);
};

/*
   Colors used for calendars etc. Note that these get used as CLASSES as well as color: x in 'style', so if you add any to here,
   make sure the appropriate class for your element in CSS exists.
*/
Dragonfly.Colors = [ 'violet', 'indigo', 'blue', 'green', 'brown', 'yellow', 'orange', 'red', 'gray'  ];
Dragonfly.getRandomColor = function ()
{
    return Dragonfly.Colors[Math.floor ((Math.random() * 100) % Dragonfly.Colors.length)];
};

if (!window.btoa) {
    window.btoa = function (s)
    {
    var b64 = [ 'A','B','C','D','E','F','G','H','I','J','K','L','M', 
            'N','O','P','Q','R','S','T','U','V','W','X','Y','Z',
            'a','b','c','d','e','f','g','h','i','j','k','l','m',
            'n','o','p','q','r','s','t','u','v','w','x','y','z',
            '0','1','2','3','4','5','6','7','8','9','+','/' ];
    
    var r = '';
    for (var i = 0; i < s.length; i += 3) {
        var a = s.charCodeAt (i);
        var b = i + 1 < s.length ? s.charCodeAt (i + 1) : 0;
        var c = i + 2 < s.length ? s.charCodeAt (i + 2) : 0;
        
        r += b64[(a & 0xfc) >> 2] +
         b64[((a & 0x03) << 4) | ((b & 0xf0) >> 4)] +
         (i + 1 < s.length ? b64[((b & 0x0f) << 2) | ((c & 0xc0) >> 6)] : '=') +
         (i + 2 < s.length ? b64[c & 0x3f] : '=');
    }
    return r;
    };
}

$V  = function (v) { return v && v.value; };
$LV = function (v) { return v && (v.valueLocal || v.value); };

Dragonfly.undefer = function (deferred)
{
    if (deferred.fired == -1) {
        throw new Error ('Deferred not yet fired');
    }
    return deferred.results[deferred.fired];
};

/* wait for some number of deferreds to fire */
Dragonfly.MultiDeferred = function (deferreds)
{
    Deferred.call (this, 
                   bind (function (err) {
                             this.defs.each (
                                 function (def) {
                                     def && def.cancel ();
                                 });
                         }, this));
    this.defs = [ ];
    this.res = null;
    if (deferreds) {
        forEach (deferreds, this.addDeferred, this);
    }
};

Dragonfly.MultiDeferred.prototype = new Deferred;

Dragonfly.MultiDeferred.prototype.addDeferred = function (def)
{
    if (!(def instanceof MochiKit.Async.Deferred)) {
        throw new Error ('Argument not Deferred: ' + repr (def));
    }

    this._pause();
    var defIdx = this.defs.length;
    this.defs.push (def);

    def.addBoth (bind (function (res) {
                           //console.debug (this.id + ' got callback: ' + repr (res) + ', ' + (this.paused - 1) + ' remaining.');
                           //console.debug (this.id + ' waiting on ' + (this.paused - 1) + ' more deferreds...');
                           if (res instanceof Error) {
                               this.res = res;
                           }
                           this.defs[defIdx] = null;
                           this._continue (this.res);
                           return res;
                       }, this));
    return this;
};

Dragonfly.setToolbarDisabled = function (parent, disabled)
{
    forEach ($(parent).getElementsByTagName ('input'),
             function (elem) {
                 elem.disabled = disabled;
             });
};

Dragonfly.enableToolbar  = partial (Dragonfly.setToolbarDisabled, 'toolbar', false);
Dragonfly.disableToolbar = partial (Dragonfly.setToolbarDisabled, 'toolbar', true);

Dragonfly.isInteger = function (n)
{
    return n == Math.round (n);
};

Dragonfly.getParentByTagName = function (elem, parentTag)
{
    elem = $(elem);
    parentTag = parentTag.toUpperCase ();
    while (elem = elem.parentNode) {
        if (elem.tagName == parentTag) {
            return elem;
        }
    }
    return null;
};

Dragonfly.AutoCompleter = function (labels, ids) 
{
    this.index = { };
    for (var i = 0; i < labels.length; i++) {
        this.addItem (labels[i], ids[i]);
    }
};

Dragonfly.AutoCompleter.prototype = { };

Dragonfly.AutoCompleter.prototype.addItem = function (label, id) 
{
    var keylist = label.split (/\s+/);
    for (var i = 0; i < keylist.length; i++) {
        var key = keylist[i].slice (0, 2).toLowerCase();
        var idList = this.index[key] || [ ];
        idList.push ([label.toLowerCase(), id]);
        this.index[key] = idList;
    }
};

Dragonfly.AutoCompleter.prototype.complete = function (substr) 
{
    var substr = substr.toLowerCase();
    var possibleHits = this.index[substr.slice (0, 2)];
    var hits = [ ];
    if (!possibleHits) {
        return hits;
    }
    for (var i = 0; i < possibleHits.length; i++) {
        var item = possibleHits[i];
        if (item[0].indexOf(substr) != -1) {
            hits.push (item[1]);
        }
    }
    return hits;
};

Dragonfly.getIFrameDocument = function (iframe)
{
    var iframe = $(iframe);
    // moz || (ie6 || ie5)
    if (!iframe.contentDocument)
    {
        if (!iframe.contentWindow)
        {
            // IE5
            return iframe.document;
        }
        else
        {
            // IE6
            return iframe.contentWindow.document;
        }
    }
    else
    {
        // Mozilla
        return iframe.contentDocument;
    }
    //return iframe.contentDocument || (iframe.contentWindow || iframe).document;
};
