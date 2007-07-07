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

#define PRODUCT_NAME "Bongo Memory Manager"
#define PRODUCT_VERSION "$Revision: 1.3 $"

#include <config.h>
#include <xpl.h>
#include <ctype.h>
#include <time.h>
#include <string.h>

#if !defined(LIBC)

#if defined(TARGET_CPU_68K) || defined(__CFM68K__) || defined(m68k) || defined(_M_M68K)
#define ALIGNMENT 2
#elif defined(__ppc__) || defined(__POWERPC__) || defined(_POWER)
#define ALIGNMENT 2
#elif defined(__amd64__)
#define ALIGNMENT 4
#elif defined(i386) || defined(__i386__)
#define ALIGNMENT 4
#elif defined(__alpha) || defined(__alpha__)
#define ALIGNMENT 8
#elif defined(__hppa__)
#define ALIGNMENT 4
#elif defined(sparc) || defined(__sparc)
#define ALIGNMENT 4
#elif defined(__s390__)
#define ALIGNMENT 4
#elif defined(__mips)
#ifdef __sgi
#include <sgidefs.h>
#endif
#ifdef _MIPS_SZPTR
#define ALIGNMENT (_MIPS_SZPTR/8)
#else
#define ALIGNMENT 4
#endif
#else
/* mostly safe default */
#define ALIGNMENT 4
#endif

#define ALIGN_SIZE(s, a) (((s) + a - 1) & ~(a - 1))

#define PLATFORM_ALLOC_POOL(pool, s) malloc((s))
#define PLATFORM_ALLOC_PRIVATE(pool, s) malloc((s))
#define PLATFORM_ALLOC_EXTREME(s) malloc((s))
#define PLATFORM_ALLOC(s) malloc((s))
#define PLATFORM_ALLOC_FREE(p) free((p))

#else

#include <library.h>

#define ALIGN_SIZE(s, a) (s)

#define PLATFORM_ALLOC_POOL(pool, s) AllocSleepOK((s), (pool).resourceTag, NULL)
#define PLATFORM_ALLOC_PRIVATE(pool, s) AllocSleepOK((s), (pool)->resourceTag, NULL)
#define PLATFORM_ALLOC_EXTREME(s) AllocSleepOK((s), MemManager.control.poolExtreme.resourceTag, NULL)
#define PLATFORM_ALLOC(s) AllocSleepOK((s), MemManager.nlm.resourceTag, NULL)
#define PLATFORM_ALLOC_FREE(p) free((p))

#endif

#include "memmgrp.h"

typedef enum _MemPoolStates {
    LIBRARY_LOADED = 0, 
    LIBRARY_INITIALIZING, 
    LIBRARY_RUNNING, 
    LIBRARY_SHUTTING_DOWN, 
    LIBRARY_SHUTDOWN, 

    LIBRARY_MAX_STATES
} MemManagerStates;

struct {
    MemManagerStates state;

    unsigned long flags;

    struct {
        XplThreadID group;
        XplThreadID main;
    } id;

    time_t startTime;

    struct {
        XplSemaphore pools;
        XplSemaphore shutdown;
    } sem;

    XplAtomic useCount;

    struct {
        MemPoolControl pool128;
        MemPoolControl pool256;
        MemPoolControl pool512;
        MemPoolControl pool1KB;
        MemPoolControl pool2KB;
        MemPoolControl pool4KB;
        MemPoolControl pool8KB;
        MemPoolControl pool16KB;
        MemPoolControl pool32KB;
        MemPoolControl pool64KB;
        MemPoolControl pool128KB;
        MemPoolControl pool256KB;
        MemPoolControl pool512KB;
        MemPoolControl pool1MB;
        MemPoolControl pool2MB;
        MemPoolControl pool4MB;
        MemPoolControl poolExtreme;

        MemPoolControl *poolPrivate;
    } control;

    unsigned char *agentName;

#if defined(LIBC)
    struct {
        void *handle;
        rtag_t resourceTag;
    } nlm;
#endif
} MemManager;

#define PUSH_NON_EXTREME_FREE_ENTRY(pool, entry)                              \
        {                                                                     \
            (entry)->flags &= ~PENTRY_FLAG_INUSE;                             \
            XplWaitOnLocalSemaphore((pool)->sem);                             \
            if ((entry)->next != NULL) {                                      \
                (entry)->next->previous = (entry)->previous;                  \
            }                                                                 \
            if ((entry)->previous != NULL) {                                  \
                (entry)->previous->next = (entry)->next;                      \
            } else {                                                          \
                (pool)->inUse = (entry)->next;                                \
            }                                                                 \
            (entry)->previous = NULL;                                         \
            if (((entry)->next = (pool)->free) != NULL) {                     \
                (entry)->next->previous = (entry);                            \
            }                                                                 \
            (pool)->free = (entry);                                           \
            XplSignalLocalSemaphore((pool)->sem);                             \
        }

#define PUSH_EXTREME_FREE_ENTRY(entry, eNext)                                 \
        {                                                                     \
            (entry)->flags &= ~PENTRY_FLAG_INUSE;                             \
            XplWaitOnLocalSemaphore(MemManager.control.poolExtreme.sem);      \
            if ((entry)->next != NULL) {                                      \
                (entry)->next->previous = (entry)->previous;                  \
            }                                                                 \
            if ((entry)->previous != NULL) {                                  \
                (entry)->previous->next = (entry)->next;                      \
            } else {                                                          \
                MemManager.control.poolExtreme.inUse = (entry)->next;         \
            }                                                                 \
            if (((eNext) = MemManager.control.poolExtreme.free) != NULL) {    \
                do {                                                          \
                    if ((entry)->allocSize < (eNext)->allocSize) {            \
                        if (((entry)->previous = (eNext)->previous) != NULL) {\
                            (entry)->previous->next = (entry);                \
                        } else {                                              \
                            MemManager.control.poolExtreme.free = (entry);    \
                        }                                                     \
                        (entry)->next = (eNext);                              \
                        (eNext)->previous = (entry);                          \
                    } else if ((eNext)->next != NULL) {                       \
                        (eNext) = (eNext)->next;                              \
                        continue;                                             \
                    } else {                                                  \
                        (eNext)->next = (entry);                              \
                        (entry)->previous = (eNext);                          \
                        (entry)->next = NULL;                                 \
                    }                                                         \
                    break;                                                    \
                } while (TRUE);                                               \
            } else {                                                          \
                (entry)->previous = NULL;                                     \
                (entry)->next = NULL;                                         \
                MemManager.control.poolExtreme.free = (entry);                \
            }                                                                 \
            XplSignalLocalSemaphore(MemManager.control.poolExtreme.sem);      \
        }

#define PULL_INTERNAL_FREE_ENTRY(pool, entry, count)                          \
        {                                                                     \
            XplWaitOnLocalSemaphore((pool).sem);                              \
            (pool).stats.pitches++;                                           \
            if ((pool).free != NULL) {                                        \
                (entry) = (pool).free;                                        \
                if (((pool).free = (entry)->next) != NULL) {                  \
                    (pool).free->previous = NULL;                             \
                }                                                             \
                (entry)->previous = NULL;                                     \
                if (((entry)->next = (pool).inUse) != NULL) {                 \
                    (entry)->next->previous = (entry);                        \
                }                                                             \
                (pool).inUse = (entry);                                       \
                XplSignalLocalSemaphore((pool).sem);                          \
                (entry)->size = (count);                                      \
                (entry)->flags |= PENTRY_FLAG_INUSE;                          \
            } else {                                                          \
                (pool).stats.strikes++;                                       \
                XplSignalLocalSemaphore((pool).sem);                          \
                {   register XplThreadID threadID;                            \
                    threadID = XplSetThreadGroupID(MemManager.id.group);      \
                    (entry) = (MemPoolEntry *)PLATFORM_ALLOC_POOL((pool),     \
                                    (pool).allocSize + ALIGN_SIZE(sizeof(MemPoolEntry), ALIGNMENT)); \
                    XplSetThreadGroupID(threadID);                            \
                }                                                             \
                if ((entry) != NULL) {                                        \
                    (entry)->control = &(pool);                               \
                    (entry)->flags = (pool).defaultFlags;                     \
                    (entry)->allocSize = (pool).allocSize;                    \
                    (entry)->suffix[0] = 0;                                   \
                    (entry)->suffix[1] = 0;                                   \
                    (entry)->suffix[2] = 0;                                   \
                    (entry)->suffix[3] = 0;                                   \
                    (entry)->previous = NULL;                                 \
                    (entry)->size = (count);                                  \
                    (entry)->flags |= PENTRY_FLAG_INUSE;                      \
                    XplWaitOnLocalSemaphore((pool).sem);                      \
                    (pool).eCount.allocated++;                                \
                    if ((pool).eCount.allocated < (pool).eCount.maximum) {    \
                        ;                                                     \
                    } else {                                                  \
                        (entry)->flags |= PENTRY_FLAG_NON_POOLABLE;           \
                    }                                                         \
                    (entry)->previous = NULL;                                 \
                    if (((entry)->next = (pool).inUse) != NULL) {             \
                        (entry)->next->previous = (entry);                    \
                    }                                                         \
                    (pool).inUse = (entry);                                   \
                    XplSignalLocalSemaphore((pool).sem);                      \
                }                                                             \
            }                                                                 \
        }

#define PULL_PRIVATE_FREE_ENTRY(pool, entry)                                  \
        {                                                                     \
            XplWaitOnLocalSemaphore((pool)->sem);                             \
            (pool)->stats.pitches++;                                          \
            if ((pool)->free != NULL) {                                       \
                (entry) = (pool)->free;                                       \
                if (((pool)->free = (entry)->next) != NULL) {                 \
                    (pool)->free->previous = NULL;                            \
                }                                                             \
                (entry)->previous = NULL;                                     \
                if (((entry)->next = (pool)->inUse) != NULL) {                \
                    (entry)->next->previous = (entry);                        \
                }                                                             \
                (pool)->inUse = (entry);                                      \
                XplSignalLocalSemaphore((pool)->sem);                         \
                (entry)->flags |= PENTRY_FLAG_INUSE;                          \
            } else {                                                          \
                (pool)->stats.strikes++;                                      \
                XplSignalLocalSemaphore((pool)->sem);                         \
                {   register XplThreadID threadID;                            \
                    threadID = XplSetThreadGroupID(MemManager.id.group);      \
                    (entry) = (MemPoolEntry *)PLATFORM_ALLOC_PRIVATE((pool),  \
                                    (pool)->allocSize + ALIGN_SIZE(sizeof(MemPoolEntry), ALIGNMENT));\
                    XplSetThreadGroupID(threadID);                            \
                }                                                             \
                if ((entry) != NULL) {                                        \
                    (entry)->control = (pool);                                \
                    (entry)->flags = (pool)->defaultFlags;                    \
                    (entry)->allocSize = (pool)->allocSize;                   \
                    (entry)->suffix[0] = 0;                                   \
                    (entry)->suffix[1] = 0;                                   \
                    (entry)->suffix[2] = 0;                                   \
                    (entry)->suffix[3] = 0;                                   \
                    (entry)->previous = NULL;                                 \
                    if ((pool)->allocCB != NULL) {                            \
                        if (((pool)->allocCB)((entry)->suffix,                \
                                                (pool)->clientData)) {        \
                            ;                                                 \
                        } else {                                              \
                            PLATFORM_ALLOC_FREE((entry));                     \
                            (entry) = NULL;                                   \
                        }                                                     \
                    }                                                         \
                }                                                             \
                if ((entry) != NULL) {                                        \
                    XplWaitOnLocalSemaphore((pool)->sem);                     \
                    (pool)->eCount.allocated++;                               \
                    if ((pool)->eCount.allocated < (pool)->eCount.maximum) {  \
                        ;                                                     \
                    } else {                                                  \
                        (entry)->flags |= PENTRY_FLAG_NON_POOLABLE;           \
                    }                                                         \
                    (entry)->previous = NULL;                                 \
                    if (((entry)->next = (pool)->inUse) != NULL) {            \
                        (entry)->next->previous = (entry);                    \
                    }                                                         \
                    (pool)->inUse = (entry);                                  \
                    XplSignalLocalSemaphore((pool)->sem);                     \
                    (entry)->size = (pool)->allocSize;                        \
                    (entry)->flags |= PENTRY_FLAG_INUSE;                      \
                }                                                             \
            }                                                                 \
        }

