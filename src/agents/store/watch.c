#include "stored.h"
#include "messages.h"
#include "lock.h"

/** Watch stuff **/


/** \internal
 * Add the client as a watcher of the collection locked by cLock 
 * returns: -1 on failure, 0 o/w
 */

int
StoreWatcherAdd(StoreClient *client,
                NLockStruct *cLock)
{
    int i;

    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (!cLock->watchers[i]) {
            cLock->watchers[i] = client;
            return 0;
        }
    }
    return -1;
}

/** \internal
 * Remove the client as a watcher of the collection
 */
int
StoreWatcherRemove(StoreClient *client,
                   NLockStruct *cLock)
{
    int i;

    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (client == cLock->watchers[i]) {
            cLock->watchers[i] = NULL;
            client->watch.collection = 0;
            return 0;
        }
    }
    return -1;
}


void
StoreWatcherEvent(StoreClient *thisClient,  /* can be NULL */
                  NLockStruct *cLock,
                  StoreWatchEvents event,
                  uint64_t guid,
                  uint32_t imapuid,
                  uint32_t flags)
{
    int i;
    StoreClient *client;

    if (!imapuid) {
        imapuid = guid;
    }
                  
    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (cLock->watchers[i]) {
            client = cLock->watchers[i];

            if (client != thisClient) {
                if (!(client->watch.flags & event)) {
                    continue;
                }
                if (client->watch.journal.count < STORE_CLIENT_WATCH_JOURNAL_LEN) {
                    client->watch.journal.events[client->watch.journal.count] = event;
                    client->watch.journal.guids[client->watch.journal.count] = guid;
                    client->watch.journal.imapuids[client->watch.journal.count] = imapuid;
                    client->watch.journal.flags[client->watch.journal.count] = flags;
                }
                ++client->watch.journal.count;
            }
        }
    }
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
                if (StoreWatcherRemove(client, client->watchLock)) {
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
