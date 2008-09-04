
#include <xpl.h>
#include <memmgr.h>
#include <msgapi.h>

#include "stored.h"

/**
 * /file Functions for pooling DB connections.
 * 
 * "Pooling" is possibly the wrong word here. What we intend is that any
 * thread accessing a certain user's store gets exactly the same SQLite handle, 
 * so that our mutexes etc. are effective. We also want to keep re-using
 * handles so we're not opening/closing SQLite DBs constantly, since 
 * that also takes time.
 * 
 * This is really more like a Factory pattern or some kind of reference
 * counter, because we do want to close SQLite DB handles when they're 
 * no longer being actively used.
 * 
 * So, once a store is opened, it's placed in the pool but also given to
 * the client. If another client comes along, we also give them that handle.
 * When the client is done and closes the handle, that actually doesn't 
 * close anything, and the handle remains in the pool for the next client
 * that comes along (unless it gets reaped before then for being idle
 * for too long).
 */

typedef struct _DBPoolEntry {
	// name of the store this handle is for
	char *user;
	// a handle to SQLite
	MsgSQLHandle *handle;
	// how many people are currently using this handle
	int clients;
	// when this handle was last given up
	uint64_t lastAccessTime;
} DBPoolEntry;

static void
DBPoolEntryDelete(DBPoolEntry *entry)
{
	MemFree(entry->user);
	MemFree(entry);
}

int
DBPoolInit()
{
	StoreAgent.dbpool.entries = BongoCreateStringHashTable(64);
	if (!StoreAgent.dbpool.entries) {
		return -1;
	}
	XplMutexInit(StoreAgent.dbpool.lock);
	
	return 0;
}

/**
 * Open the Store database for this user. Updates the storedb handle of
 * the StoreClient to contain an SQLite handle to the Store.
 * \param	client	storeclient we're working for
 * \param	user	name of the store (usually, the username)
 * \return	0 on success, error codes otherwise
 */
int
StoreDBOpen(StoreClient *client, const char *user)
{
	char path[XPL_MAX_PATH +1];
	DBPoolEntry *entry = NULL;

	// try to find the handle in the pool - first, lock the pool
	XplMutexLock(StoreAgent.dbpool.lock); 
	{
		// find the entry
		entry = (DBPoolEntry *)
			BongoHashtableGet(StoreAgent.dbpool.entries, user);
		
		if (entry == NULL) {
			// no such entry, need to create one - we
			// do it here while locked
			entry = MemNew0(DBPoolEntry, 1);
			entry->user = MemStrdup(user);
			entry->clients = 0;
			
			if (BongoHashtablePutNoReplace(StoreAgent.dbpool.entries, MemStrdup(user), (void *)entry)) {
				// couldn't insert the new entry for whatever reason :(
				DBPoolEntryDelete(entry);
				goto db_fail;
			}
			
			// this bit is slow, but I can't think how we could
			// do this outside the dbpool.lock without being racy :(
			snprintf(path, XPL_MAX_PATH, "%s/%s/store.db", StoreAgent.store.rootDir, user);
			entry->handle = MsgSQLOpen(path, &client->memstack, 3000);
			if (entry->handle == NULL) {
				BongoHashtableRemove(StoreAgent.dbpool.entries, (void *)user);
				DBPoolEntryDelete(entry);
				goto db_fail;
			}
		}
		entry->clients++;
	}
	// unlock the pool on success
	XplMutexUnlock(StoreAgent.dbpool.lock);
	
	client->storedb = entry->handle;
	
	return 0;

db_fail:
	// unlock the pool on failure
	XplMutexUnlock(StoreAgent.dbpool.lock);
	return -1;
}

void
StoreDBClose(StoreClient *client)
{
	if ((client == NULL) || (client->storedb == NULL) || (client->storeName == NULL)) {
		// no store open; nothing to close?
		return;
	}
	
	XplMutexLock(StoreAgent.dbpool.lock); 
	{
		DBPoolEntry *entry = NULL;
		
		// find the entry
		entry = (DBPoolEntry *)
			BongoHashtableGet(StoreAgent.dbpool.entries, client->storeName);
		
		if (entry == NULL) 
			// very odd error; shouldn't happen
			goto unlock;
		
		entry->clients--;
		if (entry->clients < 0) entry->clients = 0;
		entry->lastAccessTime = time(NULL);
		
		// FIXME: perhaps we want some kind of reaping system in here.
		// Want to remove entries with lastAccessTime set in the past (10mn?)
		// and where clients == 0.
		/*
		if (client->storedb != NULL) {
			MsgSQLClose(client->storedb);
			client->storedb = NULL;
		}
		*/
	}
unlock:
	XplMutexUnlock(StoreAgent.dbpool.lock);
}
