/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005, 2006 Novell, Inc. All Rights Reserved.
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
#include <limits.h>
#include <time.h>

#include "stored.h"
#include "calendar.h"

#include "mail.h"
#include "conversations.h"
#include "messages.h"
#include "mime.h"
#include "index.h"


static uint32_t
CommandHash (const void *key)
{
    char *s = (char *) key;
    uint32_t b = 1;
    uint32_t h = 0;

    for (; *s && ' ' != *s; s++) {
        h += b * tolower(*s);
        b = (b << 5) - b;
    }
    return h;
}


/* a strcmp() that ends on first space.  (might be optimizable by word-compares) */

static int
CommandCmp(const void *cmda, const void *cmdb)
{
    char *a = (char *) cmda;
    char *b = (char *) cmdb;

    while (*a) {
        if (tolower(*a) != tolower(*b)) {
            if (!*b && ' ' == *a) {
                return 0;
            } else {
                return -1;
            }
        }
        a++;
        b++;
    }
    return 0;
}


static BongoHashtable *CommandTable;


/* returns 0 on success */

/* I probably should have used ProtocolCommandTree?  The hashtable came
 * about when I tried to do this with with lex yacc code */

int
StoreSetupCommands()
{
    CommandTable = BongoHashtableCreate(64, CommandHash, CommandCmp);

    if (!CommandTable ||

        /* session commands */
        BongoHashtablePutNoReplace(CommandTable, "AUTH", (void *) STORE_COMMAND_AUTH) ||
        BongoHashtablePutNoReplace(CommandTable, "COOKIE", (void *) STORE_COMMAND_COOKIE) ||
        BongoHashtablePutNoReplace(CommandTable, "QUIT", (void *) STORE_COMMAND_QUIT) ||
        BongoHashtablePutNoReplace(CommandTable, "STORE", (void *) STORE_COMMAND_STORE) ||
        BongoHashtablePutNoReplace(CommandTable, "TOKEN", (void *) STORE_COMMAND_TOKEN) ||
        BongoHashtablePutNoReplace(CommandTable, "TOKENS", (void *) STORE_COMMAND_TOKENS) ||
        BongoHashtablePutNoReplace(CommandTable, "USER", (void *) STORE_COMMAND_USER) ||
        
        /* collection commands */
        BongoHashtablePutNoReplace(CommandTable, "COLLECTIONS", 
                         (void *) STORE_COMMAND_COLLECTIONS) ||
        BongoHashtablePutNoReplace(CommandTable, "CREATE", (void *) STORE_COMMAND_CREATE) ||
        BongoHashtablePutNoReplace(CommandTable, "LIST", (void *) STORE_COMMAND_LIST) ||
        BongoHashtablePutNoReplace(CommandTable, "MAILINGLISTS", 
                                  (void *) STORE_COMMAND_MAILINGLISTS) ||
        BongoHashtablePutNoReplace(CommandTable, "RENAME", (void *) STORE_COMMAND_RENAME) ||
        BongoHashtablePutNoReplace(CommandTable, "REMOVE", (void *) STORE_COMMAND_REMOVE) ||
        BongoHashtablePutNoReplace(CommandTable, "SEARCH", (void *) STORE_COMMAND_SEARCH) ||
        BongoHashtablePutNoReplace(CommandTable, "WATCH", (void *) STORE_COMMAND_WATCH) ||

        /* document commands */
        BongoHashtablePutNoReplace(CommandTable, "COPY", (void *) STORE_COMMAND_COPY) ||
        BongoHashtablePutNoReplace(CommandTable, "DELETE", (void *) STORE_COMMAND_DELETE) ||
        BongoHashtablePutNoReplace(CommandTable, "FLAG", (void *) STORE_COMMAND_FLAG) ||
        BongoHashtablePutNoReplace(CommandTable, "INFO", (void *) STORE_COMMAND_INFO) ||
        BongoHashtablePutNoReplace(CommandTable, "MESSAGES ",  (void *) STORE_COMMAND_MESSAGES) ||
        BongoHashtablePutNoReplace(CommandTable, "MFLAG", (void *) STORE_COMMAND_MFLAG) ||
        BongoHashtablePutNoReplace(CommandTable, "MIME ",  (void *) STORE_COMMAND_MIME) ||
        BongoHashtablePutNoReplace(CommandTable, "MOVE ", (void *) STORE_COMMAND_MOVE) ||
        BongoHashtablePutNoReplace(CommandTable, "MWRITE", (void *) STORE_COMMAND_MWRITE) ||
        BongoHashtablePutNoReplace(CommandTable, "PROPGET", (void *) STORE_COMMAND_PROPGET) ||
        BongoHashtablePutNoReplace(CommandTable, "PROPSET", (void *) STORE_COMMAND_PROPSET) ||
        BongoHashtablePutNoReplace(CommandTable, "PURGE", (void *) STORE_COMMAND_DELETE) ||
        BongoHashtablePutNoReplace(CommandTable, "READ", (void *) STORE_COMMAND_READ) ||
        BongoHashtablePutNoReplace(CommandTable, "REPLACE", (void *) STORE_COMMAND_REPLACE) ||
        BongoHashtablePutNoReplace(CommandTable, "WRITE", (void *) STORE_COMMAND_WRITE) ||

        /* delivery command */
        BongoHashtablePutNoReplace(CommandTable, "DELIVER", (void *) STORE_COMMAND_DELIVER) ||

        /* conversation commands */
        BongoHashtablePutNoReplace(CommandTable, "CONVERSATION", 
                         (void *) STORE_COMMAND_CONVERSATION) ||
        BongoHashtablePutNoReplace(CommandTable, "CONVERSATIONS", 
                         (void *) STORE_COMMAND_CONVERSATIONS) ||

        BongoHashtablePutNoReplace(CommandTable, "LISTCONVERSATION", 
                         (void *) STORE_COMMAND_CONVERSATION) ||
        BongoHashtablePutNoReplace(CommandTable, "LISTCONVERSATIONS", 
                         (void *) STORE_COMMAND_CONVERSATIONS) ||


        /* index commands */
        BongoHashtablePutNoReplace(CommandTable, "ISEARCH", (void *) STORE_COMMAND_ISEARCH) ||

        /* calendar commands */
        BongoHashtablePutNoReplace(CommandTable, "CALENDARS", 
                         (void *) STORE_COMMAND_CALENDARS) ||
        BongoHashtablePutNoReplace(CommandTable, "EVENTS", (void *) STORE_COMMAND_EVENTS) ||
        BongoHashtablePutNoReplace(CommandTable, "LINK", (void *) STORE_COMMAND_LINK) ||
        BongoHashtablePutNoReplace(CommandTable, "UNLINK", (void *) STORE_COMMAND_UNLINK) ||

        /* alarm commands */

        BongoHashtablePutNoReplace(CommandTable, "ALARMS", (void *) STORE_COMMAND_ALARMS) ||

        /* other */
        BongoHashtablePutNoReplace(CommandTable, "NOOP", (void *) STORE_COMMAND_NOOP) || 
        BongoHashtablePutNoReplace(CommandTable, "TIMEOUT", (void *) STORE_COMMAND_TIMEOUT) || 

        /* debugging commands */
        
        BongoHashtablePutNoReplace(CommandTable, "REINDEX", (void *) STORE_COMMAND_REINDEX) ||
        BongoHashtablePutNoReplace(CommandTable, "RESET", (void *) STORE_COMMAND_RESET) ||

        0)
    {
        XplConsolePrintf(AGENT_NAME ": Couldn't setup NMAP command table.\r\n");
        if (CommandTable) {
            BongoHashtableDelete(CommandTable);
            CommandTable = NULL;
        }
        return -1;
    }
    return 0;
}


#define TOKEN_OK 0xffff /* magic */

__inline static CCode
CheckTokC(StoreClient *client, int n, int min, int max)
{
    if (n < min || n > max) {
        return ConnWriteStr(client->conn, MSG3010BADARGC);
    }

    return TOKEN_OK;
}


static CCode
ParseGUID(StoreClient *client, char *token, uint64_t *out)
{
    uint64_t t;
    char *endp;

    t = HexToUInt64(token, &endp);
    if (*endp || 0 == t /* || ULLONG_MAX == t */) {
        return ConnWriteStr(client->conn, MSG3011BADGUID);
    }

    *out = t;
    return TOKEN_OK;
}


static CCode
ParseUnsignedLong(StoreClient *client, char *token, unsigned long *out)
{
    unsigned long t;
    char *endp;

    t = strtoul(token, &endp, 10);
    if (*endp) {
        return ConnWriteStr(client->conn, MSG3017BADINTARG);
    }
    *out = t;
    return TOKEN_OK;
}


static CCode
ParseInt(StoreClient *client, char *token, int *out)
{
    long t;
    char *endp;

    t = strtol(token, &endp, 10);
    if (*endp) {
        return ConnWriteStr(client->conn, MSG3017BADINTARG);
    }
    *out = t;
    return TOKEN_OK;
}


static CCode
ParseStreamLength(StoreClient *client, char *token, int *out) 
{
    long t;
    char *endp;
    
    if (token[0] == '-' && token[1] == '\0') {
        *out = -1;
        return TOKEN_OK;
    }
    
    t = strtol(token, &endp, 10);
    if (*endp) {
        return ConnWriteStr(client->conn, MSG3017BADINTARG);
    }

    *out = t;
    return TOKEN_OK;    
}


static int 
ParseRange(StoreClient *client, char *token, int *startOut, int *endOut) 
{
    long t;
    char *endp;
    
    if (!strcmp(token, "all")) {
        *startOut = *endOut = -1;
        return TOKEN_OK;
    }
    
    t = strtol(token, &endp, 10);
    if (endp == token || *endp != '-' || t < 0) {
        return ConnWriteStr(client->conn, MSG3020BADRANGEARG);
    }
    *startOut = t;
    
    t = strtol(endp + 1, &endp, 10);
    if (*endp /* || t < *startOut */) {
        return ConnWriteStr(client->conn, MSG3020BADRANGEARG);
    }
    *endOut = t;
    
    return TOKEN_OK;
}

static CCode
ParseISODateTime(StoreClient *client,
                 char *token,
                 BongoCalTime *timeOut)
{
    /* iso-date-time: YYYY-MM-DD HH:MM:SS'Z' */
    int n;
    BongoCalTime t = BongoCalTimeEmpty();

    t = BongoCalTimeSetTimezone(t, BongoCalTimezoneGetUtc());

    n = sscanf(token, "%4d-%2d-%2d %2d:%2d:%2dZ",
               &t.year, &t.month, &t.day,
               &t.hour, &t.minute, &t.second);

    if (6 != n) {
        return ConnWriteStr(client->conn, MSG3021BADISODATETIME);
    }

    t.month--;

    *timeOut = t;
    
    return TOKEN_OK;
}

static CCode
ParseIcalDateTime(StoreClient *client,
                  char *token,
                  BongoCalTime *timeOut)
{
    BongoCalTime t;

    t = BongoCalTimeParseIcal(token);
    if (BongoCalTimeIsEmpty(t)) {
        return ConnWriteStr(client->conn, MSG3021BADICALDATETIME);
    }

    *timeOut = BongoCalTimeSetTimezone(t, BongoCalTimezoneGetUtc());

    return TOKEN_OK;
}

static CCode
ParseDateTime(StoreClient *client, char *token, BongoCalTime *timeOut)
{
    int len;
    
    len = strlen(token);

    if (len == 20) {
        return ParseISODateTime(client, token, timeOut);
    } else if (len == 15 || len == 16) {
        return ParseIcalDateTime(client, token, timeOut);
    } else {
        unsigned long ulong;
        CCode ccode;

        ccode = ParseUnsignedLong(client, token, &ulong);

        if (ccode == TOKEN_OK) {
            *timeOut = BongoCalTimeNewFromUint64(ulong, FALSE, NULL);
        }

        return ccode;
    }
}

static CCode
ParseDateTimeToUint64(StoreClient *client, char *token, uint64_t *timeOut)
{
    CCode ccode;
    int len;
    
    len = strlen(token);

    if (len == 20 || len == 15 || len == 16) {
        BongoCalTime t;

        ccode = ParseDateTime(client, token, &t);
        if (ccode == TOKEN_OK) {
            *timeOut = BongoCalTimeAsUint64(t);
        }

        return ccode;
    } else {
        /* If it's a long arg, just pass it through */
        unsigned long ulong;
        CCode ccode;

        ccode = ParseUnsignedLong(client, token, &ulong);

        *timeOut = (uint64_t)ulong;

        return ccode;
    }
}

static CCode
ParseDateTimeToString(StoreClient *client, char *token, char *buffer, int buflen)
{
    int len;

    len = strlen(token);

    if (len == 15 || len == 16) {
        /* FIXME: do a better validation */
        
        if (!isdigit(token[0]) || !isdigit(token[1]) || !isdigit(token[2]) || 
            !isdigit(token[3]) || !isdigit(token[4]) || !isdigit(token[5]) || 
            !isdigit(token[6]) || !isdigit(token[7]))
        {
            return ConnWriteStr(client->conn, MSG3021BADDATETIME);
        }
        
        if (len > 8) {
            if ('T' != token[8] || 
                !isdigit(token[9]) || !isdigit(token[10]) || !isdigit(token[11]) || 
                !isdigit(token[12]) || !isdigit(token[13]) || !isdigit(token[14]))
            {
                return ConnWriteStr(client->conn, MSG3021BADDATETIME);
            }
        }

        BongoStrNCpy(buffer, token, buflen);

        return TOKEN_OK;
    } else {
        BongoCalTime t;
        CCode ccode;
        
        ccode = ParseDateTime(client, token, &t);
        if (ccode == TOKEN_OK) {
            BongoCalTimeToIcal(t, buffer, buflen);
        }

        return ccode;
    }
}

static CCode
ParseDateRange(StoreClient *client, char *token, char *startOut, char *endOut)
{
    /* date-range: date-time '-' date-time */
 
    CCode ccode;
    char *p;

    p = strchr(token, '-');
    if (!p) {
        return ConnWriteStr(client->conn, MSG3021BADDATERANGE);
    }
    *p = 0;
    ccode = ParseDateTimeToString(client, token, startOut, BONGO_CAL_TIME_BUFSIZE);
    if (TOKEN_OK != ccode) {
        return ccode;
    }
    ++p;
    return ParseDateTimeToString(client, p, endOut, BONGO_CAL_TIME_BUFSIZE);
}


static CCode
ParseFlag(StoreClient *client, char *token, int *valueOut, int *actionOut)
{
    long t;
    char *endp;

    if (*token == '+') {
        *actionOut = STORE_FLAG_ADD;
        token++;
    } else if (*token == '-') {
        *actionOut = STORE_FLAG_REMOVE;
        token++;
    } else {
        *actionOut = STORE_FLAG_REPLACE;
    }

    t = strtol(token, &endp, 10);
    if (*endp) {
        return ConnWriteStr(client->conn, MSG3017BADINTARG);
    }
    *valueOut = t;
    return TOKEN_OK;
}


static CCode
ParseCollection(StoreClient *client, char *token, DStoreDocInfo *info)
{
    int ccode;
    int dcode;

    if ('/' != token[0]) {
        uint64_t guid;
        ccode = ParseGUID(client, token, &guid);
        if (TOKEN_OK != ccode) {
            return ccode;
        } 
        dcode = DStoreGetDocInfoGuid(client->handle, guid, info);
    } else {
        dcode = DStoreGetDocInfoFilename(client->handle, token, info);
    }
    switch (dcode) {
    case 1:
        if (STORE_IS_FOLDER(info->type)) {
            return TOKEN_OK;
        } else {
            return (StoreCheckAuthorization(client, info, STORE_PRIV_READ) || 
                    ConnWriteStr(client->conn, MSG3015NOTCOLL));
        }
    case 0:
        if ('/' == token[0]) {
            return (StoreCheckAuthorizationPath(client, token, STORE_PRIV_READ) ||
                    ConnWriteStr(client->conn, MSG4224NOCOLLECTION));
        } else {
            return ConnWriteStr(client->conn, MSG4220NOGUID);
        }
    default:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }
    return TOKEN_OK;
}


static CCode
ParseDocumentInfo(StoreClient *client, char *token, DStoreDocInfo *info)
{
    int ccode;
    int dcode;
    uint64_t guid;
    
    if ('/' != token[0]) {
        ccode = ParseGUID(client, token, &guid);
        if (ccode != TOKEN_OK) {
            return ccode;
        }
        dcode = DStoreGetDocInfoGuid(client->handle, guid, info);
    } else {
        dcode = DStoreGetDocInfoFilename(client->handle, token, info);
    }

    switch (dcode) {
    case 1:
        return TOKEN_OK;
    case 0:
        if ('/' == token[0]) {
            return (StoreCheckAuthorizationPath(client, token, STORE_PRIV_READ) ||
                    ConnWriteStr(client->conn, MSG4224NODOCUMENT));
        } else {
            return ConnWriteStr(client->conn, MSG4220NOGUID);
        }
    default:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }
}


static CCode
ParseDocument(StoreClient *client, char *token, uint64_t *outGuid)
{
    int ccode;
    
    if ('/' != token[0]) {
        return ParseGUID(client, token, outGuid);
    }

    ccode = DStoreFindDocumentGuid(client->handle, token, outGuid);
    
    if (-1 == ccode) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    } else if (0 == ccode) {
        if ('/' == token[0]) {
            return (StoreCheckAuthorizationPath(client, token, STORE_PRIV_READ) ||
                    ConnWriteStr(client->conn, MSG4224NODOCUMENT));
        } else {
            return ConnWriteStr(client->conn, MSG4220NOGUID);
        }
    }

    return TOKEN_OK;
}


static CCode
ParseCreateDocument(StoreClient *client, char *token, uint64_t *outGuid, 
                    StoreDocumentType type)
{
    int ccode;
    int dcode;
    DStoreDocInfo info;

    if ('/' != token[0]) {
        ccode = ParseGUID(client, token, outGuid);
        if (TOKEN_OK != ccode) {
            return ccode;
        }
        dcode = DStoreGetDocInfoGuid(client->handle, *outGuid, &info);
    } else {
        dcode = DStoreGetDocInfoFilename(client->handle, token, &info);
    }
    *outGuid = info.guid;

    if (1 == dcode) {
        return TOKEN_OK;
    } else if (dcode) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    if ('/' != token[0]) {
        return ConnWriteStr(client->conn, MSG4220NOGUID);
    }

    switch (StoreCreateDocument(client, type, token, outGuid)) {
    case 0:
        return TOKEN_OK;
    case -1:
        return ConnWriteStr(client->conn, MSG3019BADCOLLNAME);
    case -2:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    case -5:
        return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
    case -4:
    default:
        return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    }
}


static CCode
ParseDocType(StoreClient *client, char *token, StoreDocumentType *out)
{
    long t;
    char *endp;

    t = strtol(token, &endp, 10);
    if (*endp) {
        return ConnWriteStr(client->conn, MSG3016DOCTYPESYNTAX);
    }

    *out = t;

    t &= ~(STORE_DOCTYPE_FOLDER | STORE_DOCTYPE_SHARED);
    if (t < STORE_DOCTYPE_UNKNOWN || t > STORE_DOCTYPE_CONFIG) {
        return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
    }

    return TOKEN_OK;
}



