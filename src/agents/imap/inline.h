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

/* isspace() includes \r on some platforms including linux */
/* isblank() is only tab and space but it is not available on all platforms */
__inline static BOOL
IsWhiteSpace(char character)
{
    if ((character == ' ') || (character == '\t')) {
        return(TRUE);
    }
    return(FALSE);
}

__inline static long
ReadCommandLine(Connection *conn, char **buffer, unsigned long *bufferLen)
{
    long ccode;

    ccode = ConnReadToAllocatedBuffer(conn, buffer, bufferLen);
    if (ccode > -1) {
        return(STATUS_CONTINUE);
    }

    if (ccode == CONN_ERROR_MEMORY) {
        return(STATUS_LINE_TOO_LONG);
    }

    return(STATUS_ABORT);
}

__inline static long
GrabOctet(ImapSession *session, unsigned char **Input, unsigned char **Destination)
{
    unsigned char *start;
    unsigned char *end;
    long Size;
    long ccode;

    /* skip '{' */
    start = *Input + 1;
    end = strchr(start, '}');

    if (end) {
        if ((end - start) < 7) { /* sanity check */
            Size = atol(start);
            if (Size) {
                *Destination = MemMalloc(Size + 1);
                if (*Destination) {
                    if ((ConnWrite(session->client.conn, "+ Ready for more data\r\n", 23) != -1) && (ConnFlush(session->client.conn) != -1)) {
                        if (ConnReadCount(session->client.conn, *Destination, Size) == Size) {
                            (*Destination)[Size] = '\0';
                            ccode = ReadCommandLine(session->client.conn, &(session->command.buffer), &(session->command.bufferLen));
                            if (ccode == STATUS_CONTINUE) {
                                *Input = session->command.buffer;
                                return(STATUS_CONTINUE);
                            }
                            MemFree(*Destination);
                            *Destination = NULL;
                            return(ccode);                    
                        }
                    }
                    MemFree(*Destination);
                    *Destination = NULL;
                    return(STATUS_ABORT);
                }
                return(STATUS_MEMORY_ERROR);
            }

            *Destination = MemStrdup("");
            if (*Destination) {
                ccode = ReadCommandLine(session->client.conn, &(session->command.buffer), &(session->command.bufferLen));
                if (ccode == STATUS_CONTINUE) {
                    *Input = session->command.buffer;
                    return(STATUS_CONTINUE);
                }
                MemFree(*Destination);
                *Destination = NULL;
                return(ccode);
            }
            return(STATUS_MEMORY_ERROR);
        }
    }
    *Destination = NULL;
    return(STATUS_INVALID_ARGUMENT);
}

__inline static long
GrabLiteral(ImapSession *session, unsigned char **Input, unsigned char **Destination)
{
    unsigned char *contentBegin;
    unsigned long contentLen;
    unsigned char *closeQuote;

    /* skip opening quote */
    contentBegin = *Input + 1;

    if ((closeQuote = strchr(contentBegin, '"')) != NULL) {
        contentLen = closeQuote - contentBegin;
        *Destination = MemMalloc(contentLen + 1);
        if (*Destination) {
            memcpy(*Destination, contentBegin, contentLen);
            (*Destination)[contentLen] = '\0';
            *Input = closeQuote + 1;
            return(STATUS_CONTINUE);
        }

        return(STATUS_MEMORY_ERROR);
    }

    *Destination = NULL;
    return(STATUS_INVALID_ARGUMENT);
}

__inline static unsigned char
GrabGroup(ImapSession *session, unsigned char **Input, unsigned char **Destination)
{
    unsigned char *contentBegin;
    unsigned long contentLen;
    unsigned char *closeParen;
    int nesting = 1;

    /* We have to look for the proper closing paren, since they can be nested */
    closeParen = *Input;

    do {
        closeParen++;
        if (*closeParen != ')') {
            if (*closeParen != '(') {
                if (*closeParen != '\0') {
                    continue;
                }
                *Destination = NULL;
                return(STATUS_INVALID_ARGUMENT);
            }
            nesting++;
            continue;
        }

        nesting--;
        if (nesting == 0) {
            break;
        }
    } while (TRUE);

    /* we know where the closing paren is */
    /* skip the opening paren and copy the contents */
    contentBegin = *Input + 1;
    contentLen = closeParen - contentBegin;

    *Destination = MemMalloc(contentLen + 1);
    if (*Destination) {
        memcpy(*Destination, contentBegin, contentLen);
        (*Destination)[contentLen] = '\0';
        *Input = closeParen + 1;
        return(STATUS_CONTINUE);
    }

    return(STATUS_MEMORY_ERROR);
}

