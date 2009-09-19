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

int
LogicalLockGain(StoreClient *client, StoreObject *object, LogicalLockType type)
{
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(object);
	UNUSED_PARAMETER_REFACTOR(type);
	
	return 0;
}

int
LogicalLockRelease(StoreClient *client, StoreObject *object)
{
	UNUSED_PARAMETER_REFACTOR(client);
	UNUSED_PARAMETER_REFACTOR(object);
	
	return 0;
}