static CCode
ParsePropName(StoreClient *client, char *token, StorePropInfo *prop)
{
    char *p;

    if (strchr(token, ',') || strchr(token, '/')) {
        return ConnWriteStr(client->conn, MSG3244BADPROPNAME);
    }
    p = strchr(token, '.');
    if (!p) {
        return ConnWriteStr(client->conn, MSG3244BADPROPNAME);
    }
    *p = 0;
    if (!XplStrCaseCmp(token, "nmap")) {
        p++;
        if (!XplStrCaseCmp(p, "guid")) {
            prop->type = STORE_PROP_GUID;
        } else if (!XplStrCaseCmp(p, "collection")) {
            prop->type = STORE_PROP_COLLECTION;
        } else if (!XplStrCaseCmp(p, "document")) {
            prop->type = STORE_PROP_DOCUMENT;
        } else if (!XplStrCaseCmp(p, "index")) {
            prop->type = STORE_PROP_INDEX;
        } else if (!XplStrCaseCmp(p, "length")) {
            prop->type = STORE_PROP_LENGTH;
        } else if (!XplStrCaseCmp(p, "type")) {
            prop->type = STORE_PROP_TYPE;
        } else if (!XplStrCaseCmp(p, "flags")) {
            prop->type = STORE_PROP_FLAGS;
        } else if (!XplStrCaseCmp(p, "created")) {
            prop->type = STORE_PROP_CREATED;
        } else if (!XplStrCaseCmp(p, "lastmodified")) {
            prop->type = STORE_PROP_LASTMODIFIED;
        } else if (!XplStrCaseCmp(p, "version")) {
            prop->type = STORE_PROP_VERSION;
        } else if (!XplStrCaseCmp(p, "mail.conversation")) {
            prop->type = STORE_PROP_MAIL_CONVERSATION;
        } else if (!XplStrCaseCmp(p, "mail.imapuid")) {
            prop->type = STORE_PROP_MAIL_IMAPUID;
        } else if (!XplStrCaseCmp(p, "mail.sent")) {
            prop->type = STORE_PROP_MAIL_SENT;
        } else if (!XplStrCaseCmp(p, "mail.subject")) {
            prop->type = STORE_PROP_MAIL_SUBJECT;
        } else if (!XplStrCaseCmp(p, "mail.messageid")) {
            prop->type = STORE_PROP_MAIL_MESSAGEID;
        } else if (!XplStrCaseCmp(p, "mail.parentmessageid")) {
            prop->type = STORE_PROP_MAIL_PARENTMESSAGEID;
        } else if (!XplStrCaseCmp(p, "mail.headersize")) {
            prop->type = STORE_PROP_MAIL_HEADERLEN;
        } else if (!XplStrCaseCmp(p, "conversation.subject")) {
            prop->type = STORE_PROP_CONVERSATION_SUBJECT;
        } else if (!XplStrCaseCmp(p, "conversation.unread")) {
            prop->type = STORE_PROP_CONVERSATION_UNREAD;
        } else if (!XplStrCaseCmp(p, "conversation.count")) {
            prop->type = STORE_PROP_CONVERSATION_COUNT;
        } else if (!XplStrCaseCmp(p, "conversation.date")) {
            prop->type = STORE_PROP_CONVERSATION_DATE;
        } else if (!XplStrCaseCmp(p, "access-control")) {
            prop->type = STORE_PROP_ACCESS_CONTROL;
            prop->name = token;
        } else if (!XplStrCaseCmp(p, "event.alarm")) {
            prop->type = STORE_PROP_EVENT_ALARM;
            prop->name = token;
        } else if (!XplStrCaseCmp(p, "event.calendars")) {
            prop->type = STORE_PROP_EVENT_CALENDARS;
        } else if (!XplStrCaseCmp(p, "event.end")) {
            prop->type = STORE_PROP_EVENT_END;
        } else if (!XplStrCaseCmp(p, "event.location")) {
            prop->type = STORE_PROP_EVENT_LOCATION;
        } else if (!XplStrCaseCmp(p, "event.start")) {
            prop->type = STORE_PROP_EVENT_START;
        } else if (!XplStrCaseCmp(p, "event.summary")) {
            prop->type = STORE_PROP_EVENT_SUMMARY;
        } else if (!XplStrCaseCmp(p, "event.uid")) {
            prop->type = STORE_PROP_EVENT_UID;
        } else if (!XplStrCaseCmp(p, "event.stamp")) {
            prop->type = STORE_PROP_EVENT_STAMP;
        } else {
            return ConnWriteStr(client->conn, MSG3244BADPROPNAME);
        }
        --p;
    } else {
        prop->type = STORE_PROP_EXTERNAL;
        prop->name = token;
    }
    
    *p = '.';
    return TOKEN_OK;
}


static CCode
ParsePropList(StoreClient *client,
              char *token,
              StorePropInfo * props,
              int propcount,
              int *count,
              int requireLengths)
{
    int i;
    char *p = token;
    char *q;
    CCode ccode = TOKEN_OK;

    for (i = 0; i < propcount && p && TOKEN_OK == ccode; i++) {
        p = strchr(token, ','); 
        if (p && ',' == *p) {
            *p = 0;
        }
        q = strchr(token, '/');
        if (q) {
            ccode = ParseInt(client, q + 1, &props[i].valueLen);
            if (TOKEN_OK != ccode) {
                return ccode;
            }
            *q = 0;
        } else if (requireLengths) {
            return ConnWriteStr(client->conn, MSG3244BADPROPLENGTH);
        }
        ccode = ParsePropName(client, token, props + i);
        token = p + 1;
    }
    *count = i;
    return ccode;
}


static CCode
ParseHeaderList(StoreClient *client, 
                char *token,
                StoreHeaderInfo *headers,
                int size,
                int *count)
{
    /* <headerlist> ::= (<headerlist> '&') <andclause>
       <andclause>  ::= (<andclause> '|') <header> 
       <header>     ::= ('!') <headername> '=' <value>
    */
    
    int flags = HEADER_INFO_OR;
    int i;
    char *p = token;
    char *q;
    CCode ccode = TOKEN_OK;

    for (i = 0; i < size && p && TOKEN_OK == ccode; i++) {
        headers[i].flags = flags;

        p = strpbrk(token, "|&"); 
        if (p) {
            if ('&' == *p) {
                flags = HEADER_INFO_AND;
            } else {
                flags = HEADER_INFO_OR;
            }
            *p = 0;
        }
        if ('!' == *token) {
            headers[i].flags |= HEADER_INFO_NOT;
            ++token;
        }
        q = strchr(token, '=');
        if (!q) {
            return ConnWriteStr(client->conn, MSG3022BADSYNTAX);
        }
        headers[i].value = q + 1;
        *q = 0;
        
        for (q = headers[i].value; *q; q++) {
            /* FIXME: not UTF-8 safe */
            *q = tolower(*q);
        }

        if (!strcmp("from", token)) {
            headers[i].type = HEADER_INFO_FROM;
        } else if (!strcmp("to", token)) {
            headers[i].type = HEADER_INFO_RECIP;
        } else if (!strcmp("list", token)) {
            headers[i].type = HEADER_INFO_LISTID;
        } else if (!strcmp("subject", token)) {
            headers[i].type = HEADER_INFO_SUBJECT;
        } else {
            return ConnWriteStr(client->conn, MSG3022BADSYNTAX);
        }

        token = p + 1;
    }
    *count = i;
    return ccode;
}

/* FIXME: Unused, deprecated?
static CCode
ParseConversationSource(StoreClient *client,
                        char *token,
                        int *outflags)
{
    GetConversationSourceMask(token, outflags);
    return TOKEN_OK;
}
*/

static CCode
ParseStoreName(StoreClient *client,
               char *token) /* token will be modified */
{
    char *i;

    for (i = token; i < token + STORE_MAX_STORENAME; i++) {
        if (0 == *i) return TOKEN_OK;
	*i = tolower(*i);
    }

    return ConnWriteStr(client->conn, MSG3023USERNAMETOOLONG);
}


static CCode
ParseUserName(StoreClient *client, char *token)
{
    return ParseStoreName(client, token);
}


static CCode
RequireUser(StoreClient *client)
{
    return STORE_PRINCIPAL_USER == client->principal.type 
        ? TOKEN_OK
        : ConnWriteStr(client->conn, MSG3241NOUSER);
}


static CCode
RequireStore(StoreClient *client)
{
    return client->handle ? TOKEN_OK 
                          : ConnWriteStr(client->conn, MSG3243NOSTORE);
}


static CCode
RequireNoStore(StoreClient *client)
{
    return client->handle ? ConnWriteStr(client->conn, MSG3247STORE)
                          : TOKEN_OK;
}


static CCode
RequireManager(StoreClient *client)
{
    return IS_MANAGER(client) ? TOKEN_OK
                              : ConnWriteStr(client->conn, MSG3243MANAGERONLY);
}


static CCode
RequireIdentity(StoreClient *client)
{
    return (IS_MANAGER(client) || HAS_IDENTITY(client)) 
        ? TOKEN_OK
        : ConnWriteStr(client->conn, MSG3241NOUSER);
}


static CCode
RequireAlarmDB(StoreClient *client)
{
    if (!client->system.alarmdb) {
        client->system.alarmdb = AlarmDBOpen(&client->memstack);
    }
    return client->system.alarmdb ? TOKEN_OK 
                                  : ConnWriteStr(client->conn, MSG5008NOALARMDB);
}


static CCode
RequireExclusiveTransaction(StoreClient *client)
{
    switch (DStoreBeginTransaction(client->handle)) {
    case 0:
        return TOKEN_OK;
    case -2:
        return ConnWriteStr(client->conn, MSG4120DBLOCKED);
    default:
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }
}


static CCode
RequireSharedTransaction(StoreClient *client)
{
    switch (DStoreBeginSharedTransaction(client->handle)) {
    case 0:
        return TOKEN_OK;
    case -2:
        return ConnWriteStr(client->conn, MSG4120DBLOCKED);
    default:
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }
}




#define TOK_ARR_SZ 10
#define PROP_ARR_SZ 10
#define HDR_ARR_SZ 10