#define PULL_EXTREME_FREE_ENTRY(entry, count)                                 \
        {                                                                     \
            XplWaitOnLocalSemaphore(MemManager.control.poolExtreme.sem);      \
            MemManager.control.poolExtreme.stats.pitches++;                   \
            (entry) = MemManager.control.poolExtreme.free;                    \
            while ((entry) != NULL) {                                         \
                if ((count) <= (entry)->allocSize) {                          \
                    break;                                                    \
                }                                                             \
                (entry) = (entry)->next;                                      \
            }                                                                 \
            if ((entry) != NULL) {                                            \
                if ((entry)->next != NULL) {                                  \
                    (entry)->next->previous = (entry)->previous;              \
                }                                                             \
                if ((entry)->previous != NULL) {                              \
                    (entry)->previous->next = (entry)->next;                  \
                } else {                                                      \
                    MemManager.control.poolExtreme.free = (entry)->next;      \
                }                                                             \
                (entry)->previous = NULL;                                     \
                if (((entry)->next = MemManager.control.poolExtreme.inUse) != NULL) {   \
                    (entry)->next->previous = (entry);                        \
                }                                                             \
                MemManager.control.poolExtreme.inUse = (entry);               \
                XplSignalLocalSemaphore(MemManager.control.poolExtreme.sem);  \
                (entry)->size = (count);                                      \
                (entry)->flags |= PENTRY_FLAG_INUSE;                          \
            } else {                                                          \
                MemManager.control.poolExtreme.stats.strikes++;               \
                XplSignalLocalSemaphore(MemManager.control.poolExtreme.sem);  \
                {   register XplThreadID threadID;                            \
                    threadID = XplSetThreadGroupID(MemManager.id.group);      \
                    (entry) = (MemPoolEntry *)PLATFORM_ALLOC_EXTREME((count) + ALIGN_SIZE(sizeof(MemPoolEntry), ALIGNMENT));   \
                    XplSetThreadGroupID(threadID);                            \
                }                                                             \
                if ((entry) != NULL) {                                        \
                    (entry)->control = &MemManager.control.poolExtreme;       \
                    (entry)->flags = MemManager.control.poolExtreme.defaultFlags;   \
                    (entry)->allocSize = ALIGN_SIZE(count, ALIGNMENT);                             \
                    (entry)->suffix[0] = 0;                                   \
                    (entry)->suffix[1] = 0;                                   \
                    (entry)->suffix[2] = 0;                                   \
                    (entry)->suffix[3] = 0;                                   \
                    (entry)->previous = NULL;                                 \
                    XplWaitOnLocalSemaphore(MemManager.control.poolExtreme.sem);    \
                    MemManager.control.poolExtreme.eCount.allocated++;        \
                    if (MemManager.control.poolExtreme.eCount.allocated < MemManager.control.poolExtreme.eCount.maximum) {  \
                        ;                                                     \
                    } else {                                                  \
                        (entry)->flags |= PENTRY_FLAG_NON_POOLABLE;           \
                    }                                                         \
                    (entry)->previous = NULL;                                 \
                    if (((entry)->next = MemManager.control.poolExtreme.inUse) != NULL) {   \
                        (entry)->next->previous = (entry);                    \
                    }                                                         \
                    MemManager.control.poolExtreme.inUse = (entry);           \
                    XplSignalLocalSemaphore(MemManager.control.poolExtreme.sem);    \
                    (entry)->size = (count);                                  \
                    (entry)->flags |= PENTRY_FLAG_INUSE;                      \
                }                                                             \
            }                                                                 \
        }

EXPORT void *
MemCallocDirect(size_t Number, size_t Size)
{
#if _BONGO_SYSTEM_MALLOC
    if (Size == 0) {
        return calloc(1, 1);
    } else {
        return calloc(Number, Size);
    }
#else
    register MemPoolEntry *pEntry;
    register unsigned int size = Number * Size;

    if (size < 128) {
        {
            XplWaitOnLocalSemaphore((MemManager.control.pool128).sem);
            (MemManager.control.pool128).stats.pitches++;
            if ((MemManager.control.pool128).free != NULL) {
                (pEntry) = (MemManager.control.pool128).free;
                if (((MemManager.control.pool128).free = (pEntry)->next) != NULL) {
                    (MemManager.control.pool128).free->previous = NULL;
                }
                (pEntry)->previous = NULL;
                if (((pEntry)->next = (MemManager.control.pool128).inUse) != NULL) {
                    (pEntry)->next->previous = (pEntry);
                }
                (MemManager.control.pool128).inUse = (pEntry);
                XplSignalLocalSemaphore((MemManager.control.pool128).sem);
                (pEntry)->size = (size);
                (pEntry)->flags |= PENTRY_FLAG_INUSE;
            } else {
                (MemManager.control.pool128).stats.strikes++;
                XplSignalLocalSemaphore((MemManager.control.pool128).sem);
                {   register XplThreadID threadID;
                    threadID = XplSetThreadGroupID(MemManager.id.group);
                    (pEntry) = (MemPoolEntry *)PLATFORM_ALLOC_POOL((MemManager.control.pool128),
                                    (MemManager.control.pool128).allocSize + ALIGN_SIZE(sizeof(MemPoolEntry), ALIGNMENT));
                    XplSetThreadGroupID(threadID);
                }
                if ((pEntry) != NULL) {
                    (pEntry)->control = &(MemManager.control.pool128);
                    (pEntry)->flags = (MemManager.control.pool128).defaultFlags;
                    (pEntry)->allocSize = (MemManager.control.pool128).allocSize;
                    (pEntry)->suffix[0] = 0;
                    (pEntry)->suffix[1] = 0;
                    (pEntry)->suffix[2] = 0;
                    (pEntry)->suffix[3] = 0;
                    (pEntry)->previous = NULL;
                    (pEntry)->size = (size);
                    (pEntry)->flags |= PENTRY_FLAG_INUSE;
                    XplWaitOnLocalSemaphore((MemManager.control.pool128).sem);
                    (MemManager.control.pool128).eCount.allocated++;
                    if ((MemManager.control.pool128).eCount.allocated < (MemManager.control.pool128).eCount.maximum) {
                        ;
                    } else {
                        (pEntry)->flags |= PENTRY_FLAG_NON_POOLABLE;
                    }
                    (pEntry)->previous = NULL;
                    if (((pEntry)->next = (MemManager.control.pool128).inUse) != NULL) {
                        (pEntry)->next->previous = (pEntry);
                    }
                    (MemManager.control.pool128).inUse = (pEntry);
                    XplSignalLocalSemaphore((MemManager.control.pool128).sem);
                }
            }
        }
    } else if (size < 256) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntry, size);
    } else if (size < 512) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntry, size);
    } else if (size < 1024) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntry, size);
    } else if (size < (2 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntry, size);
    } else if (size < (4 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntry, size);
    } else if (size < (8 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntry, size);
    } else if (size < (16 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntry, size);
    } else if (size < (32 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntry, size);
    } else if (size < (64 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntry, size);
    } else if (size < (128 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntry, size);
    } else if (size < (256 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntry, size);
    } else if (size < (512 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntry, size);
    } else if (size < (1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntry, size);
    } else if (size < (2 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntry, size);
    } else if (size < (4 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntry, size);
    } else {
        PULL_EXTREME_FREE_ENTRY(pEntry, size);
    }

    if (pEntry != NULL) {
        memset(pEntry->suffix, 0, size);
        return(pEntry->suffix);
    }

    return(NULL);
#endif
}

EXPORT void *
MemCallocDebugDirect(size_t Number, size_t Size, const char *SourceFile, unsigned long SourceLine)
{
#if _BONGO_SYSTEM_MALLOC
    if (Size == 0) {
        return calloc(1, 1);
    } else {
        return calloc(Number, Size);
    }
#else
    register MemPoolEntry *pEntry;
    register unsigned int size = Number * Size;

    if (size < 128) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntry, size);
    } else if (size < 256) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntry, size);
    } else if (size < 512) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntry, size);
    } else if (size < 1024) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntry, size);
    } else if (size < (2 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntry, size);
    } else if (size < (4 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntry, size);
    } else if (size < (8 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntry, size);
    } else if (size < (16 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntry, size);
    } else if (size < (32 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntry, size);
    } else if (size < (64 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntry, size);
    } else if (size < (128 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntry, size);
    } else if (size < (256 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntry, size);
    } else if (size < (512 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntry, size);
    } else if (size < (1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntry, size);
    } else if (size < (2 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntry, size);
    } else if (size < (4 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntry, size);
    } else {
        PULL_EXTREME_FREE_ENTRY(pEntry, size);
    }

    if (pEntry != NULL) {
        if (SourceFile != NULL) {
            strncpy(pEntry->source.file, SourceFile, sizeof(pEntry->source.file));
            pEntry->source.line = SourceLine;
        } else {
            strcpy(pEntry->source.file, "UNKNOWN");
            pEntry->source.line = 0;
        }

        memset(pEntry->suffix, 0, size);
        return(pEntry->suffix);
    }

    return(NULL);
#endif
}

EXPORT void *
MemMallocDirect(size_t Size)
{
#if _BONGO_SYSTEM_MALLOC
    if (Size == 0) {
        return malloc(1);
    } else {
        return malloc(Size);
    }
#else
    register MemPoolEntry *pEntry;

    if (Size < 128) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntry, Size);
    } else if (Size < 256) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntry, Size);
    } else if (Size < 512) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntry, Size);
    } else if (Size < 1024) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntry, Size);
    } else if (Size < (2 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntry, Size);
    } else if (Size < (4 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntry, Size);
    } else if (Size < (8 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntry, Size);
    } else if (Size < (16 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntry, Size);
    } else if (Size < (32 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntry, Size);
    } else if (Size < (64 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntry, Size);
    } else if (Size < (128 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntry, Size);
    } else if (Size < (256 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntry, Size);
    } else if (Size < (512 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntry, Size);
    } else if (Size < (1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntry, Size);
    } else if (Size < (2 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntry, Size);
    } else if (Size < (4 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntry, Size);
    } else {
        PULL_EXTREME_FREE_ENTRY(pEntry, Size);
    }

    if (pEntry != NULL) {
        return(pEntry->suffix);
    }

    return(NULL);
