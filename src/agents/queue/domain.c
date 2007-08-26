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

#include <config.h>
#include <xpl.h>
#include <logger.h>

#include "domain.h"

int 
QDBStartup(int minimum, int maximum)
{
    return 0;
}

void 
QDBShutdown(void)
{
}

void *
QDBHandleAlloc(void)
{
    return NULL;
}

void 
QDBHandleRelease(void *handle)
{
}

int 
QDBAdd(void *handle, unsigned char *domain, unsigned long queueID, int queue)
{
    return -1;
}

int 
QDBRemoveID(void *handle, unsigned long queueID)
{
    return -1;
}

int 
QDBRemoveDomain(void *handle, unsigned char *domain)
{
    return -1;
}

int 
QDBSearchID(void *handle, unsigned long queueID)
{
    return -1;
}

int 
QDBSearchDomain(void *handle, unsigned char *domain)
{
    return -1;
}

int 
QDBQuery(void *handle, unsigned char *query, QDBQueryResults *results)
{
    return -1;
}

int 
QDBDump(unsigned char *domain)
{
    return -1;
}

void
QDBSummarizeQueue(void)
{
}