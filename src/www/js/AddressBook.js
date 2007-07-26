Dragonfly.AddressBook = { 
    Preferences: { },
    sets: [ 'all', 'personal', 'collected', 'system' ],
    handlers: { },
    defaultSet: 'personal', 
    defaultHandler: 'contacts',
    setLabels: {
        all: 'All',
        personal: 'Personal',
        collected: 'Collected', 
        system: 'System'
    }    
};

Dragonfly.tabs['addressbook'] = Dragonfly.AddressBook;

Dragonfly.AddressBook.buildNewContact = function ()
{
    return { fn: '', n: '', version: [{ value: '3.0' }] };
};

Dragonfly.AddressBook.loadContacts = function ()
{
    var d = Dragonfly;
    d.AddressBook.contactMap = { };
    var request = d.requestJSON ('GET', new d.Location ({ tab: 'addressbook' }));
    return request.addCallback (function (result) {
        for (var i = 0; i < result.length; i++) {
            var contact = result[i];
            contact.sortKey = d.AddressBook.getContactSortKey (contact.fn);
            d.AddressBook.contactMap[contact.bongoId] = contact;
        }
        result.sort (keyComparator ('sortKey'));
        return result;
    });
};

Dragonfly.AddressBook.getContactSortKey = function (name) 
{
    var keylist = name.split (/\s+/);
    var i = keylist.length - 1;
    while (i > 0 && keylist[i-1].charAt (0) == keylist[i-1].charAt (0).toLowerCase()) {
        i--;
    }
    return keylist.slice(i).join(' ').toLowerCase();
};

Dragonfly.AddressBook.compareContacts = function (a, b)
{
    var getKey = Dragonfly.AddressBook.getContactSortKey;
    return compare (getKey (a.fn), getKey (b.fn));
};


Dragonfly.AddressBook.getNameForContact = function (bongoId)
{
    var contact = Dragonfly.AddressBook.contactMap[bongoId];
    return contact && contact.fn;
};

Dragonfly.AddressBook.loadContact = function (bongoId)
{
    var d = Dragonfly;
    return d.requestJSON ('GET', new d.Location ({ tab: 'addressbook', contact: bongoId }));
};

Dragonfly.AddressBook.saveContact = function (contact)
{
    var d = Dragonfly, loc, request;
    if (contact.bongoId) {
        loc = new d.Location ({ tab: 'addressbook', contact: contact.bongoId });
        request = d.request ('PUT', loc.getServerUrl(), contact);
    } else {
        loc = new d.Location ({ tab: 'addressbook'});   
        request = d.requestJSONRPC ('createContact', loc, contact);
    }
    return request.addCallback (function (result) {
        contact.bongoId = result.bongoId;
        Dragonfly.AddressBook.contactMap[result.bongoId] = contact;
        return result;
    });
};

Dragonfly.AddressBook.delContact = function (bongoId)
{
    var d = Dragonfly;
    var loc = new d.Location ({ tab: 'addressbook', set: 'personal', contact: bongoId });
    var request = d.request ('DELETE', loc.getServerUrl());
    return request.addCallback (function (result) {
        delete d.AddressBook.contactMap[bongoId];
        return result;
    });
};

Dragonfly.AddressBook.Preferences.update = function (prefs)
{
    if (!prefs.addressbook) {
        prefs.addressbook = { };
    }
};

Dragonfly.AddressBook.Preferences.getMyContactId = function ()
{
    return Dragonfly.Preferences.prefs.addressbook.me;
};

Dragonfly.AddressBook.Preferences.setMyContactId = function (guid)
{
    Dragonfly.Preferences.prefs.addressbook.me = guid || null;
    Dragonfly.Preferences.save();
};

Dragonfly.AddressBook.Contacts = { };

Dragonfly.AddressBook.handlers['contacts'] = Dragonfly.AddressBook.Contacts;