#endif
}

EXPORT void *
MemMallocDebugDirect(size_t Size, const char *SourceFile, unsigned long SourceLine)
{
#if _BONGO_SYSTEM_MALLOC
    if (Size == 0) {
        return malloc(1);
    } else {
        return malloc(Size);
    }
#else
    register MemPoolEntry *pEntry;

    if (Size < 128) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntry, Size);
    } else if (Size < 256) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntry, Size);
    } else if (Size < 512) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntry, Size);
    } else if (Size < 1024) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntry, Size);
    } else if (Size < (2 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntry, Size);
    } else if (Size < (4 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntry, Size);
    } else if (Size < (8 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntry, Size);
    } else if (Size < (16 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntry, Size);
    } else if (Size < (32 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntry, Size);
    } else if (Size < (64 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntry, Size);
    } else if (Size < (128 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntry, Size);
    } else if (Size < (256 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntry, Size);
    } else if (Size < (512 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntry, Size);
    } else if (Size < (1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntry, Size);
    } else if (Size < (2 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntry, Size);
    } else if (Size < (4 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntry, Size);
    } else {
        PULL_EXTREME_FREE_ENTRY(pEntry, Size);
    }

    if (pEntry != NULL) {
        if (SourceFile != NULL) {
            strncpy(pEntry->source.file, SourceFile, sizeof(pEntry->source.file));
            pEntry->source.line = SourceLine;
        } else {
            strcpy(pEntry->source.file, "UNKNOWN");
            pEntry->source.line = 0;
        }

        return(pEntry->suffix);
    }

    return(NULL);
#endif
}

EXPORT void *
MemMalloc0Direct(size_t size)
{
#if _BONGO_SYSTEM_MALLOC
    if (size == 0) {
        return calloc(1, 1);
    } else {
        return calloc(size, 1);
    }
#else
    void *ret = MemMallocDirect(size);
    if (ret) {
        memset(ret, 0, size);
    }
    return ret;
#endif
}


EXPORT void *
MemMalloc0DebugDirect(size_t size, const char *sourceFile, unsigned long line)
{
#if _BONGO_SYSTEM_MALLOC
    if (size == 0) {
        return calloc(1, 1);
    } else {
        return calloc(size, 1);
    }
#else
    void *ret = MemMallocDebugDirect(size, sourceFile, line);
    if (ret) {
        memset(ret, 0, size);
    }
    return ret;
#endif
}


EXPORT void *
MemReallocDirect(void *Source, size_t Size)
{
#if _BONGO_SYSTEM_MALLOC
    if (Size == 0) {
        return realloc(Source, 1);
    } else {
        return realloc(Source, Size);
    }
#else 
    register MemPoolEntry *pEntryOld;
    register MemPoolEntry *pEntryNew;

    if (Source != NULL) {
        pEntryOld = (MemPoolEntry *)((unsigned char *)Source - SUFFIX_OFFSET);

        if (!(pEntryOld->flags & PENTRY_FLAG_PRIVATE_ENTRY)) {
            if (Size < pEntryOld->allocSize) {
                pEntryOld->size = Size;
                return(pEntryOld->suffix);
            }
        } else {
            /* Private pool entries are fixed sizes and therefore cannot be
               reallocated to a different size. */
            return(NULL);
        }
    } else {
        pEntryOld = NULL;
    }

    if (Size < 128) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntryNew, Size);
    } else if (Size < 256) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntryNew, Size);
    } else if (Size < 512) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntryNew, Size);
    } else if (Size < 1024) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntryNew, Size);
    } else if (Size < (2 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntryNew, Size);
    } else if (Size < (4 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntryNew, Size);
    } else if (Size < (8 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntryNew, Size);
    } else if (Size < (16 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntryNew, Size);
    } else if (Size < (32 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntryNew, Size);
    } else if (Size < (64 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntryNew, Size);
    } else if (Size < (128 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntryNew, Size);
    } else if (Size < (256 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntryNew, Size);
    } else if (Size < (512 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntryNew, Size);
    } else if (Size < (1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntryNew, Size);
    } else if (Size < (2 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntryNew, Size);
    } else if (Size < (4 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntryNew, Size);
    } else {
        PULL_EXTREME_FREE_ENTRY(pEntryNew, Size);
    }

    if (pEntryNew != NULL) {
        if (pEntryOld != NULL) {
            memcpy(pEntryNew->suffix, pEntryOld->suffix, pEntryOld->size);

            if (!(pEntryOld->flags & (PENTRY_FLAG_NON_POOLABLE | PENTRY_FLAG_EXTREME_ENTRY))) {
                PUSH_NON_EXTREME_FREE_ENTRY(pEntryOld->control, pEntryOld);
            } else {
                XplWaitOnLocalSemaphore(pEntryOld->control->sem);

                if (pEntryOld->next != NULL) {
                    pEntryOld->next->previous = pEntryOld->previous;
                }

                if (pEntryOld->previous != NULL) {
                    pEntryOld->previous->next = pEntryOld->next;
                } else {
                    pEntryOld->control->inUse = pEntryOld->next;
                }

                pEntryOld->control->eCount.allocated--;

                XplSignalLocalSemaphore(pEntryOld->control->sem);

                {   register XplThreadID threadID = XplSetThreadGroupID(MemManager.id.group);
                    PLATFORM_ALLOC_FREE(pEntryOld);
                    XplSetThreadGroupID(threadID);
                }
            }
        }

        return(pEntryNew->suffix);
    }

    return(NULL);
#endif
}

EXPORT void *
MemReallocDebugDirect(void *Source, size_t Size, const char *SourceFile, unsigned long SourceLine)
{
#if _BONGO_SYSTEM_MALLOC
    if (Size == 0) {
        return realloc(Source, 1);
    } else {
        return realloc(Source, Size);
    }
#else
    register MemPoolEntry *pEntryOld;
    register MemPoolEntry *pEntryNew;

    if (Source != NULL) {
        pEntryOld = (MemPoolEntry *)((unsigned char *)Source - SUFFIX_OFFSET);

        if (!(pEntryOld->flags & PENTRY_FLAG_PRIVATE_ENTRY)) {
            if (SourceFile != NULL) {
                strncpy(pEntryOld->source.file, SourceFile, sizeof(pEntryOld->source.file));
                pEntryOld->source.line = SourceLine;
            } else {
                strcpy(pEntryOld->source.file, "UNKNOWN");
                pEntryOld->source.line = 0;
            }

            if (Size < pEntryOld->allocSize) {
                pEntryOld->size = Size;
                return(pEntryOld->suffix);
            }
        } else {
            /* Private pool entries are fixed sizes and therefore cannot be
               reallocated to a different size. */
            return(NULL);
        }
    } else {
        pEntryOld = NULL;
    }

    if (Size < 128) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntryNew, Size);
    } else if (Size < 256) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntryNew, Size);
    } else if (Size < 512) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntryNew, Size);
    } else if (Size < 1024) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntryNew, Size);
    } else if (Size < (2 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntryNew, Size);
    } else if (Size < (4 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntryNew, Size);
    } else if (Size < (8 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntryNew, Size);
    } else if (Size < (16 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntryNew, Size);
    } else if (Size < (32 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntryNew, Size);
    } else if (Size < (64 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntryNew, Size);
    } else if (Size < (128 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntryNew, Size);
    } else if (Size < (256 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntryNew, Size);
    } else if (Size < (512 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntryNew, Size);
    } else if (Size < (1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntryNew, Size);
    } else if (Size < (2 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntryNew, Size);
    } else if (Size < (4 * 1024 * 1024)) {
        PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntryNew, Size);
    } else {
        PULL_EXTREME_FREE_ENTRY(pEntryNew, Size);
    }

    if (pEntryNew != NULL) {
        if (pEntryOld != NULL) {
            memcpy(pEntryNew->suffix, pEntryOld->suffix, pEntryOld->size);

            if (!(pEntryOld->flags & (PENTRY_FLAG_NON_POOLABLE | PENTRY_FLAG_EXTREME_ENTRY))) {
                if (SourceFile != NULL) {
                    strncpy(pEntryOld->source.file, SourceFile, sizeof(pEntryOld->source.file));
                    pEntryOld->source.line = SourceLine;
                } else {
                    strcpy(pEntryOld->source.file, "UNKNOWN");
                    pEntryOld->source.line = 0;
                }

                PUSH_NON_EXTREME_FREE_ENTRY(pEntryOld->control, pEntryOld);
            } else {
                XplWaitOnLocalSemaphore(pEntryOld->control->sem);

                if (pEntryOld->next != NULL) {
                    pEntryOld->next->previous = pEntryOld->previous;
                }

                if (pEntryOld->previous != NULL) {
                    pEntryOld->previous->next = pEntryOld->next;
                } else {
                    pEntryOld->control->inUse = pEntryOld->next;
                }

                pEntryOld->control->eCount.allocated--;

                XplSignalLocalSemaphore(pEntryOld->control->sem);

                {   register XplThreadID threadID = XplSetThreadGroupID(MemManager.id.group);
                    PLATFORM_ALLOC_FREE(pEntryOld);
                    XplSetThreadGroupID(threadID);
                }
            }
        }

        if (SourceFile != NULL) {
            strncpy(pEntryNew->source.file, SourceFile, sizeof(pEntryNew->source.file));
            pEntryNew->source.line = SourceLine;
        } else {
            strcpy(pEntryNew->source.file, "UNKNOWN");
            pEntryNew->source.line = 0;
        }

        return(pEntryNew->suffix);
    }

    return(NULL);
#endif
}

EXPORT void 
MemFreeDirect(void *Source)
{
#if _BONGO_SYSTEM_MALLOC
    free(Source);
#else
    register MemPoolEntry *pEntry;

    if (Source != NULL) {
        pEntry = (MemPoolEntry *)((unsigned char *)Source - SUFFIX_OFFSET);

        if (pEntry->flags & PENTRY_FLAG_INUSE) {
            if (!(pEntry->flags & (PENTRY_FLAG_NON_POOLABLE | PENTRY_FLAG_EXTREME_ENTRY))) {
                PUSH_NON_EXTREME_FREE_ENTRY(pEntry->control, pEntry);
            } else {
                XplWaitOnLocalSemaphore(pEntry->control->sem);

                if (pEntry->next != NULL) {
                    pEntry->next->previous = pEntry->previous;
                }

                if (pEntry->previous != NULL) {
                    pEntry->previous->next = pEntry->next;
                } else {
                    pEntry->control->inUse = pEntry->next;
                }

                pEntry->control->eCount.allocated--;

                XplSignalLocalSemaphore(pEntry->control->sem);

                {   register XplThreadID threadID = XplSetThreadGroupID(MemManager.id.group);
                    PLATFORM_ALLOC_FREE(pEntry);
                    XplSetThreadGroupID(threadID);
                }
            }
        }
    }

    return;
#endif
}

