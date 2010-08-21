/* Internal Store-level locking
 * This provides read-only (shared) and read-write (exclusive) locking
 * at a logical level within the store.
 * 
 * Locks are held on the internal path within the store, e.g. on /mail,
 * rather than on GUIDs. This allows locks to conflict with each other,
 * e.g. /mail/INBOX cannot be taken if /mail is locked, and vice-versa
 * 
 * Where locks are not immediately available the request is then queued.
 */

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <time.h>
#include <unistd.h>
#include <stdio.h>

#include "stored.h"

static XplMutex logicallock_global;
static GHashTable *storelocks_global;

typedef struct {
	char *		store;
	XplMutex	lock;
	XplThreadID	thread;
	LogicalLockType	type;
} StoreLock;

/* FIXME: do i need to worry about the mutex being locked? */
void
pvt_MemFree(void *data) {
	StoreLock *sl=data;

	XplMutexDestroy(sl->lock);
	MemFree(sl->store);
	MemFree(data);
}

void
LogicalLockInit()
{
	XplMutexInit(logicallock_global);

	/* go ahead and initialize the hash table now */
	storelocks_global = g_hash_table_new_full(g_str_hash, g_str_equal, NULL, pvt_MemFree);
}

/* Acquire the desired logical lock. 
 * TODO: what if we already hold an equivalent lock? promotion / etc.?
 * Return 0 on failure
 */
int
LogicalLockGain(StoreClient *client, StoreObject *object, LogicalLockType type, const char *location)
{
	/* this has to be on the heap since it has to be valid across threads */
	StoreLock *sl;
	gpointer orig_key;
	gpointer sl_tmp;

	UNUSED_PARAMETER(location);
	UNUSED_PARAMETER(object);

	Log(LOG_DEBUG, "%lu asking for lock on %s in %s", XplGetThreadID(), client->storeName, location);

	XplMutexLock(logicallock_global);
	if (!g_hash_table_lookup_extended(storelocks_global, client->storeName, &orig_key, &sl_tmp)) {
		/* it must not exist, create it first.  i can lock it too since this is new. */
		sl = MemMalloc(sizeof(StoreLock));
		if (!sl) {
			XplMutexUnlock(logicallock_global);
			return 0;
		}
		XplMutexInit(sl->lock);
		XplMutexLock(sl->lock);
		sl->thread=XplGetThreadID();
		sl->store=MemStrdup(client->storeName);
		sl->type=type;

		g_hash_table_insert(storelocks_global, sl->store, sl);

		/* unlock the global */
		XplMutexUnlock(logicallock_global);
	} else {
		sl=(StoreLock *)sl_tmp;

		/* unlock the global so we don't deadlock */
		XplMutexUnlock(logicallock_global);

		XplMutexLock(sl->lock);
		sl->thread = XplGetThreadID();
		sl->type = type;

		/* note: the store name is already set.  we don't need to re-set it */
	}

	Log(LOG_DEBUG, "%lu gained lock on %s in %s", XplGetThreadID(), client->storeName, location);
	return 1;
}

void
printfunc(gpointer key, gpointer value, gpointer user_data) {
	StoreLock *sl=value;

	Log(LOG_DEBUG, "lock table: LOCK on %s by %lu (%s)", key, sl->thread, (char *)user_data);
}

/* Free the desired logical lock.
 * One way or another, this function must succeed!
 * It's not an error to call this on an object already unlocked?
 */
void
LogicalLockRelease(StoreClient *client, StoreObject *object, LogicalLockType type, const char *location)
{
	StoreLock *sl;
	gpointer orig_key;
	gpointer sl_tmp;

	UNUSED_PARAMETER(object);
	UNUSED_PARAMETER(type);

	Log(LOG_DEBUG, "%lu asking to unlock %s in %s", XplGetThreadID(), client->storeName, location);
	XplMutexLock(logicallock_global);

	if (g_hash_table_lookup_extended(storelocks_global, client->storeName, &orig_key, &sl_tmp)) {
		sl=(StoreLock *)sl_tmp;

		sl->thread = 0;
		sl->type = LLOCK_NONE;
		XplMutexUnlock(sl->lock);
		Log(LOG_DEBUG, "%lu released lock on %s in %s", XplGetThreadID(), client->storeName, location);
	} else {
		Log(LOG_DEBUG, "Release Failed for %s by %lu in %s.  Printing table", client->storeName, XplGetThreadID(), location);
		g_hash_table_foreach(storelocks_global, printfunc, (char *)location);
	}

	XplMutexUnlock(logicallock_global);
}

/* Free all the store locks that are around */
void
LogicalLockDestroy() {
	XplMutexLock(logicallock_global);

	g_hash_table_destroy(storelocks_global);

	XplMutexUnlock(logicallock_global);
	XplMutexDestroy(logicallock_global);
}
