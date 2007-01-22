#include <config.h>

extern "C" {
#include "stored.h"
#include "mime.h"
}

#include "filter.h"
#include "mail-parser.h"
#include "lucene-index.h"
#include "messages.h"
#include "BongoStreamInputStream.h"

static int
AddTextPart(Document *doc,
	    DStoreDocInfo *info, 
            FILE *fh,
	    MimeResponseLine *part)
	    
{
    jstreams::BongoStreamInputStream <TCHAR> *istream = 
        new jstreams::BongoStreamInputStream<TCHAR>
        (fh, 
         info->start + info->headerlen + part->start,
         part->len,
         part->encoding[0] != '-' ? (char*)part->encoding : NULL,
         part->charset[0] != '-' ? (char*)part->charset : NULL);
    
    Reader *reader = new Reader(istream, true);

    doc->add(*Field::Text(_T("everything"), reader));

    //delete reader;

    return 0;
}

static int
AddPart(Document *doc,
	    DStoreDocInfo *info, 
            FILE *fh,
	    MimeResponseLine *part)
	    
{  
    if (part->type[0] && part->subtype[0]) {
        char attachmentType[CONN_BUFSIZE];
        
        snprintf(attachmentType, CONN_BUFSIZE, "%s/%s", part->type, part->subtype);
        BongoStrToLower(attachmentType);
        
        FilterAddKeyword(doc, _T("nmap.mail.attachmentmimetype"), attachmentType);
    }    

    if (part->name[0]) {
        FilterAddText(doc, _T("attachment"), (const char*)part->name);
    }
            
    if (!strcmp((char*)part->type, "text")) {
        AddTextPart(doc, info, fh, part);
    }

    return 0;
}

static int 
AddParts(Document *doc,
         DStoreDocInfo *info, 
         StoreClient *client, 
         FILE *fh)
{
    MimeReport *report;
    unsigned char buffer[XPL_MAX_RFC822_LINE];
    int ccode;    

    if (0 != XplFSeek64(fh, info->start + info->headerlen, SEEK_SET)) {
        ccode = ConnWriteStr(client->conn, MSG4224CANTWRITE);
        goto finish;
    }

    report = MimeParse(fh, info->bodylen, buffer, sizeof(buffer));
    if (report) {
        for (unsigned long i = 0; i < report->lineCount; i++) {
            MimeResponseLine *part = &report->line[i];
            if (part->code == 2002) {
                AddPart(doc, info, fh, part);    
            } 
        }
        MimeReportFree(report);
    } else {
        /* FIXME: log */
        printf("bongostore: couldn't parse mime for doc: %016llx, full text won't be indexed\n", 
               info->guid);
    }

finish :

    return ccode;    
}

static void
AddAddressKeywords(Document *doc, 
                   const TCHAR *property, 
                   const char *addressList)
{
    char *dup;
    char *p;
    char *p2;

    dup = MemStrdup(addressList);

    p = strchr(dup, '<');
    while (p) {
        p++;
        p2 = strchr(p, '>');
        if (p2) {
            *p2 = '\0';
            FilterAddKeyword(doc, property, p);
            p = strchr(p2 + 1, '>');
        } else {
            p = NULL;
        }
    }

    MemFree(dup);
}

static int 
AddMailSummaryInfo(Document *doc, DStoreDocInfo *info)
{
    TCHAR *date;
    Field *f;

    FilterAddSummaryInfo(doc, info);
    if (info->data.mail.subject) {
        FilterAddTextWithBoost(doc, _T("nmap.mail.subject"),
                               info->data.mail.subject, true, 2.0);
    }

    if (info->data.mail.from) {
	FilterAddText(doc, _T("nmap.from"), info->data.mail.from);
        AddAddressKeywords(doc, _T("nmap.summary.from"), info->data.mail.from);    
    }
    
    FilterAddDate(doc, _T("nmap.date"), info->data.mail.sent);
    
    if (info->data.mail.listId) {
        FilterAddKeyword(doc, _T("nmap.summary.listid"), info->data.mail.listId, FALSE);
    }

    return 0;
}

static int
AddMailPropertyInfo(StoreClient *client, Document *doc, DStoreDocInfo *info)
{
    BongoStringBuilder sb;
    
    
    BongoStringBuilderInit(&sb);

    DStoreGetPropertySBAppend(client->handle, info->guid, "bongo.to", &sb);
    if (sb.value && sb.value[0]) {
        FilterAddText(doc, _T("nmap.to"), sb.value);
        AddAddressKeywords(doc, _T("nmap.summary.to"), sb.value);
        sb.len = 0;
        sb.value[0] = '\0';
    }

    DStoreGetPropertySBAppend(client->handle, info->guid, "bongo.cc", &sb);
    if (sb.value && sb.value[0]) {
        FilterAddText(doc, _T("nmap.cc"), sb.value);
        AddAddressKeywords(doc, _T("nmap.summary.to"), sb.value);    
    }
    
    sb.len = 0;

    MemFree(sb.value);

    return 0;
}


int
FilterMail(StoreClient *client, DStoreDocInfo *info, LuceneIndex *index)
{
    FILE *fh;
    char path[XPL_MAX_PATH + 1];
    int ccode;
    Document *doc = new Document();

    FilterAddKeyword(doc, _T("type"), "mail", false);
    
    FindPathToDocFile(client, info->collection, path, sizeof(path));
    
    fh = fopen(path, "rb");
    if (!fh) {
        ccode = ConnWriteStr(client->conn, MSG4224CANTWRITE);
        return ccode;
    } else {
        int ret;
        
        AddMailSummaryInfo(doc, info);
        AddMailPropertyInfo(client, doc, info);
        AddParts(doc, info, client, fh);
        
        try {
            index->GetIndexWriter()->addDocument(doc);
            ret = 0;
        } catch (...) {
            ret = 1;
        }        
        
        delete doc;
        fclose(fh);

        return ret;
    }
}
