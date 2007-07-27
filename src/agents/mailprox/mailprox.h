/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
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

#ifndef _MAILPROX_H
#define _MAILPROX_H

#include <connio.h>
#include <mdb.h>
#include <msgapi.h>
#include <nmap.h>
#include <management.h>

#define PRODUCT_SHORT_NAME "mailprox.nlm"

#define PORT_IMAP 143
#define PORT_IMAP_SSL 993
#define PORT_POP3 110
#define PORT_POP3_SSL 995

#define PROTO_POP3 1
#define PROTO_IMAP 2

#define STATE_IDLE 0
#define STATE_TALKING 1
#define STATE_ENDING 2

#define POP_OK '+'
#define POP_ERR '-'
#define POP_EOD '.'

#define PROXY_STACK_SPACE (48 * 1024)

#define CONNECTION_TIMEOUT (15 * 60)

enum MailProxyStates {
    MAILPROXY_STATE_STARTING = 0, 
    MAILPROXY_STATE_INITIALIZING, 
    MAILPROXY_STATE_LOADING, 
    MAILPROXY_STATE_RUNNING, 
    MAILPROXY_STATE_RELOADING, 
    MAILPROXY_STATE_UNLOADING, 
    MAILPROXY_STATE_STOPPING, 
    MAILPROXY_STATE_DONE, 

    MAILPROXY_MAX_STATES
};

enum MailProxyClientStates {
    MAILPROXY_CLIENT_FRESH = 0, 
    MAILPROXY_CLIENT_AUTHORIZATION, 
    MAILPROXY_CLIENT_TRANSACTION, 
    MAILPROXY_CLIENT_UPDATE, 
    MAILPROXY_CLIENT_ENDING, 

    MAILPROXY_CLIENT_MAX_STATES
};

enum MailProxyClientFlags {
    MAILPROXY_FLAG_ENABLED = (1 << 0), 
    MAILPROXY_FLAG_IMAP = (1 << 1), 
    MAILPROXY_FLAG_POP = (1 << 2), 
    MAILPROXY_FLAG_LEAVE_MAIL = (1 << 3), 
    MAILPROXY_FLAG_STORE_UID = (1 << 4), 
    MAILPROXY_FLAG_BAD_PASSWORD = (1 << 5), 
    MAILPROXY_FLAG_BAD_HOST = (1 << 6), 
    MAILPROXY_FLAG_BAD_HANDSHAKE = (1 << 7), 
    MAILPROXY_FLAG_BAD_PROXY = (1 << 8), 
    MAILPROXY_FLAG_SSL = (1 << 9),
    MAILPROXY_FLAG_ALLFOLDERS = (1 << 10)
};

typedef struct _ProxyUID {
    struct _ProxyUID *next;
    unsigned long uid;
    unsigned char *folderPath;
} ProxyUID;

typedef struct _ProxyList {
    struct _ProxyList *next;

    enum MailProxyClientFlags flags;

    unsigned short port;

    unsigned char *title;
    unsigned char *host;
    unsigned char *user;
    unsigned char *password;
    unsigned char *uid; /* for POP3 code */
    unsigned long uidCount;
    ProxyUID uidList;
    unsigned char *lastError;
} ProxyAccount;

typedef struct _MailProxyQueue {
    struct _MailProxyQueue *next;
    struct _MailProxyQueue *previous;

    unsigned char user[MAXEMAILNAMESIZE + 1];
    unsigned char context[MDB_MAX_OBJECT_CHARS + 1];

    MDBValueStruct *accounts;
} MailProxyQueue;

typedef struct _MailProxyClient {
    enum MailProxyClientStates state;

    Connection  *conn;
    Connection  *nmap;


    unsigned char line[CONN_BUFSIZE + 1];
    unsigned char lastUID[128];  /* for POP3 code */

    ProxyAccount *list;

    MailProxyQueue *q;
} MailProxyClient;

typedef struct {
    enum MailProxyStates state;

    BOOL stopped;
    BOOL force;

    unsigned long runInterval;

    unsigned char postmaster[MDB_MAX_OBJECT_CHARS + 1];

    MDBValueStruct *contexts;

    XplRWLock lock;

    struct {
        XplSemaphore semaphore;

        struct {
            BOOL enable;

            unsigned long options;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        } ssl;

        struct {
            XplSemaphore todo;

            XplAtomic maximum;
            XplAtomic active;
            XplAtomic idle;

            MailProxyQueue *head;
            MailProxyQueue *tail;
        } worker;

        void *pool;

        time_t sleepTime;
    } client;

    struct {
        MDBHandle directory;

        void *logging;
    } handle;

    struct {
        XplThreadID main;
        XplThreadID group;
    } id;

    struct {
        int accounts;
        int threadsParallel;
    } max;

    struct {
        struct {
            BOOL enable;

            bongo_ssl_context *context;
        } ssl;

        unsigned char address[80];
        unsigned char hash[NMAP_HASH_SIZE];
    } nmap;

    struct {
        XplSemaphore main;
        XplSemaphore shutdown;
    } sem;

    struct {
        struct {
            BOOL enable;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        } ssl;

        Connection *conn;

        XplAtomic active;

        struct sockaddr_in addr;
    } server;

    struct {
        XplAtomic serviced;
        XplAtomic messages;
        XplAtomic kiloBytes;
        XplAtomic wrongPassword;
    } stats;
} MailProxyGlobals;

extern MailProxyGlobals MailProxy;

/* imap.c */
typedef struct _IMAPFolderList {
    struct _IMAPFolderList *next;
    
    unsigned char *folderPath;
} IMAPFolderPath;

void FreeFolderList(IMAPFolderPath *folder);
ProxyUID *GetProxyUID(ProxyAccount *proxy, const unsigned char *folderPath);
unsigned long ProxyIMAPAccount(MailProxyClient *client, ProxyAccount *account);

/* pop.c */
unsigned long ProxyPOP3Account(MailProxyClient *client, ProxyAccount *account);

#endif /* _MAILPROX_H */