CCode
StoreCommandLoop(StoreClient *client)
{
    int ccode = 0;
    StoreCommand command;

    while (-1 != ccode && BONGO_AGENT_STATE_RUNNING == StoreAgent.agent.state) {
        char *tokens[TOK_ARR_SZ];
        int n;
        int i;
        
        BongoMemStackReset(&client->memstack);

        ccode = ConnReadAnswer(client->conn, client->buffer, CONN_BUFSIZE);
        if (-1 == ccode || ccode >= CONN_BUFSIZE) {
            break;
        }
                        
        memset(tokens, 0, sizeof(tokens));
        n = CommandSplit(client->buffer, tokens, TOK_ARR_SZ);
        if (0 == n) {
            command = STORE_COMMAND_NULL;
        } else {
            command = (StoreCommand) BongoHashtableGet(CommandTable, tokens[0]);
        }

        if (STORE_COMMAND_NOOP == command) {
            /* Or maybe we want to do something clever, like check for new mail
               after a timeout has elapsed */
            ImportIncomingMail(client);
        }
        
        if (client->watch.collection) {
            /* out of band events */
            ccode = StoreShowWatcherEvents(client);
            if (-1 == ccode) {
                break;
            }

            /* FIXME: What if there were an event related to a document relevant 
               to the command we just parsed?  (or any event?)  Should we ignore 
               the command? */
        }

        switch (command) {
            StoreDocumentType doctype;
            DStoreDocInfo coll;
            int int1;
            int int2;
            int int3;
            int int4;
            unsigned long ulong;
            unsigned long ulong2;
            uint64_t guid;
            uint64_t guid2;
            uint64_t timestamp;
            uint64_t timestamp2;
            const char *filename;
            char *uid;
            char *query;
            StorePropInfo prop;
            StorePropInfo props[PROP_ARR_SZ];
            StoreHeaderInfo hdrs[HDR_ARR_SZ];
            int hdrcnt;
            char dt_start[BONGO_CAL_TIME_BUFSIZE];
            char dt_end[BONGO_CAL_TIME_BUFSIZE];

        case STORE_COMMAND_NULL:
            ccode = ConnWriteStr(client->conn, MSG3000UNKNOWN);
            break;
            
        case STORE_COMMAND_ALARMS:
            /* ALARMS <starttime> <endtime> */
            
            if (TOKEN_OK == (ccode = RequireManager(client)) &&
                TOKEN_OK == (ccode = RequireNoStore(client)) &&
                TOKEN_OK == (ccode = RequireAlarmDB(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = ParseDateTimeToUint64(client, tokens[1], 
                                                           &timestamp)) &&
                TOKEN_OK == (ccode = ParseDateTimeToUint64(client, tokens[2], 
                                                           &timestamp2)))
            {
                ccode = StoreCommandALARMS(client, timestamp, timestamp2);
            }
            break;

        case STORE_COMMAND_AUTH:
            /* AUTH COOKIE <username> <token> [NOUSER] */
            /* AUTH SYSTEM <md5 hash> */
            /* AUTH USER <username> <password> [NOUSER] */
            
            int1 = 0; /* "NOUSER" flag */

            if (TOKEN_OK != (ccode = CheckTokC(client, n, 3, 5))) {
                break;
            }
            if (5 == n) {
                if (!XplStrCaseCmp(tokens[4], "NOUSER")) {
                    int1 = 1;
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    break;
                }
            }
            if (!XplStrCaseCmp(tokens[1], "COOKIE")) {
                if (TOKEN_OK == (ccode = CheckTokC(client, n, 4, 5)) &&
                    TOKEN_OK == (ccode = ParseUserName(client, tokens[2]))) 
                {
                    ccode = StoreCommandAUTHCOOKIE(client, 
                                                   tokens[2], tokens[3], int1);
                }
            } else if (0 == XplStrCaseCmp(tokens[1], "SYSTEM")) {
                ccode = StoreCommandAUTHSYSTEM(client, tokens[2]);
            } else if (0 == XplStrCaseCmp(tokens[1], "USER")) {
                if (TOKEN_OK == (ccode = CheckTokC(client, n, 4, 5)) &&
                    TOKEN_OK == (ccode = ParseUserName(client, tokens[2]))) 
                {
                    ccode = StoreCommandUSER(client, tokens[2], tokens[3], int1);
                }
            } else {
                ccode = ConnWriteStr(client->conn, MSG3000UNKNOWN);
            }
            break;

        case STORE_COMMAND_CALENDARS:
            /* FIXME: this command is no longer needed */
            /* CALENDARS [F <flags>] [P <proplist>] */

            int1 = 0;          /* propcount */
            ulong = ULONG_MAX; /* flags */
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 3)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client))) 
            {
                break;
            }
            for (i = 1; TOKEN_OK == ccode && i < n; i++) {
                if ('F' == *tokens[i]) {
                    ccode = ParseUnsignedLong(client, tokens[1] + 1, &ulong);
                } else if ('P' == *tokens[i] && 0 == int3) {
                    ccode = ParsePropList(client, tokens[i] + 1, 
                                          props, PROP_ARR_SZ, &int1, 0);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
            }
                
            if (TOKEN_OK == ccode) {
                ccode = StoreCommandCALENDARS(client, ulong, props, int1);
            }
            break;

        case STORE_COMMAND_COLLECTIONS:
            /* COLLECTIONS [<collection>] */

            if (1 == n) {
                tokens[1] = "/";
            }
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 1, 2)) &&
                TOKEN_OK == (ccode = RequireSharedTransaction(client)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[1], &coll)))
            {
                ccode = StoreCommandCOLLECTIONS(client, &coll);
            }
            break;

        case STORE_COMMAND_CONVERSATIONS:
            /* CONVERSATIONS [Ssource] [Rxx-xx] [Q<query>] [P<proplist>] 
                             [C<center document>] [T] [F<flags>] [M<flags mask>]
                             [H<headerlist>]
            */

            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 10)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client)))
            {
                break;
            }

            filename = NULL; /* source */
            query = NULL;
            int1 = int2 = -1; /* range */
            int3 = 0; /* propcount */
            int4 = 0; /* display total */
            guid = 0; /* center document */
            ulong = 0; /* flags mask */
            ulong2 = 0; /* flags */
            hdrcnt = 0;
            for (i = 1; i < n; i++) {
                if ('S' == *tokens[i] && tokens[i][1] != '\0' && filename == NULL) { 
                    filename = tokens[i] + 1;
                } else if ('P' == *tokens[i] && 0 == int3) {
                    ccode = ParsePropList(client, tokens[i] + 1, 
                                          props, PROP_ARR_SZ, &int3, 0);
                } else if ('R' == *tokens[i] && int1 == -1) {
                    ccode = ParseRange(client, tokens[i] + 1, &int1, &int2);
                } else if ('Q' == *tokens[i]) {
                    query = tokens[i] + 1;
                } else if ('C' == *tokens[i] && !guid) {
                    ccode = ParseDocument(client, tokens[i] + 1, &guid);
                } else if ('T' == *tokens[i] && !int4 && !tokens[i][1]) {
                    int4 = 1;
                } else if ('M' == *tokens[i]) {
                    ccode = ParseUnsignedLong(client, tokens[i] + 1, &ulong);
                } else if ('F' == *tokens[i]) {
                    ccode = ParseUnsignedLong(client, tokens[i] + 1, &ulong2);
                } else if ('H' == *tokens[i]) {
                    ccode = ParseHeaderList(client, tokens[i] + 1, 
                                            hdrs, HDR_ARR_SZ, &hdrcnt);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    break;
                }
            }

            if (TOKEN_OK != ccode) {
                break;
            }
            
            ccode = StoreCommandCONVERSATIONS(client, filename, query, 
                                              int1, int2, guid, 
                                              (uint32_t) ulong, (uint32_t) ulong2, 
                                              int4,
                                              hdrs, hdrcnt,
                                              props, int3);
            break;

        case STORE_COMMAND_CONVERSATION:
            /* CONVERSATION <conversation> [P<proplist>] */

            guid = 0;
            int3 = 0; /* propcount */
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 2, 3)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client)) ||
                TOKEN_OK != (ccode = ParseDocument(client, tokens[1], &guid))) 
            {
                break;
            }
            
            for (i = 2; i < n; i++) {
                if ('P' == *tokens[i] && 0 == int3) {
                    ccode = ParsePropList(client, tokens[i] + 1, 
                                          props, PROP_ARR_SZ, &int3, 0);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    break;
                }
            }

            if (TOKEN_OK == ccode) {
                ccode = StoreCommandCONVERSATION(client, guid, props, int3);
            }
            break;

        case STORE_COMMAND_COOKIE:
            /* COOKIE [BAKE | NEW] <timeout> */
            /* COOKIE [CRUMBLE | DELETE] [<token>] */
            
            if (TOKEN_OK != (ccode = RequireUser(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 2, 3)))
            {
                break;
            }
            if (!XplStrCaseCmp(tokens[1], "BAKE") || 
                !XplStrCaseCmp(tokens[1], "NEW"))
            {
                if (TOKEN_OK != (ccode = CheckTokC(client, n, 3, 3)) ||
                    TOKEN_OK != (ccode = ParseDateTimeToUint64(client, tokens[2], 
                                                               &timestamp))) 
                {
                    break;
                }
                ccode = StoreCommandCOOKIENEW(client, timestamp); 
            } else if (!XplStrCaseCmp(tokens[1], "CRUMBLE") || 
                       !XplStrCaseCmp(tokens[1], "DELETE"))
            {
                ccode = StoreCommandCOOKIEDELETE(client, 3 == n ? tokens[2] : NULL);
            } else {
                ccode = ConnWriteStr(client->conn, MSG3000UNKNOWN);
            }
            break;

        case STORE_COMMAND_COPY:
            /* COPY <document> <collection> */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[2], &coll)))
            {
                ccode = StoreCommandCOPY(client, guid, &coll);
            }
            break;

        case STORE_COMMAND_CREATE:
            /* CREATE <collection name> [<guid>] */

            guid = 0;
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 3)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                (n <= 2 || TOKEN_OK == (ccode = ParseGUID(client, tokens[2], &guid))))
            {
                ccode = StoreCommandCREATE(client, tokens[1], guid);
            }
            break;

        case STORE_COMMAND_DELETE:
            /* DELETE <document> */
            /* PURGE <document> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 2)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)))
            {
                ccode = StoreCommandDELETE(client, guid);
            }
            break;

        case STORE_COMMAND_DELIVER:
            /* DELIVER FILE <type> <sender> <authsender> <filename> */
            /* DELIVER STREAM <type> <sender> <authsender> <count> */

            if (TOKEN_OK != (ccode = RequireManager(client)) ||
                TOKEN_OK != (ccode = RequireNoStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 6, 6)) ||
                TOKEN_OK != (ccode = ParseDocType(client, tokens[2], &doctype)))
            {
                break;
            }
            if (0 == XplStrCaseCmp(tokens[1], "FILE")) {
                ccode = StoreCommandDELIVER(client, tokens[3], tokens[4], 
                                            tokens[5], 0);
            } else if (0 == XplStrCaseCmp(tokens[1], "STREAM")) {
                if (TOKEN_OK != (ccode = ParseInt(client, tokens[5], &int1))) {
                    break;
                }
                ccode = StoreCommandDELIVER(client, tokens[3], tokens[4], NULL, int1);
            } else {
                ccode = ConnWriteStr(client->conn, MSG3000UNKNOWN);
            }
            break;

        case STORE_COMMAND_EVENTS:
            /* EVENTS [D<daterange>] [C<calendar> | U<uid>] 
               [F<mask>] [Q<query>] [P<proplist>] */

            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 6)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client)))
            {
                break;
            }

            dt_start[0] = '\0';
            int1 = 0; /* proplist len */
            int2 = 0; /* have coll */
            int3 = int4 = -1; /* range */
            int4 = -1;
            uid = NULL;
            query = NULL; /* uid */
            ulong = ULONG_MAX;
            for (i = 1; TOKEN_OK == ccode && i < n; i++) {
                if ('C' == *tokens[i] && !int2 && !query) {
                    ccode = ParseDocumentInfo(client, tokens[i] + 1, &coll);
                    int2++;
                } else if ('D' == *tokens[i] && !dt_start[0]) {
                    ccode = ParseDateRange(client, tokens[i] + 1, dt_start, dt_end);
                } else if ('F' == *tokens[i] && ULONG_MAX == ulong) {
                    ccode = ParseUnsignedLong(client, tokens[i] + 1, &ulong);
                } else if ('P' == *tokens[i] && 0 == int1) {
                    ccode = ParsePropList(client, tokens[i] + 1, 
                                          props, PROP_ARR_SZ, &int1, 0);
                } else if ('Q' == *tokens[i] && !query) {
                    query = tokens[i] + 1;
                } else if ('R' == *tokens[i] && -1 == int3) {
                    ccode = ParseRange(client, tokens[i] + 1, &int3, &int4);
                } else if ('U' == *tokens[i] && !uid && !int2) {
                    uid = tokens[i] + 1;
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
            }
                
            if (TOKEN_OK != ccode) {
                break;
            }

            if (!dt_start[0]) {
                strcpy(dt_start, "00000000T000000");
                strcpy(dt_end, "99999999T999999");
            }            
            ccode = StoreCommandEVENTS(client, dt_start, dt_end, 
                                       int2 ? &coll : 0, 
                                       ulong, uid,
                                       query, int3, int4,
                                       props, int1);
            break;

        case STORE_COMMAND_FLAG:
            /* FLAG <document> [[+ | -]<value>] */

            int2 = STORE_FLAG_SHOW;             
            guid = 0;
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 3)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)) &&
                (n < 3 || TOKEN_OK == (ccode = ParseFlag(client, tokens[2], &int1, &int2)))) 
                
            {
                ccode = StoreCommandFLAG(client, guid, int1, int2);
            }
            break;

        case STORE_COMMAND_INDEXED:
            /* INDEXED <value> <document> */

            if (TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = ParseInt(client, tokens[1], &int1)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[2], &guid)))
            {
                ccode = StoreCommandINDEXED(client, int1, guid);
            }

            break;

        case STORE_COMMAND_INFO:
            /* INFO <document> [P<proplist>] */
            
            guid = 0;
            int1 = 0; /* proplist len */
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 2, 3)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client)) ||
                TOKEN_OK != (ccode = ParseDocument(client, tokens[1], &guid)))
            {
                break;
            }
            if (n > 2) {
                if ('P' == *tokens[2]) {
                    ccode = ParsePropList(client, tokens[2] + 1, 
                                          props, PROP_ARR_SZ, &int1, 0);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
            }
            if (TOKEN_OK == ccode) {
                ccode = StoreCommandINFO(client, guid, props, int1);
            }
            break;

        case STORE_COMMAND_ISEARCH:
            /* ISEARCH R<range> <query> [P<proplist>] */

            int1 = 0; /* range */
            int2 = 0; /* range */
            int3 = 0; /* proplist len */
            
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 3, 4)))
            {
                break;
            }            
             
            if ('R' != *tokens[1]) {
                ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                break;
            } 
            ccode = ParseRange(client, tokens[1] + 1, &int1, &int2);

            for (i = 3; TOKEN_OK == ccode && i < n; i++) {
                if ('P' == *tokens[i] && 0 == int3) {
                    ccode = ParsePropList(client, tokens[i] + 1, 
                                          props, PROP_ARR_SZ, &int3, 0);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
            }
                
            if (TOKEN_OK != ccode) {
                break;
            }

            ccode = StoreCommandISEARCH(client, tokens[2], int1, int2,
                                        props, int3);
            break;

        case STORE_COMMAND_LINK:
            /* LINK <calendar> <document> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseCreateDocument(client, tokens[1], &guid,
                                                         STORE_DOCTYPE_CALENDAR)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[2], &guid2)))
            {
                ccode = StoreCommandLINK(client, guid, guid2);
            }
            break;

        case STORE_COMMAND_LIST:
            /* LIST <collection> [Rxx-xx] [P<proplist>] [M<flags mask>] [F<flags>] */

            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 2, 6)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client)) ||
                TOKEN_OK != (ccode = ParseCollection(client, tokens[1], &coll))) 
            {
                break;
            }

            int1 = int2 = -1; /* range */
            int3 = 0;   /* proplist len */
            ulong = 0;  /* flags mask */
            ulong2 = 0; /* flags */
            for (i = 2; TOKEN_OK == ccode && i < n; i++) {
                if ('R' == *tokens[i] && -1 == int1) {
                    ccode = ParseRange(client, tokens[i] + 1, &int1, &int2);
                } else if ('P' == *tokens[i] && 0 == int3) {
                    ccode = ParsePropList(client, tokens[i] + 1, 
                                          props, PROP_ARR_SZ, &int3, 0);
                } else if ('M' == *tokens[i]) {
                    ccode = ParseUnsignedLong(client, tokens[i] + 1, &ulong);
                } else if ('F' == *tokens[i]) {
                    ccode = ParseUnsignedLong(client, tokens[i] + 1, &ulong2);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
            }
                
            if (TOKEN_OK != ccode) {
                break;
            }
            
            ccode = StoreCommandLIST(client, &coll, int1, int2, 
                                     (uint32_t) ulong, (uint32_t) ulong2, 
                                     props, int3);
            break;            

        case STORE_COMMAND_MAILINGLISTS:
            /* MAILINGLISTS <source> */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 2)))
            {
                ccode = StoreCommandMAILINGLISTS(client, tokens[1]);
            }
            break;

        case STORE_COMMAND_MESSAGES:
            /* MESSAGES [Ssource] [H<headerlist>] [Q<query>] 
                        [F<flags>] [M<flags mask>]
                        [Rxx-xx] [C<center document>] [T] 
                        [P<proplist>] 
            */
            
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 10)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client)))
            {
                break;
            }

            filename = NULL; /* source */
            query = NULL;
            int1 = int2 = -1; /* range */
            int3 = 0; /* propcount */
            int4 = 0; /* display total */
            guid = 0; /* center document */
            ulong = 0; /* flags mask */
            ulong2 = 0; /* flags */
            hdrcnt = 0;
            for (i = 1; i < n; i++) {
                if ('S' == *tokens[i] && tokens[i][1] != '\0' && filename == NULL) { 
                    filename = tokens[i] + 1;
                } else if ('P' == *tokens[i] && 0 == int3) {
                    ccode = ParsePropList(client, tokens[i] + 1, 
                                          props, PROP_ARR_SZ, &int3, 0);
                } else if ('R' == *tokens[i] && int1 == -1) {
                    ccode = ParseRange(client, tokens[i] + 1, &int1, &int2);
                } else if ('Q' == *tokens[i]) {
                    query = tokens[i] + 1;
                } else if ('C' == *tokens[i] && !guid) {
                    ccode = ParseDocument(client, tokens[i] + 1, &guid);
                } else if ('T' == *tokens[i] && !int4 && !tokens[i][1]) {
                    int4 = 1;
                } else if ('M' == *tokens[i]) {
                    ccode = ParseUnsignedLong(client, tokens[i] + 1, &ulong);
                } else if ('F' == *tokens[i]) {
                    ccode = ParseUnsignedLong(client, tokens[i] + 1, &ulong2);
                } else if ('H' == *tokens[i]) {
                    ccode = ParseHeaderList(client, tokens[i] + 1, 
                                            hdrs, HDR_ARR_SZ, &hdrcnt);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    break;
                }
            }

            if (TOKEN_OK != ccode) {
                break;
            }
            
            ccode = StoreCommandMESSAGES(client, filename, query, 
                                         int1, int2, guid, 
                                         (uint32_t) ulong, (uint32_t) ulong2, 
                                         int4,
                                         hdrs, hdrcnt,
                                         props, int3);
            break;

        case STORE_COMMAND_MFLAG:
            /* MFLAG [+ | -] <value> */

            int2 = STORE_FLAG_SHOW; /* action */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 2)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseFlag(client, tokens[1], &int1, &int2)))
            {
                ccode = StoreCommandMFLAG(client, int1, int2);
            }
            break;

        case STORE_COMMAND_MIME:
            /* MIME <document> */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 2)) &&
                TOKEN_OK == (ccode = RequireSharedTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)))
            {
                ccode = StoreCommandMIME(client, guid);
            }
            break;

        case STORE_COMMAND_MOVE:
            /* MOVE <document> <collection> [<filename>] */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 4)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[2], &coll)))
            {
                ccode = StoreCommandMOVE(client, guid, &coll, 
                                         4 == n ? tokens[3] : NULL);
            }
            break;

        case STORE_COMMAND_MWRITE:
            /* MWRITE <collection> <type> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[1], &coll)) &&
                TOKEN_OK == (ccode = ParseDocType(client, tokens[2], &doctype)))
            {
                ccode = StoreCommandMWRITE(client, &coll, doctype);
            }
            break;

        case STORE_COMMAND_NOOP:
            /* NOOP */

            ccode = ConnWriteStr(client->conn, MSG1000OK);
            break;

        case STORE_COMMAND_PROPGET:
            /* PROPGET <document> [<proplist>] */
            
            int3 = 0; /* propcount */
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 3)) &&
                TOKEN_OK == (ccode = RequireSharedTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)) &&
                (2 == n || 
                 TOKEN_OK == (ccode = ParsePropList(client, tokens[2], 
                                                    props, PROP_ARR_SZ, &int3, 0))))
            {
                ccode = StoreCommandPROPGET(client, guid, props, int3);
            }
            break;

        case STORE_COMMAND_PROPSET:
            /* PROPSET <document> <propname> <length> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 4, 4)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)) &&
                TOKEN_OK == (ccode = ParsePropName(client, tokens[2], &prop)) &&
                TOKEN_OK == (ccode = ParseInt(client, tokens[3], &int1)))
                
            {
                prop.valueLen = int1;
                ccode = StoreCommandPROPSET(client, guid, &prop);
            }
            break;

        case STORE_COMMAND_QUIT:
            /* QUIT */

            ccode = StoreCommandQUIT(client);
            break;

        case STORE_COMMAND_READ:
            /* READ <document> [<start> [<length>]] */

            int1 = -1;
            int2 = -1;
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 4)) &&
                TOKEN_OK == (ccode = RequireSharedTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)) &&
                (n < 3 || TOKEN_OK == (ccode = ParseInt(client, tokens[2], &int1))) &&
                (n < 4 || TOKEN_OK == (ccode = ParseInt(client, tokens[3], &int2))))
            {
                ccode = StoreCommandREAD(client, guid, int1, int2);
            }
            break;

        case STORE_COMMAND_REINDEX:
            /* REINDEX [<document>] */
            
            guid = 0;
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 1, 2)) &&
                TOKEN_OK == (ccode = RequireSharedTransaction(client)) &&
                (n < 2 || 
                 TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid))))
            {
                ccode = StoreCommandREINDEX(client, guid);
            }
            break;

        case STORE_COMMAND_RENAME:
            /* RENAME <collection> <new name> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[1], &coll)))
            {
                ccode = StoreCommandRENAME(client, &coll, tokens[2]);
            }
            break;

        case STORE_COMMAND_REMOVE:
            /* REMOVE <collection> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 2)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[1], &coll)))
            {
                ccode = StoreCommandREMOVE(client, &coll);
            }
            break;


        case STORE_COMMAND_REPLACE:
            /* REPLACE <document> <length> [R<xx-xx>] [V<version>] 
                       [L<link document>]*/

            int2 = -1; /* range start */
            int3 = -1; /* range end */
            int4 = -1; /* version */
            guid2 = 0;
            
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 3, 6)) ||
                TOKEN_OK != (ccode = RequireExclusiveTransaction(client)) ||
                TOKEN_OK != (ccode = ParseDocument(client, tokens[1], &guid)) ||
                TOKEN_OK != (ccode = ParseStreamLength(client, tokens[2], &int1)))
            {
                break;
            }

            for (i = 3; TOKEN_OK == ccode && i < n; i++) {
                if ('R' == *tokens[i] && -1 == int2) {
                    ccode = ParseRange(client, tokens[i] + 1 , &int2, &int3);
                } else if ('V' == *tokens[i] && -1 == int4) {
                    ccode = ParseInt(client, tokens[i] + 1, &int4);
                } else if ('L' == *tokens[i] && !guid2) {
                    ccode = ParseDocument(client, 1 + tokens[i], &guid2);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
            }
            if (TOKEN_OK == ccode) {
                ccode = StoreCommandWRITE(client, 
                                          NULL, 0, 
                                          int1, int2, int3, 
                                          0, guid, NULL, 
                                          0, int4, guid2);
            }
            break;
            
        case STORE_COMMAND_RESET:
            /* RESET */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 1, 1)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)))
            {
                ccode = StoreCommandRESET(client); 
            }
            break;

        case STORE_COMMAND_SEARCH:
            /* SEARCH <document or collection> TEXT <query>
               SEARCH <document or collection> BODY <query>
               SEARCH <document or collection> HEADER <header> <query>
               SEARCH <document or collection> HEADERS <query>
            */

            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 4, 5)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client)) ||
                TOKEN_OK != (ccode = ParseDocument(client, tokens[1], &guid)))
            {
                break;
            }
            
            {
                StoreSearchInfo search;
                
                if (0 == XplStrCaseCmp(tokens[2], "TEXT")) {
                    search.type = STORE_SEARCH_TEXT;
                    search.query = tokens[3];
                } else if (0 == XplStrCaseCmp(tokens[2], "BODY")) {
                    search.type = STORE_SEARCH_BODY;
                    search.query = tokens[3];
                } else if (0 == XplStrCaseCmp(tokens[2], "HEADER")) {
                    if (TOKEN_OK != (ccode = CheckTokC(client, n, 5, 5))) {
                        break;
                    }
                    search.type = STORE_SEARCH_HEADER;
                    search.header = tokens[3];
                    search.query = tokens[4];
                } else if (0 == XplStrCaseCmp(tokens[2], "HEADERS")) {
                    search.type = STORE_SEARCH_HEADERS;
                    search.query = tokens[3];
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3000UNKNOWN);
                    break;
                }

                ccode = StoreCommandSEARCH(client, guid, &search);
            }
            break;

        case STORE_COMMAND_STORE:
            /* STORE [<storename>] */

            if (TOKEN_OK != (ccode = CheckTokC(client, n, 1, 2)) ||
                TOKEN_OK != (ccode = RequireIdentity(client)))
            {
                break;
            }
             
            if (2 == n) {
                if (TOKEN_OK != (ccode = ParseStoreName(client, tokens[1]))) {
                    break;
                }
            } else {
                tokens[1] = NULL;
            }

            ccode = StoreCommandSTORE(client, tokens[1]);
            break;

        case STORE_COMMAND_TIMEOUT:
            /* TIMEOUT [<timeoutms>] */
            
            int1 = -1;

            if (TOKEN_OK == (ccode = CheckTokC(client, n, 1, 2)) &&
                (1 == n || TOKEN_OK == (ccode = ParseInt(client, tokens[1], &int1))))
            {
                ccode = StoreCommandTIMEOUT(client, int1);
            }
            break;
            
        case STORE_COMMAND_TOKEN:
            /* TOKEN <token> */

            if (TOKEN_OK == (ccode = CheckTokC(client, n, 2, 2)) &&
                TOKEN_OK == (ccode = RequireIdentity(client)))
            {
                ccode = StoreCommandTOKEN(client, tokens[1]);
            }
            break;
            
        case STORE_COMMAND_TOKENS:
            /* TOKENS */

            if (TOKEN_OK == (ccode = CheckTokC(client, n, 1, 1)) &&
                TOKEN_OK == (ccode = RequireIdentity(client)))
            {
                ccode = StoreCommandTOKENS(client);
            }
            break;
            
        case STORE_COMMAND_UNLINK:
            /* UNLINK <calendar> <document> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = RequireExclusiveTransaction(client)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &guid)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[2], &guid2)))
            {
                ccode = StoreCommandUNLINK(client, guid, guid2);
            }
            break;

        case STORE_COMMAND_USER:
            /* USER [<username>] */

            if (TOKEN_OK != (ccode = RequireManager(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 2))) 
            {
                break;
            }
            if (2 == n) {
                if (TOKEN_OK != (ccode = ParseUserName(client, tokens[1]))) {
                    break;
                }
            } else {
                tokens[1] = NULL;
            }

            ccode = StoreCommandUSER(client, tokens[1], NULL, 0);
            break;

        case STORE_COMMAND_WATCH:
            /* WATCH <collection> [DELETED] [FLAGS] [MODIFIED] [NEW] */

            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 2, 6)) ||
                TOKEN_OK != (ccode = RequireSharedTransaction(client)) ||
                TOKEN_OK != (ccode = ParseCollection(client, tokens[1], &coll)))
            {
                break;
            }
                
            int1 = 0;
            for (i = 2; i < n; i++) {
                if (!XplStrCaseCmp("FLAGS", tokens[i])) {
                    int1 |= STORE_WATCH_EVENT_FLAGS;
                } else if (!XplStrCaseCmp("MODIFIED", tokens[i])) {
                    int1 |= STORE_WATCH_EVENT_MODIFIED;
                } else if (!XplStrCaseCmp("NEW", tokens[i])) {
                    int1 |= STORE_WATCH_EVENT_NEW;
                } else if (!XplStrCaseCmp("DELETED", tokens[i])) {
                    int1 |= STORE_WATCH_EVENT_DELETED;
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    break;
                }
            }
            
            if (TOKEN_OK == ccode) {
                ccode = StoreCommandWATCH(client, &coll, int1);
            }
            break;

        case STORE_COMMAND_WRITE:
            /* WRITE <collection> <type> <length> 
                     [F<filename>] [G<guid>] [T<timeCreated>] 
                     [Z<flags>] [L<link guid>]
             */
            
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 4, 9)) ||
                TOKEN_OK != (ccode = RequireExclusiveTransaction(client)) ||
                TOKEN_OK != (ccode = ParseCollection(client, tokens[1], &coll)) ||
                TOKEN_OK != (ccode = ParseDocType(client, tokens[2], &doctype)) ||
                TOKEN_OK != (ccode = ParseStreamLength(client, tokens[3], &int1)))
            {
                break;
            }

            filename = NULL;
            timestamp = 0;
            ulong = 0; /* addflags */
            guid = 0;
            guid2 = 0; /* link guid */

            for (i = 4; i < n; i++) {
                if ('G' == *tokens[i] && !guid) {
                    ccode = ParseGUID(client, 1 + tokens[i], &guid);
                } else if ('L' == *tokens[i] && !guid2) {
                    ccode = ParseDocument(client, 1 + tokens[i], &guid2);
                } else if ('F' == *tokens[i] && tokens[i][1] != '\0') {
                    filename = tokens[i] + 1;
                } else if ('T' == *tokens[i] && !timestamp) {
                    ccode = ParseDateTimeToUint64(client, 1 + tokens[i], &timestamp); 
                } else if ('Z' == *tokens[i] && !ulong) {
                    ccode = ParseUnsignedLong(client, tokens[i] + 1, &ulong);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
                if (TOKEN_OK != ccode) {
                    break;
                }
            }
            if (TOKEN_OK != ccode) {
                break;
            }

            ccode = StoreCommandWRITE(client, 
                                      &coll, doctype, 
                                      int1, -1, -1,
                                      (uint32_t) ulong, guid, filename,
                                      timestamp, -1, guid2);
            break;

        default:
            break;
        }

        if (client->watchLock) {
            StoreReleaseSharedLock(client, client->watchLock);
            client->watchLock = NULL;
        }

        if (client->handle && DStoreCancelTransactions(client->handle)) {
            ConnWriteStr(client->conn, MSG5920FATALDBLIBERR);
            ConnFlush(client->conn);
            ccode = -1;
        }

        if (ccode >= 0) {
            ccode = ConnFlush(client->conn);
        }
    }
    
    return ccode;
}


