/** \file 
 * An implementation of locks which governs order of access via a FIFO queue
 */

#include "stored.h"
#include "lock.h"

NFairLockPool *lockpool;

/**
 * Initialize the pool of fair locks. Needs to be called once per program
 * in order for fair locks to be available
 */
void
StoreInitializeFairLocks()
{
	lockpool = MemMalloc(sizeof(NFairLockPool));
	XplOpenLocalSemaphore(lockpool->lock, SEM_UNLOCKED);
	lockpool->first = NULL;
	lockpool->last = NULL;
}

/** \internal
 * Create a string name for a store resource, used for find a specific lock
 * for that resource
 * \param	name	Usually the name of a store the resource is in
 * \param	guid	GUID of the resource within that store
 * \return		A string name unique to that resource
 */
char *
FairLockName(char *name, uint64_t guid)
{
	char *result;
	int result_len;
	
	if (name == NULL) name = "";
	
	result_len = strlen(name) + 33;
	result = MemMalloc(result_len);
	
	snprintf(result, result_len, "%s-" GUID_FMT, name, guid);
	
	return result;
}

/** \internal
 * Retrieve a lock for a resource from a global pool of locks. This doesn't ascertain
 * or set the actual status of that lock, it simply finds a reference to it.
 * \bug A long list is a bad implementation, we want a big hash of short lists for quick access
 * \bug Should we retire locks who haven't been taken in a while while we're looking?
 * \param	store	Usually the name of the store the resource resides.
 * \param	guid	The GUID of the resource within that store
 * \return		A fair lock governing access to that resource
 */
NFairLock *
FairLockFind(char *store, uint64_t guid)
{
	NFairLockList *search;
	NFairLock *result;
	char *name;
	//char *new_lock = "";
	
	name = FairLockName(store, guid);
	XplWaitOnLocalSemaphore(lockpool->lock);
	
	// look for our lock
	result = NULL;
	search = lockpool->first;
	while ((search != NULL) && (result == NULL)) {
		if (strcmp(search->lock->lockname, name) == 0) {
			result = search->lock;
		}
		search = search->next;
	}
	
	if (result == NULL) {
		// couldn't find the lock, so add a new one
		NFairLockList *new_entry;
		//new_lock = "new ";
		
		new_entry = MemMalloc(sizeof(NFairLockList));
		result = MemMalloc(sizeof(NFairLock));
		
		new_entry->next = NULL;
		new_entry->lock = result;
		
		result->lockname = MemMalloc(strlen(name)+1);
		result->readers = 0;
		result->writer = 0;
		result->queue = NULL;
		XplOpenLocalSemaphore(result->access, SEM_UNLOCKED);
		strcpy(result->lockname, name);
		
		if (lockpool->first != NULL) {
			// append to existing list
			lockpool->last->next = new_entry;
			lockpool->last = new_entry;
		} else {
			// start a new list
			lockpool->first = new_entry;
			lockpool->last = new_entry;
		}
	}
	
	XplSignalLocalSemaphore(lockpool->lock);
	MemFree(name);
	return result;
}

/** \internal
 * Places a ticket in the queue for a lock. Usually called when someone already
 * has the lock, and the process wishes to queue up for when it's free again.
 * \param	lock	Fair lock we want access to
 * \param	is_writer	Whether or not we desire write access to the resource
 * \return		A ticket which has been placed in the queue for the lock
 */
NFairLockQueueEntry *
FairLockNewQueueEntry (NFairLock *lock, BOOL is_writer)
{
	NFairLockQueueEntry *result;
	
	result = MemMalloc(sizeof(NFairLockQueueEntry));
	if (result == NULL) return NULL;
	
	result->is_writer = is_writer;
	result->next = NULL;
	XplOpenLocalSemaphore(result->signal, SEM_LOCKED);
	
	if (lock->queue == NULL) {
		// first entry in the queue
		lock->queue = result;
	} else {
		// find the end of the queue
		NFairLockQueueEntry *list = lock->queue;
		
		while(list->next != NULL) { list = list->next; }
		list->next = result;
	}
	return (result);
}

