/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001-5 Novell, Inc. All Rights Reserved.
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

#include "stored.h"

/* NMAP Lock Cache Definitions */
void *NMAPLockPool = NULL;

/* NMAP Lock Head Array Definitions */
#define NMAP_LOCK_HEAD_MASK 0x000001FF
#define NMAP_LOCK_HEAD_ARRAY_SIZE (NMAP_LOCK_HEAD_MASK + 1)

NLockHeadStruct NLockHeadArray[NMAP_LOCK_HEAD_ARRAY_SIZE];

static BOOL 
NMAPLockAllocInit(void *buffer, void *clientData)
{
    if (buffer != NULL) {
        memset(buffer, 0, sizeof(NLockStruct));
        return(TRUE);
    }

    return(FALSE);
}


__inline static unsigned char *
GetUID(unsigned char *user, uint64_t resource)
{
    unsigned char *uid;
    unsigned char *src;
    unsigned char *dest;
    unsigned long userLen;
    unsigned long i;

    userLen = strlen(user);

    uid = MemMalloc(userLen + 16 + 2);
    if (uid) {
        src = user;
        dest = uid;
        for (i = 0; i < userLen; i++, dest++, src++) {
            *dest = tolower(*src);
        }

        *dest++ = '-';
        snprintf(dest, 17, GUID_FMT, resource);
        dest[16] = '\0';
        return(uid);
    }

    return(NULL);
}

__inline static void
FreeUID(unsigned char *UID)
{
    MemFree(UID);
}

#if !defined(DEBUG)
__inline static long
GetNMAPLock(NLockStruct **lockHandle, unsigned long *hashHandle, unsigned char *user, uint64_t resource, unsigned long mode)
#else
__inline static long
GetNMAPLock(NLockStruct **lockHandle, unsigned long *hashHandle, unsigned char *user, uint64_t resource, unsigned long mode, unsigned char *file, unsigned long line)
#endif
{
    unsigned long slot;
    unsigned long hash;
    unsigned char *uid;
    NLockStruct *nLock;
    NLockStruct *newLock;

    /* Determine UID */
    uid = GetUID(user, resource);
    if (uid) {
        /* Determine hash (if necessary) */
        if (hashHandle) {
            if (*hashHandle) {
                hash = *hashHandle;
            } else {
                BONGO_STRING_HASH(user, hash);
                *hashHandle = hash;
            }
        } else {
            BONGO_STRING_HASH(user, hash);
        }

        /* Get slot */
        slot = hash & NMAP_LOCK_HEAD_MASK;

        XplWaitOnLocalSemaphore(NLockHeadArray[slot].lock);

        nLock = NLockHeadArray[slot].next;

        if (nLock) {
            do {
                if (nLock->hash != hash) {
                    nLock = nLock->nextHash;
                    continue;
                }

                /* We found the uid list */
                do {
                    if (!strcmp((char *) uid, (char *) nLock->uid)) {
                        /* We found the lock for this resource */
                        if (nLock->eLock == 0) {
                            nLock->rLock++;
                            XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
                            *lockHandle = nLock;
                            FreeUID(uid);
                            return(NLOCK_ACQUIRED);
                        }

                        if ((mode & NLOCK_COPY) == FALSE) {
                            XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
                            FreeUID(uid);
                            return(NLOCK_HAS_PURGE);
                        }

#if !defined(DEBUG)
                        newLock = MemPrivatePoolGetEntry(NMAPLockPool);
#else
                        newLock = MemPrivatePoolGetEntryDebug(NMAPLockPool, file, line);
#endif

                        if (newLock) {
                            newLock->rLock = nLock->rLock;
                            newLock->wLock = nLock->wLock;
                            newLock->eLock = nLock->eLock;
                            newLock->slot = nLock->slot;
                            newLock->hash = nLock->hash;
                            newLock->createTime = nLock->createTime;
                            newLock->lockTime = nLock->lockTime;
                            newLock->uid = uid;

                            XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
                            return(NLOCK_HAS_PURGE);
                        }

                        XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
                        FreeUID(uid);
                        return(NLOCK_MEMORY_ERROR);
                    }

                    if (nLock->nextUID) {
                        nLock = nLock->nextUID;
                        continue;
                    }

                    /* Add a lock at the end of the uid list */

                    if (mode & NLOCK_CREATE) {
#if !defined(DEBUG)
                        newLock = MemPrivatePoolGetEntry(NMAPLockPool);
#else
                        newLock = MemPrivatePoolGetEntryDebug(NMAPLockPool, file, line);
#endif

                        if (newLock) {
                            newLock->uid = uid;
                            newLock->createTime = 0; //time(NULL);
                            newLock->hash = hash;
                            newLock->slot = slot;
                            newLock->rLock = 1;
                            newLock->wLock = 0;
                            newLock->eLock = 0;
                            newLock->nextHash = NULL;
                            newLock->prevHash = NULL;
                            newLock->nextUID = NULL;

                            /* There is no match in the list; we need to append a new entry at the end */
                            nLock->nextUID = newLock;
                            newLock->prevUID = nLock;
                            
                            XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
                            *lockHandle = newLock;
                            return(NLOCK_ACQUIRED);
                        }

                        XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
                        FreeUID(uid);
                        return(NLOCK_MEMORY_ERROR);
                    } else {
                        XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
                        FreeUID(uid);
                        return(NLOCK_NOT_FOUND);
                    }
                } while (TRUE);
            } while (nLock);

            /* No hash matches; Add a new lock at the beginning of the hash list */
        }

        if (mode & NLOCK_CREATE) {
#if !defined(DEBUG)
            newLock = MemPrivatePoolGetEntry(NMAPLockPool);
#else
            newLock = MemPrivatePoolGetEntryDebug(NMAPLockPool, file, line);
#endif

            if (newLock) {
                newLock->uid = uid;                
                newLock->createTime = 0; //time(NULL);
                newLock->hash = hash;
                newLock->slot = slot;
                newLock->rLock = 1;
                newLock->wLock = 0;
                newLock->eLock = 0;
                newLock->prevUID = NULL;
                newLock->nextUID = NULL;
                newLock->prevHash = NULL;

                /* add the entry in the slot head */
                if (NLockHeadArray[slot].next) {
                    NLockHeadArray[slot].next->prevHash = newLock;
                    newLock->nextHash = NLockHeadArray[slot].next;
                } else {
                    newLock->nextHash = NULL;
                }
                NLockHeadArray[slot].next = newLock;

                XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
                *lockHandle = newLock;
                return(NLOCK_ACQUIRED);
            }

            XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
            FreeUID(uid);
            return(NLOCK_MEMORY_ERROR);
        } else {
            XplSignalLocalSemaphore(NLockHeadArray[slot].lock);
            FreeUID(uid);
            return(NLOCK_NOT_FOUND);
        }
    }

    return(NLOCK_MEMORY_ERROR);
}

