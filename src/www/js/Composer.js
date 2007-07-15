Dragonfly.Mail.Composer = function (parent, msg)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    this.msg = msg;

    this.state = c.MaybeDirty;
    this.inReplyTo = this.msg.inreplyto;
    this.forward = this.msg.forward;
    this.conversation = this.msg.conversation;
    this.calendarInvitationFor = this.msg.calendarInvitationFor;
    this.useAuthUrls = this.msg.useAuthUrls;
    this.isReply = this.msg.isReply;

    this.pendingAttachments = [ ];
    
    logDebug('Composer instantiated.');

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Mail.Composer.MaybeDirty = 2;

Dragonfly.Mail.Composer.Saving = 3;
Dragonfly.Mail.Composer.SavingSaveAgain = 4;

Dragonfly.Mail.Composer.Sending = 6;
Dragonfly.Mail.Composer.Sent = 7;

Dragonfly.Mail.Composer.Discarding = 8;
Dragonfly.Mail.Composer.Discarded = 9;

Dragonfly.Mail.Composer.parseMailto = function (mailto)
{
    var d = Dragonfly;
    var m = d.Mail;
    var p = m.Preferences;
    
    var defaultbody = '';
    
    if (p.getSignatureAvailable())
    {
        // Append signature to end of message.
        defaultbody = '\n\n-- \n' + p.getSignature();
    }
    
    var msg = { 
        from: m.getFromAddress(),
        to: '',
        cc: '',
        bcc: p.getAutoBcc() || '',
        body: defaultbody
    };
    if (mailto.substr (0, 7) != 'mailto:') {
        return null;
    }
    var args = mailto.substr (7).split ('?', 2);
    msg.to = args[0].split (',').map (decodeURIComponent).join (', ');
    if (args[1]) {
        args = args[1].split ('&');
        args.each (
            function (arg) {
                arg = arg.split ('=', 2);
                var field = decodeURIComponent (arg[0]).toLowerCase ();
                switch (field) {
                case 'to':
                case 'cc':
                case 'bcc':
                    msg[field] += arg[1].split (',').map (decodeURIComponent).join (', ');
                    break;
                case 'subject':
                case 'inreplyto':
                case 'forward':
                case 'body':
                case 'conversation':
                    msg[field] = decodeURIComponent (arg[1]);
                    break;
                }
            });
    }

    return msg;
};

Dragonfly.Mail.Composer.prototype = { };

Dragonfly.Mail.Composer.prototype.buildHtml = function (html)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    this.id = d.nextId ('msg-compose');

    this.to = d.nextId ('to');
    this.cc = d.nextId ('cc');
    this.bcc = d.nextId ('bcc');
    this.from = d.nextId ('from');

    this.addAttachment = d.nextId ('add-attachment');

    this.subject = d.nextId ('subject');
    this.body = d.nextId ('body');

    this.toolbar = d.nextId ('toolbar');
    this.discard = d.nextId ('discard');
    this.save = d.nextId ('save');
    this.send = d.nextId ('send');

    function escapeRecips (recips) {
        return recips ? d.escapeHTML (recips.join ? recips.join (', ') : recips) : '';
    }

    html.push (
        '<table id="', this.id, '" class="msg-compose">',

        '<tr><th>', _('To:'), '</th><td>',
        '<input id="', this.to, '" class="to" type="text" value="', escapeRecips(this.msg.to), '">',
        '<div id="autocomplete-', this.to, '" class="autocompletions"></div></td></tr>',

        '<tr><th>', _('CC:'), '</th><td>',
        '<input id="', this.cc, '" class="cc" type="text" value="', escapeRecips (this.msg.cc), '">',
        '<div id="autocomplete-', this.cc, '" class="autocompletions"></td></tr>',
        
        '<tr><th>', _('BCC:'), '</th><td>',
        '<input id="', this.bcc, '" class="bcc" type="text" value="', escapeRecips (this.msg.bcc), '">',
        '<div id="autocomplete-', this.bcc, '" class="autocompletions"></td></tr>',
        
        '<tr><th>', _('Subject:'), '</th><td><input id="', this.subject, '" class="subject" value="', d.escapeHTML (this.msg.subject || ''), '"></td></tr>',
        '<tr><th>', _('From:'), '</th><td id="', this.from, '" class="from">', d.escapeHTML (this.msg.from), '</td></tr>',
        '<tr><th>', _('Attachments:'), '</th><td class="attachments"><ul>');

    if (this.msg.contents) {
        switch (this.msg.contents.contenttype) {
        case 'multipart/mixed':
            var body = this.msg.contents.children[0];
            if (body.contenttype == 'text/plain') {
                this.msg.body = body.content;
            }
            for (var i = 1; i < this.msg.contents.children.length; i++) {
                var attachment = this.msg.contents.children[i];
                html.push ('<li>');
                new m.AttachmentForm (html, this.msg.conversation, this.msg.bongoId, attachment.name, attachment.contenttype);
                html.push ('</li>');
            }
            break;
        case 'text/plain':
            this.msg.body = this.msg.contents.content;
            break;
        }
    }

    html.push ('<li><a id="', this.addAttachment, '">', _('Attach file...'), '</a></li></ul>',
               '<tr><td colspan="2"><textarea id="', this.body, '" class="body" cols="75" rows="15"  wrap="on">',
               d.escapeHTML (this.msg.body), '</textarea></td></tr>',
               '<tr class="action"><td id="', this.toolbar, '" colspan="2">',
               '<input id="', this.discard, '" class="discard" type="button" value="', _('Discard'), '">',
               '<input id="', this.save, '" class="save" type="button" value="', _('Save Draft'), '">',
               '<input id="', this.send, '" class="send" type="button" value="', _('Send'), '"></td></tr></table>');

    html.addCallback (bind ('connectHtml', this));
    return html;
};

