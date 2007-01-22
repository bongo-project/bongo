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

#include <ctype.h>
#include <mdb.h>
#include <logger.h>

#include <connmgr.h>

#define PRODUCT_SHORT_NAME  "cmlist.nlm"
#define PRODUCT_NAME        "Bongo Connection Manager Block and Allow List Module"

typedef struct _ListGlobal {
    /* Handles */
    void *logHandle;
    MDBHandle directoryHandle;

    /* Module State */	
    time_t timeStamp;
    int tgid;
    BOOL unloadOK;
    XplSemaphore shutdownSemaphore;
    XplAtomic threadCount;

    struct {
        long last;

        unsigned char datadir[XPL_MAX_PATH + 1];
    } config;

    struct {
        long *start;
        long *end;
        unsigned long count;
        unsigned long allocated;
    } blocked;

    struct {
        long *start;
        long *end;
        unsigned long count;
        unsigned long allocated;
    } allowed;
} ListGlobal;

extern ListGlobal List;

/* cmlist.c */
void ListShutdownSigHandler(int Signal);
int _NonAppCheckUnload(void);

EXPORT BOOL CMLISTSInit(CMModuleRegistrationStruct *registration, unsigned char *dataDirectory);
EXPORT BOOL ListsShutdown(void);
EXPORT int ListsVerify(ConnMgrCommand *command, ConnMgrResult *result);

