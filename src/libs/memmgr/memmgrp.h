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

/* Library Definitions    */
#if !defined(offsetof)
#define offsetof(s, m)                  ((size_t)&(((s *)0)->m))
#endif

#define SUFFIX_OFFSET                   offsetof(MemPoolEntry, suffix)

#define POOL128_ALLOC_SIZE              (128)
#define POOL256_ALLOC_SIZE              (256)
#define POOL512_ALLOC_SIZE              (512)
#define POOL1KB_ALLOC_SIZE              (1024)
#define POOL2KB_ALLOC_SIZE              (2 * 1024)
#define POOL4KB_ALLOC_SIZE              (4 * 1024)
#define POOL8KB_ALLOC_SIZE              (8 * 1024)
#define POOL16KB_ALLOC_SIZE             (16 * 1024)
#define POOL32KB_ALLOC_SIZE             (32 * 1024)
#define POOL64KB_ALLOC_SIZE             (64 * 1024)
#define POOL128KB_ALLOC_SIZE            (128 * 1024)
#define POOL256KB_ALLOC_SIZE            (256 * 1024)
#define POOL512KB_ALLOC_SIZE            (512 * 1024)
#define POOL1MB_ALLOC_SIZE              (1024 * 1024)
#define POOL2MB_ALLOC_SIZE              (2 * 1024 * 1024)
#define POOL4MB_ALLOC_SIZE              (4 * 1024 * 1024)

/*                                                1111111111222222222233333333334444444444555555555566666666667777777777
                                         1234567890123456789012345678901234567890123456789012345678901234567890123456789     */

#define LIBMSGREADY                     "Bongo Memmory Manager Ready\r\n"
#define LIBMSGREADY_LEN                 31

#define LIBMSGBYE                       "Bongo Memmory Manager Signing Off\r\n"
#define LIBMSGBYE_LEN                   37

#define LIBMSGBADCOMMAND                "NO: Invalid command.\r\n"
#define LIBMSGBADCOMMAND_LEN            22

#define LIBMSGALLOCFAILED               "NO: Allocation request failed.\r\n"
#define LIBMSGALLOCFAILED_LEN           32

#define LIBMSGACTION                    "OK: Action performed.\r\n"
#define LIBMSGACTION_LEN                23

/* Can't do LIBMSGHELP_LEN because of VCS revision insertion */
#if defined(MEMMGR_DEBUG)
#if defined(NETWARE) || defined(LIBC)
#define LIBMSGHELP                      "Bongo Memory Manager $Revision: 1.2 $ Help\r\n"                                         \
                                        "--------------------------------------------------------------------------------\r\n"      \
                                        "CALLOC    CONFIG    ENTDBG    FREE      MALLOC    POOL      POOLS     QUIT\r\n"            \
                                        "REALLOC\r\n"                                                                               \
                                        "--------------------------------------------------------------------------------\r\n"
#else        /*    !defined(NETWARE) || defined(LIBC)    */
#define LIBMSGHELP                      "Bongo Memory Manager $Revision: 1.2 $ Help\r\n"                                         \
                                        "--------------------------------------------------------------------------------\r\n"      \
                                        "CALLOC    CONFIG    FREE      MALLOC    POOL      POOLS     QUIT      REALLOC   \r\n"      \
                                        "--------------------------------------------------------------------------------\r\n"
#endif  /*    defined(NETWARE) || defined(LIBC)    */
#else   /*    defined(MEMMGR_DEBUG)    */
#define LIBMSGHELP                      "Bongo Memory Manager $Revision: 1.2 $ Help\r\n"                                         \
                                        "--------------------------------------------------------------------------------\r\n"      \
                                        "CONFIG    POOL      POOLS     QUIT\r\n"                                                    \
                                        "--------------------------------------------------------------------------------\r\n"
#endif  /*    defined(MEMMGR_DEBUG)    */

#define LIBMSGHELPENTDBG                "HELP: ENTDBG - Enter the NetWare Debugger.\r\n"
#define LIBMSGHELPENTDBG_LEN            44

#define LIBMSGHELPCONFIG                "HELP: CONFIG - Return current library configuration.\r\n"
#define LIBMSGHELPCONFIG_LEN            54

/*                                                1111111111222222222233333333334444444444555555555566666666667777777777
                                         1234567890123456789012345678901234567890123456789012345678901234567890123456789     */
#define LIBMSGHELPPOOL                  "HELP: POOL <IDENT> - Return current state for the pool matching IDENT.\r\n"
#define LIBMSGHELPPOOL_LEN              72

#define LIBMSGHELPPOOLS                 "HELP: POOLS - Return current state for all pools.\r\n"
#define LIBMSGHELPPOOLS_LEN             51

#define LIBMSGHELPQUIT                  "HELP: QUIT - Close session.\r\n"
#define LIBMSGHELPQUIT_LEN              29

#define LIBMSGHELPCALLOC                "HELP: CALLOC <COUNT> <SIZE> - Allocate and memset COUNT * SIZE octets to 0.\r\n"
#define LIBMSGHELPCALLOC_LEN            77

#define LIBMSGHELPFREE                  "HELP: FREE - Release last successful allocation.\r\n"
#define LIBMSGHELPFREE_LEN              50

#define LIBMSGHELPMALLOC                "HELP: MALLOC <COUNT> - Allocate COUNT octets.\r\n"
#define LIBMSGHELPMALLOC_LEN            47

#define LIBMSGHELPREALLOC               "HELP: REALLOC <COUNT> - Reallocate previous allocation to COUNT octets.\r\n"
#define LIBMSGHELPREALLOC_LEN           73

#define LIBMSGHELPSTRDUP                "HELP: STRDUP <STRING> - Allocate octets to store STRING.\r\n"
#define LIBMSGHELPSTRDUP_LEN            58

#define LIBMSGHELPUNKNOWN               "ERR: Unknown command \"%s\".\r\n"

#define PENTRY_FLAG_NON_POOLABLE        (1 << 0)
#define PENTRY_FLAG_EXTREME_ENTRY       (1 << 1)
#define PENTRY_FLAG_PRIVATE_ENTRY       (1 << 2)
#define PENTRY_FLAG_INUSE               (1 << 3)
#define PENTRY_FLAG_INDIRECT_ALLOC      (1 << 4)

#define PCONTROL_FLAG_DYNAMIC           (1 << 0)
#define PCONTROL_FLAG_CONFIG_CHANGED    (1 << 1)
#define PCONTROL_FLAG_CONFIG_SAVED      (1 << 2)
#define PCONTROL_FLAG_INITIALIZED       (1 << 3)
#define PCONTROL_FLAG_DO_NOT_SAVE       (1 << 4)

#define PENTRY_FLAGS_ACCEPTABLE_FREE    (PENTRY_FLAG_NON_POOLABLE | PENTRY_FLAG_EXTREME_ENTRY | PENTRY_FLAG_PRIVATE_ENTRY | PENTRY_FLAG_INDIRECT_ALLOC)

/*    Library Structures    */
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

    union {
        double dummy;                       /* Force appropriate alignment */
        unsigned char suffix[4];            /* Base pointer for allocations */
    };
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

#if defined(MEMMGR_DEBUG)
#define MEMMGR_FLAG_REPORT_LEAKS (1 << 0)
#else
#define MEMMGR_FLAG_REPORT_LEAKS (0)
#endif

#endif /* MEMMGRP_H */
