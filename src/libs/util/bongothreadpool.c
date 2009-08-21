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

#include <config.h>
#include <time.h>
#include "bongothreadpool.h"
#include "memmgr.h"

struct _BongoWorkItem {
    BongoThreadPoolHandler handler;
    void *data;
    
    BongoWorkItem *next;
};

static void 
Worker(void *data) 
{
    BongoThreadPool *pool = data;
    BOOL done = FALSE;
    time_t beforeSleep;
    time_t afterSleep;
    BOOL first = TRUE;
    
    do {
        BongoWorkItem *item;
        
        XplRenameThread(XplGetThreadID(), pool->name);

        /* Wait on the todo signal for work to do */
        beforeSleep = time(NULL);
        XplWaitOnLocalSemaphore(pool->todo);
        afterSleep = time(NULL);

        /* Pull a work item from the queue */
        XplMutexLock(pool->lock);

        if (first) {
            first = FALSE;
        } else {
            pool->idle--;
        }

        if (pool->shutdown) {
            pool->total--;
            XplMutexUnlock(pool->lock);
            break;
        }
        
        item = pool->head;
        
        if (item) {
            pool->head = pool->head->next;
            if (pool->head == NULL) {
                pool->tail = NULL;
            }
            item->next = NULL;
        }

        /* Contemplate death */
        if ((afterSleep - beforeSleep) > pool->minSleep
            && (pool->total > pool->minimum)) {
            pool->total--;
            done = TRUE;
        }
        
        XplMutexUnlock(pool->lock);

        /* Do the work */
        if (item) {
            item->handler(item->data);
            MemFree(item);
        }

        if (!done) {
            XplMutexLock(pool->lock);
            pool->idle++;
            XplMutexUnlock(pool->lock);
        }
    } while (!done);

    XplExitThread(TSR_THREAD, 0);
}

BongoThreadPool *
BongoThreadPoolNew(const char *name, 
                  int stackSize,
                  int minimum,
                  int maximum,
                  int minSleep) 
{
    BongoThreadPool *pool;

    /* FIXME: do we want a private mempool for work items? */
    
    pool = MemMalloc(sizeof(BongoThreadPool));
    memset(pool, 0, sizeof(BongoThreadPool));
    
    XplMutexInit(pool->lock);
    XplOpenLocalSemaphore(pool->todo, 0);
    pool->minimum = minimum;
    pool->maximum = maximum;
    pool->minSleep = minSleep;
    pool->name = MemStrdup(name);
    pool->stackSize = stackSize;

    return pool;
}

void
BongoThreadPoolShutdown(BongoThreadPool *pool)
{
    int numWorkers;
    int i;
    
    XplMutexLock(pool->lock);

    pool->shutdown = TRUE;
    
    /* Wake up all the worker threads */
    numWorkers = pool->total;
    while (numWorkers--) {
        XplSignalLocalSemaphore(pool->todo);
    }

    XplMutexUnlock(pool->lock);

    /* Wait for the worker threads to go away */
    for (i = 0; pool->total && (i < 60); i++) {
        XplDelay(1000);
    }
}

void
BongoThreadPoolFree(BongoThreadPool *pool)
{
    BongoWorkItem *item;
    
    XplMutexDestroy(pool->lock);
    XplCloseLocalSemaphore(pool->todo);

    for (item = pool->head; item != NULL; item = item->next) {
        MemFree(item);
    }
}

int
BongoThreadPoolAddWork(BongoThreadPool *pool,
                      BongoThreadPoolHandler handler,
                      void *data)
{
    BongoWorkItem *item;
    int ret;
    BOOL spawnThread = FALSE;

    item = MemMalloc(sizeof(BongoWorkItem));
    item->handler = handler;
    item->data = data;

    XplMutexLock(pool->lock);

    item->next = NULL;
    if (pool->tail) {
        pool->tail->next = item;
    }

    if (pool->idle == 0) {
        spawnThread = TRUE;     
    }
    
    pool->tail = item;
    if(pool->head == NULL) {
        pool->head = item;
    }

    XplMutexUnlock(pool->lock);

    XplSignalLocalSemaphore(pool->todo);

    /* Possibly spawn a worker */
    if (spawnThread && (pool->total < pool->maximum)) {
        XplThreadID id;
        pool->total++;
        XplBeginThread(&(id), Worker, pool->stackSize, pool, ret);
    } else {
        ret = 0;
    }

    return ret;
}

void
BongoThreadPoolGetStatistics(BongoThreadPool *pool, 
                            BongoThreadPoolStatistics *statistics)
{
    BongoWorkItem *item;
    int i;
    
    XplMutexLock(pool->lock);
    statistics->total = pool->total;
    for (item = pool->head, i = 0; 
         item != NULL;
         item = item->next, i++) {
    }
    
    statistics->queueLength = i;

    XplMutexUnlock(pool->lock);
}

