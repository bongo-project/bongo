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
 ****************************************************************************/

#include <config.h>

#include <xpl.h>

#include <logger.h>

#include <msgapi.h>

#include "imapd.h"

__inline static BOOL
ParseEventArguments(char *argument, uint64_t *guid, uint32_t *uid, unsigned long *flags)
{
    char *ptr;

    *guid = HexToUInt64(argument, &ptr);
    if (*ptr == ' ') {
        ptr++;
        *uid = (uint32_t)HexToUInt64(ptr, &ptr);
        if (flags) {
            if (*ptr == ' ') {
                ptr++;
                *flags = (unsigned long)atol(ptr);
                return(TRUE);
            }
            return(FALSE);
        }
        return(TRUE);
    }
    return(FALSE);
}

__inline static void
RememberFlagEvent(StoreEvents *events, uint32_t uid, unsigned long flags)
{
    FlagEvent *tmp;

    if (events->flagCount < events->flagAllocCount) {
        ;
    } else {
        if (events->flagAllocCount == 0) {
            events->flagAllocCount = 4;
        }

        tmp = MemRealloc(events->flag, sizeof(FlagEvent) * events->flagAllocCount * 2);
        if (tmp) {
            events->flag = tmp;
            events->flagAllocCount *= 2;
        } else {
            /* we couldn't make room */
            return;
        }
    }

    events->flag[events->flagCount].uid = uid;
    events->flag[events->flagCount].value = flags;
    events->flagCount++;
    events->remembered |= STORE_EVENT_FLAG;
    return;
}

__inline static void
RememberNewEvent(StoreEvents *events, uint64_t guid, uint32_t uid)
{
    NewEvent *tmp;

    if (events->newCount < events->newAllocCount) {
        ;
    } else {
        if (events->newAllocCount == 0) {
            events->newAllocCount = 4;
        }

        tmp = MemRealloc(events->new, sizeof(NewEvent) * events->newAllocCount * 2);
        if (tmp) {
            events->new = tmp;
            events->newAllocCount *= 2;
        } else {
            /* we couldn't make room */
            return;
        }
    }

    events->new[events->newCount].guid = guid;
    events->new[events->newCount].uid = uid;
    events->newCount++;
    events->remembered |= STORE_EVENT_NEW;
    return;
}

__inline static void
RememberDeleteEvent(StoreEvents *events, uint32_t uid)
{
    PurgeEvent *tmp;

    if (events->purgeCount < events->purgeAllocCount) {
        ;
    } else {

        if (events->purgeAllocCount == 0) {
            events->purgeAllocCount = 4;
        }

        tmp = MemRealloc(events->purge, sizeof(PurgeEvent) * events->purgeAllocCount * 2);
        if (tmp) {
            events->purge = tmp;
            events->purgeAllocCount *= 2;
        } else {
            /* we couldn't make room */
            return;
        }
    }

    events->purge[events->purgeCount].uid = uid;
    events->purgeCount++;
    events->remembered |= STORE_EVENT_PURGE;
    return;
}

__inline static void
RememberModifiedEvent(StoreEvents *events, uint64_t guid, uint32_t uid, MessageInformation *messageList, unsigned long messageCount)
{
    MessageInformation *modifiedMessage;

    if ((modifiedMessage = SearchKnownMessagesByGuid(messageList, messageCount, guid)) != NULL) {
        RememberDeleteEvent(events, modifiedMessage->uid);
    }
    RememberNewEvent(events, guid, uid);
    return;
}

__inline static void
RememberResetEvent(StoreEvents *events)
{
    events->flagCount = 0;
    events->purgeCount = 0;
    events->newCount = 0;
    events->remembered = STORE_EVENT_LIMIT_EXCEEDED;
}

