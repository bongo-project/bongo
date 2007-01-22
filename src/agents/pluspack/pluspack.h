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

#ifndef _BONGOPLUSPACK_H
#define _BONGOPLUSPACK_H

#include <connio.h>
#include <mdb.h>
#include <management.h>
#include <msgapi.h>
#include <streamio.h>
#include <nmap.h>
#include <nmlib.h>

#define PRODUCT_SHORT_NAME "bongopluspack.nlm"

#define CONNECTION_TIMEOUT (15 * 60)

typedef enum _PlusPackStates {
    PLUSPACK_STATE_STARTING = 0, 
    PLUSPACK_STATE_INITIALIZING, 
    PLUSPACK_STATE_LOADING, 
    PLUSPACK_STATE_RUNNING, 
    PLUSPACK_STATE_RELOADING, 
    PLUSPACK_STATE_UNLOADING, 
    PLUSPACK_STATE_STOPPING, 
    PLUSPACK_STATE_DONE, 

    PLUSPACK_STATE_MAX_STATES
} PlusPackStates;

typedef enum _PlusPackFlags {
    PLUSPACK_FLAG_SIGNATURE_HTML = (1 << 0), 
    PLUSPACK_FLAG_SIGNATURE = (1 << 1), 
    PLUSPACK_FLAG_NO_ATTACHMENTS = (1 << 2), 
    PLUSPACK_FLAG_ACL_ENABLED = (1 << 3), 
    PLUSPACK_FLAG_ACL_SENDNOMEMBERONLY = (1 << 4), 
    PLUSPACK_FLAG_BIGBROTHER_ENABLED = (1 << 5), 
    PLUSPACK_FLAG_ACL_AUTH_REQUIRED = (1 << 6), 
    PLUSPACK_FLAG_ACL_DELETE_UNKNOWN = (1 << 7), 
    PLUSPACK_FLAG_ACL_BOUNCE_ON_DELETE = (1 << 8)
} PlusPackFlags;

typedef enum _PlusPackClientFlags {
    PLUSPACK_CLIENT_FLAG_NEW = (1 << 0), 
    PLUSPACK_CLIENT_FLAG_WAITING = (1 << 1), 
    PLUSPACK_CLIENT_FLAG_EXITING = (1 << 2), 
    PLUSPACK_CLIENT_FLAG_FOUND = (1 << 3), 
    PLUSPACK_CLIENT_FLAG_ALLOWED = (1 << 4), 
    PLUSPACK_CLIENT_FLAG_SIGNATURE = (1 << 5), 
    PLUSPACK_CLIENT_FLAG_SIGNATURE_HTML = (1 << 6), 
    PLUSPACK_CLIENT_FLAG_HAVE_REMOTE = (1 << 7), 
    PLUSPACK_CLIENT_FLAG_COPIED = (1 << 8), 
    PLUSPACK_CLIENT_FLAG_SPLIT = (1 << 9)
} PlusPackClientFlags;

#pragma pack(push, 4)
typedef struct _PlusPackMime {
    BOOL attachments;

    unsigned long size;
    unsigned long header;

    struct {
        unsigned long position;
        unsigned long size;
    } html;

    struct {
        unsigned long position;
        unsigned long size;
    } plain;

    struct {
        BOOL html;

        unsigned long position;
        unsigned long size;
    } signature[2];
} PlusPackMime;

typedef struct _PlusPackClient {
    PlusPackClientFlags flags;

    Connection *conn;

    unsigned char *bounce;
    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char user[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char line[CONN_BUFSIZE + 1];

    PlusPackMime mime;

    MDBValueStruct *vs;

    struct {
        int queue;
        long id;
        unsigned long flags;
        unsigned char name[16];
    } entry;

    struct {
        unsigned long length;
        unsigned char *data;
    } envelope;

    struct {
        unsigned long calendar;
        unsigned long local;
        unsigned long remote;
    } recipients;

    struct {
        unsigned char auth[MAXEMAILNAMESIZE + 1];
        unsigned char claim[MDB_MAX_OBJECT_CHARS + 1];
    } sender;
} PlusPackClient;

typedef struct _PlusPackGlobals {
    PlusPackStates state;
    PlusPackFlags flags;

    unsigned char *allowedGroup;
    unsigned char officialName[MAXEMAILNAMESIZE + 1];
    unsigned char postmaster[MDB_MAX_OBJECT_CHARS + 1];

    struct {
        unsigned char user[MAXEMAILNAMESIZE + 1];
        unsigned char inbox[XPL_MAX_PATH + 1];
        unsigned char outbox[XPL_MAX_PATH + 1];
    } copy;

    struct {
        MDBHandle directory;

        void *logging;
    } handle;

    struct {
        XplThreadID main;
        XplThreadID group;
    } id;

    struct {
        XplSemaphore semaphore;

        struct {
            XplSemaphore todo;

            XplAtomic maximum;
            XplAtomic active;
            XplAtomic idle;

            Connection *head;
            Connection *tail;
        } queue;

        struct {
            BOOL enable;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        } ssl;

        struct {
            Connection *queue5;
            Connection *queue6;
        } conn;

        void *pool;

        time_t sleepTime;

        unsigned char address[80];
        unsigned char hash[NMAP_HASH_SIZE];
    } nmap;

    struct {
        XplSemaphore main;
        XplSemaphore shutdown;
    } sem;

    struct {
        XplAtomic active;
    } server;

    struct {
        unsigned char *plain;
        unsigned char *html;
    } signature;

    struct {
        XplAtomic called;
    } stats;
} PlusPackGlobals;
#pragma pack(pop)

extern PlusPackGlobals PlusPack;

/* management.c */
ManagementVariables *GetPlusPackManagementVariables(void);
int GetPlusPackManagementVariablesCount(void);
ManagementCommands *GetPlusPackManagementCommands(void);
int GetPlusPackManagementCommandsCount(void);
unsigned char *GetPlusPackVersion(void);

#endif /* _BONGOPLUSPACK_H */
