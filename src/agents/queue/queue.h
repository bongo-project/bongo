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

#ifndef QUEUE_H
#define QUEUE_H

#include "queued.h"
#include "conf.h"

#define SPOOL_LOCK_ARRAY_SIZE 256
#define SPOOL_LOCK_IDARRAY_SIZE 64

typedef struct _QueuePushClient {
    int port;
    int queue;

    unsigned long address;
    unsigned long usageCount;
    unsigned long errorCount;

    unsigned char identifier[101];
} QueuePushClient;

typedef struct _Queue {
    /* Queue IDs */
    unsigned long queueID;
    XplMutex queueIDLock;

    struct {
        XplSemaphore semaphores[SPOOL_LOCK_ARRAY_SIZE];
        unsigned long *entryIDs;
    } spoolLocks;

    /* Queue agent management  */
    struct {
        XplMutex lock;
        
        int count;
        int allocated;
        
        QueuePushClient *array;
    } pushClients;

    BOOL pushClientsRegistered[8];

    /* Worker threads */
    /* FIXME: use a thread pool here */
    XplSemaphore todo;
    XplAtomic activeWorkers;

#if QUEUE_COMPLETE
    XplAtomic maximumWorkers;

    long defaultSequential;
    long maxSequential;

    long check;
#endif

    BOOL restartNeeded;
    BOOL flushNeeded;

    /* Bounce management */
    time_t lastBounce;
    unsigned long bounceCount;

    /* Statistics */
    XplAtomic numServicedAgents;
    XplAtomic queuedLocal;
    XplAtomic localDeliveryFailed;
    XplAtomic remoteDeliveryFailed;
    XplAtomic totalSpam;
} MessageQueue;

extern MessageQueue Queue;

FILE * 	fopen_check(FILE **handle, char *path, char *mode, int line);
int	fclose_check(FILE **handle, int line);
int	unlink_check(char *path, int line);
int	rename_check(const char *oldpath, const char *newpath, int line);

BOOL QueueInit(void);
void QueueShutdown(void);

int CreateQueueThreads(BOOL failed);

int CommandQaddm(void *param);
int CommandQaddq(void *param);
int CommandQabrt(void *param);
int CommandQbody(void *param);
int CommandQbraw(void *param);
int CommandQcopy(void *param);
int CommandQcrea(void *param);
int CommandQdele(void *param);
int CommandQdone(void *param);
int CommandQdspc(void *param);
int CommandQend(void *param);
int CommandQgrep(void *param);
int CommandQhead(void *param);
int CommandQinfo(void *param);
int CommandQmodFrom(void *param);
int CommandQmodFlags(void *param);
int CommandQmodLocal(void *param);
int CommandQmodMailbox(void *param);
int CommandQmodRaw(void *param);
int CommandQmodTo(void *param);
int CommandQmime(void *param);
int CommandQmove(void *param);
int CommandQrcp(void *param);
int CommandQretr(void *param);
int CommandQrts(void *param);
int CommandQrun(void *param);
int CommandQsql(void *param);
int CommandQsrchDomain(void *param);
int CommandQsrchHeader(void *param);
int CommandQsrchBody(void *param);
int CommandQsrchBraw(void *param);
int CommandQstorAddress(void *param);
int CommandQstorCal(void *param);
int CommandQstorFlags(void *param);
int CommandQstorFrom(void *param);
int CommandQstorLocal(void *param);
int CommandQstorMessage(void *param);
int CommandQstorRaw(void *param);
int CommandQstorTo(void *param);
int CommandQwait(void *param);
int CommandQflush(void *param);

#endif