Dragonfly.Mail.Composer.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    this.composer = $(this.id);

    d.HtmlZone.add (this.composer, this);

    new m.RecipientsField (this.to, 'autocomplete-' + this.to);
    new m.RecipientsField (this.cc, 'autocomplete-' + this.cc);
    new m.RecipientsField (this.bcc, 'autocomplete-' + this.bcc);

    this.to = $(this.to);
    this.cc = $(this.cc);
    this.bcc = $(this.bcc);
    this.from = $(this.from);
    this.subject = $(this.subject);
    this.body = $(this.body);
    this.addAttachment = $(this.addAttachment);
    this.discard = $(this.discard);
    this.send = $(this.send);
    this.save = $(this.save);

    this.toolbar = $(this.toolbar);

    Event.observe (this.discard, 'click',
                   this.discardDraft.bindAsEventListener (this));
    
    Event.observe (this.save, 'click',
                   this.saveDraft.bindAsEventListener (this));
    
    Event.observe (this.send, 'click',
                   this.sendMessage.bindAsEventListener (this));
    
    Event.observe (this.addAttachment, 'click',
                   this.addNewAttachment.bindAsEventListener (this));
                   
    Event.observe (this.subject, 'keyup',
                   this.updateTitle.bindAsEventListener (this));

    if (this.msg.bongoId) {
        this.bongoId = this.msg.bongoId;
        this.lastSavedMsg = this.getCurrentMessage();
        this.scheduleSave();
    } else {
        // Save draft in 60 seconds
        this.scheduleSave ();
    }   
    
    delete this.msg;

    return elem;
};

Dragonfly.Mail.Composer.prototype.cancelScheduledSave = function ()
{
    if (this.scheduledSave) {
        this.scheduledSave.cancel();
    }
    delete this.scheduledSave;
};

Dragonfly.Mail.Composer.prototype.scheduleSave = function ()
{
    this.cancelScheduledSave();
    this.scheduledSave = callLater (60, bind ('saveDraft', this));
};

Dragonfly.Mail.Composer.prototype.getCurrentMessage = function ()
{
    return {
        from: Dragonfly.Mail.getFromAddress(),
        to: this.to.value,
        cc: this.cc.value,
        bcc: this.bcc.value,
        subject: this.subject.value,
        body: this.body.value
    };
};

