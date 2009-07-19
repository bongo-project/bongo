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
    
    // this union _must_ be of all the same type, else assumptions elsewhere
    // will fail..
    __extension__ union {
    	char *hack;
        char *user;
        char *group;
        char *token;
    };
} StorePrincipal;


/* Access Control Entry */
typedef struct {
	BOOL deny;
    StorePrincipal principal;
    unsigned int privileges;   /* granted privileges */
} StoreACE;

/* 
   <user-principal>         ::= 'user:' <username>
   <group-principal>        ::= 'group:' <groupname>
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
#if 0
// FIXME: need to re-do how ACLs are set and read

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
        ace->deny = FALSE;
    } else if (!strncmp("deny", p, 4)) {
        ace->deny = TRUE;
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
    } else if (! strncmp(p, "group:", 6)) {
    	ace->principal.type = STORE_PRINCIPAL_GROUP;
    	ace->principal.group = p + 6;
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
#endif

CCode 
StoreCommandAUTHSYSTEM(StoreClient *client, char *md5hash)
{
	char credential[XPLHASH_MD5_LENGTH];
	xpl_hash_context hash;

	if (StoreAgent.installMode) goto success;

	XplHashNew(&hash, XPLHASH_MD5);
	XplHashWrite(&hash, client->authToken, strlen(client->authToken));
	XplHashWrite(&hash, StoreAgent.server.hash, NMAP_HASH_SIZE);
	XplHashFinal(&hash, XPLHASH_LOWERCASE, credential, XPLHASH_MD5_LENGTH);

	if (strcmp((const char *)credential, md5hash)) {
		XplDelay(2000);
		ConnWriteStr(client->conn, MSG3242BADAUTH);
		ConnFlush(client->conn);
		return -1;
	}

success:
	client->flags |= STORE_CLIENT_FLAG_MANAGER;
	return ConnWriteStr(client->conn, MSG1000OK);
}
