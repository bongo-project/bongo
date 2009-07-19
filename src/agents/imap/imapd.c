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

#define N_PLAT_NLM
#define N_IAPX386

/* Product defines */
#define PRODUCT_SHORT_NAME "imapd.nlm"
#define PRODUCT_NAME   "Bongo IMAP4 Agent"
#define PRODUCT_DESCRIPTION "Provides IMAP4 access to the Bongo mail-store. (IMAP4 = Internet Mail Access Protocol, Rev4, RFC2060)"
#define PRODUCT_VERSION  "$Revision: 1.8 $"

#define MAX_THREAD_LOAD  50

#undef DEBUG_FETCH
#define NMLOGID_H_NEED_LOGGING_KEY
#define NMLOGID_H_NEED_LOGGING_CERT
#include <xpl.h>

#include <logger.h>

#include <msgapi.h>

#include "imapd.h"

ImapGlobal Imap;

static ProtocolCommand ImapProtocolCommands[] = {
    { IMAP_COMMAND_CAPABILITY, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_CAPABILITY) - 1, ImapCommandCapability, NULL, NULL },
    { IMAP_COMMAND_NOOP, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_NOOP) - 1, ImapCommandNoop, NULL, NULL },
    { IMAP_COMMAND_LOGOUT, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_LOGOUT) - 1, ImapCommandLogout, NULL, NULL },
    { IMAP_COMMAND_STARTTLS, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_STARTTLS) - 1, ImapCommandStartTls, NULL, NULL },
    { IMAP_COMMAND_AUTHENTICATE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_AUTHENTICATE) - 1, ImapCommandAuthenticate, NULL, NULL },
    { IMAP_COMMAND_LOGIN, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_LOGIN) - 1, ImapCommandLogin, NULL, NULL },
    { IMAP_COMMAND_SELECT, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_SELECT) - 1, ImapCommandSelect, NULL, NULL },
    { IMAP_COMMAND_EXAMINE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_EXAMINE) - 1, ImapCommandExamine, NULL, NULL },
    { IMAP_COMMAND_CREATE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_CREATE) - 1, ImapCommandCreate, NULL, NULL },
    { IMAP_COMMAND_DELETE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_DELETE) - 1, ImapCommandDelete, NULL, NULL },
    { IMAP_COMMAND_RENAME, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_RENAME) - 1, ImapCommandRename, NULL, NULL },
    { IMAP_COMMAND_SUBSCRIBE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_SUBSCRIBE) - 1, ImapCommandSubscribe, NULL, NULL },
    { IMAP_COMMAND_UNSUBSCRIBE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_UNSUBSCRIBE) - 1, ImapCommandUnsubscribe, NULL, NULL },
    { IMAP_COMMAND_LIST, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_LIST) - 1, ImapCommandList, NULL, NULL },
    { IMAP_COMMAND_LSUB, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_LSUB) - 1, ImapCommandLsub, NULL, NULL },
    { IMAP_COMMAND_STATUS, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_STATUS) - 1, ImapCommandStatus, NULL, NULL },
    { IMAP_COMMAND_APPEND, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_APPEND) - 1, ImapCommandAppend, NULL, NULL },
    { IMAP_COMMAND_CHECK, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_CHECK) - 1, ImapCommandCheck, NULL, NULL },
    { IMAP_COMMAND_CLOSE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_CLOSE) - 1, ImapCommandClose, NULL, NULL },
    { IMAP_COMMAND_EXPUNGE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_EXPUNGE) - 1, ImapCommandExpunge, NULL, NULL },
    { IMAP_COMMAND_SEARCH, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_SEARCH) - 1, ImapCommandSearch, NULL, NULL },
    { IMAP_COMMAND_UID_SEARCH, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_UID_SEARCH) - 1, ImapCommandUidSearch, NULL, NULL },
    { IMAP_COMMAND_FETCH, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_FETCH) - 1, ImapCommandFetch, NULL, NULL },
    { IMAP_COMMAND_UID_FETCH, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_UID_FETCH) - 1, ImapCommandUidFetch, NULL, NULL },
    { IMAP_COMMAND_STORE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_STORE) - 1, ImapCommandStore, NULL, NULL },
    { IMAP_COMMAND_UID_STORE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_UID_STORE) - 1, ImapCommandUidStore, NULL, NULL },
    { IMAP_COMMAND_COPY, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_COPY) - 1, ImapCommandCopy, NULL, NULL },
    { IMAP_COMMAND_UID_COPY, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_UID_COPY) - 1, ImapCommandUidCopy, NULL, NULL },
    { IMAP_COMMAND_SETACL, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_SETACL) - 1, ImapCommandSetAcl, NULL, NULL },
    { IMAP_COMMAND_DELETEACL, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_DELETEACL) - 1, ImapCommandDeleteAcl, NULL, NULL },
    { IMAP_COMMAND_GETACL, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_GETACL) - 1, ImapCommandGetAcl, NULL, NULL },
    { IMAP_COMMAND_LISTRIGHTS, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_LISTRIGHTS) - 1, ImapCommandListRights, NULL, NULL },
    { IMAP_COMMAND_MYRIGHTS, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_MYRIGHTS) - 1, ImapCommandMyRights, NULL, NULL },
    { IMAP_COMMAND_SETQUOTA, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_SETQUOTA) - 1, ImapCommandSetQuota, NULL, NULL },
    { IMAP_COMMAND_GETQUOTA, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_GETQUOTA) - 1, ImapCommandGetQuota, NULL, NULL },
    { IMAP_COMMAND_GETQUOTAROOT, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_GETQUOTAROOT) - 1, ImapCommandGetQuotaRoot, NULL, NULL },
    { IMAP_COMMAND_NAMESPACE, IMAP_HELP_NOT_DEFINED, sizeof(IMAP_COMMAND_NAMESPACE) - 1, ImapCommandNameSpace, NULL, NULL },
    { NULL, NULL, 0, NULL, NULL, NULL }
};

#undef DEBUG

#define ImapSessionGet()   MemPrivatePoolGetEntryDirect(IMAPConnectionPool, __FILE__, __LINE__)

void    *IMAPConnectionPool  = NULL;

static void
FreeReturnValueIndex(unsigned long *index)
{
    MemFree(index);
}

static long *
CreateReturnValueIndex()
{
    long status;
    unsigned long count;
    unsigned long i;
    long *index;


    index = MemMalloc(STATUS_MAX * sizeof(unsigned long *));
    if (index) {


        /* find the number of strings */
        count = 0;

        do {
            if (ImapErrorStrings[count].formatString != NULL) {
                count++;
                continue;
            }
            break;
        } while (TRUE);

        for (status = 0; status < STATUS_MAX; status++) {
            index[status] = -1;
            for (i = 0; i < count; i++) {
                if (ImapErrorStrings[i].id != status) {
                    continue;
                }
                index[status] = i;
                break;
            }
        }
        return(index);
    }
    return(NULL);
}

#if 0
// FIXME: Shutdown not needed atm
static BOOL 
IMAPShutdown(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection)
{
    XplThreadID id;

    if (Response) {
        if (!Arguments) {
            if (Imap.server.conn->socket != -1) {
                *Response = MemStrdup("Shutting down.\r\n");
                if (*Response) {
                    id = XplSetThreadGroupID(Imap.server.threadGroupId);

                    Imap.exiting = TRUE;

                    if (Imap.server.conn) {
                        ConnClose(Imap.server.conn);
                        Imap.server.conn = NULL;
                    }

                    XplSignalLocalSemaphore(Imap.server.shutdownSemaphore);

                    if (CloseConnection) {
                        *CloseConnection = TRUE;
                    }

                    XplSetThreadGroupID(id);
                }
            } else if (Imap.exiting) {
                *Response = MemStrdup("Shutdown in progress.\r\n");
            } else {
                *Response = MemStrdup("Unknown shutdown state.\r\n");
            }

            if (*Response) {
                return(TRUE);
            }

            return(FALSE);
        }

        *Response = MemStrdup("Arguments not allowed.\r\n");
        return(TRUE);
    }

    return(FALSE);
}
#endif

static BOOL 
IMAPSessionAllocCB(void *Buffer, void *ClientData)
{
    register ImapSession *session = (ImapSession *)Buffer;
    memset(session, 0, sizeof(ImapSession));
    session->client.state = STATE_FRESH;
   return(TRUE);
}

static BOOL 
IMAPSessionFreeCB(void *Buffer, void *ClientData)
{
    register ImapSession *session = (ImapSession *)Buffer;

    if (session->command.buffer) {
        MemFree(session->command.buffer);
    }
    
    return(TRUE);
}

static void 
ImapSessionReturn(ImapSession *session)
{
    register ImapSession *s = session;
    register unsigned char  *command;

    if (s->command.bufferLen == BUFSIZE) {
        command = s->command.buffer;
    } else {
        if (s->command.buffer != NULL) {
            MemFree(s->command.buffer);
        }

        command = (unsigned char *)MemMalloc(BUFSIZE + 1);
    }

    if (command != NULL) {
        memset(s, 0, sizeof(ImapSession));

        s->client.state = STATE_FRESH;
        s->command.bufferLen = BUFSIZE;
        s->command.buffer = command;

        s->command.buffer[0] = '\0';
    }
    MemPrivatePoolReturnEntry(IMAPConnectionPool, s);

    return;
}



#define FOLDER_INITIAL_SIZE 5





long 
FolderListInitialize(ImapSession *session)
{
    if (!session->folder.list) {

        session->folder.list = MemMalloc(sizeof(FolderInformation) * FOLDER_INITIAL_SIZE);
        if (session->folder.list) {
            session->folder.allocated = FOLDER_INITIAL_SIZE;
            return(STATUS_CONTINUE);
        }
        
        return(STATUS_MEMORY_ERROR);
    }

    /* This should always be null when called */
    return(STATUS_BUG);
}

__inline static void
FolderListClear(FolderInformation *folder, size_t count)
{
    if (count > 0) {
        do {
            MemFree(folder->name.utf8);
            folder->name.utf8 = NULL;
            MemFree(folder->name.utf7);
            folder->name.utf7 = NULL;
            folder++;
            (count)--;
        } while (count > 0);
    }
}

__inline static void 
FolderListShutdown(ImapSession *session)
{
    FolderListClear(session->folder.list, session->folder.count);
    if (session->folder.list) {
        MemFree(session->folder.list);
        session->folder.list = NULL;
    }
}

__inline static long
FolderSetRecentUid(Connection *storeConn, FolderInformation *folder)
{
    long ccode;

    /* Set the new recent uid equal to the uidNext */
    ccode = NMAPSetHexadecimalProperty(storeConn, folder->guid, "imap.recentuid", folder->uidNext);
    if (ccode == 1000)  {
        return(STATUS_CONTINUE);
    }
    return(CheckForNMAPCommError(ccode));
}

__inline static long
FolderWatchAdd(Connection *storeConn, FolderInformation *folder, const char *eventString)
{
    long ccode;

    if ((ccode = CheckConnResultIsNot(-1, STATUS_NMAP_COMM_ERROR, NMAPSendCommandF(storeConn, "WATCH %llx %s\r\n", folder->guid, eventString))) == STATUS_CONTINUE) {
        ccode = CheckStoreResponseCode(1000, NMAPReadResponse(storeConn, NULL, 0, 0));
    }
    return(ccode);
}

__inline static long
FolderWatchRemove(Connection *storeConn, FolderInformation *folder)
{
    long ccode;

    if ((ccode = CheckConnResultIsNot(-1, STATUS_NMAP_COMM_ERROR, NMAPSendCommandF(storeConn, "WATCH %llx\r\n", folder->guid))) == STATUS_CONTINUE) {
        ccode = CheckStoreResponseCode(1000, NMAPReadResponse(storeConn, NULL, 0, 0));
    }
    return(ccode);
}

__inline static long
FolderDeselect(ImapSession *session)
{
    /* This function resets the selected folder to the unselected state */
    long ccode;

    OpenedFolder *selectedFolder = &(session->folder.selected);

    if (session->client.state == STATE_SELECTED) {
        session->client.state = STATE_AUTH;
        ccode = FolderWatchRemove(session->store.conn, selectedFolder->info);
        EventsFree(&(selectedFolder->events));

        if ((ccode == STATUS_CONTINUE) && !(selectedFolder->readOnly)) {
            ccode = FolderSetRecentUid(session->store.conn, selectedFolder->info);
        }
    }

    FolderClose(selectedFolder);
    return(ccode);
}


__inline static long
FolderGetHighestUid(Connection *storeConn, FolderInformation *folder, uint32_t *highestUid)
{
    long ccode;
    unsigned long propertyValue;

    /* Get the highest UID for the collection */
    ccode = NMAPGetHexadecimalProperty(storeConn, folder->guid, "nmap.mail.imapuid", &propertyValue);
    if (ccode == 2001) {
        *highestUid = (uint32_t)propertyValue;
        return(STATUS_CONTINUE);
    }

    return(CheckForNMAPCommError(ccode));
}

__inline static long
FolderGetRecentUid(Connection *storeConn, FolderInformation *folder, uint32_t *recentUid)
{
    long ccode;
    unsigned long propertyValue;

    /* Get the recent UID for the collection */
    ccode = NMAPGetHexadecimalProperty(storeConn, folder->guid, "imap.recentuid", &propertyValue);
    if (ccode == 2001)  {
        *recentUid = (uint32_t)propertyValue;
        return(STATUS_CONTINUE);
    }

    if (ccode == 3245)  {
        *recentUid = 0;
        return(STATUS_CONTINUE);
    }
    return(CheckForNMAPCommError(ccode));

}

__inline static unsigned long
FolderFindFirstUnseen(MessageInformation *messageList, unsigned long messageCount)
{
    unsigned long firstUnseen = 0;
    MessageInformation *currentMessage = &(messageList[0]);

    for (;;) {
        if (currentMessage->flags & STORE_MSG_FLAG_SEEN) {
            firstUnseen++;
            if (firstUnseen < messageCount) {
                currentMessage++;
                continue;
            }
        }
        break;
    }
    return(firstUnseen);
}

