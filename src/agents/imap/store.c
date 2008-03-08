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
#include "imapd.h"

static StoreCommandFlag StoreCommandFlags[] = {
    { "\\Deleted", sizeof("\\Deleted") - 1, STORE_MSG_FLAG_DELETED },
    { "\\Seen", sizeof("\\Seen") - 1, STORE_MSG_FLAG_SEEN },
    { "\\Recent", sizeof("\\Recent") - 1, STORE_MSG_FLAG_RECENT },
    { "\\Draft", sizeof("\\Draft") - 1, STORE_MSG_FLAG_DRAFT },
    { "\\Answered", sizeof("\\Answered") - 1, STORE_MSG_FLAG_ANSWERED },
    { "\\Flagged", sizeof("\\Flagged") - 1, STORE_MSG_FLAG_FLAGGED },
    { NULL, 0, 0 }
};

static StoreCommandType StoreCommandTypes[] = {
    { "FLAGS.SILENT", "", TRUE },
    { "+FLAGS.SILENT", "+", TRUE },
    { "-FLAGS.SILENT", "-", TRUE },
    { "FLAGS", "", FALSE },
    { "+FLAGS", "+", FALSE },
    { "-FLAGS", "-", FALSE },
    { NULL, NULL, FALSE }
};

unsigned long
ParseStoreFlags(unsigned char *command, unsigned long *returnFlags)
{
    unsigned char *ptr;
    unsigned long flags;
    long ccode;

    flags = 0;
    ptr = command;

    do {
        if (*ptr) {
            ccode = BongoKeywordBegins(Imap.command.store.flagIndex, ptr);
            if (ccode != -1) {
                flags |= StoreCommandFlags[ccode].value;
                ptr += StoreCommandFlags[ccode].stringLen;
                if (*ptr == ' ') {
                    ptr++;
                }
                continue;
            }
            return(STATUS_INVALID_ARGUMENT);
        }
        break;
    } while (TRUE);

    *returnFlags = flags;
    return(STATUS_CONTINUE);
}

BOOL
CommandStoreInit()
{
    BongoKeywordIndexCreateFromTable(Imap.command.store.typeIndex, StoreCommandTypes, .string, TRUE);
    if (Imap.command.store.typeIndex) {
        BongoKeywordIndexCreateFromTable(Imap.command.store.flagIndex, StoreCommandFlags, .string, TRUE);
        if (Imap.command.store.flagIndex) {
            return(TRUE);
        }
        BongoKeywordIndexFree(Imap.command.store.typeIndex);
    }
    return(FALSE);
}

void
CommandStoreCleanup()
{
    BongoKeywordIndexFree(Imap.command.store.typeIndex);
    BongoKeywordIndexFree(Imap.command.store.flagIndex);
}

__inline static long
DoStoreForMessage(ImapSession *session, MessageInformation *message, unsigned long messageId, char *actionString, unsigned long flags, BOOL silent, BOOL byUid)
{
    long ccode;

    if ((ccode = StoreMessageFlag(session->store.conn, message->guid, actionString, flags, &(message->flags))) == STATUS_CONTINUE) {
        if (!silent) {
            ccode = SendFetchFlag(session->client.conn, messageId, message->flags, byUid ? message->uid: 0);
        }
    }
    return(ccode);
}


__inline static long
DoStoreForMessageRange(ImapSession *session, MessageInformation *message, unsigned long rangeStart, unsigned long rangeCount, char *actionString, unsigned long flags, BOOL silent, BOOL byUid, BOOL *purgedMessage)
{
    unsigned long messageId;
    unsigned long count;
    long ccode = STATUS_CONTINUE;

    messageId = rangeStart;
    count = rangeCount;

    for (;;) {
        if (!(message->flags & STORE_MSG_FLAG_PURGED)) {
            if ((ccode = DoStoreForMessage(session, message, messageId, actionString, flags, silent, byUid)) == STATUS_CONTINUE) {
                count--;
                if (count > 0) {
                    DoProgressUpdate(session);
                    message++;
                    messageId++;
                    continue;
                }
            }
            break;
        }

        *purgedMessage = TRUE;
        count--;
        if (count > 0) {
            message++;
            messageId++;
            continue;
        }
        break;
    }

    return(ccode);
}

