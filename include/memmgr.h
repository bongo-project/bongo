/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001, 2006 Novell, Inc. All Rights Reserved.
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

#ifndef MEMMGR_H
#define MEMMGR_H

#include "xpl.h"

XPL_BEGIN_C_LINKAGE

/* Library Specific */
#define MEMMGR_API_VERSION          1
#define MEMMGR_API_COMPATIBILITY    1

typedef BOOL (*PoolEntryCB)(void *Buffer, void *ClientData);

typedef struct _MemStatistics {
    struct {
        unsigned long count;
        unsigned long size;
    } totalAlloc;

    unsigned long pitches;
    unsigned long hits;
    unsigned long strikes;

    struct {
        unsigned long size;

        unsigned long minimum;
        unsigned long maximum;

        unsigned long allocated;
    } entry;

    unsigned char *name;

    struct _MemStatistics *next;
    struct _MemStatistics *previous;
} MemStatistics;

EXPORT BOOL MemoryManagerOpen(const unsigned char *AgentName);
EXPORT BOOL MemoryManagerClose(const unsigned char *AgentName);

/*    Memory Allocation API's    */
EXPORT void *MemCallocDirect(size_t Number, size_t Size);
EXPORT void *MemCallocDebugDirect(size_t Number, size_t Size, const char *SourceFile, unsigned long SourceLine);

EXPORT void *MemMallocDirect(size_t Size);
EXPORT void *MemMallocDebugDirect(size_t Size, const char *SourceFile, unsigned long SourceLine);

EXPORT void *MemMalloc0Direct(size_t size);
EXPORT void *MemMalloc0DebugDirect(size_t size, const char *sourceFile, unsigned long sourceLine);


EXPORT void *MemReallocDirect(void *Source, size_t Size);
EXPORT void *MemReallocDebugDirect(void *Source, size_t Size, const char *SourceFile, unsigned long SourceLine);

#if !defined(MEMMGR_DEBUG)
#define MemCalloc(n, s)     MemCallocDirect((n), (s))
#else
#define MemCalloc(n, s)     MemCallocDebugDirect((n), (s), __FILE__, __LINE__)
#endif

#if !defined(MEMMGR_DEBUG)
#define MemMalloc(s)        MemMallocDirect((s))
#else
#define MemMalloc(s)        MemMallocDebugDirect((s), __FILE__, __LINE__)
#endif

#if !defined(MEMMGR_DEBUG)
#define MemMalloc0(s)        MemMalloc0Direct((s))
#else
#define MemMalloc0(s)        MemMalloc0DebugDirect((s), __FILE__, __LINE__)
#endif

#if !defined(MEMMGR_DEBUG)
#define MemRealloc(m, s)    MemReallocDirect((m), (s))
#else
#define MemRealloc(m, s)    MemReallocDebugDirect((m), (s), __FILE__, __LINE__)
#endif

/* Helpers */
#define MemNew0(type, num) ((type*)MemMalloc0(sizeof(type) * (num)))
#define MemNew(type, num) ((type*)MemMalloc(sizeof(type) * (num)))

/*    Memory De-Allocation API's    */
EXPORT void MemFreeDirect(void *Source);
EXPORT void MemFreeDebugDirect(void *Source, const char *SourceFile, unsigned long SourceLine);

#if !defined(MEMMGR_DEBUG)
#define MemFree(m)          MemFreeDirect((m))
#else
#define MemFree(m)          MemFreeDebugDirect((m), __FILE__, __LINE__)
#endif

/*    String Allocation API's    */
EXPORT char *MemStrdupDirect(const char *StrSource);
EXPORT char *MemStrndupDirect(const char *StrSource, int n);
EXPORT char *MemStrdupDebugDirect(const char *StrSource, const char *SourceFile, unsigned long SourceLine);
EXPORT char *MemStrndupDebugDirect(const char *StrSource, int n, const char *SourceFile, unsigned long SourceLine);
EXPORT char *MemStrCatDirect(const char *stra, const char *strb);
EXPORT char *MemStrCatDebugDirect(const char *stra, const char *strb, const char *sourceFile, unsigned long sourceLine);

#if !defined(MEMMGR_DEBUG)
#define MemStrdup(s)        MemStrdupDirect((s))
#define MemStrndup(s, n)        MemStrndupDirect((s), (n))
#define MemStrCat(s1, s2)        MemStrCatDirect((s1), (s2))
#else
#define MemStrdup(s)        MemStrdupDebugDirect((s), __FILE__, __LINE__)
#define MemStrndup(s, n)        MemStrndupDebugDirect((s), (n), __FILE__, __LINE__)
#define MemStrCat(s1, s2)        MemStrCatDebugDirect((s1), (s2), __FILE__, __LINE__)
#endif

/*    Management Statistics    */
EXPORT MemStatistics *MemAllocStatistics(void);
EXPORT void MemFreeStatistics(MemStatistics *Statistics);

/*    Private Pool API's    */
EXPORT void *MemPrivatePoolAlloc(unsigned char *Ident, size_t AllocationSize, unsigned int MinAllocCount, unsigned int MaxAllocCount, BOOL Dynamic, BOOL Temporary, PoolEntryCB AllocCB, PoolEntryCB FreeCB, void *ClientData);
EXPORT void MemPrivatePoolFree(void *PoolHandle);
EXPORT void MemPrivatePoolEnumerate(void *PoolHandle, PoolEntryCB EnumerationCB, void *Data);
EXPORT void MemPrivatePoolStatistics(void *PoolHandle, MemStatistics *PoolStats);

EXPORT void *MemPrivatePoolGetEntry(void *PoolHandle);
EXPORT void *MemPrivatePoolGetEntryDebug(void *PoolHandle, const char *SourceFile, unsigned long SourceLine);
EXPORT void MemPrivatePoolReturnEntry(void *PoolEntry);
EXPORT void MemPrivatePoolDiscardEntry(void *Source);

EXPORT BOOL MemPrivatePoolZeroCallback(void *buffer, void *clientData);


/** memstack.c **/

typedef struct _BongoMemChunk BongoMemChunk;

struct _BongoMemChunk {
    uint8_t *curptr;
    uint8_t *endptr;

    BongoMemChunk *next;

    uint8_t data[1];
};

typedef struct _BongoMemStack {
    BongoMemChunk *chunk;
} BongoMemStack;

int BongoMemStackInit(BongoMemStack *stack, size_t size);
void BongoMemStackDestroy(BongoMemStack *stack);

BongoMemStack *BongoMemStackNew(size_t size);
void BongoMemStackDelete(BongoMemStack *stack);

void *BongoMemStackAlloc(BongoMemStack *stack, size_t size); /* but use the macro... */
void *BongoMemStackPush(BongoMemStack *stack, void *obj, size_t size);

char *BongoMemStackPushStr(BongoMemStack *stack, const char *str);

void BongoMemStackPop(BongoMemStack *stack, void *obj);

#define BongoMemStackPeek(stack) ((stack)->chunk->curptr)

void BongoMemStackReset(BongoMemStack *stack);

#define BONGO_MEMSTACK_ALLOC(stack, size)                                  \
(                                                                         \
    (stack)->chunk->curptr + size > (stack)->chunk->endptr                \
        ? BongoMemStackAlloc((stack), size)                                \
        : ((stack)->chunk->curptr += size, (stack)->chunk->curptr - size) \
)

/** end memstack.c **/

XPL_END_C_LINKAGE

#endif    /* MEMMGR_H */