static CCode
StoreShowFolder(StoreClient *client, int64_t coll, int typemask)
{
    DStoreStmt *stmt;
    DStoreDocInfo info;
    int ccode;

    stmt = DStoreFindDocInfo(client->handle, coll, NULL, NULL);
    if (!stmt) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        return -1;
    }
    
    while (1) {
        ccode = DStoreInfoStmtStep(client->handle, stmt, &info);
        if (ccode > 0) {
            ccode = ConnWriteF(client->conn, "2001 " GUID_FMT " %d\r\n", 
                               info.guid, info.version);
        } else if (ccode < 0) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            break;
        } else {
            ccode = ConnWriteStr(client->conn, MSG1000OK);
            break;
        }
    };

    DStoreStmtEnd(client->handle, stmt);
    return ccode;
}


static CCode
ShowDocumentBody(StoreClient *client, DStoreDocInfo *info,
                 int64_t requestStart, int64_t requestLength)
{
    int ccode = 0;
    char path[XPL_MAX_PATH + 1];
    FILE *fh = NULL;
    uint64_t start;
    uint64_t length;

    if (STORE_IS_FOLDER(info->type)) {
        return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
    } else {
        FindPathToDocFile(client, info->collection, path, sizeof(path));
        fh = fopen(path, "rb");
        if (!fh) {
            return ConnWriteStr(client->conn, MSG4224CANTREAD);
        }

        if (requestStart < 0) {
            start = 0;
            length = info->bodylen;
        } else {
            if (requestStart < info->bodylen) {
                start = requestStart;
                length = info->bodylen - start;
                if ((requestLength > -1) && ((unsigned long)requestLength < length)) {
                    length = requestLength; 
                }
            } else {
                start = info->bodylen;
                length = 0;
            }
        }

        ccode = ConnWriteF(client->conn, 
                           "2001 nmap.document %ld\r\n", (long) length);
        if (-1 == ccode) {
            goto finish;
        }
        if (0 != XplFSeek64(fh, info->start + info->headerlen + start, SEEK_SET)) {
            ccode = ConnWriteStr(client->conn, MSG4224CANTREAD);
            goto finish;
        }
        ccode = ConnWriteFromFile(client->conn, fh, length);
        if (-1 == ccode) {
            goto finish;
        }
        ccode = ConnWriteStr(client->conn, "\r\n");
    }
    
finish:
    if (fh) {
        fclose(fh);
    }

    return ccode;
}


static CCode
ShowInternalProperty(StoreClient *client,
                     StorePropertyType prop,
                     DStoreDocInfo *info)
{
    struct tm tm;
    time_t tt;
    char buf[64];
    int len;

    switch (prop) {
    case STORE_PROP_COLLECTION:
        return ConnWriteF(client->conn, 
                          "2001 nmap.collection 16\r\n" GUID_FMT "\r\n", 
                          info->collection);
    case STORE_PROP_DOCUMENT:
        if (STORE_IS_FOLDER(info->type)) {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.document");
        } else {
            return ShowDocumentBody(client, info, -1, -1);
        }
    case STORE_PROP_GUID:
        return ConnWriteF(client->conn, "2001 nmap.guid 16\r\n" GUID_FMT "\r\n", 
                          info->guid);
    case STORE_PROP_INDEX:
        len = snprintf(buf, 64, "%d", info->indexed);
        return ConnWriteF(client->conn, "2001 nmap.index %d\r\n%s\r\n", len, buf);
    case STORE_PROP_FLAGS:
        len = snprintf(buf, 64, "%lu", (unsigned long) info->flags);
        return ConnWriteF(client->conn, "2001 nmap.flags %d\r\n%s\r\n", len, buf);
    case STORE_PROP_LENGTH:
        len = snprintf(buf, 64, "%lld", info->bodylen);
        return ConnWriteF(client->conn, "2001 nmap.length %d\r\n%s\r\n", len, buf);
    case STORE_PROP_TYPE:
        len = snprintf(buf, 64, "%d", info->type);
        return ConnWriteF(client->conn, "2001 nmap.type %d\r\n%s\r\n", len, buf);
    case STORE_PROP_CREATED:
        tt = info->timeCreatedUTC;
        gmtime_r(&tt, &tm);
        len = strftime(buf, 64, "%Y-%m-%d %H:%M:%SZ", &tm);
        return ConnWriteF(client->conn, "2001 nmap.created %d\r\n%s\r\n", len, buf);
    case STORE_PROP_LASTMODIFIED:
        tt = info->timeModifiedUTC;
        gmtime_r(&tt, &tm);
        len = strftime(buf, 64, "%Y-%m-%d %H:%M:%SZ", &tm);
        return ConnWriteF(client->conn, "2001 nmap.lastmodified %d\r\n%s\r\n", 
                          len, buf);
    case STORE_PROP_VERSION:
        len = snprintf(buf, 64, "%d", info->version);
        return ConnWriteF(client->conn, "2001 nmap.version %d\r\n%s\r\n", len, buf);
    case STORE_PROP_EVENT_CALENDARS:
    {
        CCode ccode;
        BongoArray guidlist;
        unsigned int i;

        BongoArrayInit(&guidlist, sizeof(uint64_t), 64);
        
        if (DStoreFindCalendars(client->handle, info->guid, &guidlist)) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        } else {
            ccode = ConnWriteF(client->conn, "2001 nmap.event.calendars %d\r\n",
                               guidlist.len * 17);
            for (i = 0; i < guidlist.len && -1 != ccode; i++) {
                ccode = ConnWriteF(client->conn, GUID_FMT "\n", 
                                   BongoArrayIndex(&guidlist, uint64_t, i));
            }
            if (-1 != ccode) {
                ccode = ConnWriteStr(client->conn, "\r\n");
            }
        }
        BongoArrayDestroy(&guidlist, TRUE);
        return ccode;
    }
    case STORE_PROP_EVENT_END:
        if (STORE_DOCTYPE_EVENT == info->type) {
            return ConnWriteF(client->conn, "2001 nmap.event.end %d\r\n%s\r\n", 
                              strlen(info->data.event.end), 
                              info->data.event.end);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.event.end");
        }

    case STORE_PROP_EVENT_LOCATION:
        if (STORE_DOCTYPE_EVENT == info->type && info->data.event.location) {
            return ConnWriteF(client->conn, "2001 nmap.event.location %d\r\n%s\r\n", 
                              strlen(info->data.event.location), 
                              info->data.event.location);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.event.location");
        }
    case STORE_PROP_EVENT_START:
        if (STORE_DOCTYPE_EVENT == info->type) {
            return ConnWriteF(client->conn, "2001 nmap.event.start %d\r\n%s\r\n", 
                              strlen(info->data.event.start), 
                              info->data.event.start);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.event.start");
        }

    case STORE_PROP_EVENT_SUMMARY:
        if (STORE_DOCTYPE_EVENT == info->type && info->data.event.summary) {
            return ConnWriteF(client->conn, "2001 nmap.event.summary %d\r\n%s\r\n", 
                              strlen(info->data.event.summary), 
                              info->data.event.summary);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.event.summary");
        }
    case STORE_PROP_EVENT_UID:
        if (STORE_DOCTYPE_EVENT == info->type && info->data.event.uid) {
            return ConnWriteF(client->conn, "2001 nmap.event.uid %d\r\n%s\r\n", 
                              strlen(info->data.event.uid), 
                              info->data.event.uid);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.event.uid");
        }
    case STORE_PROP_EVENT_STAMP:
        if (STORE_DOCTYPE_EVENT == info->type && info->data.event.stamp) {
            return ConnWriteF(client->conn, "2001 nmap.event.stamp %d\r\n%s\r\n", 
                              strlen(info->data.event.stamp), 
                              info->data.event.stamp);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.event.stamp");
        }
    case STORE_PROP_MAIL_CONVERSATION:
        if (info->type == STORE_DOCTYPE_MAIL) {
            return ConnWriteF(client->conn, 
                              "2001 nmap.mail.conversation 16\r\n" GUID_FMT "\r\n", 
                              info->conversation);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.mail.conversation");
        }
    case STORE_PROP_MAIL_HEADERLEN:
        if (info->type == STORE_DOCTYPE_MAIL) {
            len = snprintf(buf, 64, "%d", info->data.mail.headerSize);
            return ConnWriteF(client->conn, "2001 nmap.mail.headersize %d\r\n%s\r\n",
                              len, buf);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.mail.headersize");
        }
    case STORE_PROP_MAIL_IMAPUID:
        if (STORE_DOCTYPE_MAIL != info->type && !STORE_IS_FOLDER(info->type)) {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.mail.imapuid");
        }
        return ConnWriteF(client->conn, 
                          "2001 nmap.mail.imapuid 8\r\n%08x\r\n",
                          info->imapuid ? info->imapuid : (uint32_t) info->guid);
    case STORE_PROP_MAIL_SENT:
        if (info->type == STORE_DOCTYPE_MAIL) {
            tt = info->data.mail.sent;
            gmtime_r(&tt, &tm);
            len = strftime(buf, 64, "%Y-%m-%d %H:%M:%SZ", &tm);
            return ConnWriteF(client->conn, "2001 nmap.mail.sent %d\r\n%s\r\n", 
                              len, buf);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.mail.sent");
        }
    case STORE_PROP_MAIL_SUBJECT:
        if (info->type == STORE_DOCTYPE_MAIL && info->data.mail.subject) {
            return ConnWriteF(client->conn, "2001 nmap.mail.subject %d\r\n%s\r\n", 
                              strlen(info->data.mail.subject), 
                              info->data.mail.subject);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.mail.subject");
        }
    case STORE_PROP_MAIL_MESSAGEID:
        if (info->type == STORE_DOCTYPE_MAIL && info->data.mail.messageId) {
            return ConnWriteF(client->conn, "2001 nmap.mail.messageid %d\r\n%s\r\n", 
                              strlen(info->data.mail.messageId), info->data.mail.messageId);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.mail.messageid");
        }
    case STORE_PROP_MAIL_PARENTMESSAGEID:
        if (info->type == STORE_DOCTYPE_MAIL && info->data.mail.parentMessageId) {
            return ConnWriteF(client->conn, 
                              "2001 nmap.mail.parentmessageid %d\r\n%s\r\n", 
                              strlen(info->data.mail.parentMessageId), 
                              info->data.mail.parentMessageId);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, 
                              "nmap.mail.parentmessageid");
        }
    case STORE_PROP_CONVERSATION_SUBJECT:
        if (info->type == STORE_DOCTYPE_CONVERSATION && 
            info->data.conversation.subject) 
        {
            return ConnWriteF(client->conn, 
                              "2001 nmap.conversation.subject %d\r\n%s\r\n", 
                              strlen(info->data.conversation.subject), 
                              info->data.conversation.subject);
        } else {
            return ConnWriteF(client->conn, 
                              MSG3245NOPROP, "nmap.conversation.subject");
        }        
    case STORE_PROP_CONVERSATION_COUNT:
        if (info->type == STORE_DOCTYPE_CONVERSATION) {
            len = snprintf(buf, 64, "%d", info->data.conversation.numDocs);
            return ConnWriteF(client->conn, 
                              "2001 nmap.conversation.count %d\r\n%s\r\n", len, buf);
        } else {
            return ConnWriteF(client->conn, 
                              MSG3245NOPROP, "nmap.conversation.count");
        }        
    case STORE_PROP_CONVERSATION_UNREAD:
        if (info->type == STORE_DOCTYPE_CONVERSATION) {
            len = snprintf(buf, 64, "%d", info->data.conversation.numUnread);
            return ConnWriteF(client->conn, 
                              "2001 nmap.conversation.unread %d\r\n%s\r\n", len, buf);
        } else {
            return ConnWriteF(client->conn, 
                              MSG3245NOPROP, "nmap.conversation.unread");
        }        
    case STORE_PROP_CONVERSATION_DATE:
        if (info->type == STORE_DOCTYPE_CONVERSATION) {
            tt = info->data.conversation.lastMessageDate;
            gmtime_r(&tt, &tm);
            len = strftime(buf, 64, "%Y-%m-%d %H:%M:%SZ", &tm);
            return ConnWriteF(client->conn, 
                              "2001 nmap.conversation.date %d\r\n%s\r\n", len, buf);
        } else {
            return ConnWriteF(client->conn, MSG3245NOPROP, "nmap.conversation.date");
        } 
    case STORE_PROP_EXTERNAL:
    default:
        assert(0);
        return ConnWriteStr(client->conn, MSG5004INTERNALERR);
    }
}


static CCode
ShowExternalProperty(StoreClient *client, StorePropInfo *prop)
{
    /* FIXME - implement PRIV_READ_ACL and PRIV_READ_OWN_ACL? (bug 174097) */
    
    CCode ccode;
    int len = strlen(prop->value);

    ccode = ConnWriteF(client->conn, "2001 %s %d\r\n", prop->name, len);
    if (ccode < 0) {
        return -1;
    }
    ccode = ConnWrite(client->conn, prop->value, len);
    if (ccode < 0) {
        return -1;
    }
    return ConnWriteStr(client->conn, "\r\n");
}


static CCode
ShowProperty(StoreClient *client, DStoreDocInfo *info, StorePropInfo *prop)
{
    CCode ccode;
    int dcode;
    DStoreStmt *stmt;

    if (STORE_IS_EXTERNAL_PROPERTY_TYPE(prop->type)) {
        char *propname = prop->name;

        stmt = DStoreFindProperties(client->handle, info->guid, prop->name);
        if (!stmt) {
            return ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }
        dcode = DStorePropStmtStep(client->handle, stmt, prop);
        if (1 == dcode) {
            ccode = ShowExternalProperty(client, prop);
        } else if (0 == dcode) {
            ccode = ConnWriteF(client->conn, MSG3245NOPROP, propname);
        } else {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }
        DStoreStmtEnd(client->handle, stmt);
        prop->name = propname;
    } else {
        ccode = ShowInternalProperty(client, prop->type, info);
    }
    return ccode;
}


static CCode
ShowDocumentInfo(StoreClient *client, DStoreDocInfo *info,
                 StorePropInfo *props, int propcount)
{
    CCode ccode;
    char *filename;
    int i;
    char buffer[2 * sizeof(info->filename) + 1];
    char *p;

    ccode = StoreCheckAuthorizationQuiet(client, info, STORE_PRIV_READ_PROPS);
    if (ccode) {
        return ccode;
    }

    if (STORE_IS_FOLDER(info->type)) {
        filename = info->filename;
    } else {
        filename = strrchr(info->filename, '/');
        if (filename) {
            filename++;
        } else {
            XplConsolePrintf("Invalid path for document " GUID_FMT ": '%s'.\r\n",
                             info->guid, info->filename);
            filename = info->filename;
        }
    }

    for (p = buffer; *filename; ++filename, ++p) {
        if (isspace(*filename)) {
            *p++ = '\\';
        }
        *p = *filename;
    }
    *p = 0;

    ccode = ConnWriteF(client->conn, 
                       "2001 " GUID_FMT " %d %d %08x %d %lld %s\r\n", 
                       info->guid, info->type, info->flags, 
                       info->imapuid ? info->imapuid : (uint32_t) info->guid, 
                       info->timeCreatedUTC, info->bodylen,
                       buffer);

    for (i = 0; i < propcount && ccode >= 0; i++) {
        ccode = ShowProperty(client, info, &props[i]);
    }

    return ccode;
}


static CCode
ShowStmtDocumentsWithMessage(StoreClient *client, DStoreStmt *stmt, 
                             StorePropInfo *props, int propcount,
                             const char *msg)
{
    int ccode = 0;
    int dcode;

    DStoreDocInfo info;

    while (-1 != ccode) {
        dcode = DStoreInfoStmtStep(client->handle, stmt, &info);
        if (1 == dcode) {
            ccode = ShowDocumentInfo(client, &info, props, propcount);
        } else if (0 == dcode) {
            ccode = ConnWriteStr(client->conn, msg);
            break;
        } else if (-1 == dcode) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            break;
        }
    }

    return ccode;    
}


static CCode
ShowStmtDocuments(StoreClient *client, DStoreStmt *stmt, 
                  StorePropInfo *props, int propcount)
{
    return ShowStmtDocumentsWithMessage(client, stmt, props, propcount, MSG1000OK);
}


static CCode
ShowGuidsDocumentsWithMessage(StoreClient *client, uint64_t *guids, 
                              InfoStmtFilter *filter, void *userdata,
                              StorePropInfo *props, int propcount,
                              const char *msg)
{
    CCode ccode = 0;
    int dcode;

    DStoreDocInfo info;

    while (*guids && -1 != ccode) {
        dcode = DStoreGetDocInfoGuid(client->handle, *guids, &info);
        if (1 == dcode) {
            if (!filter || filter(&info, userdata)) {
                ccode = ShowDocumentInfo(client, &info, props, propcount);
            }
        } else if (0 == dcode) {
            /* */
        } else if (-1 == dcode) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            break;
        }
        ++guids;
    }
    
    if (-1 != ccode) {
        ccode = ConnWriteStr(client->conn, msg);
    }
    return ccode;    
}


