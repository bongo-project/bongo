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

#ifndef _ANTIVIRUS_H
#define _ANTIVIRUS_H


#ifdef LOGGERNAME
#undef LOGGERNAME
#endif

#define AGENT_NAME  "antivirus"
#define LOGGERNAME "antivirus"
#include <logger.h>

#include <connio.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>

#define PRODUCT_SHORT_NAME "bongoavirus.nlm"

#define CONNECTION_TIMEOUT (15 * 60)

typedef enum _AVirusStates {
    AVIRUS_STATE_STARTING = 0, 
    AVIRUS_STATE_INITIALIZING, 
    AVIRUS_STATE_LOADING, 
    AVIRUS_STATE_RUNNING, 
    AVIRUS_STATE_RELOADING, 
    AVIRUS_STATE_UNLOADING, 
    AVIRUS_STATE_STOPPING, 
    AVIRUS_STATE_DONE, 

    AVIRUS_STATE_MAX_STATES
} AVirusStates;

typedef enum _AVirusFlags {
    AVIRUS_FLAG_RETURN_TO_SENDER = (1 << 0), 
    AVIRUS_FLAG_NOTIFY_POSTMASTER = (1 << 1), 
} AVirusFlags;

typedef enum _AVirusClientFlags {
    AVIRUS_CLIENT_FLAG_NEW = (1 << 0), 
    AVIRUS_CLIENT_FLAG_WAITING = (1 << 1), 
    AVIRUS_CLIENT_FLAG_EXITING = (1 << 2)
} AVirusClientFlags;

typedef struct {
    AVirusClientFlags flags;

    Connection *conn;

    unsigned int envelopeLength;
    unsigned int messageLength;
    unsigned int envelopeLines;

    char qID[16];   /* holds the queueID */
    unsigned char *envelope;
    unsigned char line[CONN_BUFSIZE + 1];
    unsigned char command[CONN_BUFSIZE + 1];
} AVirusClient;


typedef struct _AVirusGlobals {
    AVirusStates state;
    AVirusFlags flags;
    struct {
        struct {
            BOOL enable;
            ConnSSLConfiguration config;
            bongo_ssl_context *context;
            Connection *conn;
        } ssl;

        Connection *conn;
        unsigned long queue;
    } nmap;

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

    struct {
        AddressPool hosts;
        BongoArray *hostlist;
        BOOL enabled;
        unsigned long connectionTimeout;
    } clamd;

    BongoAgent agent;
    void *QueueMemPool;
    BongoThreadPool *QueueThreadPool;
    Connection *QueueConnection;
} AVirusGlobals;

extern AVirusGlobals AVirus;

/* spamd.c */
BOOL VirusCheck(AVirusClient *client, const char *queueID, BOOL hasFlags, unsigned long msgFlags, unsigned long senderIp, char *senderUserName);
void VirusShutdown();
void VirusStartup();
BOOL VirusReadConfiguration(BongoJsonNode *node);

#endif /* _ANTIVIRUS_H */
