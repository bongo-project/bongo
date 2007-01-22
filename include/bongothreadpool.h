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

#ifndef BONGOTHREADPOOL_H
#define BONGOTHREADPOOL_H

#include <xpl.h>

typedef void (*BongoThreadPoolHandler)(void *data);

typedef struct _BongoWorkItem BongoWorkItem;

typedef struct {
    const char *name;

    XplMutex lock;    
    XplSemaphore todo;

    int stackSize;
    int minimum;
    int maximum;
    int minSleep;
    int total;
    int idle;

    BongoWorkItem *head;
    BongoWorkItem *tail;

    BOOL shutdown;
} BongoThreadPool;

typedef struct {
    int total;
    int queueLength;
} BongoThreadPoolStatistics;

BongoThreadPool *BongoThreadPoolNew(const char *name, 
                                  int stackSize,
                                  int minimum,
                                  int maximum,
                                  int minSleep);

int BongoThreadPoolAddWork(BongoThreadPool *pool,
                          BongoThreadPoolHandler handler,
                          void *data);

void BongoThreadPoolGetStatistics(BongoThreadPool *pool, BongoThreadPoolStatistics *statistics);

void BongoThreadPoolShutdown(BongoThreadPool *pool);
void BongoThreadPoolFree(BongoThreadPool *pool);


#endif
