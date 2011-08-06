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
#include <unistd.h>
#include <stdio.h>

#include "stored.h"
#include "command-parsing.h"
#include "object-model.h"
#include "calendar.h"

#include "mail.h"
#include "messages.h"
#include "mime.h"

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

// internal prototypes
CCode ReceiveToFile(StoreClient *client, const char *path, uint64_t *size);

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

#define CHECK_NOT_READONLY(x)	{ CCode ret = IsStoreReadonly((x)); if (ret != 0) return ret; }

static CCode
IsStoreReadonly(StoreClient *client)
{
	if (client->readonly) {
		ConnWriteStr(client->conn, client->ro_reason);
		return 4250;
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
	BongoHashtablePutNoReplace(CommandTable, "ACCOUNT", (void *) STORE_COMMAND_ACCOUNT) ||
        BongoHashtablePutNoReplace(CommandTable, "AUTH", (void *) STORE_COMMAND_AUTH) ||
        BongoHashtablePutNoReplace(CommandTable, "COOKIE", (void *) STORE_COMMAND_COOKIE) ||
        BongoHashtablePutNoReplace(CommandTable, "QUIT", (void *) STORE_COMMAND_QUIT) ||
        BongoHashtablePutNoReplace(CommandTable, "STORE", (void *) STORE_COMMAND_STORE) ||
        BongoHashtablePutNoReplace(CommandTable, "STORES", (void *) STORE_COMMAND_STORES) ||
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
        BongoHashtablePutNoReplace(CommandTable, "REPAIR", (void *) STORE_COMMAND_REPAIR) ||

        /* document commands */
        BongoHashtablePutNoReplace(CommandTable, "COPY", (void *) STORE_COMMAND_COPY) ||
        BongoHashtablePutNoReplace(CommandTable, "DELETE", (void *) STORE_COMMAND_DELETE) ||
        BongoHashtablePutNoReplace(CommandTable, "FLAG", (void *) STORE_COMMAND_FLAG) ||
        BongoHashtablePutNoReplace(CommandTable, "INFO", (void *) STORE_COMMAND_INFO) ||
        BongoHashtablePutNoReplace(CommandTable, "LINK", (void *) STORE_COMMAND_LINK) ||
        BongoHashtablePutNoReplace(CommandTable, "LINKS", (void *) STORE_COMMAND_LINKS) ||
        BongoHashtablePutNoReplace(CommandTable, "UNLINK", (void *) STORE_COMMAND_UNLINK) ||
        BongoHashtablePutNoReplace(CommandTable, "MESSAGES ",  (void *) STORE_COMMAND_MESSAGES) ||
        BongoHashtablePutNoReplace(CommandTable, "MIME ",  (void *) STORE_COMMAND_MIME) ||
        BongoHashtablePutNoReplace(CommandTable, "MOVE ", (void *) STORE_COMMAND_MOVE) ||
        BongoHashtablePutNoReplace(CommandTable, "PROPGET", (void *) STORE_COMMAND_PROPGET) ||
        BongoHashtablePutNoReplace(CommandTable, "PROPSET", (void *) STORE_COMMAND_PROPSET) ||
        BongoHashtablePutNoReplace(CommandTable, "PURGE", (void *) STORE_COMMAND_DELETE) ||
        BongoHashtablePutNoReplace(CommandTable, "READ", (void *) STORE_COMMAND_READ) ||
        BongoHashtablePutNoReplace(CommandTable, "REPLACE", (void *) STORE_COMMAND_REPLACE) ||
        BongoHashtablePutNoReplace(CommandTable, "WRITE", (void *) STORE_COMMAND_WRITE) ||

        /* conversation commands */
        BongoHashtablePutNoReplace(CommandTable, "CONVERSATION", 
                         (void *) STORE_COMMAND_CONVERSATION) ||
        BongoHashtablePutNoReplace(CommandTable, "CONVERSATIONS", 
                         (void *) STORE_COMMAND_CONVERSATIONS) ||

        /* index commands */
        BongoHashtablePutNoReplace(CommandTable, "ISEARCH", (void *) STORE_COMMAND_ISEARCH) ||

        /* calendar commands */
        BongoHashtablePutNoReplace(CommandTable, "CALENDARS", 
                         (void *) STORE_COMMAND_CALENDARS) ||
        BongoHashtablePutNoReplace(CommandTable, "EVENTS", (void *) STORE_COMMAND_EVENTS) ||

        /* alarm commands */

        BongoHashtablePutNoReplace(CommandTable, "ALARMS", (void *) STORE_COMMAND_ALARMS) ||

        /* other */
        BongoHashtablePutNoReplace(CommandTable, "ACL", (void *) STORE_COMMAND_ACL) ||
        BongoHashtablePutNoReplace(CommandTable, "NOOP", (void *) STORE_COMMAND_NOOP) || 
        BongoHashtablePutNoReplace(CommandTable, "TIMEOUT", (void *) STORE_COMMAND_TIMEOUT) || 

        /* debugging commands */
        
        BongoHashtablePutNoReplace(CommandTable, "REINDEX", (void *) STORE_COMMAND_REINDEX) ||
        BongoHashtablePutNoReplace(CommandTable, "RESET", (void *) STORE_COMMAND_RESET) ||
        BongoHashtablePutNoReplace(CommandTable, "SHUTDOWN", (void *) STORE_COMMAND_SHUTDOWN) ||

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
	return client->storedb ? TOKEN_OK 
                         : ConnWriteStr(client->conn, MSG3243NOSTORE);
}


static CCode
RequireNoStore(StoreClient *client)
{
	return client->storedb ? ConnWriteStr(client->conn, MSG3247STORE)
                          : TOKEN_OK;
}


static CCode
RequireManager(StoreClient *client)
{
    return IS_MANAGER(client) ? TOKEN_OK
                              : ConnWriteStr(client->conn, MSG3243MANAGERONLY);
}

static CCode
RequireAdmin(StoreClient *client)
{
    if (IS_MANAGER(client)) return TOKEN_OK;
    if (HAS_IDENTITY(client)) {
        if (! XplStrCaseCmp(client->principal.name, "admin")) return TOKEN_OK;
    }
    return ConnWriteStr(client->conn, MSG3243MANAGERONLY);
}

static CCode
RequireIdentity(StoreClient *client)
{
    return (IS_MANAGER(client) || HAS_IDENTITY(client)) 
        ? TOKEN_OK
        : ConnWriteStr(client->conn, MSG3241NOUSER);
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
        char command_message[256];
        if (XplStrNCaseCmp("AUTH", client->buffer, 4) == 0) {
            strcpy(command_message, "-> AUTH .....");
        } else {
            snprintf(command_message, 255, "-> %s", client->buffer);
        }
        Ringlog(command_message);
        
        memset(tokens, 0, sizeof(tokens));
        n = CommandSplit(client->buffer, tokens, TOK_ARR_SZ);
        if (0 == n) {
            command = STORE_COMMAND_NULL;
        } else {
            command = (StoreCommand) BongoHashtableGet(CommandTable, tokens[0]);
        }
                
        if (client->watch.collection.guid > 0) {
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
            StoreObject object;
            StoreObject collection;
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
        case STORE_COMMAND_ACCOUNT:
            /* ACCOUNT CREATE <account> */
            /* ACCOUNT DELETE <account> */
            /* ACCOUNT LIST */
            
            query = NULL; // who
            
            if (TOKEN_OK == (ccode = RequireAdmin(client)) &&
                TOKEN_OK == (ccode = RequireNoStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 0, 6))) 
            {
                if (!XplStrCaseCmp(tokens[1], "list")) {
                    ccode = AccountList(client);
                }
                
                if (!XplStrCaseCmp(tokens[1], "create")) {
                    ccode = AccountCreate(client, tokens[2], NULL);
                }
                
                if (!XplStrCaseCmp(tokens[1], "delete")) {
                    ccode = AccountDelete(client, tokens[2]);
                }
            }
            break;
        case STORE_COMMAND_ACL:
            /* ACL GRANT <document> P<priv> T<principal> [W<who>] */
            /* ACL DENY <document> P<priv> T<principal> [W<who>] */
            /* ACL LIST [<document>] */
            /* ACL REMOVE <document> P<priv> [T<principal>] [W<who>] */
            
            int1 = 0; // priv
            int2 = 0; // principal
            query = NULL; // who
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 0, 6))) 
            {
                StorePrincipalType type = 0;
                StorePrivilege priv = 0;
                char *who = NULL;

                for (i = 3; TOKEN_OK == ccode && i < n; i++) {
                    if ('P' == *tokens[i]) {
                        ccode = ParsePrivilege(client, tokens[i] + 1, &priv);
                    } else if ('T' == *tokens[i]) {
                        ccode = ParsePrincipal(client, tokens[i] + 1, &type);
                    } else if ('W' == *tokens[i]) {
                        who = tokens[i] + 1;
                        ccode = TOKEN_OK;
                    } else {
                        // 'bad' token - something we don't understand.
                        ccode = 0;
                    }
                    if (ccode != TOKEN_OK) {
                        ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    }
                }
                if (ccode != TOKEN_OK) break;
                
                if (!XplStrCaseCmp(tokens[1], "list")) {
                    if ((n == 3) && ParseDocument(client, tokens[2], &object) == TOKEN_OK) {
                        ccode = StoreObjectListACL(client, &object);
                        break;
                    } else {
                        ccode = StoreObjectListACL(client, NULL);
                        break;
                    }
                } else {
                     if (n < 3) {
                        ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                     } else if (ParseDocument(client, tokens[2], &object) != TOKEN_OK) {
                        ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                     }
                }
                
                if (!XplStrCaseCmp(tokens[1], "remove")) {
                    if (priv == 0) {
                        ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    } else {
                        if (StoreObjectRemoveACL(client, &object, type, priv, who) == 0) {
                            ccode = ConnWriteStr(client->conn, MSG1000OK);
                        } else {
                            ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                        }
                    }
                    break;
                } else {
                    BOOL deny;
                    if (! XplStrCaseCmp(tokens[1], "grant")) {
                        deny = FALSE;
                    } else if (! XplStrCaseCmp(tokens[1], "deny")) {
                        deny = TRUE;
                    } else {
                        ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                        break;
                    }
                    if ((priv == 0) || (type == 0) || (who == NULL)) {
                        ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                        break;
                    }
                    if (StoreObjectAddACL(client, &object, type, priv, deny, who) == 0) {
                        ccode = ConnWriteStr(client->conn, MSG1000OK);
                    } else {
                        ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                    }
                    break;
                }
            }
            break;
        case STORE_COMMAND_ALARMS:
            /* ALARMS <starttime> <endtime> */
            
            if (TOKEN_OK == (ccode = RequireManager(client)) &&
                TOKEN_OK == (ccode = RequireNoStore(client)) &&
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
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 3))) 
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
                TOKEN_OK == (ccode = ParseCollection(client, tokens[1], &object)))
            {
                ccode = StoreCommandCOLLECTIONS(client, &object);
            }
            break;

        case STORE_COMMAND_CONVERSATIONS:
            /* CONVERSATIONS [Ssource] [Rxx-xx] [Q<query>] [P<proplist>] 
                             [C<center document>] [T] [F<flags>] [M<flags mask>]
                             [H<headerlist>]
            */

            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 10)))
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
                    ccode = ParseDocument(client, tokens[i] + 1, &object);
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
                                              int1, int2, &object, 
                                              (uint32_t) ulong, (uint32_t) ulong2, 
                                              int4,
                                              hdrs, hdrcnt,
                                              props, int3);
            break;

        case STORE_COMMAND_CONVERSATION:
            /* CONVERSATION <conversation> [P<proplist>] */

            guid = 0;
            int3 = 0; /* propcount */
            if (TOKEN_OK != (ccode = RequireStore(client))) break;
            if (TOKEN_OK != (ccode = CheckTokC(client, n, 2, 3))) break;
            if (TOKEN_OK != (ccode = ParseDocument(client, tokens[1], &object))) break;
            
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
                ccode = StoreCommandCONVERSATION(client, &object, props, int3);
            }
            break;

        case STORE_COMMAND_COOKIE:
            /* COOKIE [BAKE | NEW] <timeout> */
            /* COOKIE [CRUMBLE | DELETE] [<token>] */
            /* COOKIE LIST */
            
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
            } else if (!XplStrCaseCmp(tokens[1], "LIST")) {
                ccode = StoreCommandCOOKIELIST(client);
            } else {
                ccode = ConnWriteStr(client->conn, MSG3000UNKNOWN);
            }
            break;

        case STORE_COMMAND_COPY:
            /* COPY <document> <collection> */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[2], &collection)))
            {
                ccode = StoreCommandCOPY(client, &object, &collection);
            }
            break;

        case STORE_COMMAND_CREATE:
            /* CREATE <collection name> [<guid>] */

            guid = 0;
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 3)) &&
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
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object)))
            {
                ccode = StoreCommandDELETE(client, &object);
            }
            break;

        case STORE_COMMAND_EVENTS:
            /* EVENTS [D<daterange>] [C<calendar> | U<uid>] 
               [F<mask>] [Q<query>] [P<proplist>] */

            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 6)))
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
                    ccode = ParseDocumentInfo(client, tokens[i] + 1, &object);
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
                                       int2 ? &object : NULL, 
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
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object)) &&
                (n < 3 || TOKEN_OK == (ccode = ParseFlag(client, tokens[2], &int1, &int2)))) 
                
            {
                ccode = StoreCommandFLAG(client, &object, int1, int2);
            }
            break;

        case STORE_COMMAND_INFO:
            /* INFO <document> [P<proplist>] */
            
            guid = 0;
            int1 = 0; /* proplist len */
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 2, 3)) ||
                TOKEN_OK != (ccode = ParseDocument(client, tokens[1], &object)))
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
                ccode = StoreCommandINFO(client, &object, props, int1);
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
            /* LINK <document> <document> 
             * abuses collection - first argument should be another document*/
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &collection)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[2], &object)))
            {
                ccode = StoreCommandLINK(client, &collection, &object);
            }
            break;
        
        case STORE_COMMAND_LINKS:
            /* LINKS <document> */
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[2], &object)))
            {
                BOOL reverse_links;
                if (!XplStrCaseCmp(tokens[1], "TO")) {
                    reverse_links = TRUE;
                } else if (!XplStrCaseCmp(tokens[1], "FROM")) {
                    reverse_links = FALSE;
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    break;
                }
                
                ccode = StoreCommandLINKS(client, reverse_links, &object);
            }
            break; 

        case STORE_COMMAND_LIST:
            /* LIST <collection> [Rxx-xx] [P<proplist>] [M<flags mask>] [F<flags>] [Q<query>] */

            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 2, 6)) ||
                TOKEN_OK != (ccode = ParseCollection(client, tokens[1], &object))) 
            {
                break;
            }

            int1 = int2 = -1; /* range */
            int3 = 0;   /* proplist len */
            ulong = 0;  /* flags mask */
            ulong2 = 0; /* flags */
            query = NULL; /* query */
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
                } else if ('Q' == *tokens[i]) {
                    query = tokens[i] + 1;
                    ccode = TOKEN_OK;
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
            }
                
            if (TOKEN_OK != ccode) {
                break;
            }
            
            ccode = StoreCommandLIST(client, &object, int1, int2, 
                                     (uint32_t) ulong, (uint32_t) ulong2, 
                                     props, int3, query);
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
                TOKEN_OK != (ccode = CheckTokC(client, n, 1, 10)))
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
                    ccode = ParseDocument(client, tokens[i] + 1, &object);
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
                                         int1, int2, &object, 
                                         (uint32_t) ulong, (uint32_t) ulong2, 
                                         int4,
                                         hdrs, hdrcnt,
                                         props, int3);
            break;

        case STORE_COMMAND_MIME:
            /* MIME <document> */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 2)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object)))
            {
                ccode = StoreCommandMIME(client, &object);
            }
            break;

        case STORE_COMMAND_MOVE:
            /* MOVE <document> <collection> [<filename>] */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 4)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[2], &collection)))
            {
                ccode = StoreCommandMOVE(client, &object, &collection, 
                                         4 == n ? tokens[3] : NULL);
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
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object)) &&
                (2 == n || 
                 TOKEN_OK == (ccode = ParsePropList(client, tokens[2], 
                                                    props, PROP_ARR_SZ, &int3, 0))))
            {
                ccode = StoreCommandPROPGET(client, &object, props, int3);
            }
            break;

        case STORE_COMMAND_PROPSET:
            /* PROPSET <document> <propname> <length> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 4, 4)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object)) &&
                TOKEN_OK == (ccode = ParsePropName(client, tokens[2], &prop)) &&
                TOKEN_OK == (ccode = ParseInt(client, tokens[3], &int1)))
                
            {
                prop.valueLen = int1;
                ccode = StoreCommandPROPSET(client, &object, &prop, int1);
            }
            break;

        case STORE_COMMAND_QUIT:
            /* QUIT */

            ccode = StoreCommandQUIT(client);
            break;

        case STORE_COMMAND_READ:
            /* READ <document> [<start> [<length>]] */
            // FIXME: need to get signedness etc. right on these args
            int1 = -1;
            int2 = -1;
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 4)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object)) &&
                (n < 3 || TOKEN_OK == (ccode = ParseInt(client, tokens[2], &int1))) &&
                (n < 4 || TOKEN_OK == (ccode = ParseInt(client, tokens[3], &int2))))
            {
                ccode = StoreCommandREAD(client, &object, int1, int2);
            }
            break;

        case STORE_COMMAND_REINDEX:
            /* REINDEX [<document>] */
            
            guid = 0;
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 1, 2)) &&
                (n < 2 || 
                 TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &object))))
            {
                ccode = StoreCommandREINDEX(client, &object);
            }
            break;

        case STORE_COMMAND_RENAME:
            /* RENAME <collection> <new name> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[1], &object)))
            {
                ccode = StoreCommandRENAME(client, &object, tokens[2]);
            }
            break;

        case STORE_COMMAND_REMOVE:
            /* REMOVE <collection> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 2, 2)) &&
                TOKEN_OK == (ccode = ParseCollection(client, tokens[1], &object)))
            {
                ccode = StoreCommandREMOVE(client, &object);
            }
            break;

        case STORE_COMMAND_REPAIR:
            /* REPAIR */
            
            if (TOKEN_OK == (ccode = RequireStore(client))) 
            {
                ccode = StoreCommandREPAIR(client);
            }
            break;

        case STORE_COMMAND_REPLACE:
            /* REPLACE <document> <length> [R<xx-xx>] [V<version>] 
                       [L<link document>]*/

            int2 = 0; /* range start */
            int3 = 0; /* range end */
            int4 = -1; /* version */
            guid2 = 0;
            
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 3, 6)) ||
                TOKEN_OK != (ccode = ParseDocument(client, tokens[1], &object)) ||
                TOKEN_OK != (ccode = ParseStreamLength(client, tokens[2], &int1)))
            {
                break;
            }

            for (i = 3; TOKEN_OK == ccode && i < n; i++) {
                if ('R' == *tokens[i] && -1 == int2) {
                    ccode = ParseRange(client, tokens[i] + 1 , &int2, &int3);
                } else if ('V' == *tokens[i] && -1 == int4) {
                    ccode = ParseInt(client, tokens[i] + 1, &int4);
                //} else if ('L' == *tokens[i] && !guid2) {
                //    ccode = ParseDocument(client, 1 + tokens[i], &guid2);
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                }
            }
            if (TOKEN_OK == ccode) {
            	ccode = StoreCommandREPLACE(client, &object, int1, int2, int3, int4);
            }
            break;
            
        case STORE_COMMAND_RESET:
            /* RESET */

            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 1, 1)))
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
                TOKEN_OK != (ccode = ParseDocument(client, tokens[1], &object)))
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

                ccode = StoreCommandSEARCH(client, object.guid, &search);
            }
            break;

        case STORE_COMMAND_SHUTDOWN:
            /* SHUTDOWN */
            exit(0);
            break;
        
        case STORE_COMMAND_STORES:
            if (TOKEN_OK == (ccode = RequireIdentity(client))) {
                ccode = StoreCommandSTORES(client);
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
            /* UNLINK <document> <document> */
            
            if (TOKEN_OK == (ccode = RequireStore(client)) &&
                TOKEN_OK == (ccode = CheckTokC(client, n, 3, 3)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[1], &collection)) &&
                TOKEN_OK == (ccode = ParseDocument(client, tokens[2], &object)))
            {
                ccode = StoreCommandUNLINK(client, &collection, &object);
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
                TOKEN_OK != (ccode = ParseCollection(client, tokens[1], &object)))
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
                ccode = StoreCommandWATCH(client, &object, int1);
            }
            break;

        case STORE_COMMAND_WRITE:
            /* WRITE <collection> <type> <length> 
                     [F<filename>] [G<guid>] [T<timeCreated>] 
                     [Z<flags>] [L<link guid>] [N]
             */
            
            if (TOKEN_OK != (ccode = RequireStore(client)) ||
                TOKEN_OK != (ccode = CheckTokC(client, n, 4, 9)) ||
                TOKEN_OK != (ccode = ParseCollection(client, tokens[1], &object)) ||
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
            int2 = 0; /* whether or not to process the document */

            for (i = 4; i < n; i++) {
                if ('G' == *tokens[i] && !guid) {
                    ccode = ParseGUID(client, 1 + tokens[i], &guid);
                //} else if ('L' == *tokens[i] && !guid2) {
                //    ccode = ParseDocument(client, 1 + tokens[i], &guid2);
                } else if ('F' == *tokens[i] && tokens[i][1] != '\0') {
                    filename = tokens[i] + 1;
                } else if ('N' == *tokens[i] && tokens[i][1] == '\0') {
                    int2 = 1;
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

            ccode = StoreCommandWRITE(client, &object, doctype, int1,
                                      (uint32_t) ulong, guid, filename, 
                                      timestamp, guid2, int2);
            break;

        default:
            break;
        }

        if (ccode >= 0) {
            ccode = ConnFlush(client->conn);
        }
    
        command_message[0] = '<';
        command_message[1] = '-';
        Ringlog(command_message);
    }
    
    
    return ccode;
}

