/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 * (C) 2007 Patrick Felt
 ****************************************************************************/

#include <config.h>

#include "queue.h"
#include "mime.h"
#include "messages.h"
#include "domain.h"

#include <xpl.h>
#include <logger.h>
#include <nmap.h>
#include <nmlib.h>

#define PUSHCLIENTALLOC 20
#define REMOTENMAP_ALLOC_STEPS 10
#define MAX_PUSHCLIENTS_ERRORS 25

#if defined(SOLARIS) || defined(S390RH)
#define SPOOL_LOCK_ARRAY_MASK 0xFF000000
#else
#define SPOOL_LOCK_ARRAY_MASK 0x000000FF
#endif
#define UNUSED_SPOOL_LOCK_ID ((unsigned long)(1 << 31))

typedef struct _NMAPConnection {
    Connection *conn;
    unsigned long address;
    unsigned short port;
    BOOL error;
} NMAPConnection;
    

typedef struct _NMAPConnections {
    long used;
    long allocated;

    NMAPConnection *connections;
} NMAPConnections;

/* Globals */
MessageQueue Queue = { 0, };
int FindQueue(const void *i, const void *node);
int FindPool(const void *str, const void *node);
BOOL SpoolEntryIDLock(unsigned long id);
uint32_t IntHash (unsigned long *s);
int IntCmp (const unsigned long *s1, const unsigned long *s2);

static int HandleDSN(FILE *data, FILE *control);

#define MAX_CHARS_IN_PDBSEARCH    512

/* File open check. Warns if the handle is already in use, and tries to close that file.
 * Any FILE * pointers should therefore be NULL before use, otherwise it triggers a false
 * alarm
 */

#define FOPEN_CHECK(handle, path, mode) fopen_check(&(handle), (path), (mode), __LINE__)
FILE *
fopen_check(FILE **handle, char *path, char *mode, int line)
{
    int ret;
    if (*handle != NULL) {
        LogFailureF("File handle already open on line %d", line);
        ret = fclose(*handle);
        LogAssertF(ret == 0, "Couldn't close dangling file handle from line %d", line);
    }
    *handle = fopen(path, mode);
    // LogFailureF("File open: %s, handle %ld", path, *handle);
    return *handle;
}

/* File close check. Warns if it cannot close the file, and sets the file handle to NULL
 */

#define FCLOSE_CHECK(f) fclose_check(&(f), __LINE__)
int
fclose_check(FILE **fh, int line)
{
    int ret;
    ret = fclose(*fh);
    if (ret == 0) {
        // LogFailureF("File close: handle %ld", *fh);
        *fh = NULL;
    } else {
        LogFailureF("File close failed on line %d: %d", line, errno);
    }
    return ret;
}

#define UNLINK_CHECK(path) unlink_check((path), __LINE__)
int
unlink_check(char *path, int line)
{
    int ret;
    ret = unlink(path);
    LogAssertF(ret == 0, "Unable to delete file %s on line %d: %d", path, line, errno);
    return ret;
}

#define RENAME_CHECK(oldpath, newpath) rename_check((oldpath), (newpath), __LINE__)
int
rename_check(const char *oldpath, const char *newpath, int line)
{
    int ret;
    ret = rename(oldpath, newpath);
    LogAssertF(ret == 0, "Unable to rename %s to %s on line %d: %d", oldpath, newpath, line, errno);
    return ret;
}

uint32_t IntHash (unsigned long *s) {
    return (unsigned long)s;
}

int IntCmp (const unsigned long *s1, const unsigned long *s2) {
    return s1 - s2;
}

static int 
PDBSearch(char *doc, char *searchString)
{
    unsigned char *w[MAX_CHARS_IN_PDBSEARCH], *textStart, *textEnd, *text, *end;
    int wl[MAX_CHARS_IN_PDBSEARCH];
    unsigned char expression[MAX_CHARS_IN_PDBSEARCH];
    long word, words = 0, i,len;
    int found = 0;

    if (!doc || !(*doc)) {
        return(0);
    }

    len = strlen(searchString);
    if (len == 0) {
        return 0;
    }

    if (len < MAX_CHARS_IN_PDBSEARCH) {
        strcpy(expression, searchString);
    } else {
        len = sizeof(expression) - 1;
        strncpy(expression, searchString, len);
        expression[MAX_CHARS_IN_PDBSEARCH - 1] = '\0';
    }

    for (i = 0; i < len; i++) {
        expression[i] = toupper(expression[i]);
    }

    text = expression;
    while (*text && isspace(*text)) {
        text++;
    }

    if ((*text == '\"') && ((end = strchr(text + 1, '\"')) != NULL)) {
        w[words++] = text + 1;
        *end = '\0';
        text = end + 1;
    }
        
    while (*text && isspace(*text)) {
        text++;
    }

    if ((*text == '\0') && (!words))
        return 0;

    if (*text) {
        w[words++] = text;
    }

    for(i = text - expression; (i < len) && (words < MAX_CHARS_IN_PDBSEARCH) ;i++) {
        if (isspace(expression[i])) {
            if (!isspace(expression[i+1])) {
                w[words++] = expression + i + 1;
                expression[i] = '\0';
            } else {
                expression[i] = '\0';
            }
        }
    }

    for (i = 0; i < words; i++) {
        wl[i] = strlen(w[i]);
    }

    textStart = doc;
    textEnd = doc + strlen(doc) - 1;

    for (text = textStart; text != textEnd; text++) {
        for (word = 0; word < words; word++) {
            i = 0;
            while ((i != wl[word]) && (w[word][i] == toupper(text[i]))) {
                i++;
            }
            if (wl[word]==i) {
                found = 1;

                if ((text != textStart && !isspace(*(text - 1))) && ((text != textEnd) && !isspace(*(text + wl[word])))) {
                    goto NoMatch;
                }
            }
        }
NoMatch:
        ;
    }
    return found;
}

static BOOL 
InitSpoolEntryIDLocks(void)
{
    Queue.spoolLocks.hash = BongoHashtableCreate(64, (HashFunction)IntHash, (CompareFunction)IntCmp);
    XplOpenLocalSemaphore(Queue.spoolLocks.semaphore, 1);

    if (Queue.spoolLocks.hash) {
        return(TRUE);
    }

    return(FALSE);
}

static void 
DeInitSpoolEntryIDLocks(void)
{
    XplCloseLocalSemaphore(Queue.spoolLocks.semaphore);
    BongoHashtableDelete(Queue.spoolLocks.hash);
}

BOOL
SpoolEntryIDLock(unsigned long id)
{
    BOOL result = FALSE;

    XplWaitOnLocalSemaphore(Queue.spoolLocks.semaphore);
    void *p = BongoHashtableGet(Queue.spoolLocks.hash, (void *)id);

    if (!p) {
        Log(LOG_DEBUG, "Aquiring Lock on %ld", id);
        BongoHashtablePutNoReplace(Queue.spoolLocks.hash, (void *)id, (void *)id);
        result = TRUE;
    } else {
        Log(LOG_DEBUG, "Failed to aquire lock on %ld.  Already locked!", id);
    }
    XplSignalLocalSemaphore(Queue.spoolLocks.semaphore);

    return result;
}

static void 
SpoolEntryIDUnlock(unsigned long id)
{
    XplWaitOnLocalSemaphore(Queue.spoolLocks.semaphore);
    Log(LOG_DEBUG, "Unlocked %ld", id);
    BongoHashtableRemove(Queue.spoolLocks.hash, (void *)id);
    XplSignalLocalSemaphore(Queue.spoolLocks.semaphore);

    return;
}

int FindQueue(const void *n_lft, const void *n_rgt) {
    /* casting them like this makes the code a bit easier to read */
    QueueList *lft = *(QueueList **)n_lft;
    QueueList *rgt = *(QueueList **)n_rgt;
    return (lft->queue - rgt->queue);
}

int FindPool(const void *n_lft, const void *n_rgt) {
    QueuePoolList *lft = *(QueuePoolList **)n_lft;
    QueuePoolList *rgt = *(QueuePoolList **)n_rgt;
    return strcmp(lft->identifier, rgt->identifier);
}

static unsigned long
AddPushAgent(QueueClient *client, 
             char *address, 
             int port, 
             int queue, 
             unsigned char *identifier)
{
    /* since the sorting doesn't always pass in the int or the struct the same every time i need to put the int in a struct */
    QueueList tempQueue;
    QueueList *p_tempQueue = &tempQueue;
    QueuePoolList tempPool;
    QueuePoolList *p_tempPool = &tempPool;

    QueueList *CurrentQueue;
    QueuePoolList *CurrentPool;
    int ItemIndex;

    Log(LOG_INFO, "Adding client (%s) on host %s:%d to queue %d", identifier, LOGIP(client->conn->socketAddress), port, queue);

    XplMutexLock(Queue.PushClients.lock);

    /* first look through the list to find the right queue */
    tempQueue.queue = queue;
    ItemIndex = GArrayFindSorted(Queue.PushClients.queues, &p_tempQueue, FindQueue);
    if (ItemIndex < 0) {
        /* there is no queue item in the array yet.  we need to initialize one */
        CurrentQueue = MemNew0(QueueList, 1);
        CurrentQueue->queue = queue;
        CurrentQueue->pools = g_array_new(FALSE, FALSE, sizeof(QueuePoolList *));
        g_array_append_val(Queue.PushClients.queues, CurrentQueue); 
    } else {
        CurrentQueue = g_array_index(Queue.PushClients.queues, QueueList *, ItemIndex);
    }

    /* CurrentQueue should be set now */
    strncpy(tempPool.identifier, identifier, 100);
    ItemIndex = GArrayFindSorted(CurrentQueue->pools, &p_tempPool, FindPool);
    if (ItemIndex < 0) {
        /* there is no pool already.  we need to initialize one */
        CurrentPool = MemNew0(QueuePoolList, 1);
        strncpy(CurrentPool->identifier, identifier, 100);
        ConnAddressPoolStartup(&CurrentPool->pool, 5, 60);
        g_array_append_val(CurrentQueue->pools, CurrentPool);
    } else {
        CurrentPool = g_array_index(CurrentQueue->pools, QueuePoolList *, ItemIndex);
    }

    /* CurrentPool should be set now */
    ConnAddressPoolAddHost(&CurrentPool->pool, address, htons(port), 1);   /* the protocol doesn't support passing a weight so weight everything equally */

    /* resort both arrays */
    g_array_sort(CurrentQueue->pools, FindPool);
    g_array_sort(Queue.PushClients.queues, FindQueue);

    XplMutexUnlock(Queue.PushClients.lock);
    return 0;
}

static void
RemoveAllPushAgents(void)
{
    QueueList *CurrentQueue;
    QueuePoolList *CurrentPool;

    XplMutexLock(Queue.PushClients.lock);
    while (Queue.PushClients.queues->len) {
        CurrentQueue = g_array_index(Queue.PushClients.queues, QueueList *, 0);
        while (CurrentQueue->pools->len) {
            CurrentPool = g_array_index(CurrentQueue->pools, QueuePoolList *, 0);
            ConnAddressPoolShutdown(&(CurrentPool->pool));
            g_array_remove_index_fast(CurrentQueue->pools, 0);
            MemFree(CurrentPool);
        }
        MemFree(CurrentQueue);
        g_array_remove_index_fast(Queue.PushClients.queues, 0);
    }

    XplMutexUnlock(Queue.PushClients.lock);

    return;
}


static BOOL
CheckIfDeliveryDeferred(void)
{
    struct tm tm;
    time_t tod=time(NULL);
    register unsigned char day;
    register unsigned char hour;

    localtime_r(&tod, &tm);
    day = tm.tm_wday;
    hour = tm.tm_hour;

    if ((hour < Conf.deferStart[day]) || (hour >= Conf.deferEnd[day])) {
        return(FALSE);
    }

    return(FALSE);
}

static int
GetNMAPConnection(NMAPConnections *list, 
                  struct sockaddr_in *address,
                  NMAPConnection **nmapRet,
                  BOOL *new)
{
    Connection *conn;
    int index;
    BOOL result;
    void *temp;
    char line[CONN_BUFSIZE + 1];
    
    for (index = 0; index < list->used; index++) {
        if ((list->connections[index].address == address->sin_addr.s_addr) && (list->connections[index].port == address->sin_port)) {
            *nmapRet = &list->connections[index];
            *new = FALSE;
            return 0;
        }
    }

    *new = TRUE;
    
    conn = NMAPConnect(NULL, address);
    if (conn == NULL) {
        Log(LOG_ERROR, "Could not connect to bongostore");
        return DELIVER_TRY_LATER;
    }
    
    result = NMAPAuthenticateToStore(conn, line, CONN_BUFSIZE);
    if (!result) {
        NMAPQuit(conn);
        ConnClose(conn);
        ConnFree(conn);
        return DELIVER_TRY_LATER;
    }

    if ((list->used + 1) > list->allocated) {
        temp = MemRealloc(list->connections, (list->allocated + REMOTENMAP_ALLOC_STEPS) * sizeof(NMAPConnection));
        if (temp) {
            list->connections = (NMAPConnection *)temp;
        } else {
            NMAPQuit(conn);
            ConnClose(conn);
            ConnFree(conn);
            return DELIVER_TRY_LATER;
        }
        
        list->allocated += REMOTENMAP_ALLOC_STEPS;
    }
    
    index = list->used++;
    list->connections[index].address = address->sin_addr.s_addr;
    list->connections[index].port = address->sin_port;
    list->connections[index].conn = conn;
    list->connections[index].error = FALSE;

    *nmapRet = &list->connections[index];
    return 0;
}

static int
DeliverToStore(NMAPConnections *list, 
               struct sockaddr_in *address,
               int type,
               char *from, 
               char *authFrom, 
               char *filename,
               FILE *fh,
               unsigned long count, 
               char *recipient, 
               char *mailbox,
               unsigned long messageFlags)
{
    NMAPConnection *nmap = NULL;
    int ccode = 0;
    unsigned char line[CONN_BUFSIZE + 1];
    BOOL new;

    if ((ccode = GetNMAPConnection(list, address, &nmap, &new)) < 0) {
        return DELIVER_TRY_LATER;
    }

    if (nmap->error) {
        return DELIVER_TRY_LATER;
    }

    if (new) {
        /* We've got the connection, now create the entry and get everything set up */
        if (address->sin_addr.s_addr == MsgGetHostIPAddress()) {
            if ((ccode = NMAPSendCommandF(nmap->conn, "DELIVER FILE %d %s %s %s\r\n", type, from, authFrom, filename)) == -1) {
                nmap->error = TRUE;
            }       
        } else {
            if ((ccode = ConnWriteF(nmap->conn, "DELIVER STREAM %d %s %s %d\r\n", type, from, authFrom, (int)count)) == -1 ||
                (ccode = ConnWriteFile(nmap->conn, fh)) == -1 ||
                (ccode = ConnFlush(nmap->conn)) == -1) {
                nmap->error = TRUE;

                return DELIVER_TRY_LATER;
            }
        }

        ccode = NMAPReadAnswer(nmap->conn, line, CONN_BUFSIZE, TRUE);
        
        if (ccode != 2053) {
            nmap->error = TRUE;
            return DELIVER_TRY_LATER;
        }       
    }

    ccode = NMAPRunCommandF(nmap->conn, line, CONN_BUFSIZE, "%s %s %lu\r\n", 
                            recipient, mailbox, messageFlags);

    if (ccode == 1000) {
        return DELIVER_SUCCESS;
    } else {
        return atoi(line);
    }
}

static void 
EndStoreDelivery(NMAPConnections *list)
{
    int ccode;
    long index;
    unsigned char line[CONN_BUFSIZE + 1];

    for (index = 0; index < list->used; index++) {
        NMAPConnection *nmap = &list->connections[index];

        if (nmap) {
            if (!nmap->error &&
                ((ccode = ConnWrite(nmap->conn, "\r\n", 2)) != -1) && 
                ((ccode = ConnFlush(nmap->conn)) != -1)) {
                NMAPReadAnswer(nmap->conn, line, CONN_BUFSIZE, FALSE);
            }

            ConnClose(nmap->conn);
            ConnFree(nmap->conn);
        } 
    }

    MemFree(list->connections);
}

static void
ProcessQueueEntryCleanUp(unsigned long id, MIMEReportStruct *report)
{
    if (report) {
        FreeMIME(report);
    }

    if (id) {
        SpoolEntryIDUnlock(id);
    }

    XplSafeDecrement(Queue.activeWorkers);
}

