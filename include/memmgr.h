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
#include <glib.h>

XPL_BEGIN_C_LINKAGE

/* Alignment macros */
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
/* default */
#define ALIGNMENT 4
#endif

#define ALIGN_SIZE(s, a) (((s) + a - 1) & ~(a - 1))

#define MemMalloc(s)    g_malloc((s))
#define MemMalloc0(s)   g_malloc0((s))
#define MemRealloc(m, s)    g_realloc((m), (s))
#define MemStrdup(s)    g_strdup((s))
#define MemStrndup(m, s)   g_strndup((m), (s))
#define MemFree(m)      g_free((m))
#define MemCalloc(n, s) g_malloc((n)*(s))

/* Helpers */
#define MemNew0(type, num) ((type*)MemMalloc0(sizeof(type) * (num)))
#define MemNew(type, num) ((type*)MemMalloc(sizeof(type) * (num)))

/*    Private Pool API's    */
typedef BOOL (*PoolEntryCB)(void *Buffer, void *ClientData);

EXPORT void *MemPrivatePoolAlloc(unsigned char *Ident, size_t AllocationSize, unsigned int MinAllocCount, unsigned int MaxAllocCount, BOOL Dynamic, BOOL Temporary, PoolEntryCB AllocCB, PoolEntryCB FreeCB, void *ClientData);
EXPORT void MemPrivatePoolFree(void *PoolHandle);

EXPORT void *MemPrivatePoolGetEntryDirect(void *PoolHandle, const char *SourceFile, unsigned long SourceLine);
EXPORT void MemPrivatePoolReturnEntryDirect(void *PoolHandle, void *PoolEntry, const char *SourceFile, unsigned long SourceLine);
#define	MemPrivatePoolGetEntry(p)	MemPrivatePoolGetEntryDirect((p), __FILE__, __LINE__)
#define MemPrivatePoolReturnEntry(p, e)	MemPrivatePoolReturnEntryDirect((p), (e), __FILE__, __LINE__);


/** memstack.c **/

typedef struct _BongoMemChunk BongoMemChunk;

struct _BongoMemChunk {
    // TODO: review pointers below; not sure the types make sense.
    uint8_t *curptr;
    uint8_t *endptr;

    BongoMemChunk *next;

    uint32_t data[1];
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

#define BONGO_MEMSTACK_ALLOC(stack, size)                                 \
    BONGO_MEMSTACK_ALLOC_ALIGNED(stack, ALIGN_SIZE(size, ALIGNMENT))

#define BONGO_MEMSTACK_ALLOC_ALIGNED(stack, size)                         \
(                                                                         \
    (stack)->chunk->curptr + size > (stack)->chunk->endptr                \
        ? BongoMemStackAlloc((stack), size)                               \
        : ((stack)->chunk->curptr += size, (stack)->chunk->curptr - size) \
)

/** end memstack.c **/

XPL_END_C_LINKAGE

#endif    /* MEMMGR_H */