__inline static unsigned char
GrabWord(ImapSession *session, unsigned char **Input, unsigned char **Destination, BOOL lookForParen)
{
    long len;
    unsigned char *spacePtr;
    unsigned char *parenPtr;

    spacePtr = strchr((*Input), ' ');
    if (lookForParen) {
        if (spacePtr) {
            *spacePtr ='\0';
            parenPtr = strchr(*Input, ')');
            if (parenPtr) {
                len = parenPtr - (*Input);
            } else {
                len = spacePtr - (*Input);
            }
            *spacePtr = ' ';
        } else {
            parenPtr = strchr(*Input, ')');
            if (parenPtr) {
                len = parenPtr - (*Input);
            } else {
                len = strlen((*Input));
            }
        }
    } else {
        if (spacePtr) {
            len = spacePtr - (*Input);
        } else {
            len = strlen((*Input));
        }
    }

    *Destination = MemMalloc(len + 1);
    if (*Destination) {
        memcpy(*Destination, (*Input), len);
        (*Destination)[len] = '\0';
        (*Input) += len;
        return(STATUS_CONTINUE);
    }

    return(STATUS_MEMORY_ERROR);
}

__inline static long
GrabArgumentEx(ImapSession *session, unsigned char **Input, unsigned char **Destination, BOOL lookForParen)
{
    unsigned char *ptr = *Input;

    while (IsWhiteSpace(*ptr)) {
        ptr++;
    }

    *Input = ptr;

    if (*Input) {
        if (**Input == '{') {          /* octets */
            return(GrabOctet(session, Input, Destination));
        }
    
        if (**Input == '"') {          /* literal */
            return(GrabLiteral(session, Input, Destination));
        }
    
        if (**Input == '(') {          /* group */
            return(GrabGroup(session, Input, Destination));
        }

        return(GrabWord(session, Input, Destination, lookForParen));
    }

    *Destination = MemStrdup("");
    if (*Destination) {
        return(STATUS_CONTINUE);
    }

    return(STATUS_MEMORY_ERROR);
}

__inline static long
GrabArgument(ImapSession *session, unsigned char **Input, unsigned char **Destination)
{
    return(GrabArgumentEx(session, Input, Destination, FALSE));
}


__inline static long
CheckConnResultIs(const long desiredResult, const long failureCode, long result)
{
    if (result == desiredResult) {
        return(STATUS_CONTINUE);
    }

    return(failureCode);
}

__inline static long
CheckConnResultIsNot(const long desiredResult, const long failureCode, long result)
{
    if (result != desiredResult) {
        return(STATUS_CONTINUE);
    }

    return(failureCode);
}


__inline static long
CheckStoreResponseCode(const long desiredResult, long result)
{
    if (result == desiredResult) {
        return(STATUS_CONTINUE);
    }

    if ((result > 999) && (result < 10000)) {
        return(result);
    }
    
    return(STATUS_NMAP_COMM_ERROR);
}

__inline static long
CheckForNMAPCommError(long result)
{
    if ((result > 999) && (result < 10000)) {
        return(result);
    }
    
    return(STATUS_NMAP_COMM_ERROR);
}

__inline static long
SendError(Connection *conn, char *prefix, char *command, long errorValue)
{
    long ccode;
    
    if (errorValue != STATUS_ABORT) {
        if ((errorValue > 0) && (errorValue < STATUS_MAX)) {
            if (Imap.command.returnValueIndex[errorValue] != -1) {
                ccode = ConnWriteF(conn, ImapErrorStrings[Imap.command.returnValueIndex[errorValue]].formatString, prefix, command);
            } else {
                ccode = ConnWriteF(conn, "%s NO %s undefined internal error %d\r\n", prefix, command, (int)errorValue);
            }
        } else if ((errorValue < 10000) || (errorValue > 999)) {
            ccode = ConnWriteF(conn, "%s NO %s message store returned error %d\r\n", prefix, command, (int)errorValue);
        } else {
            ccode = ConnWriteF(conn, "%s NO %s undefined internal error %d\r\n", prefix, command, (int)errorValue);
        }

        if (ccode != -1) {
            return(errorValue);
        }
    }

    return(STATUS_ABORT);
}

