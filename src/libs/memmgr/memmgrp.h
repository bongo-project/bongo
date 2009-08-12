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

#ifndef MEMMGRP_H
#define MEMMGRP_H

#include <memmgr.h>

/*    Library Structures    */
#if 0
typedef struct _MemPoolEntry {
    struct _MemPoolEntry *next;         /* Next pooled entry */
    struct _MemPoolEntry *previous;     /* Previous pooled entry */

    struct _MemPoolControl *control;    /* Owned by pool */

    unsigned long flags;                /* Pooled entry flags */

    unsigned long size;                 /* Size requested by caller */
    unsigned long allocSize;            /* Original allocation size (for extreme alloc's) */

    struct {
        unsigned char file[128];        /* Source code file */
        unsigned long line;             /* Source code line */
    } source;

} MemPoolEntry;

typedef struct _MemPoolControl {
    struct _MemPoolControl *next;       /* Next speciality pool */
    struct _MemPoolControl *previous;   /* Previous speciality pool */

    MemPoolEntry *free;                 /* List available pooled entries */
    MemPoolEntry *inUse;                /* Head of the pools in use list */

    XplSemaphore sem;                   /* Pool semaphore */

#if defined(LIBC)
    rtag_t resourceTag;                 /* Pool allocation resource tag */
#endif

    unsigned long flags;                /* Flags */
    unsigned long defaultFlags;         /* Default flags for new entries */

    size_t allocSize;                   /* Pool allocation size (0 for the extreme pool) */

    PoolEntryCB allocCB;                /* Pool entry allocation callback */
    PoolEntryCB freeCB;                 /* Pool entry free callback */

    void *clientData;                   /* Client data for callbacks */

    struct {
        unsigned int minimum;           /* Minimum pooled allocations */
        unsigned int maximum;           /* Maximum pooled allocations */

        unsigned int allocated;         /* Current allocation count */
    } eCount;

    unsigned char *name;

    struct {
        unsigned long pitches;          /* Requests to pull from the pool */
        unsigned long strikes;          /* Failed attempts to pull from the pool */
    } stats;

    struct {                            /* Base allocation pointers for the pool */
        MemPoolEntry *start;
        MemPoolEntry *end;
    } base;
} MemPoolControl;
#endif

#if defined(MEMMGR_DEBUG)
#define MEMMGR_FLAG_REPORT_LEAKS (1 << 0)
#else
#define MEMMGR_FLAG_REPORT_LEAKS (0)
#endif

#endif /* MEMMGRP_H */
