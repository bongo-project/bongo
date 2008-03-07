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
#include "time.h"
#include "imapd.h"

typedef struct _CopyRangeStatusUpdate {
	time_t last_update;
	int messages_processed;
	ImapSession *session;
} CopyRangeStatusUpdate;

// Update our internal status and warn the client if it has been waiting
// for too long since it last heard from us.
void
CopyMessageRangeDoStatusUpdate(CopyRangeStatusUpdate *status)
{
	time_t now = time(0);
	
	status->messages_processed++;
	if ((now - status->last_update) > 10) {
		// update the client 
		ConnWriteF(status->session->client.conn, 
		           "* OK - Copied %d messages so far\n", status->messages_processed);
		ConnFlush(status->session->client.conn);
		status->last_update = now;
		status->messages_processed = 0;
	}
}

__inline static long
CopyMessageRangeToTarget(Connection *storeConn, MessageInformation *message, 
                         unsigned long rangeCount, uint64_t target, 
                         CopyRangeStatusUpdate *status)
{
    long ccode;
    unsigned long count;
    count = rangeCount; 

    do {
        if (NMAPSendCommandF(storeConn, "COPY %llx %llx\r\n", message->guid, target) != -1) {
            if ((ccode = NMAPReadResponse(storeConn, NULL, 0, 0)) == 1000) {
                count--;
                if (count > 0) {
                    message++;
                    CopyMessageRangeDoStatusUpdate(status);
                    continue;
                }

                break;
            }

            return(CheckForNMAPCommError(ccode));
        }
        return(STATUS_NMAP_COMM_ERROR);
    } while (TRUE);

    return(STATUS_CONTINUE);
}


__inline static long
CopyMessageSet(ImapSession *session, char *messageSet, uint64_t target, BOOL byUid, CopyRangeStatusUpdate *status)
{
    long ccode;
    char *nextRange;
    unsigned long rangeStart;
    unsigned long rangeEnd;
    
    nextRange = messageSet;
    do {
        ccode = GetMessageRange(session->folder.selected.message, session->folder.selected.messageCount, &(nextRange), &rangeStart, &rangeEnd, byUid);
        if (ccode == STATUS_CONTINUE) {
            ccode = CopyMessageRangeToTarget(session->store.conn, 
                        &(session->folder.selected.message[rangeStart]), 
                        rangeEnd - rangeStart + 1, target, status);
            if (ccode == STATUS_CONTINUE) {
                continue;
            }
        }
        if (ccode == STATUS_UID_NOT_FOUND) {
            continue;
        }
        return(ccode);
    } while (nextRange);

    return(STATUS_CONTINUE);
}

__inline static long
HandleCopy(ImapSession *session, BOOL ByUID)
{
    FolderInformation *targetFolder;
    unsigned char *messageSet;
    unsigned char *ptr;
    FolderPath     targetPath;
    long ccode;
    char *ptr2;
    CopyRangeStatusUpdate status;

    status.last_update = time(0);
    status.messages_processed = 0;
    status.session = session;
    
    if ((ccode = CheckState(session, STATE_SELECTED)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            ptr = session->command.buffer + 5;
            if ((ccode = GrabArgument(session, &ptr, &messageSet)) == STATUS_CONTINUE) {
                ptr2 = ptr;
                if ((ccode = GetPathArgument(session, ptr2, &ptr2, &targetPath, FALSE)) == STATUS_CONTINUE) {
                    if ((ccode = FolderListLoad(session)) == STATUS_CONTINUE) {
                        if ((ccode = FolderGetByName(session, targetPath.name, &targetFolder)) == STATUS_CONTINUE) {
                            ccode = CopyMessageSet(session, messageSet, targetFolder->guid, ByUID, &status);
                        }
                    }
                    FreePathArgument(&targetPath);
                }
                MemFree(messageSet);
            }
        }
    }
    return(ccode);
}

int
ImapCommandCopy(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;

    if ((ccode = HandleCopy(session, FALSE)) == STATUS_CONTINUE) {
        return(SendOk(session, "COPY"));
    }
    return(SendError(session->client.conn, session->command.tag, "COPY", ccode));
}

int
ImapCommandUidCopy(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;

    memmove(session->command.buffer, session->command.buffer + strlen("UID "), strlen(session->command.buffer + strlen("UID ")) + 1);
    if ((ccode = HandleCopy(session, TRUE)) == STATUS_CONTINUE) {
        return(SendOk(session, "UID COPY"));
    }
    return(SendError(session->client.conn, session->command.tag, "UID COPY", ccode));
}