__inline static long
SendOk(ImapSession *session, unsigned char *command)
{
    if (ConnWriteF(session->client.conn, "%s OK %s completed\r\n", session->command.tag, command) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}



#define UNSEEN_FLAGS_BLOCK_SIZE 16

__inline static void
RememberFlags(unsigned long *unseenFlags, unsigned long msgNum, unsigned long msgFlags)
{
    if (unseenFlags ==  NULL) {
        unseenFlags = MemMalloc((1 + UNSEEN_FLAGS_BLOCK_SIZE) * 2 * sizeof(unsigned long));
        if (unseenFlags) {
            unseenFlags[2] = msgNum;
            unseenFlags[3] = msgFlags;
            /* Set Counters */
            unseenFlags[0] = UNSEEN_FLAGS_BLOCK_SIZE;       /* Allocated */
            unseenFlags[1] = 1;                             /* Used */
        }
    } else {
        if (unseenFlags[1] < unseenFlags[0]) {
            unseenFlags[2 + (unseenFlags[1] * 2)]   = msgNum;
            unseenFlags[2 + (unseenFlags[1] * 2) + 1]  = msgFlags;
            /* Set Counter */
            unseenFlags[1]++;
        } else {
            unsigned long *Tmp;
            Tmp = MemRealloc(unseenFlags, ((1 + unseenFlags[0] + UNSEEN_FLAGS_BLOCK_SIZE) * 2 * sizeof(unsigned long)));
            if (Tmp) {
                unseenFlags = Tmp;
                unseenFlags[2 + (unseenFlags[1] * 2)]  = msgNum;
                unseenFlags[2 + (unseenFlags[1] * 2) + 1] = msgFlags;
                /* Set Counters */
                unseenFlags[0] += UNSEEN_FLAGS_BLOCK_SIZE;
                unseenFlags[1]++;
            }
        }
    }
}

__inline static long
SendFetchFlag(Connection *conn, unsigned long sequenceNumber, unsigned long flags, unsigned long uid)
{
    long ccode;
    char *space = "";

    ccode = ConnWriteF(conn, "* %lu FETCH (FLAGS (", sequenceNumber + 1);

    if (flags & STORE_MSG_FLAG_ANSWERED) {
        ccode = ConnWrite(conn, "\\Answered", sizeof("\\Answered") - 1);
        space = " ";
    }

    if (flags & STORE_MSG_FLAG_DELETED) {
        ccode = ConnWriteF(conn, "%s\\Deleted", space);
        space = " ";
    }

    if (flags & STORE_MSG_FLAG_RECENT) {
        ccode = ConnWriteF(conn, "%s\\Recent", space);
        space = " ";
    }

    if (flags & STORE_MSG_FLAG_SEEN) {
        ccode = ConnWriteF(conn, "%s\\Seen", space);
        space = " ";
    }

    if (flags & STORE_MSG_FLAG_DRAFT) {
        ccode = ConnWriteF(conn, "%s\\Draft", space);
        space = " ";
    }

    if (flags & STORE_MSG_FLAG_FLAGGED) {
        ccode = ConnWriteF(conn, "%s\\Flagged", space);
        space = " ";
    }

    if (!uid) {
        ccode = ConnWrite(conn, "))\r\n", sizeof("))\r\n") - 1);
    } else {
        ccode = ConnWriteF(conn, ") UID %lu)\r\n", uid);
    }

    if (ccode != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);

}

__inline static void
FreeUnseenFlags(ImapSession *session)
{
    if (session->folder.selected.events.flags) {
        MemFree(session->folder.selected.events.flags);
        session->folder.selected.events.flags = NULL;
    }
}

__inline static long
SendUnseenFlags(ImapSession *session)
{
    long ccode;
    unsigned long i;
    char *space = "";
    unsigned long MsgFlags;
    unsigned long imapId;

    if (!session->folder.selected.events.flags) {
        return(STATUS_CONTINUE);
    }

    for (i = 0; i < session->folder.selected.events.flags[1]; i++) {
        MsgFlags = session->folder.selected.events.flags[2 + (i * 2) + 1];
        
        ccode = UidToSequenceNum(session->folder.selected.message, session->folder.selected.messageCount, session->folder.selected.events.flags[2 + (i * 2)], &imapId);  /* NEEDS_WORK: folder.selected.event.flags needs to be UIDs and they are not currently */
        if (ccode == STATUS_CONTINUE) {
            ccode = ConnWriteF(session->client.conn, "* %lu FETCH (FLAGS (", imapId);
            if ((MsgFlags & STORE_MSG_FLAG_ANSWERED) && (ccode != -1)) {
                ccode = ConnWrite(session->client.conn, "\\Answered", sizeof("\\Answered") - 1);
                space = " ";
            }

            if ((MsgFlags & STORE_MSG_FLAG_DELETED) && (ccode != -1)) {
                ccode = ConnWriteF(session->client.conn, "%s\\Deleted", space);
                space = " ";
            }

            if ((MsgFlags & STORE_MSG_FLAG_RECENT) && (ccode != -1)) {
                ccode = ConnWriteF(session->client.conn, "%s\\Recent", space);
                space = " ";
            }

            if ((MsgFlags & STORE_MSG_FLAG_SEEN) && (ccode != -1)) {
                ccode = ConnWriteF(session->client.conn, "%s\\Seen", space);
                space = " ";
            }

            if ((MsgFlags & STORE_MSG_FLAG_DRAFT) && (ccode != -1)) {
                ccode = ConnWriteF(session->client.conn, "%s\\Draft", space);
                space = " ";
            }

            if ((MsgFlags & STORE_MSG_FLAG_FLAGGED) && (ccode != -1)) {
                ccode = ConnWriteF(session->client.conn, "%s\\Flagged", space);
                space = " ";
            }

            if (ccode != -1) {
                ccode = ConnWrite(session->client.conn, "))\r\n", sizeof("))\r\n") - 1);
            }

            if (ccode != -1) {
                continue;
            }
            return(STATUS_ABORT);
        }

        break;
    }

    MemFree(session->folder.selected.events.flags);
    session->folder.selected.events.flags = NULL;
    return(STATUS_CONTINUE);
}


__inline static void
CheckPathLen(char *path)
{
    if (strlen(path) >= XPL_MAX_PATH) {
        path[XPL_MAX_PATH - 1] = '\0';
    }
}

__inline static void
AddMboxSpaces(char *mbox)
{
    unsigned char *ptr = mbox;

    while((ptr = strchr(ptr, 127)) != NULL) {
        *ptr++ = ' ';
    }
}

__inline static long
GrabTwoArguments(ImapSession *session, unsigned char *arguments, unsigned char **argument1, unsigned char **argument2)
{
    long ccode;
    unsigned char *ptr = arguments;

    do {
        if (!IsWhiteSpace(*ptr)) {
            break;
        }
        ptr++;
    } while(TRUE);

    if (*ptr) {
        ccode = GrabArgument(session, &ptr, argument1);
        if (ccode == STATUS_CONTINUE) {
            if (*ptr) {
                ccode = GrabArgument(session, &ptr, argument2);
                if (ccode == STATUS_CONTINUE) {
                    return(STATUS_CONTINUE);
                }
            } else {
                ccode = STATUS_INVALID_ARGUMENT;
            }
            MemFree(*argument1);
            *argument1 = NULL;
        }
        return(ccode);
    }
    return(STATUS_INVALID_ARGUMENT);
}


__inline static long
LeadCommandLine(ImapSession *session)
{
    long ccode;
    unsigned char *tmpCommand;
    unsigned long bytesRead;

    ccode = ConnReadAnswer(session->client.conn, session->command.buffer, session->command.bufferLen);
    if (ccode > -1) {
        if ((unsigned long)ccode < session->command.bufferLen) {
            return(STATUS_CONTINUE);
        } 

        bytesRead = ccode;
        do {
            tmpCommand = MemRealloc(session->command.buffer, session->command.bufferLen * 2);
            if (tmpCommand) {
                session->command.buffer = tmpCommand;
                ccode = ConnReadAnswer(session->client.conn, session->command.buffer + session->command.bufferLen, session->command.bufferLen);
                if (ccode > 0) {
                    if ((unsigned long)ccode < session->command.bufferLen) {
                        session->command.bufferLen *= 2;
                        return(STATUS_CONTINUE);
                    } 

                    session->command.bufferLen *= 2;
                    continue;
                }
                
                if (ccode == 0) {
                    /* handle boundry condition */
                    if (session->command.buffer[session->command.bufferLen - 1] == '\r') {
                        session->command.buffer[session->command.bufferLen - 1] = '\0';
                    }
                    session->command.bufferLen *= 2;
                    return(STATUS_CONTINUE);
                }

                session->command.bufferLen *= 2;
                return(STATUS_ABORT);
            }

            return(STATUS_MEMORY_ERROR);
        } while (TRUE);
    }

    return(STATUS_ABORT);
}

__inline static void
CleanExtremeDelimiters(char *path, char **clean, unsigned long *cleanLen)
{
    int len;
    char *ptr;

    /* stip off trailing slashes */
    len = strlen(path);
    for (;;) {
        if (len > 0) {
            if ((path[len - 1] != '/') && (path[len - 1] != '\\')) {
                break;
            }
            path[--len] = '\0';
            continue;
        }
        break;
    }

    /* strip off any leading slashes */
    ptr = path;
    for (;;) {
        if (*ptr) {
            if ((*ptr != '/') && (*ptr != '\\')) {
                break;
            }
            len--;
            ptr++;
            continue;
        }
        break;
    }
    *cleanLen = len;
    *clean = ptr;
    return;
} 

__inline static char *
FindNextArgument(char *ptr)
{
    for(;;) {
        if (IsWhiteSpace(*ptr)) {
            ptr++;
            continue;
        }
        
        if (*ptr != '\0') {
            return(ptr);
        }
        return(NULL);
    }
}

__inline static BOOL
ParseListResponse(void *response, MessageInformation *message)
{
    char *ptr;
    unsigned int uidInt;

    message->guid = HexToUInt64((char *)response, &ptr);
    if (*ptr == ' ') {
        ptr++;
        if (sscanf(ptr, "%lu %lu %x %lu %lu %*s", &message->type, &message->flags, &uidInt, &message->internalDate, &message->size) == 5) {
            message->uid = (uint32_t)uidInt;
            return(TRUE);
        }
    }
    return(FALSE);
}


__inline static long
MessageListMakeSpace(OpenedFolder *folder)
{
    MessageInformation *newList;

    if (folder->messageCount < folder->messageAllocated) {
        return(STATUS_CONTINUE);
    }

    newList = MemRealloc(folder->message, sizeof(MessageInformation) * folder->messageAllocated * 2);
    if (newList) {
        folder->message = newList;
        folder->messageAllocated *= 2;
        return(STATUS_CONTINUE);
    }

    folder->messageCount = 0;
    return(STATUS_MEMORY_ERROR);
}


__inline static long
MessageListAddMessage(OpenedFolder *folder, char *response, long headerSize)
{
    long ccode;
    MessageInformation *currentMessage;

    ccode = MessageListMakeSpace(folder);
    if (ccode == STATUS_CONTINUE) {
        currentMessage = &(folder->message[folder->messageCount]); 
                
        if (ParseListResponse(response, currentMessage)) {
            if (!(currentMessage->type & STORE_DOCTYPE_FOLDER) && (currentMessage->type & STORE_DOCTYPE_MAIL)) {
                if (currentMessage->uid >= folder->info->uidRecent) {
                    currentMessage->flags |= STORE_MSG_FLAG_RECENT;
                    folder->recentCount++;
                }

                if (currentMessage->uid >= folder->info->uidNext) {
                    folder->info->uidNext = currentMessage->uid + 1;
                }

                currentMessage->headerSize = headerSize;
                currentMessage->bodySize = currentMessage->size - headerSize;

                folder->messageCount++;
                return(STATUS_CONTINUE);
            }

            return(STATUS_CONTINUE);
        }
        return(STATUS_NMAP_PROTOCOL_ERROR);
    }
    return(ccode);
}


__inline static long
CheckState(ImapSession *session, long requiredState)
{
    if (session->client.state >= requiredState) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_INVALID_STATE);
}