/* Init/Destroy */
BOOL 
NMAPLockInit(unsigned long cacheSize)
{
    unsigned long i;

    memset(&NLockHeadArray, 0, sizeof(NLockHeadArray));
    for (i = 0; i < NMAP_LOCK_HEAD_ARRAY_SIZE; i++) {
        XplOpenLocalSemaphore(NLockHeadArray[i].lock, 1);
    }

    NMAPLockPool = MemPrivatePoolAlloc("NMAP Locks", sizeof(NLockStruct), 0, cacheSize, TRUE, FALSE, NMAPLockAllocInit, NULL, NULL);
    if (NMAPLockPool) {
        return(TRUE);
    }

    return(FALSE);
}

void
NMAPLockDestroy(void)
{
    unsigned long i;
    NLockStruct *nLock;
    NLockStruct *nextHash;
    NLockStruct *toFree;

    for (i = 0; i < NMAP_LOCK_HEAD_ARRAY_SIZE; i++) {
        XplCloseLocalSemaphore(NLockHeadArray[i].lock);
        nLock = NLockHeadArray[i].next;
        while (nLock) {
            nextHash = nLock->nextHash;
            do {
                toFree = nLock;
                nLock = nLock->nextUID;
                FreeUID(toFree->uid);
                MemPrivatePoolReturnEntry((void *)toFree);
            } while (nLock);
            nLock = nextHash;
        }
    }
    
    MemPrivatePoolFree(NMAPLockPool);

    return;
}

