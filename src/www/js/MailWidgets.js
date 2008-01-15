Dragonfly.Pager = function (parent, urlFmt, curPage, totPages)
{
    var d = Dragonfly;

    this.id = d.nextId ('pager');
    this.urlFmt = urlFmt;
    this.curPage = curPage || 1;
    this.totPages = totPages;

    if (parent) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Pager.prototype = { };
Dragonfly.Pager.prototype.buildPager = function (html)
{
    var d = Dragonfly;

    var pageStart, pageEnd;

    if (this.curPage < 6) {
        pageStart = 1;
        pageEnd = Math.min (10, this.totPages);
    } else if (this.totPages - this.curPage < 4) {
        pageStart = Math.max (1, this.totPages - 9);
        pageEnd = this.totPages;
    } else {
        pageStart = this.curPage - 5;
        pageEnd = this.curPage + 4;
    }

    html.push (' <a href="#', escapeHTML (d.format (this.urlFmt, 1)),
               '" style="visibility: ', pageStart > 1 ? 'visible' : 'hidden',
               '">&lt;&lt; First</a> ',

               ' <a href="#', escapeHTML (d.format (this.urlFmt, this.curPage - 1)), 
               '" style="visibility: ', this.curPage > 1 ? 'visible' : 'hidden',
               '">&lt; Prev</a> ');

    for (var i = pageStart; i <= pageEnd; i++) {
        if (i == this.curPage) {
            html.push (' &nbsp;', i, '&nbsp; ');
        } else {
            html.push (' <a href="#', escapeHTML (d.format (this.urlFmt, i)), '">&nbsp;', i, '&nbsp;</a> ');
        }
    }

    html.push (' <a href="#', escapeHTML (d.format (this.urlFmt, this.curPage + 1)),
               '" style="visibility: ', this.curPage < this.totPages ? 'visible' : 'hidden',
               '">Next &gt;</a> ',

               ' <a href="#', escapeHTML (d.format (this.urlFmt, this.totPages)), 
               '" style="visibility: ', pageEnd < this.totPages ? 'visbile' : 'hidden',
               '">Last &gt;&gt;</a> ');
    return html;
};

Dragonfly.Pager.prototype.buildHtml = function (html)
{
    html.push ('<div id="', this.id, '" class="pager">');
    this.buildPager (html);
    html.push ('</div>');
    return html;
};



Dragonfly.Mail.ConversationList = function (parent, convs, urlFmt)
{
    var d = Dragonfly;

    this.id = d.nextId ('conversation-list');
    this.convs = convs;
    this.urlFmt = urlFmt;

    if (parent && convs) {
        d.HtmlBuilder.buildChild (parent, this);
    }
};

Dragonfly.Mail.ConversationList.prototype = { };
Dragonfly.Mail.ConversationList.prototype.buildRow = function (html, conv, className)
{
    var d = Dragonfly;
    var m = d.Mail;
    var c = d.Calendar;

    if (!conv.props.flags.read) {
        className += ' unread';
    }

    var starClassName = 'star';
    if (conv.props.flags.starred) {
        starClassName += ' starred';
    }

    var href = 'href="#' + escapeHTML (d.format (this.urlFmt, conv.bongoId)) + '"';

    html.push ('<tr bongoId="', escapeHTML (conv.bongoId), '" class="', className, '">',
               '<td class="checkbox"><input type="checkbox"></td>',
               '<td class="star"><div class="', starClassName, '">*</div></td>',

               '<td class="from" ', href, '><a ', href, '>', d.escapeHTML (m.joinParticipants (conv.props.from)),
               (conv.props.count > 1 ? ' (' + conv.props.count + ')' : ''), '</a></td>',

               '<td class="subject" ', href, '><a ', href, '>', escapeHTML (conv.props.subject.substr(0, 55)), '</a></td>',
               '<td class="date time" ', href, '><a ', href, '>');

    var date = Day.localToUTC (isoTimestamp (conv.props.date));
    if (date) {
        var today = Day.getUTCToday ();
        if (date.getUTCFullYear () == today.getUTCFullYear () &&
            date.getUTCMonth ()    == today.getUTCMonth () &&
            date.getUTCDate ()     == today.getUTCDate ()) {
            
            html.push (escapeHTML (d.formatDate (date, 't')));
        } else {
            html.push (escapeHTML (c.niceDate (date)));
        }
    } else {
        html.push (escapeHTML (conv.props.date));
    }
    
    html.push ('</a></td>',
               '</tr>');
};

Dragonfly.Mail.ConversationList.prototype.buildStart = function (html)
{
    html.push ('<table id="', this.id, '" class="conversation-list" cellpadding="0" cellspacing="0">');
};

Dragonfly.Mail.ConversationList.prototype.buildEnd = function (html)
{
    html.push ('</table>');
};

Dragonfly.Mail.ConversationList.prototype.addConversations = function (html, convs)
{
    var i;

    for (i = 0; i < convs.length; i++) {
        var conv = convs[i];
        if (!conv.props) {
            console.error ('conversation lacks properties');
            return;
        }
        this.buildRow (html, conv, i % 2 ? 'odd' : 'even');
    }
};

Dragonfly.Mail.ConversationList.prototype.addGroupHeader = function (html, header)
{
    var d = Dragonfly;

    html.push ('<tr class="group-header"><td colspan="5">', header, '</td></tr>');
};

Dragonfly.Mail.ConversationList.prototype.addGroupFooter = function (html, footer)
{
    var d = Dragonfly;
    html.push ('<tr class="group-footer"><td colspan="5">', footer, '</td></tr>');
};

Dragonfly.Mail.ConversationList.prototype.buildHtml = function (html)
{
    this.buildStart (html);
    if (this.convs) {
        this.addConversations (html, this.convs);
    }
    this.buildEnd (html);
};