__inline static long
GetPathArgument(ImapSession *session, char *start, char **next, FolderPath *path, BOOL nullStringValid)
{
    long ccode;
    unsigned char *ptr;

    ptr = FindNextArgument(start);
    if (ptr) {
        ccode = GrabArgument(session, &ptr, &path->buffer);
        if (ccode == STATUS_CONTINUE) {
            CleanExtremeDelimiters(path->buffer, &path->name, &path->nameLen);
            if (path->nameLen < STORE_MAX_COLLECTION) {
                if (nullStringValid || *(path->name)) {
                    *next = ptr;            
                    return(STATUS_CONTINUE);
                }
                MemFree(path->buffer);
                return(STATUS_INVALID_ARGUMENT);
            }
            MemFree(path->buffer);
            return(STATUS_PATH_TOO_LONG);
        }
        return(ccode);        
    }
    return(STATUS_INVALID_ARGUMENT);
}

__inline static void
FreePathArgument(FolderPath *path)
{
    MemFree(path->buffer);
    path->buffer = NULL;
}

__inline static uint64_t
SearchKnownFolders(FolderInformation *folder, const unsigned long messageCount, const char *folderName)
{
    unsigned long i;

    for (i = 0; i < messageCount; i++) {
        if (strcmp(folder->name.utf7, folderName) != 0) {
            folder++;
            continue;
        }

        return(folder->guid);
    }

    return(0);
}