Dragonfly.Mail.Composer.prototype.hasChanges = function (msg)
{
    function messagesDiffer (a, b) {
        for (field in a) {
            if (a[field] != b[field]) {
                return true;
            }
        }
        return false;
    }
    return !this.lastSavedMsg || messagesDiffer (this.lastSavedMsg, msg || this.getCurrentMessage());
};

Dragonfly.Mail.Composer.prototype.updateTitle = function (evt)
{
    if (this.subject.value == "")
    {
        document.title = _('(untitled)') + ' - ' + _('Composing:') + ' ' + Dragonfly.title;
    }
    else
    {
        document.title = this.subject.value + ' - ' + _('Composing:') + ' ' + Dragonfly.title;
    }
}

Dragonfly.Mail.Composer.prototype.saveDraft = function (evt, msg)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    if (this.state != c.MaybeDirty) {
        logDebug ('Not saving a composer in state:', this.state);
        return false;
    }

    //msg = msg || this.getCurrentMessage ();
    msg = this.getCurrentMessage();

    if (!this.hasChanges (msg)) {
        logDebug ('No changes to save');
        return true;
    }

    this.state = c.Saving;
    this.cancelScheduledSave ();

    var loc = {
        tab: 'mail',
        set: 'drafts',
        handler: 'conversations',
        conversation: this.conversation,
        message: this.bongoId
    };

    loc = new d.Location (loc);
    this.def = d.requestJSONRPC ((this.draftSavedAlready ? 'update' : 'create') + 'Draft', loc,
                                 msg,
                                 this.inReplyTo || null,
                                 this.forward || null,
                                 this.calendarInvitationFor || null,
                                 this.useAuthUrls || false);

    var displaysubject = '';
    if (msg.subject)
    {
        displaysubject = d.escapeHTML (msg.subject);
    }
    else
    {
        msg.subject = _('Untitled');
        displaysubject = d.escapeHTML (msg.subject);
    }
    
    d.notify (d.format (_('Saving draft "{0}"...'), displaysubject), true);

    return this.def.addCallbacks (
        bind (function (jsob) {
                  d.notify (_('Saved draft') + ' "' + displaysubject + '".');
                  this.draftSavedAlready = true;
                  this.lastSavedMsg = msg;
                  
                  if (!this.bongoId && jsob.bongoId) {
                      logDebug ('setting bongoId to |' + jsob.bongoId + '|');
                      this.bongoId = jsob.bongoId;
                  }
                  
                  if (this.conversation != jsob.conversation) {
                      this.conversation = jsob.conversation;
                      if (!this.isReply) {
                          loc.conversation = this.conversation;
                          delete loc.message;
                          d.setLoc (loc);
                          d.updateLocation ();
                      }
                  }
                  
                  delete this.def;
                  if (this.state == c.SavingSaveAgain) {
                      this.state = c.MaybeDirty;
                      return this.saveDraft();
                  } else {
                      this.state = c.MaybeDirty;
                      this.scheduleSave();
                  }
                  return jsob;
              }, this),
        bind (function (err) {
                  if (!(err instanceof CancelledError)) {
                      logError (_('Error saving draft:'), d.reprError (err));
                  }
                  this.state = c.MaybeDirty;
                  this.scheduleSave();
                  return err;
              }, this));
};

Dragonfly.Mail.Composer.prototype.discardDraft = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    var loc = new d.Location ({ tab: 'mail', set: 'drafts', handler: 'conversations', page: 0,
                                      conversation: this.conversation, message: this.bongoId, valid: true });

    var discardLoc = d.prevLoc;
    // don't try to go back to the discarded message
    if (!discardLoc || (this.bongoId && discardLoc.message == this.bongoId))
    {
        discardLoc = new d.Location (loc);
        discardLoc.set = d.curLoc.set;
        delete discardLoc.conversation;
        delete discardLoc.message;
    }

    if (this.def) {
        this.def.cancel();
    }
    this.state = c.Discarding;

    var composer = this;
    d.request ('DELETE', loc).addCallbacks (
        bind (function (jsob) {
                  this.state = c.Discarded;
                  d.go (discardLoc);
                  return jsob;
              }, this),
        bind (function (err) {
                  // Ignore the fact we errored - probably because we NEVER SAVED ANYTHING.
                  /*if (!(err instanceof CancelledError)) {
                      d.notifyError ('Could not discard draft');
                      logDebug ('error deleting draft:', d.reprError (err));
                  }
                  this.state = c.MaybeDirty;
                  this.scheduleSave();
                  return err;*/
                  
                  // Now, go do what success normally does.
                  this.state = c.Discarded;
                  d.go (discardLoc);
                  return jsob;
              }, this));
};