EXPORT void 
MemFreeDebugDirect(void *Source, const char *SourceFile, unsigned long SourceLine)
{
#if _BONGO_SYSTEM_MALLOC 
    free(Source);
#else
    register MemPoolEntry *pEntry;

    if (Source != NULL) {
        pEntry = (MemPoolEntry *)((unsigned char *)Source - SUFFIX_OFFSET);

        if (pEntry->flags & PENTRY_FLAG_INUSE) {
            if (!(pEntry->flags & (PENTRY_FLAG_NON_POOLABLE | PENTRY_FLAG_EXTREME_ENTRY))) {
                if (SourceFile != NULL) {
                    strncpy(pEntry->source.file, SourceFile, sizeof(pEntry->source.file));
                    pEntry->source.line = SourceLine;
                } else {
                    strcpy(pEntry->source.file, "UNKNOWN");
                    pEntry->source.line = 0;
                }

                PUSH_NON_EXTREME_FREE_ENTRY(pEntry->control, pEntry);
            } else {
                XplWaitOnLocalSemaphore(pEntry->control->sem);

                if (pEntry->next != NULL) {
                    pEntry->next->previous = pEntry->previous;
                }

                if (pEntry->previous != NULL) {
                    pEntry->previous->next = pEntry->next;
                } else {
                    pEntry->control->inUse = pEntry->next;
                }

                pEntry->control->eCount.allocated--;

                XplSignalLocalSemaphore(pEntry->control->sem);

                {   register XplThreadID threadID = XplSetThreadGroupID(MemManager.id.group);
                    PLATFORM_ALLOC_FREE(pEntry);
                    XplSetThreadGroupID(threadID);
                }
            }
        } else {
            XplConsolePrintf("MemMGR: free'd allocation free'd again by %s [%lu]; originally free'd by %s [%lu].\r\n", SourceFile, SourceLine, pEntry->source.file, pEntry->source.line);
        }
    }

    return;
#endif
}

EXPORT char *
MemStrCatDirect(const char *stra, const char *strb) 
{
    int len;
    char *ret;

    if (stra == NULL) {
        stra = "";
    }

    len = strlen(stra) + strlen(strb);
    ret = MemMallocDirect(len + 1);
    if (!ret) {
        return NULL;
    }
    
    strcpy(ret, stra);
    strcpy(ret + strlen(stra), strb);

    return ret;
}

EXPORT char *
MemStrCatDebugDirect(const char *stra, const char *strb, const char *sourceFile, unsigned long sourceLine) 
{
    int len;
    char *ret;

    if (stra == NULL) {
        stra = "";
    }

    len = strlen(stra) + strlen(strb);
    ret = MemMallocDebugDirect(len + 1, sourceFile, sourceLine);
    if (!ret) {
        return NULL;
    }
    
    strcpy(ret, stra);
    strcpy(ret + strlen(stra), strb);

    return ret;
}

EXPORT char *
MemStrdupDirect(const char *StrSource)
{
#if _BONGO_SYSTEM_MALLOC
    if (StrSource) {
        return strdup(StrSource);
    } else {
        return NULL;
    }
#else
    register MemPoolEntry *pEntry;
    register unsigned int size;

    if (StrSource != NULL) {
        size = strlen(StrSource) + 1;

        if (size < 128) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntry, size);
        } else if (size < 256) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntry, size);
        } else if (size < 512) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntry, size);
        } else if (size < 1024) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntry, size);
        } else if (size < (2 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntry, size);
        } else if (size < (4 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntry, size);
        } else if (size < (8 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntry, size);
        } else if (size < (16 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntry, size);
        } else if (size < (32 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntry, size);
        } else if (size < (64 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntry, size);
        } else if (size < (128 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntry, size);
        } else if (size < (256 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntry, size);
        } else if (size < (512 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntry, size);
        } else if (size < (1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntry, size);
        } else if (size < (2 * 1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntry, size);
        } else if (size < (4 * 1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntry, size);
        } else {
            PULL_EXTREME_FREE_ENTRY(pEntry, size);
        }

        if (pEntry != NULL) {
            memcpy(pEntry->suffix, StrSource, size);
            return(pEntry->suffix);
        }
    }

    return(NULL);
#endif
}

EXPORT char *
MemStrndupDirect(const char *StrSource, int n)
{
#if _BONGO_SYSTEM_MALLOC
    char *mem = malloc(n);
    if (mem) {
        strncpy(mem, StrSource, n);
        return mem;
    } else {
        return NULL;
    }
#else
    register MemPoolEntry *pEntry;
    register unsigned int size = n + 1;

    if (StrSource != NULL) {
        if (size < 128) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntry, size);
        } else if (size < 256) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntry, size);
        } else if (size < 512) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntry, size);
        } else if (size < 1024) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntry, size);
        } else if (size < (2 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntry, size);
        } else if (size < (4 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntry, size);
        } else if (size < (8 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntry, size);
        } else if (size < (16 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntry, size);
        } else if (size < (32 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntry, size);
        } else if (size < (64 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntry, size);
        } else if (size < (128 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntry, size);
        } else if (size < (256 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntry, size);
        } else if (size < (512 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntry, size);
        } else if (size < (1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntry, size);
        } else if (size < (2 * 1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntry, size);
        } else if (size < (4 * 1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntry, size);
        } else {
            PULL_EXTREME_FREE_ENTRY(pEntry, size);
        }

        if (pEntry != NULL) {
            memcpy(pEntry->suffix, StrSource, n);
            pEntry->suffix[n] = '\0';
            return(pEntry->suffix);
        }
    }

    return(NULL);
#endif
}

EXPORT char *
MemStrdupDebugDirect(const char *StrSource, const char *SourceFile, unsigned long SourceLine)
{
#if _BONGO_SYSTEM_MALLOC
    if (StrSource) {
        return strdup(StrSource);
    } else {
        return NULL;
    }
#else
    register MemPoolEntry *pEntry;
    register unsigned int size;

    if (StrSource != NULL) {
        size = strlen(StrSource) + 1;

        if (size < 128) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntry, size);
        } else if (size < 256) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntry, size);
        } else if (size < 512) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntry, size);
        } else if (size < 1024) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntry, size);
        } else if (size < (2 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntry, size);
        } else if (size < (4 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntry, size);
        } else if (size < (8 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntry, size);
        } else if (size < (16 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntry, size);
        } else if (size < (32 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntry, size);
        } else if (size < (64 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntry, size);
        } else if (size < (128 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntry, size);
        } else if (size < (256 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntry, size);
        } else if (size < (512 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntry, size);
        } else if (size < (1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntry, size);
        } else if (size < (2 * 1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntry, size);
        } else if (size < (4 * 1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntry, size);
        } else {
            PULL_EXTREME_FREE_ENTRY(pEntry, size);
        }

        if (pEntry != NULL) {
            if (SourceFile != NULL) {
                strncpy(pEntry->source.file, SourceFile, sizeof(pEntry->source.file));
                pEntry->source.line = SourceLine;
            } else {
                strcpy(pEntry->source.file, "UNKNOWN");
                pEntry->source.line = 0;
            }

            memcpy(pEntry->suffix, StrSource, size);
            return(pEntry->suffix);
        }
    }

    return(NULL);
#endif
}

EXPORT char *
MemStrndupDebugDirect(const char *StrSource, int n, const char *SourceFile, unsigned long SourceLine)
{
#if _BONGO_SYSTEM_MALLOC
    return MemStrndupDirect(StrSource, n);
#else
    register MemPoolEntry *pEntry;
    register unsigned int size = n + 1;

    if (StrSource != NULL) {
        if (size < 128) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128, pEntry, size);
        } else if (size < 256) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256, pEntry, size);
        } else if (size < 512) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512, pEntry, size);
        } else if (size < 1024) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1KB, pEntry, size);
        } else if (size < (2 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2KB, pEntry, size);
        } else if (size < (4 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4KB, pEntry, size);
        } else if (size < (8 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool8KB, pEntry, size);
        } else if (size < (16 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool16KB, pEntry, size);
        } else if (size < (32 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool32KB, pEntry, size);
        } else if (size < (64 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool64KB, pEntry, size);
        } else if (size < (128 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool128KB, pEntry, size);
        } else if (size < (256 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool256KB, pEntry, size);
        } else if (size < (512 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool512KB, pEntry, size);
        } else if (size < (1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool1MB, pEntry, size);
        } else if (size < (2 * 1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool2MB, pEntry, size);
        } else if (size < (4 * 1024 * 1024)) {
            PULL_INTERNAL_FREE_ENTRY(MemManager.control.pool4MB, pEntry, size);
        } else {
            PULL_EXTREME_FREE_ENTRY(pEntry, size);
        }

        if (pEntry != NULL) {
            if (SourceFile != NULL) {
                strncpy(pEntry->source.file, SourceFile, sizeof(pEntry->source.file));
                pEntry->source.line = SourceLine;
            } else {
                strcpy(pEntry->source.file, "UNKNOWN");
                pEntry->source.line = 0;
            }

            memcpy(pEntry->suffix, StrSource, n);
            pEntry->suffix[n] = '\0';
            return(pEntry->suffix);
        }
    }

    return(NULL);
#endif
}

static BOOL 
GetPoolStatistics(MemPoolControl *Pool, MemStatistics **Head, MemStatistics **Previous)
{
    register MemStatistics *next;
    register BOOL result = TRUE;

    if (Pool && Head) {
        if (Pool->flags & PCONTROL_FLAG_INITIALIZED) {
            next = (MemStatistics *)PLATFORM_ALLOC(sizeof(MemStatistics));
            if (next) {
                if (*Head) {
                    next->next = NULL;
                    next->previous = *Previous;

                    (*Previous)->next = next;
                } else {
                    *Head = next;

                    next->next = next->previous = NULL;
                }

                *Previous = next;

                XplWaitOnLocalSemaphore(Pool->sem);

                next->name = strdup(Pool->name);

                next->totalAlloc.count = Pool->eCount.allocated;
                next->totalAlloc.size = Pool->eCount.allocated * Pool->allocSize;

                next->entry.size = Pool->allocSize;
                next->entry.minimum = Pool->eCount.minimum;
                next->entry.maximum = Pool->eCount.maximum;
                next->entry.allocated = Pool->eCount.allocated;

                next->pitches = Pool->stats.pitches;
                next->strikes = Pool->stats.strikes;
                next->hits = Pool->stats.pitches - Pool->stats.strikes;

                XplSignalLocalSemaphore(Pool->sem);
            } else {
                result = FALSE;
            }
        }
    } else {
        result = FALSE;
    }

    return(result);
}