Dragonfly.AddressBook.Contacts.parseArgs = function (loc, args)
{
    if (!args) {
        return;
    }

    var arg = args[0];
    if (arg[0] == '-') {
        return;
    }
    var matches = arg.match (/^page(\d*)$/);
    if (matches) {
        loc.page = parseInt (matches[1]);
    }
};

Dragonfly.AddressBook.Contacts.getArgs = function (loc)
{
    return [ loc.page ? 'page' + loc.page : '-' ];
};

Dragonfly.AddressBook.Contacts.parsePath = function (loc, path)
{
    loc.contact = path.shift ();
    loc.valid = true;
    
    return loc;
};

Dragonfly.AddressBook.Contacts.getServerUrl = function (loc, path)
{
    if (loc.contact) {
        path.push (loc.contact);
    }

    return false;
};

Dragonfly.AddressBook.Contacts.convert = function (contactname, email)
{
    var d = Dragonfly;
    
    var picker = d.AddressBook.sideboxPicker.unselectedId;
    var children = $(picker).childNodes;
    var contact;
    var myemail = d.Preferences.prefs.mail.from;
    
    // Loop through and filter contacts that match.
    for (var i = 0; i < children.length; i++) {
        // Find out our bongoId
        var tempelement = children[i];
        var bId = tempelement.id;
        bId = bId.substring($(picker).parentNode.id.length);
        
        var tempcontact = d.AddressBook.contactMap[bId];
        if (tempcontact)
        {        
            logDebug('Checking contact ' + bId);
        
            if (email && tempcontact.email)
            {
                // We have an email from the page - use it!
                for (var x = 0; x < tempcontact.email.length; x++) {
                    if (tempcontact.email[x] && tempcontact.email[x].toLowerCase() == email)
                    {
                        logDebug('Result found by checking contact email against mail.');
                        contact = tempcontact;
                        continue;
                    }
                }
            }
            else
            {
                // First, check if it's just sent to us (which means display our own card)
                if (tempcontact.email)
                {
                    for (var x = 0; x < tempcontact.email.length; x++) {
                        if (tempcontact.email[x] && tempcontact.email[x].toLowerCase() == myemail && contactname == myemail) {
                            logDebug('Result found via checking if contact was us.');
                            contact = tempcontact;
                            continue;
                        }
                    }
                }
                
            
                // Search based on name, not email.
                if (tempcontact.fn.toLowerCase() == contactname.toLowerCase())
                {
                    logDebug('Result found via name search.');
                    contact = tempcontact;
                    continue;
                }
            }
        }
    }
    
    return contact;
}