Dragonfly.Mail.Composer.prototype.sendMessage = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;
    
    if (!this.draftSavedAlready)
    {
        if (!this.draftSaveCalled)
        {
            this.saveDraft(this);
            this.draftSaveCalled = true;
            callLater(1, bind('sendMessage', this));
        }
        else
        {
            // Still waiting for draft to save...
            logDebug('Save not finished yet - checking again in 1sec.');
            callLater(1, bind('sendMessage', this));
        }
        
        return;
    }
    
    var msg = this.getCurrentMessage ();
    if (!this.hasChanges (msg)) {
        msg = null;
    }
        
    var loc = new d.Location ({ tab: 'mail', set: 'drafts', handler: 'conversations', page: 1, 
                                      conversation: this.conversation, message: this.bongoId, valid: true });

    this.setToolbarDisabled (true);
    
    this.cancelScheduledSave();
    this.state = c.Sending;
    
    var def = d.requestJSONRPC ('send', loc, msg);
    return def.addCallbacks (
        bind (function (jsob) {
                  this.state = c.Sent;
                  
                  this.setToolbarDisabled (false);
                  
                  d.notify (_('Message sent.'));
                  
                  var sentLoc = new d.Location (loc);
                  sentLoc.set = 'sent';
                  sentLoc.conversation = jsob.conversation;
                  delete sentLoc.message;
                  
                  d.go(sentLoc);
                  return jsob;
              }, this),
        bind (function (err) {
                  if (!(err instanceof CancelledError)) {
                      d.notifyError (_('Error sending message: '), d.reprError (err));
                  }
                  this.state = c.MaybeDirty;
                  this.scheduleSave();
                      
                  this.setToolbarDisabled (false);
                  return err;
              }, this));
};

Dragonfly.Mail.Composer.prototype.updateSendEnabled = function ()
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    logDebug ('updateSendEnabled:', this.pendingAttachments.length);
    if (this.discard.disabled || this.pendingAttachments.length) {
        this.send.disabled = true;
        return;
    }
    switch (this.state) {
    case c.MaybeDirty:
    case c.Saving:
    case c.SavingSaveAgain:
        this.send.disabled = false;
        break;
    default:
        logError ('this should probably not be reached, but we are in state:', this.state);
        break;
    }
};

Dragonfly.Mail.Composer.prototype.setToolbarDisabled = function (disabled)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    d.setToolbarDisabled ('toolbar', disabled);
    d.setToolbarDisabled (this.toolbar, disabled);
    if (!disabled) {
        this.updateSendEnabled();
    }
};

Dragonfly.Mail.Composer.prototype.addNewAttachment = function (evt)
{
    var d = Dragonfly;
    var m = d.Mail;

    var oldLi = this.addAttachment.parentNode;
    var newLi = LI();

    oldLi.parentNode.insertBefore (newLi, oldLi);
    var attachment = new m.AttachmentForm (newLi, this.conversation, this.bongoId);

    var def = attachment.getUploadComplete();
    this.pendingAttachments.push (def);
    logDebug ('addNewAttachment:', this.pendingAttachments.length);
    def.addBoth (
        bind (function (res) {
                  for (var i = this.pendingAttachments.length - 1; i >= 0; --i) {
                      if (this.pendingAttachments[i] == def) {
                          this.pendingAttachments.splice (i, 1);
                          break;
                      }
                  }
                  this.updateSendEnabled();
                  return res;
              }, this));

    this.updateSendEnabled();

    Event.stop (evt);
};

Dragonfly.Mail.Composer.prototype.canDisposeZone = function ()
{
    var d = Dragonfly;
    var c = d.Mail.Composer;

    logDebug ('canDisposeZone: we are in state', this.state);

    switch (this.state) {
    case c.MaybeDirty:
        return this.saveDraft();
    case c.Saving:
        this.state = c.SavingSaveAgain;
        // fall through
    case c.Sending:
    case c.Discarding:
        return this.def;
    case c.Sent:
    case c.Discarded:
        return true;
    }
};

