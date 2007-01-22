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

/* command functions   included by stored.h */
#ifndef COMMAND_H
#define COMMAND_H

#include <xpl.h>

XPL_BEGIN_C_LINKAGE

typedef enum {
    STORE_COMMAND_NULL = 0,

    /* session commands */
    STORE_COMMAND_AUTH, /* COOKIE, USER or SYSTEM */
    STORE_COMMAND_COOKIE, /* NEW or DELETE */
    STORE_COMMAND_IDENTITY,
    STORE_COMMAND_MANAGE,
    STORE_COMMAND_QUIT,
    STORE_COMMAND_STORE,
    STORE_COMMAND_TOKEN,
    STORE_COMMAND_TOKENS,
    STORE_COMMAND_USER,

    /* collection commands */
    STORE_COMMAND_COLLECTIONS,
    STORE_COMMAND_CREATE,
    STORE_COMMAND_MAILINGLISTS,
    STORE_COMMAND_REMOVE,
    STORE_COMMAND_RENAME,
    STORE_COMMAND_SEARCH,
    STORE_COMMAND_STATUS,
    STORE_COMMAND_WATCH,

    /* document commands */
    STORE_COMMAND_COPY,
    STORE_COMMAND_DELETE,
    STORE_COMMAND_INDEXED,
    STORE_COMMAND_FLAG,
    STORE_COMMAND_INFO,
    STORE_COMMAND_LIST,
    STORE_COMMAND_MFLAG,
    STORE_COMMAND_MIME,
    STORE_COMMAND_MESSAGES,
    STORE_COMMAND_META,
    STORE_COMMAND_MOVE,
    STORE_COMMAND_MWRITE,
    STORE_COMMAND_PROPGET,
    STORE_COMMAND_PROPSET,
    STORE_COMMAND_READ,
    STORE_COMMAND_REPLACE,
    STORE_COMMAND_SALVAGE,
    STORE_COMMAND_UNINDEXED,
    STORE_COMMAND_WRITE,

    /* calendar commands */
    STORE_COMMAND_CALENDARS,
    STORE_COMMAND_EVENTS,
    STORE_COMMAND_LINK,
    STORE_COMMAND_UNLINK,

    /* conversation commands */
    STORE_COMMAND_CONVERSATION,
    STORE_COMMAND_CONVERSATIONS,

    /* indexing commands */
    STORE_COMMAND_ISEARCH,

    /* delivery commands */
    STORE_COMMAND_DELIVER,

    /* alarm commands */
    STORE_COMMAND_ALARMS,

    /* misc. commands */
    STORE_COMMAND_NOOP,
    STORE_COMMAND_TIMEOUT,

    /* debugging commands */
    STORE_COMMAND_REINDEX,
    STORE_COMMAND_RESET,
} StoreCommand;

typedef enum {
    STORE_FLAG_SHOW = 0,
    STORE_FLAG_ADD,
    STORE_FLAG_REMOVE,
    STORE_FLAG_REPLACE,
} StoreFlagMode;

typedef struct {
    enum {
        STORE_SEARCH_BODY,
        STORE_SEARCH_TEXT,
        STORE_SEARCH_HEADER,
        STORE_SEARCH_HEADERS,
    } type;
    
    char *header;
    char *query;
} StoreSearchInfo;


CCode StoreCommandALARMS(StoreClient *client, uint64_t dt_start, uint64_t dt_end);

CCode StoreCommandAUTHCOOKIE(StoreClient *client, 
                             char *user, char *token, int nouser);

CCode StoreCommandAUTHSYSTEM(StoreClient *client, char *md5hash);

CCode StoreCommandAUTHUSER(StoreClient *client, 
                           char *username, char *passwd, int nouser);

CCode StoreCommandCALENDARS(StoreClient *client, unsigned long mask,
                            StorePropInfo *props, int propcount);

CCode StoreCommandCOLLECTIONS(StoreClient *client, DStoreDocInfo *collections);

CCode StoreCommandCONVERSATION(StoreClient *client, uint64_t guid,
                               StorePropInfo *props, int propcount);

CCode StoreCommandCONVERSATIONS(StoreClient *client, const char *source, 
                                const char *query, 
                                int start, int end, uint64_t center, 
                                uint32_t flagsmask, uint32_t flags,
                                int displayTotal,
                                StoreHeaderInfo *headers, int headercount, 
                                StorePropInfo *props, int propcount);

CCode StoreCommandCOOKIENEW(StoreClient *client, uint64_t timeout);

