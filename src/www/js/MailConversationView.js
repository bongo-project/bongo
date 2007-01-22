Dragonfly.Mail.ConversationView = { };

Dragonfly.Mail.ConversationView.getSelectedIds = function ()
{
    var d = Dragonfly;
    var m = d.Mail;

    return [ d.curLoc.message || d.curLoc.conversation ];
};

Dragonfly.Mail.ConversationView.build = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;

    if (!$('conv-subject')) {
        Element.setHTML ('content-iframe', [
                             '<table class="conversation-list" cellpadding="0" cellspacing="0">',
                             '<tr class="group-header"><td id="conv-breadcrumbs"></td></tr></table>',
                             '<h2 id="conv-subject"></h2>',
                             '<div class="scrollv" id="conv-msg-list"></div>'
                             ]);
    }
};

Dragonfly.Mail.ConversationView.getScrolledElement = function (loc)
{
    return 'conv-msg-list';
};

Dragonfly.Mail.ConversationView.getData = function (loc)
{
    var d = Dragonfly;
    var m = d.Mail;

    return d.requestJSONRPC ('getConversation', loc, 1, 1, 1, 1);
};

Dragonfly.Mail.ConversationView.selectAlternative = function (part) 
{
    var favorite = null;

    //logDebug('checking alternatives');
    for (var i = 0; i < part.children.length; i++) {
        var child = part.children[i];
        if (child.preview && (child.previewtype == 'text/plain' 
                              || child.previewtype == 'text/html')) {
            favorite = child;
        }
    }
    
    return favorite;
};

Dragonfly.Mail.ConversationView.formatPart = function (loc, msg, part)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.ConversationView;

    var html = [ ];
     
    if (part.preview) {
        html.push ('<div class="msg-body">');
        if (part.previewtype == 'text/plain') {
            html.push (d.htmlizeText (part.preview));
        } else if (part.previewtype == 'text/html') {
            html.push (d.linkifyHtml (part.preview));
        } else {
            html.push ('<p>Unknown preview type "', d.escapeHTML (part.previewtype), '" found.</p>');
        }
    }
    
    if (part.children) {
        if (part.contenttype == 'multipart/alternative') {
            var child = v.selectAlternative(part);
            if (child) {
                html.push (v.formatPart(loc, msg, child));
            }
        } else {
            var inAttachments = 0;
            for (var i = 0; i < part.children.length; i++) {
                var child = part.children[i];
		
                /* Treat anything without children or a preview as an attachment */
                if (child.children || child.preview) {
                    if (inAttachments) {
                        html.push ('</ul></div>');
                        inAttachments = 0;
                    }
                    html.push (v.formatPart(loc, msg, child));
                } else {
                    if (!inAttachments) {
                        html.push ('<div class="msg-attachments"><b>Attachments:</b><ul>');
                        inAttachments = 1;
                    }
                    var partLoc = new d.Location (loc);
                    partLoc.message = msg.bongoId;
                    partLoc.attachment = child.name;
                    
                    html.push ('<li><a href="', d.escapeHTML (partLoc.getServerUrl ()), '">',
                               d.escapeHTML (child.name), '</a> (',
                               d.escapeHTML (child.contenttype), ')</li>');
                }
            }
            if (inAttachments) {
                html.push ('</ul></div>');
                inAttachments = 0;
            }
        }
    }
    
    if (part.preview) {
        html.push ('</div>');
    }
    
    //logDebug('returning ' + html);
    return html.join ('');
};