static
CCode
StoreListCollections(StoreClient *client, DStoreDocInfo *coll)
{
    /* FIXME: instead of using the db, it might be better just to look 
       at the dat files to get the guids? */

    uint64_t *glist;
    int glist_size = 32;
    int n = 0;
    CCode ccode = 0;
    DStoreStmt *stmt = NULL;

    ccode = StoreCheckAuthorization(client, coll, STORE_PRIV_LIST);
    if (ccode) {
        return ccode;
    }

    glist = MemMalloc(glist_size * sizeof(uint64_t));
    if (!glist) {
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }
    glist[n++] = coll->guid;

    while (n) {
        uint64_t guid;
        DStoreDocInfo info;
        int dcode;

        guid = glist[--n];

        stmt = DStoreListColl(client->handle, guid, -1, -1);
        if (!stmt) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto abort;
        }
        
        while (-1 != ccode) {
            dcode = DStoreInfoStmtStep(client->handle, stmt, &info);
            if (1 == dcode && STORE_IS_FOLDER(info.type)) {
                ccode = ConnWriteF(client->conn, "2001 " GUID_FMT " %d %d %s\r\n", 
                                   info.guid, info.type, info.flags, info.filename);
                glist[n++] = info.guid;
                if (n >= glist_size) {
                    /* grow glist */
                    uint64_t *tmp;
                    glist_size *= 2;
                    tmp = MemRealloc(glist, glist_size * sizeof(uint64_t));
                    if (!tmp) {
                        ccode = ConnWriteStr(client->conn, MSG5001NOMEMORY);
                        goto abort;
                    }
                    glist = tmp;
                }

            } else if (0 == dcode) {
                break;
            } else if (-1 == dcode) {
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            }
        }
    
        DStoreStmtEnd(client->handle, stmt);
    }

    MemFree(glist);

    return ConnWriteStr(client->conn, MSG1000OK);

abort:
    if (glist) {
        MemFree(glist);
    }
    if (stmt) {
        DStoreStmtEnd(client->handle, stmt);
    }

    return ccode;
}


CCode
StoreCommandALARMS(StoreClient *client, uint64_t start, uint64_t end)
{
    AlarmInfoStmt *stmt;
    AlarmInfo info;
    CCode ccode = 0;

    stmt = AlarmDBListAlarms(client->system.alarmdb, start, end);
    if (!stmt) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }
    do {
        switch (AlarmInfoStmtStep(client->system.alarmdb, stmt, &info)) {
        case 1:
            ccode = ConnWriteF(client->conn, 
                               "2001 " GUID_FMT " %lld\r\n"
                               "2001 nmap.alarm.summary %d\r\n%s\r\n",
                               info.guid, BongoCalTimeUtcAsUint64(info.time),
                               strlen(info.summary), info.summary);
            if (-1 == ccode) {
                break;
            }

            if (info.email) {
                ccode = ConnWriteF(client->conn, 
                                   "2001 nmap.alarm.email %d\r\n%s\r\n",
                                   strlen(info.email), info.email);
            } else {
                ccode = ConnWriteF(client->conn, MSG3245NOPROP, "nmap.alarm.email");
            }
            if (-1 == ccode) {
                break;
            }

            if (info.sms) {
                ccode = ConnWriteF(client->conn, 
                                   "2001 nmap.alarm.sms %d\r\n%s\r\n",
                                   strlen(info.email), info.email);
            } else {
                ccode = ConnWriteF(client->conn, MSG3245NOPROP, "nmap.alarm.sms");
            }
            break;
        case 0:
            ccode = ConnWriteStr(client->conn, MSG1000OK);
            goto finish;
        case -1:
        default:
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }
    } while (ccode >= 0);

finish:
    AlarmStmtEnd(client->system.alarmdb, stmt);

    return ccode;
}


CCode
StoreCommandCALENDARS(StoreClient *client, unsigned long mask,
                      StorePropInfo *props, int propcount)
{
    CCode ccode = 0;
    DStoreStmt *stmt;
    int dcode = 1;
    DStoreDocInfo info;
    
    ccode = StoreCheckAuthorizationGuid(client, 
                                        STORE_CALENDARS_GUID, STORE_PRIV_LIST);
    if (ccode) {
        return ccode;
    }

    stmt = DStoreListColl(client->handle, STORE_CALENDARS_GUID, -1, -1);
    if (!stmt) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    while (-1 != ccode && 1 == dcode) {
        dcode = DStoreInfoStmtStep(client->handle, stmt, &info);
        if (1 == dcode) {
            if ((info.flags & mask) == info.flags) {
                ccode = ShowDocumentInfo(client, &info, props, propcount);
            }
        } else if (0 == dcode) {
            ccode = ConnWriteStr(client->conn, MSG1000OK);
        } else if (-1 == dcode) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }
    }
    
    DStoreStmtEnd(client->handle, stmt);
    return ccode;
}


CCode
StoreCommandCOLLECTIONS(StoreClient *client, DStoreDocInfo *coll)
{
    return StoreListCollections(client, coll);
}            


CCode
StoreCommandCONVERSATION(StoreClient *client, uint64_t guid,
                         StorePropInfo *props, int propcount)
{
    DStoreStmt *stmt;
    CCode ccode = 0;

    ccode = StoreCheckAuthorizationGuid(client, 
                                        STORE_CONVERSATIONS_GUID, STORE_PRIV_READ);
    if (ccode) {
        return ccode;
    }

    stmt = DStoreFindConversationMembers(client->handle, guid);
    if (!stmt) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        return -1;
    }

    if (!stmt) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    ccode = ShowStmtDocuments(client, stmt, props, propcount);

    DStoreStmtEnd(client->handle, stmt);
    return ccode;
}


typedef struct {
    uint32_t mask;
    uint32_t flags;
} MaskAndFlagStruct;


static int
MaskFilter(DStoreDocInfo *info, void *maskptr)
{
    MaskAndFlagStruct *mf = (MaskAndFlagStruct *) maskptr;

    return (info->flags & mf->mask) == mf->flags;
}


CCode
StoreCommandCONVERSATIONS(StoreClient *client, const char *source, const char *query, 
                          int start, int end, uint64_t center, 
                          uint32_t flagsmask, uint32_t flags,
                          int displayTotal,
                          StoreHeaderInfo *headers, int headercount, 
                          StorePropInfo *props, int propcount)
{
    CCode ccode = 0;
    DStoreStmt *stmt = NULL;
    uint64_t *guids = NULL;
    uint64_t *guidsptr = NULL;
    char buf[200];
    int total;
    MaskAndFlagStruct mf = { flagsmask, flags };
    uint32_t required;

    ccode = StoreCheckAuthorizationGuid(client, 
                                        STORE_CONVERSATIONS_GUID, STORE_PRIV_READ);
    if (ccode) {
        return ccode;
    }

    GetConversationSourceMask(source, &required);

    if (query) {
        IndexHandle *index;
        char newQuery[CONN_BUFSIZE * 2];
        int i;
        DStoreDocInfo *info;

        snprintf(newQuery, sizeof(newQuery) - 1, "(nmap.type:mail) AND (%s)", query);

        index = IndexOpen(client);
        if (!index) {
            return ConnWriteStr(client->conn, MSG4120INDEXLOCKED);
        }
        guids = IndexSearch(index, newQuery, TRUE, TRUE, 
                            -1, -1, NULL, &total, INDEX_SORT_DATE_DESC);
        IndexClose(index);

        if (!guids) {
            return ConnWriteStr(client->conn, MSG5007INDEXLIBERR);
        }
        
        /* FIXME: This is a big allocation */
        info = BONGO_MEMSTACK_ALLOC(&client->memstack, total * sizeof(DStoreDocInfo));

        for (guidsptr = guids, i = 0; *guidsptr; ++guidsptr) {
            switch (DStoreGetDocInfoGuid(client->handle, *guidsptr, &info[i])) {
            case 1:
                if ((flagsmask && !MaskFilter(&info[i], &mf)) ||
                    (required && !(info[i].data.conversation.sources & required)))
                {
                    --total;
                    continue;
                }
                i++;
                break;
            case 0:
                --total;
                continue;
            case -1:
                MemFree(guids);
                return ConnWriteStr(client->conn, MSG5005DBLIBERR);
            }
        }

        MemFree(guids);

        if (center) {
            /* fix up start and end */
            for (i = 0; i < total; i++) {
                if (info[i].guid == center) {
                    start = i - start;
                    if (start < 0) { 
                        start = 0;
                    }
                    end = i + end + 1;
                    if (end > total) {
                        end = total;
                    }
                    center = 0;
                    break;
                }
            }
            if (center) {
                start = end = 0;
            }
        }

        if (-1 == start) {
            start = 0;
            end = total;
        }

        for (i = start; i < end && i < total && -1 != ccode; i++) {
            ccode = ShowDocumentInfo(client, &info[i], props, propcount);
        }
        
        if (-1 != ccode) {
            if (displayTotal) {
                ccode = ConnWriteF(client->conn, "1000 %d\r\n", total);
            } else {
                ccode = ConnWriteStr(client->conn, MSG1000OK);
            }
        }

    } else {
        if (displayTotal) {
            total = DStoreCountConversations(client->handle, required, 0,
                                             headers, headercount,
                                             flagsmask, flags);
            if (total == -1) {
                return ConnWriteStr(client->conn, MSG5005DBLIBERR);
            }
            snprintf(buf, sizeof(buf), "1000 %d\r\n", total);
        }

        stmt = DStoreListConversations(client->handle, required, 0, 
                                       headers, headercount,
                                       start, end, center, 
                                       flagsmask, flags);
        if (!stmt) {
            return ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }

        ccode = ShowStmtDocumentsWithMessage(client, stmt, props, propcount, 
                                             displayTotal ? buf : MSG1000OK);
        DStoreStmtEnd(client->handle, stmt);
    }

    return ccode;
}            


CCode 
StoreCommandCOPY(StoreClient *client, uint64_t guid, DStoreDocInfo *collection)
{
    CCode ccode;
    const char *errmsg;
    DStoreDocInfo info;
    NLockStruct *lock = NULL;
    FILE *src = NULL;
    FILE *dest = NULL;
    char path[XPL_MAX_PATH + 1];
    struct stat sb;
    size_t start;
    size_t end;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case 1:
        break;
    case 0:
        return ConnWriteStr(client->conn, MSG4220NOGUID);
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }
        
    if (STORE_IS_FOLDER(info.type)) {
        return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
    }

    ccode = StoreCheckAuthorization(client, collection, 
                                    STORE_PRIV_BIND | STORE_PRIV_READ);
    if (ccode) {
        return ccode;
    }
        
    ccode = StoreGetCollectionLock(client, &lock, collection->guid);
    if (ccode) {
        return ccode;
    }

    FindPathToDocFile(client, info.collection, path, sizeof(path));
    src = fopen(path, "r");
    if (!src) {
        ccode = ConnWriteStr(client->conn, MSG4224CANTREADMBOX);
        goto finish;
    }

    FindPathToDocFile(client, collection->guid, path, sizeof(path));
    if (stat(path, &sb)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }
    dest = fopen(path, "a");
    if (!dest) {
        ccode = ConnWriteStr(client->conn, ENOSPC == errno ? MSG5220QUOTAERR 
                                                           : MSG4228CANTWRITEMBOX);
        goto finish;
    }

    start = info.start + info.headerlen;
    end = info.start + info.headerlen + info.bodylen;

    info.start = sb.st_size;
    info.guid = 0;
    info.imapuid = 0;
    info.collection = collection->guid;
    snprintf(info.filename, sizeof(info.filename), "%s/", collection->filename);

    info.headerlen = WriteDocumentHeaders(client, dest, NULL, info.timeCreatedUTC);
    if (info.headerlen < 0) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    if (BongoFileAppendBytes(src, dest, start, end) ||
        fflush(dest))
    {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    /* info.timeCreatedUTC = time(NULL); */

    if (DStoreSetDocInfo(client->handle, &info)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }

    errmsg = StoreProcessDocument(client, &info, collection, path, NULL, 0);
    if (errmsg) {
        ccode = ConnWriteStr(client->conn, errmsg);
        goto finish;
    }

    if (0 != DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    } 

    ++client->stats.insertions;
    StoreWatcherEvent(client, lock, STORE_WATCH_EVENT_NEW,
                      info.guid, info.imapuid, 0);

    ccode = ConnWriteF(client->conn, "1000 " GUID_FMT " %d\r\n", 
                       info.guid, info.version);
    
finish:
    if (src) {
        fclose(src);
    }
    if (dest) {
        fclose(dest);
    }
    if (lock) {
        StoreReleaseExclusiveLock(client, lock);
        StoreReleaseSharedLock(client, lock);
    }

    return ccode;
}


CCode
StoreCommandCREATE(StoreClient *client, char *name, uint64_t guid)
{
    uint64_t newguid;
    int32_t version;

    switch (StoreCreateCollection(client, name, guid, &newguid, &version)) {
    case 0:
        if (DStoreCommitTransaction(client->handle)) {
            return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
         }
        return ConnWriteF(client->conn, "1000 " GUID_FMT " %d\r\n", newguid, version);
    case -1:
        return ConnWriteStr(client->conn, MSG3019BADCOLLNAME);
    case -2:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    case -3:
        return ConnWriteF(client->conn, "4226 Guid " GUID_FMT " already exists\r\n", 
                          guid);
    case -5:
        return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
    case -4:
    default:
        return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    }
}


CCode
StoreCommandDELETE(StoreClient *client, uint64_t guid)
{
    int ccode = 0;
    DStoreDocInfo info;
    DStoreDocInfo conversation;
    NLockStruct *lock = NULL;
    BOOL updateConversation;
    IndexHandle *index = NULL;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case 1:
        if (STORE_IS_FOLDER(info.type)) {
            return ConnWriteStr(client->conn, MSG4231USEREMOVE);
        } 

        ccode = StoreCheckAuthorizationGuid(client, info.collection, 
                                            STORE_PRIV_UNBIND);
        if (ccode) {
            return ccode;
        }

        ccode = StoreGetCollectionLock(client, &lock, info.collection);
        if (ccode) {
            return ccode;
        }
        
        updateConversation = FALSE;

        switch (info.type) {
        case STORE_DOCTYPE_MAIL:
            if (info.conversation) {
                if (DStoreGetDocInfoGuid(client->handle, info.conversation, 
                                         &conversation) == 1) 
                {
                    updateConversation = TRUE;
                }
            }
            break;
        case STORE_DOCTYPE_CALENDAR:
            if (-1 == DStoreUnlinkCalendar(client->handle, guid)) {
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                goto finish;
            }
            break;
        case STORE_DOCTYPE_EVENT:
            /* FIXME: should unlink the event from its calendars */
            break;
        default:
            break;
        }

        if (-1 == DStoreDeleteDocInfo(client->handle, guid)) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }

        /* Try our best here, it's ok if it fails */ 
        if (updateConversation) {
            UpdateConversationMetadata(client->handle, &conversation);
        }

        break;
    case 0:
        ccode = ConnWriteStr(client->conn, MSG4220NOGUID);
        goto finish;
    case -1:
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }        

    index = IndexOpen(client);
    if (!index) {
        ccode = ConnWriteStr(client->conn, MSG5007INDEXLIBERR);
        goto finish;
    }

    if (DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    } 

    if (0 == rand() % 20) {
        /* compact collections to preserve disk space */
        client->flags |= STORE_CLIENT_FLAG_NEEDS_COMPACTING;
    }

    IndexRemoveDocument(index, guid);

    ++client->stats.deletions;
    StoreWatcherEvent(client, lock, STORE_WATCH_EVENT_DELETED, 
                      info.guid, info.imapuid, 0);


    ccode = ConnWriteStr(client->conn, MSG1000OK);;

finish:
    if (index) {
        IndexClose(index);
    }

    if (lock) {
        StoreReleaseExclusiveLock(client, lock);
        StoreReleaseSharedLock(client, lock);
    }

    return ccode;
}


/* one of filename and bytes must be set */

CCode
StoreCommandDELIVER(StoreClient *client, char *sender, char *authSender, 
                    char *filename, size_t bytes)
{
    CCode ccode;
    FILE *msgfile = NULL;
    char tmpFile[XPL_MAX_PATH+1];

    tmpFile[0] = 0;

    if (!filename) {
        if (bytes <= 0) {
            return ConnWriteStr(client->conn, MSG3017INTARGRANGE);
        }
        msgfile = XplOpenTemp(MsgGetDir(MSGAPI_DIR_WORK, NULL, 0), "w+b", tmpFile);
        if (!msgfile) {
            return ConnWriteStr(client->conn, MSG5202TMPWRITEERR);
        }
        ccode = ConnReadToFile(client->conn, msgfile, bytes);
        if (-1 == ccode) {
            unlink(tmpFile);
            fclose (msgfile);
            return ConnWriteStr(client->conn, MSG5202TMPWRITEERR);
        }
    } else {
        struct stat sb;
        char fullPath[XPL_MAX_PATH + 1];

        snprintf(fullPath, sizeof(fullPath), "%s/%s", StoreAgent.store.spoolDir, filename);


        if (stat(fullPath, &sb)) {
            return ConnWriteStr(client->conn, MSG4201FILENOTFOUND);
        }
        bytes = sb.st_size;

        msgfile = fopen(fullPath, "r");
        if (!msgfile) {
            return ConnWriteStr(client->conn, MSG5201FILEREADERR);
        }
    }

    if (-1 == (ccode = ConnWriteStr(client->conn, MSG2053SENDRECIP)) ||
        -1 == (ccode = ConnFlush(client->conn))) 
    {
        goto cleanup;
    }

    while (ccode >= 0) {
        char *args[3];
        char *recipient;
        char *mbox;
        unsigned long flags = 0;
        int n;
        int status;
        char buffer[CONN_BUFSIZE];

        ccode = ConnReadAnswer(client->conn, buffer, sizeof(buffer));
        if (-1 == ccode) {
            goto cleanup;
        }
        if (!buffer[0]) {
            ccode = ConnWriteStr(client->conn, MSG1000OK);
            break;
        }

        /* <recipient> <mailbox> <flags> */

        n = BongoStringSplit(buffer, ' ', args, 3);
        if (TOKEN_OK != (ccode = CheckTokC(client, n, 3, 3)) ||
            TOKEN_OK != (ccode = ParseUserName(client, args[0])) ||
            TOKEN_OK != (ccode = ParseUnsignedLong(client, args[2], &flags)))
        {
            if (-1 == (ccode = ConnFlush(client->conn))) {
                goto cleanup;
            }
            continue;
        }
        recipient = args[0];
        mbox = args[1];

        if (fseek (msgfile, 0, SEEK_SET)) {
            ccode = ConnWriteStr(client->conn, MSG5201FILEREADERR);
            goto cleanup;
        }
        status = DeliverMessageToMailbox(client, sender, authSender, recipient, mbox,
                                         msgfile, 0, bytes, flags);
        switch (status) {
        case DELIVER_SUCCESS:
            ccode = ConnWriteStr(client->conn, MSG1000DELIVERYOK);
            break;
        default:
            ccode = ConnWriteF(client->conn, MSG5260DELIVERYFAILED, status);
            break;
        }
        if (-1 == ccode) {
            goto cleanup;
        }
        if (-1 == (ccode = ConnFlush(client->conn))) {
            goto cleanup;
        }
    }

cleanup:    
    if (msgfile) {
        fclose (msgfile);
    }
    if (tmpFile[0]) {
        unlink(tmpFile);
    }

    return ccode;
}


typedef struct {
    StoreClient *client;
    unsigned int neededPrivileges;
} AuthFilterStruct;


static int
AuthFilter(DStoreDocInfo *info, void *filterptr)
{
    AuthFilterStruct *f = (AuthFilterStruct *) filterptr;

    return !StoreCheckAuthorizationQuiet(f->client, info, f->neededPrivileges);
}


