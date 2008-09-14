#include "stored.h"
#include "messages.h"
#include "lock.h"

/** Watch stuff **/

typedef struct {
	char *store;
	uint64_t collection;
	StoreClient *watchers[STORE_COLLECTION_MAX_WATCHERS];
} WatchItem;

WatchItem * StoreWatcherFindWatchItem(StoreClient *client, uint64_t collection);

#define MAX_STORE_WATCH	50

static XplMutex global_watch_list_lock;
static WatchItem global_watch_list[MAX_STORE_WATCH];

void
StoreWatcherInit()
{
	XplMutexInit(global_watch_list_lock);
	memset(global_watch_list, 0, sizeof(global_watch_list));
}

WatchItem *
StoreWatcherFindWatchItem(StoreClient *client, uint64_t collection)
{
	int i;
	WatchItem *to_watch = NULL;
	
	for (i = 0; i < MAX_STORE_WATCH; i++) {
	WatchItem *item = &global_watch_list[i];
		if ((item->collection == collection) && (item->store != NULL) && (strcmp(item->store, client->storeName)==0)) {
			// found the watch item for this collection in the right store
			to_watch = item;
		}
		if ((to_watch == NULL) && (item->store == NULL)) {
			// found the first free entry (overridden by above)
			to_watch = item;
		}
	}
	return to_watch;
}

/** \internal
 * Add the client as a watcher of the collection locked by cLock 
 * returns: -1 on failure, 0 o/w
 */

int
StoreWatcherAdd(StoreClient *client, uint64_t collection)
{
	WatchItem *to_watch = NULL;
	StoreClient **watchers = NULL;
    int i;
    int retcode = -1;
    
    XplMutexLock(global_watch_list_lock);
    
    to_watch = StoreWatcherFindWatchItem(client, collection);
    if (to_watch == NULL)
    	// can't find a free entry
    	goto done;
    
    if (to_watch->store == NULL) {
    	// we need to start a new item
    	to_watch->store = strdup(client->storeName);
    	to_watch->collection = collection;
    	memset(to_watch->watchers, 0, sizeof(to_watch->watchers));
	}
	watchers = to_watch->watchers;

    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (watchers[i] == NULL) {
            watchers[i] = client;
            retcode = 0;
            goto done;
        }
    }
    
    // can't find a free watch slot on this entry

done:
	XplMutexUnlock(global_watch_list_lock);
	return retcode;
}

/** \internal
 * Remove the client as a watcher of the collection
 */
int
StoreWatcherRemove(StoreClient *client, uint64_t collection)
{
	WatchItem *to_watch = NULL;
	StoreClient **watchers = NULL;
    int i;
    int retcode = -1;
    
    XplMutexLock(global_watch_list_lock);
    
    to_watch = StoreWatcherFindWatchItem(client, collection);
    if ((to_watch == NULL) || (to_watch->store == NULL))
    	// can't find the entry
    	goto done;
    
	watchers = to_watch->watchers;

    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (watchers[i] == client) {
            watchers[i] = NULL;
            retcode = 0;
            goto done;
        }
    }
    
    // FIXME - remove unused slots from the global list, otherwise we run out!
    
done:
	XplMutexUnlock(global_watch_list_lock);
	return retcode;
}


void
StoreWatcherEvent(StoreClient *thisClient,
                  StoreObject *object,
                  StoreWatchEvents event)
{
	WatchItem *to_watch = NULL;
    StoreClient *client;
    int i;
    
    XplMutexLock(global_watch_list_lock);
    
    to_watch = StoreWatcherFindWatchItem(thisClient, object->collection_guid);
    if ((to_watch == NULL) || (to_watch->store == NULL))
    	// can't find the entry
    	goto done;
                  
    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (to_watch->watchers[i]) {
            client = to_watch->watchers[i];

            if (client != thisClient) {
                if (!(client->watch.flags & event)) {
                    continue;
                }
                if (client->watch.journal.count < STORE_CLIENT_WATCH_JOURNAL_LEN) {
                    client->watch.journal.events[client->watch.journal.count] = event;
                    client->watch.journal.guids[client->watch.journal.count] = object->guid;
                    client->watch.journal.imapuids[client->watch.journal.count] = object->imap_uid;
                    client->watch.journal.flags[client->watch.journal.count] = object->flags;
                }
                ++client->watch.journal.count;
            }
        }
    }