__inline static long
FolderSendStatus(Connection *conn, OpenedFolder *folder)
{
    long ccode;
    unsigned long firstUnseen;

    firstUnseen = FolderFindFirstUnseen(folder->message, folder->messageCount);
    if (firstUnseen != folder->messageCount) {
        ccode = ConnWriteF(conn, "* %lu EXISTS\r\n* %lu RECENT\r\n* OK [UNSEEN %lu] Message %lu is first unseen\r\n* OK [UIDVALIDITY %lu] UIDs valid\r\n* OK [UIDNEXT %lu] Predicted next UID\r\n* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)\r\n", 
                           folder->messageCount, 
                           folder->recentCount,
                           firstUnseen + 1,
                           firstUnseen + 1,
                           (unsigned long)(folder->info->guid & 0x00000000FFFFFFFF),
                           (unsigned long)folder->info->uidNext);
    } else {
        ccode = ConnWriteF(conn, "* %lu EXISTS\r\n* %lu RECENT\r\n* OK [UIDVALIDITY %lu] UIDs valid\r\n* OK [UIDNEXT %lu] Predicted next UID\r\n* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)\r\n", 
                           folder->messageCount, 
                           folder->recentCount,
                           (unsigned long)(folder->info->guid & 0x00000000FFFFFFFF),
                           (unsigned long)folder->info->uidNext);
    }
    if (ccode != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}

long
FolderOpen(Connection *storeConn, OpenedFolder *openFolder, FolderInformation *folder, BOOL readOnly)
{
    long ccode;
    uint32_t highestUid = 0;

    ccode = FolderGetRecentUid(storeConn, folder, &folder->uidRecent);
    if (ccode == STATUS_CONTINUE) {
        ccode = FolderGetHighestUid(storeConn, folder, &highestUid);
        if (ccode == STATUS_CONTINUE) {
            openFolder->info = folder;
            ccode = MessageListLoad(storeConn, openFolder);
            if (ccode == STATUS_CONTINUE) {
                /* the nmap.mail.imapuid property is managed by the store agent.  */
                /* On a collection, this property contains the highest uid ever assigned */
                /* to a document in the collection. UIDNEXT needs to be at least one bigger than this value */
                if ((openFolder->messageCount == 0) || (folder->uidNext <= highestUid)) {
                    folder->uidNext = highestUid + 1;
                }
                openFolder->readOnly = readOnly;
                return(STATUS_CONTINUE);
            }
        }
        FolderClose(openFolder);
    }

    return(ccode);
}

__inline static long
FolderSelect(ImapSession *session, char *folderName, BOOL readOnly)
{
    long ccode;
    FolderInformation *folder;

    FolderDeselect(session);

    if ((ccode = FolderGetByName(session, folderName, &folder)) == STATUS_CONTINUE) {
        if ((ccode = FolderWatchAdd(session->store.conn, folder, "FLAGS MODIFIED NEW DELETED")) == STATUS_CONTINUE) {
            if ((ccode = FolderOpen(session->store.conn, &session->folder.selected, folder, readOnly)) == STATUS_CONTINUE) {
                session->client.state = STATE_SELECTED;
                return(STATUS_CONTINUE);
            }
            FolderWatchRemove(session->store.conn, folder);
            EventsFree(&(session->folder.selected.events));
        }
    }
    return(ccode);
}

__inline static long
ParseCollectionLine(char *buffer, FolderInformation *folder)
{
    char *ptr;

    ptr = buffer;
    folder->guid = HexToUInt64(ptr, &ptr);
    if (*ptr == ' ') {
        unsigned long type;
        char *typeStart;
        
        typeStart = ptr + 1;
        do {
            ptr++;
        } while (isdigit(*ptr));

        if ((ptr > typeStart) && 
            (*ptr == ' ')) {
            type = atol(typeStart);
            if (type & (STORE_DOCTYPE_MAIL | STORE_DOCTYPE_FOLDER)) {
                char *start;
                size_t len;
                start = ptr + 1;

                do {
                    ptr++;
                } while (isdigit(*ptr));

                if ((ptr > start) && 
                    (*ptr == ' ')) {
                    folder->flags = atol(start);
                    ptr++;

                    len = strlen(ptr);
                    folder->name.utf8 = MemMalloc(len + 1);
                    if (folder->name.utf8) {
                        char *to, *from;
                        
                        memcpy(folder->name.utf8, ptr, len);
                        folder->name.utf8[len] = '\0';
                        
                        // remove any escaped spaces
                        to = from = folder->name.utf8;
                        while (*from != '\0') {
                            if (*from != '\\') {
                                *to = *from;
                                to++;
                            } else {
                                len--;
                            }
                            from++;
                        }
                        *to = '\0';
                        
                        if (memcmp(folder->name.utf8, "/mail/", strlen("/mail/")) == 0) {
                            len = GetModifiedUtf7FromUtf8(folder->name.utf8 + strlen("/mail/"), len - strlen("/mail/"), &folder->name.utf7);
                            if (len > 0) {
                                return(STATUS_CONTINUE);
                            }
                            MemFree(folder->name.utf8);
                            folder->name.utf8 = NULL;
                        }
                        MemFree(folder->name.utf8);
                        return(STATUS_INVALID_FOLDER_NAME);
                    }
                    return(STATUS_MEMORY_ERROR);
                }
                return(STATUS_NMAP_PROTOCOL_ERROR);
            }
            return(STATUS_NOT_MAIL_FOLDER);
        }
    }
    return(STATUS_NMAP_PROTOCOL_ERROR);
}

static int
FolderCompare(const void *param1, const void *param2)
{
    FolderInformation *folder1 = (FolderInformation *)param1;
    FolderInformation *folder2 = (FolderInformation *)param2;

    if (strncmp("INBOX", folder1->name.utf7, strlen("INBOX")) == 0) {
        if (folder1->name.utf7[strlen("INBOX")] == '\0') {
            /* folder 1 is INBOX; INBOX is always first */
            return(-1);
        }

        if (folder1->name.utf7[strlen("INBOX")] == '/') {
            /* folder1 is a sub folder of INBOX */
            if (strncmp("INBOX", folder2->name.utf7, strlen("INBOX")) != 0) {
                /* folder2 is not related to INBOX; folder1 is first */
                return(-1);
            }

            if (folder2->name.utf7[strlen("INBOX")] == '\0') {
                /* folder 2 is INBOX; INBOX is always first */
                return(1);
            }
                
            if (folder2->name.utf7[strlen("INBOX")] == '/') {
                /* folder2 is also a sub folder of INBOX; break the tie */
                return(strcmp(folder1->name.utf7 + strlen("INBOX/"), folder2->name.utf7 + strlen("INBOX/")));
            }

            /* folder 2 is not related to INBOX; folder 1 is first */
            return(-1);
        }
    }

    /* folder1 is not related to INBOX */
    if (strncmp("INBOX", folder2->name.utf7, strlen("INBOX")) != 0) {
        /* folder2 is not related to INBOX either; normal compare */
        return(strcmp(folder1->name.utf7, folder2->name.utf7));
    }

    if (folder2->name.utf7[strlen("INBOX")] == '\0') {
        /* folder 2 is INBOX; INBOX is always first */
        return(1);
    }
                
    if (folder2->name.utf7[strlen("INBOX")] == '/') {
        /* folder2 is a sub folder of INBOX; folder 2 is first */
        return(1);
    }

    /* folder2 is not related to INBOX either; normal compare */
    return(strcmp(folder1->name.utf7, folder2->name.utf7));
}

long
FolderListLoad(ImapSession *session)
{
    char buffer[CONN_BUFSIZE];
    uint64_t selectedGuid = 0;
    FolderInformation *folderList;
    FolderInformation *newList;
    FolderInformation *currentFolder;
    long ccode;
    long folderCount;
    long allocatedCount;

    allocatedCount = session->folder.allocated;
    folderList = session->folder.list;
    currentFolder = folderList;

    if (session->client.state == STATE_SELECTED) {
        selectedGuid = session->folder.selected.info->guid;
    } 

    /* clear anything that is already there */
    FolderListClear(currentFolder, session->folder.count);
    folderCount = 0;

    if (NMAPSendCommand(session->store.conn, "COLLECTIONS /mail\r\n", strlen("COLLECTIONS /mail\r\n")) != -1) {
        do {
            ccode = NMAPReadResponse(session->store.conn, buffer, sizeof(buffer), TRUE);
            if (ccode == 2001) {
                if (folderCount < allocatedCount) {
                    ;
                } else {
                    newList = MemRealloc(folderList, sizeof(FolderInformation) * allocatedCount * 2);
                    if (newList) {
                        folderList = newList;
                        allocatedCount *= 2;
                        currentFolder = &(folderList[folderCount]); 
                    } else {
                        session->folder.list = folderList;
                        session->folder.allocated = allocatedCount;
                        session->folder.count = 0;
                        return(STATUS_MEMORY_ERROR);
                    }
                }
                
                ccode = ParseCollectionLine(buffer + 5, currentFolder);
                if (ccode == STATUS_CONTINUE) {
                    folderCount++;
                    currentFolder++;
                    continue;
                }

                if ((ccode == STATUS_NOT_MAIL_FOLDER) || (ccode == STATUS_INVALID_FOLDER_NAME)) {
                    continue;
                }

                session->folder.list = folderList;
                session->folder.allocated = allocatedCount;
                session->folder.count = 0;
                return(STATUS_NMAP_PROTOCOL_ERROR);
            }

            if (ccode == 1000) {
                break;
            }

            session->folder.list = folderList;
            session->folder.allocated = allocatedCount;
            session->folder.count = 0;
            return(CheckForNMAPCommError(ccode));
        } while (TRUE);

        qsort(folderList, folderCount, sizeof(FolderInformation), FolderCompare);
        
        session->folder.list = folderList;
        session->folder.allocated = allocatedCount;
        session->folder.count = folderCount;

        if (session->client.state == STATE_SELECTED) {
            FolderInformation *selectedFolder;
            ccode = FolderGetByGuid(session, selectedGuid, &selectedFolder);
            if (ccode == STATUS_CONTINUE) {
                session->folder.selected.info = selectedFolder;
                return(STATUS_CONTINUE);;
            }
            session->client.state = STATE_AUTH;
            FolderDeselect(session);
            if (ccode == STATUS_NO_SUCH_FOLDER) {
                return(STATUS_SELECTED_FOLDER_LOST);
            }
            return(ccode);
        } 

        return(STATUS_CONTINUE);
    }
    return(STATUS_NMAP_COMM_ERROR);
}

#define START_MESSAGE_LIST_ALLOCATION 25

__inline static long
MessageListReset(OpenedFolder *folder)
{
    folder->recentCount = 0;
    folder->messageCount = 0;

    if (folder->messageAllocated > 0) {
        return(STATUS_CONTINUE);
    }

    folder->message = MemMalloc(sizeof(MessageInformation) * START_MESSAGE_LIST_ALLOCATION);
    if (folder->message) {
        folder->messageAllocated = START_MESSAGE_LIST_ALLOCATION;
        return(STATUS_CONTINUE);
    }

    folder->message = NULL;
    folder->messageAllocated = 0;
    folder->messageCount = 0;
    return(STATUS_MEMORY_ERROR);
}

static int
CompareMessageInformation(const void *param1, const void *param2)
{
    MessageInformation *msg1 = (MessageInformation *)param1;
    MessageInformation *msg2 = (MessageInformation *)param2;

    return(msg1->uid - msg2->uid);
}

long
MessageListLoad(Connection *storeConn, OpenedFolder *folder)
{
    char reply[1024];
    long ccode;
    long headerSize;

    if ((ccode = MessageListReset(folder)) == STATUS_CONTINUE) {
        if (NMAPSendCommandF(storeConn, "LIST %llx Pnmap.mail.headersize\r\n", folder->info->guid) != -1) {
            for (;;) {
                ccode = NMAPReadResponse(storeConn, reply, sizeof(reply), TRUE);
                if (ccode == 2001) {
                    ccode = NMAPReadDecimalPropertyResponse(storeConn, "nmap.mail.headersize", &headerSize);
                    if (ccode == 2001) {
                        // don't look at mails which are obviously bad. Bug #11199
                        if (headerSize > 0) {
                            ccode = MessageListAddMessage(folder, reply, headerSize);
                        } else {
                            // ignore this for now - we need to log a message
                            // bug #11200
                        }
                        continue;
                    }

                    if (ccode == 3245) {
                        ccode = MessageListAddMessage(folder, reply, 0);
                        continue;
                    }

                    return(ccode);
                }

                if (ccode == 1000) {
                    break;
                }

                folder->messageCount = 0;
                return(CheckForNMAPCommError(ccode));
            }

            qsort(folder->message, folder->messageCount, sizeof(MessageInformation), CompareMessageInformation);

            return(STATUS_CONTINUE);
        }
        return(STATUS_NMAP_COMM_ERROR);
    }
    return(ccode);
}

static BOOL
SessionCleanup(ImapSession *session)
{
    if (session) {
        if (session->client.state == STATE_EXITING) {
            return(FALSE);
        }
        session->client.state = STATE_EXITING;

        FreeUnseenFlags(session);

        if (session->store.conn) {
            if (session->store.conn->socket != -1) {
                NMAPSendCommand(session->store.conn, "QUIT\r\n", 6);
                ConnClose(session->store.conn);
                session->store.conn->socket = -1;
            }

            ConnFree(session->store.conn);
            session->store.conn = NULL;
        }
        
        if (session->client.conn) {
            if (session->client.conn->socket != -1) {
                ConnFlush(session->client.conn);
                ConnClose(session->client.conn);
                session->client.conn->socket = -1;
            }

            ConnFree(session->client.conn);
            session->client.conn = NULL;
        }

        FolderDeselect(session);
        FolderListShutdown(session);

        if (session->user.name) {
            MemFree(session->user.name);
        }
        ImapSessionReturn(session);
    }
 
    XplSafeDecrement(Imap.session.threads.inUse);
    XplSafeIncrement(Imap.session.served);

    return(TRUE);
}

static int
ImapCommandUnknown(void *param)
{
    ImapSession *session = (ImapSession *)param;

    Log(LOG_INFO, "Unhandled command for user %s", session->user.name);
    
    return(SendError(session->client.conn, "*", session->command.tag, STATUS_UNKNOWN_COMMAND));
}

/********** IMAP session commands - any state (Capability, Noop, Logout) **********/
int
ImapCommandCapability(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;
    
    if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
        if (ConnWrite(session->client.conn, Imap.command.capability.ssl.message, Imap.command.capability.ssl.len) != -1) {
            return(SendOk(session, "CAPABILITY"));
        }
        return(STATUS_ABORT);
    }
    return(SendError(session->client.conn, session->command.tag, "CAPABILITY", ccode));
}

int
ImapCommandNoop(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;

    if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
        return(SendOk(session, "NOOP"));
    }
    return(SendError(session->client.conn, session->command.tag, "NOOP", ccode));
}

int
ImapCommandLogout(void *param)
{
    ImapSession *session = (ImapSession *)param;

    if (ConnWrite(session->client.conn, "* BYE IMAP4rev1 Server signing off\r\n", 36) != -1) {
        if (SendOk(session,"LOGOUT") == STATUS_CONTINUE) {
            ConnFlush(session->client.conn);
        }
    }
    
    return(STATUS_ABORT);
}


/********** IMAP session commands - authenticated state **********/

int
ImapCommandSelect(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    char *ptr;
    FolderPath path;

    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        ccode = GetPathArgument(session, session->command.buffer + strlen("SELECT"), &ptr, &path, FALSE);
        if (ccode == STATUS_CONTINUE) {
            ccode = FolderListLoad(session);
            if (ccode == STATUS_CONTINUE) {
                ccode = FolderSelect(session, path.name, FALSE);
                if (ccode == STATUS_CONTINUE) {
                    ccode = FolderSendStatus(session->client.conn, &session->folder.selected);
                    if (ccode == STATUS_CONTINUE) {
                        FreePathArgument(&path);
                        return(SendOk(session, "[READ-WRITE] SELECT"));
                    }
                }
            }
            FreePathArgument(&path);
        }
        return(SendError(session->client.conn, session->command.tag, "SELECT", ccode));
    }
    return(SendError(session->client.conn, session->command.tag, "SELECT", STATUS_INVALID_STATE));
}

int
ImapCommandExamine(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    char *ptr;
    FolderPath path;

    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        ccode = GetPathArgument(session, session->command.buffer + strlen("EXAMINE"), &ptr, &path, FALSE);
        if (ccode == STATUS_CONTINUE) {
            ccode = FolderListLoad(session);
            if (ccode == STATUS_CONTINUE) {
                ccode = FolderSelect(session, path.name, TRUE);
                if (ccode == STATUS_CONTINUE) {
                    ccode = FolderSendStatus(session->client.conn, &session->folder.selected);
                    if (ccode == STATUS_CONTINUE) {
                        FreePathArgument(&path);
                        return(SendOk(session, "[READ-ONLY] EXAMINE"));
                    }
                }
            }
            FreePathArgument(&path);
        }
        return(SendError(session->client.conn, session->command.tag, "EXAMINE", ccode));
    }
    return(SendError(session->client.conn, session->command.tag, "EXAMINE", STATUS_INVALID_STATE));
}

__inline static FolderInformation *
FolderGet(ImapSession *session, char *folderName)
{
    unsigned long i;
    FolderInformation *folder;

    folder = &session->folder.list[0];
    for(i = 0; i < session->folder.count; i++) {
        if (strcmp(folderName, folder->name.utf7)) {
            folder++;
            continue;
        }
        return(folder);
    }
    return(NULL);
}

