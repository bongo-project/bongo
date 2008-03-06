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

#ifndef __NMAP_LOCK_H
#define __NMAP_LOCK_H

XPL_BEGIN_C_LINKAGE

#define STORE_COLLECTION_MAX_WATCHERS 8

/*********************** Structures ***********************/

// "fair" lock implementation

#define	SEM_UNLOCKED	1
#define SEM_LOCKED	0

typedef struct _NFairLockQueueEntry {
	// "signal" is used by the queueing process to wait its turn
	XplSemaphore *signal;
	struct _NFairLockQueueEntry *next;
	BOOL is_writer;
} NFairLockQueueEntry;

typedef struct _NFairLock {
	char *lockname;
	// "access" mediates changes to the internal data, shouldn't be held for long
	XplSemaphore *access;
	int readers;
	int writer;
	NFairLockQueueEntry *queue;
} NFairLock;

typedef struct _NFairLockList {
	struct _NFairLockList *next;
	NFairLock *lock;
} NFairLockList;

typedef struct _NFairLockPool {
	// lock governs access to the Pool
	XplSemaphore *lock;
	NFairLockList *first;
	NFairLockList *last;
} NFairLockPool;

void	StoreInitializeFairLocks();
int		StoreGetExclusiveFairLockQuiet(StoreClient *client, NFairLock **lock,
                                       uint64_t guid, time_t timeout);
int		StoreGetSharedFairLockQuiet(StoreClient *client, NFairLock **lock, 
                                    uint64_t guid, time_t timeout);
int		StoreReleaseSharedFairLockQuiet(NFairLock **lock);
int		StoreReleaseExclusiveFairLockQuiet(NFairLock **lock);

// "normal" locks

typedef struct _NLock {        /* FIXME: what's with all the unsigned longs? */
    struct _NLock *nextHash;
    struct _NLock *prevHash;
    struct _NLock *nextUID;
    struct _NLock *prevUID;
    unsigned char *uid;
    unsigned long slot;
    unsigned long hash;        /* should be uint32_t */
    unsigned long rLock;
    unsigned long wLock;
    unsigned long eLock;
    unsigned long createTime;
    unsigned long lockTime;

    /* used for WATCH command -- FIXME should be a linked list or growable array */
    StoreClient *watchers[STORE_COLLECTION_MAX_WATCHERS]; 
    NFairLock *fairlock; // HACK - gets around API changes for now.
} NLockStruct;

typedef struct {
    XplSemaphore lock;
    NLockStruct *next;
} NLockHeadStruct;

typedef struct _NLockReportDepth {
    struct _NLockReportDepth *next;
    unsigned long depth;
    unsigned long count;
} NLockReportDepthStruct;

typedef struct _NLockReport {
    unsigned long totalLocks;
    unsigned long slotsAvailable;
    unsigned long slotsUsed;
    NLockReportDepthStruct *slotProfile;
    NLockReportDepthStruct *slotDepthData;
    NLockReportDepthStruct *hashData;
    unsigned char *oldestReadLockUID;
    unsigned long oldestReadLockAge;
    unsigned char *oldestWriteLockUID;
    unsigned long oldestWriteLockAge;
    unsigned char *oldestPurgeLockUID;
    unsigned long oldestPurgeLockAge;
} NLockReportStruct;

/*********************** Definitions ***********************/

#define NLOCK_MEMORY_ERROR -1
#define NLOCK_ACQUIRED 0
#define NLOCK_RELEASED 0
#define NLOCK_INVALID 1
#define NLOCK_HAS_READERS 2
#define NLOCK_HAS_WRITER 3
#define NLOCK_HAS_PURGE 4
#define NLOCK_NOT_FOUND 5
#define NLOCK_HANDLE_NOT_PROVIDED 6
#define NLOCK_HANDLE_HAS_LOCK 7

#define NLOCK_CREATE (1 << 0)
#define NLOCK_COPY (1 << 1)

/*********************** Function Prototypes ***********************/