__inline static long
DoStoreForMessageSet(ImapSession *session, char *messageSet, unsigned long Flags, BOOL Silent, BOOL ByUID, char *actionString, MessageInformation *message, unsigned long messageCount, BOOL *purgedMessage)
{
    char *nextRange;
    unsigned long rangeStart;
    unsigned long rangeEnd;
    long ccode;

    nextRange = messageSet;

    do {
        if ((ccode = GetMessageRange(message, messageCount, &(nextRange), &rangeStart, &rangeEnd, ByUID)) == STATUS_CONTINUE) {
            if ((ccode = DoStoreForMessageRange(session, &(message[rangeStart]), rangeStart, rangeEnd - rangeStart + 1, actionString, Flags, Silent, ByUID, purgedMessage)) == STATUS_CONTINUE) {
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
HandleStore(ImapSession *session, BOOL ByUID, BOOL *purgedMessage)
{
    unsigned char *messageSet;
    long ccode;
    long keywordId;
    unsigned char *ptr;
    unsigned char *type;
    unsigned char *flagString;
    unsigned long flags;

    if ((ccode = CheckState(session, STATE_SELECTED)) == STATUS_CONTINUE) {
        /* rfc 2180 discourages purge notifications during the store command */
        if ((ccode = EventsSend(session, STORE_EVENT_NEW | STORE_EVENT_FLAG)) == STATUS_CONTINUE) {
            ccode = STATUS_READ_ONLY_FOLDER;
            if (!session->folder.selected.readOnly) {
                ptr = session->command.buffer + 5;
                if ((ccode = GrabArgument(session, &ptr, &messageSet)) == STATUS_CONTINUE) {
                    if ((ccode = GrabArgument(session, &ptr, &type)) == STATUS_CONTINUE) {
                        if ((ccode = GrabArgument(session, &ptr, &flagString)) == STATUS_CONTINUE) {
                            if ((ccode = ParseStoreFlags(flagString, &flags)) == STATUS_CONTINUE) {
                                ccode = STATUS_INVALID_ARGUMENT;
                                if ((keywordId = BongoKeywordBegins(Imap.command.store.typeIndex, type)) != -1) {
                                    ccode = DoStoreForMessageSet(session, messageSet, flags, StoreCommandTypes[keywordId].silent, ByUID, StoreCommandTypes[keywordId].nmapCommand, session->folder.selected.message, session->folder.selected.messageCount, purgedMessage);
                                }
                            }
                            MemFree(flagString);
                        }
                        MemFree(type);
                    }
                    MemFree(messageSet);
                }
            }
        }
    }
    return(ccode);
}

int
ImapCommandStore(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;
    BOOL purgedMessage = FALSE;

    StartProgressUpdate(session, NULL);

    if ((ccode = HandleStore(session, FALSE, &purgedMessage)) == STATUS_CONTINUE) {
        if (!purgedMessage) {
            StopProgressUpdate(session);
            return(SendOk(session, "STORE"));
        }
        ccode = STATUS_REQUESTED_MESSAGE_NO_LONGER_EXISTS;
    }
    StopProgressUpdate(session);
    return(SendError(session->client.conn, session->command.tag, "STORE", ccode));
}

int
ImapCommandUidStore(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;
    BOOL purgedMessage = FALSE;
    
    StartProgressUpdate(session, NULL);
    
    memmove(session->command.buffer, session->command.buffer + strlen("UID "), strlen(session->command.buffer + strlen("UID ")) + 1);
    if ((ccode = HandleStore(session, TRUE, &purgedMessage)) == STATUS_CONTINUE) {
        if (!purgedMessage) {
            StopProgressUpdate(session);
            return(SendOk(session, "UID STORE"));
        }
        ccode = STATUS_REQUESTED_MESSAGE_NO_LONGER_EXISTS;
    }
    StopProgressUpdate(session);
    return(SendError(session->client.conn, session->command.tag, "UID STORE", ccode));
}