__inline static BOOL
FolderHasSubFolders(ImapSession *session, FolderInformation *folder)
{
    unsigned long folderLen;
    FolderInformation *nextFolder;

    if ((unsigned long)((folder - session->folder.list) + 1) < session->folder.count) {
        folderLen = strlen(folder->name.utf7);
        nextFolder = folder + 1;
        if (strncmp(folder->name.utf7, nextFolder->name.utf7, folderLen) == 0) {
            if ((nextFolder->name.utf7)[folderLen] == '/') {
                return(TRUE);
            }
        }        
    }
    return(FALSE);
}

__inline static BOOL
FolderIsSelected(ImapSession *session, char *folderName)
{
    unsigned long i;
    FolderInformation *folder;

    folder = &session->folder.list[0];
    for(i = 0; i < session->folder.count; i++) {
        if (strcmp(folderName, folder->name.utf7)) {
            folder++;
            continue;
        }
        if (folder == session->folder.selected.info) {
            return(TRUE);
        }
        break;
    }
    return(FALSE);
}

__inline static BOOL
FolderExists(ImapSession *session, char *folderPathUtf8)
{
    unsigned long i;
    FolderInformation *folder;

    folder = &session->folder.list[0];
    for(i = 0; i < session->folder.count; i++) {
        if (strcmp(folderPathUtf8, folder->name.utf8 + strlen("/mail/"))) {
            folder++;
            continue;
        }
        return(TRUE);
    }
    return(FALSE);
}


__inline static long
FolderHierarchyCreate(ImapSession *session, char *folderPathUtf8)
{
    char *ptr;
    long ccode;

    ptr = folderPathUtf8;

    while ((ptr = strchr(ptr, '/')) != NULL) {
        *ptr = '\0';
        if (FolderExists(session, folderPathUtf8)) {
            *ptr = '/';
            ptr++;
            continue;
        }
        
        do {
            *ptr = '\0';
            if (NMAPSendCommandF(session->store.conn, "CREATE /mail/%s\r\n", folderPathUtf8) != -1) {
                ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
                if (ccode == 1000) {
                    if (NMAPSendCommandF(session->store.conn, "FLAG /mail/%s +%u\r\n", folderPathUtf8, STORE_COLLECTION_FLAG_HIERARCHY_ONLY) != -1) {
                        ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
                        if (ccode == 1000) {
                            *ptr = '/';
                            ptr++;
                            continue;
                        }
                        *ptr = '/';
                        return(ccode);
                    }
                    *ptr = '/';
                    return(STATUS_NMAP_COMM_ERROR);
                }
                *ptr = '/';
                return(ccode);
            }
            *ptr = '/';
            return(STATUS_NMAP_COMM_ERROR);

        } while((ptr = strchr(ptr, '/')) != NULL);
        break;
    }
    return(STATUS_CONTINUE);
}


int
ImapCommandCreate(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode; 
    char *ptr;
    FolderPath path;
    char *pathUtf8;
    int len;
    FolderInformation *folder;

    if ((ccode = CheckState(session, STATE_AUTH)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "CREATE", ccode));
    }
    
    if ((ccode = EventsSend(session, STORE_EVENT_ALL)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "CREATE", ccode));
    }

    ccode = GetPathArgument(session, session->command.buffer + strlen("CREATE"), &ptr, &path, FALSE);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "CREATE", ccode));        
    }

    /* prohibition of naming a folder the same name as the userid;   don't know why (rprice) */
    if (XplStrCaseCmp(path.name, session->user.name) == 0) {
        FreePathArgument(&path);
        return(SendError(session->client.conn, session->command.tag, "CREATE", STATUS_INVALID_FOLDER_NAME));
    }

    len = GetUtf8FromModifiedUtf7(path.name, path.nameLen, &pathUtf8);
    if (len < 1) {
        if (len == 0) {
            MemFree(pathUtf8);
        }
        FreePathArgument(&path);
        return(SendError(session->client.conn, session->command.tag, "CREATE", STATUS_INVALID_FOLDER_NAME));
    }

    ccode = ValidateUtf7Utf8Pair(path.name, path.nameLen, pathUtf8, len);
    if (ccode != UTF7_STATUS_SUCCESS) {
        FreePathArgument(&path);
        MemFree(pathUtf8);
        return(SendError(session->client.conn, session->command.tag, "CREATE", STATUS_INVALID_FOLDER_NAME));
    }

    /* Make sure the folder information is up to date */
    ccode = FolderListLoad(session);
    if (ccode != STATUS_CONTINUE) {
        FreePathArgument(&path);
        MemFree(pathUtf8);
        return(SendError(session->client.conn, session->command.tag, "CREATE", ccode));
    }

    folder = FolderGet(session, path.name);
    FreePathArgument(&path);
    if (folder) {
        /* The collection is already there */
        if ((folder->flags & STORE_COLLECTION_FLAG_HIERARCHY_ONLY) == 0) {
            /* this is a full fledge folder */
            MemFree(pathUtf8);
            return(SendError(session->client.conn, session->command.tag, "CREATE", STATUS_FOLDER_ALREADY_EXISTS));
        }

        /* this folder is just hierarchy.  Make it a real folder */
        if (NMAPSendCommandF(session->store.conn, "FLAG /mail/%s -%u\r\n", pathUtf8, STORE_COLLECTION_FLAG_HIERARCHY_ONLY) == -1) {
            MemFree(pathUtf8);
            return(STATUS_NMAP_COMM_ERROR);
        }
        
        folder->flags &= ~(STORE_COLLECTION_FLAG_HIERARCHY_ONLY);

        MemFree(pathUtf8);
        ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
        if (ccode != 1000) {
            return(ccode);
        }

        return(SendOk(session,"CREATE"));
    }

    ccode = FolderHierarchyCreate(session, pathUtf8);
    if (ccode != STATUS_CONTINUE) {
        MemFree(pathUtf8);
        return(SendError(session->client.conn, session->command.tag, "CREATE", ccode));
    }
    
    if (NMAPSendCommandF(session->store.conn, "CREATE \"/mail/%s\"\r\n", pathUtf8) == -1) {
        MemFree(pathUtf8);
        return(SendError(session->client.conn, session->command.tag, "CREATE", STATUS_NMAP_COMM_ERROR));
    }
    
    MemFree(pathUtf8);
    ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
    if (ccode != 1000) {
        return(SendError(session->client.conn, session->command.tag, "CREATE", CheckForNMAPCommError(ccode)));
    }
 
    return(SendOk(session,"CREATE"));
}


__inline static uint64_t
ParseMessageGuid(char *ptr)
{
    uint64_t guid;
    unsigned long type;

    if (*ptr == ' ') {
        ptr++;

        guid = HexToUInt64(ptr, &ptr);
        if (*ptr == ' ') {
            ptr++;
            type = strtoul(ptr, NULL, 10);
            if (!(type & STORE_DOCTYPE_FOLDER) && (type & STORE_DOCTYPE_MAIL)) {
                return(guid);
            }
        }
    }

    return(0);
}

__inline static long
FolderGetMessageGuids(ImapSession *session, char *collectionName, uint64_t **list, unsigned long *count)
{
    long ccode;
    char buffer[CONN_BUFSIZE];
    uint64_t *guid;
    uint64_t *tmpList;
    long guidCount;
    long allocCount;

    if (NMAPSendCommandF(session->store.conn, "LIST %s\r\n", collectionName) != -1) {

        guidCount = 0;
        allocCount = 50;

        guid = MemMalloc(sizeof(uint64_t) * 50);
                
        for (;;) {
            ccode = NMAPReadResponse(session->store.conn, buffer, sizeof(buffer), TRUE);
            if (ccode == 2001) {
                if (guid) {
                    if (guidCount < allocCount) {
                        guid[guidCount] = ParseMessageGuid(buffer + 4);
                        if (guid[guidCount]) {
                            guidCount++;
                        }
                        continue;
                    } 

                    tmpList = MemRealloc(guid, sizeof(uint64_t) * allocCount * 2);
                    if (tmpList) {
                        guid = tmpList;
                        allocCount *= 2;

                        guid[guidCount] = ParseMessageGuid(buffer + 4);
                        if (guid[guidCount]) {
                            guidCount++;
                        }
                        continue;
                    }

                    MemFree(guid);
                    guid = NULL;
                }

                /* keep reading to clean out the network buffer */
                continue;
            }

            if (ccode == 1000) {
                if (guid) {
                    *list = guid;
                    *count = guidCount;
                    return(STATUS_CONTINUE);
                }
                    
                return(STATUS_MEMORY_ERROR);
            }

            if (guid) {
                MemFree(guid);
            }

            return(CheckForNMAPCommError(ccode));
        }
    }

    return(STATUS_NMAP_COMM_ERROR);
}


__inline static long
FolderConvertToHierarchyOnly(ImapSession *session, FolderInformation *folder)
{
    long ccode;
    uint64_t *guid;
    uint64_t *guidList = NULL;
    long guidCount;

    if (NMAPSendCommandF(session->store.conn, "FLAG %s +%u\r\n", folder->name.utf8, STORE_COLLECTION_FLAG_HIERARCHY_ONLY) == -1) {
        return(STATUS_NMAP_COMM_ERROR);
    }

    ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
    if (ccode != 1000) {
        return(CheckForNMAPCommError(ccode));
    }

    folder->flags |= STORE_COLLECTION_FLAG_HIERARCHY_ONLY;
    ccode = FolderGetMessageGuids(session, folder->name.utf8, &guidList, &guidCount);
    if (ccode != STATUS_CONTINUE) {
        return(ccode);
    }

    guid = &guidList[0];

    while(guidCount > 0) {
        if (NMAPSendCommandF(session->store.conn, "PURGE %llx\r\n", *guid) != -1) {
            ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
            if ((ccode == 1000) || (ccode == 4220)) {
                guid++;
                guidCount--;
                continue;
            }
            MemFree(guidList);
            return(CheckForNMAPCommError(ccode));
        }
        MemFree(guidList);
        return(STATUS_NMAP_COMM_ERROR);
    }
    MemFree(guidList);
    return(STATUS_CONTINUE);
}

int
ImapCommandDelete(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;
    char *ptr;
    FolderPath path;
    FolderInformation *folder;

    if ((ccode = CheckState(session, STATE_AUTH)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "DELETE", ccode));
    }
    
    if ((ccode = EventsSend(session, STORE_EVENT_ALL)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "DELETE", ccode));
    }

    ccode = GetPathArgument(session, session->command.buffer + strlen("DELETE"), &ptr, &path, FALSE);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "DELETE", ccode));        
    }

    if(strcmp(path.name, "INBOX") == 0) {
        MemFree(path.buffer);
        return(SendError(session->client.conn, session->command.tag, "DELETE", STATUS_INVALID_FOLDER_NAME));
    }

    ccode = FolderListLoad(session);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "DELETE", ccode));        
    }

    ccode = FolderGetByName(session, path.name, &folder);
    if (ccode != STATUS_CONTINUE) {
        MemFree(path.buffer);
        return(SendError(session->client.conn, session->command.tag, "DELETE", ccode));
    }

    MemFree(path.buffer);

    if (FolderHasSubFolders(session, folder)) {
        if (folder->flags & STORE_COLLECTION_FLAG_HIERARCHY_ONLY) {
            /* Can't delete hierarchy for other folders */
            return(SendError(session->client.conn, session->command.tag, "DELETE", STATUS_FOLDER_HAS_INFERIOR));
        }

        if (folder == session->folder.selected.info) {
            FolderDeselect(session);
        }

        /* delete the folder and leave the hierarchy */
        ccode = FolderConvertToHierarchyOnly(session, folder);
        if (ccode != STATUS_CONTINUE) {
            return(SendError(session->client.conn, session->command.tag, "DELETE", ccode));
        }

        return(SendOk(session, "DELETE"));
    }

    /* delete the collection */
    if (folder == session->folder.selected.info) {
        FolderDeselect(session);
    }

    if (NMAPSendCommandF(session->store.conn, "REMOVE \"%s\"\r\n", folder->name.utf8) == -1) {
        return(SendError(session->client.conn, session->command.tag, "DELETE", STATUS_NMAP_COMM_ERROR));
    }

    ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
    if (ccode != 1000) {
        return(SendError(session->client.conn, session->command.tag, "DELETE", CheckForNMAPCommError(ccode)));
    }

    return(SendOk(session, "DELETE"));
}

__inline static long
FolderMoveMessages(ImapSession *session, char *sourcePathUtf8, char *destinationPathUtf8)
{
    long ccode;
    uint64_t *guidList = NULL;
    uint64_t *guid;
    long guidCount;

    ccode = FolderGetMessageGuids(session, sourcePathUtf8, &guidList, &guidCount);
    if (ccode != STATUS_CONTINUE) {
        return(ccode);
    }

    guid = &guidList[0];

    while(guidCount > 0) {
        if (NMAPSendCommandF(session->store.conn, "MOVE %llx /mail/%s\r\n", *guid, destinationPathUtf8) != -1) {
            ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
            if ((ccode == 1000) || (ccode == 4220)) {
                guid++;
                guidCount--;
                continue;
            }
        }
        MemFree(guidList);
        return(STATUS_NMAP_COMM_ERROR);
    }
    MemFree(guidList);
    return(STATUS_CONTINUE);
}

__inline static BOOL
SelectedFolderWillBeRenamed(FolderInformation *source, FolderInformation *selected)
{
    unsigned long len;

    if (!selected) {
        return(FALSE);
    }

    if (source == selected) {
        return(TRUE);
    }

    /* check to see if selected is a subfolder of source */    
    len = strlen(source->name.utf7);
    if (strncmp(source->name.utf7, selected->name.utf7, len) != 0) {
        return(FALSE);
    }

    if (source->name.utf7[len] == '/') {
        return(TRUE);
    }

    return(FALSE);
}

