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
#include <logger.h>
#include <mdb.h>
#include <nmap.h>

#include "stored.h"
#include "messages.h"

/*
** auth.c
**
** Contains code to evaluate authorization to access
** store documents
*/


typedef struct {
    StorePrincipalType type;
    union {
        char *user;
        char *token;
    };
} StorePrincipal;


/* Access Control Entry */
typedef struct {
    StorePrincipal principal;
    unsigned int privileges;   /* granted privileges */
} StoreACE;



/* 
   <user-principal>         ::= 'user:' <username>
   <token-principal>        ::= 'token:' <token>
   <principal>              ::= <user-principal> | <token-principal> | 'all' | 'auth'
   <operation>              ::= 'grant' | 'deny'
   <access-control-element> ::= <operation> ' ' <principal> ' ' <privilege> ';'    
   <access-control-list>    ::= <access-control-element> [<access-control-list>]
*/

/* modifies: ace's members will be initialized.  **ptr will be disrupted.
             *ptr itself will be advanced to the the next character following the ACE.
   returns: 0 on success, -1 on error

   FIXME: we should store ACLs in the db in a pre-parsed form for speed. (bug 178217)
*/

static int
parseACE (char **ptr, StoreACE *ace)
{
    char *p = *ptr;
    char *q;

#define GetNextToken(end,str,delim)          \
        if (!(end = strchr(str, delim))) {   \
            return -1;                       \
        } 

    /* operation */
    GetNextToken(q, p, ' ');
    if (!strncmp("grant", p, 5)) {
        
    } else if (!strncmp("deny", p, 4)) {
        return -1; /* unsupported */
    } else {
        return -1;
    }
    p = ++q;
    
    /* principal */
    GetNextToken(q, p, ' ');
    *q = 0;

    if (!strcmp(p, "all")) {
        ace->principal.type = STORE_PRINCIPAL_ALL;
    } else if (!strcmp(p, "auth")) {
        ace->principal.type = STORE_PRINCIPAL_AUTHENTICATED;
    } else if (!strncmp(p, "user:", 5)) {
        ace->principal.type = STORE_PRINCIPAL_USER;
        ace->principal.user = p + 5;
    } else if (!strncmp(p, "token:", 6)) {
        ace->principal.type = STORE_PRINCIPAL_TOKEN;
        ace->principal.token =  p + 6;
    } else {
        return -1;
    }
    p = ++q;
                
    /* privilege */
    GetNextToken(q, p, ';');
    *q = 0;
    if (!strcmp(p, "all")) {
        ace->privileges = STORE_PRIV_ALL;
    } else if (!strcmp(p, "read")) {
        ace->privileges = STORE_PRIV_READ;
    } else if (!strcmp(p, "write")) { /* write -> write-content */
        ace->privileges = STORE_PRIV_WRITE_CONTENT;
    } else if (!strcmp(p, "write-props")) {
        ace->privileges = STORE_PRIV_WRITE_PROPS;
    } else if (!strcmp(p, "write-content")) {
        ace->privileges = STORE_PRIV_WRITE_CONTENT;
    } else if (!strcmp(p, "unlock")) {
        ace->privileges = STORE_PRIV_UNLOCK;
    } else if (!strcmp(p, "read-busy")) {
        ace->privileges = STORE_PRIV_READ_BUSY;
    } else if (!strcmp(p, "read-acl")) {
        ace->privileges = STORE_PRIV_READ_ACL;
    } else if (!strcmp(p, "read-own-acl")) {
        ace->privileges = STORE_PRIV_READ_OWN_ACL;
    } else if (!strcmp(p, "write-acl")) {
        ace->privileges = STORE_PRIV_WRITE_ACL;
    } else if (!strcmp(p, "bind")) {
        ace->privileges = STORE_PRIV_BIND;
    } else if (!strcmp(p, "unbind")) {
        ace->privileges = STORE_PRIV_UNBIND;
    } else if (!strcmp(p, "read-props")) {
        ace->privileges = STORE_PRIV_READ_PROPS;
    } else if (!strcmp(p, "list")) {
        ace->privileges = STORE_PRIV_LIST;
    } else {
        return -1;
    }

    *ptr = ++q;
    return 0;
}