Dragonfly.Mail.Composer.prototype.disposeZone = function ()
{
    logDebug ('disposing with a composer...');
    this.cancelScheduledSave();
    if (this.def) {
        this.def.cancel();
    }
};

Dragonfly.Mail.Composer.composeNew = function (msg)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    var def = d.contentIFrameZone.canDisposeZone ();
    if (def instanceof Deferred) {
        return def.addCallback (partial (c.composeNew, msg));
    } else if (!def) {
        logWarning ('content-iframe is not clean, not composing new message');
        return;
    }
    d.contentIFrameZone.disposeZone();

    var fakeLoc = new d.Location ({ tab: 'mail', set: 'drafts', composing: true });
    d.buildSourcebar (fakeLoc);
    m.build (fakeLoc);
    m.ConversationView.build (fakeLoc);
    
    d.setLoc (fakeLoc);
    d.updateLocation ();

    Element.setHTML ('conv-breadcrumbs', fakeLoc.getBreadCrumbs());
    
    // Update title
    document.title = '(untitled) - Composing: ' + Dragonfly.title;

    var html = new d.HtmlBuilder ();

    var composer = new m.Composer (html, msg);

    html.set ('conv-msg-list');
    
    d.resizeScrolledView();
    
    return composer;
};

Dragonfly.Mail.Composer.mailtoClicked = function (elem, href)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = m.Composer;

    var msg = c.parseMailto (href);
    if (!msg) {
        return;
    }

    if (Element.hasClassName (elem.parentNode, 'msg-actions')) {
        var div = DIV();
        msg.isReply = true;

        Element.hide (elem.parentNode);
        elem.parentNode.parentNode.parentNode.appendChild (div);

        new m.Composer (div, msg);
        div.scrollIntoView (false);
    } else {
        c.composeNew (msg);
    }
};

Dragonfly.Mail.RecipientsField = function (elem, update)
{
    var d = Dragonfly;

    if (arguments.length < 2) {
        return;
    }

    this.baseInitialize (elem, update, { tokens: ',' });
};

Dragonfly.Mail.RecipientsField.prototype = new Autocompleter.Base;

Dragonfly.Mail.RecipientsField.prototype.clear = function ()
{
    if (this.loadDef) {
        this.loadDef.cancel ();
        delete this.loadDef;
    }

    this.hide ();
    this.element.setValue ('');
};

Dragonfly.Mail.RecipientsField.prototype.getUpdatedChoices = function ()
{
    var d = Dragonfly;
    var a = d.AddressBook;

    var i;

    var ret = [ ];
    var needle = this.getToken().toLowerCase ();

    logDebug ('searching for:', needle);

    var Found = new Error;

    function checkMatch (val) {
        if ((val.value || val).toLowerCase().indexOf (needle) != -1) {
            throw Found;
        }
    }

    for (var bongoId in a.contactMap) {
        var contact = a.contactMap[bongoId];
        if (!contact || !contact.fn || !contact.email || !contact.email.length) {
            continue;
        }

        try {
            checkMatch (contact.fn);
            forEach (contact.email || [], checkMatch);
            forEach (contact.im || [], checkMatch);
        } catch (e) {
            if (e === Found) {
                logDebug (contact.fn, 'matches!');
                for (var i = 0; i < contact.email.length; i++) {
                    ret.push (d.escapeHTML (contact.fn) + ' &lt;' + d.escapeHTML (contact.email[i]) + '&gt;');
                }
                continue;
            }
            throw e;
        }
    }
    logDebug ('got ' + ret.length + ' matches');
    if (ret.length) {
        this.updateChoices ('<ul><li>' + ret.join ('</li><li>') + '</li></ul>');
    } else {
        this.updateChoices ('<span></span>');
    }
};

