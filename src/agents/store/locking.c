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


void
LogicalLockInit()
{
	XplMutexInit(logicallock_global);
}

/* Acquire the desired logical lock. 
 * TODO: what if we already hold an equivalent lock? promotion / etc.?
 * Return 0 on failure
 */
int
LogicalLockGain(StoreClient *client, StoreObject *object, LogicalLockType type)
{
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(object);
	UNUSED_PARAMETER_REFACTOR(type);
	
	XplMutexLock(logicallock_global);
	
	return 1;
}

/* Free the desired logical lock.
 * One way or another, this function must succeed!
 * It's not an error to call this on an object already unlocked?
 */
void
LogicalLockRelease(StoreClient *client, StoreObject *object)
{
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(object);
	
	XplMutexUnlock(logicallock_global);
}