static CCode
ShowDocumentBody(StoreClient *client, StoreObject *document,
                 int64_t requestStart, uint64_t requestLength)
{
    int ccode = 0;
    char path[XPL_MAX_PATH + 1];
    FILE *fh = NULL;
    uint64_t start;
    uint64_t length;

    if (STORE_IS_FOLDER(document->type)) {
        return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
    } else {
        FindPathToDocument(client, document->collection_guid, document->guid, path, sizeof(path));
        fh = fopen(path, "rb");
        if (!fh)
            return ConnWriteStr(client->conn, MSG4224CANTREAD);

        if (requestStart < 0) {
            start = 0;
            length = document->size;
        } else {
            if ((uint64_t)requestStart < document->size) {
                start = requestStart;
                length = document->size - start;
                if ((requestLength < length) && (requestLength != 0)) {
                    length = requestLength; 
                }
            } else {
                start = document->size;
                length = 0;
            }
        }

        ccode = ConnWriteF(client->conn, "2001 nmap.document %ld\r\n", (long) length);
        if (-1 == ccode) goto finish;
        
        if (0 != XplFSeek64(fh, start, SEEK_SET)) {
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

CCode
StoreCommandALARMS(StoreClient *client, uint64_t start, uint64_t end)
{
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(start);
	UNUSED_PARAMETER_REFACTOR(end);
	/* FIXME - redo this completely
	 * 
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
	*/
	return ConnWriteStr(client->conn, MSG1000OK);
}

CCode
StoreCommandCALENDARS(StoreClient *client, unsigned long mask,
                      StorePropInfo *props, int propcount)
{
	StoreObject calendar_collection;
	CCode ccode;
	
	UNUSED_PARAMETER_REFACTOR(mask);
	
	StoreObjectFind(client, STORE_CALENDARS_GUID, &calendar_collection);
	ccode = StoreObjectCheckAuthorization(client, &calendar_collection, STORE_PRIV_LIST);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	return StoreObjectIterCollectionContents(client, &calendar_collection, -1, 
		-1, 0, 0, props, propcount, "= nmap.type 6", NULL, FALSE);
}

// list the collections who are subcollections of container
// [LOCKING] Collections(X) -> RoLock(X)
CCode
StoreCommandCOLLECTIONS(StoreClient *client, StoreObject *container)
{
	CCode ccode;
	
	ccode = StoreObjectCheckAuthorization(client, container, STORE_PRIV_LIST);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	if (LogicalLockGain(client, container, LLOCK_READONLY, "StoreCommandCOLLECTIONS")) {
		ccode = StoreObjectIterSubcollections(client, container);
		LogicalLockRelease(client, container, LLOCK_READONLY, "StoreCommandCOLLECTIONS");
		return ccode;
	} else {
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	}
}

// FIXME : needs logical locking 
CCode
StoreCommandCONVERSATION(StoreClient *client, StoreObject *conversation,
                         StorePropInfo *props, int propcount)
{
	CCode ccode = 0;
	StoreObject conversation_collection;
	
	StoreObjectFind(client, STORE_CONVERSATIONS_GUID, &conversation_collection);
	ccode = StoreObjectCheckAuthorization(client, &conversation_collection, STORE_PRIV_READ);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	return StoreObjectIterConversationMails(client, conversation, props, propcount);
}

// FIXME: Need to implement support for 'center' properly.
// FIXME: needs logical locking
// The headers options can take a running jump, though.
CCode
StoreCommandCONVERSATIONS(StoreClient *client, const char *source, const char *query, 
                          int start, int end, StoreObject *center, 
                          uint32_t flagsmask, uint32_t flags,
                          int displayTotal,
                          StoreHeaderInfo *headers, int headercount, 
                          StorePropInfo *props, int propcount)
{
	int ccode;
	StoreObject conversation_collection;
	BOOL show_total;
	
	UNUSED_PARAMETER_REFACTOR(headers);
	UNUSED_PARAMETER_REFACTOR(headercount);
	UNUSED_PARAMETER_REFACTOR(source);
	UNUSED_PARAMETER_REFACTOR(center);
	
	StoreObjectFind(client, STORE_CONVERSATIONS_GUID, &conversation_collection);
	
	// FIXME: which priv should we really be checking? either/or/both?
	ccode = StoreObjectCheckAuthorization(client, &conversation_collection, STORE_PRIV_LIST);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	ccode = StoreObjectCheckAuthorization(client, &conversation_collection, STORE_PRIV_READ);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	show_total = (displayTotal == 0)? FALSE : TRUE;
	
	return StoreObjectIterCollectionContents(client, &conversation_collection, start, 
		end, flagsmask, flags, props, propcount, NULL, query, show_total);
}

// [LOCKING] Copy(X to Y) => RoLock(X), RwLock(Y)
CCode 
StoreCommandCOPY(StoreClient *client, StoreObject *object, StoreObject *collection)
{
	CCode ccode;
	FILE *src = NULL;
	char srcpath[XPL_MAX_PATH + 1];
	char dstpath[XPL_MAX_PATH + 1];
	StoreObject newobject;

	LogicalLockType xLock = LLOCK_NONE, yLock = LLOCK_NONE;
	StoreObject *xObject = NULL, *yObject = NULL;
	
	CHECK_NOT_READONLY(client)
	
	// check the parameters are ok and the document exists   
	if (STORE_IS_FOLDER(object->type)) return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
	
	// take a RO lock on the source to ensure it doesn't change while we copy it
	if (! LogicalLockGain(client, object, LLOCK_READONLY, "StoreCommandCopy"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);

	/* xLock is set */
	xLock = LLOCK_READONLY;
	xObject = object;
	
	// check authorization on the parent collection
	ccode = StoreObjectCheckAuthorization(client, collection, STORE_PRIV_BIND | STORE_PRIV_READ);
	if (ccode) {
		ccode = ConnWriteStr(client->conn, MSG4240NOPERMISSION);
		goto finish;
	}
	
	// copy the files on disk
	FindPathToDocument(client, object->collection_guid, object->guid, srcpath, sizeof(srcpath));
	src = fopen(srcpath, "r");
	if (!src) {
		ccode = ConnWriteStr(client->conn, MSG4224CANTREADMBOX);
		goto finish;
	}
	fclose(src);
	
	MaildirTempDocument(client, collection->guid, dstpath, sizeof(dstpath));
	
	if (link(srcpath, dstpath) != 0) {
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	// TODO: how to detect MSG5220QUOTAERR ?
	
	// make a copy of the storeobject
	memcpy(&newobject, object, sizeof(StoreObject));
	newobject.guid = 0;
	newobject.filename[0] = '\0';
		
	if (StoreObjectCreate(client, &newobject)) {
		Log(LOG_ERROR, "COPY: Can't create new store object");
		ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
		goto finish;
	}
	
	StoreObjectCopyInfo(client, object, &newobject);
	
	// swap dest to src, and find new dest - move temp file into correct position
	srcpath[0] = '\0';
	strncpy(srcpath, dstpath, sizeof(srcpath));
	FindPathToDocument(client, collection->guid, newobject.guid, dstpath, sizeof(dstpath));
	if (link(srcpath, dstpath) != 0) {
		// StoreObjectCreate saved the new object; if we can't create
		// the data, we should remove that object
		StoreObjectRemove(client, &newobject);
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	unlink(srcpath);
	
	// update metadata, at this point we need our RW-lock  this is the y lock
	// FIXME:  We do store level locking so skip this....
	/*
	 if (! LogicalLockGain(client, collection, LLOCK_READWRITE, "StoreCommandCOPY")) {
		ccode = ConnWriteStr(client->conn, MSG4120BOXLOCKED);
		goto finish;
	}
	yLock = LLOCK_READWRITE;
	yObject = collection;
	*/

	newobject.collection_guid = collection->guid;
	newobject.time_created = newobject.time_modified = time(NULL);
	
	StoreObjectFixUpFilename(collection, &newobject);
	
	StoreObjectSave(client, &newobject);
	
	StoreObjectUpdateImapUID(client, &newobject);
	
	/* release the [xy]locks */
	//FIXME: Store level lock!!
	//LogicalLockRelease(client, yObject, yLock, "StoreCommandCOPY");
	LogicalLockRelease(client, xObject, xLock, "StoreCommandCOPY");
	xLock = yLock = LLOCK_NONE;
	
	++client->stats.insertions;
	StoreWatcherEvent(client, &newobject, STORE_WATCH_EVENT_NEW);
	
	ccode = ConnWriteF(client->conn, "1000 " GUID_FMT " %d\r\n",
		newobject.guid, newobject.version);

finish:
	if (xLock != LLOCK_NONE) {
		LogicalLockRelease(client, xObject, xLock, "StoreCommandCOPY");
	}
	if (yLock != LLOCK_NONE) {
		LogicalLockRelease(client, yObject, yLock, "StoreCommandCOPY");
	}
	return ccode;
}

// FIXME: doesn't detect all error conditions
// [LOCKING] Create(X) => RwLock(Parent(X))
CCode
StoreCommandCREATE(StoreClient *client, char *name, uint64_t guid)
{
	StoreObject object;
	StoreObject container;
	int ret;
	char container_path[MAX_FILE_NAME+1];
	char *container_final_sep = NULL;
	char *ptr;
	
	CHECK_NOT_READONLY(client)
	
	// look for existing name/guid first?
	if (name != NULL) {
		if (StoreObjectFindByFilename(client, name, &object) == 0) {
			return ConnWriteStr(client->conn, MSG4226MBOXEXISTS);
		}
	}
	if (guid != 0) {
		if (StoreObjectFind(client, guid, &object) == 0) {
			return ConnWriteStr(client->conn, MSG4226GUIDINUSE);
		}
	}
	
	// first, find the name of the containing collection from the
	// name given. This will end in a /, but we will want to remove
	// that final / later unless the path is literally '/', so we
	// save the pointer for later...
	strncpy(container_path, name, MAX_FILE_NAME);
	ptr = strrchr(container_path, '/');
	if (ptr == NULL) {
		return ConnWriteStr(client->conn, MSG3019BADCOLLNAME);
	} 
	container_final_sep = ptr++;
	*ptr = '\0';
	
	// Create the object first to allocate our wanted guid
	memset(&object, 0, sizeof(StoreObject));
	object.type = STORE_DOCTYPE_FOLDER;
	object.guid = guid;
	strncpy(object.filename, name, MAX_FILE_NAME);
	
	ret = StoreObjectCreate(client, &object);
	switch (ret) {
		case 0:
			// if it's successful, that's ok.
			break;
		case -1:
			return ConnWriteF(client->conn, "4226 Guid " GUID_FMT " already exists\r\n", 
				object.guid);
			break;
		default:
			Log(LOG_ERROR, "CREATE: Can't create new store object");
			return ConnWriteStr(client->conn, MSG5005DBLIBERR);
			break;
	}
	/* FIXME - detect following errors?
	return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
	*/
	// now try to find the parent collection, creating any collections
	// as needed.
	if (container_path[1] != '\0') {
		uint64_t parent_guid = 1;
		
		ptr = container_path + 1;
		while (*ptr != '\0') {
			if (*ptr == '/') {
				*ptr = '\0';
				if (StoreObjectFindByFilename(client, container_path, &container)) {
					// need to create this path
					memset(&container, 0, sizeof(StoreObject));
					container.type = STORE_DOCTYPE_FOLDER;
					container.collection_guid = parent_guid;
					strncpy(container.filename, container_path, MAX_FILE_NAME);
					ret = StoreObjectCreate(client, &container);
					if (ret != 0) {
						StoreObjectRemove(client, &container);
						Log(LOG_ERROR, "CREATE: unable to create parent collections");
						return ConnWriteStr(client->conn, MSG5005DBLIBERR);
					}
				} else {
					if (container.type != 4096) {
						// this isn't a collection - erk!
						return ConnWriteStr(client->conn, MSG3019BADCOLLNAME);
					}
				}
				// save the guid for later.
				parent_guid = container.guid;
				*ptr = '/';
			}
			ptr++;
		}
	}
	
	// remove any trailing / from the path unless it's literally /
	if (container_final_sep != container_path) {
		*container_final_sep = '\0';
	}
	// now find the containing collection
	if (StoreObjectFindByFilename(client, container_path, &container)) {
		// no such collection - but it should have been created. Must be DB error
		Log(LOG_ERROR, "CREATE: Unable to find parent collection for new object");
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	}
	
	// grab the lock on the container now we're modifying it directly
	if (! LogicalLockGain(client, &container, LLOCK_READWRITE, "StoreCommandCREATE")) {
		// FIXME: here and below, what if we created other collections?
		StoreObjectRemove(client, &object);
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	}
	
	// check we can save this new object and if not, error out
	object.collection_guid = container.guid;
	StoreObjectFixUpFilename(&container, &object);
	
	if (StoreObjectSave(client, &object)) {
		StoreObjectRemove(client, &object);
		LogicalLockRelease(client, &container, LLOCK_READWRITE, "StoreCommandCREATE");
		return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
	}
	
	// release any lock we hold
	LogicalLockRelease(client, &container, LLOCK_READWRITE, "StoreCommandCREATE");
	
	// announce creation on parent container
	++client->stats.insertions;
	StoreWatcherEvent(client, &container, STORE_WATCH_EVENT_NEW);
	
	return ConnWriteF(client->conn, "1000 " GUID_FMT " %d\r\n", 
		object.guid, object.version);
}


// [LOCKING] Delete(X) => RwLock(Parent(X))
CCode
StoreCommandDELETE(StoreClient *client, StoreObject *object)
{
	StoreObject collection;
	char path[XPL_MAX_PATH]; 
	int ccode;
	
	// FIXME: do we want some kind of delete-only mode too?
	CHECK_NOT_READONLY(client)
	
	if (STORE_IS_FOLDER(object->type))
		return ConnWriteStr(client->conn, MSG4231USEREMOVE);
	
	// find the containing collection
	ccode = StoreObjectFind(client, object->collection_guid, &collection);
	if (ccode != 0) {
		Log(LOG_ERROR, "DELETE: Unable to find parent for " GUID_FMT, object->guid);
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	}
	
	// check we have the required permissions.
	ccode = StoreObjectCheckAuthorization(client, &collection, STORE_PRIV_UNBIND);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	// grab a read/write lock on the containing collection
	if (! LogicalLockGain(client, &collection, LLOCK_READWRITE, "StoreCommandDELETE")) {
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	}
	
	switch(object->type) {
	case STORE_DOCTYPE_MAIL:
		// check to see if we can remove this document from a conversation
		if (StoreObjectUnlinkFromConversation(client, object)) {
			// log the error, but don't fail on it
			Log(LOG_ERROR, "DELETE: Unable to unlink conversations from " GUID_FMT, object->guid);
		}
		break;
	case STORE_DOCTYPE_CALENDAR:
		// do we want to remove its events also?
		break;
	default:
		break;
	}
	
	// Unlink other documents from this document
	StoreObjectUnlinkAll(client, object);
	
	// Remove the document from the store
	StoreObjectRemove(client, object);
	
	// Remove document from the search index
	// IndexRemoveDocument(index, guid);
	
	// Release our RW lock now - we can still fail, but that doesn't bring
	// back the store object we just nuked :)
	LogicalLockRelease(client, &collection, LLOCK_READWRITE, "StoreCommandDELETE");
	
	// Remove the file content from the filesystem
	FindPathToDocument(client, object->collection_guid, object->guid, path, sizeof(path));
	if (unlink(path) != 0) {
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	
	// update stats and fire any event
	++client->stats.deletions;
	StoreWatcherEvent(client, object, STORE_WATCH_EVENT_DELETED);
	
	ccode = ConnWriteStr(client->conn, MSG1000OK);
	
finish:
	
	return ccode;
}

CCode 
StoreCommandEVENTS(StoreClient *client, 
                   char *startUTC, char *endUTC, 
                   StoreObject *calendar, 
                   unsigned int mask,
                   char *uid,
                   const char *query,
                   int start, int end,
                   StorePropInfo *props, int propcount)
{
	// pretend we have no events for now.
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(startUTC);
	UNUSED_PARAMETER_REFACTOR(endUTC);
	UNUSED_PARAMETER_REFACTOR(calendar);
	UNUSED_PARAMETER_REFACTOR(mask);
	UNUSED_PARAMETER_REFACTOR(uid);
	UNUSED_PARAMETER_REFACTOR(query);
	UNUSED_PARAMETER_REFACTOR(start);
	UNUSED_PARAMETER_REFACTOR(end);
	UNUSED_PARAMETER_REFACTOR(props);
	UNUSED_PARAMETER_REFACTOR(propcount);
	
	return ConnWriteStr(client->conn, MSG1000OK);
/*
    CCode ccode = 0;

    // FIXME - need to handle STORE_PRIV_READ_BUSY (bug 174023) 

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
        
        // FIXME: This is a big allocation 
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
    */
}

// [LOCKING] Flag(X) => RwLock(X)
CCode
StoreCommandFLAG(StoreClient *client, StoreObject *object, uint32_t change, int mode)
{
	uint32_t old_flags;
	int ccode;
	
	CHECK_NOT_READONLY(client)
	
	if (mode == STORE_FLAG_SHOW) {
		ccode = StoreObjectCheckAuthorization(client, object, STORE_PRIV_READ_PROPS);
		if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
		
		return ConnWriteF(client->conn, "1000 %u %u\r\n", object->flags, object->flags);
	}
	
	// just handle ADD / REMOVE / REPLACE now
	old_flags = object->flags;
	if (mode == STORE_FLAG_ADD) 	object->flags |= change;
	if (mode == STORE_FLAG_REMOVE)	object->flags &= ~change;
	if (mode == STORE_FLAG_REPLACE)	object->flags = change;
	
	ccode = StoreObjectCheckAuthorization(client, object, STORE_PRIV_WRITE_PROPS);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	// FIXME: need to propogate changes on mail documents to conversations?
	// if so, this may also change our locking requirements since we'd also
	// need to lock the conversation.
	if (object->type == STORE_DOCTYPE_MAIL) {
		
	}
	
	// save the changes
	LogicalLockGain(client, object, LLOCK_READWRITE, "StoreCommandFLAG");
	if (StoreObjectSave(client, object) != 0) {
		LogicalLockRelease(client, object, LLOCK_READWRITE, "StoreCommandFLAG");
		Log(LOG_ERROR, "FLAG: Unable to save updated store object");
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	}
	LogicalLockRelease(client, object, LLOCK_READWRITE, "StoreCommandFLAG");
	
	++client->stats.updates;
	StoreWatcherEvent(client, object, STORE_WATCH_EVENT_FLAGS);

	return ConnWriteF(client->conn, "1000 %u %u\r\n", old_flags, object->flags);
}

// [LOCKING] Info(X) => RoLock(X)
CCode
StoreCommandINFO(StoreClient *client, StoreObject *object,
                 StorePropInfo *props, int propcount)
{
	CCode ret;
	if (! LogicalLockGain(client, object, LLOCK_READONLY, "StoreCommandINFO"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	
	ret = StoreObjectIterDocinfo(client, object, props, propcount, FALSE);
	
	LogicalLockRelease(client, object, LLOCK_READONLY, "StoreCommandINFO");
	return ret;
}


CCode
StoreCommandISEARCH(StoreClient *client, const char *query, int start, int end,
                    StorePropInfo *props, int propcount)
{
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(query);
	UNUSED_PARAMETER_REFACTOR(start);
	UNUSED_PARAMETER_REFACTOR(end);
	UNUSED_PARAMETER_REFACTOR(props);
	UNUSED_PARAMETER_REFACTOR(propcount);
	
	return ConnWriteStr(client->conn, MSG1000OK);
	/*
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
    */
}

// [LOCKING] List(X) => RoLock(X)
CCode
StoreCommandLIST(StoreClient *client, 
                 StoreObject *collection, 
                 int start, int end, 
                 uint32_t flagsmask, uint32_t flags,
                 StorePropInfo *props, int propcount,
                 const char *query)
{
	int ccode;
	
	// check we have permission to List this collection
	ccode = StoreObjectCheckAuthorization(client, collection, STORE_PRIV_LIST);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	// grab a read-only lock to ensure consistency
	if (! LogicalLockGain(client, collection, LLOCK_READONLY, "StoreCommandLIST")) 
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	
	ccode = StoreObjectIterCollectionContents(client, collection, start, 
		end, flagsmask, flags, props, propcount, NULL, query, FALSE);
	
	// release the lock
	LogicalLockRelease(client, collection, LLOCK_READONLY, "StoreCommandLIST");
	return ccode;
}

// [LOCKING] Link(X to Y) => RoLock(X), RwLock(Y)
CCode
StoreCommandLINK(StoreClient *client, StoreObject *document, StoreObject *related)
{
	CCode ret;
	LogicalLockType xLock = LLOCK_NONE, yLock = LLOCK_NONE;
	StoreObject *xObject = NULL, *yObject = NULL;

	CHECK_NOT_READONLY(client)
	
	// grab the pair of locks we need
	// FIXME: we need to check first this will not deadlock?
	if (! LogicalLockGain(client, related, LLOCK_READONLY, "StoreCommandLINK"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	xLock = LLOCK_READONLY;
	xObject = related;

	UNUSED_PARAMETER(yObject);
	/* FIXME: Store level locks!
	if (! LogicalLockGain(client, document, LLOCK_READWRITE, "StoreCommandLINK")) {
		LogicalLockRelease(client, xObject, xLock, "StoreCommandLINK");
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	}
	yLock = LLOCK_READWRITE;
	yObject = document;
	*/
	
	// link the documents together
	ret = StoreObjectLink(client, document, related);
	
	// release our locks
	// FIXME: store level locks
	LogicalLockRelease(client, xObject, xLock, "StoreCommandLINK");
	//LogicalLockRelease(client, yObject, yLock, "StoreCommandLINK");
	xLock = yLock = LLOCK_NONE;
	
	if (ret == 0) {
		return ConnWriteStr(client->conn, MSG1000OK);
	} else {
		Log(LOG_ERROR, "LINK: Unable to link " GUID_FMT " to " GUID_FMT,
			document->guid, related->guid);
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	}
}

// [LOCKING] Links(X) => RoLock(X)
CCode
StoreCommandLINKS(StoreClient *client, BOOL reverse, StoreObject *document)
{
	int ccode;
	
	// FIXME: does this auth check make sense?
	ccode = StoreObjectCheckAuthorization(client, document, STORE_PRIV_READ);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	if (! LogicalLockGain(client, document, LLOCK_READONLY, "StoreCommandLINKS"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	
	ccode = StoreObjectIterLinks(client, document, reverse);
	
	LogicalLockRelease(client, document, LLOCK_READONLY, "StoreCommandLINKS");
	
	return ccode;
}

// [LOCKING] Unlink(X to Y) => RwLink(Y)
// Don't think we need a lock on X, because the link is going away.
CCode
StoreCommandUNLINK(StoreClient *client, StoreObject *document, StoreObject *related)
{
	CCode ret;
	CHECK_NOT_READONLY(client)
	
	if (! LogicalLockGain(client, document, LLOCK_READWRITE, "StoreCommandUNLINK"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	
	ret = StoreObjectUnlink(client, document, related);
	
	LogicalLockRelease(client, document, LLOCK_READWRITE, "StoreCommandUNLINK");
	
	if (ret == 0) {
		return ConnWriteStr(client->conn, MSG1000OK);
	} else {
		Log(LOG_ERROR, "UNLINK: Unable to unlink " GUID_FMT " from " GUID_FMT,
			document->guid, related->guid);
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	}
}

CCode
StoreCommandMAILINGLISTS(StoreClient *client, char *source)
{
	// TODO
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(source);
	
	return ConnWriteStr(client->conn, MSG3000UNKNOWN);
}

CCode
StoreCommandMESSAGES(StoreClient *client, const char *source, const char *query, 
                     int start, int end, StoreObject *center, 
                     uint32_t flagsmask, uint32_t flags,
                     int displayTotal,
                     StoreHeaderInfo *headers, int headercount, 
                     StorePropInfo *props, int propcount)
{
	// FIXME: obsolete? This looks very similar to LIST; just added
	// source and query. Might as well make it one command...
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(source);
	UNUSED_PARAMETER_REFACTOR(query);
	UNUSED_PARAMETER_REFACTOR(start);
	UNUSED_PARAMETER_REFACTOR(end);
	UNUSED_PARAMETER_REFACTOR(center);
	UNUSED_PARAMETER_REFACTOR(flagsmask);
	UNUSED_PARAMETER_REFACTOR(flags);
	UNUSED_PARAMETER_REFACTOR(displayTotal);
	UNUSED_PARAMETER_REFACTOR(headers);
	UNUSED_PARAMETER_REFACTOR(headercount);
	UNUSED_PARAMETER_REFACTOR(props);
	UNUSED_PARAMETER_REFACTOR(propcount);
	
	return ConnWriteStr(client->conn, MSG3000UNKNOWN);
}

// [LOCKING] Mime(X) => RoLock(X)
CCode
StoreCommandMIME(StoreClient *client, StoreObject *document)
{
	int ccode = 0;
	MimeReport *report = NULL;
    
	ccode = StoreObjectCheckAuthorization(client, document, STORE_PRIV_READ);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	if (STORE_IS_FOLDER(document->type)) return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);

	if (! LogicalLockGain(client, document, LLOCK_READONLY, "StoreCommandMIME"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);

	// FIXME: need to re-do the MimeGetInfo() API for store objects
	switch (MimeGetInfo(client, document, &report)) {
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
	
	LogicalLockRelease(client, document, LLOCK_READONLY, "StoreCommandMIME");
	
	return ccode;
}

// FIXME: needs to move any store properties etc. related to this document
// [LOCKING] Move(X to Y) => RwLock(Parent(X)), RwLock(Y)
CCode 
StoreCommandMOVE(StoreClient *client, StoreObject *object, 
                 StoreObject *destination_collection, const char *newfilename)
{
	int ccode = 0;
	char src_path[XPL_MAX_PATH];
	char dst_path[XPL_MAX_PATH];
	StoreObject source_collection;
	StoreObject original;

	StoreObject *xObject = NULL, *yObject = NULL;
	LogicalLockType xLock = LLOCK_NONE, yLock = LLOCK_NONE;
	
	CHECK_NOT_READONLY(client)
	
	// ensure the request makes sense (eg., not moving a collection into a doc..)
	if (STORE_IS_FOLDER(object->type)) return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
	if (!STORE_IS_FOLDER(destination_collection->type)) 
		return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
	
	// find the parent collection of the document we want to move.
	ccode = StoreObjectFind(client, object->collection_guid, &source_collection);
	if (ccode != 0) return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	
	// check we have the rights to do this move
	ccode = StoreObjectCheckAuthorization(client, &source_collection, STORE_PRIV_UNBIND);
	if (ccode) {
		ccode = ConnWriteStr(client->conn, MSG4240NOPERMISSION);
		goto finish;
	}
	ccode = StoreObjectCheckAuthorization(client, destination_collection, STORE_PRIV_BIND);
	if (ccode) {
		ccode = ConnWriteStr(client->conn, MSG4240NOPERMISSION);
		goto finish;
	}
	
	// grab the locks that we need to do the move
	// FIXME: both RW locks, real chance of deadlock here.
	if (! LogicalLockGain(client, &source_collection, LLOCK_READWRITE, "StoreCommandMOVE")) {
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	}
	xObject = &source_collection;
	xLock = LLOCK_READWRITE;

	/* FIXME: store level locks!
	if (! LogicalLockGain(client, destination_collection, LLOCK_READWRITE, "StoreCommandMOVE")) {
		LogicalLockRelease(client, xObject, xLock, "StoreCommandMOVE");
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	}
	yObject = destination_collection;
	yLock = LLOCK_READWRITE;
	*/
	
	// copy the original object so we can fire events on it later
	memcpy(&original, object, sizeof(StoreObject));
	
	// move the file to a temporary location
	FindPathToDocument(client, object->collection_guid, object->guid, src_path, sizeof(src_path));  
	MaildirTempDocument(client, destination_collection->guid, dst_path, sizeof(dst_path)); 
	
	if (link(src_path, dst_path) != 0) {
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	
	if (unlink(src_path) != 0) {
		// FIXME: try to remove the new link we just created?
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	
	// now change the information on this object and fix up the filename
	object->collection_guid = destination_collection->guid;
	if (newfilename != NULL)
		strncpy(object->filename, newfilename, MAX_FILE_NAME);
	StoreObjectFixUpFilename(destination_collection, object);
	StoreObjectUpdateImapUID(client, object);
	
	if (STORE_DOCTYPE_MAIL == object->type) {
		// FIXME: do something with the conversations ??
	}
	
	// move the temp doc we created to the new location
	src_path[0] = '\0';
	strncpy(src_path, dst_path, sizeof(src_path));
	FindPathToDocument(client, object->collection_guid, object->guid, dst_path, sizeof(dst_path));
	
	if (link(src_path, dst_path) != 0) {
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	if (unlink(src_path) != 0) {
		// FIXME: try to remove the new link we just created?
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	
	// save our changes
	StoreObjectSave(client, object);
	
	// unlock the collections
	// FIXME: Store level locks
	LogicalLockRelease(client, xObject, xLock, "StoreCommandMOVE");
	//LogicalLockRelease(client, yObject, yLock, "StoreCommandMOVE");
	xLock = yLock = LLOCK_NONE;
	
	// fire off any events
	StoreWatcherEvent(client, &original, STORE_WATCH_EVENT_DELETED);
	StoreWatcherEvent(client, object, STORE_WATCH_EVENT_NEW);

	return ConnWriteStr(client->conn, MSG1000OK);
	
finish:
	// unlock the collections
	if (xLock != LLOCK_NONE) {
		LogicalLockRelease(client, xObject, xLock, "StoreCommandMOVE");
	}
	if (yLock != LLOCK_NONE) {
		LogicalLockRelease(client, yObject, yLock, "StoreCommandMOVE");
	}

	return ccode;
}

// [LOCKING] Propget(X) => RoLock(X)
CCode
StoreCommandPROPGET(StoreClient *client, 
                    StoreObject *object, 
                    StorePropInfo *props, int propcount)
{
	CCode ret;
	
	LogicalLockGain(client, object, LLOCK_READONLY, "StoreCommandPROPGET");
	
	if (propcount == 0) {
		ret = StoreObjectIterProperties(client, object);
	} else {
		// output requested props for each document
		ret = StoreObjectIterDocinfo(client, object, props, propcount, TRUE);
	}
	
	LogicalLockRelease(client, object, LLOCK_READONLY, "StoreCommandPROPGET");
	
	return ret;
}

// [LOCKING] Propset(X) => RwLock(X)
CCode
StoreCommandPROPSET(StoreClient *client, 
                    StoreObject *object,
                    StorePropInfo *prop,
                    int size)
{
	int ccode;
	
	CHECK_NOT_READONLY(client)
	
	UNUSED_PARAMETER_REFACTOR(size);
	
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
	
	ccode = StoreObjectCheckAuthorization(client, object, STORE_PRIV_WRITE_PROPS);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	if (prop->type == STORE_PROP_EVENT_ALARM)
		// FIXME: this did cache alarms in a separate DB.
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	if (prop->type == STORE_PROP_ACCESS_CONTROL) 
		// this is how we used to set ACLs, deprecated
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	
	if (! LogicalLockGain(client, object, LLOCK_READWRITE, "StoreCommandPROPSET"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	
	// this is a plain external property
	ccode = StoreObjectSetProperty(client, object, prop);
	
	LogicalLockRelease(client, object, LLOCK_READWRITE, "StoreCommandPROPSET");
	
	if (ccode < -1) {
		Log(LOG_ERROR, "PROPSET: Unable to set propert on " GUID_FMT, object->guid);
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	} else if (ccode == -1) {
		// failed, but already returned a notice to the client
		return -1;
	}
	
	++client->stats.updates;
	StoreWatcherEvent(client, object, STORE_WATCH_EVENT_MODIFIED);
	ccode = ConnWriteStr(client->conn, MSG1000OK);
	
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

// [LOCKING] Read(X) => RoLock(X)
CCode
StoreCommandREAD(StoreClient *client, StoreObject *object,
                 int64_t requestStart, uint64_t requestLength)
{
	int ccode = 0;
	
	// check that we're allowed to read
	ccode = StoreObjectCheckAuthorization(client, object, STORE_PRIV_READ);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	if (! LogicalLockGain(client, object, LLOCK_READONLY, "StoreCommandREAD"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	
	if (STORE_IS_FOLDER(object->type)) {
		// 2001 <guid> <version>
		ccode = ConnWriteF(client->conn, "2001 " GUID_FMT " %d\r\n", object->guid, object->version);
		ccode = ConnWriteStr(client->conn, MSG1000OK);
	} else if (STORE_IS_DBONLY(object->type)) {
		ccode = ConnWriteStr(client->conn, MSG3016DOCTYPEUNREADABLE);
	} else {
		ccode = ShowDocumentBody(client, object, requestStart, requestLength);
	}
	
	LogicalLockRelease(client, object, LLOCK_READONLY, "StoreCommandREAD");
	
	return ccode;
}


CCode 
StoreCommandREINDEX(StoreClient *client, StoreObject *document)
{
	// want to re-do this completely.
	CHECK_NOT_READONLY(client)
	
	UNUSED_PARAMETER_REFACTOR(document);
	
	return ConnWriteStr(client->conn, MSG5005DBLIBERR);
}

// FIXME: doesn't handle following errors
// MSG3019BADCOLLNAME
// MSG4240NOPERMISSION
// MSG4228CANTWRITEMBOX
// FIXME: doesn't create collections as needed to create destination tree
// [LOCKING] Rename(X to Y) => RwLock(Parent(X)) [assumes Parent(X) == Parent(Y)?]
// This locking ensures that Y doesn't suddenly pop into existance also.
CCode
StoreCommandRENAME(StoreClient *client, StoreObject *collection,
                   char *newfilename)
{
	int ccode = 0;
	StoreObject parent_collection;
	
	CHECK_NOT_READONLY(client)
	
	ccode = StoreObjectCheckAuthorization(client, collection, STORE_PRIV_UNBIND);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	ccode = StoreObjectFind(client, collection->collection_guid, &parent_collection);
	if (ccode != 0) return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	
	if (! LogicalLockGain(client, &parent_collection, LLOCK_READWRITE, "StoreCommandRENAME"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	
	if (StoreObjectRenameSubobjects(client, collection, newfilename))
		goto dberror;
	
	strncpy(collection->filename, newfilename, MAX_FILE_NAME);
	if (StoreObjectSave(client, collection)) 
		goto dberror;
	
	LogicalLockRelease(client, &parent_collection, LLOCK_READWRITE, "StoreCommandRENAME");
	
	// FIXME - need to fire an event for each collection renamed
	StoreWatcherEvent(client, collection, STORE_WATCH_EVENT_COLL_RENAMED);
	
	return ConnWriteStr(client->conn, MSG1000OK);

dberror:
	LogicalLockRelease(client, &parent_collection, LLOCK_READWRITE, "StoreCommandRENAME");
	return ConnWriteStr(client->conn, MSG5005DBLIBERR);
}

CCode
ReceiveToFile(StoreClient *client, const char *path, uint64_t *size)
{
	FILE *fh;
	CCode ccode;
	uint64_t receive_size;
	
	fh = fopen(path, "w");
	if (!fh) {
		if (ENOSPC == errno)
			return ConnWriteStr(client->conn, MSG5220QUOTAERR); // FIXME
		else
			return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
	}
	if (-1 == (ccode = ConnWrite(client->conn, "2002 Send document.\r\n", 21)) || 
	    -1 == (ccode = ConnFlush(client->conn))) {
		return ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
	}
	
	if (*size > 0) {
		ccode = ConnReadToFile(client->conn, fh, *size);
		receive_size = *size;
	} else {
		ccode = receive_size = ConnReadToFileUntilEOS(client->conn, fh);
	}
	if (-1 == ccode) {
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	*size = receive_size;
	
	ccode = fsync(fileno(fh));
	if (ccode) {
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		fclose(fh);
		fh = NULL;
		goto finish;
	}
	
	ccode = fclose(fh);
	fh = NULL;
	if (ccode) {
		ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
		goto finish;
	}
	
	return 0;
	
finish:
	return ccode;
}

// FIXME: rstart/rend range processing totally broken. 
// FIXME: range processing needs to unlink and copy-on-write
// FIXME: we should probably be removing some/all document properties?
// [LOCKING] Replace(X) => RwLock(X)
CCode
StoreCommandREPLACE(StoreClient *client, StoreObject *object, int size, uint64_t rstart, uint64_t rend, uint32_t version)
{
	CCode ccode;
	char path[XPL_MAX_PATH + 1];
	char tmppath[XPL_MAX_PATH + 1];
	uint64_t tmpsize;
	
	LogicalLockType xLock = LLOCK_NONE;
	StoreObject *xObject = NULL;

	CHECK_NOT_READONLY(client)

	if ((rend <= rstart) && ( rstart > 0)) {
		// given a range that makes no sense....!
		return ConnWriteStr(client->conn, MSG3021BADRANGESIZE);
	}
	
	// check object type and permissions
	if (STORE_IS_FOLDER(object->type) || STORE_IS_CONVERSATION(object->type)) {
		return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
	}
	
	ccode = StoreObjectCheckAuthorization(client, object, STORE_PRIV_WRITE_CONTENT);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	// write the waiting data to a temporary file
	MaildirTempDocument(client, object->collection_guid, tmppath, sizeof(tmppath));
	
	tmpsize = size;
	if (size < 0) tmpsize = 0;

	ccode = ReceiveToFile(client, tmppath, &tmpsize);
	if (ccode) goto finish;
	
	// update metadata
	if ((rend != 0) && (rend > object->size)) {
		// writing a range - new size must be maximum of old size and new end of range
		tmpsize = rend;
	}
	object->size = tmpsize;
	
	// we gain the lock quite late - we have the new file, but haven't updated
	// anything yet. Potential waste of effort.
	if (! LogicalLockGain(client, object, LLOCK_READWRITE, "StoreCommandREPLACE")) {
		unlink(tmppath);
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	}
	xLock = LLOCK_READWRITE;
	xObject = object;
	
	StoreObjectUpdateImapUID(client, object);
	StoreProcessDocument(client, object, tmppath);
	
	// update the document
	FindPathToDocument(client, object->collection_guid, object->guid, path, sizeof(path));
	if (rend != 0) {
		// we're writing a range, not replacing the whole file
		// TODO
	} else {
		if (rename(tmppath, path) != 0) {
			ccode = ConnWriteStr(client->conn, MSG4228CANTWRITEMBOX);
			goto finish;
		}
	}
	
	// update the version if required
	if ((version > 0) && (version != object->version)) {
		object->version = version;
	}
	// update other metadata
	StoreObjectUpdateModifiedTime(object);
	
	if (StoreObjectSave(client, object)) {
		ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
		goto finish;
	}
	
	LogicalLockRelease(client, xObject, xLock, "StoreCommandREPLACE");
	
	// now we're done, do watcher events
	++client->stats.updates;
	StoreWatcherEvent(client, object, STORE_WATCH_EVENT_MODIFIED);
	
	return ConnWriteF(client->conn, "1000 " GUID_FMT " %d\r\n", 
                       object->guid, object->version);

finish:
	if (xLock != LLOCK_NONE) {
		LogicalLockRelease(client, xObject, xLock, "StoreCommandREPLACE");
	}
	return ccode;
}

// we want to check through this store and fix up any bad data
// FIXME: there is a race condition in here. When we turn on read-only
// mode, we should wait for any other users to finish up. In reality,
// this is going to be exceptionally hard to trigger though.
// [LOCKING]  TODO!
CCode 
StoreCommandREPAIR(StoreClient *client)
{
	StoreObject object;
	int result;
	char reason[200];
	
	// put the store into read-only mode to prevent other people
	// fiddling with it!
	SetStoreReadonly(client, MSG4250READONLY);
	
	// we want to loop through each document in the store and make
	// sure that it's "sane"
	object.guid = 0;
	while ((result = StoreObjectFindNext(client, object.guid + 1, &object)) == 0) {
		int test = StoreObjectRepair(client, &object);
		
		switch (test) {
			case 1:
				// repaired
				sprintf(reason, "2001 %s\r\n", object.filename);
				break;
			case -1:
				// broken
				sprintf(reason, "2011 %s\r\n", object.filename);
				break;
			case -2:
				// removed
				sprintf(reason, "2012 %s\r\n", object.filename);
				break;
			default:
				break;
		}
		if (test != 0) {
			ConnWriteStr(client->conn, reason);
		}
	}
	
	// put it back in read-write mode
	UnsetStoreReadonly(client);
	
	if (result == -1) {
		// no such document - therefore, we went through all ok
		return ConnWriteStr(client->conn, MSG1000OK);
	} else {
		// result must be -2 or something - some error occured
		// sad; DB repair shouldn't return a db error... :D
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	}
}

// [LOCKING] Remove(X) => RwLock(Parent(X))
CCode
StoreCommandREMOVE(StoreClient *client, StoreObject *collection)
{
	CCode ccode;
	StoreObject parent_collection;
	
	// FIXME: do we want some kind of 'can delete' state?
	CHECK_NOT_READONLY(client)
	
	if (! STORE_IS_FOLDER(collection->type))
		return ConnWriteStr(client->conn, MSG4231USEPURGE);
	
	ccode = StoreObjectCheckAuthorization(client, collection, STORE_PRIV_UNBIND);
	if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
	
	ccode = StoreObjectFind(client, collection->collection_guid, &parent_collection);
	if (ccode != 0) return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	
	if (! LogicalLockGain(client, &parent_collection, LLOCK_READWRITE, "StoreCommandREMOVE"))
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	
	ccode = StoreObjectRemoveCollection(client, collection);
	
	LogicalLockRelease(client, &parent_collection, LLOCK_READWRITE, "StoreCommandREMOVE");
	
	if (ccode == 0) {
		// finished with success!
		return ConnWriteStr(client->conn, MSG1000OK);
	} else {
		// some error
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	}
}


/* N.B. This command does no locking.  It is for debugging purposes only. */

CCode 
StoreCommandRESET(StoreClient *client)
{
	CHECK_NOT_READONLY(client)
	
	DeleteStore(client);
	return ConnWriteStr(client->conn, MSG1000OK);
}

CCode
StoreCommandSTORES(StoreClient *client)
{
	char **userlist;
	int result;

	result = MsgAuthUserList(&userlist);
	if (result) {
		int i;
		
		for (i = 0; userlist[i] != 0; i++) {
			ConnWriteF(client->conn, "2001 %s\r\n", userlist[i]);
		}
		MsgAuthUserListFree(&userlist);
	}
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
		int ret = 0;
		ret = SelectStore(client, user);
		switch (ret) {
			case 0:
				return ConnWriteStr(client->conn, MSG1000OK);
				break;
			case -2:
				return ConnWriteStr(client->conn, MSG4225BADVERSION);
			case -1:
			default:
				return ConnWriteStr(client->conn, MSG4224BADSTORE);
				break;
		}
	}
	
	if (0 != MsgAuthFindUser(user)) {
		Log(LOG_INFO, "Couldn't find user object for %s\r\n", user);
		/* the previous nmap returned some sort of user locked message? */
		return ConnWriteStr(client->conn, MSG4100STORENOTFOUND);
	}
	
	if (SelectStore(client, user)) {
		return ConnWriteStr(client->conn, MSG4224BADSTORE);
	} else {
		return ConnWriteStr(client->conn, MSG1000OK);
	}
}

CCode
StoreCommandTIMEOUT(StoreClient *client, int lockTimeoutMs)
{
	// should be obsolete really
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(lockTimeoutMs);
	return ConnWriteStr(client->conn, MSG3000UNKNOWN);
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

// Set the watched flags for the specified collection.  If we were watching 
// something else before, stop watching it now.

CCode 
StoreCommandWATCH(StoreClient *client, StoreObject *collection, int flags)
{
	CCode ccode;
	
	// stop watching whatever we were watching before
	if (client->watch.collection.guid > 0) {
		if (StoreWatcherRemove(client, &(client->watch.collection)))
			return ConnWriteStr(client->conn, MSG5004INTERNALERR);
		
		memset(&(client->watch.collection), 0, sizeof(StoreObject));
		client->watch.flags = 0;
	}
	
	// watch the new collection
	if (flags) {
		flags |= STORE_WATCH_EVENT_COLL_REMOVED;
		flags |= STORE_WATCH_EVENT_COLL_RENAMED;
		
		ccode = StoreObjectCheckAuthorization(client, collection, STORE_PRIV_READ);
		if (ccode) return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
		
		if (StoreWatcherAdd(client, collection))
			return ConnWriteStr(client->conn, MSG5004INTERNALERR);
		
		memcpy(&(client->watch.collection), collection, sizeof(StoreObject));
		client->watch.flags = flags;
	}
	return ConnWriteStr(client->conn, MSG1000OK);
}

// FIXME: do something with the link document
// [LOCKING] Write(X) => RwLock(X)
// X above is the containing collection the new document will go into
CCode
StoreCommandWRITE(StoreClient *client, 
				  StoreObject *collection, 
				  int doctype, 
				  uint64_t size,
				  uint32_t addflags, 
				  uint64_t guid, 
				  const char *filename, 
				  uint64_t timestamp, 
				  uint64_t guid_link,
				  int no_process)
{
	CCode ccode;
	StoreObject newdocument;
	uint64_t tmpsize;
	char path[XPL_MAX_PATH+1];
	char tmppath[XPL_MAX_PATH+1];
	
	UNUSED_PARAMETER_REFACTOR(guid_link);

	CHECK_NOT_READONLY(client)
	
	if (STORE_IS_FOLDER(doctype) || STORE_IS_CONVERSATION(doctype)) {
		return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
	}
	
	memset(&newdocument, 0, sizeof(StoreObject));
	
	// try to create new object
	newdocument.guid = guid;
	newdocument.type = doctype;
	
	if (StoreObjectCreate(client, &newdocument))
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	
	// read content to temporary file
	MaildirTempDocument(client, collection->guid, tmppath, sizeof(tmppath));
	
	tmpsize = size;
	ccode = ReceiveToFile(client, tmppath, &tmpsize);
	if (ccode) goto finish;
	
	// update new object metadata
	if (timestamp == 0) timestamp = time(NULL);
	
	newdocument.collection_guid = 0;
	newdocument.size = tmpsize;
	newdocument.time_created = timestamp;
	newdocument.time_modified = timestamp;
	newdocument.flags = addflags;
	if (filename) strncpy(newdocument.filename, filename, MAX_FILE_NAME);
	
	if (no_process == 0) {
		// update metadata if we haven't been asked not to
		StoreProcessDocument(client, &newdocument, tmppath);
	}
	
	// put content in place
	FindPathToDocument(client, collection->guid, newdocument.guid, path, sizeof(path));
	if (link(tmppath, path) != 0) {
		unlink(tmppath);
		return ConnWriteStr(client->conn, MSG4224CANTWRITE);
	}
	unlink(tmppath);
	
	// save new object
	if (! LogicalLockGain(client, collection, LLOCK_READWRITE, "StoreCommandWRITE")) {
		unlink(path);
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	}
	
	// place the new document in the right collection
	// we take the lock on the collection this late, 
	newdocument.collection_guid = collection->guid;
	StoreObjectFixUpFilename(collection, &newdocument);
	StoreObjectUpdateImapUID(client, &newdocument);
	
	if (StoreObjectSave(client, &newdocument)) {
		StoreObjectRemove(client, &newdocument);
		LogicalLockRelease(client, collection, LLOCK_READWRITE, "StoreCommandWRITE");
		return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	}
	LogicalLockRelease(client, collection, LLOCK_READWRITE, "StoreCommandWRITE");
	
	// announce its creation
	++client->stats.insertions;
	StoreWatcherEvent(client, &newdocument, STORE_WATCH_EVENT_NEW);
	
	return ConnWriteF(client->conn, "1000 " GUID_FMT " %d\r\n", newdocument.guid, newdocument.version);

finish:
	return ccode;
}