static BOOL
ProcessQueueEntry(unsigned char *entryIn)
{
    int i;
    int queue;
    int count;
    int qDateLength = 0;
    int qFlagsLength = 0;
    int qIDLength = 0;
    int qAddressLength = 0;
    int qFromLength = 0;
    long lines=0;
    QueueList *CurrentQueue;
    QueueList tempQueue;
    QueueList *p_tempQueue=&tempQueue;
    int ItemIndex=0;
    unsigned int iter_p=0;
    unsigned long dSize = 0;
    unsigned long entryID = 0;
    unsigned char *ptr;
    unsigned char *ptr2 = NULL;
    unsigned char *cur;
    unsigned char *next;
    unsigned char *limit;
    unsigned char *qDate;
    unsigned char *qFlags;
    unsigned char *qID;
    unsigned char *qAddress;
    unsigned char *qFrom;
    unsigned char *qEnvelope = NULL;
    unsigned char path[XPL_MAX_PATH + 1] = "";
    unsigned char path2[XPL_MAX_PATH + 1];
    unsigned char line[CONN_BUFSIZE + 1] = "";
    unsigned char entry[15];
    BOOL keep = TRUE;
    BOOL bounce = FALSE;
    time_t date;
    struct sockaddr_in saddr;
    struct stat sb;
    FILE *fh = NULL;
    FILE *data = NULL;
    FILE *newFH = NULL;
    MIMEReportStruct *report = NULL;
    void *handle;
    QueueClient *client;

StartOver:
    if (!entryIn) {
        ProcessQueueEntryCleanUp(entryID, report);
        return(FALSE);
    }

    XplRenameThread(XplGetThreadID(), entryIn);

    line[0] = '\0';

    strcpy(entry, entryIn + 3);
    entryIn[3] = '\0';

    queue = atoi(entryIn);

    MemFree(entryIn);

    entryID = strtol(entry, NULL, 16);

    Log(LOG_DEBUG, "Processing entry %ld on queue %d", entryID, queue);

    if (SpoolEntryIDLock(entryID)) {
        sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
        FOPEN_CHECK(fh, path, "r+b");
    } else {
        ProcessQueueEntryCleanUp(0, report);
        return(FALSE);
    }

    if (fh) {
        fgets(line, CONN_BUFSIZE, fh);
        date = atoi(line + 1);
        FCLOSE_CHECK(fh);
    } else {
        ProcessQueueEntryCleanUp(entryID, report);
        return(FALSE);
    }

    /* We've got pre and post processing off queue entries - this is pre */
    switch(queue) {
        case Q_INCOMING: {
            FILE *temp = NULL;
            sb.st_size = -1;
            qDate = NULL;
            qFlags = NULL;
            qID = NULL;
            qAddress = NULL;
            qFrom = NULL;

            sprintf(path, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
            FOPEN_CHECK(newFH, path, "wb");

            sprintf(path, "%s/c%s.%03d",Conf.spoolPath, entry, queue);
            FOPEN_CHECK(temp, path, "rb");
            if (newFH 
                    && (stat(path, &sb) == 0) 
                    && (sb.st_size > 8) 
                    && ((qEnvelope = (unsigned char *)MemMalloc(sb.st_size + 1)) != NULL) 
                    && (temp) 
                    && (fread(qEnvelope, sizeof(unsigned char), sb.st_size, temp) == (size_t)sb.st_size)) {
                /* Sort the control file as follows:
                    QUEUE_DATE
                    QUEUE_FLAGS
                    QUEUE_ID
                    QUEUE_ADDRESS
                    QUEUE_FROM

                   Followed by, in no particular order:
                    QUEUE_BOUNCE
                    QUEUE_RECIP_LOCAL
                    QUEUE_RECIP_MBOX_LOCAL
                    QUEUE_CALENDAR_LOCAL
                    QUEUE_RECIP_REMOTE
                    QUEUE_THIRD_PARTY
                */
                FCLOSE_CHECK(temp);
                qEnvelope[sb.st_size] = '\0';

                count = 0;
                cur = qEnvelope;
                limit = qEnvelope + sb.st_size;
                while (cur < limit) {
                    next = strchr(cur, '\n');
                    if (next) {
                        next++;
                    } else {
                        next = limit;
                    }

                    switch (*cur) {
                        case 0x0A:
                        case 0x0D: {
                            qFrom = NULL;
                            cur = limit;
                            break;
                        }

                        case QUEUE_DATE: {
                            qDate = cur;
                            qDateLength = next - cur;
                            break;
                        }

                        case QUEUE_FLAGS: {
                            qFlags = cur;
                            qFlagsLength = next - cur;
                            break;
                        }

                        case QUEUE_ID: {
                            qID = cur;
                            qIDLength = next - cur;
                            break;
                        }

                        case QUEUE_ADDRESS: {
                            qAddress = cur;
                            qAddressLength = next - cur;
                            break;
                        }

                        case QUEUE_FROM: {
                            qFrom = cur;
                            qFromLength = next - cur;
                            break;
                        }

                        case QUEUE_BOUNCE:
                        case QUEUE_CALENDAR_LOCAL:
                        case QUEUE_RECIP_LOCAL:
                        case QUEUE_RECIP_MBOX_LOCAL:
                        case QUEUE_RECIP_REMOTE:
                        case QUEUE_THIRD_PARTY: {
                            count++;
                            break;
                        }

                        default: {
                            break;
                        }
                    }

                    if (next < limit) {
                        next[-1] = '\n';
                    }

                    cur = next;
                }

                if (qDate && qFrom && count) {
                    fwrite(qDate, sizeof(unsigned char), qDateLength, newFH);

                    if (qFlags) {
                        fwrite(qFlags, sizeof(unsigned char), qFlagsLength, newFH);
                    } else {
                        fwrite(QUEUES_FLAGS"0\r\n", sizeof(unsigned char), 4, newFH);
                    }

                    if (qID) {
                        fwrite(qID, sizeof(unsigned char), qIDLength, newFH);
                    }

                    if (qAddress) {
                        fwrite(qAddress, sizeof(unsigned char), qAddressLength, newFH);
                    }

                    fwrite(qFrom, sizeof(unsigned char), qFromLength, newFH);
                } else {
                    /* fixme - if a new queue entry has at least QUEUE_FROM 
                       but no recipients we should bounce the message rather
                       than consuming it! */
                    FCLOSE_CHECK(newFH);

                    UNLINK_CHECK(path);

                    sprintf(path, "%s/w%s.%03d",Conf.spoolPath, entry, queue);
                    UNLINK_CHECK(path);

                    sprintf(path, "%s/d%s.msg",Conf.spoolPath, entry);
                    UNLINK_CHECK(path);

                    MemFree(qEnvelope);

                    if ((handle = QDBHandleAlloc()) != NULL) {
                        QDBRemoveID(handle, entryID);

                        QDBHandleRelease(handle);
                    }

                    XplSafeDecrement(Queue.queuedLocal);
                    ProcessQueueEntryCleanUp(entryID, report);
                    return(TRUE);
                }

/* TODO: look this over to make sure this is still valid */
                count = 0;
                cur = qEnvelope;
                while (cur < limit) {
                    next = strchr(cur, '\n');
                    if (next) {
                        ptr = next++;
                    } else {
                        ptr = NULL;
                        next = limit;
                    }

                    switch (*cur) {
                        case QUEUE_BOUNCE:
                        case QUEUE_CALENDAR_LOCAL: {
                            fwrite(cur, sizeof(unsigned char), next - cur, newFH);
                            break;
                        }

                        case QUEUE_RECIP_LOCAL:
                        case QUEUE_RECIP_MBOX_LOCAL:
                        case QUEUE_RECIP_REMOTE: {
                            if (ptr) {
                                *ptr = '\0';
                            }

                            ptr2 = strchr(cur + 1, ' ');
                            if (ptr2) {
                                *ptr2 = '\0';
                            }

                            /* alias this user */
                            unsigned char buffer[1024]; /* FIXME: is this too big? */
                            unsigned char *addrptr;
                            unsigned char *flags = strchr(ptr2+1,' '); /* the original flags */
                            int cnt=0;

                            aliasing(cur+1, &cnt, buffer);

                            /* find the address in teh result buffer so that we can rewrite it properly */
                            addrptr = strrchr(buffer, ' ');
                            addrptr++;
                            switch(atoi(buffer)) {
                            case 1000:
                                /* this is a local user, change the envelope to reflect that
                                 * in this case ptr2+1 is the orecip which will also include the flags */
                                fprintf(newFH, QUEUES_RECIP_LOCAL"%s %s\r\n", addrptr, ptr2+1);
                                break;
                            case 1002:
                                /* in this case ptr2+1 is the orecip which will also contain the flags */
                                fprintf(newFH, QUEUES_RECIP_REMOTE"%s %s\r\n", addrptr, ptr2+1);
                                break;
                            default:
                                /* in this case we need to chop the flags off so that we can replace them
                                 * with DSN_FAILURE */
                                if (flags) {
                                    *flags = '\0';
                                }
                                fprintf(newFH, "%c%s %s %d\r\n", *cur, addrptr, ptr2+1, DSN_FAILURE);
                                if (flags) {
                                    *flags = ' ';
                                }
                                break;
                            }
                            if (ptr) {
                                *ptr = '\n';
                            }
                            if (ptr2) {
                                *ptr2 = ' ';
                            }
                        }

                        case QUEUE_ADDRESS: 
                        case QUEUE_DATE: 
                        case QUEUE_FROM: 
                        case QUEUE_ID: 
                        case QUEUE_FLAGS: {
                            break;
                        }

                        case QUEUE_THIRD_PARTY:
                        default: {
                            fwrite(cur, sizeof(unsigned char), next - cur, newFH);
                            break;
                        }
                    }

                    cur = next;
                }

                MemFree(qEnvelope);

                FCLOSE_CHECK(newFH);

                UNLINK_CHECK(path);

                sprintf(path2, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
                RENAME_CHECK(path2, path);

                break;
            }
            if (temp) { 
                // this might be left over when the if() above fails
                FCLOSE_CHECK(temp); 
            }

            if (!newFH) { 
                sprintf(path, "%s/w%s.%03d",Conf.spoolPath, entry, queue);
                LogFailureF("File open failure. Entry %ld, path %s", entryID, path);
            } else if (!qEnvelope) {
                LogFailureF("Out of memory. Entry %ld, size %ld", entryID, sb.st_size);
            } else if (!fh) {
                LogFailureF("File open failure. Entry %ld, path %s", entryID, path);
            } else {
                LogFailureF("Event file open failure. Entry %ld, path %s", entryID, path);
            }

            if (qEnvelope) {
                MemFree(qEnvelope);
            }

            if (fh) {
                FCLOSE_CHECK(fh);
            }

            if (newFH) {
                FCLOSE_CHECK(newFH);
            }

            Log(LOG_WARNING, "Write error in queue");

            sprintf(path, "%s/w%s.%03d",Conf.spoolPath, entry, queue);
            UNLINK_CHECK(path);

            ProcessQueueEntryCleanUp(entryID, report);
            return(TRUE);
        }

        /* It's in the deliver queue, check if it's local and deliver it, if not, hand it off to the agents!*/
        /* By way of design,this function also removes all entries w/o any receiver from the queue */
        case Q_OUTGOING: {
            if (date < time(NULL) - Conf.maxLinger) {
                /* We move it to the Q_RTS queue and the linger code there will bounce it for us */
                sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
                sprintf(path2, "%s/c%s.%03d", Conf.spoolPath, entry, Q_RTS);
                RENAME_CHECK(path, path2);

                sprintf(path, "%03d%s", Q_RTS, entry);
                SpoolEntryIDUnlock(entryID);
                entryIn = MemStrdup(path);
                goto StartOver;
            }

            if (Conf.deferEnabled && CheckIfDeliveryDeferred()) {
                ProcessQueueEntryCleanUp(entryID, report);
                return(TRUE);
            }

            /* Fall-through to Q_DELIVER */
        }

        case Q_DELIVER: {
            int status;
            unsigned long messageFlags;
            unsigned long flags = DSN_FAILURE | DSN_HEADER | DSN_BODY;
            unsigned char *mailbox;
            unsigned char *preMailboxDelim = NULL;
            unsigned char *postMailboxDelim = NULL;
            unsigned char recipient[MAXEMAILNAMESIZE + 1];
            unsigned char sender[MAXEMAILNAMESIZE + 1];
            unsigned char authenticatedSender[MAXEMAILNAMESIZE + 1];
            unsigned char messageID[MAXEMAILNAMESIZE + 1];
            char dataFilename[XPL_MAX_PATH];
            NMAPConnections list = { 0, };

            data = NULL;
            saddr.sin_addr.s_addr = 0;

            sprintf(dataFilename, "d%s.msg", entry);

            if (!dSize) {
                sprintf(path, "%s/%s", Conf.spoolPath, dataFilename);
                if (stat(path, &sb)) {
                    ProcessQueueEntryCleanUp(entryID, report);
                    return(TRUE);
                }

                dSize = (unsigned long)sb.st_size;
            }

            keep = FALSE;
            bounce = FALSE;

            sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
            FOPEN_CHECK(fh, path, "rb");

            sprintf(path, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
            FOPEN_CHECK(newFH, path, "wb");

            if (!fh || !newFH) {
                if (fh) {
                    FCLOSE_CHECK(fh);
                }

                if (newFH) {
                    FCLOSE_CHECK(newFH);
                }

                sprintf(path, "%s/w%s.%03d",Conf.spoolPath, entry, queue);
                UNLINK_CHECK(path);

                ProcessQueueEntryCleanUp(entryID, report);
                return(TRUE);
            }

            while (!feof(fh) && !ferror(fh)) {
                if (fgets(line, CONN_BUFSIZE, fh)) {
                    CHOP_NEWLINE(line);

                    mailbox = "INBOX";
                    messageFlags = 0;

                    switch(line[0]) {
                        case QUEUE_FROM: {
                            /* sender  */
                            ptr = strchr(line + 1, ' ');
                            if (ptr) {
                                *ptr = '\0';
                                ptr2 = strchr(ptr + 1, ' ');
                            }

                            strncpy(sender, line + 1, MAXEMAILNAMESIZE);
                            if (ptr) {
                                if (ptr2) {
                                    *ptr2 = '\0';
                                }

                                strncpy(authenticatedSender, ptr + 1, MAXEMAILNAMESIZE);
                                if (ptr2) {
                                    strcpy(messageID, ptr2 + 1);
                                    *ptr2 = ' ';
                                }
                                *ptr = ' ';
                            } else {
                                authenticatedSender[0]='-';
                                authenticatedSender[1]='\0';
                            }
                    
                            fprintf(newFH, "%s\r\n", line);
                            break;
                        }

                        case QUEUE_CALENDAR_LOCAL: {
                            struct sockaddr_in    siaddr;

                            if (!data) {
                                FOPEN_CHECK(data, path, "rb");
                                if (!data) {
                                    FCLOSE_CHECK(fh);
                                    sprintf(path, "%s/w%s.%03d",Conf.spoolPath, entry, queue);
                                    FCLOSE_CHECK(newFH);
                                    UNLINK_CHECK(path);
                                    ProcessQueueEntryCleanUp(entryID, report);
                                    return(TRUE);
                                }
                            }

                            /* First arg is recipient name, second arg is calendar name; third would be flags */
                            ptr = strchr(line+1, ' ');
                            if (ptr) {
                                *ptr = '\0';
                                mailbox = ptr + 1;
                                ptr2 = strchr(ptr + 1, ' ');
                                if (ptr2) {
                                    *ptr2 = '\0';
                                    flags = atol(ptr2 + 1);
                                }
                            } else {
                                ptr2 = NULL;
                                mailbox = "MAIN";
                            }
                            strcpy(recipient, line + 1);
                            if (ptr) {
                                *ptr = ' ';
                            }

                            if (MsgAuthFindUser(recipient) != 0) {
                                Log(LOG_WARNING, "User %s unknown, entry %ld", recipient, entryID);
                                status = DELIVER_USER_UNKNOWN;
                            } else {
                                MsgAuthGetUserStore(recipient, &siaddr);
                                Log(LOG_DEBUG, "Delivering %s on queue %d to %s", entry, queue, line+1);
                                status = DeliverToStore(&list, &siaddr, NMAP_DOCTYPE_CAL, sender, authenticatedSender, dataFilename, data, dSize, recipient, mailbox, flags);
                                if (Agent.agent.state == BONGO_AGENT_STATE_STOPPING) {
                                    status = DELIVER_TRY_LATER;
                                }
                            }

                            /* Restore our buffer */
                            if (ptr2) {
                                *ptr2 = ' ';
                            }

                            if (status<DELIVER_SUCCESS) {
                                Log(LOG_NOTICE, "Delivery failed: entry %ld, queue %d, status %d", entryID, queue, status);
                                if (status==DELIVER_USER_UNKNOWN || status==DELIVER_INTERNAL_ERROR || status==DELIVER_QUOTA_EXCEEDED) {
                                    XplSafeIncrement(Queue.localDeliveryFailed);
                                    if (flags & DSN_FAILURE) {
                                        fprintf(newFH, QUEUES_BOUNCE"%s %s %lu %d\r\n", recipient, recipient, flags, status);
                                        bounce=TRUE;
                                    }
                                } else {
                                    fprintf(newFH,"%s\r\n",line);
                                    keep=TRUE; 
                                }
                            } else {
                                if (status==DELIVER_SUCCESS) {
                                    if (flags & DSN_SUCCESS) {
                                        fprintf(newFH, QUEUES_BOUNCE"%s %s %lu %d\r\n", recipient, recipient, flags, DELIVER_SUCCESS);
                                        bounce=TRUE;
                                    }
                                }
                            }

                            fseek(data, 0, SEEK_SET);
                            break;
                        }

                        case QUEUE_RECIP_MBOX_LOCAL: {    /* Local w/ Mailbox; syntax: MRecip ORecip flags MBox MsgFlags */
                            i = 0;
                            ptr=line;
                            while (ptr[0]!='\0') {
                                if (ptr[0]==' ') {
                                    i++;
                                   if (i > 2) {
                                        switch(i) {
                                            case 3: {
                                                preMailboxDelim = ptr;
                                                *preMailboxDelim = '\0';
                                                mailbox = ptr + 1;
                                                break;
                                            }
                                            case 4: {
                                                postMailboxDelim = ptr;
                                                *postMailboxDelim = '\0';
                                                messageFlags = atol(ptr + 1);
                                                break;
                                            }
                                        }
                                    }
                                }
                                ptr++;
                            }
                        }
                        /**** FALLTHROUGH ****/

                        case QUEUE_RECIP_LOCAL: { /* Local */
                            struct sockaddr_in siaddr;
                            if (!data) {
                                sprintf(path, "%s/d%s.msg", Conf.spoolPath, entry);
                                FOPEN_CHECK(data, path, "rb");
                                if (!data) {
                                    FCLOSE_CHECK(fh);
                                    FCLOSE_CHECK(newFH);

                                    sprintf(path, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
                                    UNLINK_CHECK(path);

                                    ProcessQueueEntryCleanUp(entryID, report);
                                    return(TRUE);
                                }
                            }
                
                            ptr = strchr(line + 1, ' ');
                            if (ptr) {
                                *ptr = '\0';
                                ptr2 = strchr(ptr + 1, ' ');
                                if (ptr2) {
                                    *ptr2 = '\0';
                                    flags = atol(ptr2 + 1);
                                }
                            }

                            strcpy(recipient, line + 1);
                            if (ptr) {
                                *ptr = ' ';
                                if (ptr2)
                                    *ptr2 = ' ';
                            }

                            /* Attempt delivery, check if local or remote */
                            if (MsgAuthFindUser(recipient) == 0) {
                                MsgAuthGetUserStore(recipient, &siaddr);
                                Log(LOG_DEBUG, "Deliver to store entry %s in queue %d for host %s", entry, queue, LOGIP(siaddr));
                                status = DeliverToStore(&list, &siaddr, NMAP_DOCTYPE_MAIL, sender, authenticatedSender, dataFilename, data, dSize, recipient, mailbox, messageFlags);

                                if (Agent.agent.state == BONGO_AGENT_STATE_STOPPING) {
                                    status = DELIVER_TRY_LATER;
                                }
                            } else {
                                status = DELIVER_USER_UNKNOWN;

                                /* This will forward the message to a remote domain */
                                XplRWReadLockAcquire(&Conf.lock);
                                if (Conf.forwardUndeliverableEnabled) {
                                    XplRWReadLockRelease(&Conf.lock);

                                    /* Handle NUA address */
                                    if ((ptr = strchr(recipient, '@')) != NULL) {
                                        *ptr = '%';
                                    }

                                    ptr = strchr(line, ' ');
                                    if (ptr) {
                                        XplRWReadLockAcquire(&Conf.lock);
                                        fprintf(newFH, "%c%s@%s%s\r\n", QUEUE_RECIP_REMOTE, recipient, Conf.forwardUndeliverableAddress, ptr);
                                        XplRWReadLockRelease(&Conf.lock);
                                    }

                                    /* We want success, but flags = 0 so we don't notify the sender (yet) */
                                    /* SMTP will take care of it if the remote system doesn support DSN */
                                    status = DELIVER_SUCCESS;
                                    flags = 0;
                                    keep = TRUE; /* recipient prevent it from getting removed */
                                } else {
                                    Log(LOG_INFO, "Can't forward undeliverable entry %ld in queue %d for user %s on host %s", entry, queue, sender, LOGIP(saddr));

                                    XplRWReadLockRelease(&Conf.lock);
                                }
                            }

                            if (status < DELIVER_SUCCESS) {
                                Log(LOG_WARNING, "Couldn't deliver entry %s on queue %d, status %d", entry, queue, status);
                                if ((status == DELIVER_USER_UNKNOWN) || (status == DELIVER_INTERNAL_ERROR) || (status == DELIVER_QUOTA_EXCEEDED)) {
                                    XplSafeIncrement(Queue.localDeliveryFailed);
                                    if (flags & DSN_FAILURE) {
                                        ptr = strchr(line, ' ');
                                        if (ptr) { /* ptr points to the origrecip */
                                            ptr = strchr(ptr + 1, ' ');
                                            if (ptr) { /* ptr points to flags */
                                                ptr = strchr(ptr + 1, ' ');
                                                if (ptr) { /* ptr points behind flags */
                                                    *ptr = '\0';
                                                    fprintf(newFH, QUEUES_BOUNCE"%s %d\r\n", line + 1, status);
                                                    *ptr = ' ';
                                                } else {
                                                    fprintf(newFH, QUEUES_BOUNCE"%s %d\r\n", line  +1, status);
                                                }
                                            }
                                        }

                                        bounce = TRUE;
                                    }
                                } else {
                                    if (preMailboxDelim) {
                                        *preMailboxDelim = ' ';
                                        if (postMailboxDelim) {
	                                        *postMailboxDelim = ' ';
                                        }
                                    }
                                    fprintf(newFH, "%s\r\n", line);
                                    keep = TRUE; 
                                }
                            } else {
                                if (status==DELIVER_SUCCESS) {
                                    if (flags & DSN_SUCCESS) {
                                        ptr = strchr(line, ' ');
                                        if (ptr) { /* ptr points to the origrecip */
                                            ptr = strchr(ptr + 1, ' ');
                                            if (ptr) { /* ptr points to flags */
                                                ptr = strchr(ptr + 1, ' ');
                                                if (ptr) { /* ptr points behind flags */
                                                    *ptr = '\0';
                                                    fprintf(newFH, QUEUES_BOUNCE"%s %d\r\n", line + 1, DELIVER_SUCCESS);
                                                    *ptr = ' ';
                                                } else {
                                                    fprintf(newFH, QUEUES_BOUNCE"%s %d\r\n", line + 1, DELIVER_SUCCESS);
                                                }
                                            }
                                        }

                                        bounce = TRUE;
                                    }
                                }
                            }

                            fseek(data, 0, SEEK_SET);
                            break;
                        }

                        case QUEUE_RECIP_REMOTE: {
                            /* Remote */
                            fprintf(newFH, "%s\r\n", line);
                            keep = TRUE;
                            break;
                        }

                        case QUEUE_BOUNCE: {
                            /* Have bounced entry */
                            fprintf(newFH, "%s\r\n", line);
                            bounce = TRUE;
                            break;                            
                        }

                        case QUEUE_ADDRESS: {
                            /* Address associated with 'Mail sender' */
                            fprintf(newFH, "%s\r\n", line);
                            saddr.sin_addr.s_addr = atol(line + 1);
                            break;
                        }

                        case QUEUE_DATE: {
                            fprintf(newFH, "%s\r\n", line);
                            break;
                        }

                        case QUEUE_FLAGS: {
                            fprintf(newFH, "%s\r\n", line);
                            break;
                        }

                        case QUEUE_THIRD_PARTY: {
                            fprintf(newFH, "%s\r\n", line);
                            break;
                        }

                        default:
                            Log(LOG_INFO, "Unknown command: %s (entry: %ld)", line, entryID);
                            fprintf(newFH, "%s\r\n", line);
                            break;
                    }
                }
            }
            if (list.allocated) {
                EndStoreDelivery(&list);
                memset(&list, 0, sizeof(NMAPConnections));
            }
            FCLOSE_CHECK(newFH);
            FCLOSE_CHECK(fh);
            if (bounce) {
                unsigned char    Path2[XPL_MAX_PATH+1];

                /* First, rename the work file into a control file */
                sprintf(path, "%s/c%s.%03d",Conf.spoolPath, entry, queue);
                UNLINK_CHECK(path);
                sprintf(Path2, "%s/w%s.%03d",Conf.spoolPath, entry, queue);
                RENAME_CHECK(Path2, path);

                if (!data) {
                    sprintf(path, "%s/d%s.msg",Conf.spoolPath, entry);
                    FOPEN_CHECK(data, path, "rb");
                } else {
                    fseek(data, 0, SEEK_SET);    
                }

                sprintf(path, "%s/c%s.%03d",Conf.spoolPath, entry, queue);
                FOPEN_CHECK(fh, path, "rb");
        
                if (fh && data && 0 == HandleDSN(data, fh)) {
                    /* Now bounce the thing */
                    fseek(fh, 0, SEEK_SET);

                    sprintf(path, "%s/w%s.%03d",Conf.spoolPath, entry, queue);
                    FOPEN_CHECK(newFH, path, "wb");
                    if (newFH) {
                        /* Now remove the bounced entries */
                        while (!feof(fh) && !ferror(fh)) {
                            if (fgets(line, sizeof(line), fh)) {
                                switch(line[0]) {
                                    case QUEUE_BOUNCE: {
                                        break;
                                    }
                                    default: {
                                        fputs(line, newFH);
                                    }
                                }
                            }
                        }
                        FCLOSE_CHECK(newFH);
                    }
                    FCLOSE_CHECK(fh);
                } else {
                    if (fh) {
                        FCLOSE_CHECK(fh);
                    }
                    if (data) {
                        FCLOSE_CHECK(data);
                    }

                    Log(LOG_CRITICAL, "File open error for entry %ld, path: %s", entryID, path);
                    ProcessQueueEntryCleanUp(entryID, report);
                    return(FALSE);
                }                
            }
            if (data) {
                FCLOSE_CHECK(data);
            }
            if (keep) {
                unsigned char    Path2[XPL_MAX_PATH+1];

                sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
                UNLINK_CHECK(path);

                sprintf(Path2, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
                RENAME_CHECK(Path2, path);
            } else {
                sprintf(path, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
                UNLINK_CHECK(path);
        
                sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
                UNLINK_CHECK(path);

                sprintf(path, "%s/d%s.msg", Conf.spoolPath, entry);
                UNLINK_CHECK(path);

                XplSafeDecrement(Queue.queuedLocal);
                ProcessQueueEntryCleanUp(entryID, report);
                return(TRUE);
            }
        }
        default: {
            break;
        }
    }

    if (Agent.agent.state < BONGO_AGENT_STATE_STOPPING) {
        sprintf(path, "%s/d%s.msg", Conf.spoolPath, entry);
    } else {
        ProcessQueueEntryCleanUp(entryID, report);
        return(TRUE);
    }

    if (stat(path, &sb) == 0) {
        dSize = (unsigned long)sb.st_size;
    } else {
        ProcessQueueEntryCleanUp(entryID, report);
        return(TRUE);
    }

    /* i want to get the current queue and iterate over its pools if there are any */
    tempQueue.queue = queue;
    ItemIndex = GArrayFindSorted(Queue.PushClients.queues, &p_tempQueue, FindQueue);
    if (ItemIndex >= 0) {
        QueueList *CurrentQueue = g_array_index(Queue.PushClients.queues, QueueList *, ItemIndex);
        for (iter_p=0; iter_p < CurrentQueue->pools->len; iter_p++) {
            QueuePoolList *CurrentPool = g_array_index(CurrentQueue->pools, QueuePoolList *, iter_p);

            sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
            stat(path, &sb);
            FOPEN_CHECK(fh, path, "rb");
            if (!fh) {
                ProcessQueueEntryCleanUp(entryID, report);
                return(TRUE);
            }
            do {
                if (fgets(line, CONN_BUFSIZE, fh) && (
                        (line[0] == QUEUE_RECIP_REMOTE) || (line[0] == QUEUE_RECIP_LOCAL) || (line[0] == QUEUE_RECIP_MBOX_LOCAL))) {
                    lines++;
                }
            } while (!feof(fh) && !ferror(fh));

            fseek(fh, 0, SEEK_SET);

            /* set up the QueueClient */
            client = QueueClientAlloc();
            if (!client) {
                LogFailureF("Cannot allocate %d bytes memory (entry %ld)", sizeof(QueueClient), entryID);
                FCLOSE_CHECK(fh);
                fh=NULL;
                continue;
            }
            memset(client, 0, sizeof(QueueClient));
            client->authorized = TRUE;

            /* connect to the waiting client */
            client->conn = ConnAddressPoolConnect(&(CurrentPool->pool), 60000);

            if (!client->conn) {
                /* FIXME: do we want to remove the whole pool at this point? */
                LogFailureF("All agents in pool %s failed (entry %ld)", CurrentPool->identifier, entryID);
                FCLOSE_CHECK(fh);
                fh=NULL;
                QueueClientFree(client);
                continue;
            }
            /* disable the nagle algorithm */
            {
                int opt = 1;
                setsockopt(client->conn->socket, IPPROTO_TCP, 1, &opt, sizeof(opt));
            }

            Log(LOG_DEBUG, "Handing off to agent queue/pool %d/%s", queue, CurrentPool->identifier);
            ConnWriteF(client->conn, "6020 %03d-%s %ld %ld %ld\r\n", queue, entry, (unsigned long)sb.st_size, dSize, lines);
            ConnWriteFile(client->conn, fh);

            FCLOSE_CHECK(fh);
            fh=NULL;

            sprintf(client->entry.workQueue, "%03d-%s", queue, entry);
            client->entry.workQueue[3] = '\0';
            ConnWriteStr(client->conn, "6021 Get busy!\r\n");
            ConnFlush(client->conn);

            client->entry.report = report;

            if (!HandleCommand(client)) {
                /* log message here ?? */
            }

            if (client->entry.work) {
                Log(LOG_DEBUG, "Ran into queue resend [C%s.%03d] Last Command: %s", entry, queue, client->buffer);
                FCLOSE_CHECK(client->entry.work);
                client->entry.work = NULL;
                sprintf(client->path,"%s/w%s.%03d", Conf.spoolPath, entry, queue);
                remove(client->path);
            }

            if (client->conn) {
                ConnClose(client->conn);
                ConnFree(client->conn);
                client->conn = NULL;
            }

            /* fixme - evaluate this section. */
            if (report == client->entry.report) {
                ;
            } else {
                if (report == NULL) {
                    report = client->entry.report;
                } else {
                    ProcessQueueEntryCleanUp(entryID, NULL);
                    return(FALSE);
                }
            }

            QueueClientFree(client);
            if (access(path, 0)) {
                XplSafeDecrement(Queue.queuedLocal);
                ProcessQueueEntryCleanUp(entryID, report);
                return(TRUE);
            }
        }
    }

    if (Agent.agent.state == BONGO_AGENT_STATE_STOPPING) {
        ProcessQueueEntryCleanUp(entryID, report);
        return(TRUE);
    }

    switch(queue) {
        case Q_INCOMING:
        case Q_INCOMING_CLEAN:
        case Q_FORWARD:
        case Q_RULE:
        case Q_FOUR:
        case Q_FIVE:
        case Q_DELIVER: {
            /* i need to take the next queue that has something in it.  Since we are sorted, if ItemIndex is we can bypass the find stuff
             * and take the next item in the array!! */
            if (ItemIndex >= 0) {
                /* i can safely cast the int here to an unsigned as i know that it is positive */
                if ((unsigned int)++ItemIndex < Queue.PushClients.queues->len) {
                    CurrentQueue = g_array_index(Queue.PushClients.queues, QueueList *, ItemIndex);
                    i = CurrentQueue->queue;
                } else {
                    /* there are no more agents, we can just jump straight to Q_OUTGOING */
                    i = Q_OUTGOING;
                }
            } else {
                /* there was no item for the current queue, I need to search for the next queue greater than the one i was in.  HOpefully
                 * this will only get hit for items that are created in a queue that has no agents in it, I don't really see an efficient
                 * way of finding this out without looping over the whole array */
                i = Q_OUTGOING;
                /* i can safely cast here as ItemIndex is never going to be less than 0 */
                for (ItemIndex=0;(unsigned int)ItemIndex<Queue.PushClients.queues->len;ItemIndex++) {
                    CurrentQueue = g_array_index(Queue.PushClients.queues, QueueList *, ItemIndex);
                    if (CurrentQueue->queue > queue) {
                        i = CurrentQueue->queue;
                        break;
                    }
                }
            }

            Log(LOG_DEBUG, "Moving %ld from queue %d to queue %d", entryID, queue, i);
            sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
            sprintf(path2, "%s/c%s.%03d", Conf.spoolPath, entry, i);
            RENAME_CHECK(path, path2);

            sprintf(path, "%03d%s", i, entry);
            SpoolEntryIDUnlock(entryID);

            entryIn = MemStrdup(path);
            goto StartOver;
            break;
        }

        case Q_OUTGOING: {
            /* Check if there are any recipients left */
            sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);

            if ((handle = QDBHandleAlloc()) != NULL) {
               QDBRemoveID(handle, entryID);

               QDBHandleRelease(handle);
            }

            FOPEN_CHECK(fh, path, "rb");
            keep = FALSE;
            if (fh) {
                do {
                    if (fgets(line, CONN_BUFSIZE, fh)!=NULL) {
                        switch(line[0]) {
                            case QUEUE_BOUNCE: {
                                bounce = TRUE;
                                break;
                            }

                            case QUEUE_CALENDAR_LOCAL:
                            case QUEUE_RECIP_LOCAL:
                            case QUEUE_RECIP_MBOX_LOCAL: {
                                keep = TRUE;
                                break;
                            }

                            case QUEUE_RECIP_REMOTE: {
                                int ccode;

                                ptr = strchr(line + 1, ' ');
                                if (ptr) {
                                    *ptr = '\0';
                                }

                                ptr2 = strchr(line + 1, '@');
                                if (ptr2) {
                                    if ((handle = QDBHandleAlloc()) != NULL) {
                                        ccode = QDBAdd(handle, ptr2 + 1, entryID, queue);
                                        LogAssertF(ccode != -1, "Database insert error: %s", ptr2 + 1);

                                        QDBHandleRelease(handle);
                                    }
                                }

                                if (ptr) {
                                    *ptr = ' ';
                                }

                                keep = TRUE;
                                break;
                            }
                        }
                    }
                } while (!feof(fh) && !ferror(fh));
                FCLOSE_CHECK(fh);
            } else {
                keep = TRUE;
            }

            /* Note that we make sure to keep the content of path pointing to the control file; this way we only need one sprintf */
            if (bounce) {
                /* Call the bouncing code */
                sprintf(path2, "%s/d%s.msg", Conf.spoolPath, entry);
                FOPEN_CHECK(data, path2, "rb");
                FOPEN_CHECK(fh, path, "rb");

                if (fh && data && 0 == HandleDSN(data, fh)) {
                    FCLOSE_CHECK(data);

                    /* If we're not keeping the file, we can ignore its contents */
                    if (keep) {
                        sprintf(path2, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
                        FOPEN_CHECK(newFH, path2, "wb");
                        if (newFH) {
                            /* Now remove the bounced entries */
                            while (!feof(fh) && !ferror(fh)) {
                                if (fgets(line, CONN_BUFSIZE, fh)) {
                                    switch(line[0]) {
                                        case QUEUE_BOUNCE: {
                                            break;
                                        }

                                        default: {
                                            fputs(line, newFH);
                                            break;
                                        }
                                    }
                                }
                            }

                            FCLOSE_CHECK(fh);
                            FCLOSE_CHECK(newFH);

                            UNLINK_CHECK(path);
                            RENAME_CHECK(path2, path);
                        } else {
                            FCLOSE_CHECK(fh);
                        }
                    } else {
                        FCLOSE_CHECK(fh);
                    }
                } else {
                    if (fh) {
                        FCLOSE_CHECK(fh);
                    }

                    if (data) {
                        FCLOSE_CHECK(data);
                    }

                    LogFailureF("File open failure: entry %ld, path %s", entryID, path);

                    ProcessQueueEntryCleanUp(entryID, report);
                    return(TRUE);
                }
            }

            if (!keep) {
                XplSafeDecrement(Queue.queuedLocal);

                UNLINK_CHECK(path);

                sprintf(path, "%s/d%s.msg",Conf.spoolPath, entry);
                UNLINK_CHECK(path);

                break;
            }

            break;
        }

        case Q_RTS: {
            if (date < time(NULL) - Conf.maxLinger) {
                if ((handle = QDBHandleAlloc()) != NULL) {
                    QDBRemoveID(handle, entryID);

                    QDBHandleRelease(handle);
                }

                sprintf(path, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
                FOPEN_CHECK(newFH, path, "wb");

                sprintf(path, "%s/d%s.msg", Conf.spoolPath, entry);
                FOPEN_CHECK(data, path, "rb");

                sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
                FOPEN_CHECK(fh, path, "rb");

                if (fh && newFH && data) {
                    XplSafeIncrement(Queue.remoteDeliveryFailed);

                    /* Write the new control file, containing the bounces */
                    while (!feof(fh) && !ferror(fh)) {
                        if (fgets(line, sizeof(line), fh)) {
                            switch(line[0]) {
                                case QUEUE_CALENDAR_LOCAL: {
                                    /* We don't bounce those yet */
                                    break;
                                }

                                case QUEUE_RECIP_LOCAL:
                                case QUEUE_RECIP_REMOTE:
                                case QUEUE_RECIP_MBOX_LOCAL: {
                                    ptr = strchr(line, ' ');
                                    if (ptr) {
                                        /* ptr points to the origrecip */
                                        ptr = strchr(ptr + 1, ' ');
                                        if (ptr) {
                                            /* ptr points to flags */
                                            ptr = strchr(ptr + 1, ' ');
                                            if (ptr) {
                                                /* ptr points behind flags */
                                                *ptr = '\0';
                                                fprintf(newFH, QUEUES_BOUNCE"%s %d\r\n", line + 1, DELIVER_TOO_LONG);
                                                *ptr = ' ';
                                            } else {
                                                fprintf(newFH, QUEUES_BOUNCE"%s %d\r\n", line + 1, DELIVER_TOO_LONG);
                                            }
                                        }
                                    }
                                    break;
                                }

                                default: {
                                    fputs(line, newFH);
                                    break;
                                }
                            }
                        }
                    }

                    FCLOSE_CHECK(newFH);
                    FCLOSE_CHECK(fh);

                    /* Still got path from above */
                    UNLINK_CHECK(path);
                    sprintf(path2, "%s/w%s.%03d", Conf.spoolPath, entry, queue);
                    RENAME_CHECK(path2, path);

                    FOPEN_CHECK(fh, path, "rb");
                    if (fh) {
                        HandleDSN(data, fh);
                        FCLOSE_CHECK(fh);
                    } else {
                        LogFailureF("Couldn't open path %s (%ld)", path, entryID);
                    }

                    FCLOSE_CHECK(data);

                    sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, queue);
                    UNLINK_CHECK(path);

                    sprintf(path, "%s/d%s.msg", Conf.spoolPath, entry);
                    UNLINK_CHECK(path);

                    XplSafeDecrement(Queue.queuedLocal);
                } else {
                    if (data) {
                        FCLOSE_CHECK(data);
                    }

                    if (fh) {
                        FCLOSE_CHECK(fh);
                    }

                    if (newFH) {
                        FCLOSE_CHECK(newFH);
                    }
                }

                ProcessQueueEntryCleanUp(entryID, report);
                return(TRUE);
            } else {
                count = 0;
                sprintf(path, "%s/c%s.%03d", Conf.spoolPath, entry, Q_RTS);
                sprintf(path2, "%s/c%s.%03d", Conf.spoolPath, entry, Q_INCOMING);
                RENAME_CHECK(path, path2);

                sprintf(path, "%03d%s", Q_INCOMING, entry);
            }

            SpoolEntryIDUnlock(entryID);
            entryIn = MemStrdup(path);
            goto StartOver;
            break;
        }

        case Q_DIGEST: {
            /* if message in here older than Conf.maxLinger, removed */
            break;
        }

        default: {
            break;
        }
    }

    ProcessQueueEntryCleanUp(entryID, report);

    return(TRUE);
}

static BOOL 
CreateDSNMessage(FILE *data, FILE *control, FILE *rtsData, FILE *rtsControl, BOOL force)
{
    int reason = 0;
    int oReason;
    unsigned long flags;
    unsigned long bodySize = 0;
    unsigned long maxBodySize;
    unsigned long received = 0;
    unsigned long handling;
    unsigned char *ptr;
    unsigned char *ptr2;
    unsigned char *transcript;
    unsigned char timeLine[80];
    unsigned char line[CONN_BUFSIZE + 1];
    unsigned char postmaster[MAXEMAILNAMESIZE + 1];
    unsigned char sender[MAXEMAILNAMESIZE + 1];
    unsigned char aSender[MAXEMAILNAMESIZE + 1] = "";
    unsigned char recipient[MAXEMAILNAMESIZE + 1] = "";
    unsigned char oRecipient[MAXEMAILNAMESIZE + 1] = "";
    unsigned char envID[128] = "";
    BOOL mBounce=FALSE;
    BOOL header;
    time_t now;

    /* Step 0, check if we want to bounce at all */
    now = time(NULL);

    /* Spam block */
    if (Conf.bounceBlockSpam && !force) {
        if (Queue.lastBounce >= now - Conf.bounceInterval) {
            Queue.lastBounce = now;
            Queue.bounceCount++;
            if (Queue.bounceCount > Conf.bounceMax) {
                /* We don't want the bounce, probably spam */
                XplSafeIncrement(Queue.totalSpam);
                return(FALSE);
            }
        } else {
            Queue.lastBounce = now;
            Queue.bounceCount = 0;
        }
    }

    /* Step 1, grab info from original control file */
    oReason = 0;
    do {
        if (fgets(line, CONN_BUFSIZE, control)) {
            switch(line[0]) {
                case QUEUE_BOUNCE: {
                    /* Syntax: BRecip ORecip DSN failure reason transcript */
                    oReason = reason;
                    if (sscanf(line + 1, "%s %s %lu %d", recipient, oRecipient, &flags, &reason) == 4) {
                        if (!mBounce && (oReason != 0) && (oReason != reason)) {
                            mBounce = TRUE;
                            reason = 0;
                        }
                    }

                    break;
                }

                case QUEUE_FROM: {
                    ptr = strchr(line + 1,' ');
                    if (ptr) {
                        *ptr = '\0';
                        ptr2 = strchr(ptr+1,' ');
                        if (ptr2) {
                            *ptr2 = '\0';
                            strncpy(envID, ptr2, 127);
                        }

                        strcpy(aSender, ptr + 1);
                    }

                    strcpy(sender, line + 1);
                    if ((sender[0] == '-') && (sender[1] == '\0')) {
                        /* We don't bounce bounces */
                        return(FALSE);
                    }

                    break;
                }

                case QUEUE_DATE: {
                    received = atol(line + 1);
                    break;
                }
            }
        }
    } while (!feof(control) && !ferror(control));

    if (recipient[0] == '\0') {
        return(FALSE);
    }

    sprintf(postmaster, "%s@%s", BongoGlobals.postmaster, BongoGlobals.hostname);
    handling = Conf.bounceHandling;

    /* We're guaranteed to have a recipient */
/* TODO: fix postmaster */
    
// REMOVE-MDB
#if 0
    ptr = strchr(recipient, '@');
    if (ptr) {
        if (MsgDomainExists(ptr + 1, dn)) {
            vs = MDBCreateValueStruct(Agent.agent.directoryHandle, NULL);
            if (MDBRead(dn, MSGSRV_A_POSTMASTER, vs) > 0) {
                ptr2 = strrchr(vs->Value[0], '\\');
                if (ptr2) {
                    sprintf(postmaster, "%s@%s", ptr2 + 1, ptr + 1);
                }

                MDBFreeValues(vs);
            }

	    if (MDBRead(dn, MSGSRV_A_BOUNCE_RETURN, vs)>0) {
		if (atol(vs->Value[0])) {
		    handling |= BOUNCE_RETURN;
		} else {
		    handling &= ~BOUNCE_RETURN;
		}
	    }
	    MDBFreeValues(vs);
	    if (MDBRead(dn, MSGSRV_A_BOUNCE_CC_POSTMASTER, vs)>0) {
		if (atol(vs->Value[0])) {
		    handling |= BOUNCE_CC_POSTMASTER;
		} else {
		    handling &= ~BOUNCE_CC_POSTMASTER;
		}
	    }
	    MDBFreeValues(vs);
            MDBDestroyValueStruct(vs);
        }
    }
#endif

    /* Step 2, create the bounce; we've got all information */
    MsgGetRFC822Date(-1, 0, timeLine);

    fprintf(rtsData, "Date: %s\r\n", timeLine);
    fprintf(rtsData, "From: Mail Delivery System <%s>\r\n", postmaster);
    fprintf(rtsData, "Message-Id: <%lu-%lu@%s>\r\n", now, (long unsigned int)XplGetThreadID(), BongoGlobals.hostname);
    fprintf(rtsData, "To: <%s>\r\n", sender);
    fprintf(rtsData, "MIME-Version: 1.0\r\nContent-Type: multipart/report; report-type=delivery-status;\r\n\tboundary=\"%lu-%lu-%s\"\r\n", now, (long unsigned int)XplGetThreadID(), BongoGlobals.hostname);
    switch (reason) {
        case DELIVER_FAILURE:           fprintf(rtsData, "Subject: Returned mail: Delivery failure\r\n");               break;
        case DELIVER_HOST_UNKNOWN:      fprintf(rtsData, "Subject: Returned mail: Host unknown\r\n");                   break;
        case DELIVER_BOGUS_NAME:        fprintf(rtsData, "Subject: Returned mail: Unrecognized address\r\n");           break;
        case DELIVER_TIMEOUT:           fprintf(rtsData, "Subject: Returned mail: Remote host unreachable\r\n");        break;
        case DELIVER_REFUSED:           fprintf(rtsData, "Subject: Returned mail: Delivery refused\r\n");               break;
        case DELIVER_UNREACHABLE:       fprintf(rtsData, "Subject: Returned mail: Remote host unreachable\r\n");        break;
        case DELIVER_LOCKED:            fprintf(rtsData, "Subject: Returned mail: Mailbox locked\r\n");                 break;
        case DELIVER_USER_UNKNOWN:      fprintf(rtsData, "Subject: Returned mail: User unknown\r\n");                   break;
        case DELIVER_HOPCOUNT_EXCEEDED: fprintf(rtsData, "Subject: Returned mail: Too many hops\r\n");                  break;
        case DELIVER_TRY_LATER:         fprintf(rtsData, "Subject: Returned mail: Delivery failure\r\n");               break;
        case DELIVER_INTERNAL_ERROR:    fprintf(rtsData, "Subject: Returned mail: Unspecified error\r\n");              break;
        case DELIVER_TOO_LONG:          fprintf(rtsData, "Subject: Returned mail: Delivery time exceeded\r\n");         break;
        case DELIVER_QUOTA_EXCEEDED:    fprintf(rtsData, "Subject: Returned mail: Quota exceeded\r\n");                 break;
        case DELIVER_BLOCKED:           fprintf(rtsData, "Subject: Returned mail: Message Blocked\r\n");                break;
        case DELIVER_VIRUS_REJECT:      fprintf(rtsData, "Subject: Returned mail: Possible Virus Infection\r\n");       break;
        default:
        case DELIVER_SUCCESS:
        case 0:                         fprintf(rtsData, "Subject: Returned mail: Delivery Status Notification\r\n");   break;
    }

    fprintf(rtsData, "Precedence: bulk\r\n\r\nThis is a MIME-encapsulated message\r\n\r\n");

    /* First section, human readable */
    fprintf(rtsData, "--%lu-%lu-%s\r\nContent-type: text/plain; charset=US-ASCII\r\n\r\n", now, (long unsigned int)XplGetThreadID(), BongoGlobals.hostname);

    MsgGetRFC822Date(-1, received, timeLine);
    fprintf(rtsData, "The original message was received %s\r\nfrom %s\r\n\r\n", timeLine, aSender[0]!='\0' ? aSender : sender);

    if (!mBounce) {
        if (reason==DELIVER_SUCCESS) {
            fprintf(rtsData, "   ----- The following address(es) have been delivered -----\r\n");
        } else if (reason==DELIVER_PENDING) {
            fprintf(rtsData, "   ----- The following address(es) have temporary problems -----\r\n");
        } else {
            fprintf(rtsData, "   ----- The following address(es) had permanent fatal errors -----\r\n");
        }
    }

    /* Need to go through the file and enumerate all recipients */
    fseek(control, 0, SEEK_SET);
    do {
        if (fgets(line, CONN_BUFSIZE, control)) {
            if (line[0] == QUEUE_BOUNCE) {
                CHOP_NEWLINE(line);

                transcript = NULL;
                flags = DSN_FAILURE | DSN_HEADER;
                reason = DELIVER_INTERNAL_ERROR;
                oRecipient[0] = '\0';

                ptr = strchr(line + 1, ' ');
                if (ptr) {
                    /* Grab the original recipient */
                    *ptr='\0';
                    ptr++;
                    ptr2 = strchr(ptr, ' ');
                    if (ptr2) {
                        *ptr2 = '\0';
                        strcpy(oRecipient, ptr);
                        ptr2++;
                        flags = (atol(ptr2) & DSN_FLAGS);
                        ptr = strchr(ptr2, ' ');
                        if (ptr) {
                            reason = atol(ptr + 1);
                            ptr2 = strchr(ptr + 1, ' ');
                            if (ptr2) {
                                transcript = ptr2 + 1;
                            }
                        }
                    } else {
                        strcpy(oRecipient, ptr);
                    }
                }

                /* Now print the status for each */
                if (oRecipient[0] != '\0') {
                    if (reason != DELIVER_SUCCESS) {
                        fprintf(rtsData, "<%s>; originally to %s (unrecoverable error)\r\n", line + 1, (char *)oRecipient);
                    } else {
                        fprintf(rtsData, "<%s>; originally to %s\r\n", line + 1, (char *)oRecipient);
                    }
                } else if (reason != DELIVER_SUCCESS) {
                    fprintf(rtsData, "<%s> (unrecoverable error)\r\n", line + 1);
                } else {
                    fprintf(rtsData, "<%s>\r\n", line + 1);
                }

                switch (reason) {
                    case DELIVER_HOST_UNKNOWN: {
                        ptr = strchr(recipient, '@');
                        if (ptr) {
                            fprintf(rtsData, "  \tThe host '%s' is unknown or could not be looked up\r\n", ptr + 1);
                        } else {
                            fprintf(rtsData, "  \tThe host '%s' is unknown or could not be looked up\r\n", recipient);
                        }

                        break;
                    }

                    case DELIVER_TOO_LONG:
                    case DELIVER_TIMEOUT: {
                        fprintf(rtsData, "  \tThe mail system tried to deliver the message for the last %d days,\r\n\tbut was not able to successfully do so.\r\n",(int)Conf.maxLinger / 86400);
                        break;
                    }

                    case DELIVER_REFUSED: {
                        ptr = strchr(recipient, '@');
                        fprintf(rtsData, "  \tThe mail exchanger for domain '%s' refused to talk to us.\r\n", ptr+1);
                        break;
                    }

                    case DELIVER_UNREACHABLE: {
                        ptr = strchr(recipient, '@');
                        fprintf(rtsData, "  \tThe mail exchanger for domain '%s' was not reachable.\r\n", ptr+1);
                        break;
                    }

                    case DELIVER_USER_UNKNOWN: {
                        ptr = strchr(recipient, '@');
                        if (ptr) {
                            *ptr = '\0';
                            fprintf(rtsData, "  \tThe recipient '%s' is unknown at host '%s'\r\n", recipient, ptr + 1);
                            *ptr = '@';
                        } else {
                            fprintf(rtsData, "  \tThe recipient '%s' is unknown\r\n", recipient);
                        }

                        break;
                    }

                    case DELIVER_HOPCOUNT_EXCEEDED: {
                        fprintf(rtsData, "  \tThe message was routed through too many hosts.\r\n\tThis usually indicates a mail loop.\r\n");
                        break;
                    }

                    case DELIVER_QUOTA_EXCEEDED: {
                        if (Conf.quotaMessage && (Conf.quotaMessage[0] != '\0')) {
                            fprintf(rtsData, Conf.quotaMessage);
                        } else {
                            fprintf(rtsData, DEFAULT_QUOTA_MESSAGE);
                        }                              
                        break;
                    }

                    case DELIVER_SUCCESS: {
                        fprintf(rtsData, "  \tThe message has been successfully delivered.\r\n");
                        break;
                    }

                    case DELIVER_LOCKED:
                    case DELIVER_FAILURE:
                    case DELIVER_TRY_LATER:
                    case DELIVER_INTERNAL_ERROR:
                    default: {
                        fprintf(rtsData, "  \tThe mail system encountered a delivery failure, code %d.\r\n", reason);
                        fprintf(rtsData, "  \tThis failure could be due to circumstances out of its control,\r\n");
                        fprintf(rtsData, "  \tplease check the transcript for details\r\n");
                        break;
                    }
                }

                if (transcript) {
                    fprintf(rtsData, "  \t  ----- transcript of session follows -----\r\n");
                    ptr = transcript;
                    while ((ptr = strchr(ptr, '\\'))!=NULL) {
                        switch(*(ptr + 1)) {
                            case 'r': {
                                memmove(ptr, ptr + 1, strlen(ptr));
                                *ptr = '\r';
                                break;
                            }

                            case 'n': {
                                memmove(ptr, ptr + 1, strlen(ptr));
                                *ptr = '\n';
                                break;
                            }
                        }

                        ptr++;
                    }

                    fprintf(rtsData, "%s", transcript);
                }

                fprintf(rtsData, "\r\n");
            }
        }
    } while (!feof(control) && !ferror(control));

    /* Second section, computer readable */
    fprintf(rtsData, "--%lu-%lu-%s\r\nContent-type: message/delivery-status\r\n\r\n", now, (long unsigned int)XplGetThreadID(), BongoGlobals.hostname);

    /* Per message fields */
    if (envID[0]!='\0') {
        fprintf(rtsData, "Original-Envelope-Id: %s\r\n", envID);
    }

    fprintf(rtsData, "Reporting-MTA: dns; %s\r\n", BongoGlobals.hostname);

    MsgGetRFC822Date(-1, received, timeLine);
    fprintf(rtsData, "Arrival-Date: %s\r\n", timeLine);

    /* Per recipient fields */
    /* Need to go through the file and enumerate all recipients */
    fseek(control, 0, SEEK_SET);
    do {
        if (fgets(line, CONN_BUFSIZE, control)) {
            if (line[0] == QUEUE_BOUNCE) {
                CHOP_NEWLINE(line);

                flags = DSN_FAILURE | DSN_HEADER;
                reason = DELIVER_INTERNAL_ERROR;
                oRecipient[0] = '\0';

                ptr = strchr(line + 1, ' ');
                if (ptr) {
                    /* Grab the original recipient */
                    *ptr = '\0';
                    ptr++;
                    ptr2 = strchr(ptr, ' ');
                    if (ptr2) {
                        *ptr2 = '\0';
                        strcpy(oRecipient, ptr);
                        ptr2++;
                        flags = (atol(ptr2) & DSN_FLAGS);
                        ptr = strchr(ptr2, ' ');
                        if (ptr) {
                            reason = atol(ptr + 1);
                        }
                    } else {
                        strcpy(oRecipient, ptr);
                    }
                }

                /* Each per recipient block in DSN requires CRLF */
                fprintf(rtsData, "\r\n"); 

                if (oRecipient[0] != '\0') {
                    fprintf(rtsData, "Original-recipient: %s\r\n", oRecipient);
                }

                fprintf(rtsData, "Final-recipient: %s\r\n", line + 1);
                if (reason == DELIVER_SUCCESS) {
                    fprintf(rtsData, "Action: delivered\r\n");
                    fprintf(rtsData, "Status: 2.0.0\r\n");
                } else if (reason == DELIVER_PENDING) {
                    fprintf(rtsData, "Action: delayed\r\n");
                    fprintf(rtsData, "Status: 4.0.0\r\n");
                } else {
                    /* fixme - this could be smarter, use the status code */
                    fprintf(rtsData, "Action: failed\r\n");
                    fprintf(rtsData, "Status: 5.0.0\r\n");
                }
            }
        }
    } while (!feof(control) && !ferror(control));

    /* Third section, original message */
    fprintf(rtsData, "--%lu-%lu-%s\r\nContent-type: message/rfc822\r\n\r\n", now, (long unsigned int)XplGetThreadID(), BongoGlobals.hostname);

    header = TRUE;
    maxBodySize = -1;
    while (!feof(data) && !ferror(data)) {
        if ((ptr = fgets(line, CONN_BUFSIZE, data)) != NULL) {
            if (header) {
                if((line[0] == '\r') && (line[1] == '\n') && (line[2] == 0)) {
                    header = FALSE;
                    if (Conf.bounceMaxBodySize) {
                        fprintf(rtsData, "\r\n<Body content limited to %lu bytes>\r\n", Conf.bounceMaxBodySize);
                        maxBodySize = Conf.bounceMaxBodySize;
                    }
                }

                if (flags & DSN_HEADER) {
                    if ((line[0] == 'F') && (line[1] == 'r') && (line[4] == ' ')) {
                        fwrite(">", sizeof(unsigned char), 1, rtsData);
                    }

                    bodySize += fwrite(line, sizeof(unsigned char), strlen(line), rtsData);
                }
            } else {
                if (flags & DSN_BODY) {
                    bodySize += fwrite(line, sizeof(unsigned char), strlen(line), rtsData);
                    if (bodySize > maxBodySize) {
                        break;
                    }
                }
            }
        }
    }

    if (!(flags & DSN_BODY)) {
        fprintf(rtsData, "-- Message body has been omitted --\r\n");
    }

    /* End of message */
    fprintf(rtsData, "\r\n--%lu-%lu-%s--\r\n", now, (long unsigned int)XplGetThreadID(), BongoGlobals.hostname);

    /* Now create the control file */
    fprintf(rtsControl, "D%lu\r\n", now);
    fprintf(rtsControl, "F- -\r\n");

    XplRWReadLockAcquire(&Conf.lock);

    if (Conf.bounceHandling & BOUNCE_RETURN) {
        fprintf(rtsControl, QUEUES_RECIP_REMOTE"%s %s 0\r\n", sender, sender);
    }

    if (Conf.bounceHandling & BOUNCE_CC_POSTMASTER) {
        fprintf(rtsControl, QUEUES_RECIP_REMOTE"%s %s 0\r\n", postmaster, postmaster);
    }

    XplRWReadLockRelease(&Conf.lock);

    return(TRUE);
}

static int
HandleDSN(FILE *data, FILE *control)
{
    unsigned long id;
    unsigned char path[XPL_MAX_PATH + 1];
    FILE *rtsControl = NULL;
    FILE *rtsData = NULL;
    XplThreadID threadID;

    XplMutexLock(Queue.queueIDLock);
    id = Queue.queueID++ & ((1 << 28) - 1);
    XplMutexUnlock(Queue.queueIDLock);

    sprintf(path, "%s/c%07lx.%03d", Conf.spoolPath, id, Q_INCOMING);

    /* Create control file */
    FOPEN_CHECK(rtsControl, path,"wb");
    if (!rtsControl) {
        fprintf (stderr, "could not open rtsControl\n");
        return -1;
    }

    SpoolEntryIDLock(id);

    /* Create data file */
    sprintf(path, "%s/d%07lx.msg", Conf.spoolPath, id);
    FOPEN_CHECK(rtsData, path, "wb");
    if (!rtsData) {
        fprintf (stderr, "could not open rtsData\n");
        FCLOSE_CHECK(rtsControl);
        sprintf(path, "%s/d%07lx.msg", Conf.spoolPath, id);
        UNLINK_CHECK(path);
        return -1;
    }

    if (!CreateDSNMessage(data, control, rtsData, rtsControl, FALSE)) {
        FCLOSE_CHECK(rtsControl);
        FCLOSE_CHECK(rtsData);

        sprintf(path, "%s/d%07lx.msg", Conf.spoolPath, id);
        UNLINK_CHECK(path);

        sprintf(path, "%s/c%07lx.%03d", Conf.spoolPath, id, Q_INCOMING);
        UNLINK_CHECK(path);

        SpoolEntryIDUnlock(id);
        return 0;
    }

    XplSafeIncrement(Queue.queuedLocal);

    FCLOSE_CHECK(rtsControl);
    FCLOSE_CHECK(rtsData);

    SpoolEntryIDUnlock(id);
    sprintf(path, "%03d%lx",Q_INCOMING, id);

    if (XplSafeRead(Queue.activeWorkers) < Conf.maxConcurrentWorkers) {
        XplBeginCountedThread(&threadID, ProcessQueueEntry, STACKSPACE_Q, MemStrdup(path), id, Queue.activeWorkers);
    }

    return 0;
}

static void
CheckQueue(void *queueIn)
{
    int ccode;
    int burst;
    int count;
    unsigned long found;
    unsigned long handled;
    unsigned char path[XPL_MAX_PATH + 1];
    time_t now;
    XplDir *dirP;
    XplDir *dirEntry;
    XplThreadID id;
    BOOL flushing;

    XplRenameThread(XplGetThreadID(), "Message-queue Monitor");

    for (count = 0; (count < 60) && (Agent.agent.state < BONGO_AGENT_STATE_STOPPING); count++) {
        XplDelay(1000);
    }

    burst = 0;
    while (Agent.agent.state < BONGO_AGENT_STATE_STOPPING) {
        found=0;
        handled=0;

        flushing = Queue.flushNeeded;
        Queue.restartNeeded = Queue.flushNeeded = FALSE;

        if (flushing) {
            Log(LOG_INFO, "Flushing the queue.");
        } else {
            now = time(NULL) - Conf.queueInterval + 60;
        }

        Log(LOG_DEBUG, "Restarting the queue");

        dirP = XplOpenDir(Conf.spoolPath);
        if (dirP == NULL) {
            goto delayQueue;
        }

        while (Agent.agent.state < BONGO_AGENT_STATE_STOPPING) {
            dirEntry = XplReadDir(dirP);
            if (!dirEntry) {
                break;
            }

            /* We don't want to work on .IN [incoming] files */
            if (strlen(dirEntry->d_nameDOS) != 12 ||
                toupper(dirEntry->d_nameDOS[0]) != 'C'||
                !isdigit(dirEntry->d_nameDOS[9])) {
                continue;
            }

            found++;
            sprintf(path, "%.3s%.7s", dirEntry->d_nameDOS + 9, dirEntry->d_nameDOS + 1);
                
            /* skip recently-touched messages that are in the outgoing
             * queue (queue 7) */
            if (!flushing && path[2] == '7' && (XplCalendarTime(dirEntry->d_cdatetime)+600) > (unsigned long)now) {
                continue;
            }

            /* wait for an empty queue slot */
            while (Agent.agent.state < BONGO_AGENT_STATE_STOPPING &&
                   XplSafeRead(Queue.activeWorkers) >= Conf.maxConcurrentWorkers) {
                XplDelay(250);
            }

            handled++;
            XplBeginCountedThread(&id, ProcessQueueEntry, STACKSPACE_Q, MemStrdup(path), ccode, Queue.activeWorkers);
            
            if (++burst > 32) {
                burst = 0;
                XplDelay(55);
            }
        }

        XplCloseDir(dirP);
        Log(LOG_DEBUG, "Entries found: %4lu, handled: %4lu, %srestart", found, handled, Queue.restartNeeded ? "Have " : "No ");

        if (flushing) {
            Log(LOG_INFO, "Finished flushing the queue.");
        }

    delayQueue:
        for (count = 0; 
             count < Conf.queueInterval &&
                 Agent.agent.state < BONGO_AGENT_STATE_STOPPING &&
                 !Queue.restartNeeded && !Queue.flushNeeded;
             count++) {
            XplDelay(1000);
        }
    }

    XplSafeDecrement(Queue.activeWorkers);

    Log(LOG_DEBUG, "Message queue monitor done.");

    XplExitThread(EXIT_THREAD, 0);

    return;
}

BOOL
QueueInit(void) 
{
    if (!InitSpoolEntryIDLocks()) {
        Log(LOG_ERROR, "Could not create spool locks.");
        return FALSE;
    }    

    XplMutexInit(Queue.queueIDLock);

    XplSafeWrite(Queue.queuedLocal, 0);
    XplSafeWrite(Queue.activeWorkers, 0);

    Queue.queueID = time(NULL) & ((1 << 28) - 1);

    XplMutexInit(Queue.PushClients.lock);
    Queue.PushClients.queues = g_array_new(FALSE, FALSE, sizeof(QueueList *));

    return TRUE;
}

void
QueueShutdown(void) 
{
    Log(LOG_DEBUG, "Writing queue agent list.");
    RemoveAllPushAgents();

    QDBShutdown();

    DeInitSpoolEntryIDLocks();
    
    XplMutexDestroy(Queue.queueIDLock);

    XplMutexDestroy(Queue.PushClients.lock);
    g_array_free(Queue.PushClients.queues, TRUE);
}

int 
CreateQueueThreads(BOOL failed)
{
    int i;
    unsigned long count;
    unsigned long total;
    unsigned long current;
    unsigned char path[XPL_MAX_PATH + 1];
    FILE *control = NULL;
    FILE *killFile = NULL;
    XplDir *dirP;
    XplDir *dirEntry;
    XplThreadID id;

    if (QDBStartup(2, 64) != 0) {
        return(-1);
    }

    if (failed) {
        Log(LOG_INFO, "System not shut down properly, verifying queue integrity.");
    } else {
        Log(LOG_DEBUG, "Verifying queue integrity, quick mode.");
    }

    sprintf(path, "%s", Conf.spoolPath);

    dirP = XplOpenDir(path);

    sprintf(path, "%s/fragfile", MsgGetDir(MSGAPI_DIR_DBF, NULL, 0));
    FOPEN_CHECK(killFile, path, "wb");
    if (!killFile) {
        if (dirP) {
            XplCloseDir(dirP);
            dirP = NULL;
        }
    }

    XplSafeWrite(Queue.activeWorkers, Conf.maxSequentialWorkers + 1);

    total = 0;
    count = 0;

    /* Count and clean the queue */
    if (dirP) {
        while ((dirEntry = XplReadDir(dirP)) != NULL) {
            if (dirEntry->d_attr & XPL_A_SUBDIR) {
                continue;
            }

            switch(dirEntry->d_nameDOS[0] & 0xDF) {
                case 'C': {        
                    if ((dirEntry->d_nameDOS[strlen(dirEntry->d_nameDOS)-1] & 0xDF) == 'N') {
                        /* Check for *.*N */
                        fprintf(killFile, "%s/c%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS + 1);
                        fprintf(killFile, "%s/d%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS + 1);

                        count += 2;
                    } else if ((dirEntry->d_nameDOS[strlen(dirEntry->d_nameDOS)-1] & 0xDF) == 'K') {
                        /* Check for *.LCK */
                        fprintf(killFile, "%s/%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS);

                        count++;
                    } else if (failed) {
                        sprintf(path, "%s/d%s", Conf.spoolPath, dirEntry->d_nameDOS + 1);
                        sprintf(path + strlen(path) - 3, "msg");
                        if (access(path, 0) == 0) {
                            /* Check for matching D file */
                            if (dirEntry->d_size <= 0) {
                                /* Check for sparse file */
                                fprintf(killFile, "%s/c%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS + 1);
                                fprintf(killFile, "%s/d%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS + 1);

                                count += 2;
                            } else {
                                /* Find the highest number and update Queue.queueID */
                                if (Queue.queueID < ((unsigned long)strtol(dirEntry->d_nameDOS + 1, NULL, 16) + 1)) {
                                    Queue.queueID = (unsigned long)strtol(dirEntry->d_nameDOS + 1, NULL, 16) + 1;
                                }
                                XplSafeIncrement(Queue.queuedLocal);
                            }
                        } else {
                            /* No matching D file */
                            fprintf(killFile, "%s/c%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS + 1);
                            count++;
                        }
                    } else {
                        /* Find the highest number and update Queue.queueID */
                        if (Queue.queueID < ((unsigned long)strtol(dirEntry->d_nameDOS + 1, NULL, 16) + 1)) {
                            Queue.queueID = strtol(dirEntry->d_nameDOS + 1, NULL, 16) + 1;
                        }
                        XplSafeIncrement(Queue.queuedLocal);
                    }

                    break;
                }

                case 'D': {
                    if (dirEntry->d_size <=0 ) {
                        /* Check for sparse file */
                        fprintf(killFile, "%s/c%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS + 1);
                        fprintf(killFile, "%s/d%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS + 1);

                        count += 2;
                    } else if (failed) {
                        sprintf(path, "%s/c%s", Conf.spoolPath, dirEntry->d_nameDOS + 1);
                        for (i = 0; i < 10; i++) {
                            sprintf(path + strlen(path) - 3, "%03d", i);
                            if (access(path, 0)==0) {
                                break;
                            }
                        }
                    
                        if (i == 10) {
                            /* Check for matching C file */
                            fprintf(killFile, "%s/d%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS + 1);

                            count++;
                        }
                    }

                    break;
                }

                case 'W': {
                    /* Remove work file */
                    fprintf(killFile, "%s/%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS);

                    count++;
                    break;
                }

                default: {
                    fprintf(killFile, "%s/%s\r\n", Conf.spoolPath, dirEntry->d_nameDOS);

                    count++;
                    break;
                }
            }
        }

        XplCloseDir(dirP);
    }

    sprintf(path, "%s", Conf.spoolPath);
    dirP = XplOpenDir(path);

    if (killFile) {
        FCLOSE_CHECK(killFile);
        killFile=NULL;
    }

    Log(LOG_DEBUG, "Queue integrity check complete, now cleaning irrelevant entries.");

    sprintf(path, "%s/fragfile", MsgGetDir(MSGAPI_DIR_DBF, NULL, 0));
    FOPEN_CHECK(killFile, path, "rb");
    if (!killFile) {
        Log(LOG_ERROR, "Could not re-open killfile.");
    } else {
        current = 0;
        while (!feof(killFile) && !ferror(killFile)) {
            if (fgets(path, sizeof(path), killFile)) {
                current++;
                CHOP_NEWLINE(path);

                UNLINK_CHECK(path);
            }
        }

        FCLOSE_CHECK(killFile);
    }

    sprintf(path, "%s/killfile", MsgGetDir(MSGAPI_DIR_DBF, NULL, 0));
    UNLINK_CHECK(path);

    XplCloseDir(dirP);

    if (failed) {
        sprintf(path, "%s", Conf.spoolPath);

        dirP = XplOpenDir(path);
        if (dirP) {
            current = 0;
            total -= count;
            if (total == 0) {
                total = 100;
            }

            while ((dirEntry=XplReadDir(dirP)) != NULL) {
                if (dirEntry->d_attr & XPL_A_SUBDIR) {
                    continue;
                }

                current++;
                if ((dirEntry->d_nameDOS[0] & 0xDF) == 'C' ) {
                    sprintf(path, "%s/c%s", Conf.spoolPath, dirEntry->d_nameDOS + 1);
                    FOPEN_CHECK(control, path, "r+b");
                    if (!control) {
                        continue;
                    }

                    fseek(control, dirEntry->d_size - 2, SEEK_SET);
                    fwrite("\r\n", 2, 1, control);
                    FCLOSE_CHECK(control);
                }
            }

            XplCloseDir(dirP);
        }
    }

    Log(LOG_DEBUG, "Queue integrity check complete, starting Queue Monitor [%d].", XplSafeRead(Queue.queuedLocal));

    XplSafeWrite(Queue.activeWorkers, 0);

    if (!Conf.debug) {
        XplBeginCountedThread(&id, CheckQueue, STACKSPACE_Q, NULL, i, Agent.activeThreads);
    }

    return(0);
}

int 
CommandQaddm(void *param)
{
    int ccode;
    int result;
    unsigned long flags = 0;
    unsigned char *sender;
    unsigned char *authSender;
    unsigned char *recipient;
    unsigned char *mailbox;
    unsigned char *queue;
    unsigned char *ptr;
    unsigned char *ptr2;
    struct stat sb;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    if (Agent.flags & QUEUE_AGENT_DISK_SPACE_LOW) {
        return(ConnWrite(client->conn, MSG5221SPACELOW, sizeof(MSG5221SPACELOW) - 1));
    }

    ptr = client->buffer + 5;

    /* QADDM <queue>-<id> <sender> <authSender> <recipient> <mailbox> <flags> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && ((sender = strchr(ptr + 4, ' ')) != NULL) 
            && ((authSender = strchr(sender + 1, ' ')) != NULL) 
            && ((recipient = strchr(authSender + 1, ' ')) != NULL) 
            && ((mailbox = strchr(recipient + 1, ' ')) != NULL) 
            && ((ptr2 = strchr(mailbox + 1, ' ')) != NULL)) {
        ptr[3] = '\0';

        *sender++ = '\0';
        *authSender++ = '\0';
        *recipient++ = '\0';
        *mailbox++ = '\0';
        *ptr2++ = '\0';

        queue = ptr;

        ptr += 4;

        flags = atol(ptr2);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    result = DELIVER_FAILURE;
    
    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr);
    if (stat(client->path, &sb) == 0) {
        FOPEN_CHECK(data, client->path, "rb");
    }
    if (data) {
        NMAPConnections list = { 0, };
        struct sockaddr_in saddr;

        if (MsgAuthFindUser(recipient) == 0) {
            if (MsgAuthGetUserStore(recipient, &saddr) == TRUE) {
                result = DeliverToStore(&list, &saddr, NMAP_DOCTYPE_MAIL, sender, authSender, client->path, data, sb.st_size, recipient, mailbox, 0);
                EndStoreDelivery(&list);
            }
        }
    } else {
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    FCLOSE_CHECK(data);
    data = NULL;

    if (result==DELIVER_SUCCESS) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    } else if (result==DELIVER_QUOTA_EXCEEDED) {
        ccode = ConnWrite(client->conn, MSG5220QUOTAERR, sizeof(MSG5220QUOTAERR) - 1);
    } else {
        ccode = ConnWrite(client->conn, MSG4120BOXLOCKED, sizeof(MSG4120BOXLOCKED) - 1);
    }

    return(ccode);
}

int 
CommandQaddq(void *param)
{
    unsigned long len;
    unsigned long start;
    unsigned long length;
    unsigned char *ptr;
    unsigned char *ptr2;
    unsigned char *ptr3;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    start = 0;
    length = 0;

    if (!client->entry.data) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

    if (Agent.flags & QUEUE_AGENT_DISK_SPACE_LOW) {
        return(ConnWrite(client->conn, MSG5221SPACELOW, sizeof(MSG5221SPACELOW) - 1));
    }

    client->entry.target = 0;

    ptr = client->buffer + 5;

    /* QADDQ <queue>-<id> <start> <length> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && ((ptr2 = strchr(ptr + 4, ' ')) != NULL) 
            && ((ptr3 = strchr(ptr2 + 1, ' ')) != NULL)) {
        ptr[3] = '\0';

        *ptr2++ = '\0';
        *ptr3++ = '\0';

        start = atol(ptr2);
        length = atol(ptr3);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    /* fixme - LockQueueEntry?
    LockQueueEntry(ptr+4, atoi(ptr)); */

    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
    FOPEN_CHECK(data, client->path, "rb");
    if (data) {
        fseek(data, start, SEEK_SET);
    } else {
        /* fixme - UnlockQueueEntry?
        UnlockQueueEntry(ptr+4, atoi(ptr)); */
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    while (!feof(data) && !ferror(data) && (length != 0)) {
        if (length > CONN_BUFSIZE) {
            len = fread(client->line, sizeof(unsigned char), CONN_BUFSIZE, data);
        } else {
            len = fread(client->line, sizeof(unsigned char), length, data);
        }

        if (len) {
            fwrite(client->line, sizeof(unsigned char), len, client->entry.data);
            length -= len;
        }
    }

    FCLOSE_CHECK(data);

    /* fixme - UnlockQueueEntry
    UnlockQueueEntry(ptr+4, atoi(ptr)); */

    return(ConnWrite(client->conn, MSG1000ENTRYMADE, sizeof(MSG1000ENTRYMADE) - 1));
}

int 
CommandQabrt(void *param)
{
    int ccode;
    QueueClient *client = (QueueClient *)param;

    if (client->entry.control) {
        FCLOSE_CHECK(client->entry.control);
        client->entry.control = NULL;
        
        sprintf(client->path,"%s/c%07lx.in", Conf.spoolPath, client->entry.id);
        UNLINK_CHECK(client->path);
    }
    
    if (client->entry.data) {
        FCLOSE_CHECK(client->entry.data);
        client->entry.data = NULL;
        
        sprintf(client->path,"%s/d%07lx.msg",Conf.spoolPath, client->entry.id);
        UNLINK_CHECK(client->path);
    }
    if (client->entry.work) {
        FCLOSE_CHECK(client->entry.work);
        client->entry.work = NULL;
        
        if (client->entry.workQueue[0] != '\0') {
            sprintf(client->path,"%s/w%s.%s",Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
            UNLINK_CHECK(client->path);
        }
    }
    
    client->entry.id = 0;
    
    ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    return(ccode);
}

int 
CommandQbody(void *param)
{
    int ccode;
    unsigned long count = 0;
    unsigned char *ptr;
    struct stat sb;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;

    /* QBODY <queue>-<id> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4])) {
        ptr[3] = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path,"%s/d%s.msg", Conf.spoolPath, ptr + 4);
    if (stat(client->path, &sb) == 0) {
        FOPEN_CHECK(data, client->path, "rb");
    }
    if (data) {
        while (!feof(data) && !ferror(data)) {
            if (fgets(client->line, CONN_BUFSIZE, data) != NULL) {
                /* Note that for the QBODY command we count the blank line, unlike in QHEAD */
                count += strlen(client->line);
                if ((client->line[0] != '\r') || (client->line[1] != '\n')) {
                    continue;
                }

                break;
            }
        }

        if (((ccode = ConnWriteF(client->conn, "2023 %lu Message body follows\r\n", sb.st_size - count)) != -1) 
                && ((ccode = ConnWriteFromFile(client->conn, data, count)) != -1)) {
            ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
        }

        FCLOSE_CHECK(data);
    } else {
        ccode = ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1);
    }

    return(ccode);
}

int 
CommandQbraw(void *param)
{
    int ccode;
    unsigned long start;
    unsigned long size;
    unsigned long count = 0;
    unsigned char *ptr;
    unsigned char *ptr2;
    unsigned char *ptr3;
    struct stat sb;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;

    /* QBRAW <queue>-<id> <start> <length> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4])) 
            && ((ptr2 = strchr(ptr + 5, ' ')) != NULL) 
            && ((ptr3 = strchr(ptr2 + 1, ' ')) != NULL)) {
        ptr[3] = '\0';
        *ptr2++ = '\0';
        *ptr3++ = '\0';

        start = atol(ptr2);
        size = atol(ptr3);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
    if (stat(client->path, &sb) == 0) {
        FOPEN_CHECK(data, client->path, "rb");
    }
    if (data) {
        while (!feof(data) && !ferror(data)) {
            if (fgets(client->line, CONN_BUFSIZE, data) != NULL) {
                count += strlen(client->line);
                if ((client->line[0] != '\r') || (client->line[1] != '\n')) {
                    continue;
                }

                break;
            }
        }
    } else {
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    if (start) {
        fseek(data, start, SEEK_CUR);
    }

    if (size > (sb.st_size - count - start)) {
        size = sb.st_size - count - start;
    }

    if (((ccode = ConnWriteF(client->conn, "2021 %lu Partial body follows\r\n", size)) != -1) 
            && ((ccode = ConnWriteFromFile(client->conn, data, size)) != -1)) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    }

    FCLOSE_CHECK(data);

    return(ccode);
}

int 
CommandQcopy(void *param)
{
    unsigned long id;
    unsigned long len;
    unsigned long target;
    unsigned char *ptr;
    unsigned char *ptr2;
    FILE *source = NULL;
    QueueClient *client = (QueueClient *)param;

    if (client->entry.data || client->entry.control) {
        return(ConnWrite(client->conn, MSG4226QUEUEOPEN, sizeof(MSG4226QUEUEOPEN) - 1));
    }

    if (Agent.flags & QUEUE_AGENT_DISK_SPACE_LOW) {
        return(ConnWrite(client->conn, MSG5221SPACELOW, sizeof(MSG5221SPACELOW) - 1));
    }

    target = 0;
    ptr = client->buffer + 5;

    /* QCOPY <queue>-<id>[ <target>] */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4]))) {
        ptr[3] = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    ptr2 = strchr(ptr + 5, ' ');
    if (ptr2) {
        *ptr2++ = '\0';

        if (*ptr2) {
            target = atol(ptr2);
        } else {
            return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
        }
    }

    if (target <= 999) {
        client->entry.target = target;
    } else {
        client->entry.target = 0;

        return(ConnWrite(client->conn, MSG4000OUTOFRANGE, sizeof(MSG4000OUTOFRANGE) - 1));
    }

    /* fixme - LockQueueEntry?
    LockQueueEntry(ptr+4, atoi(ptr)); */
    sprintf(client->path, "%s/d%s.msg",Conf.spoolPath, ptr + 4);
    FOPEN_CHECK(source, client->path, "rb");
    if (source) {
        XplMutexLock(Queue.queueIDLock);
        id = Queue.queueID++ & ((1 << 28) - 1);
        XplMutexUnlock(Queue.queueIDLock);
    } else {
        /* fixme - UnlockQueueEntry?
        UnlockQueueEntry(ptr+4, atoi(ptr)); */

        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    client->entry.id = id;

    sprintf(client->path, "%s/c%07lx.in", Conf.spoolPath, id);
    FOPEN_CHECK(client->entry.control, client->path, "wb");
    if (client->entry.control) {
        fprintf(client->entry.control, QUEUES_DATE"%lu\r\n", time(NULL));
    } else {
        FCLOSE_CHECK(source);

        return(ConnWrite(client->conn, MSG5221SPACELOW, sizeof(MSG5221SPACELOW) - 1));
    }

    sprintf(client->path, "%s/d%07lx.msg", Conf.spoolPath, id);
    FOPEN_CHECK(client->entry.data, client->path, "wb");

    while (!feof(source) && !ferror(source)) {
        len = fread(client->line, sizeof(unsigned char), CONN_BUFSIZE, source);
        if (len) {
            fwrite(client->line, sizeof(unsigned char), len, client->entry.data);
        }
    }

    FCLOSE_CHECK(source);

    /* fixme - UnlockQueueEntry?
    UnlockQueueEntry(ptr+4, atoi(ptr)); */

    return(ConnWrite(client->conn, MSG1000ENTRYMADE, sizeof(MSG1000ENTRYMADE) - 1));
}

int 
CommandQcrea(void *param)
{
    unsigned long id;
    unsigned long target;
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    if (Agent.flags & QUEUE_AGENT_DISK_SPACE_LOW) {
        return(ConnWrite(client->conn, MSG5221SPACELOW, sizeof(MSG5221SPACELOW) - 1));
    }

    ptr = client->buffer + 5;

    /* FIXME: disallow consequtive qcrea commands by checking client->entry.control */

    /* QCREA[ <target>] */
    if (*ptr == '\0') {
        target = 0;
    } else if ((*ptr++ == ' ') && (isdigit(*ptr))) {
        target = atol(ptr);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    if (target <= 999) {
        client->entry.target = target;
    } else {
        client->entry.target = 0;

        return(ConnWrite(client->conn, MSG4000OUTOFRANGE, sizeof(MSG4000OUTOFRANGE) - 1));
    }

    XplMutexLock(Queue.queueIDLock);
    id = Queue.queueID++ & ((1 << 28) - 1);
    XplMutexUnlock(Queue.queueIDLock);

    sprintf(client->path, "%s/c%07lx.in", Conf.spoolPath, id);
    FOPEN_CHECK(client->entry.control, client->path, "wb");
    if (client->entry.control) {
        fprintf(client->entry.control, QUEUES_DATE"%lu\r\n", time(NULL));
    } else {
        return(ConnWrite(client->conn, MSG5221SPACELOW, sizeof(MSG5221SPACELOW) - 1));
    }

    sprintf(client->path, "%s/d%07lx.msg", Conf.spoolPath, id);
    FOPEN_CHECK(client->entry.data, client->path, "wb");
    if (client->entry.data) {
        client->entry.id = id;
    } else {
        FCLOSE_CHECK(client->entry.control);
        sprintf(client->path, "%s/c%07lx.in", Conf.spoolPath, id);
        UNLINK_CHECK(client->path);

        return(ConnWrite(client->conn, MSG5221SPACELOW, sizeof(MSG5221SPACELOW) - 1));
    }

    /* We leave the handles open */
    return(ConnWrite(client->conn, MSG1000ENTRYMADE, sizeof(MSG1000ENTRYMADE) - 1));
}

int 
CommandQdele(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;

    /* QDELE <queue>-<id> */
    if ((*ptr++ == ' ')
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4]))) {
        ptr[3] = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    /* fixme - LockQueueEntry?
    LockQueueEntry(ptr+4, atoi(ptr)); */

    sprintf(client->path, "%s/c%s.%s", Conf.spoolPath, ptr + 4, ptr);
    UNLINK_CHECK(client->path);

    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
    UNLINK_CHECK(client->path);

    /* FIXME: close out the file handles? */

    /* fixme - UnlockQueueEntry?
    UnlockQueueEntry(ptr+4, atoi(ptr)); */

    return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
}

int 
CommandQdone(void *param)
{
    int ccode;
    unsigned char path[XPL_MAX_PATH + 1];
    struct stat sb;
    struct stat sb1;
    struct stat sb2;
    QueueClient *client = (QueueClient *)param;

    /* QDONE */
    
    if (client->entry.work) {
        FCLOSE_CHECK(client->entry.work);
        client->entry.work = NULL;
        if (client->entry.workQueue[0] != '\0') {
            sprintf(client->path, "%s/w%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
            sprintf(path, "%s/c%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
            if ((stat(client->path, &sb1) == 0) && (stat(path, &sb2) == 0)) {
                UNLINK_CHECK(path);
                RENAME_CHECK(client->path, path);
            } else {
                if (stat(client->path, &sb) == 0) {
                    /* Our new queue file exists, everything still ok */
                    RENAME_CHECK(client->path, path);
                } else {
                    /* got to keep the old one */
                    UNLINK_CHECK(client->path);
                }
            }
            
            client->entry.workQueue[0] = '\0';
        }
    }
    
    ccode = ConnWrite(client->conn, MSG1000RQWATCHMODE, sizeof(MSG1000RQWATCHMODE) - 1);

    client->done = TRUE;

    return(ccode);
}

int 
CommandQdspc(void *param)
{
    int ccode;
    unsigned long freeBlocks;
    int bytesPerBlock;
    QueueClient *client = (QueueClient *)param;

    /* QDSPC */

    bytesPerBlock = XplGetDiskBlocksize(Conf.spoolPath);
    freeBlocks = XplGetDiskspaceFree(Conf.spoolPath);

    if (freeBlocks > (unsigned long)(LONG_MAX / bytesPerBlock)) {
        ccode = ConnWriteF(client->conn, MSG1000SPACE_AVAIL, LONG_MAX);
    } else if ((freeBlocks * bytesPerBlock) < Conf.minimumFree) {
        ccode = ConnWriteF(client->conn, MSG1000SPACE_AVAIL, 0L);
    } else {
        ccode = ConnWriteF(client->conn, MSG1000SPACE_AVAIL, (freeBlocks * bytesPerBlock) - Conf.minimumFree);
    }

    return(ccode);      
}

int 
CommandQgrep(void *param)
{
    int ccode;
    int length;
    char *field;
    unsigned char *ptr;
    BOOL found = FALSE;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;

    /* QGREP <queue>-<id> <field> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4])) 
            && ((field = strchr(ptr + 5, ' ')) != NULL)) {
        ptr[3] = '\0';
        *field++ = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
    FOPEN_CHECK(data, client->path, "rb");
    if (data) {
        length = strlen(field);
    } else {
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    ccode = 0;
    while ((ccode != -1) && !feof(data) && !ferror(data)) {
        if (fgets(client->line, CONN_BUFSIZE, data) != NULL) { 
            if ((client->line[0] != '\r') || (client->line[1] != '\n')) {
                if (!found) {
                    if (XplStrNCaseCmp(client->line, field, length) == 0) {
                        ccode = ConnWriteF(client->conn, "2002-%s", client->line);

                        found = TRUE;
                    }
                } else if (isspace(client->line[0])) {
                    ccode = ConnWriteF(client->conn, "2002-%s", client->line);
                } else {
                    /* Found the field, and no additional header lines belong to it
                       Don't break yet as the search should continue for multiple lines 
                       with the same field */
                    found = FALSE;
                }

                continue;
            }

            break;
        }
    }

    FCLOSE_CHECK(data);

    if (ccode != -1) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    }

    return(ccode);
}

int 
CommandQhead(void *param)
{
    int ccode;
    unsigned long count = 0;
    unsigned char *ptr;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;

    /* QHEAD <queue>-<id> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4]))) {
        ptr[3] = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path,"%s/d%s.msg", Conf.spoolPath, ptr+4);
    FOPEN_CHECK(data, client->path, "rb");
    if (data) {
        while (!feof(data) && !ferror(data)) {
            if (fgets(client->line, CONN_BUFSIZE, data) != NULL) {
                if ((client->line[0] != '\r') || (client->line[1] != '\n')) {
                    count += strlen(client->line);
                    continue;
                }

                break;
            }
        }
    } else {
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    fseek(data, 0, SEEK_SET);

    if (((ccode = ConnWriteF(client->conn, "2023 %lu Message header follows\r\n", count)) != -1) 
            && ((ccode = ConnWriteFromFile(client->conn, data, count)) != -1)) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    }

    FCLOSE_CHECK(data);

    return(ccode);
}

int 
CommandQinfo(void *param)
{
    unsigned long count = 0;
    unsigned char *ptr;
    struct stat sb;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;

    /* QINFO <queue>-<id> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4]))) {
        ptr[3] = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
    if (stat(client->path, &sb) == 0) {
        FOPEN_CHECK(data, client->path, "rb");
    }
    if (data) {
        while (!feof(data) && !ferror(data)) {
            if (fgets(client->line, CONN_BUFSIZE, data) != NULL) {
                if ((client->line[0] != '\r') || (client->line[1] != '\n')) {
                    count += strlen(client->line);
                    continue;
                }

                break;
            }
        }
    } else {
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    FCLOSE_CHECK(data);

    return(ConnWriteF(client->conn, "2001 %s-%s %lu %lu %lu 0 0 0 0\r\n",
        ptr, ptr + 4, /* ID */
        sb.st_size, /* Size */
        count, /* HeadSize */
        sb.st_size - count)); /* BodySize */
}

int
CommandQmodFrom(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 9;

    if (!client->entry.work) {
        sprintf(client->path, "%s/w%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
        FOPEN_CHECK(client->entry.work, client->path, "wb");
        if (!client->entry.work) {
            return(0);
        }
    }

    if ((*ptr++ == ' ') 
            && (*ptr) 
            && (!isspace(*ptr))) {
        fprintf(client->entry.work, QUEUES_FROM"%s\r\n", ptr);
    }

    return(0);
}

int
CommandQmodFlags(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 10;

    if (!client->entry.work) {
        sprintf(client->path, "%s/w%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
        FOPEN_CHECK(client->entry.work, client->path, "wb");
        if (!client->entry.work) {
            return(0);
        }
    }

    if ((*ptr++ == ' ') 
            && (*ptr) 
            && (!isspace(*ptr))) {
        fprintf(client->entry.work, QUEUES_FLAGS"%s\r\n", ptr);
    }

    return(0);
}

int
CommandQmodLocal(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 10;

    if (!client->entry.work) {
        sprintf(client->path, "%s/w%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
        FOPEN_CHECK(client->entry.work, client->path, "wb");
        if (!client->entry.work) {
            return(0);
        }
    }

    if ((*ptr++ == ' ') 
            && (*ptr) 
            && (!isspace(*ptr))) {
        fprintf(client->entry.work, QUEUES_RECIP_LOCAL"%s\r\n", ptr);
    }

    return(0);
}

int
CommandQmodMailbox(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 12;

    if (!client->entry.work) {
        sprintf(client->path, "%s/w%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
        FOPEN_CHECK(client->entry.work, client->path, "wb");
        if (!client->entry.work) {
            return(0);
        }
    }

    if ((*ptr++ == ' ') 
            && (*ptr) 
            && (!isspace(*ptr))) {
        fprintf(client->entry.work, QUEUES_RECIP_MBOX_LOCAL"%s\r\n", ptr);
    }

    return(0);
}

int
CommandQmodRaw(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 8;

    if (!client->entry.work) {
        sprintf(client->path, "%s/w%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
        FOPEN_CHECK(client->entry.work, client->path, "wb");
        if (!client->entry.work) {
            return(0);
        }
    }

    if ((*ptr++ == ' ') 
            && (*ptr) 
            && (!isspace(*ptr))) {
        fprintf(client->entry.work, "%s\r\n", ptr);
    }

    return(0);
}

int
CommandQmodTo(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 7;

    if (!client->entry.work) {
        sprintf(client->path, "%s/w%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
        FOPEN_CHECK(client->entry.work, client->path, "wb");
        if (!client->entry.work) {
            return(0);
        }
    }

    if ((*ptr++ == ' ') 
            && (*ptr) 
            && (!isspace(*ptr))) {
        fprintf(client->entry.work,QUEUES_RECIP_REMOTE"%s\r\n", ptr);
    }

    return(0);
}

int 
CommandQmime(void *param)
{
    int ccode;
    long size;
    unsigned long i;
    unsigned char *ptr;
    struct stat sb;
    FILE *data = NULL;
    MIMEReportStruct *report;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;

    /* QMIME <queue>-<id> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4]))) {
        ptr[3] = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    if (!client->entry.report) {
        /* Find the message size */
        sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
        if (stat(client->path, &sb) == 0) {
            FOPEN_CHECK(data, client->path, "rb");
        }
        if (data) {
            /* Find the start of the body */
            while (!feof(data) && !ferror(data)) {
                if (fgets(client->line, CONN_BUFSIZE, data) != NULL) {
                    i = 0;
                    while (client->line[i] == '\r') {
                        i++;
                    }

                    if (client->line[i] != '\n') {
                        continue;
                    }

                    break;
                }
            }

            size = ftell(data);
            if (size > -1) {
                ;
            } else {
                size = 0;
            }

            fseek(data, 0, SEEK_SET);

            report = ParseMIME(data, size, sb.st_size, -1, client->line, CONN_BUFSIZE);
            if (report) {
                client->entry.report = report;

                ccode = SendMIME(client->conn, report);
            } else {
                /* fixme - Make a Parsing error message */                                        
                ccode = ConnWrite(client->conn, MSG5230NOMEMORYERR, sizeof(MSG5230NOMEMORYERR) - 1);
            }

            FCLOSE_CHECK(data);
        } else {
            ccode = ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1);
        }
    } else {
        ccode = SendMIME(client->conn, client->entry.report);
    }
    return(ccode);

}

int 
CommandQmove(void *param)
{
    int ccode;
    unsigned char *ptr;
    unsigned char *ptr2;
    unsigned char path[XPL_MAX_PATH + 1];
    struct stat sb;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;    

    /* QMOVE <queue>-<id> <queue> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4])) 
            && ((ptr2 = strchr(ptr + 5, ' ')) != NULL)) {
        ptr[3] = '\0';
        *ptr2++ = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path, "%s/c%s.%s", Conf.spoolPath, ptr + 4, ptr);
    if (stat(client->path, &sb) == 0) {
        sprintf(path, "%s/c%s.%03d", Conf.spoolPath, ptr + 4, atoi(ptr));

        RENAME_CHECK(client->path, path);

        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    } else {
        sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
        UNLINK_CHECK(client->path);

        ccode = ConnWrite(client->conn, MSG4224NOENTRY, sizeof(MSG4224NOENTRY) - 1);
    }

    return(ccode);
}

int 
CommandQrcp(void *param)
{
    int ccode;
    unsigned long id;
    unsigned char *ptr;
    unsigned char path[XPL_MAX_PATH + 1];
    FILE *source = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 4;

    if (client->entry.control && client->entry.data && client->entry.id) {
        FCLOSE_CHECK(client->entry.data);
        client->entry.data = NULL;

        FCLOSE_CHECK(client->entry.control);
        client->entry.control = NULL;
    } else {
        return(ConnWrite(client->conn, MSG4000CANTUNLOCKENTRY, sizeof(MSG4000CANTUNLOCKENTRY) - 1));
    }

    /*
        Close the current queue entry, copy the data file, come out as if 
        a QCREA was just performed and then run the now old entry.

        QRCP */
    if (*ptr++ == '\0') {
        XplMutexLock(Queue.queueIDLock);
        id = Queue.queueID++ & ((1 << 28) - 1);
        XplMutexUnlock(Queue.queueIDLock);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path, "%s/c%07lx.in", Conf.spoolPath, id);
    FOPEN_CHECK(client->entry.control, client->path,"wb");
    if (client->entry.control) {
        fprintf(client->entry.control, QUEUES_DATE"%lu\r\n", time(NULL));

        sprintf(client->path, "%s/d%07lx.msg", Conf.spoolPath, id);
        FOPEN_CHECK(client->entry.data, client->path, "wb");
        if (client->entry.data) {
            sprintf(client->path, "%s/d%07lx.msg", Conf.spoolPath, client->entry.id);
            FOPEN_CHECK(source, client->path, "rb");

            while (!feof(source) && !ferror(source)) {
                ccode = fread(client->line, sizeof(unsigned char), CONN_BUFSIZE, source);
                if (ccode) {
                    fwrite(client->line, sizeof(unsigned char), ccode, client->entry.data);
                }
            }

            FCLOSE_CHECK(source);

            sprintf(client->path,"%s/c%07lx.in",Conf.spoolPath, client->entry.id);
            sprintf(path, "%s/c%07lx.%03ld", Conf.spoolPath, client->entry.id, client->entry.target);
            RENAME_CHECK(client->path, path);

            sprintf(client->path, "%03ld%lx", client->entry.target, client->entry.id);

            if (XplSafeRead(Queue.activeWorkers) < Conf.maxConcurrentWorkers) {
                XplThreadID threadID;
                XplBeginCountedThread(&threadID, ProcessQueueEntry, STACKSPACE_Q, MemStrdup(client->path), ccode, Queue.activeWorkers);
            } else if (XplSafeRead(Queue.activeWorkers) < Conf.maxSequentialWorkers) {
                ConnFlush(client->conn);

                XplSafeIncrement(Queue.activeWorkers);

                ProcessQueueEntry(MemStrdup(client->path));
            } else {
                Queue.restartNeeded = TRUE;
            }

            client->entry.id = id;
            ccode = ConnWriteF(client->conn, "1000 %03ld-%lx OK\r\n", client->entry.target, client->entry.id);

            XplSafeIncrement(Queue.queuedLocal);

            return(ccode);
        }

        FCLOSE_CHECK(client->entry.control);
        client->entry.control = NULL;

        sprintf(client->path, "%s/c%07lx.in", Conf.spoolPath, id);
        UNLINK_CHECK(client->path);
    }

    sprintf(client->path, "%s/c%07lx.in", Conf.spoolPath, client->entry.id);
    FOPEN_CHECK(client->entry.control, path, "a+b");

    sprintf(client->path,"%s/d%07lx.msg", Conf.spoolPath, client->entry.id);
    FOPEN_CHECK(client->entry.data, path, "a+b");

    if (client->entry.data && client->entry.control) {
        ccode = ConnWrite(client->conn, "1000 Didn't copy; keeping the old entry\r\n", 41);
    } else {
        if (client->entry.data) {
            FCLOSE_CHECK(client->entry.data);
            client->entry.data = NULL;
        }

        if (client->entry.control) {
            FCLOSE_CHECK(client->entry.control);
            client->entry.data = NULL;
        }

        ccode = ConnWrite(client->conn, MSG5221SPACELOW, sizeof(MSG5221SPACELOW) - 1);
    }

    return(ccode);
}

int 
CommandQretr(void *param)
{
    int ccode;
    unsigned char *ptr;
    unsigned char *ptr2;
    struct stat sb;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;

    /* QRETR <queue>-<id> <INFO | MESSAGE> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4])) 
            && ((ptr2 = strchr(ptr + 5, ' ')) != NULL) 
            && (ptr2[1]) 
            && (!isspace(ptr2[1]))) {
        ptr[3] = '\0';
        *ptr2++ = '\0';
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    if (XplStrCaseCmp(ptr2, "INFO") == 0) {
        sprintf(client->path, "%s/c07%s.%s", Conf.spoolPath, ptr + 4, ptr);
        if ((stat(client->path, &sb) == 0) && sb.st_size) {
            FOPEN_CHECK(data, client->path, "rb");
        }
        if (data) {
            ccode = ConnWriteF(client->conn, "2022 %lu Info follows\r\n", sb.st_size);
            FCLOSE_CHECK(data);
        } else {
            return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
        }
    } else if (XplStrCaseCmp(ptr2, "MESSAGE") == 0) {
        sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
        if (stat(client->path, &sb) == 0) {
            FOPEN_CHECK(data, client->path, "rb");
        }
        if (data) {
            ccode = ConnWriteF(client->conn, "2023 %lu Message follows\r\n", sb.st_size);
        } else {
            return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
        }
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    if ((ccode != -1) 
            && ((ccode = ConnWriteFromFile(client->conn, data, sb.st_size)) != -1)) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    }

    FCLOSE_CHECK(data);

    return(ccode);
}

int 
CommandQrts(void *param)
{
    unsigned char *recipient;
    unsigned char *oRecipient;
    unsigned char *flags;
    QueueClient *client = (QueueClient *)param;


    /* fixme - the Queue Return To Sender command handler is being used inconsistently. */
    if (client->entry.workQueue[0] == '\0') {
        return(ConnWrite(client->conn, MSG3012BADQSTATE, sizeof(MSG3012BADQSTATE) - 1));
    }

    recipient = client->buffer + 4;

    if (!client->entry.work) {
        sprintf(client->path, "%s/w%s.%s", Conf.spoolPath, client->entry.workQueue + 4, client->entry.workQueue);
        FOPEN_CHECK(client->entry.work, client->path, "wb");
        if (!client->entry.work) {
            return(0);
        }
    }

    /* QRETR <recipient> <original address> <routing envelope flags>[ <delivery state>][ <transcript>] */
    if ((*recipient++ == ' ') 
            && (!isspace(*recipient)) 
            && ((oRecipient = strchr(recipient, ' ')) != NULL) 
            && (oRecipient[1]) 
            && (!isspace(oRecipient[1])) 
            && ((flags = strchr(oRecipient + 1, ' ')) != NULL) 
            && (flags[1]) 
            && (!isspace(flags[1]))) {
        fprintf(client->entry.work, QUEUES_BOUNCE"%s\r\n", recipient);
    }

    return(0);
}

int 
CommandQrun(void *param)
{
    int ccode;
    unsigned long target;
    unsigned long id;
    unsigned char *ptr;
    unsigned char path[XPL_MAX_PATH + 1];
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 4;

    /* QRUN[ <queue>-<id>] */
    if (*ptr == '\0') {
        if (client->entry.control && client->entry.data && client->entry.id) {
            FCLOSE_CHECK(client->entry.data);
            client->entry.data = NULL;

            FCLOSE_CHECK(client->entry.control);
            client->entry.control = NULL;
        } else {
            return(ConnWrite(client->conn, MSG4000CANTUNLOCKENTRY, sizeof(MSG4000CANTUNLOCKENTRY) - 1));
        }

        sprintf(client->path,"%s/c%07lx.in",Conf.spoolPath, client->entry.id);
        sprintf(path, "%s/c%07lx.%03ld", Conf.spoolPath, client->entry.id, client->entry.target);
        RENAME_CHECK(client->path, path);

        XplSafeIncrement(Queue.queuedLocal);

        ccode = ConnWriteF(client->conn, "1000 %03ld-%lx OK\r\n", client->entry.target, client->entry.id);

        sprintf(client->path, "%03ld%07lx", client->entry.target, client->entry.id);
        client->entry.id = 0;
    } else if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4]))) {
        ptr[3] = '\0';

        id = strtol(ptr + 4, NULL, 16);
        target = atoi(ptr);

        ccode = ConnWriteF(client->conn, "1000 %03ld-%lx OK\r\n", target, id);

        sprintf(client->path, "%03ld%07lx", target, id);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    if (XplSafeRead(Queue.activeWorkers) < Conf.maxConcurrentWorkers) {
        XplThreadID threadID;
        XplBeginCountedThread(&threadID, ProcessQueueEntry, STACKSPACE_Q, MemStrdup(client->path), ccode, Queue.activeWorkers);
    } else if (XplSafeRead(Queue.activeWorkers) < Conf.maxSequentialWorkers) {
        ConnFlush(client->conn);

        XplSafeIncrement(Queue.activeWorkers);

        ProcessQueueEntry(MemStrdup(client->path));
    } else {
        Queue.restartNeeded = TRUE;
    }

    return(ccode);
}

int 
CommandQsrchDomain(void *param)
{
    int ccode;
    unsigned char *ptr;
    void *handle = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 12;

    /* QSRCH DOMAIN <domain> */
    if ((*ptr++ == ' ') && (!isspace(*ptr))) {
        if ((handle = QDBHandleAlloc()) != NULL) {
            ccode = QDBSearchDomain(handle, ptr);
            if (!ccode) {
                if (ccode != -1) {
                    ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
                }
            } else {
                LogFailureF("Couldn't find %s in QDB", ptr);
                ccode = ConnWrite(client->conn, MSG4261NODOMAIN, sizeof(MSG4261NODOMAIN) - 1);
            }
        } else {
            LogFailure("Out of memory");
            ccode = ConnWrite(client->conn, MSG5230NOMEMORYERR, sizeof(MSG5230NOMEMORYERR) - 1);
        }

        if (handle) {
            QDBHandleRelease(handle);
        }
    } else {
        ccode = ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1);
    }

    return(ccode);
}

int 
CommandQsrchHeader(void *param)
{
    int ccode;
    int length;
    char *field;
    char *content;
    unsigned char *ptr;
    BOOL fieldFound=FALSE;
    BOOL contentFound=FALSE;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 12;

    /* QSRCH HEADER <queue>-<id> <header field> <header content> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4])) 
            && ((field = strchr(ptr + 5, ' ')) != NULL) 
            && (field[1]) 
            && (!isspace(field[1]))) {
        ptr[3] = '\0';
        *field++ = '\0';

        content = strchr(field, ' ');
        if (content) {
            *content++ = '\0';
        }

        length = strlen(field);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }


    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
    FOPEN_CHECK(data, client->path, "rb");
    if (data) {
        while (!feof(data) && !ferror(data)) {
            if (fgets(client->line, CONN_BUFSIZE, data) != NULL) {
                if ((client->line[0] != '\r') || client->line[1] != '\n') {
                    if (!fieldFound) {
                        if (XplStrNCaseCmp(client->line, field, length) != 0) {
                            continue;
                        }

                        fieldFound = TRUE;
                        if (!content || PDBSearch(client->line + length, content)) {
                            contentFound = TRUE;
                            break;
                        }
                    } else if (isspace(client->line[0])) {
                        if (PDBSearch(client->line + length, content) == FALSE) {
                            continue;
                        }

                        contentFound=TRUE;
                        break;
                    }

                    /* fixme - compared with other NMAP searching commands 
                       this implementation stops after finding the first
                       matching field (regardless of any optional content
                       match.

                       Should this be changed? */
                }

                break;
            }
        }

        FCLOSE_CHECK(data);
    } else {
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    if (contentFound) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    } else {
        ccode = ConnWrite(client->conn, MSG4262NOTFOUND, sizeof(MSG4262NOTFOUND) - 1);
    }

    return(ccode);
}

int 
CommandQsrchBody(void *param)
{
    int ccode;
    int length;
    char *content;
    unsigned char *ptr;
    BOOL found = FALSE;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 10;

    /* QSRCH BODY <queue>-<id> <search string> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4])) 
            && ((content = strchr(ptr + 5, ' ')) != NULL) 
            && (content[1])) {
        ptr[3] = '\0';
        *content++ = '\0';

        length = strlen(content);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
    FOPEN_CHECK(data, client->path, "rb");
    if (data) {
        while (!feof(data) && !ferror(data)) {
            if (fgets(client->line, CONN_BUFSIZE, data) != NULL) {
                if ((client->line[0] != '\r') || (client->line[1] != '\n')) {
                    continue;
                }

                break;
            }
        }

        /* fixme - this doesn't search across buffer bondaries! */
        while (!feof(data) && !ferror(data)) {
            if (fread(client->line, sizeof(unsigned char), CONN_BUFSIZE, data) > 0) {
                if (PDBSearch(client->line, content) == FALSE) {
                    continue;
                }
                found = TRUE;
                break;
            }
        }

        FCLOSE_CHECK(data);
    } else {
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }


    if (found) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    } else {
        ccode = ConnWrite(client->conn, MSG4262NOTFOUND, sizeof(MSG4262NOTFOUND) - 1);
    }

    return(ccode);
}

