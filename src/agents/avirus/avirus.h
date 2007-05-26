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
 * (C) 2007 Patrick Felt
 ****************************************************************************/

#ifndef _AVIRUS_H
#define _AVIRUS_H

#include <connio.h>
#include <mdb.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>

#define PRODUCT_SHORT_NAME "avirus.nlm"

#define ANTIVIRUS_EXT "av"

#define NO_VIRUS 1

#define CONNECTION_TIMEOUT (15 * 60)

#define MIME_REALLOC_SIZE 20

#define CLAMAV_DEFAULT_ADDRESS  "127.0.0.1"
#define CLAMAV_DEFAULT_PORT     3310

typedef enum _AVirusStates {
    AV_STATE_STARTING = 0, 
    AV_STATE_INITIALIZING, 
    AV_STATE_LOADING, 
    AV_STATE_RUNNING, 
    AV_STATE_RELOADING, 
    AV_STATE_UNLOADING, 
    AV_STATE_STOPPING, 
    AV_STATE_DONE, 

    AV_STATE_MAX_STATES
} AVirusStates;

typedef enum _AVirusFlags {
    AV_FLAG_USE_CA = (1 << 0), 
    AV_FLAG_USE_MCAFEE = (1 << 1), 
    AV_FLAG_USE_SYMANTEC = (1 << 2), 
    AV_FLAG_NOTIFY_USER = (1 << 3), 
    AV_FLAG_NOTIFY_SENDER = (1 << 4), 
    AV_FLAG_SCAN_INCOMING = (1 << 5), 
    AV_FLAG_USE_COMMANDAV = (1 << 6), 
    AV_FLAG_USE_CLAMAV = (1 << 7),

    AV_FLAG_SCAN = (1 << 29), 
    AV_FLAG_INFECTED = (1 << 30), 
    AV_FLAG_CURED = (1 << 31)
} AVirusFlags;

typedef enum _AVirusClientFlags {
    AV_CLIENT_FLAG_NEW = (1 << 0), 
    AV_CLIENT_FLAG_WAITING = (1 << 1), 
    AV_CLIENT_FLAG_EXITING = (1 << 2)
} AVirusClientFlags;

typedef struct _AVMIME {
    AVirusFlags flags;

    struct {
        unsigned long start;
        unsigned long size;
    } part;

    unsigned char *type;
    unsigned char *encoding;
    unsigned char *fileName;
    unsigned char *virusName;
} AVMIME;

typedef struct _AVRecipients {
    struct _AVRecipients *next;

    unsigned char *name;
} AVRecipients;

typedef struct {
    AVirusClientFlags flags;

    Connection *conn;
    void *handle;

    MDBValueStruct *uservs;

    unsigned char *envelope;
    unsigned char work[XPL_MAX_PATH + 1];
    unsigned char line[CONN_BUFSIZE + 1];
    unsigned char command[CONN_BUFSIZE + 1];
    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];

    struct {
        unsigned long used;
        unsigned long allocated;
        unsigned long current;

        AVMIME *cache;
    } mime;

    struct {
	unsigned long used;
	unsigned long allocated;
	
	char **names;
    } foundViruses;
} AVClient;

typedef struct _AVirusGlobals {
    AVirusStates state;
    AVirusFlags flags;

    struct {
        XplSemaphore semaphore;

        struct {
            XplSemaphore todo;

            XplAtomic maximum;
            XplAtomic active;
            XplAtomic idle;

            Connection *head;
            Connection *tail;
        } worker;

        struct {
            BOOL enable;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        } ssl;

        Connection *conn;

        void *pool;

        time_t sleepTime;

        unsigned long queue;

        unsigned char address[80];
        unsigned char hash[NMAP_HASH_SIZE];
    } nmap;

    struct {
        MDBHandle directory;

        void *logging;
    } handle;

    struct {
        XplThreadID main; /* Tid */
        XplThreadID group; /* TGid */

        XplSemaphore semaphore;

        unsigned long next;
    } id;

    struct {
        XplRWLock config; /*  */
        XplRWLock pattern;
    } lock;

    struct {
        unsigned char work[XPL_MAX_PATH + 1];
        unsigned char patterns[XPL_MAX_PATH + 1];
    } path;

    struct {
        XplSemaphore main;
        XplSemaphore shutdown;
    } sem;

    struct {
        XplAtomic active;
    } server;

    struct {
        struct {
            XplAtomic scanned; /* Scanned */
        } messages;

        struct {
            XplAtomic scanned; /* Scanned */
            XplAtomic blocked; /* Blocked */
        } attachments;

        XplAtomic viruses; /* Viruses */
    } stats;

    struct {
	struct sockaddr_in addr;
    } clam;
} AVirusGlobals;

extern AVirusGlobals AVirus;

/* mime.c */
void ClearMIMECache(AVClient *client);
int LoadMIMECache(AVClient *client);

/* stream.c */
BOOL MWHandleNamedTemplate(void *ignored1, unsigned char *ignored2, void *ignored3);
int StreamAttachmentToFile(AVClient *client, unsigned char *queueID, AVMIME *mime);

#endif /* _AVIRUS_H */
