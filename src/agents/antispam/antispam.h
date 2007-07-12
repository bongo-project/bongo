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

#ifndef _ANTISPAM_H
#define _ANTISPAM_H

#include <connio.h>
#include <mdb.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>

#define PRODUCT_SHORT_NAME "bongoantispam.nlm"

#define CONNECTION_TIMEOUT (15 * 60)

typedef enum _ASpamStates {
    ASPAM_STATE_STARTING = 0, 
    ASPAM_STATE_INITIALIZING, 
    ASPAM_STATE_LOADING, 
    ASPAM_STATE_RUNNING, 
    ASPAM_STATE_RELOADING, 
    ASPAM_STATE_UNLOADING, 
    ASPAM_STATE_STOPPING, 
    ASPAM_STATE_DONE, 

    ASPAM_STATE_MAX_STATES
} ASpamStates;

typedef enum _ASpamFlags {
    ASPAM_FLAG_RETURN_TO_SENDER = (1 << 0), 
    ASPAM_FLAG_NOTIFY_POSTMASTER = (1 << 1), 
} ASpamFlags;

typedef enum _ASpamClientFlags {
    ASPAM_CLIENT_FLAG_NEW = (1 << 0), 
    ASPAM_CLIENT_FLAG_WAITING = (1 << 1), 
    ASPAM_CLIENT_FLAG_EXITING = (1 << 2)
} ASpamClientFlags;

typedef struct {
    ASpamClientFlags flags;

    Connection *conn;

    unsigned char *envelope;
    unsigned char line[CONN_BUFSIZE + 1];
    unsigned char command[CONN_BUFSIZE + 1];
    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
} ASpamClient;

typedef struct {
    AddressPool hosts;
    BongoArray *hostlist;
    BOOL enabled;
    unsigned long connectionTimeout;
} SpamdConfig;

typedef struct _ASpamGlobals {
    ASpamStates state;
    ASpamFlags flags;
    unsigned long quarantineQueue;

    unsigned char officialName[MAXEMAILNAMESIZE + 1];

    struct {
        unsigned long used;
        MDBValueStruct *list;
    } allow;

    struct {
        unsigned long used;
        MDBValueStruct *list;
    } disallow;

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
        XplThreadID main;
        XplThreadID group;
    } id;

    struct {
        XplSemaphore main;
        XplSemaphore shutdown;
    } sem;

    struct {
        XplAtomic active;
    } server;

    struct {
        XplAtomic called;
    } stats;

    SpamdConfig spamd;
} ASpamGlobals;

extern ASpamGlobals ASpam;

/* spamd.c */
BOOL SpamdCheck(SpamdConfig *spamd, ASpamClient *client, const char *queueID, BOOL hasFlags, unsigned long msgFlags, unsigned long senderIp, char *senderUserName);
void SpamdShutdown(SpamdConfig *spamd);
void SpamdStartup(SpamdConfig *spamd);
BOOL SpamdReadConfiguration(SpamdConfig *spamd, BongoJsonNode *node);

#endif /* _ANTISPAM_H */
