#include "stored.h"
#include "messages.h"
#include "lock.h"

/* ring log for debugging purposes */

typedef struct {
	time_t timestamp;
	int thread_id;
	char message[256];
} RingLogItem;

#define RINGLOG_SIZE 200

static RingLogItem ringlog[RINGLOG_SIZE];
// ringlog_pos points to the next "free" ringlog entry.
static int ringlog_pos = 0;
// thread map used to translate from pthread_t to simple int
static pthread_t ringlog_threadmap[100];
static XplMutex ringlog_lock;

void
RinglogInit()
{
	// initialise lock; this must be held to access the ringlog.
	XplMutexInit(ringlog_lock);
	
	// clear the ringlog and thread mapping
	memset(ringlog, 0, sizeof(ringlog));
	memset(ringlog_threadmap, 0, sizeof(ringlog_threadmap));
}

int
RinglogThreadID()
{
	pthread_t self = pthread_self();
	int thread_id = 99;
	
	for (int i = 0; i < 100; i++) {
		if (ringlog_threadmap[i]) {
			if (ringlog_threadmap[i] == self) {
				thread_id = i;
				break;
			}
		} else {
			ringlog_threadmap[i] = self;
			thread_id = i;
			break;
		}
	}
	
	return thread_id;
}

void
Ringlog(char *message)
{
	XplMutexLock(ringlog_lock);
	
	// find the next ring log entry and write into it
	ringlog[ringlog_pos].timestamp = time(NULL);
	ringlog[ringlog_pos].thread_id = RinglogThreadID();
	strncpy(ringlog[ringlog_pos].message, message, 255);
	
	// advance the ring pointer, and wrap around if necessary
	ringlog_pos++;
	if (ringlog_pos == RINGLOG_SIZE) ringlog_pos = 0;
	
	XplMutexUnlock(ringlog_lock);
}

void
RinglogDumpFilehandle(int fh)
{
	XplMutexLock(ringlog_lock);
	
	for (int i = ringlog_pos; i < ringlog_pos + RINGLOG_SIZE; i++) {
		// print the next ring log entry - wraps by cunning use of modulo
		char message[300];
		RingLogItem *entry = &(ringlog[i % RINGLOG_SIZE]);
		
		if (entry->timestamp) {
			ssize_t len = snprintf(message, 299, "%.19s [%d] %s\n", 
				ctime(&(entry->timestamp)),
				entry->thread_id,
				entry->message);
			if (write(fh, message, len) != len) {
				/* is there anything really to do here? */
			}
		}
	}
	
	XplMutexUnlock(ringlog_lock);
}

void
RinglogDumpConsole()
{
	// hope we still have STDOUT around?!
	RinglogDumpFilehandle(1);
}