int
ImapCommandRename(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned long len;
    char *ptr;
    FolderPath sourcePath;
    FolderInformation *sourceFolder;
    FolderPath destinationPath;
    FolderInformation *destinationFolder;
    char *destinationPathUtf8;

    if ((ccode = CheckState(session, STATE_AUTH)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));
    }
    
    if ((ccode = EventsSend(session, STORE_EVENT_ALL)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));
    }

    ccode = GetPathArgument(session, session->command.buffer + strlen("RENAME"), &ptr, &sourcePath, FALSE);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));        
    }

    ccode = GetPathArgument(session, ptr, &ptr, &destinationPath, FALSE);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));        
    }

    ccode = FolderListLoad(session);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));        
    }

    /* make sure source folder does exist */
    ccode = FolderGetByName(session, sourcePath.name, &sourceFolder);
    if (ccode != STATUS_CONTINUE) {
        FreePathArgument(&sourcePath);
        FreePathArgument(&destinationPath);
        return(SendError(session->client.conn, session->command.tag, "RENAME", STATUS_NO_SUCH_FOLDER));
    }

    FreePathArgument(&sourcePath);

    /* make sure destination folder does not exist */
    ccode = FolderGetByName(session, destinationPath.name, &destinationFolder);
    if (ccode == STATUS_CONTINUE) {
        FreePathArgument(&destinationPath);
        return(SendError(session->client.conn, session->command.tag, "RENAME", STATUS_FOLDER_ALREADY_EXISTS));
    }

    if (ccode != STATUS_NO_SUCH_FOLDER) {
        FreePathArgument(&destinationPath);
        return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));
    }

    /* Generate Utf8 for destination */
    len = GetUtf8FromModifiedUtf7(destinationPath.name, destinationPath.nameLen, &destinationPathUtf8);
    if (len < 1) {
        if (len == 0) {
            MemFree(destinationPathUtf8);
        }
        FreePathArgument(&destinationPath);
        return(SendError(session->client.conn, session->command.tag, "RENAME", STATUS_INVALID_FOLDER_NAME));
    }

    ccode = ValidateUtf7Utf8Pair(destinationPath.name, destinationPath.nameLen, destinationPathUtf8, len);
    if (ccode != UTF7_STATUS_SUCCESS) {
        FreePathArgument(&destinationPath);
        MemFree(destinationPathUtf8);
        return(SendError(session->client.conn, session->command.tag, "RENAME", STATUS_INVALID_FOLDER_NAME));
    }

    FreePathArgument(&destinationPath);

    ccode = FolderHierarchyCreate(session, destinationPathUtf8);
    if (ccode != STATUS_CONTINUE) {
        MemFree(destinationPathUtf8);
        return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));
    }

    if (strcmp(sourceFolder->name.utf7, "INBOX") != 0) {
        /* this is NOT inbox, just do the normal thing */
        if (SelectedFolderWillBeRenamed(sourceFolder, session->folder.selected.info)) {
            FolderDeselect(session);
        } 

        if (NMAPSendCommandF(session->store.conn, "RENAME \"%s\" \"/mail/%s\"\r\n", sourceFolder->name.utf8, destinationPathUtf8) == -1) {
            MemFree(destinationPathUtf8);
            return(SendError(session->client.conn, session->command.tag, "RENAME", STATUS_NMAP_COMM_ERROR));
        }
    
        ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
        if (ccode != 1000) {
            MemFree(destinationPathUtf8);
            return(SendError(session->client.conn, session->command.tag, "RENAME", CheckForNMAPCommError(ccode)));
        }
    } else {
        /* we are renaming INBOX! rfc defines special behavior */
        /* create new folder, */
        /* move the contents of INBOX to the new folder, and  */
        /* leave INBOX with it sub-hierarchy */
        if (NMAPSendCommandF(session->store.conn, "CREATE /mail/%s\r\n", destinationPathUtf8) == -1) {
            MemFree(destinationPathUtf8);
            return(SendError(session->client.conn, session->command.tag, "RENAME", STATUS_NMAP_COMM_ERROR));
        }
    
        ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
        if (ccode != 1000) {
            MemFree(destinationPathUtf8);
            return(SendError(session->client.conn, session->command.tag, "RENAME", CheckForNMAPCommError(ccode)));
        }

        ccode = FolderMoveMessages(session, sourceFolder->name.utf8, destinationPathUtf8);

        if (ccode != STATUS_CONTINUE) {
            MemFree(destinationPathUtf8);
            return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));
        }

        if (sourceFolder == session->folder.selected.info) {
            ccode = MessageListLoad(session->store.conn, &session->folder.selected);
            if (ccode != STATUS_CONTINUE) {
                MemFree(destinationPathUtf8);
                return(SendError(session->client.conn, session->command.tag, "RENAME", ccode));
            }
        } 
    }

    MemFree(destinationPathUtf8);

    return(SendOk(session,"RENAME"));
}

int
ImapCommandSubscribe(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;
    char *ptr;
    FolderPath path;
    FolderInformation *folder;

    if ((ccode = CheckState(session, STATE_AUTH)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "SUBSCRIBE", ccode));
    }
    
    if ((ccode = EventsSend(session, STORE_EVENT_ALL)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "SUBSCRIBE", ccode));
    }

    ccode = GetPathArgument(session, session->command.buffer + strlen("SUBSCRIBE"), &ptr, &path, FALSE);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "SUBSCRIBE", ccode));        
    }

    ccode = FolderListLoad(session);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "SUBSCRIBE", ccode));        
    }

    ccode = FolderGetByName(session, path.name, &folder);
    if (ccode != STATUS_CONTINUE) {
        FreePathArgument(&path);
        return(SendError(session->client.conn, session->command.tag, "SUBSCRIBE", STATUS_NO_SUCH_FOLDER));
    }

    if (NMAPSendCommandF(session->store.conn, "FLAG \"%s\" -%u\r\n", folder->name.utf8, STORE_COLLECTION_FLAG_NON_SUBSCRIBED) != -1) {
        FreePathArgument(&path);
        ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
        if (ccode == 1000) {
            return(SendOk(session, "SUBSCRIBE"));
        }
        return(SendError(session->client.conn, session->command.tag, "SUBSCRIBE", CheckForNMAPCommError(ccode)));
    }
    return(SendError(session->client.conn, session->command.tag, "SUBSCRIBE", STATUS_NMAP_COMM_ERROR));
}

int
ImapCommandUnsubscribe(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;
    char *ptr;
    FolderPath path;
    FolderInformation *folder;

    if ((ccode = CheckState(session, STATE_AUTH)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "UNSUBSCRIBE", ccode));
    }
    
    if ((ccode = EventsSend(session, STORE_EVENT_ALL)) != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "UNSUBSCRIBE", ccode));
    }

    ccode = GetPathArgument(session, session->command.buffer + strlen("UNSUBSCRIBE"), &ptr, &path, FALSE);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "UNSUBSCRIBE", ccode));        
    }

    ccode = FolderListLoad(session);
    if (ccode != STATUS_CONTINUE) {
        return(SendError(session->client.conn, session->command.tag, "UNSUBSCRIBE", ccode));        
    }

    ccode = FolderGetByName(session, path.name, &folder);
    if (ccode != STATUS_CONTINUE) {
        FreePathArgument(&path);
        return(SendError(session->client.conn, session->command.tag, "UNSUBSCRIBE", STATUS_NO_SUCH_FOLDER));
    }

    if (NMAPSendCommandF(session->store.conn, "FLAG \"%s\" +%u\r\n", folder->name.utf8, STORE_COLLECTION_FLAG_NON_SUBSCRIBED) != -1) {
        FreePathArgument(&path);
        ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
        if (ccode == 1000) {
            return(SendOk(session, "UNSUBSCRIBE"));
        }
        return(SendError(session->client.conn, session->command.tag, "UNSUBSCRIBE", CheckForNMAPCommError(ccode)));
    }
    return(SendError(session->client.conn, session->command.tag, "UNSUBSCRIBE", STATUS_NMAP_COMM_ERROR));
}


__inline static long
SendFolderInfo(Connection *conn, const char *command, FolderInformation *folder)
{
    if (ConnWriteF(conn, "* %s (%s) \"/\" \"%s\"\r\n", command, ((folder->flags & STORE_COLLECTION_FLAG_HIERARCHY_ONLY) == 0) ? "" : "\\NoSelect", folder->name.utf7) != -1) {
        return(STATUS_CONTINUE);
    }
    return(STATUS_ABORT);
}


__inline static int
SendMatchingFolders(ImapSession *session, const char *command, char *pattern, const BOOL skipUnsubscribed, const BOOL skipSubFolders)
{
    long ccode;
    unsigned long i;
    unsigned long len;
    FolderInformation *folder;

    folder = &session->folder.list[0];

    if (*pattern == '\0') {
        for (i = 0; i < session->folder.count; i++) {
            if ((skipUnsubscribed) && (folder->flags & STORE_COLLECTION_FLAG_NON_SUBSCRIBED)) {
                folder++;
                continue;
            }

            if (skipSubFolders && (strchr(folder->name.utf7, '/'))) {
                folder++;
                continue;
            }

            ccode = SendFolderInfo(session->client.conn, command, folder);
            if (ccode == STATUS_CONTINUE) {
                folder++;
                continue;
            }
            return(ccode);
        }
        return(STATUS_CONTINUE);
    }

    len = strlen(pattern);

    for (i = 0; i < session->folder.count; i++) {
        if (strncmp(folder->name.utf7, pattern, len)) {
            folder++;
            continue;
        }

        if ((skipUnsubscribed) && (folder->flags & STORE_COLLECTION_FLAG_NON_SUBSCRIBED)) {
            folder++;
            continue;
        }

        if (skipSubFolders && (strchr(folder->name.utf7 + len, '/'))) {
            folder++;
            continue;
        }

        ccode = SendFolderInfo(session->client.conn, command, folder);
        if (ccode == STATUS_CONTINUE) {
            folder++;
            continue;
        }
        return(ccode);
    }
    return(STATUS_CONTINUE);
}

__inline static long
SendSingleFolder(ImapSession *session, const char *command, char *pattern)
{
    long ccode;
    FolderInformation specialFolder;
    FolderInformation *folder;

    /* Handle RFC special command "" on the name */
    if (*pattern == '\0') {
        specialFolder.name.utf7 = "";
        specialFolder.flags = STORE_COLLECTION_FLAG_HIERARCHY_ONLY;
        if (SendFolderInfo(session->client.conn, command, &specialFolder) == STATUS_CONTINUE) {
            return(STATUS_CONTINUE);
        }
        return(STATUS_ABORT);
    }

    ccode = FolderGetByName(session, pattern, &folder);
    if (ccode == STATUS_CONTINUE) {
        if (SendFolderInfo(session->client.conn, command, folder) == STATUS_CONTINUE) {
            return(STATUS_CONTINUE);
        }
        return(STATUS_ABORT);
    }

    return(ccode);
}

__inline static long
GetSearchPattern(ImapSession *session, char *start, char *patternBuffer, unsigned long patternBufferLen, unsigned long *patternLen)
{
    long ccode;
    char *ptr;
    FolderPath context;
    FolderPath path;

    ccode = GetPathArgument(session, start, &ptr, &context, TRUE);
    if (ccode == STATUS_CONTINUE) {
        ccode = GetPathArgument(session, ptr, &ptr, &path, TRUE);
        if (ccode == STATUS_CONTINUE) {
            if ((context.nameLen + path.nameLen + 1) < patternBufferLen) {
                if (context.name[0] == '\0') {
                    memcpy(patternBuffer, path.name, path.nameLen);
                    (patternBuffer)[path.nameLen] = '\0';
                    *patternLen = path.nameLen;
                } else if (path.name[0] == '\0') {
                    memcpy(patternBuffer, context.name, context.nameLen);
                    (patternBuffer)[context.nameLen] = '\0';
                    *patternLen = context.nameLen;
                } else {
                    ptr = patternBuffer;
                    memcpy(ptr, context.name, context.nameLen);
                    ptr += context.nameLen;

                    *ptr = '/';
                    ptr++;
            
                    memcpy(ptr, path.name, path.nameLen);
                    ptr += path.nameLen;

                    *ptr = '\0';
                    *patternLen = ptr - patternBuffer;
                }

                FreePathArgument(&context);
                FreePathArgument(&path);
                return(STATUS_CONTINUE);
            }
            ccode = STATUS_PATH_TOO_LONG;
            FreePathArgument(&path);
        }
        FreePathArgument(&context);
    }
    return(ccode);
}

__inline static long
FolderSend(ImapSession *session, const char *command, char *pattern, unsigned long patternLen, BOOL skipUnsubscribed)
{
    long ccode;
    char *wildcard = pattern + patternLen - 1;
                
    if (*wildcard == '*') {
        *wildcard = '\0';
        ccode = SendMatchingFolders(session, command, pattern, FALSE, FALSE);
    } else if (*wildcard == '%') {
        *wildcard = '\0';
        ccode = SendMatchingFolders(session, command, pattern, FALSE, TRUE);
    } else {
        ccode = SendSingleFolder(session, command, pattern);
    }
    return(ccode);
}

int
ImapCommandList(void *param)
{
    ImapSession *session = (ImapSession *)param;

    long ccode;
    char pattern[STORE_MAX_COLLECTION * 2];
    unsigned long patternLen = 0;

    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            if ((ccode = GetSearchPattern(session, session->command.buffer + strlen("LIST"), pattern, sizeof(pattern), &patternLen)) == STATUS_CONTINUE) {
                if ((ccode = FolderListLoad(session)) == STATUS_CONTINUE) {
                    if ((ccode = FolderSend(session, "LIST", pattern, patternLen, FALSE)) == STATUS_CONTINUE) {
                        return(SendOk(session, "LIST"));
                    }
                }
            }
        }
    }
    return(SendError(session->client.conn, session->command.tag, "LIST", ccode));
}

int
ImapCommandLsub(void *param)
{
    ImapSession *session = (ImapSession *)param;

    long ccode;
    char pattern[STORE_MAX_COLLECTION * 2];
    unsigned long patternLen = 0;

    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            if ((ccode = GetSearchPattern(session, session->command.buffer + strlen("LSUB"), pattern, sizeof(pattern), &patternLen)) == STATUS_CONTINUE) {
                if ((ccode = FolderListLoad(session)) == STATUS_CONTINUE) {
                    if ((ccode = FolderSend(session, "LSUB", pattern, patternLen, TRUE)) == STATUS_CONTINUE) {
                        return(SendOk(session, "LSUB"));
                    }
                }
            }
        }
    }
    return(SendError(session->client.conn, session->command.tag, "LSUB", ccode));
}

typedef struct {
    unsigned long totalCount;
    unsigned long recentCount;
    unsigned long readCount;
    unsigned long purgedCount;
    unsigned long uidNext;
    unsigned long mboxUid;
} CheckResult;

static unsigned long
CheckMessages(OpenedFolder *openFolder)
{
    return(openFolder->messageCount);
}

static unsigned long
CheckRecent(OpenedFolder *openFolder)
{
    return(openFolder->recentCount);
}

static unsigned long
CheckUidnext(OpenedFolder *openFolder)
{
    return(openFolder->info->uidNext);
}

static unsigned long
CheckUidvalidity(OpenedFolder *openFolder)
{
    return((unsigned long)(openFolder->info->guid & 0x00000000FFFFFFFF));
}

static unsigned long
CheckUnseen(OpenedFolder *openFolder)
{
    MessageInformation *message;
    unsigned long count = openFolder->messageCount;
    unsigned long unseenCount = 0;

    message = &openFolder->message[0];
    while (count> 0) {
        if (!(message->flags & STORE_MSG_FLAG_SEEN)) {
            unseenCount++;
        }
        message++;
        count--;
    }

    return(unseenCount);
}

typedef unsigned long (* CheckResultFunction)(OpenedFolder *openFolder);

typedef struct {
    unsigned char *string;
    unsigned long stringLen;
    CheckResultFunction function;
} StatusCommandOption;

static StatusCommandOption StatusCommandOptions[] = {
    { "MESSAGES",       sizeof("MESSAGES") - 1,     CheckMessages },
    { "RECENT",         sizeof("RECENT") - 1,       CheckRecent },
    { "UIDNEXT",        sizeof("UIDNEXT") - 1,      CheckUidnext },
    { "UIDVALIDITY",    sizeof("UIDVALIDITY") - 1,  CheckUidvalidity },
    { "UNSEEN",         sizeof("UNSEEN") - 1,       CheckUnseen },
    { NULL,             0,                          0 }
};

BOOL
CommandStatusInit()
{
    BongoKeywordIndexCreateFromTable(Imap.command.status.optionIndex, StatusCommandOptions, .string, TRUE);
    if (Imap.command.status.optionIndex) {
        return(TRUE);
    }
    return(FALSE);
}

void
CommandStatusCleanup()
{
    BongoKeywordIndexFree(Imap.command.status.optionIndex);
}

