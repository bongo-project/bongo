#include "imapd.h"
#include <time.h>

// Update our internal status and warn the client if it has been waiting
// for too long since it last heard from us.
void
DoProgressUpdate(ImapSession *session)
{
	ProgressUpdate *status = session->progress;
	time_t now = time(0);
	
	if (status == NULL) return;
	status->messages_processed++;
	if ((now - status->last_update) > 10) {
		// update the client 
		ConnWriteF(session->client.conn, 
		           "* OK - %s (processed %d since last update)\n", 
		           status->message, status->messages_processed);
		ConnFlush(session->client.conn);
		status->last_update = now;
		status->messages_processed = 0;
	}
}

void
StartProgressUpdate(ImapSession *session, char *message)
{
	ProgressUpdate *p = MemMalloc(sizeof(ProgressUpdate));
	
	if (p == NULL) return;
	
	p->last_update = time(0);
	p->messages_processed = 0;
	p->message = message;
	session->progress = p;
}

void
StopProgressUpdate(ImapSession *session)
{
	if (session->progress == NULL) return;
	
	MemFree(session->progress);
	session->progress = NULL;
}