BOOL 
EventsCallback(void *param, char *beginPtr, char *endPtr)
{
    /* 6000 notification of an asyncronous event */
    /* Here are the events that we expect:

    RESET too many events! try LIST
    FLAGS 0000000000000010 00000010 3
    MODIFIED 0000000000000028 0000045e
    NEW 0000000000000028 00000028    
    DELETED 0000000000000021 00000021

    */

    ImapSession *session = (ImapSession *)param;
    unsigned char endChar;
    uint64_t guid;
    uint32_t uid;
    unsigned long flags;

    endChar = *endPtr;
    *endPtr = '\0';

    if (session->folder.selected.events.remembered != STORE_EVENT_LIMIT_EXCEEDED) {
        if ((*beginPtr == 'F') && (strncmp(beginPtr, "FLAGS ", strlen("FLAGS ")) == 0) && ParseEventArguments(beginPtr + strlen("FLAGS "), &guid, &uid, &flags)) {
            RememberFlagEvent(&(session->folder.selected.events), uid, flags);
        } else if ((*beginPtr == 'N') && (strncmp(beginPtr, "NEW ", strlen("NEW ")) == 0) && ParseEventArguments(beginPtr + strlen("NEW "), &guid, &uid, NULL)) {
            RememberNewEvent(&(session->folder.selected.events), guid, uid);
        } else if ((*beginPtr == 'D') && (strncmp(beginPtr, "DELETED ", strlen("DELETED ")) == 0) && ParseEventArguments(beginPtr + strlen("DELETED "), &guid, &uid, NULL)) {
            RememberDeleteEvent(&(session->folder.selected.events), uid);
        } else if ((*beginPtr == 'R') && (strncmp(beginPtr, "RESET", strlen("RESET")) == 0)) {
            RememberResetEvent(&(session->folder.selected.events));
        } else if ((*beginPtr == 'M') && (strncmp(beginPtr, "MODIFIED", strlen("MODIFIED")) == 0) && ParseEventArguments(beginPtr + strlen("MODIFIED "), &guid, &uid, NULL)) {
            RememberModifiedEvent(&(session->folder.selected.events), guid, uid, session->folder.selected.message, session->folder.selected.messageCount);
        }
    }
    *endPtr = endChar;
    return(TRUE);
}

static int
ComparePurgeEvent(const void *param1, const void *param2)
{
    PurgeEvent *prg1 = (PurgeEvent *)param1;
    PurgeEvent *prg2 = (PurgeEvent *)param2;

    return(prg1->uid - prg2->uid);
}

__inline static unsigned long
FindPurgeSequenceNumbers(OpenedFolder *selected, PurgeEvent *purge, unsigned long purgeCount)
{
    long count = purgeCount;

    do {
        if (UidToSequenceNum(selected->message, selected->messageCount, purge->uid, &(purge->sequence)) == STATUS_CONTINUE) {
            count--;
            purge++;
            continue;
        }

        /* the client doesn't know about these messages, sending an expunge would confuse it */
        return(purgeCount - count);
    } while (count != 0);
    return(purgeCount);
}

__inline static long
SendExpungeResponses(Connection *conn, PurgeEvent *purge, unsigned long purgeCount)
{
    /* send the highest sequence numbers first; otherwise sequence numbers would have to be recalculated everytime */
    do {
        if (ConnWriteF(conn, "* %lu EXPUNGE\r\n", purge->sequence + 1) != -1) {
            purgeCount--;
            purge--;
            continue;
        }
        return(STATUS_ABORT);
    } while (purgeCount != 0);
    return(STATUS_CONTINUE);
}

__inline static unsigned long
CountPurgesWithRecentFlag(PurgeEvent *purge, unsigned long purgeCount, unsigned long firstRecentUid)
{
    unsigned long count = 0;
    do {
        if (purge->uid >= firstRecentUid) { 
            count++;
            purge--;
            continue;
        }

        break;
    } while ((purgeCount - count) != 0);

    return(count);
}


__inline static void
MessageListCompact(OpenedFolder *folder, PurgeEvent *purgeList, unsigned long purgeCount)
{
    unsigned long count = 0;
    unsigned long moveCount;
    MessageInformation *destinationAddress;
    MessageInformation *sourceAddress;

    destinationAddress = &(folder->message[purgeList[0].sequence]);
    sourceAddress = destinationAddress + 1;

    while((purgeCount - count) > 1) {
        moveCount = purgeList[count + 1].sequence - purgeList[count].sequence - 1;
        memmove(destinationAddress, sourceAddress, sizeof(MessageInformation) * moveCount);
        destinationAddress += moveCount;
        sourceAddress += (moveCount + 1);
        count++;
    }

    moveCount = folder->messageCount - purgeList[count].sequence - 1;
    memmove(destinationAddress, sourceAddress, sizeof(MessageInformation) * moveCount);
    folder->messageCount -= purgeCount;
    folder->recentCount -= CountPurgesWithRecentFlag(&(purgeList[purgeCount - 1]), purgeCount, folder->info->uidRecent);
}


