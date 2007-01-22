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

#include <config.h>

#include "stored.h"
#include "limits.h"
#include "index.h"
#include "lucene-index.h"
#include "filters/filter.h"
#include <assert.h>

XPL_BEGIN_C_LINKAGE

/* How far past the last requested page to search */
#define SEARCH_EXTRA_HITS 200
  
struct _IndexHandle {
    LuceneIndex *index;
    NLockStruct *indexLock;
    DStoreHandle *dbHandle;
};

struct _SearchHandle {
    Hits *hits;
    int i;
    int start;
    int end;
};
  
IndexHandle *
IndexOpen(StoreClient *client)
{
    IndexHandle *handle = (IndexHandle*)MemMalloc(sizeof(IndexHandle));
    
    // FIXME: abusing a dummy guid for an index lock.  We really want
    // to rely on CLucene locking for finer-grained control, but for
    // right now we'll just use a big dumb lock.

    handle->indexLock = NULL;
    if (NLOCK_ACQUIRED != ReadNLockAcquire(&handle->indexLock, &client->storeHash,
                                           (unsigned char *) client->store,
                                           STORE_DUMMY_INDEXER_GUID, 3000))
    {
        return NULL;
    }
    if (NLOCK_ACQUIRED != PurgeNLockAcquire(handle->indexLock, 3000)) {
        ReadNLockRelease(handle->indexLock);
        return NULL;
    }

    handle->index = new LuceneIndex();
    handle->dbHandle = client->handle;

    char path[XPL_MAX_PATH];
    snprintf(path, sizeof(path), "%sindex", client->store);    
    handle->index->Init(path);
    
    return handle;
}

void
IndexClose(IndexHandle *handle)
{
    delete handle->index;    
    PurgeNLockRelease(handle->indexLock);
    ReadNLockRelease(handle->indexLock);

    MemFree(handle);
}


void
IndexOptimize(IndexHandle *handle)
{
    handle->index->GetIndexWriter()->optimize();
}


int
IndexRemoveDocument(IndexHandle *handle, uint64_t guid)
{
    char buf[CONN_BUFSIZE + 1];
    TCHAR tbuf[CONN_BUFSIZE + 1];

    snprintf(buf, sizeof(buf), "%016llx", guid);
    lucene_utf8towcs(tbuf, buf, CONN_BUFSIZE);
    Term *t = new Term(_T("nmap.guid"), tbuf);

    int ret;
    try {
        handle->index->GetIndexReader()->deleteTerm(t);
        ret = 0;
    } catch (...) {
        ret = 1;
    }

    delete t;

    return ret;
}

int
IndexDocument(IndexHandle *handle, StoreClient *client, DStoreDocInfo *info)
{
    if (IndexRemoveDocument(handle, info->guid)) {
        return 1;
    }
    
    return FilterDocument(client, info, handle->index);
}

int
IndexSearchTotalDocuments(IndexHandle *handle, SearchHandle *search)
{
    return search->hits->length();
}

static int
GetGuid(Document *doc, const TCHAR *field, uint64_t *guid)
{
    TCHAR tbuf[CONN_BUFSIZE];
    char guidstr[64];

    const TCHAR *v = doc->get(field);
    if (v == NULL) {
        return 1;
    }
    
    lucene_wcstoutf8 (guidstr, v, 64);
    
    char *endp;
    
    *guid = HexToUInt64(guidstr, &endp);
    if (*endp || 0 == *guid) {
        return 1;
    }

    return 0;
}

static int 
DocumentGetGuid(Document *doc, bool useConversation, uint64_t *guid)
{
    if (useConversation) {
        if (GetGuid(doc, _T("nmap.conversation"), guid) == 0) {
            return 0;
        }
    }
    
    return GetGuid(doc, _T("nmap.guid"), guid);
}


static Query *
GetQuery(IndexHandle *handle, const char *queryStr)
{
    TCHAR tbuf[CONN_BUFSIZE];

    QueryParser *parser = handle->index->GetQueryParser();
    
    lucene_utf8towcs(tbuf, queryStr, sizeof(tbuf));
    try {
        Query *query = parser->parse(tbuf);
        return query;
    } catch (...) {
        return NULL;
    }
}

static Hits *
RunQuery(IndexHandle *handle, Query *query, Sort *sort)
{
    Hits *hits;
    try {
        hits = handle->index->GetIndexSearcher()->search(query, sort);
    } catch (...) {
        return NULL;
    }

    return hits;
}


static uint32_t 
HashGuidPtr(const void *key)
{
    uint64_t val = *((uint64_t*)key);
    
    return (uint32_t)val;
}

static int
CompareGuidPtr(const void *keya, const void *keyb)
{
    uint64_t vala = *((uint64_t*)keya);
    uint64_t valb = *((uint64_t*)keyb);

    uint64_t result = (vala - valb);
    if (result == 0) {
        return 0;
    } else if (result > 0) {
        return 1;
    } else {
        return -1;
    }
}

typedef struct {
    BongoHashtable *table;
    uint64_t *guids;
    int count;
} GuidSet;

