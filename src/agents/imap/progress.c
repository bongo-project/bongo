// Parts Copyright (C) 2008 Patrick Felt. See COPYING for details.
// Parts Copyright (C) 2008 Alex Hudson. See COPYING for details.
#include "imapd.h"
#include <time.h>

/* traverse the global list and send oks to everyone */
void
DoUpdate(void)
{
    BongoList *cur;
    ImapSession *session;
    ProgressUpdate *update;

    XplWaitOnLocalSemaphore(Imap.sem_Busy);
    cur = Imap.list_Busy;
    while(cur) {
        session = (ImapSession *)cur->data;
        update = session->progress;
        if (update->message) {
            ConnWriteF(session->client.conn, "%s\r\n", update->message);
        }
        ConnFlush(session->client.conn);
        cur = cur->next;
    }
    XplSignalLocalSemaphore(Imap.sem_Busy);
}

void
StartBusy(ImapSession *session, char *Message)
{
    ProgressUpdate *p = MemMalloc(sizeof(ProgressUpdate));
    if (p == NULL) {
        return;
    }

    /* add ourselves to the list */
    p->last_update = time(0);
    p->messages_processed = 0;
    p->message = MemStrdup(Message);
    session->progress = p;

    XplWaitOnLocalSemaphore(Imap.sem_Busy);
    Imap.list_Busy = BongoListPrepend(Imap.list_Busy, session);
    XplSignalLocalSemaphore(Imap.sem_Busy);

    return;
}

/* this function takes the handle returned from StartProgressUpdate */
void
StopBusy(ImapSession *session)
{
    XplWaitOnLocalSemaphore(Imap.sem_Busy);
    Imap.list_Busy = BongoListDelete(Imap.list_Busy, session, FALSE);
    XplSignalLocalSemaphore(Imap.sem_Busy);

    if (session->progress) {
        if (session->progress->message) {
            MemFree(session->progress->message);
        }
        MemFree(session->progress);
        session->progress = NULL;
    }
    return;
}