/* Read Locks */
#if !defined(DEBUG)
long 
ReadNMAPLockAcquire(NLockStruct **lockHandle, unsigned long *hashHandle, unsigned char *user, uint64_t resource, long timeout)
#else
long 
ReadNMAPLockAcquire(NLockStruct **lockHandle, unsigned long *hashHandle, unsigned char *user, uint64_t resource, long timeout, unsigned char *file, unsigned long line)
#endif
{
    long lockStatus;
    unsigned long delayLen;

    if (lockHandle) {
        if (*lockHandle == NULL) {
            delayLen = timeout >> 3;
            
            do {
#if !defined(DEBUG)
                lockStatus = GetNMAPLock(lockHandle, hashHandle, user, resource, NLOCK_CREATE);
#else
                lockStatus = GetNMAPLock(lockHandle, hashHandle, user, resource, NLOCK_CREATE, file, line);
#endif

                if (lockStatus == NLOCK_ACQUIRED) {
                    return(lockStatus);
                }

                if (lockStatus == NLOCK_HAS_PURGE) {
                    if (timeout > 0) {
                        if (delayLen > 0) {
                            XplDelay(delayLen);
                        } else {
                            delayLen = 1;
                            XplDelay(delayLen);
                        }

                        timeout -= delayLen;
                        delayLen <<= 1;
                        continue;
                    }
                }

                return(lockStatus);

            } while(TRUE);
        }

#if defined(DEBUG)
        XplConsolePrintf("NMAP: ReadNMAPLockAcquire() called with an existing lock from %s:%lu\r\n", file, line); 
#endif

        return(NLOCK_HANDLE_HAS_LOCK);
    }
#if defined(DEBUG)
    XplConsolePrintf("NMAP: ReadNMAPLockAcquire() called with a NULL lock handle from %s:%lu\r\n", file, line); 
#endif

    return(NLOCK_HANDLE_NOT_PROVIDED);
}

#if !defined(DEBUG)
long 
ReadNMAPLockRelease(NLockStruct *lockHandle) 
#else
long 
ReadNMAPLockRelease(NLockStruct *lockHandle, unsigned char *file, unsigned long line) 
#endif
{
    int i;

    if (lockHandle) {
        XplWaitOnLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
        if (lockHandle->rLock > 0) {
            lockHandle->rLock--;
            if (lockHandle->rLock > 0) {
                XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
                return(NLOCK_RELEASED);
            }

#if defined(DEBUG)
            if ((lockHandle->wLock == 0) && (lockHandle->eLock == 0)) {
                ;
            } else {
                XplConsolePrintf("NMAP: ReadNMAPLockRelease() called with values RLock = %lu WLock = %lu and ELock = %lu from %s:%lu\r\n", lockHandle->rLock, lockHandle->wLock, lockHandle->eLock, file, line); 
            }
#endif

            for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
                if (lockHandle->watchers[i]) {
                    XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
                    return NLOCK_RELEASED;
                }
            }

            /* There are no more readers or watchers; remove link from list */
            if (lockHandle->prevUID) {
                lockHandle->prevUID->nextUID = lockHandle->nextUID;
                if (lockHandle->nextUID) {
                    lockHandle->nextUID->prevUID = lockHandle->prevUID;
                }
            } else {
                if (lockHandle->nextUID) {
                    lockHandle->nextUID->prevUID = NULL;
                    lockHandle->nextUID->prevHash = lockHandle->prevHash;
                    lockHandle->nextUID->nextHash = lockHandle->nextHash;

                    if (lockHandle->prevHash) {
                        lockHandle->prevHash->nextHash = lockHandle->nextUID;
                    } else {
                        NLockHeadArray[lockHandle->slot].next = lockHandle->nextUID;
                    }

                    if (lockHandle->nextHash) {
                        lockHandle->nextHash->prevHash = lockHandle->nextUID;
                    }
                } else {
                    if (lockHandle->prevHash) {
                        lockHandle->prevHash->nextHash = lockHandle->nextHash;
                    } else {
                        NLockHeadArray[lockHandle->slot].next = lockHandle->nextHash;
                    }

                    if (lockHandle->nextHash) {
                        lockHandle->nextHash->prevHash = lockHandle->prevHash;
                    }
                }
            }



            XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);

             FreeUID(lockHandle->uid);
            MemPrivatePoolReturnEntry((void *)lockHandle);

            return(NLOCK_RELEASED);
        }

        XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
#if defined(DEBUG)
        XplConsolePrintf("NMAP: ReadNMAPLockRelease() called with no readers from %s:%lu\r\n", file, line); 
#endif

        return(NLOCK_INVALID);
    }

#if defined(DEBUG)
    XplConsolePrintf("NMAP: ReadNMAPLockRelease() called with an NULL lock handle from %s:%lu\r\n", file, line); 
#endif

    return(NLOCK_INVALID);
}

/*********************** Write Locks ***********************/
#if !defined(DEBUG)
long
WriteNMAPLockAcquire(NLockStruct *lockHandle, long timeout)
#else
long
WriteNMAPLockAcquire(NLockStruct *lockHandle, long timeout, unsigned char *file, unsigned long line)
#endif
{
    long failureReason;
    unsigned long delayLen;

    if (lockHandle) {
        delayLen = timeout >> 3;

        do {
            XplWaitOnLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);

            if (lockHandle->wLock == 0) {
                if (lockHandle->eLock == 0)    {
                    lockHandle->wLock++;
                    lockHandle->lockTime = 0; //time(NULL);

                    XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
                    return(NLOCK_ACQUIRED);
                }

                XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
                failureReason = NLOCK_HAS_PURGE;
            }

            XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
            failureReason = NLOCK_HAS_WRITER;                    

            if (timeout > 0) {
                if (delayLen > 0) {
                    XplDelay(delayLen);
                } else {
                    delayLen = 1;
                    XplDelay(delayLen);
                }

                timeout -= delayLen;
                delayLen <<= 1;
                continue;
            }

            return(failureReason);

        } while (TRUE);
    }