CCode 
StoreCommandEVENTS(StoreClient *client, 
                   char *startUTC, char *endUTC, 
                   DStoreDocInfo *calendar, 
                   unsigned int mask,
                   char *uid,
                   const char *query,
                   int start, int end,
                   StorePropInfo *props, int propcount)
{
    CCode ccode = 0;

    /* FIXME - need to handle STORE_PRIV_READ_BUSY (bug 174023) */

    if (calendar) {
        ccode = StoreCheckAuthorization(client, calendar, 
                                        STORE_PRIV_LIST);
        if (ccode) {
            return ccode;
        }
    }

    if (query) {
        IndexHandle *index;
        uint64_t *guids = NULL;
        uint64_t *guidsptr = NULL;
        char newQuery[CONN_BUFSIZE + 64];
        int total;
        int i;
        DStoreDocInfo *info;
        int checkauth = !calendar && !IsOwnStoreSelected(client);
        
        snprintf(newQuery, sizeof(newQuery) - 1, "(nmap.type:event) AND (%s)", query);

        index = IndexOpen(client);
        if (!index) {
            return ConnWriteStr(client->conn, MSG4120INDEXLOCKED);
        }
        guids = IndexSearch(index, newQuery, TRUE, TRUE, 
                            -1, -1, NULL, &total, INDEX_SORT_DATE_DESC);
        IndexClose(index);

        if (!guids) {
            return ConnWriteStr(client->conn, MSG5007INDEXLIBERR);
        }
        
        /* FIXME: This is a big allocation */
        info = BONGO_MEMSTACK_ALLOC(&client->memstack, total * sizeof(DStoreDocInfo));

        for (guidsptr = guids, i = 0; *guidsptr; ++guidsptr) {
            switch (DStoreGetDocInfoGuid(client->handle, *guidsptr, &info[i])) {
            case 1:
                if (checkauth && 
                    StoreCheckAuthorizationQuiet(client, info, STORE_PRIV_READ)) 
                {
                    --total;
                    continue;
                }
                i++;
                break;
            case 0:
                --total;
                continue;
            case -1:
                MemFree(guids);
                return ConnWriteStr(client->conn, MSG5005DBLIBERR);
            }
        }

        MemFree(guids);

        if (-1 == start) {
            start = 0;
            end = total;
        }

        for (i = start; i < end && i < total && -1 != ccode; i++) {
            ccode = ShowDocumentInfo(client, &info[i], props, propcount);
        }
        
        if (-1 != ccode) {
            ccode = ConnWriteStr(client->conn, MSG1000OK);
        }
    } else {
        DStoreStmt *stmt;
        AuthFilterStruct f;

        stmt = DStoreFindEvents(client->handle, 
                                startUTC, endUTC,
                                calendar ? calendar->guid : 0,
                                mask, uid);
        
        if (!stmt) {
            return ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }
        
        if (!calendar && !IsOwnStoreSelected(client)) {
            f.client = client;
            f.neededPrivileges = STORE_PRIV_READ;
            DStoreInfoStmtAddFilter(stmt, AuthFilter, &f);
        }

        ccode = ShowStmtDocuments(client, stmt, props, propcount);

        DStoreStmtEnd(client->handle, stmt);
    }

    return 0;
}


CCode
StoreCommandFLAG(StoreClient *client, uint64_t guid, uint32_t change, int mode)
{
    DStoreDocInfo info;
    DStoreDocInfo conversation;
    uint32_t oldFlags = 0;
    IndexHandle *index;
    NLockStruct *lock = NULL;
    CCode ccode;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case 1:
        break;
    case 0:
        return ConnWriteStr(client->conn, MSG4220NOGUID);
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }        

    switch(mode) {
    case STORE_FLAG_SHOW: 
        ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_READ_PROPS);
        if (ccode) {
            return ccode;
        }
        return ConnWriteF(client->conn, "1000 %u %u\r\n", info.flags, info.flags);
    case STORE_FLAG_ADD:
        oldFlags = info.flags;
        info.flags |= change;
        break;
    case STORE_FLAG_REMOVE:
        oldFlags = info.flags;
        info.flags &= ~change;
        break;
    case STORE_FLAG_REPLACE:
        oldFlags = info.flags;
        info.flags = change;
        break;
    }

    ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_WRITE_PROPS);
    if (ccode) {
        return ccode;
    }

    if (STORE_DOCTYPE_CONVERSATION == info.type) {
        /* propagate STARS flag to conversation source */
        if (info.flags & STORE_DOCUMENT_FLAG_STARS) {
            info.data.conversation.sources |= CONVERSATION_SOURCE_STARRED;
        } else {
            info.data.conversation.sources &= ~CONVERSATION_SOURCE_STARRED;
        }
    }

    if (-1 == DStoreSetDocInfo(client->handle, &info)) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    if (info.type == STORE_DOCTYPE_MAIL && info.conversation != 0) {
        if (DStoreGetDocInfoGuid(client->handle, info.conversation, &conversation) == 1) {
            if (UpdateConversationMetadata(client->handle, &conversation)) {
                return ConnWriteStr(client->conn, MSG5005DBLIBERR);
            }
        }
    }

    ccode = StoreGetCollectionLock(client, &lock, info.collection);
    if (ccode) {
        return ccode;
    }

    index = IndexOpen(client);
    if (!index) {
        ccode = ConnWriteStr(client->conn, MSG4120INDEXLOCKED);
        goto finish;
    }
    if (IndexDocument(index, client, &info) != 0) {
        ccode = ConnWriteStr(client->conn, MSG5007INDEXLIBERR);
        goto finish;
    }

    if (0 != DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    ++client->stats.updates;
    StoreWatcherEvent(client, lock, STORE_WATCH_EVENT_FLAGS, 
                      info.guid, info.imapuid, info.flags);

finish:
    if (lock) {
        StoreReleaseExclusiveLock(client, lock);
        StoreReleaseSharedLock(client, lock);
    }
    if (index) {
        IndexClose(index);
    }

    return ConnWriteF(client->conn, "1000 %u %u\r\n", oldFlags, info.flags);
}


CCode
StoreCommandINDEXED(StoreClient *client, int value, uint64_t guid)
{
    int ccode = 0;
    
    ccode = DStoreSetIndexed(client->handle, value, guid);

    if (-1 == ccode) {
        DStoreAbortTransaction(client->handle);
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    if (DStoreCommitTransaction(client->handle)) {
        return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    } else {
        return ConnWriteStr(client->conn, MSG1000OK);
    }

    return ccode;
}            


CCode
StoreCommandINFO(StoreClient *client, uint64_t guid,
                 StorePropInfo *props, int propcount)
{
    DStoreDocInfo info;
    CCode ccode;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case 1:
        ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_READ_PROPS);
        if (ccode) {
            return ccode;
        }
        if (-1 == ShowDocumentInfo(client, &info, props, propcount)) {
            return -1;
        }
    case 0:
        break;
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }        
    
    return ConnWriteStr(client->conn, MSG1000OK);
}


CCode
StoreCommandISEARCH(StoreClient *client, const char *query, int start, int end,
                    StorePropInfo *props, int propcount)
{
    char buf[200];
    int ccode = 0;
    IndexHandle *index;
    uint64_t *guids;
    int displayed;
    int total;

    if (end - start > STORE_GUID_LIST_MAX) {
        return ConnWriteStr(client->conn, MSG3021BADRANGESIZE);
    }
    
    index = IndexOpen(client);
    if (!index) {
        return ConnWriteStr(client->conn, MSG4120INDEXLOCKED);
    }

    guids = IndexSearch(index, query, FALSE, FALSE, start, end, &displayed, &total, 
                        INDEX_SORT_RELEVANCE);
    if (!guids) {
        IndexClose(index);
        return ConnWriteStr(client->conn, MSG5007INDEXLIBERR);
    }

    snprintf(buf, sizeof(buf), "1000 %d\r\n", total);

    ccode = ShowGuidsDocumentsWithMessage(client, guids, NULL, NULL, 
                                          props, propcount, buf);

    IndexClose(index);

    MemFree(guids);

    return ccode;
}            


CCode
StoreCommandLIST(StoreClient *client, 
                 DStoreDocInfo *coll, 
                 int start, int end, 
                 uint32_t flagsmask, uint32_t flags,
                 StorePropInfo *props, int propcount)
{
    int ccode = 0;
    DStoreStmt *stmt;
    MaskAndFlagStruct mf = { flagsmask, flags };

    ccode = StoreCheckAuthorization(client, coll, STORE_PRIV_LIST);
    if (ccode) {
        return ccode;
    }
    
    stmt = DStoreListColl(client->handle, coll->guid, start, end);
    if (!stmt) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    if (flagsmask) {
        DStoreInfoStmtAddFilter(stmt, MaskFilter, (void *) &mf);
    }

    ccode = ShowStmtDocuments(client, stmt, props, propcount);
    
    DStoreStmtEnd(client->handle, stmt);
    return ccode;
}            


CCode
StoreCommandMAILINGLISTS(StoreClient *client, char *source)
{
    DStoreStmt *stmt;
    uint32_t required;
    CCode ccode = 0;
    int dcode;

    GetConversationSourceMask(source, &required);

    stmt = DStoreFindMailingLists(client->handle, required);
    if (!stmt) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }
    
    do {
        const char *str;
        
        dcode = DStoreStrStmtStep(client->handle, stmt, &str);
        if (1 == dcode) {
            if (str) {
                ccode = ConnWriteF(client->conn, "2001 %s\r\n", str);
            }
        } else if (0 == dcode) {
            ccode = ConnWriteStr(client->conn, MSG1000OK);
        } else {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }
    } while (1 == dcode && ccode >= 0);

    DStoreStmtEnd(client->handle, stmt);
    
    return ccode;
}


CCode
StoreCommandMESSAGES(StoreClient *client, const char *source, const char *query, 
                     int start, int end, uint64_t center, 
                     uint32_t flagsmask, uint32_t flags,
                     int displayTotal,
                     StoreHeaderInfo *headers, int headercount, 
                     StorePropInfo *props, int propcount)
{
    CCode ccode = 0;
    DStoreStmt *stmt = NULL;
    uint64_t *guids = NULL;
    char buf[200];
    int total;
    uint32_t required;

    ccode = StoreCheckAuthorizationGuid(client, 
                                        STORE_CONVERSATIONS_GUID, STORE_PRIV_READ);
    if (ccode) {
        return ccode;
    }


    GetConversationSourceMask(source, &required);


    if (query) {
        IndexHandle *index;
        char newQuery[CONN_BUFSIZE * 2];

        snprintf(newQuery, sizeof(newQuery) - 1, "(nmap.type:mail AND (%s))", query);

        index = IndexOpen(client);
        if (!index) {
            return ConnWriteStr(client->conn, MSG4120INDEXLOCKED);
        }
        guids = IndexSearch(index, newQuery, FALSE, TRUE, 
                            -1, -1, NULL, &total, INDEX_SORT_GUID);
        IndexClose(index);

        if (!guids) {
            return ConnWriteStr(client->conn, MSG5007INDEXLIBERR);
        }
    }


    if (displayTotal) {
        total = DStoreCountMessages(client->handle, required, 0,
                                    headers, headercount,
                                    flagsmask, flags);
        if (total == -1) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }
        snprintf(buf, sizeof(buf), "1000 %d\r\n", total);
    }
        
    stmt = DStoreFindMessages(client->handle, required, 0, 
                              headers, headercount,
                              guids,
                              start, end, center, 
                              flagsmask, flags);
    if (!stmt) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }

    ccode = ShowStmtDocumentsWithMessage(client, stmt, props, propcount, 
                                             displayTotal ? buf : MSG1000OK);
    DStoreStmtEnd(client->handle, stmt);

finish:
    if (guids) {
        MemFree(guids);
    }
    return ccode;
}            


CCode
StoreCommandMFLAG(StoreClient *client, uint32_t change, int mode)
{
    CCode ccode = 0;
    CollectionLockPool collLocks;
    WatchEventList events;
    IndexHandle *index;
    NLockStruct *lock = NULL;
    int updates = 0;
    
    if (CollectionLockPoolInit(client, &collLocks)) {
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }
    if (WatchEventListInit(&events)) {
        CollectionLockPoolDestroy(&collLocks);
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }

    index = IndexOpen(client);
    if (!index) {
        ccode = ConnWriteStr(client->conn, MSG4120INDEXLOCKED);
        goto finish;
    }

    while (ccode >= 0) {
        uint64_t guid;
        char buffer[CONN_BUFSIZE];
        DStoreDocInfo info;
        DStoreDocInfo conversation;
        uint32_t oldFlags = 0;

        if (-1 == (ccode = ConnWriteStr(client->conn, MSG2054SENDDOCS)) ||
            -1 == (ccode = ConnFlush(client->conn)) ||
            -1 == (ccode = ConnReadAnswer(client->conn, buffer, sizeof(buffer))))
        {
            goto finish;
        }

        if (!buffer[0]) {
            break;
        }

        /* <guid> */

        if (TOKEN_OK != (ccode = ParseGUID(client, buffer, &guid))) {
            continue;
        }

        switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
        case 1:
            break;
        case 0:
            ccode = ConnWriteStr(client->conn, MSG4220NOGUID);
            continue;
        case -1:
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }

        switch(mode) {
        case STORE_FLAG_SHOW: 
            ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
            goto finish;
        case STORE_FLAG_ADD:
            oldFlags = info.flags;
            info.flags |= change;
            break;
        case STORE_FLAG_REMOVE:
            oldFlags = info.flags;
            info.flags &= ~change;
            break;
        case STORE_FLAG_REPLACE:
            oldFlags = info.flags;
            info.flags = change;
            break;
        }

        /* FIXME: should use quiet version, and not abort on permission denied: */
        ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_WRITE_PROPS);
        if (ccode) {
            goto finish;
        }

        lock = CollectionLockPoolGet(&collLocks, info.collection);
        if (!lock) {
            ccode = ConnWriteStr(client->conn, MSG4120BOXLOCKED);
            continue;
        }

        if (STORE_DOCTYPE_CONVERSATION == info.type) {
            /* propagate STARS flag to conversation source */
            if (info.flags & STORE_DOCUMENT_FLAG_STARS) {
                info.data.conversation.sources |= CONVERSATION_SOURCE_STARRED;
            } else {
                info.data.conversation.sources &= ~CONVERSATION_SOURCE_STARRED;
            }
        }
        
        if (-1 == DStoreSetDocInfo(client->handle, &info)) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }

        if (info.type == STORE_DOCTYPE_MAIL && info.conversation != 0) {
            switch (DStoreGetDocInfoGuid(client->handle, info.conversation, 
                                         &conversation) == 1)
            {
            case 1:
                if (UpdateConversationMetadata(client->handle, &conversation)) {
                    ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                    goto finish;
                }
                break;
            case 0:
                break;
            case -1:
            default:
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                goto finish;
            }
        }

        if (IndexDocument(index, client, &info) != 0) {
            ccode = ConnWriteStr(client->conn, MSG5007INDEXLIBERR);
            goto finish;
        }
        ++updates;

        if (WatchEventListAdd(&events, lock, STORE_WATCH_EVENT_FLAGS,
                              info.guid, info.imapuid, info.flags))
        {
            ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
            goto finish;
        }

        ccode = ConnWriteF(client->conn, "2001 %u %u\r\n", oldFlags, info.flags);
    }
    
    if (0 != DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    IndexClose(index);
    index = NULL;
    
    client->stats.updates += updates;

    WatchEventListFire(&events, NULL);
    
finish:

    CollectionLockPoolDestroy(&collLocks);
    WatchEventListDestroy(&events);

    if (index) {
        IndexClose(index);
    }

    return ConnWriteStr(client->conn, MSG1000OK);
}


CCode
StoreCommandMIME(StoreClient *client, 
                 uint64_t guid)
{

    int ccode = 0;
    DStoreDocInfo info;
    MimeReport *report = NULL;
    NLockStruct *lock = NULL;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case 1:
        break;
    case 0:
        return ConnWriteF(client->conn, MSG4220NOGUID);
    case -1:
    default:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_READ);
    if (ccode) {
        return ccode;
    }

    if (STORE_IS_FOLDER(info.type)) {
        return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
    }
 
    ccode = StoreGetSharedLock(client, &lock, info.collection, 3000);
    if (ccode) {
        return ccode;
    }
    
    switch (MimeGetInfo(client, &info, &report)) {
    case 1:
        ccode = MimeReportSend(client, report);
        MimeReportFree(report);
        if (-1 != ccode) {
            ccode = ConnWriteStr(client->conn, MSG1000OK);
        }
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
            
    if (lock) {
        StoreReleaseSharedLock(client, lock);
    }

    /* we commit the transaction because MimeGetInfo() might have
       written data to the mime cache. */
    DStoreCommitTransaction(client->handle);
    
    return ccode;
}


CCode 
StoreCommandMOVE(StoreClient *client, uint64_t guid, 
                 DStoreDocInfo *newcoll, const char *newfilename)
{
    int ccode = 0;
    DStoreDocInfo oldcoll;
    DStoreDocInfo info;
    DStoreDocInfo conversation;
    FILE *oldfile = NULL;
    FILE *newfile = NULL;
    uint32_t oldimapuid;
    NLockStruct *srclock = NULL;
    NLockStruct *dstlock = NULL;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case -1:
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto abort;
    case 0:
        ccode = ConnWriteStr(client->conn, MSG4220NOGUID);
        goto abort;
    default:
        break;
    }

    if (STORE_IS_FOLDER(info.type)) {
        ccode = ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
        goto abort;
    }

    if (info.imapuid) {
        oldimapuid = info.imapuid;
    } else {
        oldimapuid = (uint32_t)guid;
    }

    ccode = StoreGetCollectionLock(client, &srclock, info.collection);
    if (ccode) {
        goto abort;
    }

    /* if the source collection is also the destination, */
    /* then we only need one lock and we already have it */
    if (info.collection != newcoll->guid) {
        ccode = StoreGetCollectionLock(client, &dstlock, newcoll->guid);
        if (ccode) {
            goto abort;
        }
    }
    
    if (1 != DStoreGetDocInfoGuid(client->handle, info.collection, &oldcoll)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto abort;
    }
    
    if ((ccode = StoreCheckAuthorization(client, &oldcoll, STORE_PRIV_UNBIND)) ||
        (ccode = StoreCheckAuthorization(client, newcoll, STORE_PRIV_BIND)))
    {
        goto abort;
    }

    if (!newfilename) {
        snprintf(info.filename, sizeof(info.filename), "%s/bongo-" GUID_FMT, 
                 newcoll->filename, info.guid);
        
    } else {
        snprintf(info.filename, sizeof(info.filename), "%s/%s",
                 newcoll->filename, newfilename);
    }

    if (STORE_DOCTYPE_MAIL == info.type) {
        uint64_t newguid;

        if (DStoreGenerateGUID(client->handle, &newguid)) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto abort;
        }
        info.imapuid = newguid;
    }

    if (newcoll->guid != info.collection) {
        char path[XPL_MAX_PATH + 1];
        struct stat sb;
        size_t total;
        size_t count;
        char buffer[CONN_BUFSIZE];

        FindPathToDocFile(client, info.collection, path, sizeof(path));
        if (!(oldfile = fopen(path, "r")) || 
            XplFSeek64(oldfile, info.start + info.headerlen, SEEK_SET)) 
        {
            ccode = ConnWriteStr(client->conn, MSG4224CANTREAD);
            goto abort;
        }
        
        FindPathToDocFile(client, newcoll->guid, path, sizeof(path));
        if (stat(path, &sb) ||
            !(newfile = fopen(path, "a"))) 
        {
            ccode = ConnWriteStr(client->conn, MSG4224CANTWRITE);
            goto abort;
        }

        info.start = sb.st_size;
        info.headerlen = WriteDocumentHeaders(client, newfile, NULL, 
                                              info.timeCreatedUTC);
        if (info.headerlen < 0) {
            ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
            goto abort;
        }

        for (total = info.bodylen;
             total;
             total -= count)
        {
            count = sizeof(buffer) < total ? sizeof(buffer) : total;

            count = fread(buffer, sizeof(char), count, oldfile);
            if (!count ||
                ferror(oldfile) || 
                count != fwrite(buffer, sizeof(char), count, newfile))
            {
                ccode = ConnWriteStr(client->conn, MSG4224CANTWRITE);
                goto abort;
            }
        }

        (void) fclose(oldfile);
        oldfile = NULL;
        if (fclose(newfile)) {
            ccode = ConnWriteStr(client->conn, MSG4224CANTWRITE);
            goto abort;
        }
        newfile = NULL;

        if (0 == rand() % 25) {
            /* compact collections to preserve disk space */
            client->flags |= STORE_CLIENT_FLAG_NEEDS_COMPACTING;
        }
    }

    info.collection = newcoll->guid;

    if (DStoreSetDocInfo(client->handle, &info)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    if (STORE_DOCTYPE_MAIL == info.type) {
        newcoll->imapuid = info.imapuid;
        if (DStoreSetDocInfo(client->handle, newcoll)) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto abort;
        }

        if (DStoreGetDocInfoGuid(client->handle, info.conversation, &conversation) == 1) {
            if (UpdateConversationMetadata(client->handle, &conversation)) {
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                goto abort;
            }
        }
    }

    if (DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto abort;
    }

    StoreWatcherEvent(client, srclock, STORE_WATCH_EVENT_DELETED, 
                      guid, oldimapuid, 0);
    if (dstlock) {
        StoreWatcherEvent(client, dstlock, STORE_WATCH_EVENT_NEW,
                          info.guid, info.imapuid, 0);
    } else {
        StoreWatcherEvent(client, srclock, STORE_WATCH_EVENT_NEW,
                          info.guid, info.imapuid, 0);
    }

    ccode = ConnWriteStr(client->conn, MSG1000OK);
    goto finish;
    