Dragonfly.Mail.AttachmentForm = function (parent, convId, msgId, attachmentId, mimeType)
{
    var d = Dragonfly;

    if (!arguments.length) {
        return;
    }

    this.submitDelay = 5;

    this.id = d.nextId ('attachment');

    this.convId = convId;
    this.msgId = msgId;
    this.attachmentId = attachmentId;
    this.mimeType = mimeType;

    this.updateUrl();

    if (!this.attachmentId) {
        this.uploadComplete = (new Deferred()).addBoth (
            bind (function (res) {
                      delete this.uploadComplete;
                      return res;
                  }, this));
        
        this.formId = d.nextId ('attachment-form');
        this.iframeId = d.nextId ('attachment-iframe');
        this.fileId = d.nextId ('attachment-file');
    }

    this.labelId = d.nextId ('attachment-label');
    this.removeId = d.nextId ('attachment-remove');

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Mail.AttachmentForm.prototype = { };

Dragonfly.Mail.AttachmentForm.prototype.buildHtml = function (html)
{
    var d = Dragonfly;

    html.push ('<div id="', this.id, '" class="attachment">');

    if (!this.attachmentId) {
        html.push ('<form style="white-space: nowrap;" id="', this.formId, '" ',
                   'method="post" enctype="multipart/form-data" ',
                   'target="', this.iframeId, '" action="', d.escapeHTML (this.url), '">',
                   '<input name="id" type="hidden" value="', d.getJsonRpcId(), '">',
                   '<input name="method" type="hidden" value="createAttachment">',
                   '<input name="authorization" type="hidden" value="Basic ', d.authToken, '">',
                   '<input name="attachment-file" id="', this.fileId, '" type="file">');
    }

    html.push ('<span id="', this.labelId, '">', d.escapeHTML (this.attachmentId), ' (', d.escapeHTML (this.mimeType), ')</span>',
               '<a id="', this.removeId, '">', _('Remove attachment'), '</a>');

    if (!this.attachmentId) {
        html.push ('</form>',
                   '<iframe name="', this.iframeId, '" id="', this.iframeId, '" style="display: none"></iframe>',
                   '</div>');
    }

    html.addCallback (bind ('connectHtml', this));
    return html;
};

Dragonfly.Mail.AttachmentForm.prototype.updateUrl = function ()
{
    var d = Dragonfly;

    var loc = { tab: 'mail', set: 'drafts', conversation: this.convId, message: this.msgId, attachment: this.attachmentId };
    this.url = d.Location.getServerUrl (loc, false, '');
    if (!this.attachmentId && $(this.formId)) {
        $(this.formId).action = this.url;
    }
};

Dragonfly.Mail.AttachmentForm.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;

    Event.observe (this.removeId, 'click',
                   this.remove.bindAsEventListener (this));

    if (!this.attachmentId) {
        d.HtmlZone.add (this.formId, this);

        Event.observe (this.fileId, 'change',
                       this.queueUpload.bindAsEventListener (this));

        Event.observe (this.iframeId, 'load',
                       this.frameLoad.bindAsEventListener (this));
    }

    return elem;
};

Dragonfly.Mail.AttachmentForm.prototype.getUploadComplete = function ()
{
    return this.uploadComplete;
};

Dragonfly.Mail.AttachmentForm.prototype.canDisposeZone = function ()
{
    return this.attachmentId || $(this.fileId).value ? this.uploadComplete : true;
};

Dragonfly.Mail.AttachmentForm.prototype.disposeZone = function ()
{
    if (this.uploadComplete) {
        this.uploadComplete.cancel();
    }
};

Dragonfly.Mail.AttachmentForm.prototype.startUpload = function (res)
{
    var d = Dragonfly;
    $(this.formId).submit();
    Element.hide (this.fileId);
    Element.setHTML (this.labelId, d.format (_('Uploading {0}...'), d.escapeHTML ($(this.fileId).value)));
    return res;
};

Dragonfly.Mail.AttachmentForm.prototype.clearDef = function (res)
{
    delete this.def;
    return res;
};

Dragonfly.Mail.AttachmentForm.prototype.queueUpload = function (evt)
{
    var d = Dragonfly;

    if (this.def) {
        this.def.cancel();
    }
    if ($(this.fileId).value) {
        this.def = callLater (this.submitDelay, bind ('startUpload', this));
        this.def.addBoth (bind ('clearDef', this));
    }
};