/** \internal
 * Remove a ticket from the from of the queue for a lock. This is called 
 * by the process holding the ticket after it has acquired the lock, 
 * since the presence of the queue on the lock prevents other threads
 * stealing the lock.
 * The ticket itself isn't provided, we assume it's simply at the front
 * of the queue.
 * \bug Is the above assumption right?
 * \param	lock	Fair lock whose queue the ticket was in.
 */
void
FairLockRemoveQueueHead (NFairLock *lock)
{
	NFairLockQueueEntry *entry;
	
	if (lock->queue == NULL) return; // no queue to alter?
	entry = lock->queue;
	
	lock->queue = entry->next;
	XplCloseLocalSemaphore(entry->signal);
	MemFree(entry);
}

/** \internal
 * Signal the thread who holds the ticket at the front of the queue for a 
 * fair lock that it's likely it can now proceed to gain the lock. Doesn't
 * touch the queue itself, as removing it too early could allow another thread 
 * to jump the queue.
 * \param	lock	Fair lock whose queue we want to look at.
 */
void
FairLockSignalQueueHead (NFairLock *lock)
{
	if (lock->queue == NULL) return; // no-one to signal
	
	if (lock->queue->is_writer == FALSE) {
		// a thread is waiting for a read lock
		XplSignalLocalSemaphore(lock->queue->signal);
		XplSignalLocalSemaphore(lock->queue->signal);
	} else {
		// a thread is waiting for a write lock - only signal if readers have left
		// this eventually succeeds when the last read lock is given up
		// new acquirees queue until then
		if (lock->readers == 0) {
			XplSignalLocalSemaphore(lock->queue->signal);
			XplSignalLocalSemaphore(lock->queue->signal);
		}
	}
}

/**
 * Get an Exclusive lock to a store resource. With an exclusive lock, no
 * other thread should read from or write to the resource protected. If
 * other readers/writer already holds the lock, this will wait in a queue
 * until the lock becomes free.
 * \bug Timeout is completely useless atm.
 * \param	client	Store client accessing the resource
 * \param	lock	Where we want to store the fair lock we receive
 * \param	guid	GUID of the resource we wish to access in the store
 * \param	timeout	How long we're willing to wait for the lock
 * \return		0 if we gained the lock, some error number otherwise.
 */
int
StoreGetExclusiveFairLockQuiet(StoreClient *client, NFairLock **lock,
                               uint64_t guid, time_t timeout)
{
	NFairLock *found_lock;
	int result = 0;
	
	if (timeout) {
		// FIXME : unused parameter; should use this somehow!
	}
	
	// find the lock
	found_lock = FairLockFind(client->storeName, guid);
	if (found_lock == NULL) return -1; // error
	*lock = found_lock;
	
	// try to acquire a write lock
	XplWaitOnLocalSemaphore(found_lock->access);
	if (found_lock->readers == 0) {
		// no readers - we're free
		found_lock->writer = 1;
	} else {
		// need to queue for write access
		NFairLockQueueEntry *ticket;
		ticket = FairLockNewQueueEntry(found_lock, TRUE);
		if (ticket) {
			// since we're now in the queue, give up our access and wait our turn
			XplSignalLocalSemaphore(found_lock->access);
			XplWaitOnLocalSemaphore(ticket->signal); // wait til we reach the head of the queue
			
			XplWaitOnLocalSemaphore(found_lock->access);
			
			// now it's our turn, remove our ticket and try to get the write lock
			FairLockRemoveQueueHead(found_lock);
			if ((found_lock->readers > 0) || (found_lock->writer > 0)) {
				// this is a bug
				result = -3;
			}
			found_lock->writer = 1;
		} else {
			result = -2;
		}
	}
	XplSignalLocalSemaphore(found_lock->access);
	return result;
}

/**
 * Get a Shared lock to a store resource. A shared lock grants read-only
 * access to the resource protected, so many threads may be accessing it
 * simultaneously. If there is a queue already waiting, or the lock has
 * been gained for Exclusive access, this function will cause the calling
 * thread to wait in a queue for shared access to again become available.
 * \bug Timeout simply doesn't work atm.
 * \param	client	Store client who wishes to access a resource
 * \param	lock	Where we want to save the lock we gain
 * \param	guid	GUID of the store resource we want to access
 * \param	timeout	How long we're willing to wait to gain the resource
 * \return		0 if we successfully got the lock, an error number otherwise
 */