EXPORT MemStatistics *
MemAllocStatistics(void)
{
    int pool;
    BOOL result = TRUE;
    MemStatistics *head = NULL;
    MemStatistics *next;
    MemStatistics *previous = NULL;
    MemPoolControl *pControl = NULL;

    for (pool = 0; (pool < 17) && result; pool++) {
        switch (pool) {
            case 0: {   result = GetPoolStatistics(&MemManager.control.pool128, &head, &previous);      break;    }
            case 1: {   result = GetPoolStatistics(&MemManager.control.pool256, &head, &previous);      break;    }
            case 2: {   result = GetPoolStatistics(&MemManager.control.pool512, &head, &previous);      break;    }
            case 3: {   result = GetPoolStatistics(&MemManager.control.pool1KB, &head, &previous);      break;    }
            case 4: {   result = GetPoolStatistics(&MemManager.control.pool2KB, &head, &previous);      break;    }
            case 5: {   result = GetPoolStatistics(&MemManager.control.pool4KB, &head, &previous);      break;    }
            case 6: {   result = GetPoolStatistics(&MemManager.control.pool8KB, &head, &previous);      break;    }
            case 7: {   result = GetPoolStatistics(&MemManager.control.pool16KB, &head, &previous);     break;    }
            case 8: {   result = GetPoolStatistics(&MemManager.control.pool32KB, &head, &previous);     break;    }
            case 9: {   result = GetPoolStatistics(&MemManager.control.pool64KB, &head, &previous);     break;    }
            case 10: {  result = GetPoolStatistics(&MemManager.control.pool128KB, &head, &previous);    break;    }
            case 11: {  result = GetPoolStatistics(&MemManager.control.pool256KB, &head, &previous);    break;    }
            case 12: {  result = GetPoolStatistics(&MemManager.control.pool512KB, &head, &previous);    break;    }
            case 13: {  result = GetPoolStatistics(&MemManager.control.pool1MB, &head, &previous);      break;    }
            case 14: {  result = GetPoolStatistics(&MemManager.control.pool2MB, &head, &previous);      break;    }
            case 15: {  result = GetPoolStatistics(&MemManager.control.pool4MB, &head, &previous);      break;    }
            case 16: {  result = GetPoolStatistics(&MemManager.control.poolExtreme, &head, &previous);  break;    }
        }
   }

    XplWaitOnLocalSemaphore(MemManager.sem.pools);

    pControl = MemManager.control.poolPrivate;
    while ((pControl != NULL) && (result)) {
        result = GetPoolStatistics(pControl, &head, &previous);

        pControl = pControl->next;
    }

    XplSignalLocalSemaphore(MemManager.sem.pools);

    if (result) {
        return(head);
    }

    next = head;
    while (next != NULL) {
        previous = next->next;

        PLATFORM_ALLOC_FREE(next->name);
        PLATFORM_ALLOC_FREE(next);

        next = previous;
    }

    return(NULL);
}

EXPORT void 
MemFreeStatistics(MemStatistics *Statistics)
{
    MemStatistics *previous;
    MemStatistics *next;

    next = Statistics;
    while (next != NULL) {
        previous = next->next;

        PLATFORM_ALLOC_FREE(next->name);
        PLATFORM_ALLOC_FREE(next);

        next = previous;
    }

    return;
}

static void 
FillMemoryPool(MemPoolControl *PControl)
{
    int count;
    XplThreadID threadID;
    MemPoolEntry *pEntry;
    MemPoolControl *pool = PControl;

    if (!(pool->flags & PCONTROL_FLAG_INITIALIZED)) {
        threadID = XplSetThreadGroupID(MemManager.id.group);

        pool->flags |= PCONTROL_FLAG_INITIALIZED;

        XplOpenLocalSemaphore(pool->sem, 0);

        pool->eCount.allocated = 0;
        pool->base.start = NULL;
        pool->base.end = NULL;
        pool->free = NULL;
        pool->inUse = NULL;

#if defined(LIBC)
        pool->ResourceTag = AllocateResourceTag(MMNLMHandle, pool->name, AllocSignature);
        if (pool->ResourceTag == NULL) {
            XplConsolePrintf("MemMGR: Unable to allocate a resource tag for \"%s\".\r\n", pool->name);

            pool->flags &= ~PCONTROL_FLAG_INITIALIZED;
            XplCloseLocalSemaphore(pool->sem);
            return;
        }
#endif

        if (pool->eCount.minimum && !(pool->defaultFlags & PENTRY_FLAG_EXTREME_ENTRY)) {
            count = pool->allocSize + ALIGN_SIZE(sizeof(MemPoolEntry), ALIGNMENT);

#if 0
            /*    Align the base for each pool entry on a 32-bit boundary.    */
            if (count % sizeof(unsigned long)) {
                count += sizeof(unsigned long) - (count % sizeof(unsigned long));
            }
#endif

            pool->base.start = (MemPoolEntry *)PLATFORM_ALLOC_PRIVATE(pool, pool->eCount.minimum * count);
            if (pool->base.start != NULL) {
                pool->base.end = (MemPoolEntry *)((unsigned char *)pool->base.start + (pool->eCount.minimum * count));

                pEntry = pool->free = pool->base.start;

                pEntry->previous = NULL;

                do {
                    pEntry->control = pool;

                    pEntry->allocSize = pool->allocSize;
                    pEntry->size = 0;

                    pEntry->flags = pool->defaultFlags | PENTRY_FLAG_INDIRECT_ALLOC;

                    pEntry->suffix[0] = 0;
                    pEntry->suffix[1] = 0;
                    pEntry->suffix[2] = 0;
                    pEntry->suffix[3] = 0;
                    pEntry->source.line = 0;
                    pEntry->source.file[0] = '\0';

                    if (pool->allocCB != NULL) {
                        if ((pool->allocCB)(pEntry->suffix, pool->clientData)) {
                            pool->eCount.allocated += 1;
                        } else {
                            XplConsolePrintf("MemMGR: Allocation call-back failed creating \"%s\".\r\n", pool->name);

                            if (pEntry->previous != NULL) {
                                pEntry->previous->next = NULL;
                            }

                            break;
                        }
                    } else {
                        pool->eCount.allocated += 1;
                    }

                    pEntry->next = (MemPoolEntry *)((unsigned char *)pEntry + count);

                    if (pEntry->next < pool->base.end) {
                        pEntry->next->previous = pEntry;
                        pEntry = pEntry->next;
                        continue;
                    }

                    pEntry->next = NULL;
                    break;
                } while (TRUE);
            }
        }

        XplSetThreadGroupID(threadID);

        XplSignalLocalSemaphore(pool->sem);
    }

    return;
}

/*  The MemManager.sem.pools must be held before calling!
    The thread group ID must be set to MemManager.id.group before calling!

    If the library hasn't been shutdown properly, we can run into trouble 
    by not calling the pool owner's freeCB routine. */
static void 
EmptyMemoryPool(MemPoolControl *PControl)
{
    MemPoolEntry *pEntry;
    MemPoolEntry *pEntryNext;

    if (PControl->flags & PCONTROL_FLAG_INITIALIZED) {
        XplWaitOnLocalSemaphore(PControl->sem);
        PControl->flags &= ~PCONTROL_FLAG_INITIALIZED;

        pEntry = PControl->free;
        PControl->free = NULL;

        while (pEntry != NULL) {
            pEntryNext = pEntry->next;

            if (PControl->freeCB != NULL) {
                (PControl->freeCB)(pEntry->suffix, PControl->clientData);
            }

            if (!(pEntry->flags & PENTRY_FLAG_INDIRECT_ALLOC)) {
                PLATFORM_ALLOC_FREE(pEntry);
            }

            pEntry = pEntryNext;
        }

        if ((pEntry = PControl->inUse) != NULL) {
            PControl->inUse = NULL;

            if (MemManager.flags & MEMMGR_FLAG_REPORT_LEAKS) {
                XplConsolePrintf("MemMGR: Leak report for \"%s\"\r\n", PControl->name);
            }

            while (pEntry != NULL) {
                if (MemManager.flags & MEMMGR_FLAG_REPORT_LEAKS) {
                    XplConsolePrintf("        %lu octets for \"%s\" on %lu\r\n", pEntry->size, pEntry->source.file, pEntry->source.line);
                }

                pEntryNext = pEntry->next;

                if (PControl->freeCB != NULL) {
                    (PControl->freeCB)(pEntry->suffix, PControl->clientData);
                }

                if (!(pEntry->flags & PENTRY_FLAG_INDIRECT_ALLOC)) {
                    PLATFORM_ALLOC_FREE(pEntry);
                }

                pEntry = pEntryNext;
            }
        }

        PControl->base.end = NULL;
        if (PControl->base.start != NULL) {
            PLATFORM_ALLOC_FREE(PControl->base.start);
            PControl->base.start = NULL;
        }

#if defined(LIBC)
        ReturnResourceTag(PControl->ResourceTag, 1);
        PControl->ResourceTag = NULL;
#endif

        XplCloseLocalSemaphore(PControl->sem);
    }

    return;
}

static void
MemoryPoolFree (MemPoolControl *pControl)
{
    EmptyMemoryPool(pControl);
    
    if (pControl->name) {
	PLATFORM_ALLOC_FREE(pControl->name);
	pControl->name = NULL;
    }    
}

static void 
MemoryPoolsFree(void)
{
    MemPoolControl *pControlNext;
    MemPoolControl *pControl;
    XplThreadID threadID;

    threadID = XplSetThreadGroupID(MemManager.id.group);

    XplWaitOnLocalSemaphore(MemManager.sem.pools);

    MemoryPoolFree(&MemManager.control.pool128);
    MemoryPoolFree(&MemManager.control.pool256);
    MemoryPoolFree(&MemManager.control.pool512);
    MemoryPoolFree(&MemManager.control.pool1KB);
    MemoryPoolFree(&MemManager.control.pool2KB);
    MemoryPoolFree(&MemManager.control.pool4KB);
    MemoryPoolFree(&MemManager.control.pool8KB);
    MemoryPoolFree(&MemManager.control.pool16KB);
    MemoryPoolFree(&MemManager.control.pool32KB);
    MemoryPoolFree(&MemManager.control.pool64KB);
    MemoryPoolFree(&MemManager.control.pool128KB);
    MemoryPoolFree(&MemManager.control.pool256KB);
    MemoryPoolFree(&MemManager.control.pool512KB);
    MemoryPoolFree(&MemManager.control.pool1MB);
    MemoryPoolFree(&MemManager.control.pool2MB);
    MemoryPoolFree(&MemManager.control.pool4MB);
    MemoryPoolFree(&MemManager.control.poolExtreme);
	    

    pControl = MemManager.control.poolPrivate;
    while (pControl != NULL) {
        pControlNext = pControl->next;

	MemoryPoolFree (pControl);

        pControl = pControlNext;
    }

    XplSignalLocalSemaphore(MemManager.sem.pools);

    XplSetThreadGroupID(threadID);

    return;
}

EXPORT BOOL 
MemPrivatePoolZeroCallback(void *buffer, void *clientData)
{
    memset(buffer, 0, XPL_PTR_TO_INT(clientData));

    return TRUE;
}