Dragonfly.Mail.AttachmentForm.prototype.frameLoad = function (evt)
{
    var d = Dragonfly;

    // ignore first load
    if (!$(this.fileId).value) {
        return;
    }

    function showError (errStr) {
        d.notifyError (_('Error saving attachment') + ' ' + d.escapeHTML ($(this.fileId).value) + ': ' + d.escapeHTML (errStr));
        Element.setHTML (this.labelId, '');
        Element.show (this.fileId);
    }

    var doc = d.getIFrameDocument (this.iframeId);
    var jsob;
    try {
        jsob = d.eval (Element.getText (doc.body));
    } catch (e) {
        showError.call (this, 'server response was not understood');
        return;
    }
    try {
        jsob = d.getJsonRpcResult (jsob);
    } catch (e) {
        showError.call (this, reprError (e));
        return;
    }
    this.attachmentId = jsob.attachId;
    this.mimeType = jsob.mimeType;
    Element.setHTML (this.labelId, [ d.escapeHTML (this.attachmentId), ' (', d.escapeHTML (jsob.mimeType), ')' ]);
    this.uploadComplete.callback ();
};

Dragonfly.Mail.AttachmentForm.prototype.remove = function (evt)
{
    var d = Dragonfly;

    if (this.def) {
        this.def.cancel();
    }

    if (this.uploadComplete) {
        this.uploadComplete.cancel();
    }

    if (this.attachmentId) {
        this.updateUrl ();
        this.def = d.request ('DELETE', this.url);
        this.def.addCallback (bind (function (res) {
                                        d.HtmlZone.remove (this.id, this);
                                        removeElement (this.id);
                                        return res;
                                    }, this));
        this.def.addBoth (bind ('clearDef', this));
    } else {
        d.HtmlZone.remove (this.id, this);
        removeElement (this.id);
    }
};

/* *** */
Dragonfly.Mail.AttachmentTester = function ()
{
    var d = Dragonfly;

    this.userId = d.nextId ('user');
    this.passId = d.nextId ('pass');
    this.convElemId = d.nextId ('convid');
    this.msgElemId = d.nextId ('msgid');
    this.actionId = d.nextId ('action');

    d.Mail.AttachmentForm.apply (this, arguments);
};

Dragonfly.Mail.AttachmentTester.prototype = new Dragonfly.Mail.AttachmentForm;

Dragonfly.Mail.AttachmentTester.prototype.buildHtml = function (html)
{
    var d = Dragonfly;
    html.push ('username: <input id="', this.userId, '"> ',
               'password: <input id="', this.passId, '" type="password"> ',
               'conversation: <input id="', this.convElemId, '"> ',
               'message: <input id="', this.msgElemId, '">');
    d.Mail.AttachmentForm.prototype.buildHtml.apply (this, arguments);
    html.push ('action: <span id="', this.actionId, '></span>');
};

Dragonfly.Mail.AttachmentTester.prototype.connectHtml = function (elem)
{
    var d = Dragonfly;

    Event.observe (this.userId, 'change',
                   this.updateCreds.bindAsEventListener (this));

    Event.observe (this.passId, 'change',
                   this.updateCreds.bindAsEventListener (this));

    Event.observe (this.convElemId, 'change',
                   this.updateAction.bindAsEventListener (this));

    Event.observe (this.msgElemId, 'change',
                   this.updateAction.bindAsEventListener (this));

    d.Mail.AttachmentForm.prototype.connectHtml.apply (this, arguments);
};

Dragonfly.Mail.AttachmentTester.prototype.updateAction = function (evt)
{
    var d = Dragonfly;

    this.msgId = $(this.msgElemId).value;
    this.convId = $(this.convElemId).value;

    this.updateUrl();
    Element.setHTML (this.actionId, d.escapeHTML ($(this.formId).action));
};

Dragonfly.Mail.AttachmentTester.prototype.updateCreds = function (evt)
{
    var d = Dragonfly;

    d.userName = $(this.userId).value;
    d.authToken = btoa($(this.userId).value + ':' + $(this.passId).value);
    this.updateAction();
};