int 
CommandQsrchBraw(void *param)
{
    int ccode;
    int read;
    int length;
    unsigned long start;
    unsigned long end;
    unsigned char *ptr;
    unsigned char *startPtr;
    unsigned char *endPtr;
    unsigned char *content;
    BOOL found = FALSE;
    FILE *data = NULL;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 10;

    /* QSRCH BRAW <queue>-<id> <start> <end> <search string> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr)) 
            && (ptr[0]) 
            && (ptr[1]) 
            && (ptr[2]) 
            && (ptr[3] == '-') 
            && (ptr[4]) 
            && (!isspace(ptr[4])) 
            && ((startPtr = strchr(ptr + 5, ' ')) != NULL) 
            && (startPtr[1]) 
            && (isdigit(startPtr[1])) 
            && ((endPtr = strchr(startPtr + 1, ' ')) != NULL) 
            && (endPtr[1]) 
            && (isdigit(endPtr[1])) 
            && ((content = strchr(endPtr + 1, ' ')) != NULL)) {
        ptr[3] = '\0';
        *startPtr++ = '\0';
        *endPtr++ = '\0';
        *content++ = '\0';

        start = atol(startPtr);
        end = atol(endPtr);

        length = strlen(content);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    sprintf(client->path, "%s/d%s.msg", Conf.spoolPath, ptr + 4);
    FOPEN_CHECK(data, client->path, "rb");
    if (data) {
        while (!feof(data) && !ferror(data)) {
            if (fgets(client->line, CONN_BUFSIZE, data) != NULL) {
                if ((client->line[0] != '\r') || (client->line[1] != '\n')) {
                    continue;
                }

                break;
            }
        }

        /* Seek to the start position */
        fseek(data, start, SEEK_CUR);

        /* fixme - this doesn't search across buffer bondaries! */
        while ((end > 0) && !feof(data) && !ferror(data)) {
            if (end >= CONN_BUFSIZE) {
                read = fread(client->line, sizeof(unsigned char), CONN_BUFSIZE, data);
            } else {
                read = fread(client->line, sizeof(unsigned char), end, data);
            }

            if (read) {
                end -= read;

                if (PDBSearch(client->line, content) == FALSE) {
                    continue;
                }

                found = TRUE;
                break;
            }

        }

        FCLOSE_CHECK(data);
    } else {
        return(ConnWrite(client->conn, MSG4224CANTREAD, sizeof(MSG4224CANTREAD) - 1));
    }

    if (found) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    } else {
        ccode = ConnWrite(client->conn, MSG4262NOTFOUND, sizeof(MSG4262NOTFOUND) - 1);
    }

    return(ccode);
}