EXPORT void *
MemPrivatePoolAlloc(unsigned char *name, size_t AllocationSize, unsigned int MinAllocCount, unsigned int MaxAllocCount, BOOL Dynamic, BOOL Temporary, PoolEntryCB allocCB, PoolEntryCB freeCB, void *clientData)
{
    unsigned char *identity;
    size_t allocSize;
    XplThreadID threadID;
    MemPoolControl *pool;

    allocSize = ALIGN_SIZE(AllocationSize, ALIGNMENT);

    if (name) {
        identity = (unsigned char *)PLATFORM_ALLOC(strlen(MemManager.agentName) + strlen(name) + 2);

        sprintf(identity, "%s:%s", MemManager.agentName, name);
    } else {
        identity = NULL;
    }

    if ((identity != NULL) && (allocSize > 0) && (MaxAllocCount > 0)) {
        XplWaitOnLocalSemaphore(MemManager.sem.pools);
        pool = MemManager.control.poolPrivate;
        while (pool != NULL) {
            if (XplStrCaseCmp(pool->name, identity) == 0) {
                break;
            }

            pool = pool->next;
        }

        if (pool != NULL) {
            XplSignalLocalSemaphore(MemManager.sem.pools);
            if (!(pool->flags & PCONTROL_FLAG_INITIALIZED)) {
                if (Dynamic) {
                    pool->flags = PCONTROL_FLAG_DYNAMIC;
                } else {
                    pool->flags = 0;
                    pool->eCount.minimum = MinAllocCount;
                    pool->eCount.maximum = MaxAllocCount;
                }

                if (Temporary) {
                    pool->flags |= PCONTROL_FLAG_DO_NOT_SAVE;
                }

                if (pool->allocSize != allocSize) {
                    XplConsolePrintf("MemMGR: Resetting allocation size for \"%s\".\r\n", pool->name);
                    pool->allocSize = allocSize;
                    pool->flags |= PCONTROL_FLAG_CONFIG_CHANGED;
                }

                pool->defaultFlags = PENTRY_FLAG_PRIVATE_ENTRY;

                pool->allocCB = allocCB;
                pool->freeCB = freeCB;
                pool->clientData = clientData;
                FillMemoryPool(pool);
            } else {
                return(NULL);
            }
        } else {
            threadID = XplSetThreadGroupID(MemManager.id.group);
            pool = (MemPoolControl *)PLATFORM_ALLOC(sizeof(MemPoolControl));
            XplSetThreadGroupID(threadID);

            if (pool != NULL) {
                memset(pool, 0, sizeof(MemPoolControl));

                if (Dynamic) {
                    pool->flags = (PCONTROL_FLAG_DYNAMIC | PCONTROL_FLAG_CONFIG_CHANGED);
                } else {
                    pool->flags = PCONTROL_FLAG_CONFIG_CHANGED;
                }

                if (Temporary) {
                    pool->flags |= PCONTROL_FLAG_DO_NOT_SAVE;
                }

                pool->name = strdup(identity);

                pool->allocSize = allocSize;
                pool->defaultFlags = PENTRY_FLAG_PRIVATE_ENTRY;

                pool->eCount.minimum = MinAllocCount;
                pool->eCount.maximum = MaxAllocCount;

                pool->allocCB = allocCB;
                pool->freeCB = freeCB;
                pool->clientData = clientData;

                FillMemoryPool(pool);

                pool->previous = NULL;
                pool->next = MemManager.control.poolPrivate;
                if (MemManager.control.poolPrivate != NULL) {
                    MemManager.control.poolPrivate->previous = pool;
                }
                MemManager.control.poolPrivate = pool;

            } else {
                XplConsolePrintf("MemMGR: Unable to allocate %d bytes for private pool \"%s\".\r\n", sizeof(MemPoolControl), identity);
            }
            XplSignalLocalSemaphore(MemManager.sem.pools);
        }

        PLATFORM_ALLOC_FREE(identity);

        return((void *)pool);
    }

    PLATFORM_ALLOC_FREE(identity);

    return(NULL);
}

EXPORT void 
MemPrivatePoolEnumerate(void *PoolHandle, PoolEntryCB EnumerationCB, void *Data)
{
    MemPoolControl *pControl;
    MemPoolEntry *pEntry;

    XplWaitOnLocalSemaphore(MemManager.sem.pools);
    pControl = MemManager.control.poolPrivate;
    while (pControl != NULL) {
        if (pControl == (MemPoolControl *)PoolHandle) {
            break;
        }

        pControl = pControl->next;
    }
    XplSignalLocalSemaphore(MemManager.sem.pools);

    if (pControl != NULL) {
        XplWaitOnLocalSemaphore(pControl->sem);

        pEntry = pControl->inUse;
        while (pEntry != NULL) {
            EnumerationCB(pEntry->suffix, Data);
            pEntry = pEntry->next;
        }

        XplSignalLocalSemaphore(pControl->sem);
    }

    return;
}

EXPORT void 
MemPrivatePoolStatistics(void *PoolHandle, MemStatistics *PoolStats)
{
    MemPoolControl *pControl;

    XplWaitOnLocalSemaphore(MemManager.sem.pools);
    pControl = MemManager.control.poolPrivate;
    while (pControl != NULL) {
        if (pControl == (MemPoolControl *)PoolHandle) {
            break;
        }

        pControl = pControl->next;
    }
    XplSignalLocalSemaphore(MemManager.sem.pools);

    if (pControl != NULL) {
        XplWaitOnLocalSemaphore(pControl->sem);

        PoolStats->totalAlloc.count = pControl->eCount.allocated;
        PoolStats->totalAlloc.size = pControl->eCount.allocated * pControl->allocSize;
        PoolStats->pitches = pControl->stats.pitches;
        PoolStats->strikes = pControl->stats.strikes;
        PoolStats->hits = pControl->stats.pitches - pControl->stats.strikes;

        XplSignalLocalSemaphore(pControl->sem);
    }

    return;
}

EXPORT void 
MemPrivatePoolFree(void *PoolHandle)
{
    MemPoolControl *pControl;
    XplThreadID threadID;

    XplWaitOnLocalSemaphore(MemManager.sem.pools);
    pControl = MemManager.control.poolPrivate;
    while (pControl != NULL) {
        if (pControl == (MemPoolControl *)PoolHandle) {
            break;
        }

        pControl = pControl->next;
    }

    if (pControl != NULL) {
        threadID = XplSetThreadGroupID(MemManager.id.group);
        EmptyMemoryPool(pControl);
        XplSetThreadGroupID(threadID);
    }
    XplSignalLocalSemaphore(MemManager.sem.pools);

    return;
}

EXPORT void *
MemPrivatePoolGetEntry(void *PoolHandle)
{
    register MemPoolEntry *pEntry;

    PULL_PRIVATE_FREE_ENTRY((MemPoolControl *)PoolHandle, pEntry);

    if (pEntry != NULL) {
        return(pEntry->suffix);
    }

    return(NULL);
}

EXPORT void *
MemPrivatePoolGetEntryDebug(void *PoolHandle, const char *SourceFile, unsigned long SourceLine)
{
    register MemPoolEntry *pEntry;

    PULL_PRIVATE_FREE_ENTRY((MemPoolControl *)PoolHandle, pEntry);

    if (pEntry != NULL) {
        if (SourceFile != NULL) {
            strncpy(pEntry->source.file, SourceFile, sizeof(pEntry->source.file));
            pEntry->source.line = SourceLine;
        } else {
            strcpy(pEntry->source.file, "UNKNOWN");
            pEntry->source.line = 0;
        }

        return(pEntry->suffix);
    }

    return(NULL);
}

EXPORT void 
MemPrivatePoolReturnEntry(void *Source)
{
    register MemPoolEntry *pEntry;

    if (Source != NULL) {
        pEntry = (MemPoolEntry *)((unsigned char *)Source - SUFFIX_OFFSET);

        if (pEntry->flags & PENTRY_FLAG_INUSE) {
            PUSH_NON_EXTREME_FREE_ENTRY(pEntry->control, pEntry);
        } else {
            XplConsolePrintf("MemMGR: free'd private allocation free'd again; originally free'd by %s [%lu].\r\n", pEntry->source.file, pEntry->source.line);
        }
    }

    return;
}

EXPORT void 
MemPrivatePoolDiscardEntry(void *Source)
{
    register XplThreadID threadID;
    register MemPoolEntry *pEntry;

    if (Source != NULL) {
        pEntry = (MemPoolEntry *)((unsigned char *)Source - SUFFIX_OFFSET);

        XplWaitOnLocalSemaphore(pEntry->control->sem);

        if (pEntry->next != NULL) {
            pEntry->next->previous = pEntry->previous;
        }

        if (pEntry->previous != NULL) {
            pEntry->previous->next = pEntry->next;
        } else {
            pEntry->control->inUse = pEntry->next;
        }

        pEntry->control->eCount.allocated--;

        XplSignalLocalSemaphore(pEntry->control->sem);

        if (!(pEntry->flags & PENTRY_FLAG_INDIRECT_ALLOC)) {
            threadID = XplSetThreadGroupID(MemManager.id.group);
            PLATFORM_ALLOC_FREE(pEntry);
            XplSetThreadGroupID(threadID);
        }
    }

    return;
}