#if defined(DEBUG)
    XplConsolePrintf("NMAP: WriteNMAPLockAcquire() called with an NULL lock handle from %s:%lu\r\n", file, line); 
#endif

    return(NLOCK_INVALID);
}

#if !defined(DEBUG)
long
WriteNMAPLockRelease(NLockStruct *lockHandle)
#else
long
WriteNMAPLockRelease(NLockStruct *lockHandle, unsigned char *file, unsigned long line)
#endif
{
    if (lockHandle) {
        XplWaitOnLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
        #if defined(DEBUG)
        if ((lockHandle->rLock > 0) && (lockHandle->eLock == 0) && (lockHandle->wLock == 1)) {
            ;
        } else {
            XplConsolePrintf("NMAP: WriteNMAPLockRelease() called with unexpected values RLock:%lu WLock: %lu ELock: %lu from %s:%lu\r\n", lockHandle->rLock, lockHandle->wLock, lockHandle->eLock, file, line); 
        }
        #endif

        lockHandle->wLock = 0;
        XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
        return(NLOCK_RELEASED);
    }

#if defined(DEBUG)
    XplConsolePrintf("NMAP: WriteNMAPLockRelease() with a NULL lock handle from %s:%lu\r\n", file, line); 
#endif

    return(NLOCK_INVALID);
}

/*********************** Purge Locks ***********************/
#if !defined(DEBUG)
long
PurgeNMAPLockAcquire(NLockStruct *lockHandle, long timeout)
#else
long
PurgeNMAPLockAcquire(NLockStruct *lockHandle, long timeout, unsigned char *file, unsigned long line)
#endif
{
    unsigned long delayLen;
    long failureReason;

    if (lockHandle) {
        delayLen = timeout >> 3;

        do {
            XplWaitOnLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);

            if (lockHandle->rLock > 1) {
                XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
                failureReason = NLOCK_HAS_READERS;                                                  
            } else if (lockHandle->wLock == 0) {
                if (lockHandle->eLock == 0) {
                    lockHandle->eLock++;
                    lockHandle->lockTime = 0; //time(NULL);
                    XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
                    return(NLOCK_ACQUIRED);
                } else {
                    XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
                    failureReason = NLOCK_HAS_PURGE;                                                      
                }
            } else {
                XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
                failureReason = NLOCK_HAS_WRITER;                    
            }

            if (timeout > 0) {
                if (delayLen > 0) {
                    XplDelay(delayLen);
                } else {
                    delayLen = 1;
                    XplDelay(delayLen);
                }

                timeout -= delayLen;
                delayLen <<= 1;
                continue;
            }

            return(failureReason);

        } while (TRUE);
    }

#if defined(DEBUG)
    XplConsolePrintf("NMAP: PurgeNMAPLockAcquire() called with an NULL lock handle from %s:%lu\r\n", file, line); 
#endif

    return(NLOCK_INVALID);
}

#if !defined(DEBUG)
long
PurgeNMAPLockRelease(NLockStruct *lockHandle)
#else
long
PurgeNMAPLockRelease(NLockStruct *lockHandle, unsigned char *file, unsigned long line)
#endif
{
    if (lockHandle) {

        XplWaitOnLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
#if defined(DEBUG)
        if ((lockHandle->eLock == 1) && (lockHandle->wLock == 0) && (lockHandle->rLock == 1)) {
            ;
        } else {
            XplConsolePrintf("NMAP: PurgeNMAPLockAcquire() called with unexpected values RLock:%lu WLock: %lu ELock: %lu from %s:%lu\r\n", lockHandle->rLock, lockHandle->wLock, lockHandle->eLock, file, line); 
        }
#endif

        lockHandle->eLock = 0;
        XplSignalLocalSemaphore(NLockHeadArray[lockHandle->slot].lock);
        return(NLOCK_RELEASED);
    }

#if defined(DEBUG)
    XplConsolePrintf("NMAP: PurgeNMAPLockAcquire() called with a NULL lock handle from %s:%lu\r\n", file, line); 
#endif

    return(NLOCK_INVALID);
}



int 
NLockCountWatchers(NLockStruct *lockHandle)
{
    int i;
    int n;

    for (i = 0, n = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (lockHandle->watchers[i]) {
            n++;
        }
    }
    return n;
}