#if 0
if (NMAPSendCommandF(session->store.conn, "CHECK %s\r\n", path) != -1) {
    ccode = NMAPReadResponse(session->store.conn, Reply, sizeof(Reply), TRUE);
    if (ccode == 1000) {
        CheckResult checkResults;
        unsigned char *ptr,*DoSpace="";

        ptr=items;
        if (*ptr=='(')
            ptr++;

        // Total, Recent, Purged, Read, IDs
        sscanf(Reply, "%lu %*u %lu %*u %lu %*u %lu %*u %lu %lu", 
               &checkResults.totalCount, 
               &checkResults.recentCount, 
               &checkResults.purgedCount, 
               &checkResults.readCount, 
               &checkResults.uidNext, 
               &checkResults.mboxUid);

        AddMboxSpaces(path);
        if (ConnWriteF(session->client.conn, "* STATUS \"%s\" (", path) != -1) {
            long option;
            while (*ptr) {
                option = BongoKeywordBegins(Imap.command.status.optionIndex, ptr);
                if (option != -1) {
                    if (ConnWriteF(session->client.conn, "%s%s %lu", DoSpace, StatusCommandOptions[option].string, StatusCommandOptions[option].function(&checkResults)) != -1) {
                        ptr += StatusCommandOptions[option].stringLen;
                        if (*ptr == ' ') {
                            ptr++;
                            DoSpace = " ";
                            continue;
                        }
                        break;
                    }
                    MemFree(items);
                    MemFree(path);
                    return(STATUS_ABORT);
                }
                MemFree(items);
                MemFree(path);
                if (ConnWrite(session->client.conn, "\r\n", sizeof("\r\n") - 1) != -1) {
                    return(SendError(session->client.conn, session->command.tag, "STATUS", STATUS_INVALID_ARGUMENT));
                }
                return(STATUS_ABORT);
            }
                    
            MemFree(items);
            MemFree(path);
            if (ConnWrite(session->client.conn, ")\r\n", 3) != -1) {
                return(SendOk(session, "STATUS"));
            }
            return(STATUS_ABORT);
        }
        MemFree(items);
        MemFree(path);
        return(STATUS_ABORT);
    }
    MemFree(items);
    MemFree(path);
    return(SendError(session->client.conn, session->command.tag, "STATUS", CheckForNMAPCommError(ccode)));
}
MemFree(path);
MemFree(items);
return(SendError(session->client.conn, session->command.tag, "STATUS", STATUS_NMAP_COMM_ERROR));
#endif


__inline static long
SendStatusResponse(Connection *conn, OpenedFolder *openFolder, char *requestString)
{
    char *DoSpace = "";
    long option;

    if (ConnWriteF(conn, "* STATUS \"%s\" (", openFolder->info->name.utf7) != -1) {
        if (*requestString) {
            for (;;) {
                if ((option = BongoKeywordBegins(Imap.command.status.optionIndex, requestString)) != -1) {
                    if ((ConnWriteF(conn, "%s%s %lu", DoSpace, StatusCommandOptions[option].string, StatusCommandOptions[option].function(openFolder))) != -1) {
                        requestString += StatusCommandOptions[option].stringLen;
                        if (*requestString == ' ') {
                            requestString++;
                            DoSpace = " ";
                            continue;
                        }
                        break;
                    }
                    return(STATUS_ABORT);
                }

                if (ConnWrite(conn, "\r\n", sizeof("\r\n") - 1) != -1) {
                    return(STATUS_INVALID_ARGUMENT);
                }
                return(STATUS_ABORT);
            }
        }
        
        if (ConnWrite(conn, ")\r\n", 3) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}

int
ImapCommandStatus(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    char *ptr;
    unsigned char *ptr2;
    unsigned char *items;
    OpenedFolder openFolder;
    FolderInformation *folder;
    FolderPath folderPath;

    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            if ((ccode = GetPathArgument(session, session->command.buffer + strlen("STATUS"), &ptr, &folderPath, FALSE)) == STATUS_CONTINUE) {
                ptr2 = (unsigned char *)ptr;
                if ((ccode = GrabArgument(session, &ptr2, &items)) == STATUS_CONTINUE) {
                    if ((ccode = FolderListLoad(session)) == STATUS_CONTINUE) {
                        if ((ccode = FolderGetByName(session, folderPath.name, &folder)) == STATUS_CONTINUE) {
                            memset(&openFolder, 0, sizeof(OpenedFolder));
                            if ((ccode = FolderOpen(session->store.conn, &openFolder, folder, TRUE)) == STATUS_CONTINUE) {
                                ccode = SendStatusResponse(session->client.conn, &openFolder, items);
                            }
                        }
                    }
                    MemFree(items);
                }
                FreePathArgument(&folderPath);
            }
        }
    }
    if (ccode == STATUS_CONTINUE) {
        return(SendOk(session, "STATUS"));
    }
    return(SendError(session->client.conn, session->command.tag, "STATUS", ccode));
}

