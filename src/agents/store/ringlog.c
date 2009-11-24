#include "stored.h"
#include "messages.h"
#include "lock.h"

/* ring log for debugging purposes */

typedef struct {
	time_t timestamp;
	char message[256];
} RingLogItem;

#define RINGLOG_SIZE 200

static RingLogItem ringlog[RINGLOG_SIZE];
// ringlog_pos points to the next "free" ringlog entry.
static int ringlog_pos = 0;
static XplMutex ringlog_lock;

void
RinglogInit()
{
	// initialise lock; this must be held to access the ringlog.
	XplMutexInit(ringlog_lock);
	
	// clear the ringlog.
	memset(ringlog, 0, sizeof(ringlog));
}

void
Ringlog(char *message)
{
	XplMutexLock(ringlog_lock);
	
	// find the next ring log entry and write into it
	ringlog[ringlog_pos].timestamp = time(NULL);
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
		size_t len = snprintf(message, 299, "%20s %s\n", 
			ctime(&(ringlog[i % RINGLOG_SIZE].timestamp)),
			ringlog[i % RINGLOG_SIZE].message);
		write(fh, message, len);
	}
	
	XplMutexUnlock(ringlog_lock);
}

void
RinglogDumpConsole()
{
	// hope we still have STDOUT around?!
	RinglogDumpFilehandle(1);
}
