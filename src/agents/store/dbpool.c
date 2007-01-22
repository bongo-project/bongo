/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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
#include <xpl.h>
#include <sqlite3.h>
#include <assert.h>

#include "stored.h"

/* 
   simple cache of db handles.  
   FIXME: tweak capacity, and implement timeouts.
*/


struct _DBPoolEntry {
    char *user;
    DStoreHandle *handle;
    uint64_t lastAccessTime;    /* not used yet */
    struct _DBPoolEntry *prev;
    struct _DBPoolEntry *next;
};


static void
DBPoolEntryDelete(DBPoolEntry *entry)
{
    MemFree(entry->user);
    MemFree(entry);
}


int
DBPoolInit()
{
    StoreAgent.dbpool.entries = BongoCreateStringHashTable(64);
    if (!StoreAgent.dbpool.entries) {
        return -1;
    }
    StoreAgent.dbpool.first = NULL;
    StoreAgent.dbpool.last = NULL;
    XplMutexInit(StoreAgent.dbpool.lock);

    return 0;
}


DStoreHandle *
DBPoolGet(const char *user)
{
    DBPoolEntry *entry;
    DStoreHandle *handle;

    XplMutexLock(StoreAgent.dbpool.lock); {

        entry = (DBPoolEntry *) 
            BongoHashtableRemove(StoreAgent.dbpool.entries, user);
        
        if (!entry) {
            XplMutexUnlock(StoreAgent.dbpool.lock);
            return NULL;
        }
        if (entry->prev) {
            entry->prev->next = entry->next;
        } else {
            StoreAgent.dbpool.first = entry->next;
        }
        if (entry->next) {
            entry->next->prev = entry->prev;
        } else {
            StoreAgent.dbpool.last = entry->prev;
        }

    } XplMutexUnlock(StoreAgent.dbpool.lock);

    handle = entry->handle;
    DBPoolEntryDelete(entry);

    return handle;
}


int
DBPoolPut(const char *user, DStoreHandle *handle)
{
    DBPoolEntry *entry;
    int result = -1;

    DStoreSetMemStack(handle, NULL);

    entry = MemMalloc(sizeof(DBPoolEntry));
    if (!entry) {
        return -1;
    }
    entry->user = MemStrdup(user);
    if (!entry->user) {
        MemFree(entry);
        return -1;
    }
    entry->prev = NULL;
    entry->handle = handle;
    entry->lastAccessTime = 0; /* FIXME */

    XplMutexLock(StoreAgent.dbpool.lock); {

        if (BongoHashtablePutNoReplace(StoreAgent.dbpool.entries, 
                                      entry->user, entry)) 
        {
            DBPoolEntryDelete(entry);
            goto finish;
        }
        
        if (StoreAgent.dbpool.first) {
            StoreAgent.dbpool.first->prev = entry;
        }
        entry->next = StoreAgent.dbpool.first;
        StoreAgent.dbpool.first = entry;
        if (!StoreAgent.dbpool.last) {
            StoreAgent.dbpool.last = entry;
        }

        result = 0;

        if (BongoHashTableCount(StoreAgent.dbpool.entries) > 
            StoreAgent.dbpool.capacity)
        {
            entry = StoreAgent.dbpool.last;
            entry->prev->next = NULL;
            StoreAgent.dbpool.last = entry->prev;
            (void) BongoHashtableRemoveKeyValue(StoreAgent.dbpool.entries, 
                                               entry->user, entry, FALSE, FALSE);
            DStoreClose(entry->handle);
            DBPoolEntryDelete(entry);
        }
        
    finish:
        ;
        
    } XplMutexUnlock(StoreAgent.dbpool.lock);

    return result;
}