Dragonfly.Mail.ConversationView.load = function (loc, jsob)
{
    var d = Dragonfly;
    var m = d.Mail;
    var v = m.ConversationView;
    var c = m.Composer;
    var cal = d.Calendar;

    var nmessages = 1;

    function expandMessage (msg) {
        return true;
        if (nmessages == 1) {
            return true;
        }
        switch (loc.set) {
        case 'all':
        case 'inbox':
        case 'drafts':
            return !msg.flags.read;
        case 'starred':
            return !msg.flags.read || msg.flags.starred;
        case 'sent':
            return !msg.flags.read || msg.flags.sent;
        case 'trash':
            return true;
        case 'junk':
            return !msg.flags.read || false;
        }
    }

    function showMessage (msg) {
        if (nmessages == 1) {
            return true;
        }
        if (loc.set == 'trash') {
            return msg.flags.deleted;
        } else {
            return !msg.flags.deleted;
        }
    }

    if (jsob.convIdx < 0) {
        throw new Error ('The server did not return a conversation.');
    }

    var itemLoc = new d.Location (loc);

    var conv;
    if (jsob.convIdx > 0) {
        conv = jsob.conversations[0];
        itemLoc.conversation = conv.bongoId;
        $('conv-prev-href').href = '#' + itemLoc.getClientUrl ();
        Element.setText ('conv-prev-href', '< ' + m.joinParticipants (conv.props.from) + ': ' + conv.props.subject);
    } else {
        Element.setHTML ('conv-prev-href', '');
    }

    if (jsob.propsEnd > jsob.convIdx) {
        conv = jsob.conversations[jsob.convIdx + 1];
        itemLoc.conversation = conv.bongoId;
        $('conv-next-href').href = '#' + itemLoc.getClientUrl ();
        Element.setText ('conv-next-href', m.joinParticipants (conv.props.from) + ': ' + conv.props.subject + ' >');
    } else {
        Element.setHTML ('conv-next-href', '');
    }
    
    conv = jsob.conversations[jsob.convIdx];
    document.title = conv.props.subject + ': ' + Dragonfly.title;

    Element.setHTML ('conv-breadcrumbs', loc.getBreadCrumbs());
    Element.setText ('conv-subject', conv.props.subject);
    
    nmessages = conv.messages.length;

    var html = new d.HtmlBuilder ();
    for (var i = 0; i < nmessages; i++) {
        var msg = conv.messages[i];

        msg.conversation = loc.conversation;
        if (!showMessage(msg)) {
            continue;
        }

        if (msg.flags.draft) {
            new m.Composer (html, msg);
            continue;
        }

        html.push ('<div id="conv-msg-', msg.bongoId, '" bongoId="', msg.bongoId, '" class="msg');
        if (msg.flags.read) {
            html.push (' read');
        }
        if (!expandMessage (msg)) {
            html.push (' closed');
        }

        var replyToSender = [
            '<a href="mailto:', encodeURIComponent (msg.from),
            '?subject=', encodeURIComponent ('Re: ' + msg.subject), 
            '&amp;inreplyto=', encodeURIComponent (msg.bongoId),
            '&amp;conversation=', encodeURIComponent (loc.conversation),
            '" class="reply">Reply to Sender</a>' ].join ('');

        var to = msg.to.map(encodeURIComponent).join(',');
        var cc = msg.cc.map(encodeURIComponent).join(',');
        cc = (to && cc) ? to + ', ' + cc : (to || cc);
        var replyToAll = [
            '<a href="mailto:', encodeURIComponent (msg.from),
            '?subject=', encodeURIComponent ('Re: ' + msg.subject),
            '&amp;inreplyto=', encodeURIComponent (msg.bongoId),
            '&amp;conversation=', encodeURIComponent (loc.conversation),
            '&amp;cc=', cc,
            '" class="replyall">Reply to All</a>' ].join ('');
        
        var forward = [
            '<a href="mailto:',
            '?subject=', encodeURIComponent ('Fwd: ' + msg.subject),
            '&amp;forward=', encodeURIComponent (msg.bongoId),
            '&amp;conversation=', encodeURIComponent (loc.conversation),
            '" class="forward">Forward</a>' ].join ('');

        html.push ('">',
                   '<span class="pointer-border">', 
                   '<span class="pointer">', 
                   '</span></span>', 
                   '<span class="face"></span>', 
                   '<div class="msg-content">', 
                   '<div class="msg-header">',
                   '<div class="msg-actions top">', forward, replyToAll, replyToSender, '</div>',
                   '<div class="people-group">',
                   '<img class="disclosure" src="img/blank.gif" />',
                   '<span class="sender" title="', msg.from, '">', 
                   m.markupParticipants (msg.from), '</span>',
                   'to <span class="recipients">', m.markupParticipants (concat (msg.to, msg.cc)), '</span>',
                   '</div>',
                   '<span class="date">',
                   d.escapeHTML (msg.date),
                   //d.escapeHTML (cal.niceTime (msg.date) + ' ' + cal.niceDate (msg.date)),
                   '</span>',
                   '<div class="msg-snippet">', escapeHTML (msg.snippet), '</div></div>',
                   v.formatPart(loc, msg, msg.contents), '</div>',
                   //'<div class="msg-actions bottom">', /* forward, */ replyToAll, replyToSender, '</div>',
                   '</div>');
        /*
        Event.observe (div, 'click',
                       (function (evt) {
                           if (hasElementClass (div, 'closed')) {
                               removeElementClass (div, 'closed');
                           } else if (hasElementClass (Event.element (evt), 'msg-header') ||
                                      hasElementClass (Event.element (evt), 'disclosure')) {
                               addElementClass (div, 'closed');
                           } else {
                               return;
                           }
                           Event.stop (evt);
                       }).bindAsEventListener (null));
        */
    }

    html.set ('conv-msg-list');
};