static void
GuidSetInit(GuidSet *set, int max) 
{
    set->table = BongoHashtableCreate(100, HashGuidPtr, CompareGuidPtr);
    set->guids = (uint64_t*)MemMalloc(max * sizeof(uint64_t));
    set->count = 0;
    set->guids[0] = 0;
}

static BOOL
GuidSetHas(GuidSet *set, uint64_t guid)
{
    return (BongoHashtableGet(set->table, &guid) != NULL);
}

static BOOL
GuidSetAdd(GuidSet *set, uint64_t guid)
{
    set->guids[set->count] = guid;
    BongoHashtablePut(set->table, (set->guids + set->count++), XPL_INT_TO_PTR(1));
    set->guids[set->count] = 0;
}

static uint64_t *
GuidSetDelete(GuidSet *set, int returnGuids)
{
    BongoHashtableDelete(set->table);
    if (returnGuids) {
        return set->guids;
    }
    MemFree(set->guids);
    return NULL;
}



static uint64_t *
DoSearchRaw(DStoreHandle *dbHandle, 
            BOOL returnConversations, 
            Hits *hits, 
            int start, 
            int end, 
            int *shown, 
            int *total)
{
    int len;
    int numGuids = 0;
    uint64_t *guids;

    guids = (uint64_t *) MemMalloc(STORE_GUID_LIST_MAX * sizeof(uint64_t));
    if (!guids) {
        return NULL;
    }

    len = hits->length();

    if (total) {
        *total = len;
    }
    
    if (start == -1) {
        start = 0;
        end = len - 1;
    }

    for (int i = start; 
         i <= end && i < len && numGuids < (STORE_GUID_LIST_MAX - 1); 
         i++) 
    {
        Document *doc;
        uint64_t guid;
        
        try {
            doc = &hits->doc(i);
        } catch (...) {
            return NULL;
        }
    
        if (DocumentGetGuid(doc, returnConversations, &guid)) {
            return NULL;
        }

        guids[numGuids++] = guid;
    }

    if (shown) {
        *shown = numGuids;
    }

    guids[numGuids] = 0;

    return guids;
}

static uint64_t *
DoSearchUnique(DStoreHandle *dbHandle, 
               BOOL useConversations, 
               Hits *hits, 
               int start, 
               int end, 
               int *shown, 
               int *total)
{
    int i;
    int len;
    int maxTotal;
    int displayed;
    GuidSet set;

    len = hits->length();

    if (start == -1) {
        start = 0;
        end = len - 1;
    }

    /* We will be reading past the requested page to get a
     * pseudo-total */
    maxTotal = end + SEARCH_EXTRA_HITS;
    if (maxTotal > len) {
        maxTotal = len;
    }
    GuidSetInit(&set, maxTotal + 1);
    
    /* Read maxRead unique guids, storing the requested ones in requestGuids */

    for (i = 0; set.count < maxTotal && i < len; i++) {
        Document *doc;
        uint64_t guid;
        
        try {
            doc = &hits->doc(i);
        } catch (...) {
            goto abort;
        }
    
        if (DocumentGetGuid(doc, useConversations, &guid)) {
            goto abort;
        }

        if (GuidSetHas(&set, guid)) {
            continue;
        }

        GuidSetAdd(&set, guid);
    }

    if (start > set.count) {
        start = set.count - 1;
        displayed = 0;
    } else {
        if (set.count - 1 < end) {
            end = set.count - 1;
        }
        
        displayed = (end - start) + 1;
        if (displayed > STORE_GUID_LIST_MAX) {
            displayed = STORE_GUID_LIST_MAX;
        }
    }
    
    if (shown) {
        *shown = displayed;
    }

    if (total) {
        *total = set.count;
    }

    return GuidSetDelete(&set, 1);

abort:
    GuidSetDelete(&set, 0);
    return NULL;
}

uint64_t *
IndexSearch(IndexHandle *handle, 
            const char *queryStr, 
            BOOL useConversations,
            BOOL uniqueResponses,
            int start, 
            int end, 
            int *shown, 
            int *total,
            IndexSort sortField)
{
    uint64_t *ret = NULL;

    assert (end - start <= STORE_GUID_LIST_MAX);

    Query *query = GetQuery(handle, queryStr);
    if (!query) {
        return NULL;
    }
    
    Sort *sort;

    switch (sortField) {
    case INDEX_SORT_GUID:
        sort = new Sort(_T("nmap.guid"), false);
        break;
    case INDEX_SORT_DATE_DESC:
        sort = new Sort(_T("nmap.sort"), true);
        break;
    case INDEX_SORT_DATE:
        sort = new Sort(_T("nmap.sort"), false);
        break;
    case INDEX_SORT_RELEVANCE:
    default:
        sort = NULL;
    }

    Hits *hits = RunQuery(handle, query, sort);
    if (!hits) {
        _CLDELETE(query);
        if (sort) {
            delete sort;
        }
        return NULL;
    }

    if (uniqueResponses) {
        ret = DoSearchUnique(handle->dbHandle, useConversations, hits, start, end, shown, total);
    } else {
        ret = DoSearchRaw(handle->dbHandle, useConversations, hits, start, end, shown, total);
    }
    
    _CLDELETE(hits);
    _CLDELETE(query);
    if (sort) {
        delete sort;
    }

    return ret;
}


XPL_END_C_LINKAGE