__inline static long
GetMonthSequenceNum(char *monthString, unsigned long *monthNum)
{
    unsigned long i;

    for (i = 0; i < 12; i++) {
        if (XplStrCaseCmp(monthString, Imap.command.months[i]) == 0) {
            *monthNum = i + 1;
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_INVALID_ARGUMENT);
}

/* parses dd-mmm-yyyy (example 23-Jul-2006) */
__inline static long
ParseDate(char *dateString, unsigned long *day, unsigned long *month, unsigned long *year)
{
    *(dateString + 2) = '\0';
    *(dateString + 6) = '\0';
    *(dateString + 11) = '\0';

    *day = atol(dateString);
    *year = atol(dateString + 7);
    return(GetMonthSequenceNum(dateString + 3, month));
}

/* parses hh:mm:ss (example 14:02:23) */
__inline static void
ParseTime(char *timeString, unsigned long *hours, unsigned long *minutes, unsigned long *seconds)
{
    *(timeString + 2) = '\0';
    *(timeString + 5) = '\0';
    *(timeString + 8) = '\0';

    *hours = atol(timeString);
    *minutes = atol(timeString + 3);
    *seconds = atol(timeString + 6);
}

/* parses ("+" / "-")hhmm (example -0730) */
__inline static void
ParseOffset(char *offsetString, long *offset)
{
    long hours;
    long minutes;

    *(offsetString + 5) = '\0';
    minutes = atol(offsetString + 3);
    /* the next line destroys a significant character, */
    /* but we already captured its meaning above */
    *(offsetString + 3) = '\0';
    hours = atol(offsetString);

    /* Make the sign of minutes match the sign of hours */
    if (hours < 0 ) {
        minutes = 0 - minutes;
    }

    *offset = ((hours * 3600) + (minutes * 60));
}

__inline static long
ParseDateTime(char *dateString, long dateStringLen, time_t *date)
{
    unsigned long day;
    unsigned long month;
    unsigned long year;
    unsigned long hour;
    unsigned long min;
    unsigned long sec;
    long offset;

    if (dateStringLen == 26) {  /* proper date-time strings are 26 characters long */
        if (ParseDate(dateString, &day, &month, &year) ==  STATUS_CONTINUE) {
            ParseTime(dateString + 12, &hour, &min, &sec);
            ParseOffset(dateString + 21, &offset);
            *date = MsgGetUTC(day, month, year, hour, min, sec);
            (*date) -= offset;
            return(STATUS_CONTINUE);                    
        }
    }

    *date = 0;
    return(STATUS_INVALID_ARGUMENT);
}

__inline static long
ParseAppendArgurment(ImapSession *session, FolderPath *path, unsigned long *messageSize, unsigned long *flags, time_t *date)
{
    long ccode;
    char *ptr;
    char *sizeArg;
    char *flagArg;
    char *dateArg;
    long tmpSize;

    if ((ccode = GetPathArgument(session, session->command.buffer + strlen("APPEND"), &ptr, path, FALSE)) == STATUS_CONTINUE) {
        ccode = STATUS_INVALID_ARGUMENT;
        if ((sizeArg = strchr(ptr, '{')) != NULL) {
            *sizeArg = '\0';
            sizeArg++;
            tmpSize = atol(sizeArg);
            if (tmpSize > 0) {
                *messageSize = (unsigned long)tmpSize;
                *flags = 0;
                *date = 0;

                for (;;) {
                    ptr = FindNextArgument(ptr);
                    if (!ptr) {
                        return(STATUS_CONTINUE);
                    }

                    if (*ptr == '(') {
                        ptr++;
                        flagArg = ptr;
                        ptr = strchr(ptr, ')');
                        if (ptr) {
                            *ptr = '\0';
                            ccode = ParseStoreFlags(flagArg, flags);
                            if (ccode == STATUS_CONTINUE) {
                                ptr++;
                                continue;
                            }
                        }
                    }

                    if (*ptr == '"') {
                        ptr++;
                        dateArg = ptr;
                        ptr = strchr(ptr, '"');
                        if (ptr) {
                            *ptr = '\0';
                            ccode = ParseDateTime(dateArg, ptr - dateArg, date);
                            if (ccode == STATUS_CONTINUE) {
                                ptr++;
                                continue;
                            }
                        }
                    }

                    if (ccode == STATUS_CONTINUE) {
                        ccode = STATUS_INVALID_ARGUMENT; 
                    }
                    
                    break;
                }
            }
        }
    }
    return(ccode);
}

__inline static long
WriteMessageInMailbox(Connection *clientConn, Connection *storeConn, uint64_t folderGuid, unsigned long size, uint64_t *messageGuid, time_t creationTime)
{
    long ccode;
    long bytesRead;
    char buffer[100];

    ccode = STATUS_NMAP_COMM_ERROR;
    if (NMAPSendCommandF(storeConn, "WRITE %llx %u %lu T%lu\r\n", folderGuid, STORE_DOCTYPE_MAIL, size, (unsigned long)creationTime) != -1) {
        if ((ccode = CheckForNMAPCommError(NMAPReadResponse(storeConn, NULL, 0, 0))) == 2002) {
            ccode = STATUS_ABORT;
            if (ConnWrite(clientConn, "+ Ready for more data\r\n", sizeof("+ Ready for more data\r\n") - 1) != -1) {
                if (ConnFlush(clientConn) != -1) {
                    if ((bytesRead = ConnReadToConn(clientConn, storeConn, size)) > 0) {
                        if ((unsigned long)bytesRead == size) {
                            if ((ccode = CheckForNMAPCommError(NMAPReadResponse(storeConn, buffer, sizeof(buffer), TRUE))) == 1000) {
                                *messageGuid = HexToUInt64(buffer + 5, NULL);
                                ccode = STATUS_CONTINUE;
                            }
                        }
                    }
                }
            }
        }
    } 
    return(ccode);
}

__inline static long
AppendFlags(Connection *storeConn, uint64_t guid, unsigned long flags, unsigned long *resultFlags)
{
    long ccode;

    if (flags) {
        if ((ccode = StoreMessageFlag(storeConn, guid, "+", flags, resultFlags)) == STATUS_CONTINUE) {
            return(STATUS_CONTINUE);
        }
        return(ccode);
    }
    *resultFlags = 0;
    return(STATUS_CONTINUE);
}


int
ImapCommandAppend(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    FolderInformation *folder;
    FolderPath folderPath;
    unsigned long messageSize;
    unsigned long flags;
    time_t date;
    uint64_t messageGuid = 0;
        
    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            if ((ccode = ParseAppendArgurment(session, &folderPath, &messageSize, &flags, &date)) == STATUS_CONTINUE) {
                if ((ccode = FolderListLoad(session)) == STATUS_CONTINUE) {
                    if ((ccode = FolderGetByName(session, folderPath.name, &folder)) == STATUS_CONTINUE) {
                        if ((ccode = WriteMessageInMailbox(session->client.conn, session->store.conn, folder->guid, messageSize, &messageGuid, date)) == STATUS_CONTINUE) {
                            if ((ccode = ReadCommandLine(session->client.conn, &(session->command.buffer), &(session->command.bufferLen))) == STATUS_CONTINUE) {
                                if ((ccode = AppendFlags(session->store.conn, messageGuid, flags, &flags)) == STATUS_CONTINUE) {
                                    if ((session->client.state == STATE_SELECTED) && (session->folder.selected.info == folder)) {
                                        if ((ccode = MessageListLoad(session->store.conn, &(session->folder.selected))) == STATUS_CONTINUE) {
                                            ccode = SendExistsAndRecent(session->client.conn, session->folder.selected.messageCount, session->folder.selected.recentCount);
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
                FreePathArgument(&folderPath);
            }
        }
    }

    if (ccode == STATUS_CONTINUE) {
        return(SendOk(session, "APPEND"));
    }
    return(SendError(session->client.conn, session->command.tag, "APPEND", ccode));
}

/********** IMAP session commands - selected state **********/

int
ImapCommandCheck(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;

    if ((ccode = CheckState(session, STATE_SELECTED)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            return(SendOk(session, "CHECK"));
        }
    }
    return(SendError(session->client.conn, session->command.tag, "CHECK", ccode));
}

__inline static long
PurgeDeletedMessages(ImapSession *session, BOOL client_response, MessageInformation *message, unsigned long messageCount)
{
    long ccode = 0;
    long count;
    Connection *store = session->store.conn;
    Connection *client = session->client.conn;

    count = messageCount;

    while (count > 0) {
        if (!(message->flags & STORE_MSG_FLAG_DELETED) || (message->flags & STORE_MSG_FLAG_PURGED)) {
            message--;
            count--;
            continue;
        }

        if (NMAPSendCommandF(store, "PURGE %llx\r\n", message->guid) != -1) {
            ccode = NMAPReadResponse(store, NULL, 0, 0);
            if (ccode == 1000) {
                if (client_response) {
                    if (ConnWriteF(client, "* %lu EXPUNGE\r\n", count) != -1) {
                        message--;
                        count--;
                        continue;
                    }
                }
                message--;
                count--;
                continue;
            }
        }
        return(CheckForNMAPCommError(ccode));
    }
    return(STATUS_CONTINUE);
}

int
ImapCommandClose(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;

    StartBusy(session, "* OK - Purging deleted messages");
    if ((ccode = CheckState(session, STATE_SELECTED)) == STATUS_CONTINUE) {
        if (session->folder.selected.readOnly) {
            FolderDeselect(session);
            StopBusy(session);
            return(SendOk(session, "CLOSE"));
        }
        
        ccode = PurgeDeletedMessages(session, FALSE, &(session->folder.selected.message[session->folder.selected.messageCount - 1]), session->folder.selected.messageCount);

        FolderDeselect(session);
        if (ccode == STATUS_CONTINUE) {
            StopBusy(session);
            return(SendOk(session, "CLOSE"));
        }
    }
    StopBusy(session);
    return(SendError(session->client.conn, session->command.tag, "CLOSE", ccode));
}

int
ImapCommandExpunge(void *param)
{
    ImapSession *session = (ImapSession *)param;
    OpenedFolder *selected = &session->folder.selected;
    long ccode;
    
    StartBusy(session, NULL);

    if ((ccode = CheckState(session, STATE_SELECTED)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            ccode = STATUS_READ_ONLY_FOLDER;
            if (!(selected->readOnly)) {
                if ((ccode = PurgeDeletedMessages(session, TRUE, &(selected->message[selected->messageCount - 1]), selected->messageCount)) == STATUS_CONTINUE) {
                    if ((ccode = MessageListLoad(session->store.conn, selected)) == STATUS_CONTINUE) {
                        StopBusy(session);
                        return(SendOk(session, "EXPUNGE"));
                    }
                }
            }
        }
    }
    StopBusy(session);
    return(SendError(session->client.conn, session->command.tag, "EXPUNGE", ccode));
}


/********** IMAP ACL commands rfc2086 **********/

int
ImapCommandSetAcl(void *param)
{
#if defined(HAVE_SHARED_FOLDERS)
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *ptr;
    unsigned char Answer[1024];
    unsigned long permissions;
    unsigned long newPermissions;
    unsigned long action;
    unsigned char *ptr2;
    unsigned char *mbox;
    unsigned char *mailbox;
    unsigned char *identifier;
    BOOL    ready;

    if (Imap.command.capability.acl.enabled) {
        if (session->client.state == STATE_SELECTED) {
            if (((ccode = PurgeMessageNotify(session)) == STATUS_CONTINUE) && ((ccode = NewMessageNotify(session)) == STATUS_CONTINUE)) {
                ;    
            } else {
                return(SendError(session->client.conn, session->command.tag, "SETACL", ccode));
            }
        } else if (session->client.state < STATE_AUTH) { 
            return(SendError(session->client.conn, session->command.tag, "SETACL", STATUS_INVALID_STATE));
        }

        ptr = session->command.buffer + 6;
        if (IsWhiteSpace(*ptr)) {
            do { ptr++; } while (IsWhiteSpace(*ptr));
        } else {
            return(SendError(session->client.conn, session->command.tag, "SETACL", STATUS_INVALID_ARGUMENT));
        }

        /* Grab the mailbox name. */
        ccode = GrabArgument(session, &ptr, &mbox);
        if (ccode == STATUS_CONTINUE) {
            /* Grab the authentication identifier */
            ccode = GrabArgument(session, &ptr, &identifier);
            if (ccode == STATUS_CONTINUE) {
                /* Advance as needed. */
                if (IsWhiteSpace(*ptr)) {
                    do { ptr++; } while (IsWhiteSpace(*ptr));
                }

                switch (*ptr) {
                case '\0': { action = 0;         break; }
                case '-': {  action = -1; ptr++;     break; }
                case '+': {  action = 1;  ptr++;     break; }
                default: {  action = 0;         break; }
                }

                /* Advance as needed. */
                if (IsWhiteSpace(*ptr)) {
                    do { ptr++; } while (IsWhiteSpace(*ptr));
                }

                ready = TRUE;
                newPermissions = 0;
                while ((ready == TRUE) && (*ptr != '\0')) {
                    switch(toupper(*ptr)) {
                    case 'A': { /* Administer (perform SETACL) */
                        newPermissions |= NMAP_SHARE_ADMINISTER; break; }

                    case 'C': { /* Create (CREATE new sub-mailboxes in any implementation-defined hierarchy. */
                        newPermissions |= NMAP_SHARE_CREATE;  break; }

                    case 'D': { /* Delete (STORE DELETED flag, perform EXPUNGE). */
                        newPermissions |= NMAP_SHARE_DELETE;  break; }

                    case 'I': { /* Insert (perform APPEND, COPY into mailbox). */
                        newPermissions |= NMAP_SHARE_INSERT;  break; }

                    case 'L': { /* Lookup (mailbox is visible to LIST/LSUB commands). */
                        newPermissions |= NMAP_SHARE_VISIBLE;  break; }

                    case 'P': { /* Post (send mail to submission address for mailbox, not enforced by IMAP4 itself. */
                        newPermissions |= NMAP_SHARE_POST;   break; }

                    case 'R': { /* Read (SELECT the mailbox, perform CHECK, FETCH, PARTIAL, SEARCH, COPY from mailbox. */
                        newPermissions |= NMAP_SHARE_READ;   break; }

                    case 'S': { /* Keep seen/unseen information across sessions (STORE SEEN flag) */
                        newPermissions |= NMAP_SHARE_SEEN;   break; }

                    case 'W': { /* Write (STORE flags other than SEEN and DELETED). */
                        newPermissions |= NMAP_SHARE_WRITE;   break; }

                    default: {
                        MemFree(mbox);
                        MemFree(identifier);

                        ready = FALSE;
                        break;
                    }
                    }

                    do { ptr++; } while(IsWhiteSpace(*ptr));
                }

                if (ready == TRUE) {
                    mailbox = mbox;
                    if ((*mailbox == '/') || (*mailbox == '\\')) {
                        mailbox++;
                    }

                    CheckPathLen(mailbox);
                } else {
                    /* Invalid permissions! */
                    return(SendError(session->client.conn, session->command.tag, "SETACL", STATUS_PERMISSION_DENIED));
                }

                if (action != 0) {
                    permissions = 0;

                    /* Load the NMAP permissions for this resource. */
                    if (NMAPSendCommand(session->store.conn, "SHARE MBOX\r\n", 12) == 12) {
                        ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);

                        if (ccode == 2002) {
                            ready = FALSE;
                            while ((ccode = NMAPReadResponse(session->store.conn, Answer, sizeof(Answer), TRUE)) == 2002) {
                                if (ready == FALSE) {
                                    ptr = strchr(Answer, ' ');
                                    if (ptr != NULL) {
                                        *(ptr++) = '\0';

                                        if (XplStrCaseCmp(mailbox, Answer) != 0) {
                                            continue;
                                        }
                                    } else {
                                        continue;
                                    }

                                    ptr2 = strchr(ptr, ' ');
                                    if (ptr2 != NULL) {
                                        *(ptr2++) = '\0';           

                                        if (XplStrCaseCmp(identifier, ptr) != 0) {
                                            continue;
                                        }
                                    } else {
                                        continue;
                                    }

                                    permissions = atol(ptr2);
                                    ready = TRUE;

                                    /* Keep looping to absorb the remaining NMAP Server responses. */
                                }
                            }

                            if (ccode == 1000) {
                                ;
                            } else {
                                MemFree(mbox);
                                MemFree(identifier);
                                return(SendError(session->client.conn, session->command.tag, "SETACL", CheckForNMAPCommError(ccode)));
                            }
                        } else {
                            MemFree(mbox);
                            MemFree(identifier);
                            return(SendError(session->client.conn, session->command.tag, "SETACL", CheckForNMAPCommError(ccode)));
                        }

                        if (action == 1) {
                            ccode = NMAPSendCommandF(session->store.conn, "SHARE MBOX %s %s %lu%s\r\n", mailbox, identifier, permissions | newPermissions, (ready == TRUE)? " NOMESSAGE": "");
                        } else {
                            ccode = NMAPSendCommandF(session->store.conn, "SHARE MBOX %s %s %lu%s\r\n", mailbox, identifier, permissions & ~newPermissions, (ready == TRUE)? " NOMESSAGE": "");
                        }

                        if (ccode != -1) {
                            ;
                        } else {
                            MemFree(mbox);
                            MemFree(identifier);
                            return(SendError(session->client.conn, session->command.tag, "SETACL", STATUS_NMAP_COMM_ERROR));
                        }
                    } else {
                        MemFree(mbox);
                        MemFree(identifier);
                        return(SendError(session->client.conn, session->command.tag, "SETACL", STATUS_NMAP_COMM_ERROR));
                    }
                } else {
                    /* Override existing permissions. */
                    if (NMAPSendCommandF(session->store.conn, "SHARE MBOX %s %s %lu NOMESSAGE\r\n", mailbox, identifier, newPermissions) != -1) {
                        ;
                    } else {
                        MemFree(mbox);
                        MemFree(identifier);
                        return(SendError(session->client.conn, session->command.tag, "SETACL", STATUS_NMAP_COMM_ERROR));
                    }
                }

                MemFree(mbox);
                MemFree(identifier);

                ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
                if (ccode == 1000) {
                    return(SendOk(session, "SETACL"));
                }
                return(SendError(session->client.conn, session->command.tag, "SETACL", CheckForNMAPCommError(ccode)));
            }
        }
        return(SendError(session->client.conn, session->command.tag, "SETACL", ccode));
    }
    return(SendError(session->client.conn, session->command.tag, "SETACL", STATUS_FEATURE_DISABLED));
#else
    return(SendError(((ImapSession *)param)->client.conn, ((ImapSession *)param)->command.tag, "SETACL", STATUS_UNKNOWN_COMMAND));
#endif
}

int
ImapCommandDeleteAcl(void *param)
{
#if defined(HAVE_SHARED_FOLDERS)
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *ptr;
    unsigned char *mbox;
    unsigned char *mailbox;
    unsigned char *identifier;

    if (Imap.command.capability.acl.enabled) {
        if (session->client.state == STATE_SELECTED) {
            if (((ccode = PurgeMessageNotify(session)) == STATUS_CONTINUE) && ((ccode = NewMessageNotify(session)) == STATUS_CONTINUE)) {
                ;    
            } else {
                return(SendError(session->client.conn, session->command.tag, "DELETEACL", ccode));
            }
        } else if (session->client.state < STATE_AUTH) {
            return(SendError(session->client.conn, session->command.tag, "DELETEACL", STATUS_INVALID_STATE));
        }

        ptr = session->command.buffer + 9;
        if (IsWhiteSpace(*ptr)) {
            do { ptr++; } while (IsWhiteSpace(*ptr));
        } else {
            return(SendError(session->client.conn, session->command.tag, "DELETEACL", STATUS_INVALID_ARGUMENT));
        }

        /* Grab the mailbox name. */
        ccode = GrabArgument(session, &ptr, &mbox);
        if (ccode == STATUS_CONTINUE) {
            /* Grab the authentication identifier */
            ccode = GrabArgument(session, &ptr, &identifier);
            if (ccode == STATUS_CONTINUE) {
                mailbox = mbox;
                if ((*mailbox == '/') || (*mailbox == '\\')) {
                    mailbox++;
                }

                CheckPathLen(mailbox);

                if (NMAPSendCommandF(session->store.conn, "SHARE MBOX %s %s 0 NOMESSAGE\r\n", mailbox, identifier) != -1) {
                    MemFree(mbox);
                    MemFree(identifier);
    
                    ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
                    if (ccode == 1000) {
                        return(SendOk(session, "DELETEACL"));
                    }
                    return(SendError(session->client.conn, session->command.tag, "DELETEACL", CheckForNMAPCommError(ccode)));
                }
                MemFree(mbox);
                MemFree(identifier);
                return(SendError(session->client.conn, session->command.tag, "DELETEACL", STATUS_NMAP_COMM_ERROR));
            }
        }
        return(SendError(session->client.conn, session->command.tag, "DELETEACL", ccode));
    }
    return(SendError(session->client.conn, session->command.tag, "DELETEACL", STATUS_FEATURE_DISABLED));
#else
    return(SendError(((ImapSession *)param)->client.conn, ((ImapSession *)param)->command.tag, "SETACL", STATUS_UNKNOWN_COMMAND));
#endif
}

int
ImapCommandGetAcl(void *param)
{
#if defined(HAVE_SHARED_FOLDERS)
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *ptr;
    unsigned char Answer[1024];
    unsigned char Reply[1024];
    unsigned long permissions;
    unsigned char *ptr2;
    unsigned char *mbox;
    unsigned char *mailbox;

    if (Imap.command.capability.acl.enabled) {
        if (session->client.state == STATE_SELECTED) {
            if (((ccode = PurgeMessageNotify(session)) == STATUS_CONTINUE) && ((ccode = NewMessageNotify(session)) == STATUS_CONTINUE)) {
                ;    
            } else {
                return(SendError(session->client.conn, session->command.tag, "GETACL", ccode));
            }
        } else if (session->client.state < STATE_AUTH) {
            return(SendError(session->client.conn, session->command.tag, "GETACL", STATUS_INVALID_STATE));
        }

        ptr = session->command.buffer + 6;
        if (IsWhiteSpace(*ptr)) {
            do { ptr++; } while (IsWhiteSpace(*ptr));
        } else {
            return(SendError(session->client.conn, session->command.tag, "GETACL", STATUS_INVALID_ARGUMENT));
        }

        /* Grab the mailbox name. */
        ccode = GrabArgument(session, &ptr, &mbox);
        if (ccode == STATUS_CONTINUE) {
            mailbox = mbox;
            if ((*mailbox == '/') || (*mailbox == '\\')) {
                mailbox++;
            }

            ptr2 = Reply + snprintf(Reply, sizeof(Reply), "* ACL \"%s\"", mailbox);

            CheckPathLen(mailbox);

            if (NMAPSendCommandF(session->store.conn, "RIGHTS %s\r\n", mailbox) != -1) {
                MemFree(mbox);
                ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
                if (ccode == 2002) {
                    while ((ccode = NMAPReadResponse(session->store.conn, Answer, sizeof(Answer), TRUE)) == 2002) {
                        ptr = strchr(Answer, ' ');
                        if (ptr != NULL) {
                            *(ptr++) = '\0';

                            permissions = atol(ptr);

                            /* Add the grantee name to the response. */
                            ptr2 += snprintf(ptr2, sizeof(Reply) - (ptr2 - Reply), " \"%s\" ", Answer);
                            if (permissions) {
                                if (permissions & NMAP_SHARE_VISIBLE) {
                                    *(ptr2++) = 'l';
                                }

                                if (permissions & NMAP_SHARE_READ) {
                                    *(ptr2++) = 'r';
                                }

                                if (permissions & NMAP_SHARE_SEEN) {
                                    *(ptr2++) = 's';
                                }

                                if (permissions & NMAP_SHARE_WRITE) {
                                    *(ptr2++) = 'w';
                                }

                                if (permissions & NMAP_SHARE_INSERT) {
                                    *(ptr2++) = 'i';
                                }

                                if (permissions & NMAP_SHARE_POST) {
                                    *(ptr2++) = 'p';
                                }

                                if (permissions & NMAP_SHARE_CREATE) {
                                    *(ptr2++) = 'c';
                                }

                                if (permissions & NMAP_SHARE_DELETE) {
                                    *(ptr2++) = 'd';
                                }

                                if (permissions & NMAP_SHARE_ADMINISTER) {
                                    *(ptr2++) = 'a';
                                }

                                continue;
                            } else {
                                *(ptr2++) = '"';
                                *(ptr2++) = '"';
                            }
                        }

                        continue;
                    }

                    if (ccode == 1000) {
                        *(ptr2++) = '\r';
                        *(ptr2++) = '\n';
                        *(ptr2) = '\0';

                        if (ConnWrite(session->client.conn, Reply, ptr2 - Reply) != -1) {
                            return(SendOk(session, "GETACL"));
                        }
                        return(STATUS_ABORT);
                    }
                }
                return(SendError(session->client.conn, session->command.tag, "GETACL", CheckForNMAPCommError(ccode)));
            }
            MemFree(mbox);
            return(SendError(session->client.conn, session->command.tag, "GETACL", STATUS_NMAP_COMM_ERROR));
        }
        return(SendError(session->client.conn, session->command.tag, "GETACL", ccode));
    }
    return(SendError(session->client.conn, session->command.tag, "GETACL", STATUS_FEATURE_DISABLED));
#else
    return(SendError(((ImapSession *)param)->client.conn, ((ImapSession *)param)->command.tag, "SETACL", STATUS_UNKNOWN_COMMAND));
#endif
}

int
ImapCommandListRights(void *param)
{
#if defined(HAVE_SHARED_FOLDERS)
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *mbox;
    unsigned char *identifier;
    unsigned char *ptr;

    if (Imap.command.capability.acl.enabled) {
        if (session->client.state == STATE_SELECTED) {
            if (((ccode = PurgeMessageNotify(session)) == STATUS_CONTINUE) && ((ccode = NewMessageNotify(session)) == STATUS_CONTINUE)) {
                ;    
            } else {
                return(SendError(session->client.conn, session->command.tag, "LISTRIGHTS", ccode));
            }
        }

        if (session->client.state >= STATE_AUTH) {
            ptr = session->command.buffer + 10;
            if (IsWhiteSpace(*ptr)) {
                do { ptr++; } while (IsWhiteSpace(*ptr));
            } else {
                return(SendError(session->client.conn, session->command.tag, "LISTRIGHTS", STATUS_INVALID_ARGUMENT));
            }
        } else {
            return(SendError(session->client.conn, session->command.tag, "LISTRIGHTS", STATUS_INVALID_STATE));
        }

        /* Grab the mailbox name. */
        ccode = GrabArgument(session, &ptr, &mbox);
        if (ccode == STATUS_CONTINUE) {
            identifier = NULL;

            /* Grab the authentication identifier */
            ccode = GrabArgument(session, &ptr, &identifier);
            if (ccode == STATUS_CONTINUE) {
                MemFree(mbox);
                MemFree(identifier);
                if (ConnWriteF(session->client.conn, "* LISTRIGHTS \"%s\" %s \"\" l r s w i p c d a\r\n", mbox, identifier) != -1) {
                    return(SendOk(session, "LISTRIGHTS"));
                }
                return(STATUS_ABORT);
            }
            MemFree(mbox);
        }
        return(SendError(session->client.conn, session->command.tag, "LISTRIGHTS", ccode));
    }
    return(SendError(session->client.conn, session->command.tag, "LISTRIGHTS", STATUS_FEATURE_DISABLED));
#else
    return(SendError(((ImapSession *)param)->client.conn, ((ImapSession *)param)->command.tag, "SETACL", STATUS_UNKNOWN_COMMAND));
#endif
}

int
ImapCommandMyRights(void *param)
{
#if defined(HAVE_SHARED_FOLDERS)
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *ptr;
    unsigned char Answer[1024];
    unsigned char Reply[1024];
    unsigned long permissions;
    unsigned char *ptr2;
    unsigned char *mbox;
    unsigned char *mailbox;
    BOOL found;

    if (Imap.command.capability.acl.enabled) {
        if (session->client.state == STATE_SELECTED) {
            if (((ccode = PurgeMessageNotify(session)) == STATUS_CONTINUE) && ((ccode = NewMessageNotify(session)) == STATUS_CONTINUE)) {
                ;    
            } else {
                return(SendError(session->client.conn, session->command.tag, "MYRIGHTS", ccode));
            }
        } else if (session->client.state < STATE_AUTH) {
            return(SendError(session->client.conn, session->command.tag, "MYRIGHTS", STATUS_INVALID_STATE));
        }

        ptr = session->command.buffer + 8;
        if (IsWhiteSpace(*ptr)) {
            do { ptr++; } while (IsWhiteSpace(*ptr));
        } else {
            return(SendError(session->client.conn, session->command.tag, "MYRIGHTS", STATUS_INVALID_ARGUMENT));
        }

        /* Grab the mailbox name. */
        ccode = GrabArgument(session, &ptr, &mbox);
        if (ccode == STATUS_CONTINUE) {
            mailbox = mbox;
            if ((*mailbox == '/') || (*mailbox == '\\')) {
                mailbox++;
            }

            ptr2 = Reply + snprintf(Reply, sizeof(Answer), "* MYRIGHTS \"%s\" \"\"", mailbox);

            CheckPathLen(mailbox);

            if (NMAPSendCommandF(session->store.conn, "RIGHTS %s\r\n", mailbox) != -1) {
                MemFree(mbox);
                ccode = NMAPReadResponse(session->store.conn, NULL, 0, 0);
                if (ccode == 2002) {
                    found = FALSE;
                    while ((ccode = NMAPReadResponse(session->store.conn, Answer, sizeof(Answer), TRUE)) == 2002) {
                        ptr = strchr(Answer, ' ');
                        if (ptr != NULL) {
                            *(ptr++) = '\0';

                            /* We are only interested in this user's permissions.
                               Keep looping to absorb all NMAP responses. */
                            if (XplStrCaseCmp(session->user.name, Answer) == 0) {
                                permissions = atol(ptr);
                                if (permissions) {
                                    ptr2 -= 2; /* Back over the default empty permissions. */

                                    if (permissions & NMAP_SHARE_VISIBLE) {
                                        *(ptr2++) = 'l';
                                    }

                                    if (permissions & NMAP_SHARE_READ) {
                                        *(ptr2++) = 'r';
                                    }

                                    if (permissions & NMAP_SHARE_SEEN) {
                                        *(ptr2++) = 's';
                                    }

                                    if (permissions & NMAP_SHARE_WRITE) {
                                        *(ptr2++) = 'w';
                                    }

                                    if (permissions & NMAP_SHARE_INSERT) {
                                        *(ptr2++) = 'i';
                                    }

                                    if (permissions & NMAP_SHARE_POST) {
                                        *(ptr2++) = 'p';
                                    }

                                    if (permissions & NMAP_SHARE_CREATE) {
                                        *(ptr2++) = 'c';
                                    }

                                    if (permissions & NMAP_SHARE_DELETE) {
                                        *(ptr2++) = 'd';
                                    }

                                    if (permissions & NMAP_SHARE_ADMINISTER) {
                                        *(ptr2++) = 'a';
                                    }

                                    continue;
                                }
                            }

                            continue;
                        }
                    }

                    if (ccode == 1000) {
                        *(ptr2++) = '\r';
                        *(ptr2++) = '\n';
                        *(ptr2) = '\0';
                        if (ConnWrite(session->client.conn, Reply, ptr2 - Reply) != -1) {
                            return(SendOk(session, "MYRIGHTS"));
                        }
                        return(STATUS_ABORT);
                    }
                }
                return(SendError(session->client.conn, session->command.tag, "MYRIGHTS", CheckForNMAPCommError(ccode)));
            }
            MemFree(mbox);
            return(SendError(session->client.conn, session->command.tag, "MYRIGHTS", STATUS_NMAP_COMM_ERROR));
        }
        return(SendError(session->client.conn, session->command.tag, "MYRIGHTS", ccode));
    }
    return(SendError(session->client.conn, session->command.tag, "MYRIGHTS", STATUS_FEATURE_DISABLED));
#else
    return(SendError(((ImapSession *)param)->client.conn, ((ImapSession *)param)->command.tag, "SETACL", STATUS_UNKNOWN_COMMAND));
#endif

}

/********** IMAP Quota commands rfc2087 **********/

int
ImapCommandSetQuota(void *param)
{
    long ccode;
    ImapSession *session = (ImapSession *)param;

    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            return(SendError(session->client.conn, session->command.tag, "SETQUOTA", STATUS_PERMISSION_DENIED));
        }
    }
    return(SendError(session->client.conn, session->command.tag, "SETQUOTA", ccode));
}

int
ImapCommandGetQuota(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *ptr;
    unsigned char Reply[1024];
    unsigned long quota = 0;
    unsigned long used = 0;

    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            if (NMAPSendCommandF(session->store.conn, "SPACE\r\n") != -1) {
                ccode = NMAPReadResponse(session->store.conn, Reply, sizeof(Reply), TRUE);
                if(ccode != 1000) {
                    ccode = CheckForNMAPCommError(ccode);
                    if (ccode != STATUS_NMAP_COMM_ERROR) {
                        return(SendError(session->client.conn, session->command.tag, "GETQUOTA", STATUS_COULD_NOT_DETERMINE_QUOTA));
                    }
                    return(SendError(session->client.conn, session->command.tag, "GETQUOTA", STATUS_NMAP_COMM_ERROR));
                }

                ptr = strchr(Reply, ' ');
                if(!ptr) {
                    return(SendError(session->client.conn, session->command.tag, "GETQUOTA", STATUS_COULD_NOT_DETERMINE_QUOTA));
                }
 
                quota = atol(ptr);
                used = atol(Reply);

                if(session->command.buffer[9] == '\"' && session->command.buffer[10] == '\"') {
                    if (ConnWriteF(session->client.conn, "* QUOTA \"\" (STORAGE %lu %lu)\r\n%s OK GETQUOTA completed\r\n", used, quota, session->command.tag) != -1) {
                        return(STATUS_CONTINUE);
                    }
                    return(STATUS_ABORT);
                }
                return(SendError(session->client.conn, session->command.tag, "GETQUOTA", STATUS_NO_SUCH_QUOTA_ROOT));
            }
            return(SendError(session->client.conn, session->command.tag, "GETQUOTA", STATUS_NMAP_COMM_ERROR));
        }
    }
    return(SendError(session->client.conn, session->command.tag, "GETQUOTA", ccode));

}

int
ImapCommandGetQuotaRoot(void *param)
{
    ImapSession *session = (ImapSession *)param;
    long ccode;
    unsigned char *ptr;
    unsigned char Reply[1024];
    unsigned long quota = 0;
    unsigned long used = 0;
    unsigned char *mbox;

    if ((ccode = CheckState(session, STATE_AUTH)) == STATUS_CONTINUE) {
        if ((ccode = EventsSend(session, STORE_EVENT_ALL)) == STATUS_CONTINUE) {
            if (NMAPSendCommandF(session->store.conn, "SPACE\r\n") != -1) {
                ccode = NMAPReadResponse(session->store.conn, Reply, sizeof(Reply), TRUE);
                if(ccode != 1000) {
                    ccode = CheckForNMAPCommError(ccode);
                    if (ccode != STATUS_NMAP_COMM_ERROR) {
                        return(SendError(session->client.conn, session->command.tag, "GETQUOTA", STATUS_COULD_NOT_DETERMINE_QUOTA));
                    }
                    return(SendError(session->client.conn, session->command.tag, "GETQUOTA", STATUS_NMAP_COMM_ERROR));
                }
                ptr = strchr(Reply, ' ');
                if(!ptr) {
                    return(SendError(session->client.conn, session->command.tag, "GETQUOTAROOT", STATUS_COULD_NOT_DETERMINE_QUOTA));
                }
                quota = atol(ptr);
                used = atol(Reply);


                ptr = session->command.buffer + 13;
                if(*(ptr - 1) != ' ') {
                    return(SendError(session->client.conn, session->command.tag, "GETQUOTAROOT", STATUS_INVALID_ARGUMENT));
                }
                ccode = GrabArgument(session, &ptr, &mbox);
                if (ccode == STATUS_CONTINUE) {
                    ccode = ConnWriteF(session->client.conn, "* QUOTAROOT %s \"\"\r\n* QUOTA \"\" (STORAGE %lu %lu)\r\n%s OK GETQUOTAROOT completed\r\n", mbox, used, quota, session->command.tag);
                    MemFree(mbox);
                    if (ccode != -1) {
                        return(STATUS_CONTINUE);
                    }
                    return(STATUS_ABORT);
                }
                return(SendError(session->client.conn, session->command.tag, "QUOTAROOT", ccode));
            }
            return(SendError(session->client.conn, session->command.tag, "QUOTAROOT", STATUS_NMAP_COMM_ERROR));
        }
    }
    return(SendError(session->client.conn, session->command.tag, "GETQUOTAROOT", ccode));

}

/********** IMAP Namespace command rfc2342 **********/

int
ImapCommandNameSpace(void *param)
{
    ImapSession *session = (ImapSession *)param;

    if (session->client.state < STATE_AUTH) {
        return(SendError(session->client.conn, session->command.tag, "NAMESPACE", STATUS_INVALID_STATE));
    }

    if (ConnWrite(session->client.conn, 
                  "* NAMESPACE ((\"\" \"/\")) NIL NIL\r\n",
                  strlen("* NAMESPACE ((\"\" \"/\")) NIL NIL\r\n")) != -1) {
        return(SendOk(session, "NAMESPACE"));
    }
    return(STATUS_ABORT);
}

static BOOL
HandleConnection(void *param)
{
    long ccode;
    long commandId;
    ImapSession  *session=(ImapSession *)param;
    unsigned char *ptr;
    ProtocolCommand *localCommand;

    session->progress = NULL;
    if (ConnNegotiate(session->client.conn, Imap.server.ssl.context)) {
        if (session->client.conn->ssl.enable == FALSE) {
            Log(LOG_INFO, "Non-SSL connection from %s", LOGIP(session->client.conn->socketAddress));
        } else {
            Log(LOG_INFO, "SSL connection from %s", LOGIP(session->client.conn->socketAddress));
        }

        if ((ConnWriteF(session->client.conn, "* OK %s %s server ready <%ud.%lu@%s>\r\n",BongoGlobals.hostname, PRODUCT_NAME, (unsigned int)XplGetThreadID(),time(NULL),BongoGlobals.hostname) != -1) && (ConnFlush(session->client.conn) != -1)) {
            while (TRUE) {
                ccode = ReadCommandLine(session->client.conn, &(session->command.buffer), &(session->command.bufferLen));
                if (ccode == STATUS_CONTINUE) {
                    /* Grab ident from command */
                    ptr = strchr(session->command.buffer,' ');
                    if (ptr) {
                        if ((unsigned long)(ptr - (unsigned char *)session->command.buffer) < sizeof(session->command.tag)) {
                            ptr[0]='\0';
                            memcpy(session->command.tag, session->command.buffer, ptr - (unsigned char *)session->command.buffer + 1);
                        } else {
                            memcpy(session->command.tag, session->command.buffer, sizeof(session->command.tag));
                            session->command.tag[sizeof(session->command.tag)-1]='\0';
                        }
                        memmove(session->command.buffer, ptr + 1, strlen(ptr + 1) + 1);
                        commandId = BongoKeywordBegins(Imap.command.index, session->command.buffer);
                        if (commandId != -1) {
                            localCommand = &ImapProtocolCommands[commandId];
                            ccode = localCommand->handler(session);
                        } else {
                            ccode = ImapCommandUnknown(session);
                        }

                        if (ccode != STATUS_ABORT) {
                            /* Post-Command Processing */
                            if (session->client.state == STATE_SELECTED) {
                                SendUnseenFlags(session);
                            }
                            ConnFlush(session->client.conn);
                            continue;
                        }

                        break;
                    }

                    if ((ConnWrite(session->client.conn, "* BAD Missing command\r\n", 23) != -1) && (ConnFlush(session->client.conn) != -1)) {
                        continue;
                    }
                    break;
                }

                if ((ccode != STATUS_ABORT) && (ConnWrite(session->client.conn, "* BAD command line too long\r\n", 29) != -1) && (ConnFlush(session->client.conn) != -1)) {
                    continue;
                }
                break;
            }
        }
    }

    SessionCleanup(session);
    return(FALSE);
}



#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int 
_NonAppCheckUnload(void)
{
    int   s;
    static BOOL checked = FALSE;
    XplThreadID id;

    if (!checked) {
        checked = Imap.exiting = TRUE;

        XplWaitOnLocalSemaphore(Imap.server.shutdownSemaphore);

        id = XplSetThreadGroupID(Imap.server.threadGroupId);
        if (Imap.server.conn->socket != -1) {
            s = Imap.server.conn->socket;
            Imap.server.conn->socket = -1;

            IPclose(s);
        }
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(Imap.server.mainSemaphore);
    }

    return(0);
}
#endif

static void 
IMAPShutdownSigHandler(int sigtype)
{
    static BOOL signaled = FALSE;

    if (!signaled && (sigtype == SIGTERM || sigtype == SIGINT)) {
        signaled = Imap.exiting = TRUE;

        if (Imap.server.conn) {
            ConnClose(Imap.server.conn);
            Imap.server.conn = NULL;
        }

        XplSignalLocalSemaphore(Imap.server.shutdownSemaphore);

        /* Allow the main thread to close the final semaphores and exit. */
        XplDelay(1000);
    }

    return;
}

static int
ServerSocketInit(void)
{
    Imap.server.conn = ConnAlloc(FALSE);
    if (!Imap.server.conn) {
        XplConsolePrintf("bongoimap: Could not allocate connection.\n");
        return -1;
    }

    Imap.server.conn->socketAddress.sin_family = AF_INET;
    Imap.server.conn->socketAddress.sin_port = htons(Imap.server.port);
    Imap.server.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

    /* Get root privs back for the bind.  It's ok if this fails - 
     * the user might not need to be root to bind to the port */
    XplSetEffectiveUserId(0);
    
    Imap.server.conn->socket = ConnServerSocket(Imap.server.conn, 2048);

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user '%s'", MsgGetUnprivilegedUser());
        return -1;
    }

    if (Imap.server.conn->socket < 0) {
        int ret = Imap.server.conn->socket;
        Log(LOG_ERROR, "Could not bind to port %d", Imap.server.port);
        ConnFree(Imap.server.conn);
        return ret;
    }

    return 0;
}

