#include "imapd.h"
#include <time.h>

/* traverse the global list and send oks to everyone */
void
DoUpdate(void)
{
    BongoList *cur = Imap.list_Busy;
    ImapSession *session;

    XplWaitOnLocalSemaphore(Imap.sem_Busy);
    while(cur) {
        session = (ImapSession *)cur->data;
        ConnWriteStr(session->client.conn, "* OK - Still Busy\r\n");
        ConnFlush(session->client.conn);
        cur = cur->next;
    }
    XplSignalLocalSemaphore(Imap.sem_Busy);
}

void *
StartBusy(ImapSession *session)
{
    XplWaitOnLocalSemaphore(Imap.sem_Busy);

    /* add ourselves to the list and return the pointer */
    Imap.list_Busy = BongoListPrepend(Imap.list_Busy, session);
    XplSignalLocalSemaphore(Imap.sem_Busy);

    return Imap.list_Busy;
}

/* this function takes the handle returned from StartProgressUpdate */
void
StopBusy(void *handle)
{
    BongoList *me = (BongoList *)handle;

    XplWaitOnLocalSemaphore(Imap.sem_Busy);
    /* unlink me */
    if (me->prev || me->next) {
        if (me->prev) {
            me->prev->next = me->next;
        }

        if (me->next) {
            me->next->prev = me->prev;
        }
    } else {
        /* i was the last item in the array.  null it */
        Imap.list_Busy = NULL;
    }

    /* free me */
    MemFree(me);

    XplSignalLocalSemaphore(Imap.sem_Busy);
    return;
}
