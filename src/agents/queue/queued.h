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

#ifndef QUEUED_H
#define QUEUED_H

#define LOGGERNAME "queue"

#include <xpl.h>
#include <connio.h>
#include <mdb.h>
#include <management.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>
#include <bongoagent.h>
#include <bongoutil.h>

#include "mime.h"

#define AGENT_NAME "bongoqueue"

typedef struct {
    Connection *conn;

    BOOL authorized;

    BOOL done;

    /* Buffers */
    unsigned char authChallenge[MAXEMAILNAMESIZE + 1];
    unsigned char buffer[CONN_BUFSIZE + 1];
    unsigned char line[CONN_BUFSIZE + 1];
    unsigned char path[XPL_MAX_PATH + 1];

    /* Active queue entry */
    struct {
        FILE *control;
        FILE *data;
        FILE *work;
        
        unsigned long id;
        unsigned long target;

        unsigned char workQueue[12];

        MIMEReportStruct *report;
    } entry;
} QueueClient;

enum {
    QUEUE_AGENT_DEBUG = (1 << 0),
    QUEUE_AGENT_DISK_SPACE_LOW = (1 << 1),
};    

typedef struct _QueueAgent {
    BongoAgent agent;

    int flags;
    
    /* Client management */
    Connection *clientListener;
    void *clientMemPool;
    BongoThreadPool *clientThreadPool;
    
    /* Command trees */
    ProtocolCommandTree authCommands;
    ProtocolCommandTree commands;

    /* Stats */
    XplAtomic activeThreads;
} QueueAgent;

extern QueueAgent Agent;

#if defined(RELEASE_BUILD)
#define QueueClientAlloc() MemPrivatePoolGetEntry(Agent.clientMemPool)
#else
#define QueueClientAlloc() MemPrivatePoolGetEntryDebug(Agent.clientMemPool, __FILE__, __LINE__);
#endif

void QueueClientFree(void *clientp);
int HandleCommand(QueueClient *client);

#define STACKSPACE_Q (1024*80)

#endif /* QUEUED_H */