static BOOL 
LoadManagerConfiguration(const unsigned char *AgentName)
{
    XplThreadID threadID;
    unsigned char *ptr;
    unsigned char *cur;
    unsigned char buffer[256];
    FILE *handle;
    MemPoolControl *privatePool;
    MemPoolControl pControl;

    memset(&pControl, 0, sizeof(MemPoolControl));

#if defined(NETWARE) || defined(LIBC)
    strcpy(buffer, "SYS:/etc/nmpools.cfg");
#elif defined(LINUX) || defined(SOLARIS) || defined(S390RH)
    if (AgentName != NULL) {
        snprintf(buffer, sizeof (buffer), XPL_DEFAULT_DBF_DIR "/%s.conf", AgentName);

        cur = ptr = buffer;
        while ((*cur = *ptr) != '\0') {
            if (!(isspace(*ptr))) {
                cur++;
            }

            ptr++;
        }

        *cur = '\0';
    } else {
        goto UseDefaultConfiguration;
    }
#else    /*    defined(WIN32)    */
    buffer[0] = '\0';

    if (AgentName != NULL) {
        int readCount;
        long result;
        long type;
        unsigned char winPath[256];
        HKEY key;

        wsprintf(buffer, "SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion");
        result = RegOpenKeyEx(HKEY_LOCAL_MACHINE, buffer, 0, KEY_READ, &key);
        if (result == ERROR_SUCCESS) {
            /*    Determine the required read buffer size and load it if the buffer is large enough.    */
            result = RegQueryValueEx(key, "SystemRoot", NULL, &type, NULL, &readCount);
            if ((result == ERROR_SUCCESS) && (type == REG_SZ) && (readCount <= sizeof(winPath))) {
                result = RegQueryValueEx(key, "SystemRoot", NULL, &type, winPath, &readCount);
                if (result == ERROR_SUCCESS) {
                    sprintf(buffer, "%s/%s.cfg", winPath, AgentName);
                }
            }

            RegCloseKey(key);
        }
    }

    if (buffer[0] == '\0') {
        goto UseDefaultConfiguration;
    }
#endif

    handle = fopen(buffer, "rt");
    if (handle != NULL) {
        /*    The format for pool configuration attributes is:
                Pool: \"<name>\" [FIX | DYN] <allocSize> <eCount.minimum> <eCount.maximum>
        */
        while ((ferror(handle) == 0) && (feof(handle) == 0)) {
            if (pControl.name) {
                PLATFORM_ALLOC_FREE(pControl.name);
            }

            memset(&pControl, 0, sizeof(MemPoolControl));
            if (fgets(buffer, sizeof(buffer), handle) != NULL) {
                if (strncmp(buffer, "Pool: ", 6) == 0) {
                    cur = (unsigned char *)(buffer) + 6;

                    /* Read the pool identification string. */
                    if (cur[0] == '"') {
                        ptr = strchr(++cur, '"');
                        if (ptr != NULL) {
                            *ptr = '\0';
                            pControl.name = strdup(cur);
                            *ptr = '"';

                            cur = ptr + 1;
                        } else {
                            XplConsolePrintf("MemMGR: Invalid config format of \"%s\".\r\n", buffer);
                            XplConsolePrintf("        Missing closing quote.\r\n");
                            continue;
                        }
                    } else {
                        XplConsolePrintf("MemMGR: Invalid config format of \"%s\".\r\n", buffer);
                        XplConsolePrintf("        Missing pool identifier opening quote.\r\n");
                        continue;
                    }

                    /* Read the pool state designator. */
                    if (strncmp(cur, " DYN ", 5) == 0) {
                        pControl.flags = PCONTROL_FLAG_DYNAMIC;
                    } else if (strncmp(cur, " FIX ", 5) == 0) {
                        pControl.flags = 0;
                    } else {
                        XplConsolePrintf("MemMGR: Invalid config format of \"%s\".\r\n", buffer);
                        XplConsolePrintf("        Invalid pool state designator.\r\n");
                        continue;
                    }

                    cur += 5;

                    /* Read the pool allocation size. */
                    ptr = strchr(cur, ' ');
                    if (ptr != NULL) {
                        *ptr = '\0';
                        pControl.allocSize = ALIGN_SIZE(atol(cur), ALIGNMENT);
                        *ptr = ' ';

                        cur = ptr + 1;
                    } else {
                        XplConsolePrintf("MemMGR: Invalid config format of \"%s\".\r\n", buffer);
                        XplConsolePrintf("        Invalid pool allocation size.\r\n");
                        continue;
                    }

                    /* Read the pool minimum allocation count. */
                    ptr = strchr(cur, ' ');
                    if (ptr != NULL) {
                        *ptr = '\0';
                        pControl.eCount.minimum = atol(cur);
                        *ptr = ' ';

                        cur = ptr + 1;
                    } else {
                        XplConsolePrintf("MemMGR: Invalid config format of \"%s\".\r\n", buffer);
                        XplConsolePrintf("        Invalid pool minimum allocation count.\r\n");
                        continue;
                    }

                    /* Read the pool maximum allocation count. */
                    pControl.eCount.maximum = atol(cur);

                    /* Validate the settings. */
                    if ((pControl.name[0] != '\0') && (pControl.eCount.maximum >= pControl.eCount.minimum)) {
                        if (strcmp(pControl.name, MemManager.control.pool128.name) == 0) {
                            MemManager.control.pool128.flags = pControl.flags;
                            MemManager.control.pool128.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool128.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool256.name) == 0) {
                            MemManager.control.pool256.flags = pControl.flags;
                            MemManager.control.pool256.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool256.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool512.name) == 0) {
                            MemManager.control.pool512.flags = pControl.flags;
                            MemManager.control.pool512.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool512.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool1KB.name) == 0) {
                            MemManager.control.pool1KB.flags = pControl.flags;
                            MemManager.control.pool1KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool1KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool2KB.name) == 0) {
                            MemManager.control.pool2KB.flags = pControl.flags;
                            MemManager.control.pool2KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool2KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool4KB.name) == 0) {
                            MemManager.control.pool4KB.flags = pControl.flags;
                            MemManager.control.pool4KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool4KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool8KB.name) == 0) {
                            MemManager.control.pool8KB.flags = pControl.flags;
                            MemManager.control.pool8KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool8KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool16KB.name) == 0) {
                            MemManager.control.pool16KB.flags = pControl.flags;
                            MemManager.control.pool16KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool16KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool32KB.name) == 0) {
                            MemManager.control.pool32KB.flags = pControl.flags;
                            MemManager.control.pool32KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool32KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool64KB.name) == 0) {
                            MemManager.control.pool64KB.flags = pControl.flags;
                            MemManager.control.pool64KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool64KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool128KB.name) == 0) {
                            MemManager.control.pool128KB.flags = pControl.flags;
                            MemManager.control.pool128KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool128KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool256KB.name) == 0) {
                            MemManager.control.pool256KB.flags = pControl.flags;
                            MemManager.control.pool256KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool256KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool512KB.name) == 0) {
                            MemManager.control.pool512KB.flags = pControl.flags;
                            MemManager.control.pool512KB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool512KB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool1MB.name) == 0) {
                            MemManager.control.pool1MB.flags = pControl.flags;
                            MemManager.control.pool1MB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool1MB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool2MB.name) == 0) {
                            MemManager.control.pool2MB.flags = pControl.flags;
                            MemManager.control.pool2MB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool2MB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.pool4MB.name) == 0) {
                            MemManager.control.pool4MB.flags = pControl.flags;
                            MemManager.control.pool4MB.eCount.minimum = pControl.eCount.minimum;
                            MemManager.control.pool4MB.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        } else if (strcmp(pControl.name, MemManager.control.poolExtreme.name) == 0) {
                            MemManager.control.poolExtreme.flags = 0;
                            MemManager.control.poolExtreme.eCount.minimum = 0;
                            MemManager.control.poolExtreme.eCount.maximum = pControl.eCount.maximum;
                            continue;
                        }

                        XplWaitOnLocalSemaphore(MemManager.sem.pools);

                        threadID = XplSetThreadGroupID(MemManager.id.group);
                        privatePool = (MemPoolControl *)PLATFORM_ALLOC(sizeof(MemPoolControl));
                        XplSetThreadGroupID(threadID);

                        if (privatePool != NULL) {
                            memset(privatePool, 0, sizeof(MemPoolControl));

                            privatePool->previous = NULL;

                            privatePool->next = MemManager.control.poolPrivate;
                            if (privatePool->next != NULL) {
                                privatePool->next->previous = privatePool;
                            }

                            MemManager.control.poolPrivate = privatePool;
                        } else {
                            XplSignalLocalSemaphore(MemManager.sem.pools);

                            XplConsolePrintf("MemMGR: Unable to allocate %d bytes for pool \"%s\".\r\n", sizeof(MemPoolControl), pControl.name);
                            XplConsolePrintf("        Shutting down.\r\n");

                            return(FALSE);
                        }

                        MemManager.control.poolPrivate->flags = pControl.flags;
                        MemManager.control.poolPrivate->allocSize = pControl.allocSize;
                        MemManager.control.poolPrivate->eCount.minimum = pControl.eCount.minimum;
                        MemManager.control.poolPrivate->eCount.maximum = pControl.eCount.maximum;

                        MemManager.control.poolPrivate->name = pControl.name;
                        pControl.name = NULL;

                        XplSignalLocalSemaphore(MemManager.sem.pools);

                        continue;
                    } else {
                        XplConsolePrintf("MemMGR: Invalid config format of \"%s\".\r\n", buffer);
                        XplConsolePrintf("        Missing identifier or minimum allocation exceeds maximum.\r\n");
                        continue;
                    }
                }
            }
        }

        fclose(handle);
    }

UseDefaultConfiguration:
    XplWaitOnLocalSemaphore(MemManager.sem.pools);

    /* Initialize memory pools. */
    FillMemoryPool(&MemManager.control.pool128);
    FillMemoryPool(&MemManager.control.pool256);
    FillMemoryPool(&MemManager.control.pool512);
    FillMemoryPool(&MemManager.control.pool1KB);
    FillMemoryPool(&MemManager.control.pool2KB);
    FillMemoryPool(&MemManager.control.pool4KB);
    FillMemoryPool(&MemManager.control.pool8KB);
    FillMemoryPool(&MemManager.control.pool16KB);
    FillMemoryPool(&MemManager.control.pool32KB);
    FillMemoryPool(&MemManager.control.pool64KB);
    FillMemoryPool(&MemManager.control.pool128KB);
    FillMemoryPool(&MemManager.control.pool256KB);
    FillMemoryPool(&MemManager.control.pool512KB);
    FillMemoryPool(&MemManager.control.pool1MB);
    FillMemoryPool(&MemManager.control.pool2MB);
    FillMemoryPool(&MemManager.control.pool4MB);
    FillMemoryPool(&MemManager.control.poolExtreme);

    XplSignalLocalSemaphore(MemManager.sem.pools);

    return(TRUE);
}

static void
SavePoolConfiguration(MemPoolControl *pControl, FILE *handle)
{    
    unsigned int adjust;

    if (pControl->flags & PCONTROL_FLAG_INITIALIZED) {
	XplWaitOnLocalSemaphore(pControl->sem);
    }
    
    if (pControl->flags & PCONTROL_FLAG_DYNAMIC) {
	if (pControl->eCount.allocated < (pControl->eCount.minimum + (pControl->eCount.minimum / 10))) {
	    /*    Adjust the pool downwards.    */
	    adjust = pControl->eCount.minimum / 5;
	    
	    if (pControl->eCount.minimum > adjust) {
		pControl->eCount.minimum -= adjust;
	    } else {
		pControl->eCount.minimum = 0;
	    }
	    
	    if (pControl->eCount.maximum > adjust) {
		pControl->eCount.maximum -= adjust;
	    } else {
		pControl->eCount.maximum = 8;
	    }
	    
	    if (pControl->eCount.maximum <= pControl->eCount.minimum) {
		pControl->eCount.maximum = pControl->eCount.minimum + (pControl->eCount.minimum / 2);
	    }
	} else if (pControl->eCount.allocated > (pControl->eCount.maximum - (pControl->eCount.maximum / 10))) {
	    /*    Adjust the pool upwards.    */
	    adjust = pControl->eCount.maximum / 3;
	    
	    pControl->eCount.minimum += adjust;
	    pControl->eCount.maximum += adjust;
	} else if (pControl->eCount.minimum == 0) {
	    pControl->eCount.minimum = pControl->eCount.allocated;
	}
    }
    
    fprintf(handle, "Pool: \"%s\" %s %010lu %010lu %010lu\r\n", pControl->name, (pControl->flags & PCONTROL_FLAG_DYNAMIC)? "DYN": "FIX", 
	    (long unsigned int)pControl->allocSize, (long unsigned int)pControl->eCount.minimum, (long unsigned int)pControl->eCount.maximum);
    pControl->flags |= PCONTROL_FLAG_CONFIG_SAVED;
    
    if (pControl->flags & PCONTROL_FLAG_INITIALIZED) {
	XplSignalLocalSemaphore(pControl->sem);
    }
}