// Sidebar Contact Picker
Dragonfly.AddressBook.ContactPicker = function (parent)
{
    if (parent) {
        Dragonfly.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.AddressBook.ContactPicker.prototype = clone (Dragonfly.ListSelector.prototype);

Dragonfly.AddressBook.ContactPicker.prototype.buildHtml = function (html)
{
    Dragonfly.ListSelector.prototype.buildHtml.call (this, html);
    this.newContactId = Dragonfly.nextId ('contact-new');
    html.push ('<p style="padding-top:3px; text-align:center;">');
    html.push ('<a id="', this.newContactId, '" class="action">', _('Add Contact'), '</a>');
    
    this.popup = new Dragonfly.AddressBook.ContactPopup();
};

Dragonfly.AddressBook.ContactPicker.prototype.connectHtml = function (elem)
{
    elem = Dragonfly.ListSelector.prototype.connectHtml.call (this, elem);
    Event.observe (this.unselectedId, 'click', this.handleClick.bindAsEventListener (this));
    Event.observe (this.newContactId, 'click', this.newContactHandler.bindAsEventListener (this));
};

Dragonfly.AddressBook.ContactPicker.prototype.load = function ()
{
    return Dragonfly.AddressBook.loadContacts().addCallback (bind ('layout', this));
};

Dragonfly.AddressBook.ContactPicker.prototype.layout = function (jsob)
{
    var labels = [ ], ids = [ ];
    for (var i = 0; i < jsob.length; i++) {
        labels.push (jsob[i].fn);
        ids.push (jsob[i].bongoId);
    }
    this.setItems (labels, ids);
};

Dragonfly.AddressBook.ContactPicker.prototype.handleClick = function (evt)
{
    var d = Dragonfly;
    var element = d.findElement (Event.element (evt), 'LI', $(this.id));
    if (!element) {
        return;
    }
    var bongoId = element.id.slice (this.id.length);
    if (!this.popup.canHideZone()) {
        return;
    }
    this.popup.setElem (element);
    d.AddressBook.loadContact(bongoId).addCallback(bind ('summarize', this.popup));   
};

Dragonfly.AddressBook.ContactPicker.prototype.newContactHandler = function (evt)
{
    if (!this.popup.canHideZone()) {
        return;
    }
    var contact = Dragonfly.AddressBook.buildNewContact();
    this.popup.edit (evt, contact, this.newContactId);
};

// The ContactPopup manages the interaction for creating and editing contacts
Dragonfly.AddressBook.ContactPopup = function (elem)
{    
    Dragonfly.PopupBuble.apply (this, arguments);
};

Dragonfly.AddressBook.ContactPopup.prototype = clone (Dragonfly.PopupBuble.prototype);

Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes = {
    props:   ['tel', 'im', 'email', 'url', 'org', 'adr'],
    email:   {name:'email', label:'Email', prop: 'email', type: 'INTERNET', protocol: 'mailto:'},
    phone:   {name:'phone', label:'Phone', prop: 'tel', protocol: 'tel:'},
    fax:     {name:'fax', label:'Fax', prop: 'tel', type: 'FAX', protocol: 'tel:'},
    mobile:  {name:'mobile', label:'Mobile', prop: 'tel', type: 'CELL', protocol: 'tel:'},
    im:      {name:'im', label:'IM', prop: 'im', protocol: 'im:'},
    website: {name:'website', label:'Website', prop: 'url', protocol: 'http:'},
    org:     {name:'org', label:'Company', prop: 'org'},
    address: {name:'address', label:'Address', prop: 'adr', longtext:true}
};

Dragonfly.AddressBook.ContactPopup.prototype.Form = [{
    category: 'Personal',
    fields: [
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.email,
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.mobile,
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.im,
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.website
    ]}, {
    category: 'Home',
    type: 'HOME',
    fields: [
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.phone,
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.address
    ]}, {
    category: 'Work',
    type: 'WORK',
    fields: [
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.org,
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.website,
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.email,
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.phone,
        Dragonfly.AddressBook.ContactPopup.prototype.FieldTypes.address
    ]}];

Dragonfly.AddressBook.ContactPopup.prototype.dispose =
Dragonfly.AddressBook.ContactPopup.prototype.disposeZone = function ()
{
    this.hide();
    delete this.contact;
    delete this.formNames;
};

Dragonfly.AddressBook.ContactPopup.prototype.summarize = function (contact, elem)
{
    var d = Dragonfly;

    this.hide ();
    this.setClosable (true);
    this.contact = contact = (contact) ? contact : this.contact;

    var deleteId = d.nextId ('person-delete');
    var editId = d.nextId ('person-edit');

    var html = new d.HtmlBuilder();
    html.push ('<p><b>' + contact.fn + '</b>');
    html.push ('<table><tr>');
    html.push ('<td><ul>');
    var i;
    if (this.contact.email) {
        for (i = 0; i < contact.email.length; i++) {
            html.push ('<li><a href="mailto:', this.contact.email[i].value, '">');
            html.push (contact.email[i].value, '</a>');
        }
    }
    html.push ('</ul></td><td><ul>');
    if (this.contact.tel) {
        for (i = 0; i < this.contact.tel.length; i++) {
            html.push ('<li>', contact.tel[i].value);
        }
    }
    html.push ('</ul></td></tr></table>');
    
    var loc = new d.Location ({ tab: 'mail', set:'all', handler:'contacts', 
                                contact: this.contact.bongoId });
    html.push ('<div class="actions">',
               '<a id="', deleteId, '" class="secondary">', _('Delete'), '</a>',
               '<a href="#', loc.getClientUrl(), '">', _('Show Conversations'), '</a>',
               '<a id="', editId, '">', _('Edit'), '</a></div>');
    html.set (this.contentId);

    Event.observe (deleteId, 'click', this.delConfirm.bindAsEventListener (this));
    Event.observe (editId, 'click', this.edit.bindAsEventListener (this));
    this.showVertical (elem);
};

Dragonfly.AddressBook.ContactPopup.prototype.buildTab = function (html, tab)
{
    var fields = tab.fields;
    var formFields = this.formFields;

    html.push ('<ul class="form-list">');    
    for (var i = 0; i < fields.length; i++) {
        var field = fields[i];
        
        var name = this.getName (field.prop, [tab.type, field.type]);
        name = this.getFreeField (name, true);
        html.push ('<li><label>', _(field.label), '</label>');
        if (field.longtext) {
            html.push ('<textarea name="', name, '"></textarea>');
        } else {
            html.push ('<input name="', name, '">');
        }
        html.push ('</li>');
    }
    html.push ('</ul>');
    return html;
};

Dragonfly.AddressBook.ContactPopup.prototype.loadContact = function ()
{
    var AB = Dragonfly.AddressBook;
    var Fields = AB.ContactPopup.prototype.FieldTypes;
    
    var form = $(this.formId);
    form.fn.value = this.contact.fn;
    //var myId = AB.Preferences.getMyContactId();
    
    //logDebug('myID: ' + myId);
    logDebug('form id: ' + form);
    logDebug('this.contact: ' + this.contact);
    logDebug('this.contact.fn: ' + this.contact.fn);
    
    //form.isMyContact.checked = myId && (myId == this.contact.bongoId);
    
    for (var i = 0; i < Fields.props.length; i++) {
        var prop = Fields.props[i];
        var values = this.contact[prop];
        if (!values) {
            continue;
        }
        for (var j = 0; j < values.length; j++) {
            var value = values[j];
            var input = this.getFreeField (this.getName (prop, value.type), false);
            if (input) {
                input.value = $V(value);
            }
        }
    }
};

Dragonfly.AddressBook.ContactPopup.prototype.serializeContact = function ()
{
    var AB = Dragonfly.AddressBook;
    var props = AB.ContactPopup.prototype.FieldTypes.props;
    var contact = this.contact;
    var form = $(this.formId);
    contact.fn = form.fn.value;

    /*if (form.isMyContact.checked) {
        this.setMyContact = true;
        this.myId = this.contact.bongoId || '';
    } else if (this.contact.bongoId == AB.Preferences.getMyContactId()) {
        this.setMyContact = true;
        this.myId = null;
    }*/
    
    // clear out existing properties
    forEach (props, function (prop) { delete contact[prop]; });
    
    for (var name in this.formNames) { // for each type of field
        var count = this.formNames[name];
        var components = name.split ('-');
        var prop = components[0];
        var types = components.slice(1);
        
        for (var i = 0; i <= count; i++) { // for each field
            var value = form[name+String(i)].value;
            if (value && value != '') {
                var proparray = contact[prop] || [ ];
                proparray.push ({value:value, type:types});
                contact[prop] = proparray;
            }
        }
    }
};

Dragonfly.AddressBook.ContactPopup.prototype.getName = function (prop, types)
{
    if (!types) {
        return prop;
    }
    var result = [prop];
    var home = false, work = false;
    for (var i = 0; i < types.length; i++) {
        var type = types[i];
        if (type == 'HOME') {
            home = true;
        } else if (type == 'WORK') {
            work = true;
        } else if (type && type != 'pref') {
            result.push (type);
        }
    }
    if (home) {
        result.push ('HOME');
    } else if (work) {
        result.push ('WORK');
    }
    return result.join('-');    
};

Dragonfly.AddressBook.ContactPopup.prototype.getFreeField = function (name, asString)
{
    if (asString) {
        var n = this.formNames[name];
        n = (n == undefined) ? 0 : n + 1;
        this.formNames[name] = n;
        return name + String (n);
    }
    return $(this.formId)[name+'0'];
};

Dragonfly.AddressBook.ContactPopup.prototype.edit = function (evt, contact, elem)
{
    var d = Dragonfly;

    this.hide ();
    
    if (!this.contact)
    {
        this.contact = contact;
    }

    this.formId = d.nextId ('contact-form');
    this.formNames = { };

    var notebook = new d.Notebook();
    for (var i = 0; i < this.Form.length; i++) {
        var tab = this.Form[i];
        var page = notebook.appendPage (tab.category);
        this.buildTab (new d.HtmlBuilder(), tab).set(page);
    }

    var form = [
        '<form id="', this.formId, '"><img style="float:left; margin-right:5px;" align="middle" ',
        'src="img/contact-unknown.png"><div><p><input style="width:75%;" name="fn"></div>', notebook, '</form>'];

    var actions = [{ value: _('Cancel'), onclick: 'dispose'}, { value: _('Save'), onclick: 'save'}];
    if (this.contact.bongoId) { // only add delete for pre-existing contacts
        actions.unshift ({ value: _('Delete'), onclick: 'del', secondary: true });
    }
    this.setForm (form, actions);
    
    this.loadContact();
    this.showVertical (elem);
};

Dragonfly.AddressBook.ContactPopup.prototype.del = function ()
{
    Dragonfly.notify ('Deleting contact...', true);
    var elem = this.elem;
    var noremove = this.skipremovechild;
    Dragonfly.AddressBook.delContact(this.contact.bongoId).addCallback (
        function (result) {
            logDebug('Remove child? ' + this.skipremovechild);
            if (elem && !noremove) {
                elem.parentNode.removeChild(elem);
            }
            Dragonfly.notify (_('Changes saved.'));
            return result;
        });
    this.dispose();
};

Dragonfly.AddressBook.ContactPopup.prototype.save = function ()
{
    var AB = Dragonfly.AddressBook;
    this.serializeContact();
    this.elem.innerHTML = this.contact.fn;

    Dragonfly.notify (_('Saving changes...'), true);
    AB.saveContact (this.contact).addCallbacks (bind (
        function (result) {
            // update preferences with contact ID
            /*if (this.setMyContact) {
                var id = (this.myId == '') ? result.bongoId : this.myId;
                AB.Preferences.setMyContactId (id);
            }*/
            
            // Create sidebox element if this is a new contact
            if (result.bongoId) {
                this.contact.bongoId = result.bongoId;
                this.setElem (AB.sideboxPicker.addItem (this.contact.fn, result.bongoId));
                $(this.elem).scrollIntoView();
            }
            
            Dragonfly.notify (_('Changes saved.'));
            
            this.summarize ();
	        this.shouldAutohide = true;
	        callLater (2, bind ('autohide', this));
            return result;
        }, this), bind ('showError', this));
};


Dragonfly.AddressBook.ContactPopup.prototype.delConfirm = function ()
{
    var d = Dragonfly;
    this.hide();
    var text = [
        '<p>', d.format(_('Are you sure you want to delete the contact "{0}"? This cannot be undone.'),
        Dragonfly.escapeHTML (this.contact.fn)), '</p>'
    ];

    var actions = [{ value: _('Cancel'), onclick: 'dispose'}, { value: _('Delete'), onclick: 'del'}];
    this.setForm (text, actions);
    this.show();
};

Dragonfly.AddressBook.UserProfile = function (elem)
{
    this.formId = elem;
    this.bongoId = null;
};

Dragonfly.AddressBook.UserProfile.prototype = {};

Dragonfly.AddressBook.UserProfile.prototype.build = function (contact)
{
    var d = Dragonfly;
    
    this.contact = contact;
    
    this.formNames = { };

    var notebook = new d.Notebook();
    for (var i = 0; i < Dragonfly.AddressBook.ContactPopup.prototype.Form.length; i++) {
        var tab = d.AddressBook.ContactPopup.prototype.Form[i];
        var page = notebook.appendPage (tab.category);
        this.buildTab (new d.HtmlBuilder(), tab).set(page);
    }
        
    var html = new d.HtmlBuilder ();
    html.push ('<img style="float:left; margin-right:5px;" align="middle" ',
        'src="img/contact-unknown.png"><div><p><input style="width:75%;" name="fn"></div>');
    d.HtmlBuilder.buildChild(html, notebook);
    
    html.set(this.formId);
    
    //var actions = [{ value: _('Save'), onclick: 'save'}];
    //Dragonfly.Preferences.Editor.buildInterface (form, actions, this.belement);
    
    if (this.contact != null)
    {
        this.loadContact();
    }
};

Dragonfly.AddressBook.UserProfile.prototype.loadContact = function ()
{
    var AB = Dragonfly.AddressBook;
    var Fields = AB.ContactPopup.prototype.FieldTypes;
    
    var form = $(this.formId);
    form.fn.value = this.contact.fn;
    
    //logDebug('myID: ' + myId);
    
    //form.isMyContact.checked = myId && (myId == this.contact.bongoId);
    
    for (var i = 0; i < Fields.props.length; i++) {
        var prop = Fields.props[i];
        var values = this.contact[prop];
        if (!values) {
            continue;
        }
        for (var j = 0; j < values.length; j++) {
            var value = values[j];
            var input = this.getFreeField (AB.ContactPopup.prototype.getName (prop, value.type), false);
            if (input) {
                input.value = $V(value);
            }
        }
    }
};

Dragonfly.AddressBook.UserProfile.prototype.getFreeField = function (name, asString)
{
    if (asString) {
        var n = this.formNames[name];
        n = (n == undefined) ? 0 : n + 1;
        this.formNames[name] = n;
        return name + String (n);
    }
    return $(this.formId)[name+'0'];
};

Dragonfly.AddressBook.UserProfile.prototype.buildTab = function (html, tab)
{
    var fields = tab.fields;
    var formFields = this.formFields;

    html.push ('<ul class="form-list">');    
    for (var i = 0; i < fields.length; i++) {
        var field = fields[i];
        
        var name = Dragonfly.AddressBook.ContactPopup.prototype.getName (field.prop, [tab.type, field.type]);
        name = this.getFreeField (name, true);
        html.push ('<li><label>', _(field.label), '</label>');
        if (field.longtext) {
            html.push ('<textarea name="', name, '"></textarea>');
        } else {
            html.push ('<input name="', name, '">');
        }
        html.push ('</li>');
    }
    html.push ('</ul>');
    return html;
};

Dragonfly.AddressBook.UserProfile.prototype.save = function ()
{
    var AB = Dragonfly.AddressBook;
    this.name = this.contact.fn;
    this.serializeContact();
    //this.elem.innerHTML = this.contact.fn;

    AB.saveContact (this.contact).addCallbacks (bind (
        function (result) {
            // update preferences with contact ID
            if (result.bongoId)
            {
                this.bongoId = result.bongoId;
            }
            else
            {
                this.bongoId = AB.Preferences.getMyContactId();
            }
            
            return result;
        }, this));
};

Dragonfly.AddressBook.UserProfile.prototype.serializeContact = function ()
{
    var AB = Dragonfly.AddressBook;
    var props = AB.ContactPopup.prototype.FieldTypes.props;
    var contact = this.contact;
    var form = $(this.formId);
    contact.fn = form.fn.value;
    
    // clear out existing properties
    forEach (props, function (prop) { delete contact[prop]; });
    
    for (var name in this.formNames) { // for each type of field
        var count = this.formNames[name];
        var components = name.split ('-');
        var prop = components[0];
        var types = components.slice(1);
        
        for (var i = 0; i <= count; i++) { // for each field
            var value = form[name+String(i)].value;
            if (value && value != '') {
                var proparray = contact[prop] || [ ];
                proparray.push ({value:value, type:types});
                contact[prop] = proparray;
            }
        }
    }
};