done: 
    XplMutexUnlock(global_watch_list_lock);
}


CCode
StoreShowWatcherEvents(StoreClient *client)
{
    CCode ccode = 0;
    int i;

    ccode = StoreGetCollectionLock(client, &(client->watchLock), client->watch.collection);
    if (ccode) { 
        return ccode;
    }

    if (client->watch.journal.count > STORE_CLIENT_WATCH_JOURNAL_LEN) {
        ccode = ConnWriteStr(client->conn, MSG6000RESET);
    } else {
        for (i = 0; i < client->watch.journal.count && ccode >= 0; i++) {
            int event = client->watch.journal.events[i];
            if (STORE_WATCH_EVENT_FLAGS == event) {
                ccode = ConnWriteF(client->conn, 
                                   "6000 FLAGS " GUID_FMT " %08x %d\r\n",
                                   client->watch.journal.guids[i],
                                   client->watch.journal.imapuids[i],
                                   client->watch.journal.flags[i]);
            } else if (STORE_WATCH_EVENT_COLL_REMOVED == event) {
                ccode = ConnWriteF(client->conn, 
                                   "6000 REMOVED " GUID_FMT "\r\n",
                                   client->watch.collection);
                if (StoreWatcherRemove(client, client->watch.collection)) {
                    ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
                }
            } else if (STORE_WATCH_EVENT_COLL_RENAMED == event) {
                ccode = ConnWriteF(client->conn, 
                                   "6000 RENAMED " GUID_FMT "\r\n",
                                   client->watch.collection);
            } else {                                  
                ccode = ConnWriteF(client->conn, 
                                   "6000 %s " GUID_FMT " %08x\r\n", 
                                   STORE_WATCH_EVENT_DELETED == event ? "DELETED" :
                                   STORE_WATCH_EVENT_MODIFIED == event ? "MODIFIED" :
                                   STORE_WATCH_EVENT_NEW == event ? "NEW" : "ERROR",
                                   client->watch.journal.guids[i],
                                   client->watch.journal.imapuids[i]);
            }
        }
    }

    client->watch.journal.count = 0;

    /* we maintain the read lock until we are done processing the current command */
    // why? StoreDowngradeExclusiveFairLock(&(client->watchLock->fairlock));
    StoreReleaseCollectionLock(client, &(client->watchLock));
    return ccode;
}

#if 0
/* watch event lists */

typedef struct {
    NLockStruct *lock;
    StoreWatchEvents event;
    uint64_t guid;
    uint64_t imapuid;
    uint32_t flags;
} eventdatum;

int 
WatchEventListInit(WatchEventList *events)
{
    return BongoArrayInit(events, sizeof(eventdatum), 32);
}


void
WatchEventListDestroy(WatchEventList *events)
{
    BongoArrayDestroy(events, TRUE);
}


int
WatchEventListAdd(WatchEventList *events, 
                  NLockStruct *lock, StoreWatchEvents event,
                  uint64_t guid, uint32_t imapuid, uint32_t flags)
{
    eventdatum evt;

    evt.lock = lock;
    evt.event = event;
    evt.guid = guid;
    evt.imapuid = imapuid;
    evt.flags = flags;

    BongoArrayAppendValue(events, evt);
    
    return 0;
}


void
WatchEventListFire(WatchEventList *events, StoreClient *client)
{
    unsigned int i;

    for (i = 0; i < events->len; i++) {
        eventdatum evt = BongoArrayIndex(events, eventdatum, i);
        
        StoreWatcherEvent(client, evt.lock, 
                          evt.event, 
                          evt.guid, evt.imapuid, evt.flags);
    }
}
#endif
