#include <config.h>

#include "filter.h"

#define TBUF_CHARS  CONN_BUFSIZE

static inline int
utf8towcs(wchar_t *result, const char *str, int len)
{
    int l;
    
    /* wmemset(result, L'\0', len); */

    l = lucene_utf8towcs(result, str, len);

    if (l < 0) {
        printf ("bad utf8 sent to lucene.  %d\n", l);
        l = 0;
    }
    if (l < len) {
        result[l] = 0;
    }
    result[len-1] = 0;
    return l;
}


int 
FilterDocument(StoreClient *client, DStoreDocInfo *info, LuceneIndex *index, char *path)
{
    if (info->type == STORE_DOCTYPE_MAIL) {
        return FilterMail(client, info, index, path);
    } else if (info->type == STORE_DOCTYPE_AB) {
        return FilterContact(client, info, index, path);
    } else if (info->type == STORE_DOCTYPE_EVENT) {
        return FilterEvent(client, info, index, path);
    }

    return 0;
}

int 
AddFlags(Document *doc, DStoreDocInfo *info)
{
    if (info->flags & STORE_MSG_FLAG_PURGED) {
        FilterAddKeyword(doc, _T("nmap.flags"), "purged", FALSE);
    }
    if (info->flags & STORE_MSG_FLAG_SEEN) {
        FilterAddKeyword(doc, _T("nmap.flags"), "seen", FALSE);
    }
    if (info->flags & STORE_MSG_FLAG_ANSWERED) {
        FilterAddKeyword(doc, _T("nmap.flags"), "answered", FALSE);
    }
    if (info->flags & STORE_MSG_FLAG_FLAGGED) {
        FilterAddKeyword(doc, _T("nmap.flags"), "flagged", FALSE);
    }
    if (info->flags & STORE_MSG_FLAG_DELETED) {
        FilterAddKeyword(doc, _T("nmap.flags"), "deleted", FALSE);
    }
    if (info->flags & STORE_MSG_FLAG_DRAFT) {
        FilterAddKeyword(doc, _T("nmap.flags"), "draft", FALSE);
    }
    if (info->flags & STORE_MSG_FLAG_RECENT) {
        FilterAddKeyword(doc, _T("nmap.flags"), "recent", FALSE);
    }
    if (info->flags & STORE_MSG_FLAG_JUNK) {
        FilterAddKeyword(doc, _T("nmap.flags"), "junk", FALSE);
    }
    if (info->flags & STORE_MSG_FLAG_SENT) {
        FilterAddKeyword(doc, _T("nmap.flags"), "sent", FALSE);
    }
    if (info->flags & STORE_DOCUMENT_FLAG_STARS) {
        FilterAddKeyword(doc, _T("nmap.flags"), "starred", FALSE);
    }

    /* This has similar semantics to the conversation's "inbox" source,
     * but only applies to this message */
    if (info->collection == STORE_MAIL_INBOX_GUID 
        && (info->flags & (STORE_MSG_FLAG_JUNK | STORE_MSG_FLAG_DELETED)) == 0) {
        FilterAddKeyword(doc, _T("nmap.flags"), "inbox", FALSE);
    }
}