int
StoreGetSharedFairLockQuiet(StoreClient *client, NFairLock **lock, 
                            uint64_t guid, time_t timeout)
{
	NFairLock *found_lock;
	int result = 0;
	
	if (timeout) {
		// FIXME : unused parameter; should use this somehow!
	}
	
	// first, find the lock
	found_lock = FairLockFind(client->store, guid);
	if (found_lock == NULL) return -1; // some error
	*lock = found_lock;
	
	// next, try to acquire a shared lock
	XplWaitOnLocalSemaphore(found_lock->access);
	
	if ((found_lock->writer == 0) && (found_lock->queue == NULL)) {
		// no writers and no-one waiting, so we can freely read
		found_lock->readers++;
	} else {
		// need to queue for read access
		NFairLockQueueEntry *ticket;
		ticket = FairLockNewQueueEntry(found_lock, FALSE);
		if (ticket) {
			// release our access and wait our turn
			XplSignalLocalSemaphore(found_lock->access);
			XplWaitOnLocalSemaphore(ticket->signal);
			XplWaitOnLocalSemaphore(found_lock->access);
			
			FairLockRemoveQueueHead(found_lock); // remove our ticket
			if (found_lock->writer > 0) {
				// bug..
				result = -2;
			} else {
				found_lock->readers++;
				// next person in queue might be a reader as well, 
				// so signal them
				FairLockSignalQueueHead(found_lock);
			}
		} else {
			// couldn't queue...
			result = -1;
		}
	}
	XplSignalLocalSemaphore(found_lock->access);
	
	// set the lock
	*lock = found_lock;
	
	return result;
}

/**
 * Downgrade an existing Exclusive lock to Shared status 
 * \param	lock	Reference to the Exlcusive lock we want to become shared
 * \return		0 if successful
 */
int
StoreDowngradeExclusiveFairLock(NFairLock **lock)
{
	NFairLock *l = *lock;
	int result = 0;
	
	XplWaitOnLocalSemaphore(l->access);
	if (l->writer) {
		if (l->readers > 0) {
			// writing and reading at the same time ... bug.
		}
		l->writer = 0;
		l->readers++;
	} else {
		// this wasn't an exclusive lock.. bug.
		result = -1;
	}
	FairLockSignalQueueHead(l);
	XplSignalLocalSemaphore(l->access);
	return result;
}

/**
 * Release a fair lock which was gained for read-only use.
 * \param	lock	Reference to the lock we're giving up
 * \return		0 if successful
 */
int
StoreReleaseSharedFairLockQuiet(NFairLock **lock)
{
	NFairLock *l = *lock;
	
	XplWaitOnLocalSemaphore(l->access);
	l->readers--;
	// signal any waiting threads
	FairLockSignalQueueHead(l);
	XplSignalLocalSemaphore(l->access);
	*lock = NULL;
	return 0;
}

/**
 * Release a fair lock which was gained for exclusive use.
 * \param	lock	Reference to the lock we're giving up
 * \return		0 if successful
 */
int
StoreReleaseExclusiveFairLockQuiet(NFairLock **lock)
{
	NFairLock *l = *lock;
	
	XplWaitOnLocalSemaphore(l->access);
	l->writer = 0;
	FairLockSignalQueueHead(l);
	XplSignalLocalSemaphore(l->access);
	*lock = NULL;
	return 0;
}

/**
 * Release a fair lock which could be exclusive or shared.
 * \param	lock	Reference to the lock we're giving up
 * \return		0 if successful
 */
int
StoreReleaseFairLockQuiet(NFairLock **lock)
{
	NFairLock *l = *lock;
	
	XplWaitOnLocalSemaphore(l->access);
	if (l->writer && l->readers) {
		// bug - shouldn't have both!
		l->writer = l->readers = 0;
	} else if (l->writer) {
		l->writer = 0;
	} else if (l->readers) {
		l->readers--;
	} else {
		// no readers or writers - another bug?
	}
	FairLockSignalQueueHead(l);
	XplSignalLocalSemaphore(l->access);
	*lock = NULL;
	return 0;
}
