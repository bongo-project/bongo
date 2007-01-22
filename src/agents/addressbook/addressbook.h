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

#ifndef _ADDRESSBOOK_H
#define _ADDRESSBOOK_H

#include <xpl.h>

XPL_BEGIN_C_LINKAGE

#include <bongoutil.h>
#include <connio.h>
#include <mdb.h>
#include <management.h>
#include <msgapi.h>
#include <bongoagent.h>
#include <nmap.h>
#include <bongostore.h>

#define AGENT_NAME "bongoaddressbook"
#define AGENT_DN "Addressbook Agent"

enum {
    ADDRESSBOOK_CLIENT_FLAG_MANAGER = (1 << 0)
};

#define IS_MANAGER(client) (((client)->flags & ADDRESSBOOK_CLIENT_FLAG_MANAGER) == ADDRESSBOOK_CLIENT_FLAG_MANAGER)

typedef struct _AddressbookClient AddressbookClient;

struct _AddressbookClient {
    Connection *conn;

    int flags;
    
    char authToken[64];
    
    char user[64];
    char dn[MDB_MAX_OBJECT_CHARS + 1];
    
    char parentObject[MDB_MAX_OBJECT_CHARS + 1];
    
    char buffer[CONN_BUFSIZE + 1];
};


struct AddressbookGlobals {
    BongoAgent agent;

    Connection *listenConn; /* this is the conn we're listening on */
    
    void *memPool;
    BongoThreadPool *threadPool;

    char defaultContext[MDB_MAX_OBJECT_CHARS + 1];
    
    struct {
        MDBHandle directory;

        void *logging;
    } handle;
    
    XplRWLock configLock;

    struct {
        struct {
            BOOL enable;
            ConnSSLConfiguration config;
            bongo_ssl_context *context;
            Connection *conn;
        } ssl;

        BOOL bound;

        Connection *conn;   /*  NMAPServerSocket */

        XplAtomic active;

        int maxClients;
        unsigned long ipAddress;
        unsigned long bytesPerBlock;

        unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char host[MAXEMAILNAMESIZE + 1];
        unsigned char hash[NMAP_HASH_SIZE];
    } server;

    struct {
        int count;
        unsigned long *hosts;
    } trustedHosts;
};


typedef enum {
    ADDRESSBOOK_COMMAND_NULL = 0,
    
    ADDRESSBOOK_COMMAND_AUTH, /* COOKIE, USER or SYSTEM */

    ADDRESSBOOK_COMMAND_SEARCH,
    
    ADDRESSBOOK_COMMAND_QUIT,
} AddressbookCommand;

extern struct AddressbookGlobals AddressbookAgent;


int AddressbookSetupCommands(void);
CCode AddressbookCommandLoop(AddressbookClient *client);

XPL_END_C_LINKAGE


#endif /* _ADDRESSBOOK_H */