abort:

    DStoreAbortTransaction(client->handle);

finish:

    if (oldfile) {
        fclose(oldfile);
    }
    if (newfile) {
        fclose(newfile);
    }
    
    if (srclock) {
        StoreReleaseExclusiveLock(client, srclock);
        StoreReleaseSharedLock(client, srclock);        
    }
    if (dstlock) {
        StoreReleaseExclusiveLock(client, dstlock);
        StoreReleaseSharedLock(client, dstlock);
    }
    
    return ccode;
}


CCode
StoreCommandPROPGET(StoreClient *client, 
                    uint64_t guid, 
                    StorePropInfo *props, int propcount)
{
    DStoreStmt *stmt = NULL;
    DStoreDocInfo info;
    int ccode = 0;
    int dcode;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    case 0:
        return ConnWriteStr(client->conn, MSG4220NOGUID);
    default:
        break;
    }
    
    ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_READ_PROPS);
    if (ccode) {
        return ccode;
    }

    if (!propcount) { /* show them all */
        StorePropInfo thisProp = { 0 };

        if (-1 == ShowInternalProperty(client, STORE_PROP_GUID, &info) ||
            -1 == ShowInternalProperty(client, STORE_PROP_TYPE, &info) ||
            -1 == ShowInternalProperty(client, STORE_PROP_COLLECTION, &info) ||
            -1 == ShowInternalProperty(client, STORE_PROP_INDEX, &info) ||
            -1 == ShowInternalProperty(client, STORE_PROP_FLAGS, &info) ||
            -1 == ShowInternalProperty(client, STORE_PROP_VERSION, &info) ||
            -1 == ShowInternalProperty(client, STORE_PROP_CREATED, &info) ||
            -1 == ShowInternalProperty(client, STORE_PROP_LASTMODIFIED, &info) ||
            (info.type == STORE_DOCTYPE_EVENT &&
             (-1 == ShowInternalProperty(client, STORE_PROP_EVENT_CALENDARS, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_EVENT_END, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_EVENT_LOCATION, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_EVENT_START, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_EVENT_SUMMARY, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_EVENT_STAMP, &info))) ||
            (info.type == STORE_DOCTYPE_MAIL && 
             (-1 == ShowInternalProperty(client, STORE_PROP_MAIL_CONVERSATION, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_MAIL_IMAPUID, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_MAIL_SENT, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_MAIL_MESSAGEID, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_MAIL_PARENTMESSAGEID, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_MAIL_HEADERLEN, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_MAIL_SUBJECT, &info))) ||
            (info.type == STORE_DOCTYPE_CONVERSATION && 
             (-1 == ShowInternalProperty(client, STORE_PROP_CONVERSATION_SUBJECT, &info) ||
              -1 == ShowInternalProperty(client, STORE_PROP_CONVERSATION_COUNT, &info) || 
              -1 == ShowInternalProperty(client, STORE_PROP_CONVERSATION_UNREAD, &info) || 
              -1 == ShowInternalProperty(client, STORE_PROP_CONVERSATION_DATE, &info))))
        {
            return -1;
        }
        
        stmt = DStoreFindProperties(client->handle, guid, NULL);
        if (!stmt) {
            return ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }
        do {
            dcode = DStorePropStmtStep(client->handle, stmt, &thisProp);
            if (1 == dcode) {
                ccode = ShowExternalProperty(client, &thisProp);
                if (-1 == ccode) {
                    break;
                }
            } else if (-1 == dcode) {
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);                
                break;
            } else if (0 == dcode) {
                ccode = ConnWriteStr(client->conn, MSG1000OK);
            }
        } while (dcode);
        DStoreStmtEnd(client->handle, stmt);
    } else {
        int i;
        
        for (i = 0; i < propcount && ccode >= 0; i++) {
            ccode = ShowProperty(client, &info, &props[i]);
        }
    }

    return ccode;
}


CCode
StoreCommandPROPSET(StoreClient *client, 
                    uint64_t guid, 
                    StorePropInfo *prop)
{
    DStoreDocInfo info;
    int ccode;
    NLockStruct *lock;
    BongoJsonNode *alarmjson = NULL;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    case 0:
        return ConnWriteStr(client->conn, MSG4220NOGUID);
    default:
        break;
    }
        
    if (!STORE_IS_EXTERNAL_PROPERTY_TYPE(prop->type)) {
        return ConnWriteStr(client->conn, MSG3244BADPROPNAME);
    }

    prop->value = BONGO_MEMSTACK_ALLOC(&client->memstack, prop->valueLen + 1);
    if (!prop->value) {
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }
    if (-1 == (ccode = ConnWriteStr(client->conn, MSG2002SENDVALUE)) ||
        -1 == (ccode = ConnFlush(client->conn)) ||
        -1 == (ccode = ConnReadCount(client->conn, prop->value, prop->valueLen)))
    {
        return ccode;
    }
    prop->value[prop->valueLen] = 0;

    if (STORE_PROP_ACCESS_CONTROL == prop->type) {
        if (StoreParseAccessControlList(client, prop->value)) {
            return ConnWriteStr(client->conn, MSG3025BADACL);
        }
    }

    ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_WRITE_PROPS);
    if (ccode) {
        return ccode;
    }

    ccode = StoreGetCollectionLock(client, &lock, info.collection);
    if (ccode) {
        return ccode;
    }
    
    if (STORE_PROP_EVENT_ALARM == prop->type) {
        ccode = RequireAlarmDB(client);
        if (TOKEN_OK != ccode) {
            goto finish;
        }
        ccode = StoreSetAlarm(client, &info, prop->value);
        if (ccode) {
            goto finish;
        }
    }

    if (DStoreSetProperty(client->handle, guid, prop)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
    } else if (DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
    } else {
        ++client->stats.updates;
        StoreWatcherEvent(client, lock, STORE_WATCH_EVENT_MODIFIED, 
                          info.guid, info.imapuid, 0);
        ccode = ConnWriteStr(client->conn, MSG1000OK);
    }

finish:
    StoreReleaseExclusiveLock(client, lock);
    StoreReleaseSharedLock(client, lock);

    if (alarmjson) {
        BongoJsonNodeFree(alarmjson);
    }

    return ccode;
}


/* returns: -1, causing storecommandloop to exit */
CCode
StoreCommandQUIT(StoreClient *client)
{
    if (-1 != ConnWriteStr(client->conn, MSG1000BYE)) {
        ConnFlush(client->conn);
    }
    return -1;
}


CCode
StoreCommandREAD(StoreClient *client, 
                 uint64_t guid, int64_t requestStart, int64_t requestLength)
{
    int ccode = 0;
    DStoreDocInfo info;
    NLockStruct *lock;

    switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    case 0:
        return ConnWriteStr(client->conn, MSG4220NOGUID);
    default:
        break;
    }

    ccode = StoreGetSharedLock(client, &lock, info.collection, 3000);
    if (ccode) {
        return ccode;
    }

    ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_READ);
    if (ccode) {
        goto finish;
    }

    if (STORE_IS_FOLDER(info.type)) {
        ccode = StoreShowFolder(client, info.collection, 0);
    } else {
        ccode = ShowDocumentBody(client, &info, requestStart, requestLength);
    }

finish:
    StoreReleaseSharedLock(client, lock);

    return ccode;
}


CCode 
StoreCommandREINDEX(StoreClient *client, uint64_t guid)
{
    IndexHandle *index;
    CCode ccode = 0;
    int dcode = 1;
    DStoreStmt *stmt = NULL;
    DStoreDocInfo info;

    index = IndexOpen(client);
    if (!index) {
        return ConnWriteStr(client->conn, MSG4120INDEXLOCKED);
    }
    
    stmt = DStoreFindDocInfo(client->handle, guid, NULL, NULL);
    if (!stmt) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }
    while (stmt && 1 == dcode)
    {
        dcode = DStoreInfoStmtStep(client->handle, stmt, &info);
        if (-1 == dcode) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }
        if (guid) { /* kludgey */
            DStoreStmtEnd(client->handle, stmt);
            stmt = NULL;
        }
        if (STORE_IS_FOLDER(info.type)) {
            continue;
        }

        IndexDocument(index, client, &info);
    }
    
    ccode = ConnWriteStr(client->conn, MSG1000OK);
finish:
    if (index) {
        IndexClose(index);
    }
    if (stmt) {
        DStoreStmtEnd(client->handle, stmt);
    }

    return ccode;
}


CCode
StoreCommandRENAME(StoreClient *client, DStoreDocInfo *collection,
                   char *newfilename)
{
    NLockStruct *lock = NULL;
    CCode ccode = 0;
    int dcode;
    char oldfilename[STORE_MAX_PATH+1];
    DStoreStmt *stmt = NULL;
    DStoreDocInfo newcollection;
    BongoArray subcolls;
    BongoArray locks;
    unsigned int i;
    int success = 0;

    ccode = StoreCheckAuthorizationGuid(client, collection->collection, 
                                        STORE_PRIV_UNBIND);
    if (ccode) {
        return ccode;
    }

    if (BongoArrayInit(&subcolls, sizeof(uint64_t), 32)) {
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }
    if (BongoArrayInit(&locks, sizeof(NLockStruct *), 32)) {
        BongoArrayDestroy(&subcolls, TRUE);
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }

    BongoArrayAppendValue(&subcolls, collection->guid);

    /* first, find and lock all the subcollections */
    /* FIXME: this is kind of inefficient.  We need a db function for listing 
       all subcollections.
     */

    for (i = 0; i < subcolls.len; i++) {
        uint64_t guid;
        DStoreDocInfo info;

        guid = BongoArrayIndex(&subcolls, uint64_t, i);
        
        ccode = StoreGetCollectionLock(client, &lock, guid);
        if (ccode) {
            goto finish;
        }
        BongoArrayAppendValue(&locks, lock);

        stmt = DStoreListColl(client->handle, guid, -1, -1);
        if (!stmt) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }
    
        while ((dcode = DStoreInfoStmtStep(client->handle, stmt, &info))) {
            if (1 == dcode) {
                /* FIXME - check return code (bug 174103) */
                if (STORE_IS_FOLDER(info.type)) {
                    BongoArrayAppendValue(&subcolls, info.guid);
                }
            } else if (-1 == dcode) {
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                goto finish;
            }
        }
        
        DStoreStmtEnd(client->handle, stmt);
        stmt = NULL;
    }
   
    strncpy(oldfilename, collection->filename, sizeof(oldfilename));

    if (DStoreDeleteDocInfo(client->handle, collection->guid)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }
    
    switch (StoreCreateCollection(client, newfilename, collection->guid, 
                                  &collection->guid, NULL)) 
    {
    case 0:
        break;
    case -1:
        ccode = ConnWriteStr(client->conn, MSG3019BADCOLLNAME);
        goto finish;
    case -2: /* db lib err */
    case -3: /* guid exists */
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    case -5: /* permission denied */
        ccode = ConnWriteStr(client->conn, MSG4240NOPERMISSION);
        goto finish;
    case -4: /* io error */
    default:
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        break;
    }

    /* we want to keep most of the docinfo from the old collection, but need to 
       pull in some info (such as the parent) from the collection we just created. */

    if (1 != DStoreGetDocInfoGuid(client->handle, collection->guid, &newcollection)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }        

    assert(!strcmp(newcollection.filename, newfilename));
    
    strncpy(collection->filename, newfilename, sizeof(collection->filename));
    collection->collection = newcollection.collection;
    if (DStoreSetDocInfo(client->handle, collection) ||
        DStoreRenameDocuments(client->handle, oldfilename, newfilename))
    {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }
    
    if (DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }
    
    ccode = ConnWriteStr(client->conn, MSG1000OK);
    success = 1;
    
finish:
    if (stmt) {
        DStoreStmtEnd(client->handle, stmt);
    }

    for (i = 0; i < locks.len; i++) {
        lock = BongoArrayIndex(&locks, NLockStruct*, i);

        if (success) {
            StoreWatcherEvent(client, lock, STORE_WATCH_EVENT_COLL_RENAMED, 
                              0, 0, 0);
        }
        StoreReleaseExclusiveLock(client, lock);
        StoreReleaseSharedLock(client, lock);
    }

    BongoArrayDestroy(&subcolls, TRUE);
    BongoArrayDestroy(&locks, TRUE);
    
    return ccode;
}


static CCode
RemoveCollections(StoreClient *client, uint64_t guid)
{
    CCode ccode = 0;
    int dcode;
    DStoreStmt *stmt = NULL;
    NLockStruct *lock = NULL;
    IndexHandle *index = NULL;
    DStoreDocInfo info;

    BongoArray subcolls;
    BongoArray locks;
    BongoArray docs;

    unsigned int i;
    int trans = 1;
    int success = 0;

    if (BongoArrayInit(&subcolls, sizeof(uint64_t), 32)) {
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }
    if (BongoArrayInit(&locks, sizeof(NLockStruct *), 32)) {
        BongoArrayDestroy(&subcolls, TRUE);
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }
    if (BongoArrayInit(&docs, sizeof(uint64_t), 256)) {
        BongoArrayDestroy(&subcolls, TRUE);
        BongoArrayDestroy(&locks, TRUE);
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }
    
    BongoArrayAppendValue(&subcolls, guid);

    index = IndexOpen(client);
    if (!index) {
        ccode = ConnWriteStr(client->conn, MSG5007INDEXLIBERR);
        goto finish;
    }

    /* first pass: find db objects */

    for (i = 0; i < subcolls.len; i++) {
        guid = BongoArrayIndex(&subcolls, uint64_t, i);
        
        ccode = StoreGetCollectionLock(client, &lock, guid);
        if (ccode) {
            goto finish;
        }
        BongoArrayAppendValue(&locks, lock);

        stmt = DStoreListColl(client->handle, guid, -1, -1);
        if (!stmt) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }
    
        while ((dcode = DStoreInfoStmtStep(client->handle, stmt, &info))) {
            if (1 == dcode) {
                /* FIXME - check return code (bug 174103) */
                if (STORE_IS_FOLDER(info.type)) {
                    BongoArrayAppendValue(&subcolls, info.guid);
                } else {
                    BongoArrayAppendValue(&docs, info.guid);
                }
            } else if (-1 == dcode) {
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                goto finish;
            }
        }
        
        DStoreStmtEnd(client->handle, stmt);
        stmt = NULL;
    }

    /* second pass: remove db objects (couldn't do this in step one 
       because the select stmt locks the db)
    */

    for (i = 0; i < subcolls.len; i++) {
        guid = BongoArrayIndex(&subcolls, uint64_t, i);

        if (-1 == DStoreDeleteCollectionContents(client->handle, guid) ||
            -1 == DStoreDeleteDocInfo(client->handle, guid)) 
        {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }
    }

    if (DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }
    trans = 0;
    
    /* third pass: remove .dat files. - if unlink() ever fails, we waste some 
       disk space, but the store will still be consistent.  
    */

    for (i = 0; i < subcolls.len; i++) {
        char path[XPL_MAX_PATH+1];

        guid = BongoArrayIndex(&subcolls, uint64_t, i);
        
        FindPathToDocFile(client, guid, path, sizeof(path));
        unlink(path);
    }

    /* fourth pass: remove docs from the index */

    for (i = 0; i < docs.len; i++) {
        guid = BongoArrayIndex(&docs, uint64_t, i);
        
        IndexRemoveDocument(index, guid);
        
        ++client->stats.deletions;
    }

    IndexClose(index);
    index = NULL;

    ccode = ConnWriteStr(client->conn, MSG1000OK); 
    success = 1;
    
finish:
    if (stmt) {
        DStoreStmtEnd(client->handle, stmt);
    }

    if (trans) {
        DStoreAbortTransaction(client->handle);
    }

    if (index) {
        IndexClose(index);
    }

    for (i = 0; i < locks.len; i++) {
        lock = BongoArrayIndex(&locks, NLockStruct*, i);

        if (success) {
            StoreWatcherEvent(client, lock, STORE_WATCH_EVENT_COLL_REMOVED, 
                              0, 0, 0);
        }
        StoreReleaseExclusiveLock(client, lock);
        StoreReleaseSharedLock(client, lock);
    }

    BongoArrayDestroy(&subcolls, TRUE);
    BongoArrayDestroy(&locks, TRUE);
    BongoArrayDestroy(&docs, TRUE);

    return ccode;
}


CCode
StoreCommandREMOVE(StoreClient *client, DStoreDocInfo *info)
{
    CCode ccode;
    
    if (!STORE_IS_FOLDER(info->type)) {
        return ConnWriteStr(client->conn, MSG4231USEPURGE);
    } else {
        ccode = StoreCheckAuthorizationGuid(client, info->collection, 
                                            STORE_PRIV_UNBIND);
        if (ccode) {
            return ccode;
        }
        return RemoveCollections (client, info->guid);
    } 
}


/* N.B. This command does no locking.  It is for debugging purposes only. */

CCode 
StoreCommandRESET(StoreClient *client)
{
    DeleteStore(client);
    return ConnWriteStr(client->conn, MSG1000OK);
}