int
FilterAddSummaryInfo(Document *doc, DStoreDocInfo *info)
{
    char buf[CONN_BUFSIZE + 1];
    TCHAR tbuf[TBUF_CHARS + 1];
    Field *f;

    snprintf(buf, sizeof(buf), GUID_FMT, info->guid);
    utf8towcs(tbuf, buf, TBUF_CHARS);
    doc->add(*Field::Keyword(_T("nmap.guid"), tbuf));

    snprintf(buf, sizeof(buf), GUID_FMT, info->conversation);
    utf8towcs(tbuf, buf, TBUF_CHARS);
    doc->add(*Field::Keyword(_T("nmap.conversation"), tbuf));

    if (info->type & STORE_DOCTYPE_FOLDER) {
        utf8towcs(tbuf, "folder", TBUF_CHARS);
        doc->add(*Field::Keyword(_T("nmap.type"), tbuf));
    }
    if (info->type & STORE_DOCTYPE_SHARED) {
        utf8towcs(tbuf, "shared", TBUF_CHARS);
        doc->add(*Field::Keyword(_T("nmap.type"), tbuf));
    }

    AddFlags(doc, info);

    char sortstr[CONN_BUFSIZE];
    char *type = NULL;
    switch (info->type) {
        BongoCalTime t;
        
    case STORE_DOCTYPE_MAIL:
        type = "mail";
        t = BongoCalTimeNewFromUint64(info->data.mail.sent, FALSE, NULL);
        BongoCalTimeToIcal(t, sortstr, sizeof(sortstr));
        break;
    case STORE_DOCTYPE_EVENT:
        type = "event";
        snprintf(sortstr, sizeof(sortstr), "%s%x", 
                 info->data.event.start, info->guid);
        break;
    case STORE_DOCTYPE_AB:
        type = "addressbook";
        /* FIXME: use the contact name */
        t = BongoCalTimeNewFromUint64(info->timeCreatedUTC, FALSE, NULL);
        BongoCalTimeToIcal(t, sortstr, sizeof(sortstr));
        break;
    case STORE_DOCTYPE_CONVERSATION:
        type = "conversation";
        /* FIXME: use the contact name */
        t = BongoCalTimeNewFromUint64(info->data.conversation.lastMessageDate, FALSE, NULL);
        BongoCalTimeToIcal(t, sortstr, sizeof(sortstr));
        break;
    default:
        break;
    }

    if (type != NULL) {
        utf8towcs(tbuf, type, TBUF_CHARS);
        doc->add(*Field::Keyword(_T("nmap.type"), tbuf));

        utf8towcs(tbuf, sortstr, TBUF_CHARS);
        doc->add(*Field::Keyword(_T("nmap.sort"), tbuf));
    }

    return 0;
}

int
FilterAddText(Document *doc, const TCHAR *fieldName, const char *value, bool includeInEverything)
{
    TCHAR tbuf[TBUF_CHARS + 1];
    int dbg;
    
    if (!value) {
        return 1;
    }

    dbg = utf8towcs(tbuf, value, TBUF_CHARS);

    doc->add(*Field::Text(fieldName, tbuf));
    
    if (includeInEverything) {
        doc->add(*Field::Text(_T("everything"), tbuf));
    }

    return 0;
}

int
FilterAddTextWithBoost(Document *doc, const TCHAR *fieldName, const char *value, float boost, bool includeInEverything)
{
    Field *f;
    TCHAR tbuf[TBUF_CHARS + 1];

    utf8towcs(tbuf, value, TBUF_CHARS);

    f = Field::Text(fieldName, tbuf);
    f->setBoost(boost);
    doc->add(*f);
    if (includeInEverything) {
        f = Field::Text(_T("everything"), tbuf);
        f->setBoost(boost);
        doc->add(*f);
    }

    return 0;
}

int
FilterAddKeyword(Document *doc, const TCHAR *fieldName, const char *value, bool includeInEverything)
{
    TCHAR tbuf[TBUF_CHARS + 1];

    utf8towcs(tbuf, value, TBUF_CHARS);

    doc->add(*Field::Keyword(fieldName, tbuf));
    if (includeInEverything) {
        doc->add(*Field::Keyword(_T("everything"), tbuf));
    }

    return 0;
}

int
FilterAddDate(Document *doc, const TCHAR* fieldName, uint64_t date)
{
    TCHAR *str;
    
    try {
        str = DateField::timeToString(date);
        doc->add(*Field::Text(fieldName, str));
        _CLDELETE_CARRAY(str);

        return 0;
    } catch (...) {
        return 1;
    }
}

int
FilterAddJsonString(Document *doc, const TCHAR *fieldName, BongoJsonObject *obj, const char *childName, bool includeInEverything)
{
    const char *val;
    
    if (BongoJsonObjectGetString(obj, childName, &val) == BONGO_JSON_OK) {
        return FilterAddText(doc, fieldName, val);
    }

    return 1;
}