#if !defined(DEBUG)
#define ReadNLockAcquire(lockHandle, hashHandle, user, resource, timeout) ReadNMAPLockAcquire(lockHandle, hashHandle, user, resource, timeout)
#define ReadNLockRelease(lockHandle) ReadNMAPLockRelease(lockHandle)
#define WriteNLockAcquire(lockHandle, timeout) WriteNMAPLockAcquire(lockHandle, timeout)
#define WriteNLockRelease(lockHandle) WriteNMAPLockRelease(lockHandle)
#define PurgeNLockAcquire(lockHandle, timeout) PurgeNMAPLockAcquire(lockHandle, timeout)
#define PurgeNLockRelease(lockHandle) PurgeNMAPLockRelease(lockHandle)
#define NLockAcquire(lockHandle, hashHandle, user, resource) NMAPLockAcquire(lockHandle, hashHandle, user, resource)
#define NLockRelease(lockHandle) NMAPLockRelease(lockHandle)
#define ReportNLockAquire(lockReport) ReportNMAPLockAcquire(lockReport)
#define ReportNLockRelease(lockReport) ReportNMAPLockRelease(lockReport)
#define NLockInit(size) NMAPLockInit(size)    
#define NLockDestroy() NMAPLockDestroy()

int NLockCountWatchers(NLockStruct *lockHandle);

long ReadNMAPLockAcquire(NLockStruct **lockHandle, unsigned long *hashHandle, unsigned char *user, uint64_t resource, long timeout);
long ReadNMAPLockRelease(NLockStruct *lockHandle);
long WriteNMAPLockAcquire(NLockStruct *lockHandle, long timeout);
long WriteNMAPLockRelease(NLockStruct *lockHandle);
long PurgeNMAPLockAcquire(NLockStruct *lockHandle, long timeout);
long PurgeNMAPLockRelease(NLockStruct *lockHandle);
long NMAPLockAcquire(NLockStruct **lockHandle, unsigned long *hashHandle, unsigned char *user, uint64_t resource);
long NMAPLockRelease(NLockStruct *lockHandle);
BOOL ReportNMAPLockAcquire(NLockReportStruct **lockReport);
void ReportNMAPLockRelease(NLockReportStruct *lockReport);
BOOL NMAPLockInit(unsigned long CacheSize);
void NMAPLockDestroy(void);

#else

#define ReadNLockAcquire(lockHandle, hashHandle, user, resource, timeout) ReadNMAPLockAcquire(lockHandle, hashHandle, user, resource, timeout, __FILE__, __LINE__)
#define ReadNLockRelease(lockHandle) ReadNMAPLockRelease(lockHandle, __FILE__, __LINE__)
#define WriteNLockAcquire(lockHandle, timeout) WriteNMAPLockAcquire(lockHandle, timeout, __FILE__, __LINE__)
#define WriteNLockRelease(lockHandle) WriteNMAPLockRelease(lockHandle, __FILE__, __LINE__)
#define PurgeNLockAcquire(lockHandle, timeout) PurgeNMAPLockAcquire(lockHandle, timeout, __FILE__, __LINE__)
#define PurgeNLockRelease(lockHandle) PurgeNMAPLockRelease(lockHandle, __FILE__, __LINE__)
#define NLockAcquire(lockHandle, hashHandle, user, resource) NMAPLockAcquire(lockHandle, hashHandle, user, resource, __FILE__, __LINE__)
#define NLockRelease(lockHandle) NMAPLockRelease(lockHandle)
#define ReportNLockAcquire(lockReport) ReportNMAPLockAcquire(lockReport)
#define ReportNLockRelease(lockReport) ReportNMAPLockRelease(lockReport)
#define NLockInit(size) NMAPLockInit(size)    
#define NLockDestroy() NMAPLockDestroy()

long ReadNMAPLockAcquire(NLockStruct **lockHandle, unsigned long *hashHandle, unsigned char *user, uint64_t collection, long timeout, unsigned char *file, unsigned long line);
long ReadNMAPLockRelease(NLockStruct *lockHandle, unsigned char *file, unsigned long line);
long WriteNMAPLockAcquire(NLockStruct *lockHandle, long timeout, unsigned char *file, unsigned long line);
long WriteNMAPLockRelease(NLockStruct *lockHandle, unsigned char *file, unsigned long line);
long PurgeNMAPLockAcquire(NLockStruct *lockHandle, long timeout, unsigned char *file, unsigned long line);
long PurgeNMAPLockRelease(NLockStruct *lockHandle, unsigned char *file, unsigned long line);
long NMAPLockAcquire(NLockStruct **lockHandle, unsigned long *hashHandle, unsigned char *user, uint64_t resource, unsigned char *file, unsigned long line);
long NMAPLockRelease(NLockStruct *lockHandle, unsigned char *file, unsigned long line);
BOOL ReportNMAPLockAcquire(NLockReportStruct **lockReport);
void ReportNMAPLockRelease(NLockReportStruct *lockReport);
BOOL NMAPLockInit(unsigned long CacheSize);
void NMAPLockDestroy(void);

#endif

XPL_END_C_LINKAGE

#endif /* __NMAP_LOCK_H */