int 
CommandQstorAddress(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    if (!client->entry.data || !client->entry.control) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

    ptr = client->buffer + 13;

    /* QSTOR ADDRESS <value> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr))) {
        fprintf(client->entry.control, QUEUES_ADDRESS"%s\r\n", ptr);

        return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
    }

    return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
}

int 
CommandQstorCal(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    if (!client->entry.data || !client->entry.control) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

    ptr = client->buffer + 9;

    /* QSTOR CAL <recipient>[ <calendar name>][ <routing envelope flags>] */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr))) {
        fprintf(client->entry.control, QUEUES_CALENDAR_LOCAL"%s\r\n", ptr);

        return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
    }

    return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
}

int 
CommandQstorFlags(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    if (!client->entry.data || !client->entry.control) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

    ptr = client->buffer + 11;

    /* QSTOR FLAGS <value> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr))) {
        fprintf(client->entry.control, QUEUES_FLAGS"%s\r\n", ptr);

        return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
    }

    return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
}

int 
CommandQstorFrom(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    if (!client->entry.data || !client->entry.control) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

    ptr = client->buffer + 10;
    /* QSTOR FROM <sender> <authenticated sender | -> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr))) {
        fprintf(client->entry.control, QUEUES_FROM"%s\r\n", ptr);

        return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
    }

    return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
}

int 
CommandQstorLocal(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    if (!client->entry.data || !client->entry.control) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

   ptr = client->buffer + 11;

    /* QSTOR LOCAL <recipient> <original recipient> <routing envelope flags> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr))) {
        fprintf(client->entry.control, QUEUES_RECIP_LOCAL"%s\r\n", ptr);

        return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
    }

    return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
}

int 
CommandQstorMessage(void *param)
{
    int ccode;
    long count;
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;
    unsigned char TimeBuf[80];

    if (!client->entry.data || !client->entry.control) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

    ptr = client->buffer + 13;

    /* QSTOR MESSAGE[ <size>] */
    if (*ptr == '\0') {
        count = 0;
    } else if ((*ptr++ == ' ') && (isdigit(*ptr))) {
        count = atol(ptr);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    MsgGetRFC822Date(-1, 0, TimeBuf);
    fprintf(client->entry.data,
            "Received: from %s (%d.%d.%d.%d) by %s\r\n\twith NMAP (bongoqueue Agent); %s\r\n",
            BongoGlobals.hostname, /* FIXME: this should eventually be the remote hsotname of the agent */
            client->conn->socketAddress.sin_addr.s_net,
            client->conn->socketAddress.sin_addr.s_host,
            client->conn->socketAddress.sin_addr.s_lh,
            client->conn->socketAddress.sin_addr.s_impno,
            BongoGlobals.hostname,
            TimeBuf);
/*
    fprintf(client->entry.data,
            "Received: from %d.%d.%d.%d [%d.%d.%d.%d] by %s\r\n\twith NMAP (%s); %s\r\n",
            client->conn->socketAddress.sin_addr.s_net,
            client->conn->socketAddress.sin_addr.s_host,
            client->conn->socketAddress.sin_addr.s_lh,
            client->conn->socketAddress.sin_addr.s_impno,

            client->conn->socketAddress.sin_addr.s_net,
            client->conn->socketAddress.sin_addr.s_host,
            client->conn->socketAddress.sin_addr.s_lh,
            client->conn->socketAddress.sin_addr.s_impno,

            BongoGlobals.hostname,
            AGENT_NAME,
            TimeBuf);
*/
    if (count) {
        ccode = ConnReadToFile(client->conn, client->entry.data, count);
    } else {
        ccode = ConnReadToFileUntilEOS(client->conn, client->entry.data);
    }

    if (ccode != -1) {
        ccode = ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1);
    }

    return(ccode);
}