static int
ServerSocketSSLInit(void)
{
    Imap.server.ssl.conn = ConnAlloc(FALSE);
    if (Imap.server.ssl.conn == NULL) {
        Log(LOG_ERROR, "Could not allocate SSL connection");
        ConnFree(Imap.server.conn);
        return -1;
    }

    Imap.server.ssl.conn->socketAddress.sin_family = AF_INET;
    Imap.server.ssl.conn->socketAddress.sin_port = htons(Imap.server.ssl.port);
    Imap.server.ssl.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();
        
    /* Get root privs back for the bind.  It's ok if this fails - 
     * the user might not need to be root to bind to the port */
    XplSetEffectiveUserId(0);
    
    Imap.server.ssl.conn->socket = ConnServerSocket(Imap.server.ssl.conn, 2048);

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user '%s'", MsgGetUnprivilegedUser());
        return -1;
    }
        
    if (Imap.server.ssl.conn->socket < 0) {
        int ret = Imap.server.ssl.conn->socket;

        Log(LOG_ERROR, "Could not bind to SSL port %d", Imap.server.ssl.port);
        ConnFree(Imap.server.conn);
        ConnFree(Imap.server.ssl.conn);
        
        return ret;
    }

    return 0;
}

static BOOL
InitializeImapGlobals()
{
    /*      Imap.session.        */
    XplSafeWrite(Imap.session.threads.inUse, 0);
    XplSafeWrite(Imap.session.threads.idle, 0);
    XplSafeWrite(Imap.session.served, 0);
    XplSafeWrite(Imap.session.badPassword, 0);
    Imap.session.threads.max = 100000;

    /*      Imap.server.        */
    XplSafeWrite(Imap.server.active, 0);
    Imap.server.disabled = FALSE;
    Imap.server.port = 143;
    Imap.server.ssl.port = 993;
    Imap.server.ssl.config.options = 0;
    Imap.server.ssl.config.options |= SSL_ALLOW_CHAIN;
    Imap.server.ssl.config.options |= SSL_ALLOW_SSL3;
    Imap.server.threadGroupId = XplGetThreadGroupID();
    strcpy(Imap.server.postmaster, "admin");

    /*      Imap.command.        */    
    Imap.command.capability.acl.enabled = TRUE;
    /* FIXME: ACL ?? */
    Imap.command.capability.len = sprintf(Imap.command.capability.message, "%s\r\n", "* CAPABILITY IMAP4 IMAP4rev1 AUTH=LOGIN NAMESPACE XSENDER");
    Imap.command.capability.ssl.len = sprintf(Imap.command.capability.ssl.message, "%s\r\n", "* CAPABILITY IMAP4 IMAP4rev1 AUTH=LOGIN NAMESPACE STARTTLS XSENDER LOGINDISABLED");

    Imap.command.months[0] = "Jan";
    Imap.command.months[1] = "Feb";
    Imap.command.months[2] = "Mar";
    Imap.command.months[3] = "Apr";
    Imap.command.months[4] = "May";
    Imap.command.months[5] = "Jun";
    Imap.command.months[6] = "Jul";
    Imap.command.months[7] = "Aug";
    Imap.command.months[8] = "Sep";
    Imap.command.months[9] = "Oct";
    Imap.command.months[10] = "Nov";
    Imap.command.months[11] = "Dec";

    /*      Imap.(misc).        */    
    Imap.exiting = FALSE;
    Imap.logHandle = NULL;

    /* Initialize the Busy List and Semaphore */
    Imap.list_Busy = NULL;
    XplOpenLocalSemaphore(Imap.sem_Busy, 1);

    /* Global allocations */
    BongoKeywordIndexCreateFromTable(Imap.command.index, ImapProtocolCommands, .name, TRUE);
    if (Imap.command.index) {
        Imap.command.returnValueIndex = CreateReturnValueIndex();
        if (Imap.command.returnValueIndex) {
            if (CommandFetchInit()) {
                if (CommandStoreInit()) {
                    if (CommandStatusInit()) {
                        if (CommandSearchInit()) {
                            return(TRUE);
                        }
                        CommandSearchCleanup();
                    }
                    CommandStoreCleanup();            
                }    
                CommandFetchCleanup();
            }
            FreeReturnValueIndex(Imap.command.returnValueIndex);
        }
        BongoKeywordIndexFree(Imap.command.index);    
    }
    return(FALSE);
    
}