__inline static long
MessageListAddNewMessages(Connection *storeConn, OpenedFolder *folder, NewEvent *newEvent, unsigned long newEventCount)
{
    long ccode;
    long result;
    long headerSize;
    unsigned char reply[1024];

    do {
        result = STATUS_CONTINUE;

        if (NMAPSendCommandF(storeConn, "INFO %llx Pnmap.mail.headersize\r\n", newEvent->guid) != -1) {
            ccode = NMAPReadResponse(storeConn, reply, sizeof(reply), TRUE);
            if (ccode == 2001) {
                ccode = NMAPReadDecimalPropertyResponse(storeConn, "nmap.mail.headersize", &headerSize);
                if (ccode == 2001) {
                    result = MessageListAddMessage(folder, reply, headerSize);
                } else if (ccode == 3245) {
                    result = MessageListAddMessage(folder, reply, 0);
                } else {
                    return(ccode);
                }
            }

            ccode = NMAPReadResponse(storeConn, NULL, 0, 0);
            if (ccode == 1000) {
                if (result == STATUS_CONTINUE) {
                    newEvent++;
                    newEventCount--;
                    continue;
                }
                return(result);
            }
            return(CheckForNMAPCommError(ccode));
        }
        return(STATUS_NMAP_COMM_ERROR);
        
    } while (newEventCount != 0);

    return(STATUS_CONTINUE);
}

__inline static long
SendFlagEvents(Connection *clientConn, StoreEvents *events, OpenedFolder *folder)
{
    long ccode;
    FlagEvent *flag = &(events->flag[0]);
    unsigned long messageId;

    do {
        if ((ccode = UidToSequenceNum(folder->message, folder->messageCount, flag->uid, &messageId)) == STATUS_CONTINUE) {
            folder->message[messageId].flags = flag->value;
            ccode = SendFetchFlag(clientConn, messageId, flag->value, 0);
            if (ccode == STATUS_CONTINUE) {
                events->flagCount--;
                flag++;
                continue;
            }
        }
        /* We don't know about this message; throw away the flags */
        /* if it is a new message, we get the flags later anyway */
        events->flagCount--;
        flag++;
        continue;
    } while (events->flagCount != 0);
    return(STATUS_CONTINUE);
}

__inline static long
SendPurgeEvents(Connection *clientConn, StoreEvents *events, OpenedFolder *folder)
{
    long ccode;

    qsort(events->purge, events->purgeCount, sizeof(PurgeEvent), ComparePurgeEvent);
    events->purgeCount = FindPurgeSequenceNumbers(folder, &(events->purge[0]), events->purgeCount);
    if (events->purgeCount > 0) {
        MessageListCompact(folder, events->purge, events->purgeCount);
        if((ccode = SendExpungeResponses(clientConn, &(events->purge[events->purgeCount - 1]), events->purgeCount)) != STATUS_CONTINUE) {
            return(ccode);
        }
    }

    return(STATUS_CONTINUE);
}

__inline static long
MarkPurgedMessages(Connection *clientConn, StoreEvents *events, OpenedFolder *folder)
{
    PurgeEvent *purge;
    unsigned long messageId;
    unsigned long count;

    purge = &(events->purge[0]);
    count = events->purgeCount;

    do {
        if (UidToSequenceNum(folder->message, folder->messageCount, purge->uid, &messageId) == STATUS_CONTINUE) {
            folder->message[messageId].flags |= STORE_MSG_FLAG_PURGED;
        }
        count--;
        purge++;
    } while (count != 0);

    return(STATUS_CONTINUE);
}

__inline static long
SendRememberedEvents(Connection *clientConn, Connection *storeConn, OpenedFolder *selectedFolder, unsigned long typesAllowed)
{
    long ccode;
    BOOL sequenceChanged = FALSE;
    StoreEvents *events = &(selectedFolder->events);

    if ((events->remembered & STORE_EVENT_FLAG) && (events->remembered & STORE_EVENT_FLAG)) {
        SendFlagEvents(clientConn, events, selectedFolder);
        events->flagCount = 0;
        events->remembered &= ~STORE_EVENT_FLAG;
    }

    if (events->remembered & STORE_EVENT_PURGE) {
        if (typesAllowed & STORE_EVENT_PURGE) {
            sequenceChanged = TRUE;
            ccode = SendPurgeEvents(clientConn, events, selectedFolder);
            events->purgeCount = 0;
            events->remembered &= ~STORE_EVENT_PURGE;
        } else {
            /* we can't send anything to the client, but we do need to update */
            /* the messageList so commands do not try to access these messages */
            ccode = MarkPurgedMessages(clientConn, events, selectedFolder);
        }
    }
                
    if ((events->remembered & STORE_EVENT_NEW) && (typesAllowed & STORE_EVENT_NEW)) {
        sequenceChanged = TRUE;
        MessageListAddNewMessages(storeConn, selectedFolder, events->new, events->newCount);
        events->newCount = 0;
        events->remembered &= ~STORE_EVENT_NEW;
    }

    if (!sequenceChanged) {
        return(STATUS_CONTINUE);
    }

    return(SendExistsAndRecent(clientConn, selectedFolder->messageCount, selectedFolder->recentCount));
}

