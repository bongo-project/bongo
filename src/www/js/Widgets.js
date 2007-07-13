// Holder for widget utility functions
Dragonfly.Widgets = { };

Dragonfly.Widgets.addOptions = function (html, labels, selected)
{
    var s = (selected) ? selected : 0;
    for (var i = 0; i < labels.length; i++) {
        html.push ('<option', (i==s) ? ' selected' : '', '>', labels[i], '</option>');
    }
}

Dragonfly.Notebook = function (parent)
{
    var d = Dragonfly;

    this.id = d.nextId ('notebook');
    this.tabsId = d.nextId ('notebook-tabs');
    this.contentId = d.nextId ('notebook-content');

    this._labels = [ ];
    this._pages = [ ];

    this.currentPage = 0;

    this.showTabs = true;

    this.pageChangeListeners = [ ];

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Notebook.prototype = { };

Dragonfly.Notebook.prototype.buildHtml = function (html)
{
    html.push ('<div id="', this.id, '" class="notebook">',
               '<div id="', this.tabsId, '" class="notebook-tabs"></div>',
               '<div id="', this.contentId, '" class="notebook-content"></div>',
               '</div>');
    html.addCallback (bind ('connectHtml', this));
}

Dragonfly.Notebook.prototype.connectHtml = function (elem)
{
    var zone = new Dragonfly.HtmlZone ($(this.id));
    for (var i = 0; i < this._labels.length; i++) {
        if (this._labels[i]) {
            $(this.tabsId).appendChild (this._labels[i]);
        }
        if (this._pages[i]) {
            this._pages[i].htmlZone.setParent (this.id);
            $(this.contentId).appendChild (this._pages[i]);
        }
    }
    Event.observe (this.tabsId, 'click', this.clickHandler.bindAsEventListener (this));    
    this.setShowTabs (this.showTabs);
    if (this._labels.length) {
      this._switchToPage (Math.min (this.currentPage, this._labels.length));
    }
    return elem;
};

Dragonfly.Notebook.prototype.clickHandler = function (evt) {
     var elem = Dragonfly.findElement (Event.element (evt), 'A', $(this.id));
     if (!elem) {
         return;
     }
     //Event.stop (evt);
     if (Element.hasClassName (elem, 'selected')) {
         return;
     }
     var idx = findIdentical (this._labels, elem);
     if (idx < 0) {
         logWarning ('Could not find label for tab');
         return;
     }
     this._switchToPage (idx);
 }


Dragonfly.Notebook.prototype.setShowTabs = function (showTabs)
{
    this.showTabs = showTabs;
    if ($(this.id)) {
        if (this.showTabs) {
            Element.addClassName (this.id, 'tabs-visible');
        } else {
            Element.removeClassName (this.id, 'tabs-visible');
        }
    }
    if ($(this.tabsId)) {
        if (this.showTabs) {
            Element.show (this.tabsId);
        } else {
            Element.hide (this.tabsId);
        }
    }
};

Dragonfly.Notebook.prototype.appendPage = function (label)
{
    return this.insertPage (label, this._labels.length);
};

Dragonfly.Notebook.prototype.insertPage = function (label, idx)
{
    var tab = DIV();
    label = A(null, label);

    var page = DIV();
    var zone = new Dragonfly.HtmlZone (page);

    hideElement (page);

    if ($(this.tabsId)) {
        if (this._labels[idx]) {
            $(this.tabsId).insertBefore (label, this._labels[idx]);
        } else {
            $(this.tabsId).appendChild (label);
        }
    }

    if ($(this.contentId)) {
        if (this._pages[idx]) {
            $(this.contentId).insertBefore (page, this._pages[idx]);
        } else {
            $(this.contentId).appendChild (page);
        }
    }

    this._labels[idx] = label;
    this._pages[idx] = page;

    return page;
};

Dragonfly.Notebook.prototype.canRemovePage = function (idx)
{
    var page = this._pages[idx];
    if (!page) {
        return true;
    }
    return page.htmlZone.canDisposeZone ();
};

Dragonfly.Notebook.prototype.removePage = function (idx)
{
    var page = this._pages[idx];

    if ($(this.tabsId)) {
        $(this.tabsId).removeChild (this._labels[idx]);
    }

    page.htmlZone.disposeZone();
    page.htmlZone.setParent (null);

    if (page.parentNode) {
        removeElement (page);
    }

    this._labels[idx] = null;
    this._pages[idx] = null;
};

Dragonfly.Notebook.prototype.getCurrentPage = function ()
{
    return this.currentPage;
};

Dragonfly.Notebook.prototype._switchToPage = function (idx)
{
    if (idx > this._labels.length) {
        throw new Error ('Tab out of range');
    }

    if (!this._labels[idx]) {
        throw new Error ('No label for tab');
    }

    var currentPage = this._pages[this.currentPage];
    if (currentPage) {
        var def = currentPage.htmlZone.canHideZone();
        if (def instanceof Deferred) {
            return def;
        } else if (!def) {
            return;
        }
        currentPage.htmlZone.hideZone();
    }

    var curLabel = this._labels[this.currentPage];
    if (curLabel) {
        Element.removeClassName (curLabel, 'selected');
    }
    
    curLabel = this._labels[idx]
    Element.addClassName (curLabel, 'selected');
    if (this._pages[this.currentPage]) {
        hideElement (this._pages[this.currentPage]);
    }
    showElement (this._pages[idx]);
    this.currentPage = idx;
    forEach (this.pageChangeListeners,
             bind (function (func) {
                       func (this, idx);
                   }, this));
};

Dragonfly.Notebook.prototype.setCurrentPage = function (idx)
{
    if (this.currentPage != idx) {
        return this._switchToPage (idx);
    }
};

Dragonfly.Notebook.prototype.addPageChangeCallback = function (func)
{
    this.pageChangeListeners.push (func);
};

// There should be at least one label per row specified, labels should be an array
Dragonfly.ToggleButtons = function (parent, labels, numRows) {
    this.id = Dragonfly.nextId ('toggle-button-group');
    this.buttonIdBase = this.id + '-';
    this.labels = labels;
    this.numRows = (numRows) ? numRows : 1;
    this.selected = { };
    this.mouseoutHandler = this.mouseoutHandler.bindAsEventListener (this);
    this.mouseupHandler = this.mouseupHandler.bindAsEventListener (this);
    if (parent) {
        Dragonfly.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.ToggleButtons.prototype = { };

Dragonfly.ToggleButtons.prototype.buildHtml = function (html) {
    var len = this.labels.length;
    var numCols = Math.ceil (len / this.numRows);
    html.push ('<table id="', this.id, '" class="toggle-button-group" cellspacing="0">');
    for (var i = 0, labelIdx = 0; i < this.numRows; i++) {
        html.push ('<tr>');
        for (var colNum = 0; colNum < numCols && labelIdx < len; colNum++) {
            html.push ('<td><a id="', this.buttonIdBase, labelIdx, '" class="toggle-button">');
            html.push (this.labels[labelIdx++], '</a></td>');
        }
        if (colNum != numCols) {
            html.push ('<td colspan=' + (numCols - colNum) + ' class="spacer"></td>');
        }
        html.push ('</tr>');
    }
    html.push ('</table>');
    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.ToggleButtons.prototype.connectHtml = function (elem) {
    Event.observe (this.id, 'mousedown', this.handleMousedown.bindAsEventListener (this));
    return elem;
};

// The event handlers are low level routines which emulate GUI buttons
Dragonfly.ToggleButtons.prototype.handleMousedown = function (evt)
{
    Event.stop (evt); // prevents text selection
    var button = Dragonfly.findElementWithClass (Event.element (evt), 'toggle-button');
    if (!button) {
        return;
    }
    this.mousedButton = button;
    toggleElementClass ('selected', this.mousedButton);
    Event.observe (button, 'mouseup', this.mouseupHandler);
    Event.observe (button, 'mouseout', this.mouseoutHandler);
}

Dragonfly.ToggleButtons.prototype.mouseoutHandler = function (evt)
{
    Event.stopObserving (this.mousedButton, 'mouseup', this.mouseupHandler);
    Event.stopObserving (this.mousedButton, 'mouseout', this.mouseoutHandler);
    toggleElementClass ('selected', this.mousedButton);
}

Dragonfly.ToggleButtons.prototype.mouseupHandler = function (evt)
{
    Event.stopObserving (this.mousedButton, 'mouseup', this.mouseupHandler);
    Event.stopObserving (this.mousedButton, 'mouseout', this.mouseoutHandler);
    var buttonNumber = Number(this.mousedButton.id.match (/\d+-(\d+)/)[1]);
    if (hasElementClass (this.mousedButton, 'selected')) {
        this.select (buttonNumber);
    } else {
        this.unselect (buttonNumber);
    }
}

Dragonfly.ToggleButtons.prototype.select = function (buttonNumber)
{
    this.selected[buttonNumber] = true;
    addElementClass ($(this.buttonIdBase + buttonNumber), 'selected');
}

Dragonfly.ToggleButtons.prototype.unselect = function (buttonNumber)
{
    delete this.selected[buttonNumber];
    removeElementClass ($(this.buttonIdBase + buttonNumber), 'selected');
}

Dragonfly.ToggleButtons.prototype.getSelected = function ()
{
    return map (Number, keys (this.selected));
}

Dragonfly.PopupBuble = function (elem /*, children */)
{
    this.elem = elem;
    this.id = Dragonfly.nextId ('popup');
    this.children = list(arguments).slice (1);
    
    var holder = document.body.appendChild (
        DIV({ id:this.id, 'class':'popup-container' }));
    Dragonfly.HtmlBuilder.buildChild (holder, this);
};

Dragonfly.PopupBuble.prototype = { above: 1, below: 2, vertical: 3, left:4, right: 5, horizontal: 6};

Dragonfly.PopupBuble.prototype.buildHtml = function (html /*, children */)
{
    var d = Dragonfly;

    this.frameId = d.nextId ('popup-frame');
    this.contentId = d.nextId ('popup-content');
    this.pointerId = d.nextId ('popup-pointer');
    this.closeId = d.nextId ('close');

    html.push ('<div id="', this.frameId, '" class="popup-buble closable">',
               '<span class="t"><span class="tr"></span></span>',
               '<span class="c"><span class="cr"></span></span>',
               '<span class="b"><span class="br"></span></span>',
               '<div id="', this.closeId, '" class="close"></div>',
               '<div id="', this.contentId, '" class="popup-content">');

    for (var i = 0; i < this.children.length; i++) {
        html.buildChild (this.children[i]);
    }

    for (var i = 1; i < arguments.length; i++) {
        html.buildChild (arguments[i]);
    }
    delete this.children;
    
    html.push ('</div></div><span id="', this.pointerId, '" class="pointer"></span>');
    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.PopupBuble.prototype.connectHtml = function (elem)
{
    this.htmlZone = new Dragonfly.HtmlZone(this.id);
    this.setElem (this.elem);
    Event.observe ($(this.closeId), 'click', function () {
        if (this.canDisposeZone()) {
            this.dispose();
        }
    }.bindAsEventListener(this));
    return elem;
};

Dragonfly.PopupBuble.prototype.isVisible = function ()
{
    return ($(this.id).style.visibility == 'visible');
};

Dragonfly.PopupBuble.prototype.isDisposed = function ()
{
    return $(this.id) ? false : true;
};

Dragonfly.PopupBuble.prototype.addPoppedOut = function ()
{
    if (this.elem) {
        addElementClass (this.elem, 'popped-out');
    }
};

Dragonfly.PopupBuble.prototype.removePoppedOut = function ()
{
    if (this.elem) {
        removeElementClass (this.elem, 'popped-out');
    }
};

Dragonfly.PopupBuble.prototype.setElem = function (elem)
{
    if (this.elem) {
        this.removePoppedOut();
        Dragonfly.HtmlZone.remove (this.elem, this);
    }
    this.elem = $(elem);
    if (elem) {
        this.htmlZone.setParent(this.elem);
        Dragonfly.HtmlZone.add (this.elem, this);
        var zindex = Dragonfly.HtmlZone.getDepth (this.elem)*100;
        $(this.frameId).style.zIndex = zindex;
        $(this.pointerId).style.zIndex = zindex + 1;
    }
}

Dragonfly.PopupBuble.prototype.setForm = function (children, actions, observer)
{
    var d = Dragonfly;
    
    this.setClosable (false);
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
    
    html.set (this.contentId);    
    for (var i = 0; i < actions.length; i++){
        var clickHandler = actions[i].onclick;
        Event.observe (ids[i], 'click', bind (clickHandler, observer || this));
    }
};

Dragonfly.PopupBuble.prototype.setClosable = function (closable)
{
    if (closable) {
        showElement (this.closeId);
        addElementClass (this.frameId, 'closable');
    } else {
        hideElement (this.closeId);
        removeElementClass (this.frameId, 'closable');
    }
};

Dragonfly.PopupBuble.prototype._positionVertically = function (elemMetrics, frameMetrics, docMetrics)
{
    if (elemMetrics.x + frameMetrics.width < docMetrics.w) {
        $(this.frameId).style.right = '';
        $(this.frameId).style.left = (elemMetrics.x > 50) ? '-25px' : '-12px';
    } else { // fix against right side
        var thisMetrics = Element.getMetrics (this.id);
        $(this.frameId).style.left = '';
        $(this.frameId).style.right = (thisMetrics.x - docMetrics.w + thisMetrics.width + 25) + 'px';
    }
};

Dragonfly.PopupBuble.prototype._positionHorizontally = function (elemMetrics, frameMetrics, docMetrics)
{
    if (elemMetrics.y + frameMetrics.height < docMetrics.h) {
        $(this.frameId).style.bottom = '';
        $(this.frameId).style.top = (elemMetrics.y > 50) ? '-25px' : '-12px';
    } else { // fix against bottom
        var thisMetrics = Element.getMetrics (this.id);
        $(this.frameId).style.top = '';
        $(this.frameId).style.bottom = (thisMetrics.y - docMetrics.h + thisMetrics.height + 25) + 'px';
    }
};

Dragonfly.PopupBuble.prototype._tryAbove = function (elemMetrics, frameMetrics, docMetrics)
{
    var top = elemMetrics.y - frameMetrics.height;
    logDebug ('above:', top);
    if (top < 25) {
        logDebug ('too high.');
        return false;
    }
    $(this.id).style.right = '';
    $(this.id).style.left = elemMetrics.x + 'px';
    $(this.id).style.top = 'auto';
    $(this.id).style.bottom = (docMetrics.h - elemMetrics.y) + 'px';
    $(this.frameId).style.top = '';
    $(this.frameId).style.bottom = '17px';
    $(this.pointerId).className = 'pointer above';
    this._positionVertically(elemMetrics, frameMetrics, docMetrics);
    return true;
};

Dragonfly.PopupBuble.prototype._tryBelow = function (elemMetrics, frameMetrics, docMetrics)
{
    var bottom = elemMetrics.y + elemMetrics.height + frameMetrics.height
    logDebug ('below:', bottom);
    if (bottom > docMetrics.h - 25) {
        logDebug ('too low.');
        return false;
    }
    $(this.id).style.right = '';
    $(this.id).style.left = elemMetrics.x + 'px';
    $(this.id).style.bottom = '';
    $(this.id).style.top = (elemMetrics.y + elemMetrics.height) + 'px';
    $(this.frameId).style.top = '18px';
    $(this.frameId).style.bottom = '';
    $(this.pointerId).className = 'pointer below';
    this._positionVertically(elemMetrics, frameMetrics, docMetrics);
    return true;
};

Dragonfly.PopupBuble.prototype._tryLeft = function (elemMetrics, frameMetrics, docMetrics)
{
    var left = elemMetrics.x - frameMetrics.width;
    logDebug ('left:', left);
    if (left < 25) {
        logDebug ('too left.');
        return false;
    }
    $(this.id).style.left = 'auto';
    $(this.id).style.right = (docMetrics.w - elemMetrics.x) + 'px';
    $(this.id).style.bottom = '';
    $(this.id).style.top = elemMetrics.y + 'px';
    $(this.frameId).style.left = '';
    $(this.frameId).style.right = '17px';
    $(this.pointerId).className = 'pointer left';
    this._positionHorizontally(elemMetrics, frameMetrics, docMetrics);
    return true;
};

Dragonfly.PopupBuble.prototype._tryRight = function (elemMetrics, frameMetrics, docMetrics)
{
    var right = elemMetrics.x + elemMetrics.width + frameMetrics.width;
    logDebug ('right:', right);
    if (right > docMetrics.w - 25) {
        logDebug ('too right.');
        return false;
    }
    $(this.id).style.right = '';
    $(this.id).style.left = (elemMetrics.x + elemMetrics.width) + 'px';
    $(this.id).style.bottom = '';
    $(this.id).style.top = elemMetrics.y + 'px';
    $(this.frameId).style.left = '18px';
    $(this.frameId).style.right = '';
    $(this.pointerId).className = 'pointer right';
    this._positionHorizontally(elemMetrics, frameMetrics, docMetrics);
    return true;
};

Dragonfly.PopupBuble.prototype._giveUp = function (elemMetrics, frameMetrics, docMetrics)
{
    $(this.id).style.right = '';
    $(this.id).style.left = (docMetrics.w - 25) + 'px';
    $(this.id).style.bottom = '';
    $(this.id).style.top = (docMetrics.h - 25) + 'px';
    $(this.frameId).style.left = '';
    $(this.frameId).style.right = '0';
    $(this.frameId).style.top = '';
    $(this.frameId).style.bottom = '0';
    $(this.pointerId).className = '';
};

Dragonfly.PopupBuble.prototype.show = function (elem, position)
{
    this.shouldAutohide = false;

    if (elem) {
        this.setElem (elem);
    }
    if (!this.elem) {
        return;
    }

    this.addPoppedOut ();

    var elemMetrics = Element.getMetrics (this.elem);
    var frameMetrics = Element.getMetrics (this.frameId);
    var docMetrics = Dragonfly.getWindowDimensions();

    this.position = position = position || this.position || this.vertical;

    switch (position) {
    case this.vertical:
        this._tryAbove(elemMetrics, frameMetrics, docMetrics) || 
            this._tryBelow(elemMetrics, frameMetrics, docMetrics) || 
            this._tryRight(elemMetrics, frameMetrics, docMetrics) || 
            this._tryLeft(elemMetrics, frameMetrics, docMetrics) || 
            this._giveUp(elemMetrics, frameMetrics, docMetrics);
        break;
        
    case this.horizontal:
        this._tryRight(elemMetrics, frameMetrics, docMetrics) ||
            this._tryLeft(elemMetrics, frameMetrics, docMetrics) || 
            this._tryAbove(elemMetrics, frameMetrics, docMetrics) || 
            this._tryBelow(elemMetrics, frameMetrics, docMetrics) || 
            this._giveUp(elemMetrics, frameMetrics, docMetrics);
        break;
        
    case this.above:
        this._tryAbove(elemMetrics, frameMetrics, docMetrics) || 
            this._tryRight(elemMetrics, frameMetrics, docMetrics) ||
            this._tryBelow(elemMetrics, frameMetrics, docMetrics) ||
            this._tryLeft(elemMetrics, frameMetrics, docMetrics) ||
            this._giveUp(elemMetrics, frameMetrics, docMetrics);
        break;
        
    case this.right:
        this._tryRight(elemMetrics, frameMetrics, docMetrics) ||
            this._tryBelow(elemMetrics, frameMetrics, docMetrics) ||
            this._tryLeft (elemMetrics, frameMetrics, docMetrics) ||
            this._tryAbove(elemMetrics, frameMetrics, docMetrics) ||
            this._giveUp(elemMetrics, frameMetrics, docMetrics);
        break;
        
    case this.below:
        this._tryBelow(elemMetrics, frameMetrics, docMetrics) ||
            this._tryLeft(elemMetrics, frameMetrics, docMetrics) ||
            this._tryAbove(elemMetrics, frameMetrics, docMetrics) ||
            this._tryRight(elemMetrics, frameMetrics, docMetrics) ||
            this._giveUp(elemMetrics, frameMetrics, docMetrics);
        break;
        
    case this.left:
        this._tryLeft(elemMetrics, frameMetrics, docMetrics) ||
            this._tryAbove(elemMetrics, frameMetrics, docMetrics) ||
            this._tryRight(elemMetrics, frameMetrics, docMetrics) ||
            this._tryBelow(elemMetrics, frameMetrics, docMetrics) ||
            this._giveUp(elemMetrics, frameMetrics, docMetrics);
        break;
    }

    $(this.id).style.visibility = 'visible';

    return;
    logDebug ('positioning summary:');
    logDebug ('     elemMetrics:', repr (elemMetrics));
    logDebug ('    frameMetrics:', repr (frameMetrics));
    logDebug ('      docMetrics:', repr (docMetrics));
    logDebug ('        this.top:', $(this.id).style.top, 'this.left:', $(this.id).style.left);
    logDebug ('     this.bottom:', $(this.id).style.bottom, 'this.right:', $(this.id).style.right);
    logDebug ('       frame.top:', $(this.frameId).style.top, 'frame.left:', $(this.frameId).style.left);
    logDebug ('    frame.bottom:', $(this.frameId).style.bottom, 'frame.right:', $(this.frameId).style.right);
};

Dragonfly.PopupBuble.prototype.showAbove = function (elem) { this.show (elem, this.above); };
Dragonfly.PopupBuble.prototype.showBelow = function (elem) { this.show (elem, this.below); };
Dragonfly.PopupBuble.prototype.showLeft  = function (elem) { this.show (elem, this.left); };
Dragonfly.PopupBuble.prototype.showRight = function (elem) { this.show (elem, this.right); };
Dragonfly.PopupBuble.prototype.showVertical = function (elem) { this.show (elem, this.vertical); };
Dragonfly.PopupBuble.prototype.showHorizontal = function (elem) { this.show (elem, this.horizontal); };

Dragonfly.PopupBuble.prototype.canDisposeZone = Dragonfly.PopupBuble.prototype.canHideZone = function ()
{
    var canHide = !this.isVisible() || hasElementClass (this.frameId, 'closable');
    canHide = canHide && this.htmlZone.canHideZone();
    if (!canHide) {
        Dragonfly.notify (_('You have unsaved changes.'));
    }
    return canHide;
}

Dragonfly.PopupBuble.prototype.hide = Dragonfly.PopupBuble.prototype.hideZone = function (result)
{
    this.htmlZone.hideZone();
    this.shouldAutohide = false;
    this.removePoppedOut();
    this.setClosable();

    // force style changes to occur synchronously
    var tmp = document.createElement ('div');
    var frame = $(this.frameId), pointer = $(this.pointerId);
    appendChildNodes (tmp, frame, pointer);
    $(this.id).style.visibility = 'hidden';
    appendChildNodes (this.id, frame, pointer);
    
    return result; // in case we are used as a callback
};

Dragonfly.PopupBuble.prototype.dispose = Dragonfly.PopupBuble.prototype.disposeZone = function (result)
{
    this.htmlZone.disposeZone();
    this.htmlZone.destroy();
    this.setElem (null);
    document.body.removeChild ($(this.id));
    return result; // in case we are used as a callback
};

Dragonfly.PopupBuble.prototype.disposeAndReload = function (result)
{
    this.dispose();
    Dragonfly.clearNotify();
    Dragonfly.go ();
    return result; // in case we are used as a callback
};

Dragonfly.PopupBuble.prototype.showError = function (err)
{
    if (err instanceof CancelledError) {
        return err;
    }
    logError (Dragonfly.reprError (err));
    this.hide();
    var closeId = Dragonfly.nextId ('popup-close');
    var html = new Dragonfly.HtmlBuilder (
        '<p><strong>', _('An error has occured:'), '</strong><p style="text-align:center;">', 
        err.message, '<div class="actions">',
        '<input id="', closeId, '" type="button" value="', _('Great.'), '"></div>'
    );
    html.set (this.contentId);
    Event.observe (closeId, 'click', this.disposeAndReload.bindAsEventListener (this));
    this.setClosable (false);
    this.show();
};

Dragonfly.PopupBuble.prototype.autohide = function ()
{
    if (this.shouldAutohide) {
        this.hide();
    }
};

Dragonfly.ListSelector = function (parent)
{
    if (parent) {
        Dragonfly.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.ListSelector.prototype = { };

Dragonfly.ListSelector.prototype.buildHtml = function (html) 
{
    var d = Dragonfly;
    this.id = d.nextId ('list-selector');
    this.unselectedId = d.nextId ('list-selector-unselected');
    html.push ('<form id="', this.id, '" class="list-selector">',
               '<input autocomplete=off type="search" name="search" class="search-field">',
               '<ul id="', this.unselectedId, '" class="unselected-items"></ul>',
               '</form>');
    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.ListSelector.prototype.connectHtml = function (elem) 
{
    Event.observe ($(this.id).search, 'keyup', this.autocomplete.bindAsEventListener(this));
    return elem;
};

Dragonfly.ListSelector.prototype.autocomplete = function ()
{
    for (var i = 0; i < this.hits.length; i++) {
        var hit = this.id + this.hits[i];
        removeElementClass (hit, 'hit');
    }

    this.hits = this.completer.complete ($(this.id).search.value);
    if (this.hits.length == 0) {
        removeElementClass (this.id, 'searching');
        return;
    }
    addElementClass (this.id, 'searching');
    for (var i = 0; i < this.hits.length; i++) {
        var hit = this.id + this.hits[i];
        addElementClass (hit, 'hit');
    }
};

Dragonfly.ListSelector.prototype.setItems = function (labels, ids)
{
    this.completer = new Dragonfly.AutoCompleter (labels, ids);
    this.hits = [ ];
    var html = new Dragonfly.HtmlBuilder();
    for (var i = 0; i < labels.length; i++) {
        html.push ('<li id="', this.id, ids[i], '">');
        html.push (Dragonfly.escapeHTML(labels[i]), '</li>');
    }
    html.set (this.unselectedId);
};

Dragonfly.ListSelector.prototype.addItem = function (label, id)
{
    var d = Dragonfly;
    this.completer.addItem (label, id);
    var child = LI({id:this.id + id}, Dragonfly.escapeHTML (label));
    appendChildNodes (this.unselectedId, child);
    return child;
};

Dragonfly.DisclosingLink = function (parent, message /*, childern... */)
{
    var d = Dragonfly;

    this.message = d.escapeHTML (message);

    this.id = d.nextId ('disclosing-link');
    this.checkId = d.nextId ('disclosing-check');
    this.linkId = d.nextId ('disclosing-href');
    this.childId = d.nextId ('disclosing-container');
    this.children = list(arguments).slice (2);

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.DisclosingLink.prototype = { };

Dragonfly.DisclosingLink.prototype.buildHtml = function (html /*, children */)
{
    html.push ('<div id="', this.id, '" class="disclosing-link">',
               '<label>',
               '<input id="', this.checkId, '" class="disclosing-check" style="visibility: hidden" type="checkbox">',
               '<a id="', this.linkId, '" class="disclosing-href">',
               this.message,
               '</a>',
               '</label>',
               '<div id="', this.childId, '" class="disclosing-container" style="display: none">');

    for (var i = 0; i < this.children.length; i++) {
        html.buildChild (this.children[i]);
    }

    for (var i = 1; i < arguments.length; i++) {
        html.buildChild (arguments[i]);
    }
    delete this.children;

    html.push ('</div></div>');

    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.DisclosingLink.prototype.connectHtml = function (elem)
{
    this.link = $(this.linkId);
    this.textNode = this.link.firstChild;

    Event.observe (this.link, 'click', this.discloseClicked.bindAsEventListener (this));
};

Dragonfly.DisclosingLink.prototype.disclose = function ()
{
    if (this.link.parentNode) {
        swapDOM (this.link, this.textNode);
    }
    // this gets set since the href is in a label
    // $(this.checkId).checked = true;
    $(this.checkId).style.visibility = 'visible';
    Element.show (this.childId);
};

Dragonfly.DisclosingLink.prototype.discloseClicked = function (evt)
{
    this.disclose();
};

Dragonfly.DisclosingLink.prototype.enclose = function ()
{
    Element.hide (this.childId);
    if (!this.link.parentNode) {
        swapDOM (this.textNode, this.link);
        $(this.link).appendChild (this.textNode);
    }
    $(this.checkId).style.visibility = 'hidden';
    $(this.checkId).checked = false;
};

Dragonfly.TzSelector = function (tzid, parent, isGlobal)
{
    var d = Dragonfly;

    this.id = d.nextId ('tz-wrapper');
    this.selectId = d.nextId ('tz-selector');
    this.editorId = d.nextId ('tz-editor');
    
    this.isGlobal = isGlobal;
    this.timezones = deepclone (d.Calendar.Preferences.getTimezones());
    
    this.tzIndex = null;
    for (var i = 0; (this.tzIndex == null) && i < this.timezones.length; i++) {
        if (this.timezones[i].tzid == tzid) {
            this.tzIndex = i;
        }
    }
    if (tzid == '/bongo/floating') {
        this.tzIndex = this.timezones.length; // see below
    } else if (this.tzIndex == null) { // tzid is not in preferences
        var name = (startsWith (tzid, '/bongo')) ? tzid.slice (6) : tzid;
        this.timezones.push ({ name: name, tzid: tzid });
        this.tzIndex = this.timezones.length - 1;
    }
    if (!isGlobal) {
        this.timezones.push ({ name: 'Floating', tzid: '/bongo/floating' });
    }

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.TzSelector.timezonemap = [
    {
        name: 'United States',
        timezones: [ 
            {tzid: '/bongo/America/New_York', name: 'Eastern'},
            {tzid: '/bongo/America/Chicago', name: 'Central'},
            {tzid: '/bongo/America/Boise', name: 'Mountain'},
            {tzid: '/bongo/America/Los_Angeles', name: 'Pacific'},
            {tzid: '/bongo/America/Anchorage', name: 'Alaska'},
            {tzid: '/bongo/America/Honolulu', name: 'Hawaii'}
        ]
    }, {
        name: 'Europe',
        timezones: [
            {tzid: '/bongo/Europe/Tirane', name: 'Albania' },
            {tzid: '/bongo/Europe/Vienna', name: 'Austria' },
            {tzid: '/bongo/Europe/Brussels', name: 'Belgium' },
            {tzid: '/bongo/Europe/Sofia', name: 'Bulgaria' },
            {tzid: '/bongo/Europe/Zagreb', name: 'Croatia' },
            {tzid: '/bongo/Europe/Prague', name: 'Czech Republic' },
            {tzid: '/bongo/Europe/Copenhagen', name: 'Denmark' },
            {tzid: '/bongo/Europe/Berlin', name: 'Germany' },
            {tzid: '/bongo/Europe/Athens', name: 'Greece' },
            {tzid: '/bongo/Europe/Paris', name: 'France'},
            {tzid: '/bongo/Europe/London', name: 'United Kingdom'},
            {tzid: '/bongo/Europe/Madrid', name: 'Madrid'}
        ]
    }, {
        name: 'Central & South America',
        timezones: [
            {tzid: '/bongo/America/Argentina/Buenos_Aires', name: 'Argentina'}
        ]
    }
];

Dragonfly.TzSelector.prototype = { };

Dragonfly.TzSelector.prototype.buildHtml = function (html)
{
    html.push ('<div id="', this.id, '" class="tz-wrapper">');
    html.push ('<select id="', this.selectId, '">');
    this.buildTimezoneOptions (html);
    html.push ('</select><br>');
    html.push ('<div id="', this.editorId, '" class="tz-editor"></div></div>');
    html.addCallback (bind ('connectHtml', this));
};

Dragonfly.TzSelector.prototype.buildTimezoneOptions = function (htmlbuilder, timezones)
{
    var html = htmlbuilder || new Dragonfly.HtmlBuilder();
    timezones = timezones || this.timezones;    

    for (var i = 0; i < timezones.length; i++) {
        var zone = timezones[i];
        html.push ('<option value="', zone.tzid, '">', zone.name);
    }
    if (timezones === this.timezones) {
        if (this.isGlobal) {
            html.push ('<option value="add">', _('Add timezone...'));
            if (this.timezones.length > 1) {
                html.push ('<option value="edit">', _('Edit list...'));
            }
        } else {
            html.push ('<option value="add">', _('Other...'));
        }
    }
    
    if (html !== htmlbuilder) {
        html.set (this.selectId);
    }
}

Dragonfly.TzSelector.prototype.connectHtml = function (elem)
{
    var select = $(this.selectId);
    select.selectedIndex = this.tzIndex;
    Event.observe (select, 'change', this.handleChange.bindAsEventListener (this));
    return elem;
};

// Programmatic Interfaces
Dragonfly.TzSelector.prototype.setChangeListener = function (listener)
{
    this.changeListener = listener;
}

Dragonfly.TzSelector.prototype.setEnabled = function (enabled)
{
    $(this.selectId).disabled = !enabled;
};

Dragonfly.TzSelector.prototype.getTzid = function () 
{
    return this.getTimezone().tzid;
};

Dragonfly.TzSelector.prototype.setTzid = function (tzid) 
{
    $(this.selectId).value = tzid;
    this.tzIndex = $(this.selectId).selectedIndex;
    if (this.changeListener) {
        this.changeListener();
    }
};

Dragonfly.TzSelector.prototype.getTimezone = function () 
{
    return this.timezones[this.tzIndex];
};

Dragonfly.TzSelector.prototype.setTimezone = function (timezone) 
{
    var tzid = timezone.tzid;
    for (var i = 0, tzIndex = null; (tzIndex == null) && i < this.timezones.length; i++) {
        if (this.timezones[i].tzid == tzid) {
            tzIndex = i;
        }
    }
    if (tzIndex == null) {
        this.addTimezone (timezone);
    } else {
        this.setTzid (tzid);
    }
};

Dragonfly.TzSelector.prototype.addTimezone = function (timezone)
{
    this.timezones.push (timezone);
    this.buildTimezoneOptions();
    this.setTzid (timezone.tzid);
    if (this.isGlobal) {
        Dragonfly.Calendar.Preferences.setTimezones (this.timezones);
    }
};

Dragonfly.TzSelector.prototype.delTimezone = function (index)
{
    if (!this.isGlobal) {
        logError ('deleting timezones is only supported on global timezone selector');
        return;
    }
    this.timezones.splice (index, 1);
    this.buildTimezoneOptions();

    var calprefs = Dragonfly.Calendar.Preferences;
    if (index = this.tzIndex) {
        this.setTzid (calprefs.getDefaultTimezone());
    }
    calprefs.setTimezones (this.timezones);
};

// Event-Driven Interfaces
Dragonfly.TzSelector.prototype.handleChange = function (evt)
{
    var tzid = $(this.selectId).value;
    if (tzid == 'edit') {
        return this.showEditor ();
    } else if (tzid == 'add') {
        return this.showBrowser ();
    }
    this.setTzid (tzid);
};

Dragonfly.TzSelector.prototype.showEditor = function ()
{
    var d = Dragonfly;
    $(this.selectId).disabled = true;

    this.editSelectId = d.nextId ('tz-edit-select');
    var cancel = d.nextId ('tz-cancel');
    this.delId = d.nextId ('tz-delete');
    this.setDefaultId = d.nextId ('tz-set-default');
    var add = d.nextId ('tz-add');
    
    var html = new d.HtmlBuilder ('<select id="', this.editSelectId, '" size="5">');
    var defaultTzid = d.Calendar.Preferences.getDefaultTimezone ();
    var selectedTzid = this.timezones[this.tzIndex].tzid;
    
    for (var i = 0; i < this.timezones.length; i++) {
        var zone = this.timezones[i];
        if (zone.tzid == defaultTzid) {
            html.push ('<option value="', zone.tzid, '" disabled="disabled">', zone.name, ' (default)');
        } else {
            html.push ('<option value="', zone.tzid, '">', zone.name);
        }
    }
    html.push ('</select><div class="actions">',
               '<input id="', cancel, '" type="button" value="', _('Cancel'), '"> ',
               '<input id="', this.delId, '" type="button" value="', _('Delete'), '"> ',
               '<input id="', this.setDefaultId, '" type="button" value="', _('Set as Default'), '"> ',
               '<input id="', add, '" type="button" value="', _('Add...'), '"></div>');
    html.set (this.editorId);
    
    Event.observe (cancel, 'click', this.cancel.bindAsEventListener (this));
    Event.observe (this.delId, 'click', this.del.bindAsEventListener (this));
    Event.observe (this.setDefaultId, 'click', this.setDefault.bindAsEventListener (this));
    Event.observe (add, 'click', this.showBrowser.bindAsEventListener (this));

    if (selectedTzid != defaultTzid) {
        $(this.editSelectId).value = selectedTzid;
    } else {
        for (var i = 0; i < this.timezones.length; i++) {
            var tzid = this.timezones[i].tzid;
            if (tzid != defaultTzid) {
                $(this.editSelectId).value = tzid;
                break;
            }
        }
    }
    
    $(this.editSelectId).focus();       
    showElement (this.editorId);
};

Dragonfly.TzSelector.prototype.showBrowser = function ()
{
    var d = Dragonfly;
    
    this.regionSelectId = d.nextId ('tz-region-select');
    this.zoneSelectId = d.nextId ('tz-zone-select');
    var zonemap = Dragonfly.TzSelector.timezonemap;

    var cancel = d.nextId ('tz-cancel');
    var add = d.nextId ('tz-add');
    var addDefault = d.nextId ('tz-add-default');
    
    var html = new d.HtmlBuilder ('<table><tr><th>', _('Region'), '</th><th>', _('Timezone'), '</th></tr>');
    html.push('<tr><td><select id="', this.regionSelectId, '" size="5">');
    for (var i = 0; i < zonemap.length; i++) {
        html.push ('<option>', zonemap[i].name);
    }
    html.push ('</select></td>');
    html.push ('<td><select id="', this.zoneSelectId, '" size="5"></select></td>');
    html.push ('</tr></table>');
    html.push ('<div class="actions"><input id="', cancel, '" type="button" value="', _('Cancel'), '"> ');
    if (this.isGlobal) {
        html.push ('<input id="', add, '" type="button" value="', _('Add'), '"> ',
                   '<input id="', addDefault, '" type="button" value="', _('Add as Default'), '"></div>');
    } else {
        html.push ('<input id="', add, '" type="button" value="', _('Set'), '">');
    }   
    html.set (this.editorId);
    
    Event.observe (this.regionSelectId , 'change', this.updateBrowser.bindAsEventListener (this));
    Event.observe (cancel, 'click', this.cancel.bindAsEventListener (this));
    Event.observe (add, 'click', this.add.bindAsEventListener (this));
    if (this.isGlobal) {
        Event.observe (addDefault, 'click', this.addDefault.bindAsEventListener (this));
    }
    
    $(this.regionSelectId).selectedIndex = 0;
    this.updateBrowser();
    $(this.selectId).disabled = true;
    showElement (this.editorId);
};

Dragonfly.TzSelector.prototype.updateBrowser = function ()
{
    var d = Dragonfly;
    var timezones = d.TzSelector.timezonemap[$(this.regionSelectId).selectedIndex].timezones;
    var html = new d.HtmlBuilder();
    this.buildTimezoneOptions (html, timezones);
    html.set (this.zoneSelectId);
    $(this.zoneSelectId).value = timezones[0].tzid;
};

Dragonfly.TzSelector.prototype.del = function ()
{
    this.delTimezone ($(this.editSelectId).selectedIndex);
    this.close();
};

Dragonfly.TzSelector.prototype.add = function ()
{
    var zonemap = Dragonfly.TzSelector.timezonemap[$(this.regionSelectId).selectedIndex];
    var timezone = zonemap.timezones[$(this.zoneSelectId).selectedIndex];
    this.addTimezone (timezone);
    this.close();
};

Dragonfly.TzSelector.prototype.addDefault = function ()
{
    this.add();
    Dragonfly.Calendar.Preferences.setDefaultTimezone (this.getTzid());
};

Dragonfly.TzSelector.prototype.setDefault = function ()
{
    this.setTzid ($(this.editSelectId).value);
    this.close();
    Dragonfly.Calendar.Preferences.setDefaultTimezone (this.getTzid());
};

Dragonfly.TzSelector.prototype.cancel = function (evt)
{
    $(this.selectId).selectedIndex = this.tzIndex;
    this.close();
};

Dragonfly.TzSelector.prototype.close = function ()
{
    this.setEnabled (true);
    hideElement (this.editorId);
};

Dragonfly.LabeledEntry = function (elem, label, value)
{
    var d = Dragonfly;
    var l = d.LabeledEntry;

    elem = $(elem);

    elem.setValue = l._setValue;
    elem.getValue = l._getValue;

    elem.emptyLabel = label;
    
    elem.setValue (value);

    Event.observe (elem, 'focus',
                   (function (evt) {
                       if (Element.hasClassName (this, 'empty')) {
                           this.value = '';
                           Element.removeClassName (this, 'empty');
                       }
                       this.select();
                   }).bindAsEventListener (elem));
    Event.observe (elem, 'blur',
                   (function (evt) {
                       this.setValue (this.value);
                   }).bindAsEventListener (elem));
    return elem;
};

Dragonfly.LabeledEntry._setValue = function (value)
{
    if (value) {
        this.value = value;
        Element.removeClassName (this, 'empty');
    } else {
        this.value = this.emptyLabel;
        Element.addClassName (this, 'empty');
    }
        
};

Dragonfly.LabeledEntry._getValue = function ()
{
    return Element.hasClassName (this, 'empty') ? '' : this.value;
};

Dragonfly.ColorPicker = function (parent)
{
    var d = Dragonfly;

    this.id = d.nextId ('calendar-color');

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.ColorPicker.prototype = { };

Dragonfly.ColorPicker.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    html.push ('<select id="', this.id, '">');

    forEach (d.Colors, function (color) {
                 html.push ('<option>', color, '</option>');
             });

    html.push ('</select>');
};

Dragonfly.ColorPicker.prototype.getColor = function ()
{
    return $(this.id).value;
};

Dragonfly.ColorPicker.prototype.setColor = function (color)
{
    $(this.id).value = color;
};