static BOOL 
SaveManagerConfiguration(const unsigned char *AgentName)
{
    unsigned char buffer[256];
    FILE *handle;
    MemPoolControl *pControl = NULL;

#if defined(NETWARE) || defined(LIBC)
    strcpy(buffer, "SYS:/etc/nmpools.cfg");
#elif defined(LINUX) || defined(SOLARIS) || defined(S390RH)
    unsigned char    *src;
    unsigned char    *dest;

    if (AgentName != NULL) {
        strcpy(buffer, "/etc/bongo");
        XplMakeDir(buffer);

        snprintf(buffer, sizeof (buffer), XPL_DEFAULT_DBF_DIR "/%s.conf", AgentName);

        src = dest = buffer;
        while ((*dest = *src) != '\0') {
            if (!(isspace(*src))) {
                dest++;
            }

            src++;
        }

        *dest = '\0';
    } else {
        return(FALSE);
    }
#else    /*    defined(WIN32)    */
    int                    readCount;
    long                    result;
    long                    type;
    unsigned char        winPath[256];
    HKEY                    key;

    buffer[0] = '\0';
    if (AgentName != NULL) {
        wsprintf(buffer, "SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion");
        result = RegOpenKeyEx(HKEY_LOCAL_MACHINE, buffer, 0, KEY_READ, &key);
        if (result == ERROR_SUCCESS) {
            /*    Determine the required read buffer size and load it if the buffer is large enough.    */
            result = RegQueryValueEx(key, "SystemRoot", NULL, &type, NULL, &readCount);
            if ((result == ERROR_SUCCESS) && (type == REG_SZ) && (readCount <= sizeof(winPath))) {
                result = RegQueryValueEx(key, "SystemRoot", NULL, &type, winPath, &readCount);
                if (result == ERROR_SUCCESS) {
                    sprintf(buffer, "%s/%s.cfg", winPath, AgentName);
                }
            }

            RegCloseKey(key);
        }
    }

    if (buffer[0] == '\0') {
        return(FALSE);
    }
#endif

    if ((time(NULL) - MemManager.startTime) < (60 *60)) {
        return(TRUE);
    }

    handle = fopen(buffer, "wt");
    if (handle != NULL) {
	SavePoolConfiguration (&MemManager.control.pool128, handle);
	SavePoolConfiguration (&MemManager.control.pool256, handle);
	SavePoolConfiguration (&MemManager.control.pool512, handle);
	SavePoolConfiguration (&MemManager.control.pool1KB, handle);
	SavePoolConfiguration (&MemManager.control.pool2KB, handle);
	SavePoolConfiguration (&MemManager.control.pool4KB, handle);
	SavePoolConfiguration (&MemManager.control.pool8KB, handle);
	SavePoolConfiguration (&MemManager.control.pool16KB, handle);
	SavePoolConfiguration (&MemManager.control.pool32KB, handle);
	SavePoolConfiguration (&MemManager.control.pool64KB, handle);
	SavePoolConfiguration (&MemManager.control.pool128KB, handle);
	SavePoolConfiguration (&MemManager.control.pool256KB, handle);
	SavePoolConfiguration (&MemManager.control.pool512KB, handle);
	SavePoolConfiguration (&MemManager.control.pool1MB, handle);
	SavePoolConfiguration (&MemManager.control.pool2MB, handle);
	SavePoolConfiguration (&MemManager.control.pool4MB, handle);

        XplWaitOnLocalSemaphore(MemManager.sem.pools);

        pControl = MemManager.control.poolPrivate;
        while (pControl != NULL) {
	    SavePoolConfiguration (pControl, handle);

            pControl = pControl->next;
        }

        XplSignalLocalSemaphore(MemManager.sem.pools);

        fclose(handle);
        handle = NULL;

        return(TRUE);
    }

    return(FALSE);
}

static void 
StopManagerLibrary(const unsigned char *AgentName)
{
    XplThreadID threadID;
    MemPoolControl *pControl;
    MemPoolControl *pControlNext;

    if (MemManager.state < LIBRARY_SHUTTING_DOWN) {
        MemManager.state = LIBRARY_SHUTTING_DOWN;

        /*    Save the configuration    */
        SaveManagerConfiguration(AgentName);

        MemManager.state = LIBRARY_SHUTDOWN;

        /*    Release memory pools.    */
        MemoryPoolsFree();

        /*    free the private pool control structures    */
        threadID = XplSetThreadGroupID(MemManager.id.group);

        XplWaitOnLocalSemaphore(MemManager.sem.pools);

        pControl = MemManager.control.poolPrivate;
        MemManager.control.poolPrivate = NULL;
        while (pControl != NULL) {
            pControlNext = pControl->next;
            PLATFORM_ALLOC_FREE(pControl);
            pControl = pControlNext;
        }

        XplSetThreadGroupID(threadID);

        XplCloseLocalSemaphore(MemManager.sem.pools);
    }

    return;
}

static void 
InternalPoolInit(MemPoolControl *PControl, const unsigned char *Name, unsigned long AllocSize, unsigned long MaximumEntries, unsigned long Flags)
{
    memset(PControl, 0, sizeof(MemPoolControl));

    PControl->flags = Flags;
    if (!PControl->flags) {
        PControl->flags = PCONTROL_FLAG_DYNAMIC;
    }

    PControl->name = strdup(Name);
    PControl->allocSize = ALIGN_SIZE(AllocSize, ALIGNMENT);
    PControl->eCount.maximum = MaximumEntries;

    return;
}

static void 
StartManagerLibrary(const unsigned char *AgentName)
{
    MemManager.agentName = strdup(AgentName);
    
    MemManager.flags |= MEMMGR_FLAG_REPORT_LEAKS;

    MemManager.id.group =  XplGetThreadGroupID();

    XplOpenLocalSemaphore(MemManager.sem.pools, 1);

    XplSafeWrite(MemManager.useCount, 0);

    InternalPoolInit(&MemManager.control.pool128, "128 Bytes", POOL128_ALLOC_SIZE, 20 * 1024, 0);
    InternalPoolInit(&MemManager.control.pool256, "256 Bytes", POOL256_ALLOC_SIZE, 6 * 1024, 0);
    InternalPoolInit(&MemManager.control.pool512, "512 Bytes", POOL512_ALLOC_SIZE, 2 * 1024, 0);
    InternalPoolInit(&MemManager.control.pool1KB, "1 KiloByte", POOL1KB_ALLOC_SIZE, 2 * 1024, 0);
    InternalPoolInit(&MemManager.control.pool2KB, "2 KiloBytes", POOL2KB_ALLOC_SIZE, 6 * 1024, 0);
    InternalPoolInit(&MemManager.control.pool4KB, "4 KiloBytes", POOL4KB_ALLOC_SIZE, 1024, 0);
    InternalPoolInit(&MemManager.control.pool8KB, "8 KiloBytes", POOL8KB_ALLOC_SIZE, 512, 0);
    InternalPoolInit(&MemManager.control.pool16KB, "16 KiloBytes", POOL16KB_ALLOC_SIZE, 512, 0);
    InternalPoolInit(&MemManager.control.pool32KB, "32 KiloBytes", POOL32KB_ALLOC_SIZE, 512, 0);
    InternalPoolInit(&MemManager.control.pool64KB, "64 KiloBytes", POOL64KB_ALLOC_SIZE, 512, 0);
    InternalPoolInit(&MemManager.control.pool128KB, "128 KiloBytes", POOL128KB_ALLOC_SIZE, 384, 0);
    InternalPoolInit(&MemManager.control.pool256KB, "256 KiloBytes", POOL256KB_ALLOC_SIZE, 384, 0);
    InternalPoolInit(&MemManager.control.pool512KB, "512 KiloBytes", POOL512KB_ALLOC_SIZE, 256, 0);
    InternalPoolInit(&MemManager.control.pool1MB, "1 MegaByte", POOL1MB_ALLOC_SIZE, 64, 0);
    InternalPoolInit(&MemManager.control.pool2MB, "2 MegaBytes", POOL2MB_ALLOC_SIZE, 32, 0);
    InternalPoolInit(&MemManager.control.pool4MB, "4 MegaBytes", POOL4MB_ALLOC_SIZE, 32, 0);
    InternalPoolInit(&MemManager.control.poolExtreme, "Extreme MegaBytes", 0, 64, PENTRY_FLAG_EXTREME_ENTRY);

    LoadManagerConfiguration(AgentName);

    MemManager.state = LIBRARY_RUNNING;

    return;
}

EXPORT BOOL 
MemoryManagerOpen(const unsigned char *AgentName)
{
    if ((MemManager.state == LIBRARY_LOADED) && (AgentName != NULL)) {
        MemManager.state = LIBRARY_INITIALIZING;

        StartManagerLibrary(AgentName);
    }

    while ((MemManager.state == LIBRARY_INITIALIZING) || (MemManager.state == LIBRARY_LOADED)) {
        XplDelay(100);
    }

    if (MemManager.state == LIBRARY_RUNNING) {
        XplSafeIncrement(MemManager.useCount);
        return(TRUE);
    }

    return(FALSE);
}

EXPORT BOOL 
MemoryManagerClose(const unsigned char *AgentName)
{
    XplSafeDecrement(MemManager.useCount);

    if (XplSafeRead(MemManager.useCount) > 0) {
        return(FALSE);
    }

    StopManagerLibrary(AgentName);

    return(TRUE);
}

#if 0
#if defined(WIN32)
BOOL WINAPI DllMain(HINSTANCE hInst, DWORD Reason, LPVOID Reserved)
{
    if (Reason == DLL_PROCESS_ATTACH) {
        DisableThreadLibraryCalls(hInst);
    }

    return(TRUE);
}
#elif defined(NETWARE) || defined(LIBC)
void MMShutdownSigHandler(int sigtype)
{
    static int    signaled = FALSE;

    if (!signaled && ((sigtype == SIGTERM) || (sigtype == SIGINT))) {
        XplSignalLocalSemaphore(MemManager.sem.shutdown);
        XplWaitOnLocalSemaphore(MemManager.sem.shutdown);

        XplCloseLocalSemaphore(MemManager.sem.shutdown);
    }

    return;
}

int main(int argc, char *argv[])
{
    int                i;
    int                ccode;
    unsigned long    semValue = 0;

    MemManager.state = LIBRARY_INITIALIZING;

    XplSignalHandler(MMShutdownSigHandler);

    XplOpenLocalSemaphore(MemManager.sem.shutdown, 0);

    MemManager.startTime = time(NULL);

    if (argc > 1) {
        for (i = 1; i < argc; i++) {
            if (XplStrCaseCmp("-leaks", argv[i]) == 0) {
                MemManager.flags |= MEMMGR_FLAG_REPORT_LEAKS;
            }
        }
    }

#if !defined(LIBC)
    StartManagerLibrary(NULL);
    if (MemManager.state == LIBRARY_RUNNING) {
        MemoryManagerOpen(NULL);

        XplWaitOnLocalSemaphore(MemManager.sem.shutdown);

        MemoryManagerClose(NULL);
    } else {
        XplBell();
        XplConsolePrintf("MemMGR: Unable to initialize!\r\n");
        XplBell();

        return(-1);
    }
#else
    MMNLMHandle = getnlmhandle();
    if (MMNLMHandle != NULL) {
        MMResourceTag = AllocateResourceTag(MMNLMHandle, "Bongo Memory Manager", AllocSignature);
        if (MMResourceTag != NULL) {
            StartManagerLibrary(NULL);
            if (MemManager.state == LIBRARY_RUNNING) {
                MemoryManagerOpen(NULL);

                XplWaitOnLocalSemaphore(MemManager.sem.shutdown);

                MemoryManagerClose(NULL);
            } else {
                XplBell();
                XplConsolePrintf("MemMGR: Unable to initialize!\r\n");
                XplBell();

                return(-1);
            }
        } else {
            XplBell();
            XplConsolePrintf("MemMGR: Unable to allocate global resource tag!\r\n");
            XplBell();

            return(-1);
        }
    } else {
        XplBell();
        XplBell();
        XplConsolePrintf("MemMGR: Unable to identify module handle!\r\n");

        return(-1);
    }
#endif

    XplSignalLocalSemaphore(MemManager.sem.shutdown);

    return(0);
}
#endif
#endif