CCode StoreCommandCOOKIEDELETE(StoreClient *client, const char *token);

CCode StoreCommandCOPY(StoreClient *client, uint64_t guid, DStoreDocInfo *collection);

CCode StoreCommandCREATE(StoreClient *client, char *collection, uint64_t guid);

CCode StoreCommandDELETE(StoreClient *client, uint64_t guid);

CCode StoreCommandDELIVER(StoreClient *client, char *sender, char *authSender,
                          char *filename, size_t bytes);

CCode StoreCommandEVENTS(StoreClient *client, char *startUTC, char *endUTC, 
                         DStoreDocInfo *calendar, unsigned int mask, char *uid,
                         const char *query, int start, int end,
                         StorePropInfo *props, int propcount);

CCode StoreCommandFLAG(StoreClient *client, uint64_t guid, uint32_t change, int mode);

CCode StoreCommandINDEXED(StoreClient *client, int value, uint64_t guid);

CCode StoreCommandINFO(StoreClient *client, uint64_t guid,
                       StorePropInfo *props, int propcount);

CCode StoreCommandINSET(StoreClient *client, int value, uint64_t guid);

CCode StoreCommandISEARCH(StoreClient *client, const char *query, int start, int end,
                          StorePropInfo *props, int propcount);


CCode StoreCommandLINK(StoreClient *client, uint64_t calendar, uint64_t guid);

CCode StoreCommandLIST(StoreClient *client, 
                       DStoreDocInfo *collection, int start, int end, 
                       uint32_t flagsmask, uint32_t flags,
                       StorePropInfo *props, int propcount);

CCode StoreCommandMAILINGLISTS(StoreClient *client, char *source);

CCode StoreCommandMANAGE(StoreClient *client);

CCode StoreCommandMESSAGES(StoreClient *client,
                           const char *source, 
                           const char *query,
                           int start, int end, uint64_t center,
                           uint32_t flagsmask, uint32_t flags,
                           int displayTotal,
                           StoreHeaderInfo *headers, int headercount,
                           StorePropInfo *props, int propcount);

CCode StoreCommandMFLAG(StoreClient *client, uint32_t change, int mode);

CCode StoreCommandMIME(StoreClient *client, uint64_t guid);

CCode StoreCommandMOVE(StoreClient *client, uint64_t guid, 
                       DStoreDocInfo *collection, const char *filename);

CCode StoreCommandMWRITE(StoreClient *client, 
                         DStoreDocInfo *collection, StoreDocumentType type);

CCode StoreCommandPROPGET(StoreClient *client, uint64_t guid, 
                          StorePropInfo *props, int propcount);

CCode StoreCommandPROPSET(StoreClient *client, uint64_t guid, 
                        StorePropInfo *prop);

CCode StoreCommandQUIT(StoreClient *client);

CCode StoreCommandREAD(StoreClient *client, uint64_t guid, 
                       int64_t requestStart, int64_t requestLength);

CCode StoreCommandREINDEX(StoreClient *client, uint64_t guid);

CCode StoreCommandRENAME(StoreClient *client, DStoreDocInfo *collection,
                         char *newfilename);

CCode StoreCommandREMOVE(StoreClient *client, DStoreDocInfo *collection);

CCode StoreCommandRESET(StoreClient *client);

CCode StoreCommandSEARCH(StoreClient *client, uint64_t guid, StoreSearchInfo *query);

CCode StoreCommandSTORE(StoreClient *client, char *user);

CCode StoreCommandTIMEOUT(StoreClient *client, int timeout);

CCode StoreCommandTOKEN(StoreClient *client, char *token);

CCode StoreCommandTOKENS(StoreClient *client);

CCode StoreCommandUNFO(StoreClient *client);

CCode StoreCommandUNINDEXED(StoreClient *client, int value, uint64_t guid);

CCode StoreCommandUNLINK(StoreClient *client, uint64_t calendar, uint64_t guid);

CCode StoreCommandUNSET(StoreClient *client);

CCode StoreCommandUSER(StoreClient *client, char *user, char *password, int nouser);

CCode StoreCommandWATCH(StoreClient *client, DStoreDocInfo *collection, int flags);

CCode StoreCommandWRITE(StoreClient *client, 
                        DStoreDocInfo *collection, StoreDocumentType type, 
                        int length, int startoffset, int endoffset,
                        uint32_t addflags, uint64_t guid, const char *filename,
                        uint64_t timeCreatedUTC, int requiredVersion, uint64_t calendar);

XPL_END_C_LINKAGE

#endif
