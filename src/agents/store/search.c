/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/
#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <assert.h>
#include <bongostream.h>

#include "stored.h"
#include "messages.h"
#include "mime.h"
#include <string.h>

long ReadSourceFile(void *source, char *buffer, unsigned long maxRead);


long
ReadSourceFile(void *source, char *buffer, unsigned long maxRead)
{
    return(fread(buffer, 1, maxRead, (FILE *)source));
}

__inline static CCode
ReportHit(Connection *conn, DStoreDocInfo *info)
{
    return(ConnWriteF(conn, 
                       "2001 " GUID_FMT " %08x\r\n", 
                       info->guid, 
                       info->imapuid ? info->imapuid : (uint32_t) info->guid));
}

static CCode
SearchDocument(StoreClient *client, DStoreDocInfo *info, 
               StoreSearchInfo *query, FILE *f)
{
    CCode ccode = 0;

    switch (query->type) {

    case STORE_SEARCH_TEXT: /* TEXT checks both HEADERS and BODY */
    case STORE_SEARCH_HEADERS:
    {
        if (XplFSeek64(f, 0, SEEK_SET)) {
            ccode = ConnWriteStr(client->conn, MSG4224CANTREAD);
            break;
        }
        if (BongoStreamSearchRfc822Header(ReadSourceFile, (void *)f, info->data.mail.headerSize, 
                                 NULL, query->query))
        {
            ccode = ReportHit(client->conn, info);
            break;
        }
        if (query->type == STORE_SEARCH_HEADERS) {
            break;
        }
        /* nothing found in headers; fall through to STORE_SEARCH_BODY */
    }

    case STORE_SEARCH_BODY: {
        MimeReport *report = NULL;
        unsigned i;

        switch (MimeGetInfo(client, info, &report)) {
        case 1:

            MimeReportFixup(report);

            for (i = 0; i < report->lineCount && -1 != ccode; i++) {
                MimeResponseLine *line = &report->line[i];

                if (2002 != line->code) {
                    continue;
                }

                if (XplFSeek64(f, line->start, SEEK_SET)) {
                    ccode = ConnWriteStr(client->conn, MSG4224CANTREAD);
                    break;
                }
                if (BongoStreamSearchMimePart(ReadSourceFile, (void *)f, line->len, 
                                             line->charset, line->encoding, line->subtype,
                                             query->query))
                {
                    ccode = ReportHit(client->conn, info);
                    break;
                }
            }
   
            MimeReportFree(report);

            break;
        case -1:
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            break;
        case -2:
            ccode = ConnWriteStr(client->conn, MSG4120DBLOCKED);
            break;
        case -3:
            ccode = ConnWriteF(client->conn, MSG4220NOGUID);
            break;
        case -4:
            ccode = ConnWriteStr(client->conn, MSG4224CANTREAD);
            break;
        case -5:
        case 0:
        default:
            ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
            break;
        }
        break;
    }

    case STORE_SEARCH_HEADER: {
        /* 'subject' and 'from' fields are special cases because they are already in utf-8 in the info structure */
        if (info->data.mail.subject && XplStrCaseCmp(query->header, "subject") == 0) {
            if (BongoStrCaseStr(info->data.mail.subject, query->query)) {
                ccode = ReportHit(client->conn, info);
            }
            break;
        }

        if (XplStrCaseCmp(query->header, "from") == 0) {
            
            if (info->data.mail.from && BongoStrCaseStr(info->data.mail.from, query->query)) {
                ccode = ReportHit(client->conn, info);
            }
            break;
        }

        if (XplFSeek64(f, 0, SEEK_SET)) {
            ccode = ConnWriteStr(client->conn, MSG4224CANTREAD);
            break;
        }
        if (BongoStreamSearchRfc822Header(ReadSourceFile, (void *)f, info->data.mail.headerSize, 
                                 query->header, query->query))
        {
            ccode = ReportHit(client->conn, info);
        }

        break;
    }

    }

    return(ccode);
}



CCode
StoreCommandSEARCH(StoreClient *client, uint64_t guid, StoreSearchInfo *query)
{
    CCode ccode = 0;
    DStoreStmt *stmt = NULL;
    DStoreDocInfo info;
    DStoreDocInfo child;
    char path[XPL_MAX_PATH + 1];
    FILE *f = NULL;

    ccode = StoreCheckAuthorizationGuid(client, guid, STORE_PRIV_READ);
    if (ccode) {
        return ccode;
    }

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case 0:
        return ConnWriteStr(client->conn, MSG4220NOGUID);
    case 1:
        break;
    case -1:
    default:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    // we're searching a document
    if (!STORE_IS_FOLDER(info.type)) {
        FindPathToDocument(client, info.collection, info.guid, path, sizeof(path));

        f = fopen(path, "rb");
        if (!f) {
            return ConnWriteStr(client->conn, MSG4224CANTREAD);
        }
        
        ccode = SearchDocument(client, &info, query, f);
        if (-1 != ccode) {
            ccode = ConnWriteStr(client->conn, MSG1000OK);
        }
        fclose(f);
        return ccode;
    }
    
    // we're searching a collection
    stmt = DStoreListColl(client->handle, guid, -1, -1);
    if (!stmt) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }
    
    while (-1 != ccode) {
        switch (DStoreInfoStmtStep(client->handle, stmt, &child)) {
        case 0:
            ccode = ConnWriteStr(client->conn, MSG1000OK);
            goto finish;
        case -1:
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        case 1:
            if (!STORE_IS_FOLDER(child.type)) {
                FindPathToDocument(client, child.collection, child.guid, path, sizeof(path));
                f = fopen(path, "rb");
                if (!f) {
                    ccode = ConnWriteStr(client->conn, MSG4224CANTREAD);
                    goto finish;
                }
                ccode = SearchDocument(client, &child, query, f);
                fclose(f);
            }
            break;
        }
    }
    ccode = ConnWriteStr(client->conn, MSG1000OK);
    
finish:
    if (stmt) DStoreStmtEnd(client->handle, stmt);
    return ccode;
}