CCode
StoreCommandSTORE(StoreClient *client, char *user)
{
    if (!user) {
        UnselectStore(client);
        return ConnWriteStr(client->conn, MSG1000OK);
    }

    if (StoreAgent.installMode || !strncmp(user, "_system", 7)) {
        if (SelectStore(client, user)) {
            return ConnWriteStr(client->conn, MSG4224BADSTORE);
        } else {
            return ConnWriteStr(client->conn, MSG1000OK);
        }
    }

    if (0 != MsgAuthFindUser(user)) {
        XplConsolePrintf("Couldn't find user object for %s\r\n", user);
        /* the previous nmap returned some sort of user locked message? */
        return ConnWriteStr(client->conn, MSG4100STORENOTFOUND);
    }

    // FIXME: Below is a check that the user resides in this store.
    // For Bongo 1.0, we're assuming a single store?
#if 0
	if (serv.sin_addr.s_addr != MsgGetHostIPAddress()) {
        // non-local store
        return ConnWriteStr(client->conn, MSG4100STORENOTFOUND);
    }
#endif
    
    if (SelectStore(client, user)) {
        return ConnWriteStr(client->conn, MSG4224BADSTORE);
    } else {
        return ConnWriteStr(client->conn, MSG1000OK);
    }
}


CCode
StoreCommandTIMEOUT(StoreClient *client, int lockTimeoutMs)
{
    if (-1 == lockTimeoutMs) {
        return ConnWriteF(client->conn, "1000 %d\r\n", client->lockTimeoutMs);
    } else if (lockTimeoutMs < 0) {
        return ConnWriteStr(client->conn, MSG3022BADSYNTAX);
    } else {
        client->lockTimeoutMs = lockTimeoutMs;
        if (client->handle) {
            DStoreSetLockTimeout(client->handle, lockTimeoutMs);
        }
        return ConnWriteStr(client->conn, MSG1000OK);
    }
}


CCode 
StoreCommandTOKEN(StoreClient *client, char *token)
{
    StoreToken *tok;

    tok = MemMalloc(sizeof(StoreToken));

    strncpy(tok->data, token, sizeof(tok->data));
    tok->data[sizeof(tok->data)-1] = 0;

    tok->next = client->principal.tokens;
    client->principal.tokens = tok;

    return ConnWriteStr(client->conn, MSG1000OK);
}


CCode 
StoreCommandTOKENS(StoreClient *client)
{
    StoreToken *tok;
    CCode ccode = 0;

    for (tok = client->principal.tokens;
         tok && ccode >= 0;
         tok = tok->next)
    {
        ccode = ConnWriteF(client->conn, "2001 %s\r\n", tok->data);
    }
    
    if (ccode >= 0) {
        ccode = ConnWriteStr(client->conn, MSG1000OK);
    }
    return ccode;
}


CCode
StoreCommandUSER(StoreClient *client, char *user, char *password, int nouser)
{
    if (user) {
        return SelectUser(client, user, password, nouser);
    } else {
        UnselectUser(client);
        return ConnWriteStr(client->conn, MSG1000OK);
    }
}


/* Set the watched flags for the specified collection.  If we were watching 
   something else before, stop watching it now.
*/

CCode 
StoreCommandWATCH(StoreClient *client, DStoreDocInfo *collection, int flags)
{
    CCode ccode;
    NLockStruct *lock;

    /* stop watching whatever we were watching before */
    
    if (client->watch.collection) {
        ccode = StoreGetCollectionLock(client, &lock, client->watch.collection);
        if (ccode) {
            return ccode;
        }
        if (StoreWatcherRemove(client, lock)) {
            return ConnWriteStr(client->conn, MSG5004INTERNALERR);
        }

        StoreReleaseExclusiveLock(client, lock);
        StoreReleaseSharedLock(client, lock);
    }

    /* watch the new collection */

    if (flags) {
        flags |= STORE_WATCH_EVENT_COLL_REMOVED;
        flags |= STORE_WATCH_EVENT_COLL_RENAMED;

        ccode = StoreCheckAuthorization(client, collection, STORE_PRIV_READ);
        if (ccode) {
            return ccode;
        }

        ccode = StoreGetExclusiveLock(client, &lock, collection->guid, 3000);
        if (ccode) {
            return ccode;
        }

        client->watch.collection = collection->guid;
        client->watch.flags = flags;
        StoreWatcherAdd(client, lock);
        StoreReleaseExclusiveLock(client, lock);
        StoreReleaseSharedLock(client, lock);
    }

    return ConnWriteStr(client->conn, MSG1000OK);
}


/* if collection is 0 and guid is set, then this function performs a REPLACE.
   startoffset and endoffset must be -1 unless in replace mode
   if version is not -1 and in replace mode, no action is taken if the original
   document version does not match the one provided.
*/

CCode
StoreCommandWRITE(StoreClient *client, 
                  DStoreDocInfo *collection,
                  StoreDocumentType type, 
                  int length, 
                  int startoffset,
                  int endoffset,
                  uint32_t addflags,  /* will be ||'ed with docinfo flags */
                  uint64_t guid,
                  const char *filename,
                  uint64_t timeCreatedUTC,
                  int version,
                  uint64_t linkGuid)
{
    int ccode;
    const char *errmsg;
    DStoreDocInfo info;
    DStoreDocInfo collinfo;     /* used if collection not provided */
    NLockStruct *cLock = NULL;
    int replace = 0;            /* are we in replace mode? */
    char path[XPL_MAX_PATH + 1];
    struct stat sb;
    FILE *fh = NULL;
    int commit = 0;
    int written = 0;            /* did we write to the file? */

    if (StoreAgent.flags & STORE_DISK_SPACE_LOW) {
        return ConnWriteStr(client->conn, MSG5221SPACELOW);
    }

    memset(&info, 0, sizeof(info));
    info.type = type;
    
    if (STORE_IS_FOLDER(type) || STORE_IS_CONVERSATION(type)) {
        return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
    }

    if (collection) {
        info.collection = collection->guid;
    }
    if (guid) {
        switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
        case -1:
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        case 1:
            if (collection) {
                ccode = ConnWriteF(client->conn, 
                                   "4226 Guid " GUID_FMT " already exists\r\n", 
                                   info.guid);
                goto finish;
            } else {
                replace = 1;
            }
            break;
        default:
            if (!collection) {
                /* REPLACE mode requires existing document */
                ccode = ConnWriteStr(client->conn, MSG4220NOGUID);
                goto finish;
            }
            break;
        }
        info.guid = guid;
    }
    /* Rely on the database to enforce uniqueness in the filename */
    snprintf(info.filename, sizeof(info.filename), "%s/%s",
         collection->filename, filename ? filename : "");
    
    if (!collection) {
        if (1 != DStoreGetDocInfoGuid(client->handle, info.collection, &collinfo)) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        } 
        collection = &collinfo;
    }

    if (replace) {
        ccode = StoreCheckAuthorization(client, &info, STORE_PRIV_WRITE_CONTENT);
    } else {
        ccode = StoreCheckAuthorization(client, collection, STORE_PRIV_BIND);
    }
    if (ccode) {
        goto finish;
    }

    if (replace && -1 != version && info.version != version) {
        ccode = ConnWriteStr(client->conn, MSG4227OLDVERSION);
        goto finish;
    }

    ccode = StoreGetCollectionLock(client, &cLock, info.collection);
    if (ccode) {
        goto finish;
    }

    FindPathToDocFile(client, info.collection, path, sizeof(path));
    if (stat(path, &sb)) {
        ccode = ConnWriteStr(client->conn, MSG4224NOMBOX);
        goto finish;
    }

    fh = fopen(path, "a+");
    if (!fh) {
        if (ENOSPC == errno) {
            ccode = ConnWriteStr(client->conn, MSG5220QUOTAERR);
        } else {
            ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        }
        goto finish;
    }

    info.start += info.headerlen;

    info.headerlen = WriteDocumentHeaders(client, fh, NULL, 
                                          info.timeCreatedUTC);

    if (info.headerlen < 0) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    if (-1 != startoffset) {
        if (BongoFileAppendBytes(fh, fh, info.start, info.start + startoffset)) {
            ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
            goto finish;
        }
    }

    if (-1 == (ccode = ConnWrite(client->conn, "2002 Send document.\r\n", 21)) || 
        -1 == (ccode = ConnFlush(client->conn))) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    }
    
    if (length >= 0) {
        ccode = written = ConnReadToFile(client->conn, fh, length);
    } else {
        ccode = written = length = ConnReadToFileUntilEOS(client->conn, fh);
    }
    if (-1 == ccode) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    if (-1 != startoffset) {
        if (BongoFileAppendBytes(fh, fh, 
                                info.start + endoffset, info.start + info.bodylen)) 
        {
            ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
            goto finish;
        }
        info.bodylen -= (endoffset - startoffset);
    } else {
        info.bodylen = 0;
    }

    if (4 != fwrite("\r\n\r\n", sizeof(char), 4, fh)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    ccode = fclose(fh);
    fh = NULL;
    if (ccode) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    info.bodylen += length;
    info.timeCreatedUTC = timeCreatedUTC ? timeCreatedUTC : (uint64_t) time(NULL);
    info.start = sb.st_size;
    info.version++;
    info.flags |= addflags;

    if (STORE_DOCTYPE_MAIL == info.type && replace) {
        uint64_t newguid;
        
        /* generate new imapuid for replace command */
        if (DStoreGenerateGUID(client->handle, &newguid)) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
            goto finish;
        }
        info.imapuid = newguid;
    }

    if (DStoreSetDocInfo(client->handle, &info)) {
        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        goto finish;
    }

    errmsg = StoreProcessDocument(client, &info, collection, path, NULL, linkGuid);
    if (errmsg) {
        ccode = ConnWriteStr(client->conn, errmsg);
        goto finish;
    }

    if (replace && !collection->data.collection.needsCompaction) {
        ++collection->data.collection.needsCompaction;
        (void) DStoreSetDocInfo(client->handle, collection); 

        if (0 == rand() % 25) {
            client->flags |= STORE_CLIENT_FLAG_NEEDS_COMPACTING;
        }
    }

    if (replace) {
        /* Clear an existing mime report */
        DStoreClearMimeReport(client->handle, info.guid);
    }
    
    if (0 != DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    } 

    if (replace) {
        ++client->stats.updates;
        StoreWatcherEvent(client, cLock, STORE_WATCH_EVENT_MODIFIED,
                          info.guid, info.imapuid, 0);
    } else {
        ++client->stats.insertions;
        StoreWatcherEvent(client, cLock, STORE_WATCH_EVENT_NEW,
                          info.guid, info.imapuid, 0);
    }

    ccode = ConnWriteF(client->conn, "1000 " GUID_FMT " %d\r\n", 
                       info.guid, info.version);

    commit = 1;

finish:
    /* NOTE: It is possible for the db and the file to get out of sync here.  This
       will result in a file with wasted space.
    */

    if (fh) {
        fclose(fh);
    }

    if (!commit) {
        DStoreAbortTransaction(client->handle);
        if (written && 0 != XplTruncate(path, sb.st_size)) {
            XplConsolePrintf("Couldn't truncate store file: %s.\r\n", 
                             strerror(errno));
        }

    }

    if (cLock) {
        StoreReleaseExclusiveLock(client, cLock);
        StoreReleaseSharedLock(client, cLock);
    }

    return ccode;
}


/* caller must setup transaction and lock the collection 
   replace mode is not supported
*/


static CCode
WriteHelper(StoreClient *client,
            IndexHandle *index,
            DStoreDocInfo *collection,
            DStoreDocInfo *info,         /* type and collection fields must be set */
            FILE *fh,                    /* collection file opened in append mode */
            char *path,
            int length, 
            uint32_t addflags,
            uint64_t linkGuid,
            StorePropInfo *props,
            int propcount)
{
    CCode ccode;
    int written = 0;
    const char *errmsg;
    int i;

    info->start = ftell(fh);

    info->headerlen = WriteDocumentHeaders(client, fh, NULL, info->timeCreatedUTC);
    if (info->headerlen < 0) {
        return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    }

    if (-1 == (ccode = ConnWrite(client->conn, "2002 Send document.\r\n", 21)) || 
        -1 == (ccode = ConnFlush(client->conn))) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    }
    
    if (length >= 0) {
        written = ConnReadToFile(client->conn, fh, length);
    } else {
        written = length = ConnReadToFileUntilEOS(client->conn, fh);
    }
    if (-1 == written) {
        return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    }

    info->bodylen = length;

    if (4 != fwrite("\r\n\r\n", sizeof(char), 4, fh)) {
        return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    }

    if (fflush(fh)) {
        return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
    }

    assert(0 == info->version);
    info->version = 1;
    info->flags |= addflags;

    if (DStoreSetDocInfo(client->handle, info)) {
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    }

    errmsg = StoreProcessDocument(client, info, collection, path, index, linkGuid);
    if (errmsg) {
        return ConnWriteStr(client->conn, errmsg);
    }

    ConnWriteF(client->conn, "2001 " GUID_FMT " %d\r\n", info->guid, info->version);

    if (propcount && 
        (ccode = StoreCheckAuthorization(client, info, STORE_PRIV_WRITE_PROPS)))
    {
        return ccode;
    }
    
    for (i = 0; i < propcount; i++) {
        StorePropInfo *prop = props + i;
        void *mark = BongoMemStackPeek(&client->memstack);

        prop->value = BONGO_MEMSTACK_ALLOC(&client->memstack, prop->valueLen);
        if (!prop->value) {
            return ConnWriteStr(client->conn, MSG5001NOMEMORY);
        }
        
        if (-1 == (ccode = ConnWriteStr(client->conn, MSG2002SENDVALUE)) ||
            -1 == (ccode = ConnFlush(client->conn)) ||
            -1 == (ccode = ConnReadCount(client->conn, prop->value, prop->valueLen)))
        {
            return ccode;
        }
        prop->value[prop->valueLen] = 0;

        if (STORE_PROP_ACCESS_CONTROL == prop->type) {
            if (StoreParseAccessControlList(client, prop->value)) {
                return ConnWriteStr(client->conn, MSG3025BADACL);
            }
        }
    
        if (STORE_PROP_EVENT_ALARM == prop->type) {
            return ConnWriteStr(client->conn, MSG3244BADPROPNAME);
        }

        if (DStoreSetProperty(client->handle, info->guid, prop)) {
            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }
        
        BongoMemStackPop(&client->memstack, mark);
    }

    return 0;
}


/* replace not allowed */

CCode
StoreCommandMWRITE(StoreClient *client,
                   DStoreDocInfo *collection,
                   StoreDocumentType type)
{
    CCode ccode;
    NLockStruct *cLock = NULL;
    FILE *fh = NULL;
    char path[XPL_MAX_PATH + 1];
    struct stat sb;
    DStoreDocInfo info;
    IndexHandle *index = NULL;
    BongoArray guidlist;
    unsigned int i;

    BongoArrayInit(&guidlist, sizeof(uint64_t), 64);

    if (StoreAgent.flags & STORE_DISK_SPACE_LOW) {
        return ConnWriteStr(client->conn, MSG5221SPACELOW);
    }

    if (STORE_IS_FOLDER(type) || STORE_IS_CONVERSATION(type)) {
        return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
    }

    index = IndexOpen(client);
    if (!index) {
        return ConnWriteStr(client->conn, MSG4120INDEXLOCKED);
    }

    ccode = StoreGetCollectionLock(client, &cLock, collection->guid);
    if (ccode) {
        goto finish;
    }

    FindPathToDocFile(client, collection->guid, path, sizeof(path));
    if (stat(path, &sb)) {
        ccode = ConnWriteStr(client->conn, MSG4224NOMBOX);
        goto finish;
    }

    fh = fopen(path, "a");
    if (!fh) {
        if (ENOSPC == errno) {
            ccode = ConnWriteStr(client->conn, MSG5220QUOTAERR);
        } else {
            ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        }
        goto finish;
    }

    while (ccode >= 0) {
        char *tokens[8];
        int length;
        char *filename = NULL;
        uint64_t guid = 0;
        uint64_t linkguid = 0;
        uint64_t timeCreated = 0;
        unsigned long flags = 0;
        int propcount = 0;
        StorePropInfo props[PROP_ARR_SZ];
        unsigned int n;
        char buffer[CONN_BUFSIZE];

        if (-1 == (ccode = ConnWriteStr(client->conn, MSG2054SENDDOCS)) ||
            -1 == (ccode = ConnFlush(client->conn)) ||
            -1 == (ccode = ConnReadAnswer(client->conn, buffer, sizeof(buffer))))
        {
            goto finish;
        }

        if (!buffer[0]) {
            break;
        }
    
        /* <length> [F<filename>] [G<guid>] 
           [T<time created>] [Z<flags>] [L<link guid>] 
           [P<proplist>]
        */

        n = BongoStringSplit(buffer, ' ', tokens, 8);
        if (TOKEN_OK != (ccode = CheckTokC(client, n, 1, 6)) ||
            TOKEN_OK != (ccode = ParseStreamLength(client, tokens[0], &length)))
        {
            continue;
        }

        for (i = 1; i < n; i++) {
            if ('G' == *tokens[i] && !guid) {
                ccode = ParseGUID(client, 1 + tokens[i], &guid);
            } else if ('L' == *tokens[i] && !linkguid) {
                ccode = ParseDocument(client, 1 + tokens[i], &linkguid);
            } else if ('F' == *tokens[i] && tokens[i][1] != '\0') {
                filename = tokens[i] + 1;
            } else if ('P' == *tokens[i] && !propcount) {
                ccode = ParsePropList(client, tokens[i] + 1, 
                                      props, PROP_ARR_SZ, &propcount, 1);
            } else if ('T' == *tokens[i] && !timeCreated) {
                ccode = ParseDateTimeToUint64(client, 1 + tokens[i], &timeCreated); 
            } else if ('Z' == *tokens[i] && !flags) {
                ccode = ParseUnsignedLong(client, tokens[i] + 1, &flags);
            } else {
                ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
            }
            if (TOKEN_OK != ccode) {
                break;
            }
        }
        if (TOKEN_OK != ccode) {
            continue;
        }

        memset(&info, 0, sizeof(info));
        info.type = type;
        info.collection = collection->guid;

        if (guid) {
            switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
            case -1:
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                goto finish;
            case 1:
                ccode = ConnWriteF(client->conn, 
                                   "4226 Guid " GUID_FMT " already exists\r\n", 
                                   info.guid);
                continue;
            default:
                info.guid = guid;
                break;
            }
        } 
        
        snprintf(info.filename, sizeof(info.filename), "%s/%s",
                 collection->filename, filename ? filename : "");

        info.timeCreatedUTC = timeCreated ? timeCreated : (uint64_t) time(NULL);

        ccode = WriteHelper(client, index,
                            collection, &info,
                            fh, path,
                            length,
                            flags, linkguid,
                            props, propcount);

        if (ccode) {
            goto finish;
        }
        
        BongoArrayAppendValue(&guidlist, info.guid);
        
    }

    ccode = fclose(fh);
    fh = NULL;
    if (ccode) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    }

    if (DStoreCommitTransaction(client->handle)) {
        ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
        goto finish;
    } 

    for (i = 0; i < guidlist.len; i++) {
        ++client->stats.insertions;
        StoreWatcherEvent(client, cLock, STORE_WATCH_EVENT_NEW,
                          BongoArrayIndex(&guidlist, uint64_t, i),
                          BongoArrayIndex(&guidlist, uint64_t, i), /* imapid is same */
                          0);
    }

    ccode = ConnWriteStr(client->conn, MSG1000OK);

finish:

    if (fh) {
        fclose(fh);
    }

    if (cLock) {
        StoreReleaseExclusiveLock(client, cLock);
        StoreReleaseSharedLock(client, cLock);
    }

    if (index) {
        IndexClose(index);
    }

    BongoArrayDestroy(&guidlist, TRUE);

    return ccode;
}