/* returns: -1 if acl is invalid, 0 o/w
*/

int
StoreParseAccessControlList(StoreClient *client, const char *acl)
{
    char buf[CONN_BUFSIZE + 1];
    char *ptr = buf;
    StoreACE ace;

    if (strlen(acl) > (sizeof(buf) - 1)) {
        return -1;
    }    
    strncpy (buf, acl, sizeof(buf));
    buf[sizeof(buf)-1] = 0;

    while (*ptr) {
        if (parseACE(&ptr, &ace)) {
            return -1;
        }
    }
    return 0;
}


/* 
   modifies: privileges are ORed with any privileges found by this function.

   returns: 0 on success
            -1 db lib err
            -2 corrupt acl

*/

static int
computePrivileges (StoreClient *client, uint64_t guid,
                   unsigned int *privileges)
{
    char buf[CONN_BUFSIZE + 1];
    char *ptr;
    StoreACE ace;
    StoreToken *token;

    switch (DStoreGetProperty(client->handle, guid, "nmap.access-control", 
                              buf, sizeof(buf))) 
    {
    case -1:
        return -1;
    case 0:
        return 0;
    }

    for (ptr = buf; *ptr; ) {
        if (parseACE(&ptr, &ace)) {
            return -2;
        }
        
#if 0
        printf("ACE: grant %s %s %x\r\n", 
               STORE_PRINCIPAL_NONE == ace.principal.type ? "NONE" :
               STORE_PRINCIPAL_ALL == ace.principal.type ? "ALL" :
               STORE_PRINCIPAL_USER == ace.principal.type ? "user" :
               STORE_PRINCIPAL_TOKEN == ace.principal.type ? "token" :
               STORE_PRINCIPAL_AUTHENTICATED == ace.principal.type ? "authenticated" :
               "????",
               ace.principal.user, ace.privileges);
#endif
            
        switch (ace.principal.type) {
        case STORE_PRINCIPAL_NONE:
        case STORE_PRINCIPAL_ALL:
            goto accept;
        case STORE_PRINCIPAL_AUTHENTICATED:
            if (!HAS_IDENTITY(client) && !IS_MANAGER(client)) {
                continue;
            }
            goto accept;
        case STORE_PRINCIPAL_USER:
            if (STORE_PRINCIPAL_USER != client->principal.type || 
                strcmp(ace.principal.user, client->principal.name)) 
            {
                continue;
            }
            goto accept;
        case STORE_PRINCIPAL_TOKEN:
            for (token = client->principal.tokens;
                 token;
                 token = token->next)
            {
                if (!strcmp(token->data, ace.principal.token)) {
                    goto accept;
                }
            }
            continue;
        }
    accept:
        *privileges |= ace.privileges;
    } while (*ptr);

    return 0;
}


/** Checks that the client is authorized to perform the requested 
    operation on the given document.

    returns: 0 if authorized
             1 if unauthorized
             -1 db lib error
             -2 corrupt acl
             -3 other error
*/

int
StoreCheckAuthorizationQuiet(StoreClient *client, DStoreDocInfo *info, 
                             unsigned int neededPrivileges)
{
    unsigned int privileges = 0;
    int pcode;
    DStoreDocInfo coll;
    DStoreDocInfo *infoptr;

    if (STORE_PRINCIPAL_USER == client->principal.type && 
        (!strcmp(client->principal.name, client->storeName)))
    {
        /* user always has full permissions to her own store. */
        return 0;
    }

    infoptr = info;

    while (1) {
        pcode = computePrivileges (client, infoptr->guid, &privileges);
        if (pcode) {
            return pcode;
        } 
        
        if (neededPrivileges == (privileges & neededPrivileges)) {
            return 0;
        } 

        XplConsolePrintf("have privs: %x need: %x; checking parent (read is %x)\r\n", 
                         privileges, neededPrivileges, STORE_PRIV_READ);
        
        /* don't have privs, check the parent collection */
        switch (DStoreGetDocInfoGuid(client->handle, infoptr->collection, &coll)) {
        case 1:
            infoptr = &coll;
            break;

        case 0:
            if (STORE_DOCTYPE_EVENT == info->type) {
                /* didn't find privs for event, but maybe we can access a calendar */
                BongoArray cals;
                unsigned int i;
                int result = 1;

                if (BongoArrayInit(&cals, sizeof(uint64_t), 10)) {
                    return -3;
                }
                if (DStoreFindCalendars(client->handle, info->guid, &cals)) {
                    result = -1;
                }
                for (i = 0; i < cals.len && 1 == result; i++) {
                    result = StoreCheckAuthorizationGuidQuiet(
                        client, 
                        BongoArrayIndex(&cals, uint64_t, i), 
                        neededPrivileges
                    );
                }
                BongoArrayDestroy(&cals, 0);

                return result;
            }

            return 1;

        case -1:
        default:
            return -1;
        }
    }
}