__inline static FolderInformation *
SearchKnownFoldersByName(FolderInformation *folder, const unsigned long folderCount, const char *folderName)
{
    unsigned long i;

    for (i = 0; i < folderCount; i++) {
        if (strcmp(folder->name.utf7, folderName) != 0) {
            folder++;
            continue;
        }

        return(folder);
    }

    return(NULL);
}


__inline static FolderInformation *
SearchKnownFoldersByGuid(FolderInformation *folder, const unsigned long folderCount, const uint64_t folderGuid)
{
    unsigned long i;

    for (i = 0; i < folderCount; i++) {
        if (folder->guid != folderGuid) {
            folder++;
            continue;
        }

        return(folder);
    }

    return(NULL);
}

__inline static long
FolderGetGuid(ImapSession *session, char *folderName, uint64_t *guid)
{
    *guid = SearchKnownFolders(session->folder.list, session->folder.count, folderName);
    if (*guid) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_NO_SUCH_FOLDER);
}

__inline static long
FolderGetByName(ImapSession *session, char *folderName, FolderInformation **folder)
{
    *folder = SearchKnownFoldersByName(session->folder.list, session->folder.count, folderName);
    if (*folder) {
        return(STATUS_CONTINUE);
    }

    return(STATUS_NO_SUCH_FOLDER);
}

