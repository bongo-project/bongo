/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005 Novell, Inc. All Rights Reserved.
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

#ifndef INDEX_H
#define INDEX_H

#include "stored.h"

XPL_BEGIN_C_LINKAGE

typedef struct _IndexHandle IndexHandle;    
typedef struct _SearchHandle SearchHandle;

IndexHandle *IndexOpen(StoreClient *client);
void IndexClose(IndexHandle *handle);
void IndexOptimize(IndexHandle *handle);

int IndexDocument(IndexHandle *handle, StoreClient *client, DStoreDocInfo *doc);

int IndexRemoveDocument(IndexHandle *handle, uint64_t guid);

typedef enum {
    INDEX_SORT_RELEVANCE = 0, /* (lucene ranking) */
    INDEX_SORT_GUID,
    INDEX_SORT_DATE_DESC,
    INDEX_SORT_DATE
} IndexSort;

uint64_t *IndexSearch(IndexHandle *handle, 
                      const char *queryStr, 
                      BOOL useConversations,
                      BOOL uniqueResponses,
                      int start, 
                      int end, 
                      int *shown, 
                      int *total,
                      IndexSort sortField);

XPL_END_C_LINKAGE 

#endif