CCode
StoreCheckAuthorization(StoreClient *client, DStoreDocInfo *info,
                        unsigned int neededPrivileges)
{
    switch (StoreCheckAuthorizationQuiet(client, info, neededPrivileges)) {
    case 0:
        return 0;
    case 1:
        return ConnWriteStr(client->conn, MSG4240NOPERMISSION);
    case -1:
        return ConnWriteStr(client->conn, MSG5005DBLIBERR);
    case -2:
        return ConnWriteStr(client->conn, MSG5006CORRUPTACL);
    case -3:
    default:
        return ConnWriteStr(client->conn, MSG5004INTERNALERR);
    }

}


int
StoreCheckAuthorizationGuidQuiet(StoreClient *client, uint64_t guid,
                                 unsigned int neededPrivileges)
{
    DStoreDocInfo info;

    if (1 != DStoreGetDocInfoGuid(client->handle, guid, &info)) {
        return -1;
    }

    return StoreCheckAuthorizationQuiet(client, &info, neededPrivileges);
}


CCode StoreCheckAuthorizationGuid(StoreClient *client, uint64_t guid, 
                                  unsigned int neededPrivileges)
{
    DStoreDocInfo info;

    if (1 != DStoreGetDocInfoGuid(client->handle, guid, &info)) {
        return -1;
    }

    return StoreCheckAuthorization(client, &info, neededPrivileges);
}


CCode
StoreCheckAuthorizationPath(StoreClient *client, const char *_path, 
                            unsigned int neededPrivileges)
{
    DStoreDocInfo info;
    char *p;
    char *path = BongoMemStackPushStr(&client->memstack, _path);

    p = path + strlen(path);

    do {
        switch (DStoreGetDocInfoFilename(client->handle, path, &info)) {
        case 1:
            return StoreCheckAuthorization(client, &info, neededPrivileges);
        case 0:
            break;
        case -1:
        default:
            return ConnWriteStr(client->conn, MSG5005DBLIBERR);
        }

        --p;
        p = strrchr(path, '/');
        if (!p) {
            /* the document or collection doesn't exist */
            return StoreCheckAuthorizationGuid(client, STORE_ROOT_GUID, 
                                               neededPrivileges);
        }
        *p = 0;

    } while (1);
}


CCode 
StoreCommandAUTHSYSTEM(StoreClient *client, char *md5hash)
{
	unsigned char credential[XPLHASH_MD5_LENGTH];
	xpl_hash_context hash;

	XplHashNew(&hash, XPLHASH_MD5);
	XplHashWrite(&hash, client->authToken, strlen(client->authToken));
	XplHashWrite(&hash, StoreAgent.server.hash, NMAP_HASH_SIZE);
	XplHashFinal(&hash, XPLHASH_LOWERCASE, credential, XPLHASH_MD5_LENGTH);

	if (strcmp(credential, md5hash)) {
		XplDelay(2000);
		ConnWriteStr(client->conn, MSG3242BADAUTH);
		ConnFlush(client->conn);
		return -1;
	}

	client->flags |= STORE_CLIENT_FLAG_MANAGER;
	return ConnWriteStr(client->conn, MSG1000OK);
}