__inline static long
FolderGetByGuid(ImapSession *session, uint64_t folderGuid, FolderInformation **folder)
{
    *folder = SearchKnownFoldersByGuid(session->folder.list, session->folder.count, folderGuid);
    if (*folder) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_NO_SUCH_FOLDER);
}

__inline static long
StoreMessageFlag(Connection *storeConn, uint64_t guid, char *actionString, unsigned long flags, unsigned long *newFlags)
{
    long ccode;
    char *ptr;
    char buffer[30];
    
    if (NMAPSendCommandF(storeConn, "FLAG %llx %s%lu\r\n", guid, actionString, flags) != -1) {
        if (newFlags) {
            if ((ccode = CheckForNMAPCommError(NMAPReadResponse(storeConn, buffer, sizeof(buffer), TRUE))) == 1000) {
                if ((ptr = strchr(buffer, ' ')) != NULL) {
                    if (*newFlags & STORE_MSG_FLAG_RECENT) {
                        *newFlags = (atol(ptr + 1) | STORE_MSG_FLAG_RECENT);
                    } else {
                        *newFlags = atol(ptr + 1);
                    }
                } else {
                    *newFlags = 0;
                }
                return(STATUS_CONTINUE);
            }
            return(ccode);
        }
        
        if ((ccode = CheckForNMAPCommError(NMAPReadResponse(storeConn, NULL, 0, 0))) == 1000) {
            return(STATUS_CONTINUE);
        }
        return(ccode);
    }
    return(STATUS_NMAP_COMM_ERROR);
}

__inline static long
SendExistsAndRecent(Connection *conn, unsigned long messageCount, unsigned long recentCount)
{
    if (ConnWriteF(conn, "* %lu EXISTS\r\n* %lu RECENT\r\n", messageCount, recentCount) != -1) {
        return(STATUS_CONTINUE);
    }                           
    return(STATUS_ABORT);
}

__inline static void
FolderClose(OpenedFolder *openFolder)
{
    if (openFolder->message) {
        MemFree(openFolder->message);
    }
    memset(openFolder, 0, sizeof(OpenedFolder));
}

__inline static MessageInformation *
SearchKnownMessagesByGuid(MessageInformation *messageList, unsigned long messageCount, const uint64_t messageGuid)
{
    MessageInformation *currentMessage;
    unsigned long count;

    count = messageCount;
    currentMessage = &(messageList[0]);

    for (;;) {
        if (count > 0) {
            if (currentMessage->guid != messageGuid) {
                currentMessage++;
                count--;
                continue;
            }
            return(currentMessage);
        }
        break;
    }
    return(NULL);
}