int 
CommandQstorRaw(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    if (!client->entry.data || !client->entry.control) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

    ptr = client->buffer + 9;

    /* QSTOR RAW <value> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr))) {
        fprintf(client->entry.control,"%s\r\n", ptr);

        return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
    }

    return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
}

int 
CommandQstorTo(void *param)
{
    unsigned char *ptr;
    QueueClient *client = (QueueClient *)param;

    if (!client->entry.data || !client->entry.control) {
        return(ConnWrite(client->conn, MSG4260NOQENTRY, sizeof(MSG4260NOQENTRY) - 1));
    }

    ptr = client->buffer + 8;

    /* QSTOR TO <value> */
    if ((*ptr++ == ' ') 
            && (!isspace(*ptr))) {
        fprintf(client->entry.control,QUEUES_RECIP_REMOTE"%s\r\n", ptr);

        return(ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
    }

    return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
}

int 
CommandQwait(void *param)
{
    int ccode;
    int port;
    int queue;
    unsigned char *ptr;
    unsigned char *identifier;
    QueueClient *client = (QueueClient *)param;

    ptr = client->buffer + 5;
    
    /* QWAIT <queue> <port> <identifier> */
    if ((*ptr++ == ' ') 
            && ((ptr = strchr(ptr, ' ')) != NULL) 
            && (ptr[1]) 
            && (isdigit(ptr[1])) 
            && ((identifier = strchr(ptr + 1, ' ')) != NULL) 
            && (identifier[1]) 
            && (!isspace(identifier[1])) 
            && (strlen(identifier + 1) < 100)) { // FIXME: REMOVE-MDB 100 was MDB maximum object identifier length
        *ptr++ = '\0';
        *identifier++ = '\0';

        queue = atol(client->buffer + 6);
        port = atol(ptr);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    if (port) {
        XplSafeIncrement(Queue.numServicedAgents);
        ccode = ConnWrite(client->conn, MSG1000QWATCHMODE, sizeof(MSG1000QWATCHMODE) - 1);
    } else {
        return(ConnWrite(client->conn, MSG3010BADARGC, sizeof(MSG3010BADARGC) - 1));
    }

    if (ccode != -1) {
        /* FIXME: this should be made a tad more efficient */
        if ((MsgGetHostIPAddress() != MsgGetAgentBindIPAddress())
            && (!client->conn->socketAddress.sin_addr.s_addr || (client->conn->socketAddress.sin_addr.s_addr == inet_addr("127.0.0.1")) || (client->conn->socketAddress.sin_addr.s_addr == MsgGetHostIPAddress()))) {
            AddPushAgent(client, "127.0.0.1", htons(port), queue, identifier);
        } else {
            AddPushAgent(client, inet_ntoa(client->conn->socketAddress.sin_addr), htons(port), queue, identifier);
        }

        client->done = TRUE;
    }

    return(ccode);
}

int
CommandQflush(void *param)
{
    QueueClient *client = (QueueClient *)param;

    Queue.flushNeeded = TRUE;
 
    return (ConnWrite(client->conn, MSG1000OK, sizeof(MSG1000OK) - 1));
}