__inline static void
GenerateEvents(OpenedFolder *selectedFolder, OpenedFolder *updatedFolder)
{
    MessageInformation *oldListMessage;
    MessageInformation *newListMessage;
    unsigned long oldListMessagesLeft;
    unsigned long newListMessagesLeft;

    oldListMessage = &(updatedFolder->message[0]);
    oldListMessagesLeft = selectedFolder->messageCount;
    newListMessage = &(updatedFolder->message[0]);
    newListMessagesLeft = updatedFolder->messageCount;
    
    for (;;) {
        if (newListMessagesLeft > 0) {
            if (oldListMessagesLeft > 0) {
                if (newListMessage->uid == oldListMessage->uid) {
                    /* Message in both lists */
                    if (newListMessage->flags != newListMessage->flags) {
                        RememberFlagEvent(&(selectedFolder->events), newListMessage->uid, newListMessage->flags);
                    }
                    oldListMessage++;
                    oldListMessagesLeft--;
                    newListMessage++;
                    newListMessagesLeft--;
                    continue;
                }

                if (newListMessage->uid > oldListMessage->uid) {
                    /* old message is gone */
                    RememberDeleteEvent(&(selectedFolder->events), oldListMessage->uid);
                    oldListMessage++;
                    oldListMessagesLeft--;
                    continue;
                }

                /* (newListMessage->uid < oldListMessage->uid) */
                /* new message has appeared */
                /* this should never happen because new messages are only supposed to appear at the end of the list */ 
                newListMessage++;
                newListMessagesLeft--;
                continue;
            }

            /* everything else in the new list is New */
            do {
                RememberNewEvent(&(selectedFolder->events), newListMessage->guid, newListMessage->uid);
                newListMessage++;
                newListMessagesLeft--;
            } while (newListMessagesLeft > 0);

            break;
        }

        if (oldListMessagesLeft > 0) {
            /* everything else in the old list has been purged */
            do {
                RememberDeleteEvent(&(selectedFolder->events), oldListMessage->uid);
                oldListMessage++;
                oldListMessagesLeft--;
            } while (oldListMessagesLeft > 0);
        }

        break;
    }
}

__inline static long
GenerateEventsFromScratch(Connection *storeConn, OpenedFolder *selectedFolder)
{
    long ccode;
    OpenedFolder updatedFolder;

    memset(&updatedFolder, 0, sizeof(OpenedFolder));
    if ((ccode = FolderOpen(storeConn, &updatedFolder, selectedFolder->info, TRUE)) == STATUS_CONTINUE) {
        /* got wait to remove the STORE_EVENT_LIMIT_EXCEEDED flag until after FolderOpen() to avoid recording spurious events */
        selectedFolder->events.remembered = 0;
        GenerateEvents(selectedFolder, &updatedFolder);
        FolderClose(&updatedFolder);
    }
    return(ccode);

}

__inline static long
GetLatestEvents(Connection *storeConn, OpenedFolder *selectedFolder)
{
    long ccode;

    /* send a NOOP to store to get the latest out of band notifications */
    if (NMAPSendCommand(storeConn, "NOOP\r\n", strlen("NOOP\r\n")) != -1) {
        if ((ccode = NMAPReadResponse(storeConn, NULL, 0, 0)) == 1000) {
            if (!(selectedFolder->events.remembered & STORE_EVENT_LIMIT_EXCEEDED)) {
                return(STATUS_CONTINUE);
            }
            return(GenerateEventsFromScratch(storeConn, selectedFolder));
        }
        return(CheckForNMAPCommError(ccode));
    }
    return(STATUS_NMAP_COMM_ERROR);
}

long
EventsSend(ImapSession *session, unsigned long typesAllowed)
{
    long ccode;

    if (session->client.state == STATE_SELECTED) {
        if ((ccode = GetLatestEvents(session->store.conn, &(session->folder.selected))) == STATUS_CONTINUE) {
            if (!session->folder.selected.events.remembered) {
                return(STATUS_CONTINUE);
            }
            return(SendRememberedEvents(session->client.conn, session->store.conn, &(session->folder.selected), typesAllowed));
        }
        return(ccode);
    }
    return(STATUS_CONTINUE);
}


void
EventsFree(StoreEvents *events)
{
    if (events->flags) {
        MemFree(events->flags);
    }

    if (events->new) {
        MemFree(events->new);
    }

    if (events->flags) {
        MemFree(events->purge);
    }

    memset(events, 0, sizeof(StoreEvents));
}
