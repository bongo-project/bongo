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
#include <msgapi.h>
#include <nmlib.h>

#include "addressbook.h"

#include "../store/messages.h"

#define MAX_PROPS 25

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
AddressbookSetupCommands(void)
{
    CommandTable = BongoHashtableCreate(64, CommandHash, CommandCmp);

    if (!CommandTable ||

        /* session commands */
        BongoHashtablePutNoReplace(CommandTable, "AUTH", (void *) ADDRESSBOOK_COMMAND_AUTH) ||
        BongoHashtablePutNoReplace(CommandTable, "QUIT", (void *) ADDRESSBOOK_COMMAND_QUIT) ||
        BongoHashtablePutNoReplace(CommandTable, "SEARCH", (void *) ADDRESSBOOK_COMMAND_SEARCH) ||
        0)
    {
        XplConsolePrintf(AGENT_NAME ": Couldn't setup command table.\r\n");
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
CheckTokC(AddressbookClient *client, int n, int min, int max)
{
    if (n < min || n > max) {
        return ConnWriteStr(client->conn, MSG3010BADARGC);
    }

    return TOKEN_OK;
}

static CCode
ParseInt(AddressbookClient *client, char *token, int *out)
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

static int 
ParseRange(AddressbookClient *client, char *token, int *startOut, int *endOut) 
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
RequireUser(AddressbookClient *client)
{
    return (IS_MANAGER(client) || client->user[0])
        ? TOKEN_OK
        : ConnWriteStr(client->conn, MSG3241NOUSER);

    return TOKEN_OK;
}

#define TOK_ARR_SZ 10
#define PROP_ARR_SZ 10
#define HDR_ARR_SZ 10

static BOOL
ReadParentObject(const char *dn, char *parentDn, MDBValueStruct *vs)
{
    char configDn[MDB_MAX_OBJECT_CHARS + 1];
    
    if (MsgGetUserSettingsDN(dn, configDn, vs, FALSE)) {
        if (MDBRead(configDn, MSGSRV_A_PARENT_OBJECT, vs)) {
            BongoStrNCpy(parentDn, vs->Value[vs->Used - 1], MDB_MAX_OBJECT_CHARS + 1);
            MDBFreeValues(vs);
            return TRUE;
        } 
    }
    parentDn[0] = '\0';
    return FALSE;
}

static void
UnselectUser(AddressbookClient *client)
{
    client->user[0] = '\0';
    client->dn[0] = '\0';
}

static CCode
SelectUser(AddressbookClient *client, char *user, char *password)
{
    MDBValueStruct *vs;
    int ret = -1;
    
    vs = MDBCreateValueStruct(AddressbookAgent.handle.directory, NULL);
    if (!vs) {
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }
    
    if (!MsgFindObject(user, client->dn, NULL, NULL, vs)) {
        XplConsolePrintf("Couldn't find user object for %s\r\n", user);
        if (IS_MANAGER(client)) {
            ret = ConnWriteStr(client->conn, MSG4224NOUSER);
        } else {
            ret = ConnWriteStr(client->conn, MSG3242BADAUTH);
            XplDelay(2000);
        }
        goto finish;
    }
    
    ReadParentObject(client->dn, client->parentObject, vs);
    
    if (password && !MDBVerifyPassword(client->dn, password, vs)) {
        ret = ConnWriteStr(client->conn, MSG3242BADAUTH);
        XplDelay(2000);
        goto finish;
    }
    
    UnselectUser(client);
    
    BongoStrNCpy(client->user, user, sizeof(client->user));
    
    ret = ConnWriteStr(client->conn, MSG1000OK);
    
finish:
    MDBDestroyValueStruct(vs);
    return ret;
}

static CCode
AddressbookCommandAUTHCOOKIE(AddressbookClient *client, char *user, char *token) 
{
    Connection *conn;
    CCode ccode;
    char dn[MDB_MAX_OBJECT_CHARS + 1];
    char buffer[CONN_BUFSIZE];
    struct sockaddr_in serv;
        
    if (!MsgFindObject(user, dn, NULL, &serv, NULL)) {
        ccode = ConnWriteStr(client->conn, MSG3242BADAUTH);
        return -1;
    }
    
    conn = NMAPConnect(NULL, &serv);
    if (!conn) {
        return ConnWriteStr(client->conn, MSG3242BADAUTH);
    }

    ccode = NMAPAuthenticateWithCookie(conn, user, token, buffer, sizeof(buffer));
    switch (ccode) {
    case 1000:
        ccode = SelectUser(client, user, NULL);
        break;
    case -1:
        break;
    default:
        if (ccode >= 1000 && ccode < 6000) {
            ccode = ConnWriteF(client->conn, "%d %s\r\n", ccode, buffer);
        } else {
            XplConsolePrintf("Unexpected response from remote store: %s\r\n",
                             buffer);
            ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
        }
        break;
    }

    NMAPQuit(conn);
    ConnFree(conn);

    return ConnWriteStr(client->conn, "5000 Not Implemented");
}

static CCode
AddressbookCommandUSER(AddressbookClient *client, char *user, char *password)
{
    if (user) {
        return SelectUser(client, user, password);
    } else {
        UnselectUser(client);
        return ConnWriteStr(client->conn, MSG1000OK);
    }
}

static MDBValueStruct *
GetUserTypes(AddressbookClient *client)
{
    MDBValueStruct *types = MDBCreateValueStruct(AddressbookAgent.handle.directory, NULL);

    MDBAddValue(C_USER, types);
    MDBAddValue(C_ORGANIZATIONAL_UNIT, types);
    
    return types;
}

static MDBValueStruct *
GetSearchAttributes(AddressbookClient *client)
{
    MDBValueStruct *attributes = MDBCreateValueStruct(AddressbookAgent.handle.directory, NULL);
    
    MDBAddValue("cn", attributes);
    MDBAddValue("ou", attributes);
    MDBAddValue(A_FULL_NAME, attributes);
    MDBAddValue(A_GIVEN_NAME, attributes);
    MDBAddValue(A_SURNAME, attributes);
    MDBAddValue(A_INTERNET_EMAIL_ADDRESS, attributes);
    
    return attributes;
}

static CCode
WriteContactProperty(AddressbookClient *client, 
                     const char *dn, 
                     const char *prop,
                     MDBValueStruct *vs) 
{
    CCode ccode = 1;
    if (MDBRead(dn, prop, vs)) {
        if (vs->Used > 0) {
            char *value = vs->Value[vs->Used - 1];
            ccode = ConnWriteF(client->conn, "2001 \"%s\" %d\r\n%s\r\n",
                               prop, (int)strlen(value), value);
            MDBFreeValues(vs);
            if (ccode != -1) {
                ccode = 0;
            }
        }
    }

    return ccode;
}

static CCode
WriteContact(AddressbookClient *client, const char *dn, char **props, int numProps, MDBValueStruct *vs)
{
    char buf[MDB_MAX_OBJECT_CHARS + 1];
    char *email;
    int i;
    
    if (ConnWriteF(client->conn, "2000 %s\r\n", dn) == -1) {
        return -1;
    }
    
    for (i = 0; i < numProps; i++) {
        char *prop = props[i];
        
        if (!strcmp(prop, A_EMAIL_ADDRESS)) {
            email = (char*)MsgGetUserEmailAddress(dn, vs, (unsigned char*)buf, sizeof(buf));
            if (email) {
                if (ConnWriteF(client->conn, "2001 \"" A_EMAIL_ADDRESS "\"  %d\r\n%s\r\n", (int)strlen(email), email) == -1) {                  
                    
                    return -1;
                }
            }
        } else {
            if (!WriteContactProperty(client, dn, prop, vs)) {
                return -1;
            }
        }
    }
    
    return 0;
}

static CCode
AddressbookCommandSEARCH(AddressbookClient *client, const char *pattern, char **props, int numProps, int start, int end)
{
    CCode ccode;
    MDBValueStruct *types;
    MDBValueStruct *attributes;
    MDBValueStruct *users;
    MDBValueStruct *vs;

    types = GetUserTypes(client);
    attributes = GetSearchAttributes(client);
    users = MDBCreateValueStruct(AddressbookAgent.handle.directory, NULL);
    
    vs = MDBCreateValueStruct(AddressbookAgent.handle.directory, NULL);
    
    MDBFreeValues(vs);
    
    /* FIXME: is this the right context to search? */
    if (MDBFindObjects(AddressbookAgent.defaultContext,
                       types,
                       pattern,
                       attributes,
                       MDB_SCOPE_INFINITE,
                       500,
                       users)) {
        unsigned int i;
        int num = 0;
        for (i = 0; i < users->Used; i++) {
            char *dn;
            char theirParentObject[MDB_MAX_OBJECT_CHARS];
            
            dn = users->Value[i];
            ReadParentObject(dn, theirParentObject, vs);
            if (!strcmp(client->parentObject, theirParentObject)) {
                if (num >= start) {
                    WriteContact(client, dn, props, numProps, vs);
                }
                num++;
                if (end != -1 && num >= end) {
                    break;
                } 
            }
        }
        ccode = ConnWriteStr(client->conn, MSG1000OK);
    } else {
        ccode = ConnWriteStr(client->conn, MSG1000OK);
    }
    
    MDBDestroyValueStruct(vs);
    MDBDestroyValueStruct(types);
    MDBDestroyValueStruct(attributes);
    MDBDestroyValueStruct(users);
    
    return ccode;
}

static CCode 
AddressbookCommandQUIT(AddressbookClient *client)
{
    if (-1 != ConnWriteStr(client->conn, MSG1000BYE)) {
        ConnFlush(client->conn);
    }
    
    return -1;
}

CCode
AddressbookCommandLoop(AddressbookClient *client)
{
    int ccode = 0;
    AddressbookCommand command;
    
    while (-1 != ccode && BONGO_AGENT_STATE_RUNNING == AddressbookAgent.agent.state) {
        char *tokens[TOK_ARR_SZ];
        int n;
        int numProps = 0;
        char *props[MAX_PROPS];
        int start, end;
        int i;

        start = end = -1;
        
        ccode = ConnReadAnswer(client->conn, client->buffer, CONN_BUFSIZE);
        if (-1 == ccode || ccode >= CONN_BUFSIZE) {
            break;
        }
        
        memset(tokens, 0, sizeof(tokens));
        n = CommandSplit(client->buffer, tokens, TOK_ARR_SZ);
        if (0 == n) {
            command = ADDRESSBOOK_COMMAND_NULL;
        } else {
            command = (AddressbookCommand) BongoHashtableGet(CommandTable, tokens[0]);
        }
        
        switch (command) {
            case ADDRESSBOOK_COMMAND_NULL:
                ccode = ConnWriteStr(client->conn, MSG3000UNKNOWN);
                break;
                
            case ADDRESSBOOK_COMMAND_AUTH:
                /* AUTH COOKIE <username> <token> */
                /* AUTH SYSTEM <md5 hash> */
                /* AUTH USER <username> <password> */
                
                if (TOKEN_OK != (ccode = CheckTokC(client, n, 3, 4))) {
                    break;
                }
                if (!XplStrCaseCmp(tokens[1], "COOKIE")) {
                    if (TOKEN_OK == (ccode = CheckTokC(client, n, 4, 4))) {
                        ccode = AddressbookCommandAUTHCOOKIE(client, tokens[2], tokens[3]);
                    }
                } else if (0 == XplStrCaseCmp(tokens[1], "USER")) {
                    if (TOKEN_OK == (ccode = CheckTokC(client, n, 4, 4))) {
                        ccode = AddressbookCommandUSER(client, tokens[2], tokens[3]);
                    }
                } else {
                    ccode = ConnWriteStr(client->conn, MSG3000UNKNOWN);
                }
                break;
            case ADDRESSBOOK_COMMAND_SEARCH:
                /* SEARCH pattern */
                if (TOKEN_OK != (ccode = RequireUser(client)) || 
                    TOKEN_OK != (ccode = CheckTokC(client, n, 2, 3))) {
                    break;
                }
                
                for (i = 2; i < n; i++) {
                    if ('R' == *tokens[i] && start == -1) {
                        ccode = ParseRange(client, tokens[i] + 1, &start, &end);
                    } else if ('P' == *tokens[i] && numProps == 0) {
                        numProps = BongoStringSplit(tokens[i] + 1, ',', props, MAX_PROPS); 
                    } else {
                        ccode = ConnWriteStr(client->conn, MSG3022BADSYNTAX);
                    }
                }
                
                ccode = AddressbookCommandSEARCH(client, tokens[1], props, numProps, start, end);
                
                break;
            case ADDRESSBOOK_COMMAND_QUIT:
                /* QUIT */
                
                ccode = AddressbookCommandQUIT(client);
                break;
                
            default:
                break;
        }
        
        if (ccode >= 0) {
            ccode = ConnFlush(client->conn);
        }
    }
    
    return ccode;
}