static BOOL
FreeImapGlobals()
{
    CommandSearchCleanup();
    CommandStatusCleanup();
    CommandStoreCleanup();
    CommandFetchCleanup();
    FreeReturnValueIndex(Imap.command.returnValueIndex);
    BongoKeywordIndexFree(Imap.command.index);
    return(TRUE);
}

static void 
IMAPServer(void *unused)
{
    int ccode;
    unsigned long j;
    XplThreadID oldTGID;
    Connection *conn;
    ImapSession *session;
    XplThreadID id = 0;

    XplRenameThread(XplGetThreadID(), "IMAP Server");
    XplSafeIncrement(Imap.server.active);

    while (!Imap.exiting) {
        if (ConnAccept(Imap.server.conn, &conn) != -1) {
            if (!Imap.exiting) {
                if (!Imap.server.disabled) {
                    if (XplSafeRead(Imap.session.threads.inUse) < (long)Imap.session.threads.max) {
                        session = ImapSessionGet();
                        if (session) {
                            session->client.conn = conn;
                    
                            XplBeginCountedThread(&id, HandleConnection, IMAP_STACK_SIZE, session, ccode, Imap.session.threads.inUse);
                            if (ccode == 0) {
                                continue;
                            }
                    
                            ImapSessionReturn(session);
                        }
                
                        ConnSend(conn, "* BYE IMAP server out of memory\r\n", strlen("* BYE IMAP server out of memory\r\n"));
                        ConnClose(conn);
                        ConnFree(conn);
                        continue;
                    }

                    ConnSend(conn, "* BYE IMAP connection limit for this server reached\r\n", strlen("* BYE IMAP connection limit for this server reached\r\n"));
                    ConnClose(conn);
                    ConnFree(conn);
                    continue;
                }

                ConnSend(conn, "* BYE IMAP access to this server currently disabled\r\n", strlen("* BYE IMAP access to this server currently disabled\r\n"));
                ConnClose(conn);
                ConnFree(conn);
                continue;
            }
            
            ConnSend(conn, "* BYE IMAP server shutting down\r\n", strlen("* BYE IMAP server shutting down\r\n"));
            ConnClose(conn);
            ConnFree(conn);
            break;
        }

        switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO: 
#endif
            case EINTR: {
                Log(LOG_WARN, "Connection interrupted");
                continue;
            }

            case EIO: {
                Log(LOG_WARN, "Server exiting after an accept() failure with an errno: %d\n", errno);

                break;
            }
        }

        break;
    }

/*    Shutting down    */
    Imap.exiting = TRUE;

    oldTGID = XplSetThreadGroupID(Imap.server.threadGroupId);

    if (Imap.server.conn) {
        ConnClose(Imap.server.conn);
        Imap.server.conn = NULL;
    }

#if VERBOSE
    XplConsolePrintf("IMAPD: Standard listener done.\r\n");
#endif

    if (Imap.server.ssl.conn) {
        ConnClose(Imap.server.ssl.conn);
    }

    /*    Wake up the children and set them free!    */
    /* fixme - SocketShutdown; */

    /*    Wait for the our siblings to leave quietly.    */
    for (j = 0; (XplSafeRead(Imap.server.active) > 1) && (j < 60); j++) {
        XplDelay(1000);
    }

    if (XplSafeRead(Imap.server.active) > 1) {
        XplConsolePrintf("\rIMAPD: %d server threads outstanding; attempting forceful unload.\r\n", XplSafeRead(Imap.server.active) - 1);
    }

#if VERBOSE
    XplConsolePrintf("\rIMAPD: Shutting down %d session threads\r\n", XplSafeRead(Imap.session.threads.inUse));
#endif

    /*    Make sure the kids have flown the coop.    */
    for (j = 0; XplSafeRead(Imap.session.threads.inUse) && (j < 3 * 60); j++) {
        XplDelay(1000);
    }

    if (XplSafeRead(Imap.session.threads.inUse)) {
        XplConsolePrintf("IMAPD: %d threads outstanding; attempting forceful unload.\r\n", XplSafeRead(Imap.session.threads.inUse));
    }

    /* Cleanup SSL */
    if (Imap.server.ssl.context) {
        ConnSSLContextFree(Imap.server.ssl.context);
    }

    LogClose();

    MsgShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();
        
    FreeImapGlobals();
    MemPrivatePoolFree(IMAPConnectionPool);
    MemoryManagerClose(MSGSRV_AGENT_IMAP);

#if VERBOSE
    XplConsolePrintf("IMAPD: Shutdown complete\r\n");
#endif

    XplSignalLocalSemaphore(Imap.server.mainSemaphore);
    XplWaitOnLocalSemaphore(Imap.server.shutdownSemaphore);

    XplCloseLocalSemaphore(Imap.server.shutdownSemaphore);
    XplCloseLocalSemaphore(Imap.server.mainSemaphore);

    XplSetThreadGroupID(oldTGID);

    return;
}

static void
BusyThread(void *unused)
{
    while (!Imap.exiting) {
        DoUpdate();
        XplDelay(10000);
    }
}

static void 
IMAPSSLServer(void *unused)
{
    ImapSession *session;
    unsigned char *message;
    unsigned long messageLen;
    XplThreadID id = 0;
    int ccode;
    Connection *conn;
    
    XplRenameThread(XplGetThreadID(), "IMAP SSL Server");

    while (!Imap.exiting) {
        if (ConnAccept(Imap.server.ssl.conn, &conn) != -1) {
            conn->ssl.enable = TRUE;
            if (!Imap.exiting) {
                if (!Imap.server.disabled) {
                    if (XplSafeRead(Imap.session.threads.inUse) < (long)Imap.session.threads.max) {
                        session = ImapSessionGet();
                        if (session) {
                            session->client.conn = conn;
// Imap.session.threads.inUse
                            XplBeginCountedThread(&id, HandleConnection, IMAP_STACK_SIZE, session, ccode, (Imap.session.threads.inUse));
                            if (ccode == 0) {
                                continue;
                            }

                            ImapSessionReturn(session);
                            message = "* BYE IMAP server unable to create threads\r\n";
                            messageLen = strlen("* BYE IMAP server unable to create threads\r\n");
                        } else {
                            message = "* BYE IMAP server out of memory\r\n";
                            messageLen = strlen("* BYE IMAP server out of memory\r\n");
                        }
                    } else {
                        message = "* BYE IMAP connection limit for this server reached\r\n";
                        messageLen = strlen("* BYE IMAP connection limit for this server reached\r\n");
                    }
                } else {
                    message = "* BYE IMAP access to this server currently disabled\r\n";
                    messageLen = strlen("* BYE IMAP access to this server currently disabled\r\n");
                }

                if (ConnNegotiate(conn, Imap.server.ssl.context)) {
                    if (ConnWrite(conn, message, messageLen) != -1) {
                        ConnFlush(conn);
                    }
                    ConnClose(conn);
                    ConnFree(conn);
                    continue;
                } 

                ConnSend(conn, message, messageLen);
                ConnClose(conn);
                ConnFree(conn);
                continue;
            }

            break;
        }

        if (!Imap.exiting) {
            switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO:
#endif
            case EINTR: {
                Log(LOG_WARN, "accept() interrupted");
                continue;
            }

            case EIO: {
                Imap.exiting = TRUE;
                Log(LOG_WARN, "SSL Server exiting after an accept() failure with an errno: %d\n", errno);
                break;
            }

            default: {
                Log(LOG_WARN, "SSL Server exiting after an accept() failure with an errno: %d\n", errno);
                break;
            }
            }
        }

        break;
    }

#if 0
    XplConsolePrintf("IMAPD: SSL listener done.\r\n");
#endif

    if(Imap.server.ssl.conn) {
        ConnFree(Imap.server.ssl.conn);
        Imap.server.ssl.conn = NULL;
    }

    XplSafeDecrement(Imap.server.active);
    
    if (!Imap.exiting) {
        raise(SIGTERM);
    }
    
    return;
}

static BongoConfigItem IMAPConfig[] = {
        { BONGO_JSON_INT, "o:port/i", &Imap.server.port },
        { BONGO_JSON_INT, "o:port_ssl/i", &Imap.server.ssl.port },
        { BONGO_JSON_INT, "o:threads_max/i", &Imap.session.threads.max },
        { BONGO_JSON_NULL, NULL, NULL }
};

static BOOL
ReadConfiguration(void)
{
    time_t   gm_time_of_day, time_of_day;
    struct tm  gm, ltm;

    tzset();
    time(&time_of_day);
    time(&gm_time_of_day);
    gmtime_r(&gm_time_of_day, &gm);
    localtime_r(&time_of_day, &ltm);
    gm_time_of_day=mktime(&gm);

    // Imap.server.postmaster needs to be set to something?
    if (! ReadBongoConfiguration(IMAPConfig, "imap")) {
        return FALSE;
    }

    if (! ReadBongoConfiguration(GlobalConfig, "global")) {
        return FALSE;
    }

    return(TRUE);
}

XplServiceCode(IMAPShutdownSigHandler)


int
XplServiceMain(int argc, char *argv[])
{
    int   ccode;
    XplThreadID id=0;

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongoimap: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
        return 1;
    }
    XplInit();

    XplSignalHandler(IMAPShutdownSigHandler);

    if (MemoryManagerOpen(MSGSRV_AGENT_IMAP) == TRUE) {
        IMAPConnectionPool = MemPrivatePoolAlloc("IMAP Connections", sizeof(ImapSession), 0, 3072, TRUE, FALSE, IMAPSessionAllocCB, IMAPSessionFreeCB, NULL);
        if (IMAPConnectionPool != NULL) {
            XplOpenLocalSemaphore(Imap.server.mainSemaphore, 0);
            XplOpenLocalSemaphore(Imap.server.shutdownSemaphore, 1);
        } else {
            MemoryManagerClose(MSGSRV_AGENT_IMAP);

            XplConsolePrintf("IMAPD: Unable to create command buffer pool; shutting down.\r\n");
            return(-1);
        }
    } else {
        XplConsolePrintf("IMAPD: Unable to initialize memory manager; shutting down.\r\n");
        return(-1);
    }

    InitializeImapGlobals();

    ConnStartup(IMAP_CONNECTION_TIMEOUT);

    MsgInit();
    MsgAuthInit();
    NMAPInitialize();

    LogOpen("bongoimap");

    ReadConfiguration();
    CONN_TRACE_INIT(XPL_DEFAULT_WORK_DIR, "imap");
    CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    if (ServerSocketInit() < 0) {
        XplConsolePrintf("bongoimap: Imap.exiting\n");
        return -1;
    }

    if (!ServerSocketSSLInit()) {
        Imap.server.ssl.config.certificate.file = MsgGetFile(MSGAPI_FILE_PUBKEY, NULL, 0);
        Imap.server.ssl.config.key.file = MsgGetFile(MSGAPI_FILE_PRIVKEY, NULL, 0);
            
        Imap.server.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
        
        Imap.server.ssl.context = ConnSSLContextAlloc(&(Imap.server.ssl.config));
        if (Imap.server.ssl.context) {
            XplBeginCountedThread(&id, IMAPSSLServer, IMAP_STACK_SIZE, NULL, ccode, Imap.server.active);
        }
    }

    /* Done binding to ports, drop privs permanently */
    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongoimap: Could not drop to unprivileged user '%s', exiting\n", MsgGetUnprivilegedUser());
        return 1;
    }

    /* start the busy thread */
    XplBeginCountedThread(&id, BusyThread, IMAP_STACK_SIZE, NULL, ccode, Imap.server.active);

    XplStartMainThread(PRODUCT_SHORT_NAME, &id, IMAPServer, 8192, NULL, ccode);

    XplUnloadApp(XplGetThreadID());
    return(0);
}
